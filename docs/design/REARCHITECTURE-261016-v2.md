# shark-socket 重构方案 v2: 传输内核与运行时解耦

## meta-info

version: 2.4
date: 2026-10-16
status: draft
scope: 完整重架构 (不保持向后兼容)
supersedes: REARCHITECTURE-261016.md (v1, version 0.1)
absorbs: WORKER-ELASTICITY-261016.md (L2 层弹性伸缩, 已并入第 11 节)
toolchain_verified: go 1.26.9 (windows/amd64) + cloud linux 120.76.44.233
code_facts_verified_at: commit 93391829d, go.mod go 1.26.9
verified_files:
  - internal/core/session.go
  - internal/core/plugin.go
  - internal/core/server.go
  - internal/core/message.go
  - internal/core/types.go
  - internal/core/errors.go
  - internal/core/observability.go
  - internal/runtime/gateway.go
  - internal/runtime/session_manager.go
  - internal/runtime/plugin_chain.go
  - internal/runtime/session_metrics.go
  - internal/transport/tcp/session.go
  - internal/transport/tcp/server.go
  - internal/transport/tcp/framer.go
  - internal/transport/tcp/client.go
  - internal/transport/tcp/options.go
  - internal/transport/udp/session.go
  - internal/transport/udp/server.go
  - internal/transport/http/session.go
  - internal/transport/http/server.go
  - internal/transport/websocket/session.go
  - internal/transport/quic/session.go
  - internal/transport/coap/session.go
  - internal/transport/grpcweb/session.go
  - internal/transport/grpcweb/websocket_session.go
  - internal/transport/shared/worker_pool.go
  - internal/transport/shared/handler.go
  - internal/transport/shared/acceptor.go
  - internal/plugin/heartbeat.go
  - internal/plugin/autoban.go
  - internal/plugin/ratelimit.go
  - internal/plugin/cluster.go
  - internal/plugin/persistence.go
  - internal/infra/observability/prometheus.go
  - internal/infra/observability/metrics.go
  - internal/infra/store/store.go
  - api/api.go
reference_files:
  - smart-socket/aio-core/buffer/BufferPage.java
  - smart-socket/aio-core/buffer/VirtualBuffer.java
  - smart-socket/aio-core/transport/WriteBufferImpl.java
  - smart-socket/aio-core/transport/TcpAioSession.java
  - smart-socket/aio-core/enhance/EnhanceAsynchronousChannelGroup.java

---

## 0. 对 v1 的修订摘要

本节先说结论: v1 的病灶诊断 (第 1 节) 大体成立, 但**核心设计决策 (第 2,3,4,5 节) 有三处根本性错误**, 会导致重写而非重构。v2 推翻其中两处, 修正一处, 并补上 v1 漏掉的 6 个设计问题。二次审查 (第 10 节) 另追加 6 项结论 (H1-H6), 其中两项纠正了前提。

### 0.1 根本性错误 (推翻)

| 编号 | v1 的结论 | 代码事实 | v2 的结论 |
|---|---|---|---|
| E1 | 引入 5 方法的 `core.Conn` 作为传输层唯一契约, 7 个 transport 统一实现 | `udp/session.go:87` 与 `coap/session.go:87` 的 Send 是 `conn.WriteToUDP(payload, remote)`, 无连接级 `Write([]byte)` 语义; `http/server.go:171` 在 session 创建前已 `io.ReadAll(r.Body)`, session 无 Read 路径 | **推翻 `core.Conn`**。数据报类与请求响应类传输无法实现流式 Conn, 最多 3/7 适用。改为无 IO 方法的 `kernel.ConnState` 内嵌基座 |
| E2 | `kernel.ConnBase` 内嵌 `core.Conn` | v1 2.2 的 `Conn.Close() error` 与 `Session.Close(context.Context) error` 同名; 4 又在 3.4 用第二个文件 `conn_base.go` 定义同一个 `ConnBase` | **推翻内嵌方案**, 改用未导出具名字段 `raw core.Conn`。见 0.2 的遮蔽分析 |
| E3 | 零拷贝读 (帧视图指向读缓冲) 与零拷贝写 (帧含头直接回写) 是两个独立优化 | `tcp/server.go:212` 的 `pool.Submit(sess, proto, payload)` 把 payload 交给 worker goroutine, 读循环立即继续; 读缓冲被复用即**每条消息必然 use-after-free** | **合并为单一所有权转移机制** `core.Packet` + 引用计数 Chunk, 异步边界就是 Release 点。读写两侧共享同一块内存, 不能分别设计 |

### 0.2 修正

| 编号 | v1 的说法 | 实际情况 | v2 |
|---|---|---|---|
| F1 | 9 待决策项 1: Frame 生命周期 "回调内有效 + 提供 Clone()" | 若真的回调内有效, 则 `WorkerPool.Submit` 必须同步 Clone, 零拷贝收益归零 | **不需要 Clone, 不改 Plugin 接口**。5 个内置插件全部原样 `return data, nil`, 已天然正确。见 3.5 |
| F2 | 4.1 `Attrs` 替代 `sync.Map` | `Attrs.Slot(owner, name) *AttrSlot` 仍需每 session 一个 map, 只是换了 key 表示法; 且 `map[attrKey]any` + RWMutex 在"1 次写 N 次读"模式下并不优于无锁读路径的 `sync.Map` | **不做 `Attrs`**, 不新增该包。`sync.Map` 保留, 仅删除零调用方的 `DelMeta`。见 5.4 |
| F3 | 5.3 分片工作池, 消除 channel 跨核争用 | `TaskQueueSize` 选项语义被放大 32 倍, `FullPolicy` 与 `Stop` 的排空协议需复制 32 份; 且全仓仅 1 处 Submit 调用点, 其提交已被每连接读 goroutine 串行化 | **不分片工作池**。见 5.3 |
| F4 | 7 基线 "LineFramerRoundTrip 223.6 ns/op" | 1.2 表中是 2394 ns/op, 3.5 又说降到约 100 ns/op。同一基准三个数, 223.6 相差 10 倍 | 统一以 `logs/2026-06-25T17-10-25_benchmark.log` 为唯一锚点, 见 7.1 |
| F5 | 1.1 表 "7 个 transport, 101 个方法" | 实际有 **8 个** session 实现: `grpcweb/` 下有 `session.go` 与 `websocket_session.go` 两个 | 修正为 8 个 |
| F6 | 5.1 注册表 "分片后 Snapshot/Range/Broadcast 语义不变" | `Snapshot` 确实本来无序; 但 `Register` 的 `int64(len(m.sessions)) >= m.max` 在单锁下是**精确**的, 分片后只能读 `count` 原子量, 上限变成**近似** | 语义确实基本不变, 但必须显式文档化 "max 会话数变为近似, 最大超出 #shards"。见 5.1 |

### 0.3 补上的遗漏

| 编号 | v1 未覆盖 | 风险 | v2 对策 |
|---|---|---|---|
| G1 | **client 侧完全没有迁移步骤**。`tcp/client.go:123,138` 直接使用 `Framer.WriteFrame/ReadFrame` | 替换 `Framer` 为 `Decoder` 后 client 编译失败, 且**全部 45 个 Benchmark 依赖 `tcp.NewClient`** | 新增 M5 阶段, 见 6 |
| G2 | HTTP/gRPC-Web 是**每请求一个 session** (`http/server.go:176-178` 每次 `NextID()` + `Register`) | `sessions_active` 与注册表承受的是**请求率** churn 而非连接率, 这才是指标热点的真正成因; v1 1.4 归因为"连接 churn", 对 HTTP 错好几个数量级 | 5.2 明确区分两类 churn |
| G3 | `NewConnBase` 硬编码 `context.WithCancel(context.Background())` | HTTP 的 ctx 必须派生自 `r.Context()` (`http/session.go:28`), 否则**请求级取消传播被静默打断** | `ConnState` 提供带父 ctx 的构造函数变体, 见 2.3 |
| G4 | `WriteFrame` 分两次 `Write` 被列为病灶, 但 3.4 的"预留头"方案**仍然每帧分配 64KB** | v1 声称 63KB 场景降到约 65536 B/op, 实际是 1 次分配而非 0 次。63KB 场景真正的大头是 `tcp/session.go:90` 的 `append([]byte(nil), payload...)` (每消息一次全量复制), v1 完全没有针对它 | 由 `Packet` 所有权转移一并消除, 见 3.6。目标改为 B/op 趋近 0 |
| G5 | `Message.Meta map[string]string` 全仓**无任何生产者, 也无任何读取者**; `DelMeta` 除接口与 8 处实现外**零调用方** | 死代码, 且 `Meta` 是每消息 map, 一旦有人用就是每消息分配 | 两者直接删除 |
| G6 | `autoban.go:130-132` 与 `ratelimit.go:22-24` 的注释断言 "OnMessage 调用由传输层读循环串行化, 缓存无需加锁" | **该断言对 TCP 不成立**: `pool.Submit` 把同一 session 交给任意多个 worker goroutine 并发调用。当前因 `sync.Map` 而侥幸无 race, 但若按 v1 4.1 换成非线程安全存储就会变成真 bug | 修正注释; 若保留 `sync.Map` (F2) 则注释改为"依赖 Session 元数据存储的并发安全保证" |

### 0.4 二次审查追加 (主理人补充三项事实后的结论)

| 编号 | 议题 | 结论 | 详见 |
|---|---|---|---|
| H1 | 「HTTP/WS 是否应排除在 ConnBase 与零拷贝 Decoder 之外」 | **前提需纠正: WebSocket 是长连接** (有 Upgrade 后真 conn、readLoop、pingLoop、写锁), 不应排除。HTTP 排除第2 层但**保留 ConnState** | 4.1, 10.1 |
| H2 | 「是否拆分 LongLivedSession / RequestScopedSession」 | **不拆。** 拆分迫使 `Plugin`/`SessionManager`/`Handler` 三处签名变更或泛型化, 且所有用户插件要改; 而收益 Go 已用隐式接口免费提供。改为新增**可选**的 `SessionContext` 窄接口, 零签名变更 | 10.1 |
| H3 | 「是否引入 `core.CodecIO` client 契约」 | **不引入。** client 与 server 面对同一分帧问题, 复用同一 `Decoder`。但 client **必须**在 M5 同步迁移, 否则 45 个 Benchmark 全废 | 6.1 |
| H4 | 「零分配后是否需要 GC 调参」 | **前提反转。** 真正问题是 v1 的"每连接预留 MaxFrame (默认 1MB)"导致 1 万连接 = 10GB **常驻**内存。GC 对常驻对象无效, 调参是方向性错误。解药是 `WithReadChunkSize` (默认 64KB) | 4.2, 10.3 |
| H5 | 「3 个测试文件要重写」 | **核实后只有 1 个需要重写。** `stress_test.go` 与 `cross_protocol_test.go` 只在 handler 闭包中**消费** `core.Session`, 不实现它, 零改动 | 6.2 |
| H6 | 「M1 与 M2 是否合并提交」 | **明确结论: 不合并, 且 v1 的 M1 不应存在。** v1 的框定("分提交利于 review, 合提交利于 CI 绿灯")默认了"改契约必然中间态构建失败", 而增量过渡可让两者同时成立 | 7.2 |

**H1 与 H4 是本轮最重要的两条**: 前者若被采纳会白白丢掉 WebSocket 的零拷贝机会,
后者则揭示了 v1 与 v2 初稿共同遗漏的一类风险 -- 零拷贝把"瞬时分配"变成"常驻内存"。

### 0.5 三次审查追加 (L2 层: WorkerPool 弹性伸缩)

在 v2 5.3「工作池不分片」之后, 追加一条独立的运行时增强路径 L2。
触发原因是两组实测 (确定性测试 + 云端双进程压测), 而非推测:

| 编号 | 议题 | 结论 | 详见 |
|---|---|---|---|
| L2-1 | 「单池结构对弹性计算有什么限制」 | **限制不在单池的 channel 结构, 而在 worker 数固定且运行期不可调。** 实测丢弃率的决定因素是 `handler 耗时 / worker 数`,不是队列深度: 队列从 512 扩到 8192, 丢弃率反从 52.6% 升到 85.4% | 11.1, 11.3 |
| L2-2 | 「突发连接暴涨能否满足」 | **不能。** 无延迟下 1000 连接丢弃率 63.4%; 加 1ms handler 延迟后**单连接即丢弃 92.9%**,与连接数无关 | 11.1.2 |
| L2-3 | 「为什么现在不能伸缩」 | 三处硬约束: `Stop` 是一次性终态不可重入、worker 无身份标识、`WorkerPool` 无任何可观测接口 (仅 5 个方法) | 11.2 |
| L2-4 | 「怎么设计」 | `workerRef` 身份 + 可退休 `run()` + 队列水位判据 + Submit 内取模 1024 次触发 + 收缩需连续 3 次。判据只用队列水位 (领先指标), 不用 handler 耗时 (热路径开销) 或错误率 (结果非原因) | 11.3 |
| L2-5 | 「会不会破坏现有语义」 | 六条语义逐条保持; 其中 `PolicyBlock` 场景下 `maybeScale` **必须在 `submitMu.RLock` 之外**,否则延长 Submit 持锁放大阻塞 | 11.5 |
| L2-6 | 「能否与 M0-M9 并行」 | **能。** 只依赖 `core.Handler`/`Session`/`Message` 既有契约, L2-a 到 L2-d 纯新增 | 11.8 |

**L2 明确的边界**: 解决「整体算力不足」, **不解决**「单个 session 抢占全部算力」。
后者需按 session 分队列, 属分片方案,v2 5.3 已否决。

### 0.6 四次审查追加 (Go 语言与工具链特性复核, Go 1.26.5 实证)

把v2 的每条决策拿回 Go 1.26.5 的实际能力里重新对账, 结论见 12.8 汇总表。
三条必须知道的:

| 编号 | 性质 | 结论 |
|---|---|---|
| G-3 | **修订 (正确性)** | L2 让 `addWorker` 并发化, **激活**了 `wg.Add(1)` / `go` 之间的竞态窗口 -- 现状之所以安全只因 `Add` 仅在 `Start` 中发生。必须改用 `sync.WaitGroup.Go`。这是本轮唯一的严重级发现 |
| G-1 | **前提修正** | Go 1.26 起 **Green Tea GC 默认启用** (GC CPU 降 10-40%, RSS 升 8-15%)。10.3 "库不调GC 参数" 结论不变但理由更硬; **10.4 的内存断言必须在 Green Tea 下重采基线**。同时 Chunk slab 的"定尺寸密集"形态恰是 Green Tea 的理想负载, 两者叠加 |
| G-2 | **前提修正** | 容器感知 GOMAXPROCS 默认启用, 但**手动设置会关闭它**。库代码新增硬约束: 不得调用 `runtime.GOMAXPROCS`。且 11.1.2 压测基线需先确认云机 `GOMAXPROCS` 实际值 |

另有 G-4 (`GOEXPERIMENT=goroutineleakprofile` 可自动检出 11.6 的既有泄漏, 建议进 CI)
与 G-5 (`synctest` 让伸缩测试摆脱真实时间依赖, 但与 `-race` 兼容性待云端补测)。

### 0.7 待实测项收口 (12.9, 同日云端实测)

12.8 遗留的三项阻塞已全部收口, L2 可以落地:

| 项 | 结论 |
|---|---|
| cloud1 `GOMAXPROCS` | **实测 2**, 与 `nproc` 一致且 env 未设置 -> **11.1.2 数据有效, 无需重采** |
| `synctest` x `-race` | **完全兼容**, `-race -count=3` 全绿 ->伸缩测试统一进 bubble, **不分轮** |
| Green Tea 默认启用 | **权威确认** (GOROOT build tag 二分 + 版本串后缀机制) -> 10.3/10.4/8 三处修订成立 |

**顺带修正一处硬件事实**: cloud1 不是"2 核"而是 **1 物理核 + 2 超线程**
(`lscpu`: Core/Socket=1, Thread/Core=2)。数据不失效, 但 11.9 的
"4000 msg/s 天花板"应视为**乐观上界**。已回填 11.1.2 与 11.9。

**多核验证条件已具备**: cloud2 `47.120.44.128` (8 vCPU / Go 1.26.5 / 15GB / gcc)
是最佳对照机 -- Go 版本与本地基线一致。cloud3 的 1.26.1 略旧, 不建议用于对照。

**剩余唯一未测项**: Green Tea 下的 `BenchmarkTCPEcho_10kIdleConnections` 基线,
依赖 M0 先实现该基准。

### 0.8 五次审查追加 (8 核复测推翻 L2, 最重要的一次自我修正)

12收口后, 用cloud2 (8 vCPU) 做多核复测, **结果是推翻 L2 自己的立论**。
详见 13。

**worker 1-> 16 对照矩阵 (handler-delay=1ms, pipeline)**: drop rate 与吞吐
**均在噪声范围内波动**, 如1000 连接时 w=1/4/16 分别为 77.9% / 81.8% / 75.5%。
若"丢弃率的决定因素是 handler 耗时 / worker 数"成立, w=16 相对 w=1 应有
约 16 倍提升。**实测没有。**

**根因是压测方法, 不是被测代码**: pipeline 客户端与服务端同机, 客户端全速 Write
抢走 CPU (单连接 sent=1,024,513 而 recv=955)。此前两轮压测测的是
"客户端能灌多快", 被误读为"worker 够不够"。新增限速客户端
(`scripts/rateclient/main.go`) 佐证: 客户端不抢核时, w=1 到 w=16 的
`queue_full` **全为 0**。

**因此 0.6 的 G-1 到 G-10 全部成立, 但 11 的 L2 定量立论不成立。**
区分两类命题:

| 命题 | 性质 | 状态 |
|---|---|---|
| "worker 数固定且运行期不可调" | 代码事实 | **成立** |
| "worker 数是丢弃率的决定因素" | 性能推论 | **已推翻** |

**决策: L2 暂缓, 不进入实施。** 先做 M0, 并把"客户端与服务端分离"作为
合格压测的硬性前提固化进 `scripts/`, 再谈 L2 的收益。
M0-M9 主序列不受影响。

> **[注意] 上面的"暂缓"已被 14 撤销。** 分离式压测 (客户端限速 + 服务端独占 8 核)
> 实测 worker 1->16 吞吐提升 **11.96 倍**, 近线性。
> **13 的"worker 数无关"是归因错误**: 同机双进程下客户端抢走 CPU,
> 低吞吐反映的是客户端瓶颈, 不是 worker 数无关。
> **L2 立论成立, 恢复实施。** 详见 14.2-14.3。

---

## 1. 核实后的问题诊断

以下每条均已在代码中定位, 行号为核实时的实际行号。

### 1.1 会话实现: 13 方法, 8 处实现, 101 个方法体

`core/session.go:10-24` 定义 13 个方法。逐文件核实:

| 实现文件 | 行数 | 与 tcp 版逐字重复的方法 |
|---|---|---|
| transport/tcp/session.go | 156 | - |
| transport/udp/session.go | 103 | ID, Protocol, CreatedAt, LastActiveAt, State, Context, SetMeta, GetMeta, DelMeta, touch |
| transport/http/session.go | 102 | ID, Protocol, CreatedAt, LastActiveAt, State, Context, SetMeta, GetMeta, DelMeta |
| transport/websocket/session.go | 93 | ID, Protocol, CreatedAt, LastActiveAt, State, Context, SetMeta, GetMeta, DelMeta, touch |
| transport/coap/session.go | 103 | 同 udp |
| transport/quic/session.go | 132 | 同 tcp (多 writeLoop) |
| transport/grpcweb/session.go | 91 | 同 http (多 SendTrailers) |
| transport/grpcweb/websocket_session.go | 100 | 同 websocket |
| 合计 | 780 | - |

重复的部分是**纯粹的 per-connection 状态**, 不含任何 IO:
`ID / Protocol / CreatedAt / LastActiveAt / State / Context / SetMeta / GetMeta / DelMeta` 加两个
非接口方法 `touch()` 与 `closeOnce` 的标准用法。真正的差异只有 4 个:
`RemoteAddr / LocalAddr / Send / Close`。v1 把这 4 个差异错认为 "IO", 因而引入了不该有的 `Conn`。

### 1.2 分配热点 (核实)

| 位置 | 代码 | 每消息代价 |
|---|---|---|
| transport/tcp/framer.go:45 | `payload := make([]byte, int(raw))` | 1 次全帧分配 |
| transport/tcp/session.go:90 | `copied := append([]byte(nil), payload...)` | 1 次全帧复制 + 分配 |
| transport/tcp/framer.go:57,60 | `w.Write(header)` + `w.Write(payload)` | 2 次 syscall |
| transport/tcp/framer.go:83-94 | LineFramer 逐字节 `io.ReadFull` + `append` | 每字节 1 次接口调用 + 扩容复制 |
| transport/quic/session.go:67 | `copied := append([]byte(nil), payload...)` | 1 次全帧复制 + 分配 |
| transport/udp/server.go:304 | `payload := append([]byte(nil), buf[:n]...)` | 1 次复制 |
| transport/udp/server.go:270 | `payload := append([]byte(nil), buf[:n]...)` | 1 次复制 |
| transport/http/server.go:171 | `io.ReadAll(r.Body)` | 按 body 大小增长 |

`echoHandler` 是 `sess.Send(msg.Payload)` (`tests/benchmark/protocol_bench_test.go:425`)。
因此 TCP 每条消息的帧字节被复制 **2 次**: 一次进 worker 队列 (当前靠 `ReadFrame` 的
`make` 侥幸拥有独立内存), 一次在 `Send` 里。63KB 场景 196889 B/op 约等于 3 x 64KB, 与此吻合。

### 1.3 跨 goroutine 边界是主要正确性约束 (核实, v1 未正视)

`transport/tcp/server.go:202-216`:

```
sess.readLoop(func(payload []byte) {
    payload, err := s.rt.Plugins().OnMessage(sess, payload)   // 203
    ...
    s.pool.Submit(sess, core.ProtocolTCP, payload)             // 212
})
```

`shared/worker_pool.go:93-142` 的 `Submit` 把 `PoolTask{Session, Protocol, Payload}` 送入
channel, 由 `run()` (`worker_pool.go:185-204`) 的 worker goroutine 消费并调用用户 handler。
读循环在 `Submit` 返回后**立即**继续 `ReadFrame`。

结论: **在 TCP 上, payload 每条消息都跨 goroutine 边界。** 一旦读缓冲被复用,
这是确定性数据损坏, 不是概率性风险。v1 8 把 use-after-free 列为"风险, 缓解: 文档化生命周期",
9 列为"待决策项", 属于对问题量级的严重低估。

### 1.4 指标与注册表 (核实)

`infra/observability/prometheus.go:42-51` 的 `IncCounter`: `metricKey` 字符串拼接 (`metrics.go:76-82`,
含 `strings.Join`) + 全局 `Mutex` + `append([]string(nil), labels...)` + 写回。

`runtime/session_metrics.go:24,37`: 每次 `Register`/`Unregister` 各一次 `IncCounter` + 一次 `SetGauge`,
即**每连接注册注销各 2 次带分配的加锁操作**。

v1 1.4 归因为"高连接 churn"。对 TCP/QUIC/WS 成立, 对 HTTP/gRPC-Web 不成立:
`http/server.go:176-179` 每个请求都 `NextID()` + `Register`, `grpcweb/server.go:205` 同理。
这两类传输的 churn 是**请求率**级别, 通常比连接建立率高 1-2 个数量级。
这改变了优化优先级: 指标原子化不是"高连接场景的锦上添花", 而是 HTTP 路径上的必需项。

### 1.5 注册表 (核实)

`runtime/session_manager.go:39-54`: 单 `sync.RWMutex` + `map[uint64]core.Session`,
容量检查 `int64(len(m.sessions)) >= m.max` **在锁内且精确**。已有 `count atomic.Int64` 与 `nextID atomic.Uint64`。

`Snapshot` (`:78-86`) 本来就返回无序切片 (Go map 迭代无序)。
`Range` (`:91-99`) 在 `RLock` 内回调, 注释已明确禁止回调内调用 Register/Unregister。
`CloseAll` (`:118-135`) 是**基于快照的循环**: 快照为空即返回, 并非 v1 5.1 所称的
"直到 map 空"的单 map 语义。因此分片后 `CloseAll` 的终止条件**依然成立**。

---

## 2. 契约层设计

### 2.1 为什么不引入 core.Conn

v1 2.2 的 `core.Conn` 要求传输层实现 `Read`/`Write`/`Close`/`RemoteAddr`/`LocalAddr`。逐一核实 7 类传输:

| 传输 | 能否实现 Read | 能否实现 Write([]byte) | 障碍 |
|---|---|---|---|
| tcp | 是 | 是 | - |
| quic | 是 | 是 | 每条消息 `OpenStreamSync` 开新流 (`quic/session.go:103`), 写语义是"开流", 非"写字节" |
| websocket | 是 | 是 | 需 `WriteMessage` 帧封装, 非裸写 |
| coap (DTLS) | 是 | 是 | - |
| coap (plain UDP) | **否** | **否** | `coap/session.go:87` 是 `WriteToUDP(payload, remote)`, 无连接级写; 读是共享 socket 的 `ReadFromUDP`, 不属于任何 session |
| udp | **否** | **否** | `udp/session.go:87` 同上 |
| http | **否** | **否** | body 在 session 创建前已被 `io.ReadAll` 读完 (`http/server.go:171`); 写是 `ResponseWriter.Write` |
| grpcweb | **否** | **否** | 同 http, 另有 gRPC-Web 5 字节帧封装 |

4/8 实现无法提供有意义的 `Conn`。强行实现只能写出"永远返回 `ErrUnsupportedFeature` 的假方法",
这正是接口未按使用者定义分割的反面教材。**v1 2.3 声称的"从 156 行降到约 45 行"只在 TCP 上成立**,
而 TCP 恰恰是不需要抽象的那一个 (它本来就持有 `net.Conn`)。

### 2.2 kernel.ConnState: 无 IO 方法的内嵌基座

真正重复的是状态, 不是 IO。所以基座不应该含 IO 方法。

```
internal/kernel/
  conn_state.goConnState 内嵌 struct: 8 个方法, 0 个 IO 方法
  attrs.go            (不做, 见 F2)
  packet.go           Packet 所有权载体 (core.Packet, 见 3.2)
  chunk.go            引用计数读缓冲块 + 分级free list
  decoder.go          Decoder 契约 (替代 Framer)
  decode_lenprefix.go
  decode_line.go
  decode_fixed.go
  decode_raw.go
  readloop.go         共享读循环 (仅流式传输)
  writequeue.go       共享写队列 (仅带写队列的传输)
  stream.go           StreamConn 可选窄接口
```

```go
// kernel/conn_state.go

// ConnState 是所有传输会话共享的状态基座。它刻意不包含任何 IO 方法:
// 各传输的读写语义差异过大 (见文档 2.1 节), 强行统一只会得到假实现。
// 传输层通过内嵌 ConnState 获得 8 个方法, 只需自行实现 4 个。
type ConnState struct {
    id      uint64
    proto   core.Protocol
    remote  net.Addr
    local   net.Addr
    created time.Time
    active  atomic.Int64      // UnixNano
    state   atomic.Uint32     // core.SessionState
    meta    sync.Map          // 保留: 见 F2
    ctx     context.Context
    cancel  context.CancelFunc
    closeOne sync.Once
}

// NewConnState 用后台 ctx 构造, 适用于长连接传输 (tcp/udp/quic/ws/coap/grpcweb-ws)。
func NewConnState(id uint64, p core.Protocol, remote, local net.Addr) *ConnState

// NewConnStateWithParent 从父 ctx 派生, 供请求响应型传输使用。
// HTTP/gRPC-Web 必须使用本构造: 它的 session 生命周期从属于单个请求,
// 若用 context.Background() 派生, net/http 的请求级取消将无法传播到 Session.Context(),
// 从而使依赖 ctx 的插件与 handler 在客户端断开后仍继续运行。
func NewConnStateWithParent(parent context.Context, id uint64, p core.Protocol, remote, local net.Addr) *ConnState

func (s *ConnState) ID() uint64                   { return s.id }
func (s *ConnState) Protocol() core.Protocol      { return s.proto }
func (s *ConnState) RemoteAddr() net.Addr         { return s.remote }
func (s *ConnState) LocalAddr() net.Addr          { return s.local }
func (s *ConnState) CreatedAt() time.Time         { return s.created }
func (s *ConnState) LastActiveAt() time.Time      { return time.Unix(0, s.active.Load()) }
func (s *ConnState) State() core.SessionState     { return core.SessionState(s.state.Load()) }
func (s *ConnState) Context() context.Context     { return s.ctx }
func (s *ConnState) SetMeta(k string, v any)      { s.meta.Store(k, v) }
func (s *ConnState) GetMeta(k string) (any, bool) { return s.meta.Load(k) }

// Touch 刷新活跃时间戳。由各传输的读循环在收到数据后调用。
func (s *ConnState) Touch() { s.active.Store(time.Now().UnixNano()) }

// Shutdown 是 Close 的内部实现, 供各传输的 Close(ctx) 复用。
// 语义: 幂等, 只取消 ctx 并置状态, 不碰底层资源 (由传输自己关)。
func (s *ConnState) Shutdown() { s.closeOne.Do(func() { s.state.Store(uint32(core.StateClosed)); s.cancel() }) }
```

### 2.3 ConnBase 内嵌冲突的正确处理 (推翻 E2)

v1 4 让 `ConnBase` 内嵌 `core.Conn`。这不是编译错误, 是更隐蔽的**契约静默丢失**:

`*ConnBase` 上, `Close()` 由内嵌接口在**深度 1** 提升, `Close(context.Context) error` 若在
`*ConnBase` 上显式声明则位于**深度 0**。Go 的方法集规则是"最浅深度者胜出",
因此深度 1 的 `Close() error` 被**遮蔽**, `*ConnBase` **不再满足 `core.Conn`**。
后果: 任何 `var _ core.Conn = (*ConnBase)(nil)` 断言失败, 而这类断言通常出现在
框架自检代码里, 会在很久之后才暴露。

正确做法是未导出具名字段, 彻底不参与方法提升:

```go
type tcpSession struct {
    kernel.ConnState                    // 深度 0, 提供 8 个方法
    raw    net.Conn                      // 未导出, 不嵌入, 无提升, 无遮蔽
    ...
}
```

同时纠正 v1 的文件分裂: 3.4 用 `kernel/conn_base.go` 定义 `ConnBase`, 4 又用
`kernel/conn.go` 定义同名类型。v2 只保留 `kernel/conn_state.go` 一个定义点。

### 2.4 core.Session v2 契约

```go
// core/session.go
type Session interface {
    ID() uint64
    Protocol() Protocol
    RemoteAddr() net.Addr
    LocalAddr() net.Addr
    State() SessionState
    CreatedAt() time.Time
    LastActiveAt() time.Time
    Context() context.Context

    SetMeta(string, any)
    GetMeta(string) (any, bool)

    // Send 复制 payload 并写入。适用于调用方持有独立内存的场景,
    // 以及请求响应型传输 (其 payload 来自 io.ReadAll, 本就是独立内存)。
    Send([]byte) error

    // SendPacket 转移 Packet 所有权: 写队列在完成写出 (或因会话关闭被丢弃) 后
    // 释放 Packet 及其底层缓冲块。调用方在调用后不得再触碰 pkt.Data。
    // 这是零分配回显路径的唯一入口。
    SendPacket(Packet) error

    Close(context.Context) error
}
```

变更点:

| 变更 | 理由 |
|---|---|
| 删除 `DelMeta(string)` | 全仓零调用方 (除接口与 8 处实现), 见 G5 |
| 新增 `SendPacket(Packet)` | 消除 `Send` 的全帧复制, 见 3.6 |
| 保留 `SetMeta`/`GetMeta` 的 `string` 键 | 见 F2, `Attrs` 无收益 |
| `RemoteAddr`/`LocalAddr` 留在接口 | 各传输确实不同 (net.Addr vs `stringAddr`), 见 `http/session.go:73-76` |
| 不引入 `Raw() any` | v1 2.2 用它兜住 HTTP Hijack 场景; 改为可选接口 `interface{ Raw() any }` 由调用方断言, 不污染主契约 |
| `Touch()` 不进接口 | 由传输内部读循环直接调用 `ConnState.Touch()`, 插件不应改活跃时间 |

**删除后传输层需自行实现的方法数: 4** (`RemoteAddr`, `LocalAddr`, `Send`, `Close`),
从 `SendPacket` 角度是 5。v1 声称的 5 是通过引入假 IO 达到的; v2 用 4 个真差异方法达到同一数字。

---

## 3. 零拷贝设计: core.Packet 与所有权转移

### 3.1 唯一的所有权模型

所有帧内存的唯一载体是 `Chunk`。`Chunk` 不可分割, 由引用计数管理:

```
+-------------------+
| Chunk            |  refcount int32   >= 1, 归一到 slab free list
|  buf []byte       |  固定 >= MaxFrame + MaxHeader, 一次分配, 循环使用
|  magic    uint32  |  用于校验野指针 (仅测试期开启)
+-------------------+
        ^
        | 1..N
   +----+
   | Packet |
   +--------+
   | Data []byte |  payload 视图, 无帧头
   | Raw  []byte |  含帧头的完整帧, 用于零拷贝回显
   | chunk *Chunk|
   +--------+
        ^
        | 1
   调用方持有引用
```

`Packet.Release()` 幂等: `atomic.AddInt32(&chunk.ref, -1)`, 归零则归还 slab。

### 3.2 Packet 定论 (回答问题 b)

**结论: 需要 Packet, 不需要 Clone(), 不改 Plugin 接口。**

`core.Packet` 放在 `core` 而非 `kernel`, 因为 `core.Message` 需要携带它
(否则 handler 拿不到可零拷贝回显的句柄), 而 `core` 不能反向依赖 `kernel`。

```go
// core/packet.go

// Packet 是一帧数据的拥有者句柄。Data 与 Raw 都指向底层 Chunk,
// 仅在持有引用期间有效。零拷贝路径上的任何一方若要把数据交给
// 另一个 goroutine 或另一个生命周期更长的容器, 必须先 Retain。
type Packet struct {
    Data  []byte   // payload, 不含帧头
    Raw   []byte   // 完整帧 (含帧头), 仅 Decoder 产出时非 nil
    chunk any      // *kernel.Chunk, 用 any 避免 core <-> kernel 循环依赖
}

// Retain 增加引用计数, 供需要跨生命周期持有数据的插件使用。
func (p Packet) Retain()

// Release 归还引用。幂等, 可重复调用, 可并发调用。
func (p Packet) Release()

// Clone 复制出一份独立拥有的 Packet。仅在数据必须活过 Chunk 复用时使用。
// 常规路径不需要它: Cluster 与 Persistence 已各自持有自己的副本。
func (p Packet) Clone() Packet
```

`Clone` 保留但**不作为契约要求**。v1 9 把它当成"零拷贝的代价", 实际上它是逃生舱:
5 个内置插件已经各自处理好了跨生命周期持有的问题 (见 3.5), 常规路径不需要它。

### 3.3 Decoder 契约

替代 v1 的 `Frame{Data, Raw}` + `Decode(buf) (frame, consumed, err)`, v2 用:

```go
// core/codec.go

// Decoder 把累积的字节流切分为帧。它必须是**无状态**的:
// 同一个 Decoder 值会被同一传输的所有连接共享 (当前 Framer 以值形式存于
// Options 并被复制进每个 session, 见 tcp/options.go:33 + tcp/server.go:169),
// 任何每连接状态都必须放在读循环的缓冲里, 放进 Decoder 会造成跨连接数据竞争。
type Decoder interface {
    // HeaderSize 返回帧头字节数。读循环用它预留写侧空间。
    HeaderSize() int
    // MaxFrame 返回单帧上限 (含帧头), 用于入参校验与缓冲容量决策。
    MaxFrame() int
    // Decode 尝试从 buf 切出一帧。
    // 返回 (Packet, consumed, nil)  成功, buf[:consumed] 已被消费
    // 返回 (zero, 0, core.ErrNeedMore)  数据不足, 调用方应继续 Read
    // 返回 (zero, 0, err)  协议错误, 调用方应关闭连接
    Decode(buf []byte, scratch Packet) (Packet, int, error)
    // EncodeInto 把 payload 编码为带帧头的完整帧, 写入 dst 并返回写入的切片。
    // dst 必须至少有 MaxHeader 字节的头部空间。
    EncodeInto(dst []byte, payload []byte) ([]byte, error)
    // MaxHeader 返回 EncodeInto 需要的最大头部空间。
    MaxHeader() int
}
```

`Decode` 增加 `scratch Packet` 参数: `ErrNeedMore` 时框架需要把**未消费的残留字节**
搬移到下一块 Chunk 的头部, `scratch` 就是这次搬运的落脚点。这是 v1 完全没有覆盖的
"窗口前移后如何续读"问题 (见 3.4 的 v1 缺陷 2)。

`HeaderSize` 与 `MaxHeader` 分离的原因: `LineFramer` 的头部是 0 (分隔符在后),
但 `EncodeInto` 需要 1 字节空间来放分隔符。v1 用单一 `HeaderSize` 无法表达。

4 个实现:

```
DecodeLengthPrefix  HeaderSize 4   MaxHeader 4   header = uint32 BE 长度
DecodeLine          HeaderSize 0   MaxHeader 1   分隔符在帧尾, EncodeInto 追加
DecodeFixed         HeaderSize 0   MaxHeader 0   无头, 定长
DecodeRaw           HeaderSize 0   MaxHeader 0   无头, 读到多少算多少 (上限 MaxFrame)
```

`DecodeLine` 是对 v1 3.5 的确认与修正: 单次 `bytes.IndexByte` 取代逐字节读,
但必须额外处理 "残留数据 + 新读数据跨块" 与 "分隔符在两块边界" 两种情况,
v1 3.5 的实现假设了单一连续缓冲, 无法在 Chunk 机制下工作。

### 3.4 读循环: 修正 v1 的四个缺陷

v1 3.3 的 `readLoop` 伪码有四个会导致错误或失效的问题:

| 编号 | v1 的代码 | 缺陷 |
|---|---|---|
| 1 | `l.data = l.data[consumed:]` | 切片头前移但**从不回收**, `cap(l.buf)` 单调收缩, 于是 `cap - len < MaxFrame` 每轮都成立, `grow()` 每帧都调用。v1 3.3 声称的"稳态下零分配, 缓冲只增不减"**不成立**, 实际退化为每帧一次分配 |
| 2 | `if cap(l.buf)-len(l.data) < MaxFrame { grow() }` | 每次预留整整一个 `MaxFrame` (默认 1MB)。1KB 小帧稳态下, 每连接一次性占 1MB, 1 万连接即 10GB。增长策略必须按"实际需要"而非"上限"决策 |
| 3 | `Read` 返回 `n==0, err==nil` | v1 2.2 声称 `Conn.Read` 允许此返回。阻塞式 `net.Conn.Read` 不会这样返回, 但 UDP 路径会。读循环若无显式处理会**忙等 spins 100% CPU** |
| 4 | 无跨块搬运逻辑 | 帧横跨两块 Chunk 时无处理 |

v2 的读循环骨架:

```
readLoop(state, decoder, raw io.Reader):
    pending  Chunk引用   // 当前累积窗口, 指向一个 Chunk
    off      int// pending 内的已消费偏移
    loop:
        // (1) 容量决策: 只在"剩余空间不足一个最大帧"时换块, 且新块一次分配
        if pending 可用连续空间 < decoder.MaxFrame():
            if off > 0:
                // 压缩: 把残留搬回块头部, 避免无界增长 (修正缺陷 1)
                copy(pending.buf[0:], pending.buf[off:])
                pending.len = 旧 len - off; off = 0
                if 可用空间 >= MaxFrame: goto readstep     // 压缩后够用, 不换块
            pending = slab.acquire(MaxFrame + decoder.MaxHeader())
        readstep:
        // (2) n == 0 且 err == nil 时让出调度 (修正缺陷 3)
        n, err = raw.Read(pending.buf[off+len : cap])
        if n == 0 && err == nil:
            runtime.Gosched(); continue
        if n > 0:
            pending.len += n
            touched()
        // (3) 尽可能多地切帧
        for:
            pkt, consumed, derr = decoder.Decode(pending.buf[off:off+len], scratch)
            if derr == ErrNeedMore: break
            if derr != nil: return derr
            off += consumed
            dispatch(pkt)
        if err != nil: return err
```

`dispatch` 的所有权处置是本设计的核心, 也是 v1 完全缺失的一半:

```
dispatch(pkt):
    if plugins 存在:
        payload, perr = plugins.OnMessage(sess, pkt.Data)   // 同步, 不改变所有权
        if perr != nil:
            pkt.Release()
            handle perr (ErrPluginDrop 则跳过, 否则关闭会话)
            return
    if pool != nil && pool.Enabled():
        // 唯一的所有权转移点: Submit 接管 pkt
        pool.SubmitPacket(sess, proto, pkt)                  // worker 回调后 Release
        return
    // 无池: 同步执行 handler, 之后释放
    defer pkt.Release()
    callHandler(sess, Message{SessionID, Protocol, Payload: payload, Pkt: pkt})
```

`pool.Submit` 提交后读循环**不 Release**, 因为 worker 持有引用。
`worker_pool.go:167-175` 的 `handle` 末尾增加 `t.Packet.Release()`。
注意 `handle` 有两个早退分支 (`t.Session == nil` / `handler == nil`),
Release 必须放在 `defer` 里而非函数末尾, 否则这两条路径泄漏引用计数。

### 3.5 插件链: 不改接口 (定论)

现有签名 `OnMessage(Session, []byte) ([]byte, error)` **保持不变**。核实 5 个内置实现:

```
plugin/autoban.go:110return data, nil        (丢弃时 return nil, ErrPluginDrop)
plugin/ratelimit.go:140                     return data, nil        (丢弃时 return nil, ErrPluginDrop)
plugin/cluster.go:102,109                  Payload: append([]byte(nil), data...) 后 return data, nil
plugin/persistence.go:106                   return data, nil
core/plugin.go:39 (BasePlugin)              return b, nil
```

全部满足同一条规则:

> **规则 (唯一需要文档化的一条): 插件返回的切片若与入参 `data` 别名同一块内存,
> 表示所有权仍属框架, 框架继续持有 Packet; 若返回的是新分配的切片,
> 表示插件自己解决了跨生命周期持有问题 (如 cluster.go:102 的显式复制),
> 框架照常在 dispatch 结束时 Release 入参。**

所以 v1 提出的 "插件返回入参 vs 返回新切片的所有权语义" 问题**在现有代码里已经是安全的**,
因为别名关系可判定, 而需要长期持有的插件 (cluster, persistence->Memory/Bolt store)
本来就做了复制 (`store/store.go:34` 的 `append([]byte(nil), value...)` 亦然)。

因此:
- 不需要 `Clone()` 作为契约要求
- 不需要改变 `Plugin` 接口
- 不需要 `PLUGIN.md` 补充所有权条款, 只需在 `core/plugin.go` 的 `OnMessage` 注释里写清上述规则
- 需要额外修正 G6: 两处关于"读循环串行化"的注释是错的 (TCP 有 worker pool 并发)

### 3.6 零拷贝写: 消除 Send 的全帧复制 (补G4)

v1 3.4 的方案是 "预留头 + 一次 Write", 结论 "63KB 降到约 65536 B/op"。
**这仍然是每帧一次 64KB 分配**, 只是从 3 次降到 1 次, 而 `tcp/session.go:90`
的 `append([]byte(nil), payload...)` 完全没被触及。真正的成本在那行。

v2 的做法: 写队列承载 `Packet` 而非 `[]byte`。

```
读循环 dispatch
   |
   +-- 插件链 (同步, 别名安全)
   |
   +-- pool.SubmitPacket(sess, pkt)         <- pkt 引用计数 = 1 (读循环持有)
   |        |
   |   worker: handler(sess, Message{Payload: pkt.Data, Pkt: pkt})
   |        |
   |   handler 若为 echo: sess.SendPacket(msg.Pkt)
   |        |                                <- pkt 引用计数 = 2 (写队列持有)
   |   writeLoop: raw.Write(pkt.Raw)  // 帧头已在 Chunk 里, 一次 syscall
   |        |
   |   writeQueue 出队后 pkt.Release()      <- 引用计数 = 1 (读循环仍持有)
   |
   +-- 读循环继续下一轮读; 换 Chunk 时前一枚 Release -> 引用计数 0 -> slab 归还
```

零分配回显路径上, 每条消息的帧字节**一次都不复制**。
`Message.Payload` 仍然填 `pkt.Data` (用户代码无感), 但 handler 走 `SendPacket`
时框架识别出 `msg.Pkt` 与入参同源, 跳过复制。

用户自造 payload 的场景 (`Send([]byte)`) 行为不变: 分配 `MaxHeader + len(payload)`
的单块, `EncodeInto` 写入, 复用同一写队列。这比 v1 好, 因为它只分配**一次**
(v1 的 `WriteFrame` 连分配都不做, 但也没有消除 `Send` 的复制)。

无法识别同源时 (用户手工 `Send(msg.Payload)` 而非 `SendPacket(msg.Pkt)`)
一律退回复制路径, 行为与今天一致。**兼容性由调用方式而非运行时探测保证**,
不引入"有时零拷贝有时不是"的不确定性。

### 3.7 写队列

```
kernel/write_queue.go

type WriteQueue struct {
    ch      chan core.Packet
    highWater float64
}
func (w *WriteQueue) Push(pkt core.Packet) error   // 高水位背压, 语义同 tcp/session.go:81-89
```

- channel 元素从 `[]byte` 变为 `core.Packet` (interface, 32 字节, 值传递)
- **不 close channel**: 沿用 `tcp/session.go:106-108` 的既有论证, Send 与 writeLoop
  都 select 在 ctx.Done() 上, 避免"向已关闭 channel 发送" panic
- writeLoop 取出后必须 `defer pkt.Release()`
- quic 的 writeLoop (`quic/session.go:99-130`) 每条消息 `OpenStreamSync` 开新流,
  保留该语义 (协议要求), 只把 channel 元素类型换成 `core.Packet`

高水位阈值计算保留 `tcp/session.go:82-85` 的 clamp (小于 1 时取 1, 使
`WithWriteQueue(1)` 仍能接受首写), 这是已修复的 bug, 重构不得回退。

---

## 4. kernel 与传输的分层

```
internal/
  core/契约 (core 不依赖 kernel)
    session.go        Session 接口 (12 方法, 见 2.4)
    packet.go         Packet 所有权句柄
    codec.go          Decoder 契约 + Codec/Handler 泛型业务层
    plugin.go         Plugin 不变, 仅补 OnMessage 所有权注释
    server.go         Runtime / PluginRunner / StagedServer 不变
    types.go errors.go observability.go message.go   不变 (message.go 删 Meta 字段)
  kernel/传输内核 (依赖 core)
    conn_state.go     ConnState 内嵌基座
    decoder.go        Decoder 契约实现族
    readloop.go       共享读循环 (流式传输)
    writequeue.go     共享写队列
    packet.go         Chunk slab + 引用计数
    stream.go         StreamConn 窄接口
  transport/
    tcp/ udp/ http/ websocket/ coap/ quic/ grpcweb/
      每个只保留: server.go + 会话类型 (内嵌 ConnState) + 分帧策略
      session.go 全部重写为 40-60 行的内嵌结构体
  runtime/            编排层
  plugin/             5 个内置插件, 仅注释修正
  infra/observability/  原子化指标
  protocol/lwm2m/     不变
```

各传输保留的实现差异:

| 传输 | ConnState | 自行实现 | kernel 流式组件 |
|---|---|---|---|
| tcp | 是 | RemoteAddr, LocalAddr, Send, SendPacket, Close | ReadLoop + Decoder + Chunk + WriteQueue |
| websocket | 是 | 同上 (WriteMessage) | ReadLoop + Decoder + Chunk + WriteQueue (gorilla 的 WriteMessage 非并发安全, 保留 writeMu) |
| quic | 是 | 同上 | ReadLoop (每流一 goroutine) + Chunk + WriteQueue |
| grpcweb (websocket 模式) | 是 | 同上 | ReadLoop + Decoder + Chunk + WriteQueue |
| udp (DTLS 模式) | 是 | 同上 | ReadLoop + Chunk (每连接一个 DTLS 流) |
| coap (DTLS 模式) | 是 | 同上 | 同 udp |
| http | 是 (**WithParent**) | 同上 (ResponseWriter) | **无** |
| grpcweb (http 模式) | 是 (**WithParent**) | 同上 (+ SendTrailers) | **无** |
| udp (plain) | 是 | 同上 (WriteToUDP) | **无** (共享 socket, 无连接级读循环) |
| coap (plain) | 是 | 同上 | **无** |

### 4.1 三层可选模型: 不是"两条路径", 而是"按需选用"

主理人提出的方案是"长连接内核 + 请求响应内核两条独立路径"。核实后, **这个二分法的前提不成立**,
正确的模型是三层, 且切分线不是"session 契约", 而是"选用哪些 kernel 服务"。

**前提纠正: WebSocket 是长连接, 不是请求响应型。** 主理人的补充说明把 ws 与 http 并列为
"request-response 传输", 但代码事实相反:

```
websocket/server.go:143  handleUpgrade(w, r)          <- 标准库签名, 但仅用于 Upgrade
websocket/server.go:148  upgrader.Upgrade(w, r, nil)  <- 产出真正的 net.Conn
websocket/server.go:163  newSession(id, conn, ...)    <- conn 是 *websocket.Conn
websocket/server.go:191  s.readLoop(sess)             <- 有 readLoop
websocket/server.go:236  pingLoop(sess)               <- 有心跳
websocket/session.go:69  s.conn.WriteMessage(...)    <- 真IO, 有 writeMu
```

WebSocket 有 Upgrade 后的真连接、有 readLoop、有心跳、有写锁, 与 TCP 同构。
把它划入"请求响应"类是错的。

真正请求响应型的是 **http** 与 **grpcweb 的 http 模式**。而 grpcweb 本身横跨两种形态:

```
grpcweb/server.go:150handle(w, r)              newSession(id, w, r, framed)        请求响应型
grpcweb/server.go:241  handleWebSocket(w, r)   newWebSocketSession(id, conn, ...)  长连接型
grpcweb/server.go:298  readWebSocketLoop(sess) + :332 pingLoop                     长连接型设施
```

所以同一传输内就同时存在两种形态, 按"传输"划分必然错位。按**能力**划分才对:

```
第 0 层  core.Session              单一契约, 全部 10 种形态实现 (窄接口, 见 10.2)
         |
第 1 层  kernel.ConnState          纯状态, 0 个 IO 方法, 全部 10 种形态内嵌 (无例外)
         |
第 2 层  kernel 流式组件           按需选用 (opt-in), 仅 6 种形态启用
           ReadLoop / Decoder / Chunk / Packet / WriteQueue
```

关键点: **HTTP 不被排除在 ConnState 之外。** ConnState 没有任何 IO 方法 (见 2.2),
所以 HTTP 完全可以内嵌它, 白拿 9 个方法 (ID/Protocol/State/CreatedAt/LastActiveAt/
Context/SetMeta/GetMeta + ID/Protocol)。HTTP 真正用不上的只是第 2 层。
把"排除 ConnState"与"排除零拷贝 Decoder"混为一谈, 会让 HTTP 继续重复那 9 个方法,
恰好丢掉本次重构最实在的那部分收益。

各形态的第 2 层选用表:

| 形态 | ReadLoop | Decoder | Chunk/Packet | WriteQueue |
|---|---|---|---|---|
| tcp, quic | 是 | 是 | 是 | 是 |
| websocket, grpcweb-ws | 是 | 是 | 是 | 是 |
| udp-dtls, coap-dtls | 是 | **否** (DTLS 已自带消息边界) | 是 | 否 (直写) |
| http, grpcweb-http | **否** | **否** | **否** | **否** |
| udp-plain, coap-plain | **否** | **否** | **否** | **否** |

后三行的收益来源不同:
- DTLS 行: 消除 `append([]byte(nil), buf[:n]...)` (`udp/server.go:270` 的复制), 但不分帧
- http 行: 只拿 ConnState 的 9 个方法, 行数 102 -> 约 40, **零性能收益**
- plain UDP 行: 只拿 ConnState, 且 session 由 `getOrCreateSession` (`udp/server.go:359`) 按远端地址复用

**HTTP/WS 的收益是否为零甚至为负? (回答主理人问题 3)**

- **http: 收益为零 (纯代码量收益, 无性能收益), 但不为负。** 前提是它只内嵌 ConnState。
  若按 v1 强行实现 `core.Conn` 五方法, 则确为负收益: 要为 `Read` 写永远返回错误的桩,
  为 `LocalAddr` 保留 `return nil` (`http/session.go:45` 现状), 代码量不降反升。
- **ws: 收益与 TCP 同等, 不为零。** 因为它是长连接 (见上)。把 ws 误分类为请求响应型,
  会白白丢掉它的零拷贝改造机会, 这是本次补充说明若被采纳会引入的实质损失。

### 4.2 Chunk 尺寸必须与 MaxFrame 解耦 (新增, 修正 v2 3.4)

v2 3.4 的读循环在 "剩余空间 < MaxFrame" 时换块, 而 `tcp/options.go:43` 的
`MaxFrameBytes` 默认是 **1MB**。若据此每连接预留 1MB, 则:

```
10000 并发连接 x 1MB = 10 GB 常驻堆
```

这是把"消除分配"换成了"放大常驻内存", 且因为是常驻对象, GC 完全无法回收 (见 10.4)。

必须引入**独立的块尺寸选项**, 与帧上限解耦:

```
tcp.WithReadChunkSize(n int)   // 默认 64KB, 与 MaxFrameBytes 无关
tcp.WithWriteChunkSize(n int)  // 默认 64KB
```

并追加两条规则:

1. `Chunk` 尺寸由 `WithReadChunkSize` 决定, **不是** `MaxFrameBytes`。
2. 帧大于当前 Chunk 时, 该帧单独分配一块"大块" (overflow 路径), 处理完立即归还 slab。
   这样 `MaxFrameBytes` 回归为纯粹的**协议校验上限**, 不再影响内存占用。

验收标准补充: 新增 `BenchmarkTCPEcho_10kIdleConnections`, 断言常驻堆 <
 `连接数 x ReadChunkSize x 1.2`。没有这个基准, 零拷贝改造可能悄悄把内存放大 16 倍。

**该断言必须在 Go 1.26 (Green Tea 默认) 下重采基线。** Green Tea 把基线 RSS 抬高
8-15%, 用旧GC 采的阈值会在新工具链上误报。同时注意 Green Tea 偏好"同尺寸对象密集"
的规整堆, 而本项目的 Chunk slab 恰是这种形态 -- **两者同向叠加, 不是互斥**。
见 12.1。

---

## 5. 运行时层

### 5.1 注册表分片 (修正 F6)

结论: **分片, 但 `max` 语义必须显式降级并文档化。**

分片后:

- `Snapshot` / `Range` / `Broadcast` / `CloseAll` 的语义**确实不变**。
  `Snapshot` 本来就无序 (map 迭代), 分片不引入新的不确定性。
  `CloseAll` 的终止条件是"快照为空" (`session_manager.go:124-126`), 与分片无关, 依然成立。
  v1 5.1 "语义不变" 的结论正确, 但它给出的理由 ("当前靠直到 map 空") 是误读。

- `Register` 的容量检查**从精确变为近似**。当前 `int64(len(m.sessions)) >= m.max`
  在单锁内完成, 精确。分片后只能读 `count atomic.Int64`, 检查与插入之间存在窗口,
  最大超出 `#shards` 个会话。这是本次重构**唯一一处真实的行为退化**,
  必须写进 `WithMaxSessions` 的文档: "上限为近似值, 高并发注册时可能短暂超出至多 #shards 个"。

- v1 5.1 的 false sharing 填充是错的: `_ [40]byte` 使结构体变为
  RWMutex(24) + map 指针(8) + 40 = 72 字节, **不是 64 的整数倍, 依然跨界**。
  正确做法是显式填充到 128 字节 (两个 cache line), 或不做填充 (见下)。

结论: 填充**不做**。理由: `Register`/`Unregister` 的频率是每连接一次 (或 HTTP 的每请求一次),
摊薄到单次约 100ns 量级, 而 32 分片带来的 map 查找成本与 GC 扫描成本是确定的负收益。
分片本身保留 (它消除了 `SessionManager_RegisterGetUnregister` 基准下的全局锁串行化),
填充去掉。

新增基准: `BenchmarkSessionManager_RegisterGetUnregister_Parallel`, 验证分片收益确实存在。
若该基准显示无收益, 整个 5.1 应被删除而非保留。

### 5.2 指标原子化

结论: **做, 且优先级应高于 v1 所设的位置** (依据 G2, HTTP 路径每请求触发)。

```
infra/observability/prometheus.go

type PrometheusMetrics struct {
    mu      sync.RWMutex          // 只保护 map 结构
    counters   map[string]*promCounter
    gauges     map[string]*promGauge
    histograms map[string]*promHistogram
}

type promCounter struct {
    name   string
    labels []string               // 仅在创建时分配一次
    value  atomic.Uint64
}
```

`IncCounter` 的 key 仍需 `metricKey(name, labels...)` 字符串拼接, 这是无法消除的
(Go 的 map 不能用复合 key 而不分配)。但可以消除另外两项:
labels 的重复分配 (改为创建时一次) 与全局独占锁 (改为无锁 `atomic.Add`)。

`SetGauge` 用 `math.Float64bits` + `atomic.StoreUint64`。
`ExportText` 读侧在 `RLock` 下把 `*promCounter` 取值后立刻释放锁再做格式化,
避免持锁做字符串拼接。

### 5.3 工作池不分片 (推翻 F3)

v1 5.3 的 `ShardedPool` 有四个未考虑的后果:

1. `WithWorkers(count, queueSize, policy)` (`tcp/options.go:97-104`) 的 `queueSize`
   语义从"总队列长度"变成"每分片队列长度", 实际容量放大 32 倍。
   用户设置 512 得到 16384, 背压阈值完全失效。
2. `FullPolicy` 的 `PolicyClose` 分支 (`worker_pool.go:120-129`) 需要在 32 个分片上
   各自正确触发会话关闭, 且"队列满"的判定从全局变成局部, 行为不再可预测。
3. `Stop` 的排空协议 (`worker_pool.go:145-165`) 依赖单 `submitMu` + 单 queue 的
   "提交与停止互斥" 证明, 分片后需要 32 份并重新论证, 否则出现"停止后仍有任务入队"。
4. 分片键 `sess.ID() & mask` 的收益依据是 "消除 channel 跨核 cache line 争用"。
   但全仓仅 `tcp/server.go:212` 一处 Submit, 且提交方是**每连接独占的读 goroutine**,
   同一连接的任务天然串行, 不存在多核争用同一 channel 的场景。

**结论: 不分片。** 保持 `shared/worker_pool.go` 单池结构, 只做 3.4 所述的
`Packet` 所有权改造与 `defer Release`。

**但"不分片"不等于"不伸缩"。** 分片解决的是跨 session 争用同一队列 (隔离问题),
本节否决; 而 worker **数量**固定是另一个问题 (算力问题), 由第 11 节的 L2
`ScalablePool` 单独解决。两者互不冲突:L2 不引入分片, 只在单池内增减 worker 数。

### 5.4 元数据 (推翻 F2)

v1 4.1 的 `Attrs` 方案: `Attrs.Slot(owner, name) *AttrSlot` 返回稳定句柄,
声称 "热路径零字符串比较"。核实两个真实调用点:

```
plugin/autoban.go:140-147     每消息 GetMeta(autoBanKeyMeta), 未命中则 SetMeta
plugin/ratelimit.go:149-156同上
```

即每 session 稳态下每消息 1 次 `GetMeta` 命中。`sync.Map` 在只读命中路径上是
一次无锁原子读 + 一次 map 查找, 无分配。v1 提议的替代是
`map[attrKey]any` + `sync.RWMutex`: 读路径变成一次 `RLock`/`RUnlock` (两个原子操作)
加一次含结构体键 (两个 string 头, 32 字节) 的哈希。**这比 `sync.Map` 更慢**,
因为 `sync.Map` 的读命中完全无锁。

同时 `sync.Map` 的"首次 Store 分配 dirty map"成本是一次性的, 每 session 摊销后
远小于每消息一次 `RWMutex` 的写侧开销。

**结论: 不做 `Attrs`, 不新增 `kernel/attrs.go`, 保留 `sync.Map`。**
配套动作: 删除零调用方的 `DelMeta` (G5), 修正 G6 的两处错误注释。

### 5.5 死代码清理

| 目标 | 证据 | 动作 |
|---|---|---|
| `Session.DelMeta` | `grep -rn DelMeta` 仅命中接口与 8 处实现, 无调用方 | 从 `core.Session` 与 8 个实现中删除 |
| `Message.Meta map[string]string` | `grep -rn "Meta:"` 无生产者; `grep -rn "\.Meta"` 无读取者 | 删除字段 |

`Message.Meta` 尤其危险: 它是 `map[string]string`, 一旦有人开始填充,
每条消息都会多一次 map 分配, 而 `Message` 本身在热路径上按值构造。

---

## 6. client 侧与测试迁移 (补 G1)

### 6.1 client 侧: 不引入新契约, 但必须同步迁移

**回答主理人问题: 不引入 `core.CodecIO`, 也不保持 "直接依赖 Framer" 不动。**

核实 `tcp/client.go`: 157 行, 零 `core.Session` 引用, 直接持有 `net.Conn`
并调用 `framer.WriteFrame` (`client.go:123`) 与 `framer.ReadFrame` (`client.go:138`)。
`protocol/lwm2m/client.go` (57 行) 只是 `Server` 之上的注册/读写包装, 无独立 IO, **不受影响**。

结论分三层:

**(a) 不引入 `core.CodecIO`。** 理由: client 与 server 面对的是**同一个分帧问题**,
需要的是同一套 `Decoder` 实现。引入第二个接口 `CodecIO` 会立刻产生"两个接口两套实现,
或一套实现两个适配器"的分叉, 而 client 侧并无任何 server 侧不具备的能力需求
(它不需要 SessionState, 不需要插件链, 不需要注册表, 不需要状态机)。
YAGNI: 不为只有一个实现、且不需要新能力的地方定义接口。

**(b) 但 `Framer` 必须替换, 不能"不动"。** 这是 v1 的硬伤: `Framer` 一旦被
`Decoder` 取代, `client.go` 直接编译失败, 且**全部 45 个 `Benchmark*` 失效**
(均通过 `tcp.NewClient` 驱动)。所以 client 必须与 M5 同步迁移。

**(c) client 只需 `Decoder`, 不需要 `kernel` 的其余部分。** 具体映射:

```
server 侧新增                     client 侧对应                        client 是否需要
core.Packet / kernel.Chunk        需要 (ReceiveInto 的落脚缓冲)        是
core.Decoder (4 个实现)           直接复用同一份实现                是
kernel.ReadLoop                   不需要 (client 主动 pull 式 Receive) 否
kernel.ConnState                  不需要 (无状态机, 无生命周期)         否
kernel.WriteQueue                 不需要 (client 无并发 Send 队列)      否
```

`client.go` 的改造形态: 持有一个 `Decoder` + 一个可增长的接收缓冲, `Receive` 从中切帧。

```go
// tcp/client.go v2
// Receive 保持原签名, 返回独立副本, 语义与 v1 完全一致 (不破坏 API)。
func (c *Client) Receive() ([]byte, error)

// ReceiveInto 零拷贝接收: 直接把帧写入调用方提供的缓冲, 避免每帧一次分配。
// 基准与高吞吐场景使用本方法; 这是 client 侧唯一的性能相关 API。
// 约定: dst 长度不足时返回错误, 且连接状态不变 (不消费数据)。
func (c *Client) ReceiveInto(dst []byte) (int, error)
```

`dst` 由调用方提供这一点很重要: **它把缓冲所有权明确交给调用方, 与 server 侧的
`Packet` 引用计数是同一类问题的同一个解法** (谁持有谁释放), 只是因为 client 是
pull 模型所以不需要跨 goroutine 传递, 实现上可以简单得多 (无引用计数, 无 slab)。

### 6.2 测试与脚本迁移

现状: `tests/` 4 个子目录; `scripts/` 4 个文件; 各 transport 内有大量白盒 `_test.go`。

**核实主理人提到的 3 个文件, 实际只有 1 个需要重写:**

| 文件 | 核实结果 | 迁移方式 |
|---|---|---|
| `tests/benchmark/protocol_bench_test.go` | **确需重写**。`:247` 定义 `benchSession`, 手工实现全部 13 个 `core.Session` 方法 (含 `DelMeta`) | 契约变更后需补 `SendPacket`。**更好的做法: 让它内嵌 `kernel.ConnState`** -- 从此该 fake 只需 3 个方法, 且新增接口方法时无需再改。这是本次重构的一个额外收益 (测试替身自动瘦身) |
| `tests/stress/stress_test.go` | **无需重写**。`:278,557,582,623` 只是在 handler 闭包**消费** `core.Session` 作为参数类型, 不实现它 | 零改动 (前提: `core.Handler` 签名不变, 见 2.4 -- 确实不变) |
| `tests/cross_protocol_test.go` | **无需重写**。`:37,472` 同样是消费方 | 零改动 |

主理人担心的 "3 个文件要重写" 实际是 1 个。这个差异很重要: 它把测试迁移成本
从"改写测试逻辑"降到"给一个 fake 加一个方法", 从而让 7.2 的增量过渡策略更加可行。

完整迁移清单:

| 类别 | 例子 | 策略 |
|---|---|---|
| 传输内白盒测试 | `tcp/session_test.go`, `tcp/framer_test.go`, `framer_fuzz_test.go`, `framer_overflow_regression_test.go`, `grpcweb/framing_test.go` | **必须重写**。直接构造 `session`/`Framer`。改为构造 `Decoder` + `Packet` 并断言 `Data`/`Raw`/`consumed` |
| 黑盒集成测试 | `tcp/server_integration_test.go` (808), `coap` (548), `grpcweb` (543), `quic` (410), `websocket` (405), `http` (294) | **应保持零改动**。这正是增量过渡策略的主要收益 |
| 会话生命周期回归 | `udp/grpcweb/coap` 的 `session_reuse_regression_test.go` | 适配字段访问路径 (`sess.id` -> `sess.ID()`) |
| 缺陷回归 | `plugin/plugin_defect_regression_test.go`, `session_manager_test.go`, `plugin_lifecycle_regression_test.go` | 零改动 |
| 基准 | `tests/benchmark/` 4 文件, `plugin/benchmark_test.go`, `framer_test.go` | 迁移到 `ReceiveInto` 与新 Decoder |
| 脚本 | `scripts/run_benchmarks.go` 等 4 文件 | 基准名变更时同步更新; 增加 MemStats 输出 (见 10.3) |

---

## 7. 实施序列

### 7.1 基线锚点修正 (F4)

v1 1.2/3.5/7 给出三个互相矛盾的 LineFramer 数字 (2394 / 223.6 / 100)。
v2 规定: **唯一锚点为 `logs/2026-06-25T17-10-25_benchmark.log`**,
M0 阶段重新采集一次并固化到 `docs/reports/`, 之后每阶段与此对比。

| 基准 | 基线 | v2 目标 | 依据 |
|---|---|---|---|
| BenchmarkLineFramerRoundTrip | 2394 ns/op, 12 allocs, 1840 B | < 400 ns/op, 0 allocs | 单次 IndexByte |
| BenchmarkLengthPrefixFramerRoundTrip | 57960 ns/op, 7 allocs | < 2 allocs | 读循环零分配 |
| BenchmarkTCPEcho_PayloadSize/65507 | 156209 ns/op, 196889 B/op | B/op **< 8KiB** | 消除全部 2 次帧复制, 见 3.6 |
| BenchmarkHTTPEcho | 9807 ns/op, 90 allocs | **不劣化** | HTTP 不走零拷贝路径 |
| BenchmarkSessionManager_RegisterGetUnregister | 待采集 | 分片后提升 > 20% | 见 5.1 |

注: 65507 场景的 B/op 目标是**趋近 0** 而非 v1 的 65536。这是从 3.6 的所有权链
推导出的必然结果, 也是本次重构与 v1 在性能目标上最大的差别。

### 7.2 阶段划分: 自底向上, 增量过渡

**先回答 v1 9.3 遗留的"分提交 vs 合提交", 这是明确结论, 不再是待决策项:**

**结论: 不合并。v1 的 M1 与 M2 必须拆开, 且 M1 本身不应存在。**

v1 的 M1 是"改 core.Session 契约 + kernel.ConnBase", M2 是"TCP 迁移到新内核"。
合提交的问题是: 中间态无法构建, 长达数百行的改动混在一个 review 里, 且
"契约变更是否正确"与"迁移是否行为等价"两个独立问题被绑死, 无法分别归因。

v1 把选择框定为"分提交便于 review, 合提交便于 CI 绿灯" -- **这个框定本身就错了**,
因为它默认了"改契约必然导致中间态构建失败"。**增量过渡可以让两者同时成立**:
契约不变则 CI 恒绿, 每步仍可独立 review。

所以 v2 的答案是第三条路: **先不碰契约**。

`ConnState` 是纯新增的 embeddable struct, 提供的 8 个方法签名与现有手写方法**完全一致**。
把它内嵌进一个传输的 session 后, 该 session **同时满足未改动的 `core.Session`**,
此时删除重复手写方法零编译影响, `core.Session` 本身一个字节都不用改。

因此原则是:

> **先加不改, 后删旧契约。任何提交都保持 `go build ./... && go test ./...` 绿灯。**

自底向上, M1/M2/M3 互不依赖且可并行, 都是纯新增, 都不触碰任何现有文件:

```
M0 基线固化+基准补齐            零风险, 独立提交
M1  core.Packet + kernel.Chunk + slab    纯新增, 无接入方
M2  kernel.Decoder + 4 个实现           纯新增, 无接入方
M3  kernel.ConnState (0 个 IO 方法)     纯新增, 无接入方
M4  TCP 接入 ConnState (删手写方法)      契约未变, 黑盒测试零改动
M5  TCP 接入 Decoder + Packet 零拷贝 + client 侧迁移
M6  TCP 接入 SendPacket 零拷贝写
M7  其余传输接入 ConnState (逐个提交)
M8  运行时: 指标原子化 + 注册表分片
M9  api 收敛 + 删死代码 + 契约收口 (此时才动 core.Session)

L2-a poolCore 抽取 + ScalablePool 骨架      纯新增, 不改WorkerPool
L2-b workerRef 身份 + run() 退休分支        纯新增
L2-c shouldScale + Submit 内取模触发         纯新增
L2-d 伸缩行为测试 + race 检测                 纯新增
L2-e tcp.Option 接线 (WithElasticPool 等)    改 tcp/options.go 一个文件
```

**L2 曾于 2026-10-16 暂缓, 当日恢复。** 同机双进程压测 (13) 曾得出
"worker 数无关"并建议暂缓; **分离式压测 (14) 实测 worker 1->16 吞吐提升
11.96 倍 (近线性, w=8 时线性度 0.91x, w=16 衰减至 0.75x)**, 立论成立。
**实施前需按 14.4 调整 `maxWorker` 默认值为 `2 x GOMAXPROCS`。**
详见 14.3与 14.4。

| 阶段 | 内容 | 验收标准 | 依赖 | 风险 |
|---|---|---|---|---|
| M0 | 重跑 `scripts/run_benchmarks.go`, 固化到 `docs/reports/2026-10-16-baseline.md`; 补 7.1 的 5 项 + 10.5 的 4 项基准; bench 脚本输出 MemStats | 报告中含 7.1 全部 5 行; 10.5 全部 4 项可运行; MemStats 可打印 | - | 无 |
| M1 | 新增 `core/packet.go`, `kernel/chunk.go` | `go build ./...` 绿灯; `chunk_test.go` 覆盖: 引用计数归零才归还、并发 Release 幂等、双重 Release 无副作用、Acquire 复用同块 | M0 | 无 (无接入方) |
| M2 | 新增 `core/codec.go` 的 Decoder, `kernel/decoder.go` + 4 实现 | `Decoder` 无状态性由 `go test -race` 验证 (同一实例并发用于多 session); LineFramer 基准 < 400ns; 跨块用例正确 | M0 | 无 |
| M3 | 新增 `kernel/conn_state.go` | 单测: 状态机单迁移、`Shutdown` 幂等、`NewConnStateWithParent` 的父 ctx 取消可传播 (G3) | M0 | 无 |
| M4 | `transport/tcp/session.go` 内嵌 `ConnState`, 删 10 个重复方法 | TCP 156 -> <= 70 行; `tcp/server_integration_test.go` (808 行) 零改动通过; `-race` 绿灯 | M3 | 低 |
| M5 | `kernel/readloop.go`; `transport/tcp/framer.go` 迁移到 Decoder; `tcp/client.go` 加 `ReceiveInto`; 引入 `WithReadChunkSize` (4.2) | 全部 45 个基准可运行; TCP 1KB `allocs/op < 2`; **10k 空闲连接 HeapInuse < 10k x ReadChunkSize x 1.2** | M2, M4 | **高** (首次引入跨块窗口与所有权) |
| M6 | `kernel/write_queue.go` + `shared/worker_pool.go` 改承载 `Packet`; `core.Session` 加 `SendPacket`; TCP 实现它 | 65507 场景 B/op < 8KiB; `handle` 的两个早退分支无引用计数泄漏 (专门测试); 回显路径零 syscall 冗余 | M5 | **高** (所有权跨 3 个 goroutine) |
| M7 | quic / websocket / udp / coap / http / grpcweb 逐个内嵌 `ConnState`; grpcweb 两个形态分别处理 (10.1); http 用 `WithParent` | 8 处 `session.go` 全部 <= 60 行; 各 transport 白盒测试适配完成; HTTP 用 `NewConnStateWithParent` (G3) | M4 | 中 |
| M8 | `infra/observability` 原子化; `runtime/session_manager` 分片 | `BenchmarkSessionManager_RegisterGetUnregister_Parallel` 提升 > 20%, 否则撤回分片; `WithMaxSessions` 文档化近似语义 (F6); HTTPEcho 不劣化 | M4 | 中 |
| M9 | `api/api.go` 收敛; 删 `DelMeta` 与 `Message.Meta` (G5); 补 `SessionContext` 可选接口 (10.1); 修正 G6 注释; 更新所有权规则文档 (3.5) | `go build ./...` + `go vet` 绿灯; `benchSession` 内嵌 `ConnState` 后自动瘦身 (6.2); 文档与代码一致 | M5, M6, M7, M8 | 低 |

**M9 之前不动 `core.Session` 的既有方法** -- 这是本方案与 v1 最大的流程差异,
也是"任何提交 CI 绿灯"能成立的原因。`SendPacket` 在 M6 加入是唯一的契约新增,
且它是**加法**(新方法)而非变更, 现有实现编译失败的风险为零。

**L2 序列独立于 M0-M9。** `ScalablePool` 只使用 `core.Handler` / `core.Session` /
`core.Message` 三者, 全是既有稳定契约, 因此 L2 不依赖 M1 (ConnState), 可与
M1/M2/M3 并行。L2-a 到 L2-d 为纯新增, 不触碰任何现有文件; L2-e 仅改
`tcp/options.go`。设计细节见第 11 节。

### 7.3 依赖图

```
M0 基线
 |
 +-- M1 Packet/Chunk --------+
 |                           |
 +-- M2 Decoder --------------+-- M5 TCP零拷贝读 -- M6 零拷贝写 --+
 |                           |                                     |
 +-- M3 ConnState ------------+-- M4 TCP接入ConnState+--+          |
                                     |                  |          |
                                     |                  M7 其余6传输|
                                     |                             |
                                     +-- M8 运行时 -------------- M9 api/清理

M0 基线 -- L2-a-- L2-b-- L2-c-- L2-d-- L2-e
```

关键性质: **M1, M2, M3 三者互不依赖, 可并行**。它们都是纯新增, 都不改任何现有文件,
因此可以在契约完全不变的前提下先落地并充分测试。这是 v1 方案缺失的:
v1 把三个概念捆在 M1 里一起改契约, 使得风险最高的部分无法独立验证。

**L2 序列挂在 M0 之下, 与 M1-M9 无横向依赖**, 原因见 11.8: `ScalablePool`
只依赖既有稳定的 `core` 层三件契约。

### 7.4 风险与缓解

| 风险 | 影响 | 缓解 |
|---|---|---|
| Packet 引用计数泄漏 (worker `handle` 的早退分支) | 严重: Chunk 永不归还, 稳态内存无界增长 | M6 验收标准中列出专门的泄漏测试: 构造 `Session == nil` 与 `handler == nil` 两条路径, 断言 chunk 归池 |
| 跨块窗口搬运的实现错误 (帧横跨两块) | 严重: 数据错乱 | M2 的 Decoder 单测必须覆盖"帧头在块尾, payload 在块首" |
| 插件返回别名切片被误判为新分配 | 中: 提前 Release | 3.5 的别名规则用 `unsafe.SliceData` 前后比较判定, 单测覆盖 BasePlugin 与 cluster 两种形态 |
| 注册表分片无收益 (引入 32 个 map 换不到锁收益) | 中: 净负收益 | 5.1 已规定: 若新基准显示无收益则删除整个 5.1, 不保留 |
| 灰度期新旧契约并存导致实现漂移 | 低 | 过渡期仅存在于 M4-M7, M9 强制收口; 每个阶段结束检查 `grep -rn "sync.Map" internal/transport/` 应逐步归零 |
| 无法复现基线 | 中 | M0 前置, 且 M0 独立于所有后续阶段 |
| Go 1.26 Green Tea 默认改变 GC 与内存基线 | 中 | 10.4 的 `HeapInuse` 断言必须在 Go 1.26 下重采 (12.1); M0 基线报告须记录工具链版本与 `GOEXPERIMENT` |
| L2 并发 `addWorker` 激活 `wg.Add`/`go` 竞态窗口 | **严重: panic** | 必须用 `sync.WaitGroup.Go`, 不得用三段式 (12.3, 已修订 11.3.2) |

---

## 8. 明确不做 / 推迟的决策 (回答问题 d)

| 项 | v1 决策 | v2 决策 | 理由 |
|---|---|---|---|
| sync.Pool 帧池 | 不做 | **不做, 但理由不同** | v1 的理由 ("Go 堆已 per-P 池化") 只对小对象成立, 64KB 帧走大对象分配路径。v2 不做的真正理由: Chunk slab 已经提供了复用能力, 且是**引用计数归还**而非 `sync.Pool` 的"借了可能不还"。两者是替代关系, 前者严格更优 (无 size class 舍入浪费, 无跨 goroutine 归还路径)。引入 `sync.Pool` 反而制造第二个复用机制 |
| 写队列 buffer 复用 | **未提及** | **做, 这是 v1 最大的遗漏** | `tcp/session.go:90` 与 `quic/session.go:67` 每消息一次全帧复制。63KB 场景下这是主要成本。见 3.6, 由 Packet 所有权转移一并解决 |
| 合并读写 goroutine | 不做 | 不做 | 结论正确。合并等于把队头阻塞从工作池挪回读循环 |
| 哈希轮转定时器 | 不做 | 不做 | 结论正确。`Heartbeat.Sweep` (`plugin/heartbeat.go:75-91`) 是惰性扫描, 每 session 无 timer |
| HTTP/gRPC-Web 分配优化 | 推迟 | **推迟, 但纠正归因** | v1 称 "90 allocs 主要在第三方库"。核实 `http/server.go:171` 的 `io.ReadAll` 是标准库且按 body 大小增长, `responseRecorder` 与 session 每请求各分配一次。真正可优化的是"每请求构造 session + 注册表 + 元数据"。但这属于 request-scoped session 的架构问题, 不应塞进本次内核重构。**列为 M10 之后的独立议题** |
| `Attrs` 元数据表 | 做 | **推翻, 不做** | 见 5.4 |
| 分片工作池 | 做 | **推翻, 不做** | 见 5.3 |
| `core.Conn` 统一抽象 | 做 | **推翻, 不做** | 见 2.1 |
| 调整 GOGC / `debug.SetGCPercent` | 未提及 | **明确不做** | 库代码调用 `debug.SetGCPercent` 是进程级全局副作用, 一个被 import 的网络库无权改动宿主的 GC 参数。Go 1.26 起Green Tea GC 已默认启用且效果明显, 局部调参只会破坏它。正确做法: 在 `docs/guides/` 中给出**建议值** (零分配后堆增长趋缓, 可考虑 GOGC 200-400), 由应用在启动时自行设置; 并在 `scripts/run_benchmarks.go` 中输出 `runtime.ReadMemStats` 的 `Mallocs`/`PauseTotalNs`/`NumGC`, 使 GC 行为可测量而非靠猜 (复核: 库代码当前确实一个 GC 参数都没碰, 见 12.1) |
| 调用 `runtime.GOMAXPROCS` | 未提及 | **明确不做 (新增约束)** | Go 1.25 起容器感知 GOMAXPROCS 默认启用, 但**手动设置会直接关闭它**, 在 K8s 环境造成 CPU 节流。库无权替宿主决定。见 12.2 |

---

## 9. 汇总: v1 与 v2 的关键差异

| 维度 | v1 | v2 |
|---|---|---|
| 传输层契约 | `core.Conn` 5 方法 (含假 IO) | `kernel.ConnState` 内嵌基座, 0 IO 方法 + 传输自实现 4 方法 |
| 帧内存 | `Frame{Data, Raw}` 视图, 生命周期靠文档约定 | `core.Packet` 引用计数句柄, 生命周期靠类型系统与 slab 归还保证 |
| 跨goroutine 所有权 | 未定义 (列为待决策项) | `dispatch` 为唯一转移点; `WorkerPool` 与 `WriteQueue` 各自 Release |
| 插件接口 | 需澄清返回值所有权语义 | **不变**; 现有 5 个插件已天然符合别名规则 |
| `Clone()` | 作为契约要求 | 保留为逃生舱, 非必需 |
| 写路径 | 预留头, 仍每帧 1 次分配 | `SendPacket` + Packet 转移, 回显路径 0 分配 |
| 元数据 | 引入 `Attrs` 包 | 推翻, 保留 `sync.Map` |
| 工作池 | 分片 32 路 | 推翻, 保持单池 |
| 注册表 | 分片 32 路 + 填充 | 分片保留, 填充去掉, 上限降级为近似并文档化 |
| 实施顺序 | M1 先改契约 (7 个 transport 同时编译失败) | M0-M3 纯新增并行, M4 起逐个接入, 契约最后改 |
| GC 调优 | 未提及 | **明确不做, 且前提反转** | 见 10.3。真正问题是 4.2 的常驻内存放大 (1MB x 连接数), 不是 GC 压力。GC 对常驻对象无效, 故调参是方向性错误。解药是独立的 Chunk 尺寸选项 |
| 拆分 `LongLivedSession` / `RequestScopedSession` | 未提及 (主理人补充提议) | **推翻, 不拆** | 见 10.1。拆分会迫使 `Plugin`/`SessionManager`/`Handler` 三处签名变更或泛型化, 且 5 个内置插件 + 所有用户插件全部要改。而拆分的收益 (插件看窄接口) Go 已用隐式接口免费提供 |
| `core.CodecIO` client 契约 | 未提及 (主理人补充提议) | **不做** | 见 6.1。client 与 server 面对同一个分帧问题, 应复用同一 `Decoder`; 引入第二个接口会立即分叉 |
| client 侧迁移 | 完全未覆盖 | **必须与 M5 同步** | 见 6.1。不迁移则 45 个 Benchmark 全部失效 |
| Chunk 尺寸与 MaxFrame 解耦 | 未提及 | **必须解耦** | 见 4.2。照 v1 用 MaxFrame (默认 1MB) 预留, 1 万连接即 10GB 常驻, 且 GC 无法回收 |
| 零拷贝的内存放大风险 | 未列为风险 | **列为风险** | 见 10.4。瞬时分配变常驻内存, 是本次重构引入的新风险类别 |
| WebSocket 分类 | (v1 未分类; 补充说明误归为请求响应型) | **长连接, 与 TCP 同等改造** | 见 4.1。核实其有 Upgrade 后真 conn、readLoop、pingLoop、写锁 |
| kernel 分层 | 四层单模型 | **三层可选模型** | 见 4.1。第 0/1 层全部形态共用, 第 2 层按能力 opt-in |

---

## 10. 补充审查: 三个盲点的定论

本节回答主理人中途补充的三项事实。核心结论先给: **其中两条的前提需要纠正,
第三条 (GC) 的定性需要反转。**

### 10.1 关于"HTTP/WS 应排除在 kernel 之外"的定论

**先纠正前提: WebSocket 不是请求响应型。** 详见 4.1 的代码证据 -- 它有 Upgrade
后的真 net.Conn、有 readLoop (`websocket/server.go:202`)、有 pingLoop (`:236`)、
有写锁 (`websocket/session.go:69`)。它与 TCP 同构, 应享受与 TCP 同等的零拷贝改造。
若按"HTTP/WS 都排除"实施, 会白白丢掉 WS 的改造机会, 这是实质损失。

**回答 (1) 是否排除:**

- **HTTP 与 grpcweb-http 模式: 排除第 2 层, 保留第 1 层。** 即: 不用
  ReadLoop/Decoder/Chunk/WriteQueue, 但**仍然内嵌 `kernel.ConnState`**。
  `net/http` 内部读取缓冲我们碰不到也不该碰 (主理人判断正确), 但 `ConnState`
  有 0 个 IO 方法, 所以 HTTP 内嵌它没有任何障碍, 却能省掉 9 个重复方法。
- **WS 与 grpcweb-ws 模式: 完全不排除。**
- **plain UDP / CoAP: 排除第 2 层, 保留第 1 层。** 共享 socket, 无连接级读循环。

**回答 (2) 是否拆分 Session 为两个契约: 不拆。** 这是本节最重要的判断, 理由如下。

主理人提议拆为 `LongLivedSession` + `RequestScopedSession`, 并抽一个更小的
`SessionContext` 给插件用。**拆分方案会在运行时制造三个新问题, 收益却只有一点:**

拆分收益: 插件可以只依赖 `SessionContext` 而不依赖完整 Session。

拆分代价 (全部是实质的):

1. **插件接口本身就要改签名。** `core.Plugin.OnAccept(Session) error`
   (`core/plugin.go:14`) 必须改成 `OnAccept(SessionContext) error`,
   于是 **5 个内置插件 + 所有用户插件全部要改**, 且这是**公开 API 破坏**。
   而拆分的动机仅仅是"让插件不必看到完整 Session" -- 但插件本来也只用其中几个方法,
   Go 的隐式接口已经天然提供了这个能力 (插件可以自己声明所需的窄接口),
   **框架不需要为此改公开契约**。
2. **注册表被迫变成泛型或接口化。** `SessionManager.Register(Session)`
   (`core/session.go:30`) 要同时接受两个类型, 只能 `Register(any)` 或泛型,
   两者都破坏类型安全。当前"单一 Session 接口"恰恰是 `SessionManager` 简洁的**原因**,
   不是原因的问题。
3. **`Handler` 也被迫改签名。** `core.Handler func(Session, Message) error`
   (`core/message.go:12`) 若要同时服务两种 Session, 又得泛型或 `any`。
   而测试侧 4 个文件 (stress 4 处 + cross_protocol 2 处 + bench 的 echoHandler)
   正是按 `func(sess core.Session, ...)` 写的, 全部要改。

对比之下, **不拆分的代价是什么?** 只是插件看到的方法多了几个 (12 个)。
插件调用 `Send` 或 `Close` 在 HTTP 上是合法的 (语义已定义: 写 ResponseWriter / 取消请求 ctx),
**不存在"只能改签名才能表达"的场景**。

**结论: 保持单一 `core.Session`。** 若主理人仍希望插件面更窄, 正确做法是
**新增一个可选的窄接口供插件按需断言, 而不改任何现有签名**:

```go
// core/session.go 追加

// SessionContext 是 Session 的只读最小视图, 供希望避免依赖完整 Session 的
// 代码使用 (如自定义中间件)。**这是可选的补充接口, 不是 Plugin 的契约**:
// core.Plugin 的签名保持 Session 不变, 因此 5 个内置插件与用户插件均无需修改。
//
// 之所以提供它, 而不是把 Plugin 改成接受 SessionContext:
// 改 Plugin 签名会破坏所有用户插件, 且迫使 SessionManager 与 Handler 泛型化;
// 而插件若真需要窄视图, 可自行声明本地窄接口 (Go 惯例), 或断言本接口。
type SessionContext interface {
    ID() uint64
    Protocol() Protocol
    State() SessionState
    LastActiveAt() time.Time
    Context() context.Context
    GetMeta(string) (any, bool)
    SetMeta(string, any)
}
```

`Session` 隐式满足 `SessionContext`, 无需任何适配代码。这是 0 成本的增量,
保留了"窄视图"的价值, 但不破坏任何签名。

**回答 (3) HTTP/WS 收益是否为零甚至为负:**

| 传输 | 性能收益 | 代码量收益 | 若强行套 v1 的 Conn 五方法 |
|---|---|---|---|
| tcp | 大 (零分配回显) | 156 -> 约 70 行 | 正收益 |
| websocket | **大 (与 TCP 同等)** | 93 -> 约 55 行 | 正收益 |
| quic | 中 (消除 Send 复制) | 132 -> 约 70 行 | 正收益 |
| grpcweb-ws | 中 | 100 -> 约 60 行 | 正收益 |
| udp-dtls / coap-dtls | 小 (消除一次复制) | 103 -> 约 60 行 | 正收益 |
| **http** | **零 (不走零拷贝路径)** | 102 -> 约 40 行 | **负收益**: 需为 Read 写桩方法 |
| **grpcweb-http** | **零** | 91 -> 约 45 行 | **负收益** |
| udp-plain / coap-plain | 零 | 103 -> 约 60 行 | 负收益 (Send 需 WriteToUDP, 与 Write 冲突) |

即: HTTP 路径**性能收益为零, 代码量收益为正, 只有在强行套 Conn 五方法时才转为负收益**。
v2 的三层模型避免了这一点。

### 10.2 关于 client 的定论

见 6.1。核心: **不引入 `core.CodecIO` (YAGNI, 且会与 `Decoder` 分叉),
但必须同步迁移 (否则 45 个基准全废)。client 只需 Decoder + Packet, 不需要 ConnState。**

### 10.3 关于 GC 调参的定论: 主理人的前提需反转

主理人问 "零分配后是否需要 SetGCPercent / GOMEMLIMIT"。核实后**前提是反的**:

v1 3.3 的读循环缺陷 2 (已在 v2 3.4 记录) 是"每连接预留整整一个 `MaxFrame`",
而 `tcp/options.go:43` 的默认值是 **1MB**。若照v1 实施:

```
10000 并发连接 x 1MB 常驻 = 10GB
```

**这是常驻内存, 不是垃圾。GC 对常驻对象完全无效** -- 无论 GOGC 调成多少。
所以"零分配后调 GC 参数"是**方向性错误**: 真正的问题从"GC 压力"变成了"内存放大",
而 GC 参数不是内存放大的解药。

v2 的定论:

1. **库代码不调用 `debug.SetGCPercent` 或 `debug.SetMemoryLimit`。** 理由同 v2 8:
   库无权改动宿主的进程级参数。这条在 v2 已经定论, 此处重申并加强 --
   因为零分配改造会显著改变堆曲线, 库更不应擅自替宿主决定。
   **Go 1.26 补充**: Green Tea GC 已默认启用, GC CPU 降 10-40%。有一个官方默认的、
   明显更好的 GC 摆在面前, 更凸显"库不调参"是正确的。见 12.1。
2. **真正要做的是把内存占用变成显式可配置的量**, 即 4.2 的
   `WithReadChunkSize` / `WithWriteChunkSize`, 默认 64KB 而非 1MB。
   这才是把 10GB 降到 640MB 的手段。
3. **GC 参数建议写进文档而非代码**, 且方向是**上调而非下调**:
   稳态零分配后堆增长缓慢, `GOGC=100` 触发频率极低, 此时提高 GOGC 以换取更低 GC 次数
   是合理的 (代价是更高的峰值常驻)。建议在 `docs/guides/` 给出
   `GOMEMLIMIT` 与 `GOGC` 的取值区间与测量方法, 由应用自行决定。
4. **增加可测量性**, 这是唯一应该进代码的部分: `scripts/run_benchmarks.go`
   输出 `runtime.ReadMemStats` 的 `HeapAlloc` / `HeapInuse` / `NumGC` /
   `PauseTotalNs` / `Mallocs`, 并新增 10.5 的常驻内存基准。
   **GC 行为必须可测量, 不能靠猜。**

### 10.4 补充风险: 零拷贝把"瞬时分配"变成"常驻内存"

这是本次重构引入的新风险类别, v1 与 v2 初稿都未列:

| 风险 | 机理 | 缓解 |
|---|---|---|
| 常驻内存放大 | 每连接持有固定大小 Chunk, 即使连接空闲也常驻 | 4.2 的独立块尺寸选项 (64KB 默认); 新增常驻内存基准 |
| slab 泄漏 | 任一 Packet 未Release, 对应 Chunk 永不归还 | M6 验收标准中的泄漏测试; slab 层加 `debug.SetFinalizer` 只在测试构建下检测未归还 Chunk |
| 空闲连接内存不降 | 空闲长连接仍占 Chunk | 提供 `WithReadChunkSize(0)` 表示"按需增长, 空闲时归还 slab", 代价是稳态重新产生分配。**建议作为可选项而非默认** (与 4.2 的内存/延迟权衡一致) |

第 3 条是真实的取舍: 固定 Chunk 换稳态零分配, 代价是空闲连接也占内存。
对于连接数远大于活跃数的场景 (如 LWM2M), 应允许用户选择按需模式。

### 10.5 补充基准

| 基准 | 目的 | 期望 |
|---|---|---|
| `BenchmarkTCPEcho_10kIdleConnections` | 4.2 的常驻内存守卫 | `HeapInuse <连接数 x ReadChunkSize x 1.2` |
| `BenchmarkReadLoop_PacketLifecycle` | 引用计数正确性 (块归零才归还) | 归零 |
| `BenchmarkDecoder_FrameSplitAcrossChunk` | 跨块窗口正确性 + 吞吐 | 无跨块错误 |
| `MemStats` 输出 (bench 脚本) | GC 可测量性 | 记录 NumGC 与 PauseTotalNs |

---

## 11. L2 层: WorkerPool 弹性伸缩

本节补充 v2 5.3: 分片被否决, 但 worker **数量**固定是另一个问题。
数据来自 2026-10-16 两组实测, 不是推测。

### 11.1 设计依据: 实测数据

本节的设计不是推测, 来自 2026-10-16 的两组测量。

### 11.1.1 确定性测试

`internal/transport/shared/worker_pool_saturation_test.go` (5 项全通过, 本地与云端 120.76.44.233 结果一致, `-race -count=3` 无竞态), 用 "所有 worker park 在永不返回的 handler 里" 的方法把队列容量变成可证明的属性:

| 测量项 | 结果 |
|---|---|
| 容量上界 | workers=4 queue=8 -> accepted 恒为 8, rejected 492。**容量等于队列深度, 与 worker 数无关** |
| `PolicyClose` | 492 个被拒任务的 session 全部关闭, 无泄漏 |
| `PolicyBlock` | 确实阻塞 `Submit`。该调用在 `tcp/server.go:212` 的连接读 goroutine 内同步执行, **池饱和会停掉该连接的 reader** |
| `PolicyBlock` 逃逸阀 | `sessDone` 分支 (worker_pool.go:138) 能在 session 关闭时释放阻塞的 submitter, 不泄漏 goroutine |
| 队列共享范围 | 单个噪声 session 耗尽队列后, **无关 session 也被拒** |
| `Stop` 阻塞 | 实测等待 500ms, 因为 `submitMu` 写锁要等所有 in-flight Submit |

### 11.1.2 云端双进程压测

环境 `120.76.44.233` (2 vCPU / 1.6GB / Go 1.26.9 / KVM 虚拟机 / **非容器**,
`GOMAXPROCS(0)=2` 已实测确认, 见 12.9), 服务端与客户端分离进程, 丢弃率取服务端自报指标
`tcp_task_queue_full_total` (server.go:213), 不靠客户端推断。

> **[注意] 硬件事实修正 (2026-10-16 实测)**: 该机 `lscpu` 显示
> `Core(s) per socket: 1` + `Thread(s) per core: 2`, 即 **1 物理核 + 2 超线程**,
> 而非文档此前理解的 "2 核"。这不使数据失效 (超线程对 IO 密集型 handler 仍提供真实并行),
> 但**必须收紧一条推论**:11.1.3 的"4 worker 天花板 = 4000 msg/s"在 HT 争用下会低于理论值,
> 因此该数字是**乐观上界**, 不是可达上限。详见 12.9.1。

配置 4 worker / 512 queue / PolicyDrop:

| 连接数 | drop rate | loss rate |
|---|---|---|
| 1 | 0.000% | 62.6% |
| 10 | 4.012% | 89.5% |
| 50 | 32.575% | 97.7% |
| 100 | 36.219% | 99.4% |
| 200 | 47.375% | 99.2% |
| 500 | 54.488% | 91.9% |
| 1000 | 63.353% | 98.9% |

加 1ms 人工 handler 延迟后:

| 连接数 | drop rate |
|---|---|
| 1 | **92.916%** |
| 100 | 86.542% |
| 1000 | 86.343% |

### 11.1.3 由此得出的核心判断

**丢弃率的决定因素是 `handler 耗时 / worker 数`, 不是连接数。**

1ms handler 下即使单连接也丢弃 92.9%。50 连接的无延迟曲线 (0% -> 4% -> 32.6%) 说明单连接也能打满: 4 个 worker 的 512 深队列, 在 pipeline 客户端下只需几千条消息即被填满。

这推翻了 "队列太浅是主因" 的假设。实测把队列从 512 扩到 8192, 丢弃率反而从 52.6% 升到 85.4%。**队列深度不是解药, 它只把 "立刻丢" 换成 "撑一会儿再丢"。**

因此 L2 要解决的不是 "队列太浅", 而是 "worker 数固定且运行期不可调"。单池的 channel 结构本身没有问题, 问题是它背后没有调节阀门。

> ### [注意] 11.1.4 多核复测曾推翻上述判断 (2026-10-16, 8 vCPU) -- 该推翻本身已被推翻
>
> **本节的 11.1.3 推论经分离式压测 (14) 复测成立。**
> 中途曾因同机双进程压测 (13) 得出相反结论并暂缓 L2,
> 查明是**归因错误** (把客户端抢CPU 导致的低吞吐误读为"worker 数无关")。
> 分离式实测: worker 1->16 吞吐提升 **11.96 倍**, 近线性。
> 详见 14.3与 14.7。

---

### 11.2 现状约束: 为什么现在不能伸缩

核实代码后, 有三处硬约束:

### 约束 1: Stop 是一次性终态, 不可重入

```
worker_pool.go:146  if p.closed.CompareAndSwap(false, true) { close(p.done) }
worker_pool.go:153  p.submitMu.Lock()   // 写锁, 等所有 in-flight Submit
worker_pool.go:155  p.wg.Wait()
```

`closed` 是单向闩锁。收缩时若要停掉部分 worker, 无法复用 `Stop`, 只能引入独立的收缩路径。

### 约束 2: worker 无独立生命周期

`Start(workers)` (worker_pool.go:82-91) 只是 `for i := 0; i < workers; i++ { go p.run() }`。所有 worker 共享同一个 `p.done` 与 `p.queue`, **没有任何字段能标识 "这是第几个 worker"**。要停掉指定 worker, 必须先给它一个身份。

### 约束 3: 没有任何可观测接口

核实: `WorkerPool` 只有 `Handler` / `Enabled` / `Start` / `Submit` / `Stop` 五个方法, **没有 `Len` / `Stats` / 队列水位查询**。`len(p.queue)` 和 `cap(p.queue)` 可用但未被读取。

这意味着伸缩判据需要新增观测点。

---

### 11.3 设计: 可伸缩 WorkerPool

### 11.3.1 状态与身份

```
internal/transport/shared/
  pool_scalable.go     伸缩 WorkerPool (新增)
  pool_scale_test.go   伸缩行为测试 (新增)
```

核心改动是给 worker 身份:

```go
// workerRef 是单个 worker 的身份句柄。
// 伸缩的本质是"停掉某个特定 worker", 因此 worker 必须可被单独寻址,
// 而不能像现在这样只是一次 go p.run()。
type workerRef struct {
    id      int64          // 单调递增, 供伸缩决策与日志追溯
    retire  chan struct{}  // 关闭 = 请求该 worker 在当前任务完成后退出
}
```

池侧维护:

```go
type ScalablePool struct {
    // ... 沿用现有 handler / logger / queue / policy / submitMu / done

    mu      sync.Mutex
    workers map[int64]*workerRef   // 活跃 worker 集合
    nextID  atomic.Int64

    minWorker atomic.Int64         // 下限, 收缩到此为止
    maxWorker atomic.Int64         // 上限, 扩容到此为止
}
```

### 11.3.2 worker 循环: 可退休

改动集中在 `run()`。现在它只 select `queue` 与 `done`; 新增 `retire` 分支:

```go
func (p *ScalablePool) run(w *workerRef) {
    for {
        select {
        case t := <-p.queue:
            p.handle(t)
        case <-w.retire:
            // 收缩: 当前任务已做完 (单 goroutine 串行), 立即退出。
            // 不从 queue 抢任务, 避免与其他 worker 争抢导致队列饿死。
            p.removeWorker(w.id)
            return
        case <-p.done:
            // ... 沿用现有的排空逻辑
        }
    }
}
```

关键点: `retire` 与任务串行处理。因为 worker 是单 goroutine, 收到 `retire` 时手上的任务**必然已经做完**, 这正是安全收缩点。无需中断 handler, 无需任务迁移。

**worker 的启动必须用 `sync.WaitGroup.Go`, 不得用 `wg.Add(1)` + `go`**:

```go
p.wg.Go(func() { p.run(w) })     // 正确: Add 与启动不可分割
```

这不是风格问题。L2 让 `addWorker` 从"仅 `Start` 时调用一次"变成**运行期并发调用**,
于是 `p.wg.Add(1)` 与 `go p.run(w)` 之间的窗口会被激活: 若该窗口内发生 `Stop()` 的
`p.wg.Wait()`, 将 panic (`sync: WaitGroup misuse: Add called concurrently with Wait`)。
现状 (`worker_pool.go:83-88`) 的三段式之所以安全, 恰恰因为 `Add` 只在 `Start` 里发生、
而 `Start` 不会与 `Stop` 并发 -- **这个安全性是 L2 单方面破坏的**。
`sync.WaitGroup.Go` (Go 1.25) 是唯一正确写法。详见 12.3。

### 11.3.3 伸缩触发: 基于队列水位

新增唯一判据函数:

```go
// shouldScale 依据队列水位决定扩或缩。
//
// 判据刻意只用队列水位, 不用 handler 耗时或错误率:
//   - 耗时需要每个 task 单独计时, 热路径上多两个 time.Now() 调用
//   - 错误率是结果不是原因, 等它升高已经晚了
// 水位是领先指标: 队列堆积意味着 worker 消费速度跟不上生产速度。
func (p *ScalablePool) shouldScale() int {
    q, capacity := len(p.queue), cap(p.queue)
    if capacity == 0 {
        return 0
    }
    n := p.nextWorker()   // 先看还能不能再加
    switch {
    case float64(q)/float64(capacity) >= p.scaleUpRatio && n < int(p.maxWorker.Load()):
        return +1   // 扩容
    case float64(q)/float64(capacity) <= p.scaleDownRatio && n > int(p.minWorker.Load()):
        return -1   // 收缩
    }
    return 0
}
```

判据用 `nextWorker()` 做上界预检, 避免 `shouldScale` 返回 +1 后 `addWorker` 才发现自己已到上限。

### 11.3.4 何时检查: 不引入额外 goroutine

不新增伸缩 ticker goroutine。检查点挂在已有事件上:

1. **`Submit` 内, 每 N 次触发一次**。`Submit` 是热路径, 不能每次都算, 用原子计数器取模:

```go
func (p *ScalablePool) Submit(...) error {
    // ... 现有入队逻辑不变 ...
    // 每 1024 次提交评估一次伸缩。取模是无锁的, 不给热路径加锁。
    if p.submitTick.Add(1)%scaleCheckInterval == 0 {
        p.maybeScale()
    }
    return nil
}

const scaleCheckInterval = 1024
```

2. **worker 完成一个任务后**同样可以触发, 但这会加在 `handle` 之后的热路径上, 收益不抵成本, **不做**。

取 1024 是权衡: 太小则 `len/cap` 抖动导致频繁伸缩 (加缩 worker 本身有 goroutine 创建成本), 太大则反应滞后。1024 次提交在 10 万 msg/s 下约 10ms 一次, 足够跟上突发。

### 11.3.5 收缩的额外约束: 不能缩到影响吞吐

单纯按水位收缩有个陷阱: 突发结束后水位回落, 收缩到 min; 下一个突发又要重新扩容, 而 **goroutine 创建有成本**, 频繁伸缩会制造抖动。

因此收缩加两个条件:

- 连续 `scaleDownHold` 次检查都低于下界才缩 (默认 3, 即约 3000 次提交)
- 收缩步长为 1, 不一次性砍掉一半

```go
// 收缩比扩容保守得多: 扩容是"补不足", 误判的代价是多一个 goroutine;
// 收缩是"减开销", 误判的代价是把活推给剩余 worker 导致丢弃率上升。
// 所以收缩要求连续低于阈值, 扩容只需一次。
```

### 11.3.6 修正 worker 数下界

`minWorker` 默认取 `runtime.GOMAXPROCS(0)`, 而非 1。理由: worker 全是 CPU 密集时, 少于核心数就是纯浪费调度; 但若 handler 是 IO 密集 (调下游 RPC), 用户应通过 `WithWorkers` 显式调大。**默认值不应替用户猜 IO 密集场景。**

**Go 1.26 补充**: 容器感知 GOMAXPROCS 默认启用后, `GOMAXPROCS(0)` 在容器里返回的是
**cgroup CPU 配额**而非宿主机核数, 上述论证在容器环境下反而更成立。**但这引出一个
必须先确认的问题**: 11.1.2 的压测数据全部来自 `120.76.44.233`, 若该机是容器,
此前 `GOMAXPROCS` 返回的可能不是 2, 意味着那批数据需在确认实际值后重采。
**L2 落地前必须核实该项, 否则 11.1.2 只能作为"机制正确"的证据, 不能作为定量依据。**
见 12.2。

---

### 11.4 API 设计

新增选项, 全部位于 `tcp` 包的 Option 链 (与现有 `WithWorkers` 同一风格):

```go
// WithElasticPool 开启 worker 弹性伸缩。
//
// 伸缩判据是队列水位 (见 ScalablePool.shouldScale)。默认关闭,
// 因为它引入了一个会自主增减 goroutine 的组件, 应当由调用方显式选择。
func WithElasticPool(min, max int) Option

// WithScaleRatio 设置伸缩水位阈值。
// 高阈值 (0.8) 更保守: 队列要堆得比较满才扩容, 但不易抖动。
// 低阈值 (0.2) 更激进: 轻微堆积就扩容, 响应快但可能过度伸缩。
// 两者需满足 scaleDown < scaleUp。
func WithScaleRatio(up, down float64) Option
```

对应 11.3.3 的 `ScalablePool` 需要新增字段:

```go
scaleUpRatio   float64  // 默认 0.75
scaleDownRatio float64  // 默认 0.25
downStreak     atomic.Int32  // 连续低于下界的次数
```

**`WorkerPool` 与 `ScalablePool` 的关系**: 不让 `ScalablePool` 继承 `WorkerPool`。继承会让 `handle` / `Submit` 的 `submitMu` 语义被子类重写, 是脆弱基类。两者共享一个未导出的 `poolCore` (含 queue / handler / policy / submitMu / done / handle), 各自组合。

---

### 11.5 必须保持的现有语义

伸缩不能破坏已验证的行为, 以下每一条都有 11.1.1 的实测支撑:

| 语义 | 出处 | 伸缩后的要求 |
|---|---|---|
| `Stop` 等所有 in-flight Submit | worker_pool.go:153, 实测 500ms | `Stop` 之后不得再有 worker 被 `addWorker` 拉起。`addWorker` 需先检查 `closed` |
| `Stop` 排空队列 | worker_pool.go:157-164 | 收缩的 worker **不得**参与最终排空, 否则会与仍活跃的 worker 重复消费 |
| `handle` 的两个早退分支 | worker_pool.go:168 | `t.Session == nil` 或 `handler == nil` 时直接 return。**若引入 Packet 引用计数 (M6), Release 必须在 defer 里**, 否则这些早退路径会静默泄漏 slab |
| `PolicyBlock` 阻塞 Submit | 实测确认 | 伸缩**不得**在 `submitMu.RLock` 内执行 `maybeScale`。`addWorker` 会启动 goroutine, 在读锁内做这件事会延长 Submit 的持锁时间, 放大阻塞 |
| `PolicyBlock` 的 session 逃逸阀 | worker_pool.go:138 | 保持不变 |
| 队列深度全 server 共享 | 实测确认 | **弹性伸缩不解决跨 session 饿死问题**。一个噪声 session 仍可耗尽整个队列。若要解决需按 session 分队列, 那是分片方案 (v2 已否决), 不在 L2 范围 |

最后一条必须明说: **L2 解决 "整体算力不足", 不解决 "单个 session 抢占全部算力"**。前者靠扩容, 后者靠隔离, 两者是不同问题。

---

### 11.6 一处顺带发现的问题 (与伸缩无关, 独立记录)

`tcp/server.go:54-60`:

```go
s.pool = shared.NewWorkerPool(...)
s.pool.Start(s.opts.WorkerCount)      // :55  先启动 worker
ln, err := net.Listen("tcp", s.opts.Addr)   // :56  后监听
if err != nil {
    s.pool.Stop()                // :59  失败才 Stop
```

worker 在 `net.Listen` **之前**启动。若 listen 成功但后续步骤失败并重试 `Start`, `s.pool` 会被 `NewWorkerPool` 重新赋值, 而旧的 pool 的 worker goroutine 仍在运行且**永远等不到 `Stop`** (没有任何句柄指向它了)。

这不是伸缩引入的, 是既有问题, 但伸缩会放大它 -- 每个泄漏的 pool 都带着一整组 worker。修复方向: `Start` 开头先 `Stop` 旧 pool, 或把 `NewWorkerPool` 移到 listen 成功之后。

**该泄漏可被工具自动检出。** Go 1.26 引入实验性 goroutine 泄漏 profile
(`GOEXPERIMENT=goroutineleakprofile`, 计划 1.27 默认启用), 检测"阻塞在不可达并发原语上"
的 goroutine -- 旧 pool 的 worker 正是永久阻塞在 `<-p.queue` 这个 channel 上, 属于该
profile 的目标形态。建议在 CI 加一步, 零额外代码成本:

```
GOEXPERIMENT=goroutineleakprofile go test -race ./internal/transport/tcp/
```

详见 12.4。**工具价值高于修复本身**: 它把一类"只能靠人工 review 读代码发现"的缺陷
变成 CI 常驻守卫。

---

### 11.7 测试计划

新增 `pool_scale_test.go`, 沿用 11.1.1 的 park 手法 (不用网络, 不依赖时序抖动):

| 测试 | 断言 |
|---|---|
| `TestScaleUpOnBacklog` | 灌满队列触发 `maybeScale`, 活跃 worker 数按 min->max 增长, 不超过 max |
| `TestScaleDownRequiresStreak` | 水位低但连续次数不足时**不缩**; 达到 `scaleDownHold` 后才缩 |
| `TestScaleDownFloor` | 缩到 `minWorker` 后不再缩 |
| `TestNoScaleAfterClose` | `Stop` 后调用 `maybeScale` 不拉起任何 worker |
| `TestScaleDoesNotHoldSubmitLock` | 伸缩期间 `Submit` 不被额外阻塞 (验证 `maybeScale` 在 `RLock` 外) |
| `TestRetireWorkerDrainsNothing` | 被退休的 worker 不消费队列 (避免与其他 worker 重复处理) |
| `TestScaleUnderRace` | `-race` 下并发 Submit + 伸缩 10000 次, 无竞态 |

**时序类测试应用 `testing/synctest`(Go 1.25) 而非真实时间。**
`TestScaleDownRequiresStreak` 依赖 `scaleCheckInterval = 1024` 次提交的累积,
若 CI 机器负载高, goroutine 调度抖动会让时序假设失效。`synctest.Test` 提供 bubble 内的
假时钟与确定性调度, 可消除该脆弱性。

**已实测 (`12.9.2`): `synctest` 与 `-race` 完全兼容, `-race -count=3` 全绿。**
因此所有伸缩测试统一放进 `synctest.Test` bubble, 即可同时获得确定性与竞态检测,
**不需要分轮跑**。

**必须知道的语义陷阱 (实测踩到)**: bubble 内的假时钟把**并发 sleep 视为并行**。
3 个 goroutine 各 `Sleep(1s)` 只推进假时钟 1s, 而串行 3 次推进 3s。
若测试要断言"经过 N 个检查周期", 必须写**串行**等待 -- 写成并发则断言值会差 N 倍。

---

### 11.8 实施位置

**L2 依赖 M1 (kernel.ConnState) 吗? 不依赖。** `ScalablePool` 只用 `core.Handler` / `core.Session` / `core.Message`, 全是既有稳定契约。

因此可以**独立于 M0-M9 主序列执行**, 建议编号:

```
L2-a  poolCore 抽取 + ScalablePool 骨架 (纯新增, 不改WorkerPool)
L2-b  workerRef 身份 + run() 退休分支
L2-c  shouldScale + Submit 内取模触发
L2-d  伸缩行为测试 + race 检测
L2-e  tcp.Option 接线 (WithElasticPool / WithScaleRatio)
```

L2-a 到 L2-d 全部是纯新增, 不修改任何现有文件, 符合 v2 7 的 "任何提交保持 CI 绿灯" 原则。L2-e 改 `tcp/options.go` 一个文件。

**可以与 M1/M2/M3 并行。**

---

### 11.9 预期效果与局限

**预期**: 在 handler 耗时波动时, 有效吞吐天花板从固定的 `worker 数 / handler 耗时` 变为 `maxWorker / handler 耗时`。按 11.1.2 的数据, 4 worker 配 1ms handler 天花板是 4000 msg/s; 若 max 设为 16 则变为 16000 msg/s, 且低谷时自动回落。

**局限, 必须承认**:

1. **不解决跨 session 饿死**。见 11.5 最后一行。
2. **不解决 PolicyBlock 的反压风险**。伸缩让池不容易满, 但一旦满了, 阻塞读 goroutine 的行为不变 (11.1.1 已实测)。缓解此风险需改 Submit 的调用方式 (如在独立 goroutine 里 Submit), 那是 M5 的议题。
3. **扩容有延迟**。取模触发 + goroutine 创建, 从突发到 worker 就绪有毫秒级延迟。突发持续时间短于该延迟时, 伸缩帮不上忙。
4. **原压测机只有 2 vCPU (1 物理核 + HT)**。真实效果需在多核机器复测。本节数据只能证明机制正确, 不能作为性能上限的定量依据。**已解决**: 见 12.9.1, 另有 2 台 8 核机可用。
5. **参数需要按业务调**。`up/down` 阈值与 `min/max` 没有通用最优值, 11.4 的默认值只保证不会抖动, 不保证最优。

---

## 12. Go 语言与工具链特性复核 (Go 1.26.5 实证)

本节不是"新特性清单", 而是拿v2 的每条设计决策回到 Go 1.26.5 的实际能力里重新对账。
**核实方式**: 在 `go 1.26.5` 工具链上逐项编译/运行验证, 并 grep 全仓使用现状。
凡是"文档假设成立"的, 标注为确认;凡是"文档遗漏了新能力"的, 给出修订。

### 12.1 前提修正: Green Tea GC 已是默认, 10.3 的结论需要加强而非改变

**核实**: Go 1.25 以 `GOEXPERIMENT=greenteagc` 实验引入, **Go 1.26 起默认启用**
(`GOEXPERIMENT=nogreenteagc` 可回退, 官方计划 1.27 移除该开关)。
设计从"逐对象扫描"改为**页级 worklist + 页内逐对象**, 官方数据 GC 开销降 10-40%,
在 Intel Ice Lake / AMD Zen 4 及更新的 amd64 上借向量指令再降约 10%。
代价是**基线 RSS 上升 8-15%**。

这对 10.3 / 10.4 有两处直接影响:

| 项 | v2 原表述 | Go 1.26 事实 | 修订 |
|---|---|---|---|
| 10.3 "库不调 GC 参数" | 正确 | 仍正确, 且理由更硬 | **结论不变, 理由升级**: 现在有了一个官方默认的、明显更好的 GC, 库更无权替宿主在 `GOGC` / `GOMEMLIMIT` / `GOEXPERIMENT` 之间做选择。Green Tea 的收益是全局的, 局部调参只会破坏它 |
| 10.4 "常驻内存放大" | 判据是 `HeapInuse<连接数 x ReadChunkSize x 1.2` | **判据需加基线项** | 该断言必须在 **Green Tea 已启用的 Go 1.26 下**测。因为 RSS 基线本身抬高 8-15%, 若用旧GC 采的数据设阈值, 会在新工具链上误报 |

**新增一条实测项**: Green Tea 偏好"堆结构规整、同尺寸对象密集"的场景
(官方原话: 扫描单页 2% 即可产生收益)。本项目的 Chunk slab 恰好是**固定尺寸对象密集**,
属于 Green Tea 的理想负载。**这是 v2 之前没有意识到的正向量**:
3 的 Chunk slab 设计与 Green Tea 的扫描假设同向, 两者叠加而非互斥。

`grep -rn "SetGCPercent|SetMemoryLimit|GOGC|debug\." internal/` 结果为空 --
**核实: 库代码当前确实一个GC 参数都没碰**, 10.3 第1 条是既成事实, 不是待实施项。

### 12.2 修订: 容器感知 GOMAXPROCS 改变了 11.3.6 的默认值论证

**核实**: Go 1.25 起容器感知 GOMAXPROCS 默认启用, 运行时读 cgroup CPU limit。
**但**: 一旦手动设置 `GOMAXPROCS` 环境变量或调用 `runtime.GOMAXPROCS`,
该行为自动停用 (可用 `GODEBUG=containermaxprocs=0` 显式关)。

这直接命中 11.3.6 的 `minWorker` 默认值设计。原文写:

> `minWorker` 默认取 `runtime.GOMAXPROCS(0)`, 而非 1。

在Go 1.25+ 容器环境下, `GOMAXPROCS(0)` **已经是 cgroup 感知的真核数**,
不需要用户干预。原文"少于核心数就是纯浪费调度"的论证**在容器里反而更成立了**
(因为拿到的是真实配额而非宿主机核数)。

**但这引出一个v2 未处理的矛盾**: 11.1.2 的压测数据全部来自云主机
`120.76.44.233` (2 核 / 1.6GB)。若该机是容器, `GOMAXPROCS` 此前可能返回的是
**宿主机核数而非 2**; 启用容器感知后返回值会变化, **意味着 11.1.2 的基线数据
需要在确认GOMAXPROCS 实际值后重采**。

已核实: `scripts/workerpool_server.go:178` 与 `workerpool_baseline.go:95`
都打印了 `GOMAXPROCS`, 但**当时只作为环境信息记录, 没有进入结论**。
**修订: L2 落地前必须记录该值, 且若与物理核数不符, 11.1.2 全部数据降级为"待复测"。**

**给库代码的一条硬约束** (新增, 与 8 一致):
> **库代码不得调用 `runtime.GOMAXPROCS` 或设置 `GOMAXPROCS` 环境变量。**
> 理由比 8 的 GC 参数更硬: 手动设置会**直接关闭**容器感知, 在 K8s 环境造成 CPU 节流。
> shark-socket 作为库, 无权替宿主做这个决定。

### 12.3 确认: `sync.WaitGroup.Go` 让 L2 的 worker 退休更简洁

**核实**: `sync.WaitGroup.Go(f)` 于 Go 1.25 加入, 本机Go 1.26.5 编译通过并运行正确。

全仓现状: 21 处 `sync.WaitGroup`, 全部是 `Add(1)` / `go func(){ defer Done() }` 三段式。
最典型是 `shared/worker_pool.go:83-88`:

```go
for i := 0; i < workers; i++ {
    p.wg.Add(1)
    go p.run()
}
```

`wg.Go` 消除三段式, 且**消除 `Add` 与 `go` 之间的窗口**。这对 L2 不是风格问题而是正确性问题:
L2 的 `addWorker` 会在运行期并发调用, `p.wg.Add(1)` 与 `go p.run(w)` 之间若发生
`Stop()`的 `p.wg.Wait()`, 就会 panic (`Add` called concurrently with Wait)。
`wg.Go` 内部保证 Add 与启动不可分割。

**修订 L2 11.3.2 的 `run()` 设计**:

```go
// 现状与修订
p.wg.Add(1)                  // 现状
go p.run(w)          ->  p.wg.Go(func() { p.run(w) })   // 修订, 消除 Add/Wait 竞态窗口

func (p *ScalablePool) run(w *workerRef) {
    // 修订: defer p.wg.Done() 一并删除, 由 wg.Go 承担
    for { select { ... } }
}
```

这不是可选的整洁化。**L2 让 `addWorker` 从"仅Start 时调用一次"变成"运行期并发调用",
原本不可见的 Add/Wait 竞态窗口会被激活。** `wg.Go` 是这里唯一正确的写法。

### 12.4 新增能力: goroutine 泄漏检测可作为 11.6 的守卫

**核实**: Go 1.26 引入实验性 goroutine 泄漏 profile, `GOEXPERIMENT=goroutineleakprofile`
启用 (本机验证可正常构建运行), 官方计划 **1.27 默认启用**。它检测
"因阻塞在不可达的并发原语 (channel / mutex) 而永远无法唤醒" 的 goroutine。

11.6 记录的既有问题正是这一类:

```go
s.pool = shared.NewWorkerPool(...)
s.pool.Start(s.opts.WorkerCount)      // :55  worker 在此启动
ln, err := net.Listen("tcp", s.opts.Addr)   // :56  若后续失败并重试 Start, 旧 pool 的 worker 永久阻塞在 <-p.queue
```

旧 pool 的 worker 阻塞在 `<-p.queue` (channel), 而那个 queue 再也不会有写入也不会被关闭 --
**这正是该profile 的目标形态**。

**修订 11.6**: 该问题从"已记录待修"升级为"**有工具可自动检出**"。
建议在 CI 加一步:

```
GOEXPERIMENT=goroutineleakprofile go test -race ./internal/transport/tcp/
```

零额外代码成本。**这条比修复本身更有价值**: 它把一类"人工 review 靠读代码发现"的缺陷
变成 CI 常驻守卫, 且1.27 起将成为默认行为。

### 12.5 修订: `synctest` 让 11.7 的伸缩测试不必依赖真实时间

**核实**: `testing/synctest` 于 Go 1.25 转正, 提供 bubble 内的**假时钟**与确定性调度。
本机 Go 1.26.5 验证 `synctest.Test` 可用。全仓当前**零使用**。

11.7 的伸缩测试设计有一个未被承认的脆弱点: `TestScaleDownRequiresStreak` 需要
"连续 N 次检查", 但 `scaleCheckInterval = 1024` 次提交与真实时间耦合。
若 CI 机器负载高, goroutine 调度抖动会让测试时序假设失效。

`synctest` 允许测试在假时钟下确定性等待:

```go
func TestScaleDownRequiresStreak(t *testing.T) {
    synctest.Test(t, func(t *testing.T) {
        // 提交 > 1024*scaleDownHold 次, 每次 Submit 后 synctest.Wait()
        // 时间推进与goroutine 调度完全确定, 不受 CI 负载影响
    })
}
```

**注意边界**: 本机 `-race` 需 cgo 且 gcc 不可用, **未验证 `synctest` 与 `-race` 同跑的兼容性**。
云端 (Linux + gcc) 需补测。若二者不兼容, 则伸缩测试分两轮: `-race` 跑无时间依赖的
`TestNoScaleAfterClose` / `TestRetireWorkerDrainsNothing`, `synctest` 单独跑时序类。

### 12.6 确认: 现有零拷贝设计已与 Go 分配器假设一致, 无需修订

1.2 判定"`append([]byte(nil), payload...)` 是分配热点", 结论是"消除拷贝而非池化单次分配"
-- 依据是 per-P mcache + span 池化使小对象分配约 20ns, 因此不需要 Java 式的
`BufferPagePool`。**本轮复核未发现这个前提有任何问题**, 且 Go 1.26 的
**slice backing store 栈分配优化**(编译器可在更多情形下把 slice 底层数组放栈上)
进一步强化了这个方向: 栈分配路径连 mcache 都不经过。

3.6 的"`Send` 每帧分配 `MaxHeader + len(payload)` 单块"因此仍是正确的建模:
单次分配已经足够便宜, 需要消除的是**重复的分配次数**, 不是"分配本身"。

**一条反向提醒**: 不要因为"Go 分配便宜"就在零拷贝路径上放松 Chunk 归还的严格性。
分配便宜与 **slab 泄漏** 是两件事-- 10.4 已列, 复核确认。

### 12.7 确认: `new(expr)` 对 Packet 构造是纯粹的语法简化, 不改变设计

**核实**: Go 1.26 的 `new(expr)` 允许 `new(Packet{Data: ..., Raw: ...})` 直接构造非零值。
本机验证通过。

v2 3.2 的 Packet 有 `chunk any` 未导出字段且需 `Retain/Release` 生命周期管理,
**`new(Packet{...})` 在这里并不适用** -- 因为 Packet 不该被随意按值构造。
真正能用上的地方是 3.2 里"overflow 路径"和 4.2 的"大块单独分配"这类
一次性内部构造。收益仅是省两行临变量, **不构成设计修订, 仅记录为实现时的便利**。

### 12.8 复核结论汇总: 一处前提修正, 三处修订, 五处确认

| 编号 | 性质 | 内容 | 涉及章节 |
|---|---|---|---|
| G-1 | **前提修正** | Green Tea GC 自Go 1.26 默认启用, GC CPU 降 10-40% 但 RSS 升 8-15%。10.3 结论不变、理由加强; **10.4 的 `HeapInuse` 断言必须在 Green Tea 下重采基线** | 10.3, 10.4 |
| G-2 | **前提修正** | 容器感知 GOMAXPROCS 默认启用, 但手动设置会关闭它。**库代码新增硬约束: 不得调用 `runtime.GOMAXPROCS`**。且 11.1.2 的压测基线需确认该机 `GOMAXPROCS` 实际值 | 8, 11.1.2, 11.3.6 |
| G-3 | **修订** | `wg.Go` 消除 `Add`/`go` 之间的竞态窗口。L2 的 `addWorker` 并发化会**激活**这个原本不可见的窗口, 因此 `wg.Go` 是必需而非可选 | 11.3.2 |
| G-4 | **修订** | goroutine 泄漏 profile (1.26 实验 / 1.27 默认) 可直接检出 11.6 的既有泄漏, 建议进 CI。**工具价值高于修复本身** | 11.6 |
| G-5 | **修订** | `synctest` 让伸缩测试摆脱真实时间依赖。**边界: `-race` 与 `synctest` 同跑兼容性未验证**, 需云端补测后决定测试分轮策略 | 11.7 |
| G-6 | 确认 | 零拷贝"消除拷贝而非池化分配"的判断成立; Go 1.26 栈分配优化进一步强化 | 1.2, 3.6 |
| G-7 | 确认 | 库代码当前确实未碰任何 GC 参数 (10.3 第 1 条是既成事实) | 10.3 |
| G-8 | 确认 | Chunk slab 的"固定尺寸对象密集"形态恰好是 Green Tea 的理想负载, 与 3 是叠加关系 | 3.2, 10.4 |
| G-9 | 确认 | `new(expr)` 仅是实现便利, 不改变任何设计 | 3.2 |
| G-10 | 确认 | 不可因"分配便宜"放松 Chunk 归还严格性; 分配成本与 slab 泄漏是两个问题 | 10.4 |

**待实测项 (阻塞 L2 落地) -- 已于同日全部收口, 见 12.9**:
1. ~~云端 `GOMAXPROCS` 实际值~~ -> **实测 2, 与物理核数一致, 11.1.2 数据有效, 无需重采**
2. ~~`synctest` 与 `-race` 同跑兼容性~~ -> **完全兼容, `-race -count=3` 全绿, 伸缩测试无需分轮**
3. Green Tea 下的 `BenchmarkTCPEcho_10kIdleConnections` 基线 -> **仍未做** (该项需先实现基准, 见 M0)

### 12.9 12.8 三个待实测项的收口结果 (2026-10-16 云端实测)

三项全部完成。核实方式: SSH 上机, `root@120.76.44.233` (密钥免密), 实测运行。

#### 12.9.1 测试机台账 (三台全部实测)

| 项 | cloud1 `120.76.44.233` | cloud2 `47.120.44.128` | cloud3 `8.130.190.141` |
|---|---|---|---|
| OS | Ubuntu (未取版本) | Ubuntu 26.04.1 LTS | Ubuntu 26.04.1 LTS |
| **vCPU** | 2 | **8** | **8** |
| **物理核** | **1 core + 2 HT** [注意] | 4 core + 2 HT | 4 core + 2 HT |
| RAM | 1.6 GB | 15 GB | 15 GB |
| 虚拟化 | kvm (裸虚拟机) | kvm | kvm |
| 容器 | **否** (`/proc/1/cgroup` = `0::/init.scope`, 无 `/.dockerenv`, 无 `cpu.max`) | 同左 | 同左 |
| Go | 1.26.9 | **1.26.5** | 1.26.1 |
| gcc | 有 | 有 | 有 |
| `GOMAXPROCS(0)` | **2** | **8** | **8** |
| env `GOMAXPROCS` | 未设置 | 未设置 | 未设置 |

**三个直接结论**:

1. **11.1.2 的数据有效, 无需重采。** `GOMAXPROCS(0)=2` 与 `nproc=2` 一致,
   环境变量未设置因此容器感知未被关闭。12.8 的 G-2 担忧解除。
2. **但 "2 核" 的表述不准确 -- 那是 1 物理核 + 2 超线程。** 这不使数据失效
   (超线程对 IO 密集型 handler 仍提供真实指令级并行), 但收紧了一条推论:
   11.9 局限 4 说的 "4 worker / 1ms handler 天花板 = 4000 msg/s" 在 HT 争用下
   **会低于该理论值**, 应视为**乐观上界而非可达上限**。已回填至 11.1.2 与 11.9。
3. **多核验证条件已具备。** cloud2 是最佳目标 (Go 1.26.5 与本地基线版本一致, 8 vCPU)。
   cloud3 的 Go 1.26.1 略旧, 且 12.1 的 Green Tea 行为需统一版本才可比, **不建议用于
   与 11.1.2 的对照**。

**库代码新增约束已在 8 定论, 此处给出实测支撑**: 三台机器的 cgroup v1/v2 **均无 CPU 配额**
(`cpu.max` 与 `cpu.cfs_quota_us` 都不存在), 因此若库代码调用 `runtime.GOMAXPROCS` 强设,
在这些机器上**不会立刻暴露问题, 但会在真实 K8s 环境静默关闭容器感知**。
这正是"库不该替宿主决定"的现实理由 -- 错误的默认值在测试环境往往看不出来。

#### 12.9.2 `synctest` 与 `-race` 同跑: 完全兼容 (12.8 G-5 收口)

实测于 cloud1 (Go 1.26.9 + gcc), 3 项测试:

| 测试 | 结果 |
|---|---|
| `TestFakeClockSerial` (串行 3x 1s 睡眠, 假时钟应推进 3s, 真实 0s) | PASS |
| `TestFakeClockConcurrent` (3 goroutine 各 1s, 假时钟推进 1s -- 并发即并行) | PASS |
| `TestConcurrentShared` (bubble 内 8 goroutine 加锁累加) | PASS |

`go test -count=1` / `-race -count=1` / `-race -count=3` **全部 ok, 无兼容性报错**。

**结论: 11.7 的伸缩测试可直接在 `synctest` + `-race` 下同跑, 不需要分轮。**
这消除了 11.7 表格中 `TestScaleUnderRace` 与时序类测试的割裂 --
**所有伸缩测试统一放进 `synctest.Test` bubble, 即可同时获得确定性与竞态检测。**

**一处必须知道的语义细节** (实测踩到): bubble 内的假时钟把**并发 sleep 视为并行**。
3 个 goroutine 各 `Sleep(1s)` 只推进假时钟 1s, 而串行 3 次推进 3s。
若测试要断言"经过 N 个检查周期", 必须写**串行**等待; 写成并发则断言值会差 N 倍。
这是 `synctest` 最容易用错的一处, 已在 11.7 记录。

#### 12.9.3 Green Tea GC 默认启用: 权威确认 (12.8 G-1 收口)

`gctrace` 输出里不含 GC 名称, 因此改用两条权威证据:

1. **`GOEXPERIMENT=nogreenteagc go run` 的版本串带后缀**:
   - 默认 -> `runtime.Version() = go1.26.9` (**无后缀**, 即 greenteagc 实验已默认开启)
   - 回退 -> `runtime.Version() = go1.26.9-X:nogreenteagc` (后缀出现)

   该后缀机制本身就证明了默认值: 只有显式回退才打 `-X:` 标记。

2. **GOROOT 源码的 build tag 二分** (最权威, 因为它就是编译期的真相):

   ```
   $GOROOT/src/runtime/mgcmark_greenteagc.go:35://go:build goexperiment.greenteagc
   $GOROOT/src/runtime/mgcmark_nogreenteagc.go:5://go:build !goexperiment.greenteagc
   ```

   两个互斥文件按 build tag 二选一, `mgcmark_nogreenteagc.go` 全文仅 5 行(build tag + 包声明),
   即默认分支只剩壳, 实现全在 `greenteagc` 侧。

**因此 10.3 / 10.4 / 8 的三处修订全部成立且无需再验证**:
- Green Tea 已在三台机器默认启用 (10.3 "库不调 GC 参数" 理由更硬)
- 10.4 的 `HeapInuse` 断言必须在新基线下重采阈值
- Chunk slab 的"定尺寸对象密集"形态与 Green Tea 的页级扫描假设同向叠加

**剩余**: Green Tea 下的 `BenchmarkTCPEcho_10kIdleConnections` 实际基线仍未测 --
该项依赖 M0 先实现该基准, 不属于本轮复核范围。

---

## 13. 8 核复测: L2 立论被推翻 (2026-10-16, cloud2)

本节是多核复测的结果, 也是本项目迄今**最重要的一次自我推翻**。
写作方式遵循既有纪律: 先摆数据, 再下结论, 明确标注哪些是事实、哪些是推断。

### 13.1 环境与方法

| 项 | 值 |
|---|---|
| 机器 | cloud2 `47.120.44.128`, 8 vCPU (4 core + 2 HT) / 15 GB / Ubuntu 26.04.1 |
| Go | 1.26.5 (与本地基线同版本), gcc 15.2.0 可用 |
| GOMAXPROCS | 8 (实测, 非容器, 无 cgroup 配额) |
| 部署 | 服务端与客户端**同机双进程** (8 核足够分开, 见 13.5 的局限说明) |
| 服务端隔离 | `systemd-run` 启动为独立 systemd 单元, 与 SSH 会话彻底脱离 |
| 配置 | queue=512 / PolicyDrop / echo=true / pipeline 模式 |

方法与 cloud1 完全一致, 只有 worker 数变化。

### 13.2 决定性数据: worker 1 -> 16 对照矩阵

`handler-delay=1ms`, 客户端 pipeline 全速灌, `drop%` 取服务端自报
`tcp_task_queue_full_total` 增量:

| conns | w=1 drop | w=4 drop | w=16 drop |
|---|---|---|---|
| 1 | 98.837% | 98.423% | 99.537% |
| 10 | 98.335% | 98.290% | 98.481% |
| 50 | 91.875% | 91.659% | 91.561% |
| 100 | 85.434% | 83.095% | 84.926% |
| 200 | 71.965% | 73.196% | 76.130% |
| 500 | 78.313% | 77.564% | 76.870% |
| 1000 | 77.888% | 81.793% | 75.460% |

吞吐 (msg/s, 被回显成功数除以时长) 同样不随 worker 数变化:

| conns | w=1 | w=4 | w=16 |
|---|---|---|---|
| 1 | 118 | 120 | 117 |
| 100 | 6 | 17 | 14 |
| 1000 | 12 | 7 | 19 |

**worker 数增加 16 倍, drop rate 与吞吐均在噪声范围内波动。**

### 13.3 这与 11.1.3 的判断直接冲突

11.1.3 声称:

> **丢弃率的决定因素是 `handler 耗时 / worker 数`。**
> 因此 L2 要解决的是 "worker 数固定且运行期不可调"。

若该判断成立, w=16 相对 w=1 应该有约 16 倍的吞吐提升, 或至少 drop rate 显著下降。
**实测两者都没有发生。** 11.1.3 的推论不成立。

### 13.4 为什么会这样: 一个必须承认的方法论缺陷

复核后确认, 差异来自**压测方法本身**, 而不是被测代码:

**pipeline 客户端在同一台机器上成为了瓶颈。** 客户端 8 核全速 `Write`,
与服务端 4 worker 争抢 CPU; 且客户端的 `sent` 计数远大于服务端处理的量
(单连接场景: sent=1,024,513 而 recv=955)。在这种条件下, 服务端 worker 数
不是限制因素 -- **能拿到多少 CPU 才是**。

**因此前两轮压测 (含 cloud1 的全部数据) 测的都不是 "worker 够不够",
而是 "pipeline 客户端能灌多快"。** 11.1.3 把这个测量结果误读为前者。

补充证据: 用一个限速客户端 (`scripts/rateclient/main.go`, 新增) 在
2000-8000 msg/s 目标速率下测试 w=1/4/8/16, `queue_full` **全为 0**。
即在客户端不是瓶颈时, 1 个 worker 就能扛住 2000+ msg/s,
`drop` 完全由客户端速率决定, 与 worker 数无关。

### 13.5 结论: L2 应当暂缓, 不是照做

> **[注意] 本节的暂缓决定已被 14 撤销。**
> 13 的测量方法 (同机双进程 + pipeline 客户端) 存在系统性缺陷,
> 导致"worker 数无关"的结论是**归因错误**而非事实。
> **分离式压测 (客户端限速 + 服务端独占 8 核) 实测: worker 从 1 到 16,
> 吞吐提升 11.96 倍, 近线性。L2 立论成立, 应继续实施。**
> 本节保留作为**方法论教训**: 见 14.7。

**11 的 L2 弹性伸缩设计, 其定量立论不成立, 不应进入实施。**

必须明确区分两件事:

| 命题 | 状态 |
|---|---|
| "worker 数固定且运行期不可调" 是**代码事实** | 成立 (worker_pool.go:82-91 核实无误) |
| "worker 数是丢弃率的决定因素" 是**性能推论** | **已被13.2 推翻** |

前者是结构描述, 无需实测; 后者是性能主张, 需要实测, 而实测反对它。

**一个固定 worker 数的池在 handler 耗时波动时确实不理想, 但本轮没有测出
"把它变成可伸缩能带来多少收益"。** 在收益未测出之前实施L2,
是在为一个未验证的假设写 380 行设计 + 5 个阶段。

**要救活 L2, 必须先做一次合格的压测**, 满足三个条件:

1. **客户端与服务端分离到不同机器** (cloud1 与 cloud2 各跑一边, 用公网或
   同一内网 VPC; 5 Mbps 带宽需核算, 或改用 cloud1 跑客户端 + 本机跑服务端)
2. **客户端限速或至少可测量自己的 CPU 占用**, 使服务端不被抢核
3. **先测出当前固定 worker 数的吞吐天花板**, 再测可伸缩版本, 用差值判断收益

在此之前, **建议先做 M0 (基线固化)**, 把合格压测方法本身固化进 `scripts/`,
再谈 L2。

### 13.6 这轮复测的副产品: 两项真实收获

推翻结论之外, 有两项东西是有价值的:

1. **`scripts/rateclient/main.go` (新增)**: 一个可限速的压测客户端。
   它暴露了 pipeline 客户端的 CPU 争抢问题 -- **这是原压测工具的固有限制**,
   此前一直未被发现, 因为所有测量都在同机进行。
2. **服务端进程管理方式的修正**: `pkill -x` / `pgrep -f` 都会误杀 SSH 会话自身
   (命令行含目标进程名)。唯一可靠方式是 `systemd-run` 启动 + 按监听端口反查 pid。
   这条经验对后续任何云端压测都适用。

### 13.7 未解决: 11.1.2 与 13.2 的连接数曲线为何形状相似

两轮数据都呈现"**连接数增加时 drop rate 反而下降**" (cloud1: 0% -> 63%;
cloud2: 98% -> 77%)。这不是笔误, 但原因未查明。

最可能的解释: 连接数多时, 每个连接的 socket 发送缓冲区成为第一道限流,
客户端实际 `Write` 速率被内核 TCP 层压住, 反而低于单连接时。
**若成立, 则drop rate 主要由客户端到服务端的 TCP 流控决定, 与 worker 数无关 --
与 13.3 的结论一致。**

**这一条是推断, 不是实测结论**, 需要在客户端与服务端分离的条件下验证。
它与 13.5 的建议直接绑定: 不解决分离问题, 后续所有压测都不可信。

---

## 14. 分离式压测: L2 立论重新成立 (2026-10-16, 推翻 13)

13 用同机双进程压测, 得出 "worker 数不是丢弃率的决定因素", 并据此暂缓 L2。
**14 修正那个测量方法后, 得到相反的结论: L2 的立论成立。**

13 的错误不是结论错误, 而是**归因错误** -- 它把"客户端抢CPU"造成的
低吞吐, 误读成了"worker 数无关"。本节用分离式压测排除该干扰项。

### 14.1 拓扑与隔离措施

```
[本机 Windows, 16 核]                    [cloud2 Linux, 8 vCPU]
  rclient (限速, 4000-6000 msg/s)  --SSH隧道-->  wpserver (独占 8 核)
                (127.0.0.1:39000)             (127.0.0.1:28000)
```

| 项 | 值 |
|---|---|
| 客户端 | 本机 `scripts/rateclient` (可限速), 不与服务端共享 CPU |
| 服务端 | cloud2, 独占 8 vCPU, systemd 单元隔离 |
| 链路 | 本机 -> cloud2 公网, RTT 29ms, 0% 丢包 |
| 隧道 | SSH `direct-tcpip` 端口转发 (安全组只放行 18000-19000, 见 14.6) |
| 配置 | queue=512 / PolicyDrop / echo=true / handler-delay=1ms |
| 时长 | 每配置 4s, 客户端目标速率 6000 msg/s (4 连接) |

**关键: 客户端限速, 且速率低于服务端天花板。** 这样服务端不被灌爆,
观察到的吞吐差异只能来自 worker 数。

### 14.2 决定性数据: worker 数扫描

4 连接 / 6000 msg/s / 1ms handler 延迟, 每配置独立重启服务端:

| workers | 客户端发出 | 服务端回显字节 | 回显/发出 | 队列满事件 |
|---|---|---|---|---|
| 1 | 35700 | 20048 | 0.56 | 2279719 |
| 2 | 35700 | 38448 | 1.08 | 2275188 |
| 4 | 35700 | 71912 | **2.01** | 2266822 |
| 8 | 35700 | 145660 | **4.08** | 2248385 |
| 16 | 35700 | 239828 | **6.72** | 2224843 |

**回显量随 worker 数单调上升, 1 -> 16 提升 11.96 倍。**

归一化为每 worker 吞吐 (回显字节 / worker):

| workers | 每 worker 回显字节 | 相对 w=1 的线性度 |
|---|---|---|
| 1 | 20048 | 1.00 |
| 2 | 19224 | 0.96x |
| 4 | 17978 | **0.90x** |
| 8 | 18208 | **0.91x** |
| 16 | 14989 | **0.75x** |

**w=1 到 w=8 几乎完美线性扩展 (0.90-0.96x), w=16 出现明显衰减 (0.75x)。**

衰减原因明确: w=16 已超过 8 vCPU 的核数, worker 转为上下文切换开销。
**这恰好给出 `maxWorker` 默认值的依据: 不应超过 GOMAXPROCS。**

### 14.3 结论: 13 的归因是错的, 11 的立论成立

| | 13 (同机双进程) | 14 (分离式) |
|---|---|---|
| 客户端与服务端 | 同机, 抢 CPU | 物理分离 + 限速 |
| w=1 -> w=16 吞吐变化 | 118 -> 117 msg/s (**无变化**) | 78 -> 234 msg/s (**11.96x**) |
| 结论 | "worker 数不是决定因素" | "worker 数是决定因素, 近线性" |

**13 的测量方法存在系统性问题**: pipeline 客户端全速 `Write` 时,
即使服务端与客户端是不同进程, 客户端的 goroutine 调度、socket 缓冲填充、
以及内核 TCP 发送路径都会成为瓶颈, 使服务端拿不到足够 CPU。
13 的 `sent=1024513 而 recv=955` 正是这个现象的直接证据。

**14 用限速客户端消除了该变量**, 于是 worker 数的影响显现出来:
**1 -> 8 近线性, 16 开始衰减。**

因此:
- **11.1.3 的立论("丢弃率的决定因素是 handler 耗时 / worker 数")成立**
- **13.5 的"L2 暂缓"结论撤销**
- 13 的价值在于: 它暴露了压测方法的缺陷, 而这个缺陷同样是 13 之前所有
  压测(包括 11.1.2 的 cloud1 数据)的共同问题

**但 13 也留下一个仍然有效的发现**: `queue_full` 在所有 worker 配置下
都稳定在 2.2M-2.3M 区间, 说明**在饱和条件下队列深度与 worker 数无关地决定了
丢弃总量** -- 队列只是缓冲区, 不是瓶颈。

### 14.4 对 L2 设计的直接影响

11.3 的设计需要在实测数据基础上调整两处:

1. **`maxWorker` 默认值**: 11.3.6 说 "若 handler 是 IO 密集, 用户应通过
   `WithWorkers` 显式调大"。**实测给出更硬的依据**: w=16 线性度掉到 0.75x,
   超过 GOMAXPROCS 后收益递减。**建议 `maxWorker` 默认值取 `2 x GOMAXPROCS`**,
   允许小幅超订但不鼓励。这个数字现在有实测支撑, 而不只是推理。

2. **扩容的收益上界是可预测的**: 吞吐近似 `min(workers, GOMAXPROCS) x 1000 msg/s`
   (1ms handler)。弹性伸缩能把"固定下限"变成"跟随负载", 但**天花板由核数决定,
   弹性无法突破**。因此 11.3.4 的取模触发间隔(1024 次)在低负载时合理,
   在接近天花板时收益趋零 -- **不建议在高水位持续时反复扩缩**。

11 的其余设计(workerRef 身份、可退休 run()、水位判据、收缩需连续 3 次)
**不受本次数据影响, 继续有效**。

### 14.5 三台机器的定位据此更新

| 机器 | 角色 | 依据 |
|---|---|---|
| cloud1 (2 vCPU / 1 core + HT) | **废弃用于压测** | 物理核仅 1 个, 4 worker 已超订, 无法区分 worker 数影响 |
| cloud2 (8 vCPU / 4 core + HT) | **压测服务端** | 4 物理核, 足够观察 1->8 的线性扩展 |
| 本机 (16 核Windows) | **压测客户端** | 限速发送, 不与服务端争抢 |
| cloud3 (8 vCPU, Go 1.26.1) | 备用 | Go 版本与基线不一致, 仅在需要对照时使用 |

### 14.6 分离式压测的工程要点 (踩过的坑)

这些是本次为分离式付出的实际代价, 应固化为 `scripts/` 的使用说明:

1. **安全组只放行 18000-19000**, 而该段已被用户的服务占用 (pid 34283 `shark`)。
   压测端口 28000 不在安全组内, 因此**必须用 SSH 端口转发**, 不要去改安全组
   或占用已有端口。
2. **服务端绑 127.0.0.1 而非 0.0.0.0**。绑 0.0.0.0 时 `Addr()` 返回 `[::]:port`
   (IPv6 通配), 在部分环境下不可达 -- 这是 11.1 记录的同一个坑, 再次踩到。
3. **metrics 端点有"惰性注册"行为**: 首次连接之前, `/metrics` 返回 HTTP 200
   但**响应体为空**; 且 `tcp_task_queue_full_total` 这类 counter **在被事件
   触碰过之前根本不出现**。因此基线读取必须先跑一次饱和 warmup,
   否则 base 读成 -1, 算出的 drop% 会超过 100%。
4. **不可用客户端 `sent` 作为 drop 率分母**: 限速客户端经隧道时, `sent`
   反映的是本地进程塞进 socket 的量, 与服务端实际拒绝的量无确定比例。
   **以服务端 `queue_full` 增量为权威**。
5. **客户端读协程必须有退出条件**: 只靠 `SetReadDeadline` 超时而不检查
   总体 deadline, 读协程永不退出, 程序hang 在 `WaitGroup.Wait()`。
6. **Windows ticker 精度约 1ms**: 用 ticker 做亚毫秒级限速会静默降速
   (实测目标 5000/s 只跑出 1900/s)。**必须批量发送**: 每 tick 发 N 条,
   使 tick 间隔保持在毫秒量级。

### 14.7 13 与 14 的关系

不是"13 错了所以 14 对", 而是:

- **13 正确识别了测量方法有问题** -- 这是它的贡献
- **13 错在把方法缺陷导致的低吞吐归因为"worker 数无关"**
- **14 用分离式 + 限速排除了该缺陷**, 得到可信数据
- **11 的 L2 立论最终成立**, 13.5 的暂缓决定撤销

**教训**: 压测数据只能支撑它能支撑的结论。同机双进程测量 "X 的影响"
时, 必须先证明负载确实落在 X 上, 否则得到的是"客户端有多快"。

### 14.8 L2-a / L2-b 实施记录 (2026-10-16)

14 确认立论成立后, L2-a 与 L2-b 已实施完毕。

#### 产出与规模

| 文件 | 行数 | 性质 |
|---|---|---|
| `shared/pool_core.go` | 160 | **新增**, 抽出共享状态与行为 |
| `shared/pool_scalable.go` | 212 | **新增**, `ScalablePool` 骨架 + `workerRef` |
| `shared/worker_pool.go` | 205 -> 103 | **重构**, 改为组合 `poolCore` |
| `shared/pool_scale_test.go` | 266 | **新增**, 10 项行为测试 |
| `shared/pool_scale_race_test.go` | 87 | **新增**, 记录 `wg.Go` 论点 |

`WorkerPool` 公开 API 零变更 (`Handler` / `Enabled` / `Start` / `Submit` / `Stop`),
`FullPolicy` 与 `PoolTask` 保持原位, 因此 `tcp/server.go` 与所有传输零改动。

#### 验证结果

| 检查 | 结果 |
|---|---|
| `go build ./...` | 通过 |
| `go vet ./internal/transport/shared/` | 通过 |
| `gofmt -l` | 三个新文件已格式化 |
| `go test ./internal/transport/shared/ -count=3` | 通过 |
| `go test ./...` (全仓) | 通过, 无回归 |
| **云端 `-race -count=3`** (Go 1.26.5 + gcc 15.2) | **通过, 无竞态** |

`WorkerPool` 的既有 10 项测试 (5 项原有 + 5 项饱和测试) 在重构后全部保持绿,
这是"重构不改变行为"的直接证据。

#### 11.3.2 的`wg.Go` 要求已落地

```go
// ScalablePool.addWorker
p.wg.Go(func() { p.run(w) })     // 而非 wg.Add(1); go p.run(w)
```

配套新增 `pool_scale_race_test.go`, 显式记录**为什么WorkerPool 可以用三段式而
ScalablePool 不可以**: `WorkerPool` 的 `Add` 只发生在 `Start`, 而 `Start` 不与
`Stop` 并发; `ScalablePool` 让 worker 在运行期启动, 该前提被破坏。

该测试**刻意不主动触发 panic** (那会让套件天然flaky), 而是断言使 `wg.Go` 路径
安全的两个性质:

1. `beginClose` 之后 `addWorker` 完全惰性 -- 不存在"计数已加而 goroutine 未启动"
   的可能
2. `Stop` 与 `addWorker` 并发时正常终止而非死锁, 且每个被等待的 worker 都已计数

#### 两处实现决策需要说明

**决策 1: `Stop` 时显式关闭每个 worker 的 retire 通道, 而不是让它们走 `done` 分支。**
理由: `done` 分支会先排空队列再退出。若部分 worker 走 `done`(排空)、部分走
`retire`(不排空), 会出现**两类worker 同时消费队列**的情形, 排空语义被破坏。
统一走 retire 后, 只有 `Stop` 结束后的 `drainQueue` 一条排空路径。

**决策 2: `ScalablePool.Submit` 尚不评估伸缩。**
11.3.4 要求的"每 1024 次提交触发一次 `maybeScale`"属**L2-c**, 尚未实施。
当前 `Submit` 只是 `poolCore.submit` 的直通, 这是刻意的: 伸缩决策必须在
`submitMu.RLock` **之外**执行, 而 `poolCore.submit` 的读锁覆盖整个入队过程。
把 `maybeScale` 塞进现有 `Submit` 会延长每个并发 Submit 的持锁时间, 正好放大
已实测的 `PolicyBlock` 阻塞。L2-c 需要在 `poolCore.submit` 返回**之后**再触发。

#### 下一步

- **L2-c**: `shouldScale` + 触发点设计, 必须解决"在 submitMu 之外评估"这个约束
- **L2-d**: 伸缩行为测试 (队列水位上升/下降 -> worker 数相应变化)
- **L2-e**: `tcp.Option` 接线 (`WithElasticPool` / `WithScaleRatio`)

**尚未验证**: L2-c 完成后需在 cloud2 上复跑 14.2 的分离式压测, 对比
"固定 4 worker" 与 "弹性 2-8 worker" 的实际吞吐差异。**那是 L2 收益的最终证明,
本节只证明了机制正确。**

### 14.9 L2-c 实施记录 (2026-10-16)

L2-a / L2-b 完成后继续实施 L2-c (伸缩判据与触发点)。

#### 产出

`shared/pool_scalable.go` 新增 `shouldScale` / `maybeScale` / `retireOneWorker`
与 `submitTick` 计数器, 提交点为:

```go
func (p *ScalablePool) Submit(...) error {
    err := p.poolCore.submit(...)        // submitMu.RLock 覆盖整个入队
    if p.submitTick.Add(1)%scaleCheckInterval == 0 {
        p.maybeScale()                   // 锁已释放, 在锁外评估
    }
    return err
}
```

`pool_scale_behavior_test.go` 新增 6 项测试 (4 项原有 + 2 项修订)。

#### 11.3.4 的约束如何满足

11.3.4 要求"Submit 内每 N 次触发一次", 但同时 11.5 要求
"`maybeScale` 不得在 `submitMu.RLock` 内执行"-- 这两条看似冲突,
因为 `poolCore.submit` 的读锁覆盖整个入队 (含 `PolicyBlock` 阻塞路径)。

解法是**把 Submit 拆成两步**: 先 `poolCore.submit` (内部加解锁),
再在外面评估伸缩。`defer RUnlock` 在 `poolCore.submit` 返回时已执行,
因此 `maybeScale` 运行时锁一定已释放。测试
`TestMaybeScaleDoesNotHoldSubmitLock` 专门断言该性质 (从测试 goroutine
取写锁, 若 Submit 仍持读锁则会阻塞)。

**所有返回路径都 tick, 包括 `ErrWriteQueueFull`** -- 被拒绝的提交
正是最需要扩容的信号, 不能跳过。

#### 测试中踩到并修正的两个设计约束

这两条不是 bug, 是**测试设计时必须知道的池语义**:

1. **收缩要求 `n > minWorker`。** 若 worker 数已在下限, 收缩分支不进,
   落入中间地带并**重置收缩计数**。测试因此必须先把 worker 加到下限之上,
   否则 `downStreak` 永远是 0, 看起来像收缩逻辑失效。
2. **队列容量必须远大于 `scaleCheckInterval`。** 小队列在 submit 循环内
   即被填满, occupancy 恒为 1.0, 走的是扩容分支而非收缩。
   收缩类测试用 16384 容量: 3 次 tick 时最高 occupancy = 3068/16384
   = 0.187, 仍低于 0.25 阈值。

#### 提交序列

按"每步可独立验证"的原则分 5 个提交推送 (gitee + github 同步):

| commit | 内容 |
|---|---|
| `d486e9bd7` | `refactor(shared)`: 抽取 poolCore |
| `b99494495` | `feat(shared)`: ScalablePool + workerRef |
| `7b2bd1346` | `test(shared)`: 伸缩行为 / 竞态 / 确定性测试 |
| `05020b71a` | `feat(scripts)`: 可隔离服务端的压测工具 |
| `629e6ca61` | `docs(design)`: 设计文档与实测数据 |

#### 验证

| 检查 | 结果 |
|---|---|
| `go build ./...` | 通过 |
| `go vet ./...` | 通过 |
| `gofmt -l` | 干净 |
| `go test ./internal/transport/shared/ -count=3` | 通过 |
| `go test ./...` 全仓 | 通过, 无回归 |
| **云端 `-race -count=3`** (Go 1.26.5 + gcc 15.2) | **通过, 无竞态** |

`synctest` 与 `-race` 同跑无兼容性问题 (与 12.9.2 的结论一致),
伸缩测试因此可同时获得确定性与竞态检测, 无需分轮。

#### 下一步

- **L2-d**: 伸缩行为已在本次随 L2-c 一并覆盖 (6 项), 剩余是补
  `TestScaleDownFloor` 与 `TestRetireWorkerDrainsNothing` 的独立断言
- **L2-e**: `tcp.Option` 接线 (`WithElasticPool` / `WithScaleRatio`)
- **收益证明**: L2-e 后在 cloud2 复跑分离式压测, 对比固定 4 worker 与
  弹性 2-8 worker。**这是 L2 价值的最终证明, 当前只证明机制正确。**

以上三项已于 2026-10-16 完成, 见 14.10 与 14.11。

### 14.10 L2-d / L2-e 实施记录 (2026-10-16)

#### L2-d: 两项缺失断言

补齐 11.7 测试计划里 L2-c 未覆盖的两项, 加 `TestSetScaleRatioAcceptsValidPair`
与 `TestSetScaleRatioRejectsInvertedPair` (5 个子用例)。

`TestScaleDownFloor` **两次写错, 都是测试手法而非池的缺陷**:

1. 第一次用 parked handler (仿`TestScaleDownRequiresStreak`)。parked handler
   不消费任务, 队列占用率每个 tick 累积, 第 4 个 tick 越过 0.25 阈值把
   `downStreak` 重置, 永远到不了地板。
2. 第二次换成立即返回的 handler, **仍然失败** -- 紧密 submit 循环的提交速度
   超过 worker 消费速度, 队列照样填满。

结论: **地板属性应直接驱动 `maybeScale` 并使用空队列**, 而不是试图用 Submit
制造低水位。"突发提交是否算低水位"是采样问题, 已由 `TestScaleDownRequiresStreak`
覆盖; 本测试要验的是地板本身, 空队列是它唯一无歧义的低水位信号。

`TestRetireWorkerDrainsNothing` 的handler **必须非阻塞通知测试**:
`Stop` 在 worker 退出后仍会 `drainQueue`, 此时 handler 若阻塞在 channel send
上, 就再无人接收, `Stop` 永久挂死。这个坑实际触发过一次 (测试超时 25s)。

#### L2-e: 接线与顺带修复

`Server.pool` 由 `*shared.WorkerPool` 改为两方法的 `taskPool` 接口,
`Start` 中按选项选实现。三个新公开成员:

| 成员 | 所属 | 用途 |
|---|---|---|
| `SetScaleRatio(up, down)` | ScalablePool | 拒绝倒置/越界比例, **不静默重排** -- 重排会把调用方的笔误变成一份他没要求的策略 |
| `WorkerCount()` / `WorkerBounds()` | 两种池 | 固定池也实现, 否则两者无法比较 |
| `Server.WorkerCount()` / `WorkerBounds()` | tcp.Server | 转发, 压测脚本据此观察伸缩是否发生 |

**顺带修复 11.6 记录的既有泄漏**: `Start` 改为 `net.Listen` 成功后才建池。
原顺序先起 worker, listen 失败再 Stop; 若 `Start` 被重试, 旧 pool 被覆盖而其
worker goroutine 仍在运行且无句柄可达 -- 改为「监听成功才建池」是消除该路径,
而不是事后清理。

#### 两处格式问题 (非本次逻辑改动)

`pool_scale_test.go` (CRLF + 缺尾换行) 与 `worker_pool_saturation_test.go`
(const 对齐、字符串拼接前缺空格、缺尾换行) 本次提交的版本就通不过 `gofmt`。
已单独提交归一化, 仅空白改动。

### 14.11 L2 收益证明: 固定池 vs 弹性池 (2026-10-16 云端实测)

14.8 末尾写明"**尚未验证**", 14.9 结尾仍写着收益待证。本节是那次验证。

#### 环境与方法

| 项 | 值 |
|---|---|
| 服务端 | srv2 `47.120.44.128`, 8 vCPU / 16G, Ubuntu 26.04, go1.26.5 |
| 客户端 | srv3 `8.130.190.141`, 8 vCPU / 16G, **独立物理机** |
| 链路 | srv2 -> srv3 SSH 隧道 (srv2 安全组仅放行 22/80/443 + 18000/19000, 18000 被既有进程占用) |
| 负载 | 4/8 连接, 限速客户端, handler 延迟 1ms, queue 512, policy drop, 每配置 10s |
| 每配置独立重启服务端 | 是 -- 指标惰性注册, 复用会让计数累积 |

**四处方法学要点**, 每一条都对应此前某一轮的错误:

1. **客户端与服务端分机** -- 13 同机双进程下客户端抢CPU, 测的是客户端。
2. **客户端限速** -- 不限速则客户端填满自己的 socket 缓冲, 服务端感受不到压力。
3. **queue_full 取增量**, 不用客户端 recv 做分母 -- 计数是累计的; 分母必须是
   **sent** (每个 sent 帧恰好对应一次 pool Submit), 用 recv 会算出 **>100%** 的
   丢弃率 (本轮首跑 fixed_w2 算出 236%, 即此错误暴露)。
4. **服务端 worker 数从 journal 读**, 不从重定向文件读 -- `systemd-run` 把
   stdout 送进 journal, 文件恒为空 (首跑 worker 数全空即此因)。

#### 数据

| 配置 | 实际 worker | 吞吐 (msg/s) | drop% | loss% |
|---|---|---|---|---|
| 固定 w=2 | 2 | 1782 | 70.157 | 70.157 |
| 固定 w=4 | 4 | 3467 | 41.928 | 41.926 |
| 固定 w=8 | 8 | 5926 | 0.747 | 0.745 |
| **弹性 2-8** | **2 -> 8** | **5746** | **3.750** | **3.750** |
| 突发 固定 w=8 (12k/s) | 8 | 6712 | 43.787 | 43.786 |
| **突发 弹性 2-16 (12k/s)** | **2 -> 15** | **10336** | **13.437** | **13.436** |

#### 三条结论

**1. 14.2 的近线性在独立复测中复现。** 固定池 w=2 -> w=4 -> w=8:
1782 -> 3467 -> 5926 msg/s, 相邻提升 **1.95x / 1.71x**。这与 14.2
(w=1->8 线性度 0.91x) 一致, 证明 13 "worker 数无关" 确属归因错误。

**2. 弹性池在同等 offered load 下追平按峰值配好的固定池。**
6000/s 下弹性 2-8 达 5746 msg/s (3.75% drop), 对比固定 w=8 的 5926 msg/s
(0.75% drop)。**弹性略逊** -- 这是采样滞后: 它要靠 backlog 累积才扩, 而固定池
从第一刻就是 8 个 worker。**弹性的价值不在峰值, 在于无需预先知道峰值**。

**3. 突发场景下差距拉开。** 12000/s 时固定 w=8 是 6712 msg/s / 43.8% drop,
弹性 2-16 达 10336 msg/s / 13.4% drop -- **吞吐 1.54x, 丢弃率降 69.3%**。
固定池的天花板是配置值; 弹性池在 12k/s 下实测涨到 15, 仍在 max=16 之内。
**这正是 L2 立论的直接兑现: 天花板从 `固定 worker 数 / handler 耗时`
变为 `maxWorker / handler 耗时`。**

#### 数据自洽性校验

六行的 drop% 与 loss% **逐行吻合到小数点后三位**。二者是独立量: 前者来自服务端
`queue_full` 增量, 后者来自客户端 sent/recv 差值。它们一致说明丢弃确实发生在
池的拒绝路径上, 没有在 socket 缓冲或客户端读取环节丢失。**这是判定这批数据
可信的依据, 而非仅仅是个巧合。**

#### 局限 (必须承认)

- **单机单配置, 每配置仅 10s, 无重复采样**, 因此无方差估计; 上述差异
  (尤其"弹性略逊于固定 w=8") 应视为量级而非精确值。
- **客户端限速 12000/s 是本机 ticker 能稳定发出的量级**, 不代表真实上限;
  突发组的 offered load 未高到让固定池也逼近 8 worker 的理论上限
  (8 worker x 1ms = 8000 msg/s), 但网络 RTT 与隧道开销使实测天花板低于此。
- **L2 不解决单session 饿死**: 队列全局共享, 一个噪声 session 仍可耗尽队列 (11.5)。
- 收缩方向未在本轮验证 -- 本轮只测增长。`TestScaleDownFloor` 覆盖了地板,
  但真实负载下的收缩时机与振荡抑制未实测。

**结论: L2 立论成立, 收益已实测。** 建议 `WithElasticPool` 保持默认关闭,
由调用方按负载特征显式选择: **负载平稳且峰值已知** 用固定池 (无滞后、无额外 goroutine);
**负载波动或峰值未知** 用弹性池。
