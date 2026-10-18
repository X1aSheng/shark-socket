# 横向对比: 与 smart-socket 及同类开源项目的机制差距与改进空间

## meta-info

- 报告编号: COMPARISON-261018-OSS-PEERS
- 日期: 2026-10-18
- 任务: 在 `COMPARISON-261017-SMARTBOOT.md` 之后, 把对比对象从 smart-socket 单一目标
  扩展到同类热门开源项目, 输出按优先级排序, 可验收的改进清单
- 代码基线: main @ 2266867b9
- 对比对象: smart-socket (Java/AIO), gnet v2 (Go/epoll), netpoll (Go/CloudWeGo),
  Netty (Java), t-io (Java)
- 方法: 全仓静态检索取证 + 引用既有报告数据 + 5 个项目的公开文档与源码结构
- 口径: 只做**结构性对比**与**代码级取证**; 不并列不同机器, 不同负载的 msg/s
  (该口径由 `COMPARISON-261017-SMARTBOOT.md` 第 8 节确立, 本报告沿用)
- 边界: 本轮未重跑基准. 第 4 节所有 syscall 与分配次数均为**代码结构计数**,
  不是 strace/pprof 实测, 必须按第 7 节的验收口径复核后才可写入 README

---

## 1. 结论摘要 TL;DR

1. **功能面领先, 数据面落后一个代际.** 8 种传输, 统一会话模型, 分阶段关停,
   插件 panic 隔离与回滚, 是 gnet/netpoll/smart-socket 都不具备的; 但读写路径仍是
   "每帧 2 次 Read + 2 次 Write, 每帧一次 `make`", 全仓 `sync.Pool` / `bufio` /
   `net.Buffers` **各 0 命中**. gnet (ring buffer + 内存池), netpoll (LinkBuffer
   nocopy + splice), Netty (池化 ByteBuf + 写合并) 都已解决这一层.

2. **三项 P0 属于安全一致性, 比性能更该先修.** UDP/CoAP 伪会话无上限 (伪造源地址
   可打爆内存); TLS 与 WebSocket 握手无独立超时; HTTP 传输没有连接上限与接收速率,
   而 TCP/WS/QUIC/gRPC-Web 都有. 同一个框架里安全姿态不一致, 是比"慢"更严重的问题.

3. **可观测名不副实.** `Metrics` 接口有 `ObserveHistogram`, 生产代码 **0 个调用点**;
   Prometheus 导出器把直方图写成**无分位数的 summary** (只有 `_count`/`_sum`),
   P99 在 Prometheus 侧算不出来; 生产代码只有 **5 个指标名, 0 个 label** --
   消息总数与 handler 延迟这两个最该有的指标只出现在测试里 (`prometheus_test.go:12-16`),
   `sessions_reclaimed_total` 把 5 个传输混在一个计数器里;
   `Tracer` 除生命周期外无任何 `Start` 调用点, 而 README 声明 "traces on critical paths".

4. **CoAP 是"主路径可用", 不是"规范符合".** 重传用固定 2s 全局 ticker + 4 次,
   不符合 RFC 7252 第 4.2 节的随机初始超时与指数退避; 无 duplicate 检测;
   无 Block1/Block2, LwM2M 固件对象这类大 payload 走不通.

5. **会话有两张表.** `tcp.Server.sessions` (sync.Map) 与 `SessionManager.sessions`
   (全局 RWMutex + map) 同时维护; 1 万会话一次广播 = 1 万次 payload 拷贝
   (每个收件人一次), 而不是一次共享.

6. **定位建议.** 不要与 gnet/netpoll 拼单连接极限吞吐 -- 它们是传输内核, 本项目是
   网关, 抽象层不同. 差异化应押在 "协议广度 + 生命周期确定性 + 可运维性",
   同时把数据面拉到 "同量级不落后", 否则协议广度会被单连接成本抵消.

---

## 2. 对比对象与可比性

### 2.1 五个项目分别是什么

| 项目 | 抽象层 | IO 模型 | 公认强项 | 与本项目的关系 |
| --- | --- | --- | --- | --- |
| smart-socket | 传输内核 (仅 TCP) | Java AIO (NIO.2), 单协议微内核, 核心约 2000 行 | 单协议极致, 缓冲复用, 接口极简 (Protocol + MessageProcessor) | 261017 已对比; 本报告补充其缓冲所有权机制 |
| gnet v2 | 传输内核 (TCP/UDP/UDS) | epoll/kqueue, 主从多 reactors, 边沿/水平触发可选 | ring buffer, 内存池, SO_REUSEPORT, 编解码器, 异步写 | 同层对照: 看"无框架包袱"能做到什么 |
| netpoll | 传输内核 (TCP/UDS) | epoll LT + NoCopy LinkBuffer | LinkBuffer 零拷贝, gopool, mcache, splice, IsActive | 同层对照: 看零拷贝 API 的设计代价 |
| Netty | 传输框架 (pipeline) | Reactor + ByteBuf | 池化 ByteBuf + 引用计数 + 写合并 + watermark 背压 + native transport | 机制最完整的参照系 |
| t-io | 应用框架 (TCP/UDP/WS/HTTP) | 固定线程模型 | 组管理, 心跳, 流控, IP 黑白名单, 内置统计 | 应用层对照: 看"网关自带运维能力"的边界 |
| 本项目 | 多协议网关 (8 传输) | 每连接 2 goroutine + 工作池 | 协议广度, 生命周期, 插件隔离, 测试与部署硬化 | 本体 |

### 2.2 为什么仍然不并列吞吐数字

这些项目抽象层不同 (内核 / 框架 / 网关), 负载语义也不同 (echo / RPC / 业务 handler).
把本项目的 316k msg/s (50 连接, 256B) 与 smart-socket 的 "500W msg/s"
(33B 消息, 不同硬件) 并列, 读者只会得到一个错误排序. 可比的是**机制**:

- 数据由谁持有 (copy / 共享 / 池化 / 引用计数)
- 一帧要几次 syscall, 几次堆分配
- 背压作用在哪一层 (队列 / socket / 对端)
- socket 选项与多进程扩展能力
- 连接表的数据结构与争用点

第 3, 4 节只谈这五项.

---

## 3. 机制对比矩阵

| 机制 | 本项目 | smart-socket | gnet v2 | netpoll | Netty | t-io |
| --- | --- | --- | --- | --- | --- | --- |
| 读路径缓冲 | 无 (`io.ReadFull` 直读 conn) | AIO 缓冲区 | ring buffer 复用 | LinkBuffer nocopy | 池化 ByteBuf | 线程内缓冲 |
| 写路径批处理 | 无 (header/payload 两次 Write) | 缓冲写出 | 异步写 + 队列 | 批量写 | flush 合并 + writev | 发送队列 |
| 每连接 goroutine | 2 (readLoop + writeLoop) | 0 (AIO 回调) | 1/event-loop 分摊 | 1/poller 分摊 | 0 (event loop) | 线程池分摊 |
| 内存池 | 无 (`sync.Pool` 0 命中) | 有 | 有 (bytebufferpool) | 有 (mcache) | 有 (PooledByteBuf) | 有 |
| 零拷贝接口 | 无 (`Framer` 返回 `[]byte`) | 解码不出缓冲 | 部分 | 有 (nocopy / splice) | 有 (FileRegion, composite) | 无 |
| 写侧背压 | 队列水位 0.8 + 满则丢 | - | 队列 | 队列 | watermark + `isWritable` + autoRead | 流控 (单条/组/IP) |
| 连接上限 | 有 (4/8 传输有) | - | - | - | 需自行组合 | 有 |
| SO_REUSEPORT | 无 | - | 有 | 部分 | native transport 有 | 无 |
| keepalive 可配 | 无 | - | 有 | 有 (sysctl) | 有 | 有 |
| PROXY protocol | 无 | 无 | 无 | 无 | 有 | 无 |
| 协议广度 | 8 (TCP/UDP/HTTP/WS/CoAP/LwM2M/QUIC/gRPC-Web) | 1 | 3 | 2 | 多 (含 HTTP/2, HTTP/3) | 4 |
| 生命周期 | 分阶段关停 + 每传输 idle 回收 | 简单 | 简单 | 简单 | 需自行组合 | 简单 |
| 插件/扩展 | 优先链 + panic 隔离 + 回滚 | 2 接口 | EventHandler | EventLoop | pipeline | 拦截器 |
| 观测 | 指标接口 + OTel 适配 (未埋点) | 无 | 无 | 无 | 需 Micrometer | 内置统计 |
| 管理面 | healthz/readyz | 无 | 无 | 无 | 无 | 有 (组/连接查看) |

读法: 表中 "无" 不等于缺陷 -- 传输内核不需要生命周期与管理面. 缺陷只在
**本项目自己声明了却没有兑现**的格子: 零拷贝, 观测埋点, 背压完整性.

---

## 4. 差距清单

优先级依据: P0 = 可被利用或可导致不可用; P1 = 影响单位成本或可信度;
P2 = 工程效率与生态. 每项给出证据 (file:line), 对标, 改法, 验收.

### 4.1 A 类: 安全与稳定性 (P0)

**A1 UDP/CoAP 伪会话无上限**

- 证据: `internal/transport/udp/server.go:357-380` 每来一个新源地址就
  `newSession` + `Register`, 无任何配额; `udp/options.go` 无 `MaxSessions` 字段;
  `coap/server.go:322` 同构
- 路径: 伪造源地址的 UDP 洪泛 -> 会话表与 SessionManager 无限增长 ->
  `SessionManager` 的 100 万兜底 (`session_manager.go:28`) 被触发后**新会话一律拒绝**,
  正常设备也进不来 (拒绝服务从 "内存耗尽" 变成 "容量占满", 更难恢复)
- 对标: Netty 也不内置, 但 t-io 有 IP 黑名单与流控; 关键不是别人有, 而是本项目
  TCP 有 `MaxConnections` 而 UDP/CoAP 没有 -- **内部不一致**
- 改法: options 增 `MaxSessions` 与单源地址会话配额, 超限丢包并计数
  (`udp_sessions_rejected_total`), 复用 `shared.Acceptor` 的计数思路
- 验收: 新增压测: 10 万个伪造源地址打 UDP, 断言 RSS 与 `sessions_active` 有上限,
  且正常源地址仍能建会话

**A2 握手无独立超时**

- 证据: TLS 走 `tls.NewListener` (`tcp/server.go:103`), 握手发生在首次 Read,
  只受 `ReadTimeout` 约束 (默认 5 分钟, `tcp/options.go:55`);
  WebSocket 的 `websocket.Upgrader` 未设 `HandshakeTimeout` (`websocket/server.go:148`),
  gorilla 默认 0 = 无超时; 同时未设 `ReadBufferSize`/`WriteBufferSize`,
  默认 4 KiB + 4 KiB 每连接
- 影响: 只发 ClientHello 或只发一半 HTTP 升级头的连接, 可以各占一个
  `MaxConnections` 名额 5 分钟 / 永久, 且 TLS 握手是 CPU 密集操作
- 改法: 新增 `TLSHandshakeTimeout` (语义同 `net/http.Server`), 握手后清零;
  WS 侧 `HandshakeTimeout=10s`, 缓冲大小可配
- 验收: 打开 N 个只发 ClientHello 的连接, 断言在 timeout 内被回收且
  `sessions_reclaimed_total` 增长; 握手失败计入 `tls_handshake_failed_total`

**A3 HTTP 传输缺连接上限与接收速率**

- 证据: `http/options.go:11-18` 只有 `Addr/Handler/Mux/MaxBodyBytes/3 个 timeout/CORS`;
  而 `tcp`, `websocket`, `quic`, `grpcweb` 均有 `MaxConnections` 与 `AcceptRate`
- 影响: 同一个网关里, WS 与 gRPC-Web 有闸门, 纯 HTTP (Mode A/B) 没有; 攻击者选最软的一扇门
- 改法: 复用 `shared.Acceptor`, 用 `http.Server.ConnState` 维护在连数
- 验收: 与 TCP 同构的连接上限测试 (并发超限时返回 503 而非接受后丢弃)

**A4 accept 错误处理无分类, 无退避, 无指标**

- 证据: `tcp/server.go:212-220` 任何错误固定 `time.Sleep(100ms)` 后重试;
  UDP/CoAP 已使用 `shared.NewBackoff` (`udp/server.go:291`), 两处不一致
- 影响: EMFILE (fd 耗尽) 时以 10 次/秒的节奏刷日志并持续抢占 accept 时机;
  且没有 `accept_errors_total`, 运维看不到 "正在被 fd 卡住"
- 改法: 复用 `shared.NewBackoff`, 区分 `net.ErrClosed` (终止) 与临时错误 (退避);
  对 EMFILE 单独计数
- 验收: 用 `ulimit -n` 压到极小复现 EMFILE, 断言进程不刷日志风暴, 恢复后自动继续

**A5 无单 IP 连接配额**

- 证据: 全局 `MaxConnections` (`tcp/server.go:90`) + Blacklist/AutoBan 插件;
  `RateLimit` 是**每 IP 的消息**限流 (`plugin/ratelimit.go:145` `remoteKey`),
  不限制连接数
- 影响: 单个 IP 可以合法地占满全部连接名额; AutoBan 的阈值是消息维度, 建连阶段不触发
- 改法: `shared.Acceptor` 增加 per-IP 计数 (带 TTL 清理), 超限直接关闭
- 验收: 单 IP 建连打到上限, 断言其他 IP 仍可接入

### 4.2 B 类: 数据面性能与资源 (P1)

**B1 读路径无缓冲, 每帧 2 次 syscall**

- 证据: `tcp/framer.go:32-48` `io.ReadFull(header)` + `io.ReadFull(payload)`
  作用在 `net.Conn` 上 = 每帧 2 次 Read; `LineFramer` (`framer.go:82-88`)
  每字节 1 次 Read, 100 字节行 = 101 次 syscall
- 对照: 五个项目里只有本项目在裸 conn 上按帧边界读; 其余都有读缓冲
- 改法: 每连接 `bufio.Reader` (4 KiB) 或池化 block reader, `Framer` 接口不变即可落地
- 验收: 计数式 reader 下的 reads/frame 从 2.000 降到 0.3 以下 (256B 帧, 1 KiB 缓冲);
  allocs/frame 2 -> 1; 常驻堆按预期上升 `ReadBufferBytes` (见 B3 的口径);
  云主机复采 316k msg/s 档的 P99 (401us) 不退化, 并以 `strace -c` 交叉验证
- **已实施 (2026-10-18, ADR-012)**: 会话持有 `bufio.Reader`, `Framer` 合同不变.
  计数式 reader 下 reads/frame 2.000 -> 0.078 (256B 帧) / 0.266 (1 KiB 帧) /
  1.016 (`RawFramer`, 不缓冲); allocs/frame 2 -> 1; 1,000 空闲连接常驻堆
  6,647 -> 7,888 B/连接 (差值即 1 KiB 缓冲). 云主机 P99 复采**未做**, 挂账

**B2 写路径 2 次 syscall + 合同性拷贝**

- 证据: `framer.go:55-63` 先 `Write(header)` 再 `Write(payload)`;
  `session.go:90` `Send` 复制 payload (合同要求, 见 ADR-001 的后续约束)
- 改法: 第一步把小帧拼进一个 `4+N` 缓冲一次写出; 第二步再评估
  `net.Buffers` / `writev` 的批量收益 (需先有 syscall 数据)
- 验收: 写 syscall 从 2/帧 降到 1/帧; `BenchmarkSendCopy` 的 B/op 不升
- **已实施 (2026-10-18, ADR-013)**: 合并写按平台生效 (unix writev), 批处理默认
  关闭. 会话级实测 3 帧排队时底层 `Write` 6 -> 1; 端到端收益**未证明** --
  Windows 回环流水线 echo 两组无可分辨差异, 需 Linux 云主机复核

**B3 每帧一次分配, header 逃逸, 全仓无池**

- 证据: `framer.go:45` `make([]byte, int(raw))`; `framer.go:33`
  `var header [4]byte` 因传给 `io.ReadFull` 逃逸; 全仓 `sync.Pool` 检索 0 命中
- 对照: smart-socket/gnet/netpoll/Netty/t-io 都有缓冲池
- 改法: 分两步, 顺序不能颠倒
  - (a) header 改为会话内复用或从池取, 低风险
  - (b) `Framer` 合同升级为 "调用方提供缓冲" 或 "返回可归还的 Frame",
    这是破坏性变更, 需先写 ADR-012 再动代码
- 验收: 64B 帧 allocs/frame 2 -> 1 (已由 `AllocsPerRun` 断言锁死); 常驻堆在
  `ReadBufferBytes=0` 时保持 6,219 B/连接, 开启缓冲时扣除缓冲本身后不升
- **已实施 (a) (2026-10-18, ADR-012)**: header 不再逃逸 -- `LengthPrefixFramer`
  在缓冲路径上用 `Peek(4)` + `Discard(4)` 直接读缓冲内的字节, `LineFramer` 用
  `ReadSlice`, 两者都不再产出中间 `[]byte`. allocs/frame 2 -> 1, 剩下的 1 次是
  payload 本身的 `make` (即 (b) 的范围, 未动)

**B4 广播拷贝放大**

- 证据: `session_manager.go:101-112` `Broadcast` 快照后逐个 `Send`;
  `session.go:90` 每个 `Send` 各复制一次 payload
- 影响: 1 万会话广播一次 = 1 万次 payload 拷贝 + 1 万次 slice 头入队;
  10 万连接下这是每次广播 GB 级的瞬时分配
- 对照: Netty 的 `ByteBuf` 引用计数天然支持一对多; gnet 也没有共享帧, 但它的
  `AsyncWrite` 允许调用方持有缓冲
- 改法: 引入只读共享帧, 生命周期由最后一个 `writeLoop` 释放 (引用计数或
  `sync.WaitGroup` 式计数); 新增 `SendShared`
- 验收: 新增基准 "1 万会话广播一次", allocs 从 O(N) 降到 O(1)
- **已实施 (2026-10-18, ADR-014)**: `core.SharedFrame` + 可选 `SharedSender`.
  1 KiB payload 广播 1,000 会话: 2,132,297 B/op -> 52,576 B/op,
  2,011 -> 12 allocs, 1.05 ms -> 34 us. 剩余 O(N) 是 Snapshot 与分派切片扩容
  (8 字节/收件人), 不是 payload 复制

**B5 会话双表 + 全局锁**

- 证据: `tcp/server.go:41` `sessions sync.Map` 与 `SessionManager` 同时维护;
  `session_manager.go:12` 全局 `sync.RWMutex`
- 影响: 每次 connect/disconnect 两次插入删除; 连接风暴时全局读写锁是单点
  (实测 859k 连接/10s 未暴露, 属余量而非已发生故障)
- 改法: 去掉传输侧第二张表 (只需 ID -> 本地 conn 的映射可保留 `sync.Map`,
  但会话注册只走一个口径); 或 SessionManager 按 ID 高字节分 256 片
- 验收: 连接风暴压测 P99 不退化, footprint 基准不升
- **已实施 (2026-10-18, ADR-015)**: 只做了分片这一半. `SessionManager` 按会话 ID
  分 64 片 (每片独立 `RWMutex` + map, 按 64 字节对齐), 全局容量改在共享计数器上
  CAS 认领, 判重仍先于判容量. `BenchmarkSessionManager_RegisterGetUnregister_Parallel`
  16 并发 366-382 -> 149-163 ns/op (2.4 倍); 单线程在噪声内持平未回退.
  **双表保留**: 传输内表按对端地址索引, 全局表按会话 ID 索引, 键与用途不同;
  合并需要全局表支持按 (协议, 对端地址) 查找, 是把 UDP 键搬进全局表的反向改动

**B6 UDP 每数据报 2 次分配**

- 证据: `udp/server.go:306` `append` 复制数据报; `udp/server.go:358`
  `addr.String()` 作为 map key (每包一次字符串分配)
- 改法: key 改为 `[16]byte + port` 结构; payload 走池或交给 handler 前不复制
- 验收: 新增 UDP 数据报基准, allocs/op 从 2+ 降到 0~1
- **已实施 (2026-10-18, ADR-015)**: 新增 `shared.PeerKey` 并在会话上缓存, UDP 与
  CoAP 的会话解析 99-113 ns / 32 B / 3 allocs -> 46-47 ns / 0 B / 0 allocs
  (装箱的那次分配因接口值未逃逸而被编译器放到栈上). 地址统一存 16 字节形式,
  使 IPv4 与 IPv4-mapped 归一为同一个键 -- 否则同一对端会拿到两个会话并绕过
  每对端资源上限. **payload 拷贝仍在**: 每包 4 次分配降到 1 次, 归零需要缓冲池
  并改 `Send` 的所有权, 属 ADR-001 层面

**B7 插件链每消息固定开销**

- 证据: `plugin_chain.go:144` `snapshot()` 每消息一次 RLock;
  每个插件一次 `defer/recover` (`safeMessage`)
- 影响: 已有实测 4 插件 +1.7% (1ms handler); 对 20us 级 handler 占比会显著上升,
  而 IoT 场景的 handler 往往就是 20us 级
- 改法: 链级单次 recover + 循环内记录当前插件序号; 用 generation 计数替代 RLock
- 验收: 插件基准在 10us handler 下的 ns/op 下降, 随插件数的斜率变缓
- **已实施 (2026-10-18, ADR-015)**: 读路径改为 `atomic.Pointer[pluginView]` 发布
  不可变视图, 不再加锁 (比 generation 计数更简单且无 ABA 问题);
  `OnMessage` / `OnAccept` 改为链级单次 recover. 16 并发下 5 插件
  23.4-24.5 -> 4.5-5.3 ns/op (4.6 倍), 并发重配置 83-97 -> 7-17 ns/op (6 倍).
  **注意**: 单线程基准看不出这一项 -- 无争用的 `RWMutex.RLock` 只有几纳秒,
  代价是所有传输所有核共用一个缓存行, 只有并发基准能测到.
  `OnClose` 保留逐插件 recover: 它的语义是 panic 后仍要关闭后续插件,
  合成链级会拖住后面所有插件的资源

**B8 每连接 2 goroutine (记录在案, 本轮不改)**

- 数据: 2.001 goroutine/连接, 栈 40.3 MB / 10k 连接, 占总量约 40%
  (`COMPARISON-261017-SMARTBOOT.md` 5.2)
- 判断: 堆占 60%, 先做 B1-B3; reactor 化等于重写 8 个传输, 与 ADR-002
  "Gateway 拥有 Runtime" 和现有分层冲突
- 处置: 作为**显式决策**记录, 不列为待办, 避免反复被提出

### 4.3 C 类: 协议完备性 (P1/P2)

**C1 CoAP 重传不符合 RFC 7252 第 4.2 节**

- 证据: `coap/server.go:44-45` `maxRetransmit=4`, `retransmitInterval=2s`;
  `coap/server.go:615` 由全局 ticker 驱动
- 规范: ACK_TIMEOUT 2s, ACK_RANDOM_FACTOR 1.5 -> 首次超时在 [2,3]s 随机,
  每次重传翻倍 (2-3s, 4-6s, 8-12s, 16-24s)
- 影响: 固定间隔且无抖动, 多设备同时掉线时会**同步重传** (thundering herd);
  在拥塞链路上成功率低于规范算法
- 改法: 每条待确认消息独立 deadline + 指数退避 + 随机抖动, 保留现有
  `pendingNotifies` 结构
- 验收: 单元测试断言第 n 次重传时刻落在 `[2^n * 2, 2^n * 3]` 秒;
  集成测试丢弃首个 CON 后仍能收到重传
- **已实施 (2026-10-18)**: `coap/retransmit.go` 抽出纯函数 `decide`/`advance`,
  每条消息持有自己的 `timeout` 与 `deadline`, 初始超时从 `[2s,3s)` 抽取,
  每次翻倍, `deadline` 从上一个 deadline 推出使 tick 延迟不累积.
  新增 6 个测试 (含 RFC 表格逐行断言, 抖动, 迟到 tick 不补发, 放弃前等待最后一个超时)
- **本报告的验收标准写错了, 已在上一条修正**: `[2^n * 2, 2^n * 3]` 既把
  **每间隔**的量当成了**累计**量, 指数又错了一位. RFC 7252 第 4.2 节的表格是
  每间隔 `[2^(n-1)*2, 2^(n-1)*3)`, 累计 `[2,3) [6,9) [14,21) [30,45)`,
  放弃在 `[62,93)` (MAX_TRANSMIT_WAIT). 测试按修正后的口径断言

**C2 无 duplicate 检测**

- 证据: 全仓未见按 `remote + MessageID` 的近期响应的缓存; 重复 CON 会被当作新请求
- 规范: RFC 7252 第 4.5 节要求在被确认方缓存响应, 重复 CON 直接重发缓存 ACK
- 影响: 客户端重传会导致业务 handler 重复执行 (对 LwM2M 的 write/execute 是副作用问题)
- 改法: 按 remote + msgID 缓存最近 ACK, TTL 取 EXCHANGE_LIFETIME
- **部分已存在**: 上一条的取证不准确 -- `coap/server.go` 早有 `seen`
  (`remote + msgID`) 表且重复 CON 不会重跑 handler. 真正的缺口是**没有缓存响应**:
  重复 CON 回的是 `2.03 Valid`, 那是在声称一个服务器并没有的响应码
- **已实施 (2026-10-18)**: `seen` 的值改为 `*seenEntry`, 保存首次响应字节;
  重复 CON 重发同样字节 (RFC 7252 第 4.5 节), 首次响应尚不存在时回空 ACK 0.00.
  集成测试用 `Responder` 返回 `response-%d`, 断言重复请求拿到的是 `response-1`
  而不是 `response-2`, 即缓存而非重算

**C3 无 Block1/Block2 (RFC 7959)**

- 证据: 全仓 `Block1`/`Block2` 检索 0 命中; `coap/message.go` 无对应 option 定义
- 影响: LwM2M 固件对象 (/5) 与大型 TLV 不可用; Leshan 等标准客户端对大 payload
  默认走 block-wise, 互联会直接失败
- 改法: 先做 Block2 (服务端分块下发, 收益大且简单), Block1 视业务需要
- 验收: 用标准客户端 (Leshan) 读一个 4 KiB 资源, 断言分块重组正确

**C4 无 PROXY protocol v1/v2**

- 证据: 全仓无相关解析; `RemoteAddr` 直接取 `conn.RemoteAddr()`
- 影响: 部署在 L4 LB/NLB 后, `RemoteAddr` 恒为 LB 地址 -> 黑名单, RateLimit
  (按 IP), 审计全部失效. 这是**上生产才暴露**的问题
- 改法: per-listener 开关, 在 `handleConn` 首帧前解析, 与 `Framer` 解耦
- 验收: 发送 PROXY v1 头后, `sess.RemoteAddr()` 返回真实客户端; 未开启时行为不变
- **已实施 (2026-10-18, ADR-016)**: 解析放在 **listener** 而非 `handleConn` --
  后者只覆盖 TCP, 而 HTTP / WebSocket / gRPC-Web 的 accept 循环在 `net/http` 手里,
  同一段逻辑要写三遍且容易在读缓冲与 `Serve` 之间丢字节. `shared.NewProxyListener`
  一份实现四路共用; v1 与 v2 两种格式, 零值即关闭 (原样返回 listener), 头在 TLS
  之下. 缺头一律拒绝而非放行, 拒绝在 `Accept` 内部循环处理 (向上返回错误会让
  `http.Server.Serve` 停掉整个服务). 两条验收均有用例: TCP 与 WebSocket 各一个,
  外加"未开启时 `RemoteAddr` 仍是 socket 对端". 未做来源 CIDR 白名单, 信任模型
  是网络拓扑, 记在 ADR 的重新评估条件里

**C5 无 SO_REUSEPORT / ListenConfig**

- 证据: `tcp/server.go:97` 直接 `net.Listen`; 全仓无 `ListenConfig` / `Setsockopt`;
  无 keepalive 配置
- 影响: 单 listener + 单 accept goroutine, 多进程不能共享端口 (无法按核扩展);
  keepalive 只能用 Go 默认 15s, 无法按设备类型调整
- 改法: `net.ListenConfig{Control: reuseport}` + `KeepAliveConfig` (Go 1.23+ 已支持)
- 验收: 两个进程绑同一端口均可收流; 单机 8 核横向扩展吞吐有实测曲线
- **已实施 (2026-10-18)**: `shared.ListenOptions` + `shared.Listen` (零值与
  `net.Listen` 等价), 四路 TCP 系传输共用; `SO_REUSEPORT` 走 `//go:build unix &&
  !solaris` 的 `Control` 回调, 不支持时返回 `shared.ErrReusePortUnsupported` 而
  非静默忽略; keepalive 由 `net.KeepAliveConfig` 的三个字段表达, `idle < 0`
  可关闭探活 (标准库没这条路). WebSocket / gRPC-Web 的 `tls.Listen` 改为
  `shared.Listen` + `tls.NewListener`, 否则 `ListenOptions` 会被丢掉.
  **验收未全满足**: 端口共享的正确性由单元测试断言 (linux/darwin 正例, 其它平台
  断言显式错误), 但 "8 核横向扩展吞吐曲线" 没有做 -- 它需要 Linux 云主机多进程
  共端口压测, 与本轮其它未复采项一并挂账

**C6 配置静态 (只热更证书)**

- 证据: `app/config.go` 一次加载; 仅 `infra/tlsutil/watcher.go` 支持证书热重载
- 影响: 改限流阈值/黑名单/日志级别都要重启, 与"确定性生命周期"的运维叙事不匹配
- 改法: SIGHUP 重读**可热更子集** (日志级别, 限流参数, 黑名单), 监听变更明确不支持
- 验收: 热更后新连接立即生效, 存量连接不受影响, 失败则保留旧配置并告警

**C7 无管理/调试面**

- 证据: 全仓无 `pprof`/`expvar`; `Gateway.Health()` 只返回 map (`gateway.go:186`)
- 改法: 独立 admin listener (默认关闭), `/debug/pprof`, `/sessions` 摘要 (脱敏),
  `/plugins` 状态
- 验收: 默认构建不监听; 打开后可按协议统计连接数, 与指标一致

### 4.4 D 类: 可观测真实性 (P1)

**D1 延迟直方图从未被记录**

- 证据: `core/observability.go:46` 定义了 `ObserveHistogram`, 全仓生产代码
  调用点 **0 个** (仅 nop 实现与 Prometheus 实现自身)
- 影响: README 的 P99 数字全部来自压测工具, 线上无法自证; 出问题时没有分位数可看

**D2 Prometheus 直方图不可聚合**

- 证据: `prometheus.go:101-114` 只输出 `_count` 与 `_sum`, `# TYPE` 写 `summary`;
  summary 不支持跨实例聚合, 且无分位数时连单实例 P99 也算不出
- 改法: 改为显式 buckets 的 histogram (或用 OTel SDK 的 Prometheus exporter)
- 验收: `histogram_quantile(0.99, ...)` 能返回非空值

**D3 指标无 label**

- 证据: 全部 `IncCounter`/`SetGauge` 均为单参数调用 (`session_metrics.go:23-37`,
  `tcp/server.go:239`, `udp/server.go:263` 等); `sessions_reclaimed_total` 把
  TCP/UDP/WS/CoAP/gRPC-Web 5 个传输混在一个计数器里. 全仓生产代码只有 5 个指标名,
  而 `shark_messages_total` 与 `shark_handler_duration_seconds` 仅存在于
  `prometheus_test.go:12-16` -- 测试覆盖了导出格式, 但没人真正上报这两个指标
- 影响: 多协议网关最需要"按协议定位", 现在只能看总量; 与 README
  "Prometheus metrics use a fixed key set (no cardinality explosion)" 的取舍有关,
  但 protocol 是**有限枚举**, 不构成基数风险
- 改法: 至少给协议维度加 `protocol` label (8 个值)

**D4 Tracer 空转**

- 证据: `internal/infra/observability/otel.go` 提供了适配器, 但传输层无任何
  `Tracer().Start` 调用点; README 声明 "traces ... on critical paths"
- 处置: 二选一 -- 补埋点 (`OnAccept`/`OnMessage`/握手三段), 或改 README.
  不计为"未完成功能", 而是**文档与代码不一致**, 需在下一轮审查中关闭

### 4.5 E 类: 工程与生态 (P2)

- **E1 缺同机横向基准.** 261017 明确拒绝跨机对比, 但没有"同一台 Linux, 同一 echo
  负载, 本项目 vs gnet vs netpoll"的 harness. netpoll 社区的做法
  (`netpoll-benchmark`) 可直接借鉴: 让对手成为可复现的依赖, 而不是 README 里的引用
- **E2 无 benchstat 回归门禁.** 基准只在 `scripts/run_benchmarks.go` 手工跑, CI 不跑;
  资源基准只在 `.workbuddy/cloud/` 手工跑. 没有门禁的基准, 半年后就是过期数字
- **E3 fuzz 覆盖窄.** 5 个 `Fuzz` 目标集中于 TCP framer, CoAP message,
  LwM2M TLV; 缺 gRPC-Web framing, WS 帧, config JSON, QUIC 路径
- **E4 发布成熟度.** 仅 `v0.1.0` 与 `v0.1.0-rc.1`; v0.x 允许破坏性变更, 但应有一页
  "兼容承诺" 说明哪些包已冻结 (core 接口), 哪些会变
- **E5 README 定位页缺失.** 现在的 README 以功能矩阵开篇, 读者会拿它和 gnet
  比吞吐; 缺一页 "与 gnet/netpoll/Netty/t-io 的分工", 而这类误读会反复发生

---

## 5. 本项目的既有优势 (对标时不要丢掉)

- **协议广度**: 8 种传输共用一个会话索引与一条插件链. gnet/netpoll 只做 TCP/UDP/UDS,
  smart-socket 只做 TCP. 这是本项目唯一无法被"更快的库"替代的部分
- **生命周期确定性**: StopAccept -> Drain -> CloseSessions + 每传输 idle 回收 +
  `sessions_reclaimed_total`. Netty 需要自行组合, t-io 只有简单关停
- **插件隔离**: 单链 + panic 隔离 + `OnAccept` 失败回滚. 这个语义在同类里很少见
- **可测性与验证链**: 27 套单元 + 6 组压测 + 3 类 fuzz + 双云主机复验 + 覆盖率门限
- **部署硬化**: 非 root, 只读 rootfs, drop ALL, HPA/PDB/NetworkPolicy, 静态二进制
- **契约化**: core 只放接口, 编译期断言, 10 篇 ADR (编号至 011). 这是能安全做 B3 类破坏性
  变更的前提 -- 有 ADR 流程, 才不会改出"更快但不可维护"的版本

---

## 6. 不建议做的四件事

1. **不要为对标 gnet 把传输层改写为 reactor.** Go runtime 已经是 netpoll + 用户态
   调度, 重写的收益只在单连接极限吞吐, 代价是 8 个传输全部重做, 且直接冲突
   ADR-002 与现有分层
2. **不要加进程内 MQTT broker** (ADR-008 已定), 也不要为了对标 EMQX 引入集群路由
3. **不要做 HTTP 反向代理** (README 边界), HTTP/2 与 HTTP/3 同理 -- gRPC-Web 的
   "Unary + Server Streaming" 已是明确边界
4. **不要用 `sync.Pool` 掩盖接口问题.** 只池化 `[]byte` 而不改 `Framer` 合同,
   省的是分配, 省不掉拷贝; 正确顺序是 B1 (syscall) -> B2 (合并写) -> B3(b) (合同)

---

## 7. 实施顺序与验收

### 7.1 三批

| 批次 | 内容 | 改动面 | 前置 |
| --- | --- | --- | --- |
| 批 1 (P0) | A2 A3 A4 A5 A1 | 各传输 options + shared.Acceptor | 无, 可立即开始 |
| 批 2 (P1 数据面) | B1 B2 B3(a) B4 B6 D1 D2 | tcp framer/session, udp key, 观测 | 批 1 合并后; B3(b) 需 ADR-012 |
| 批 3 (P2 能力) | C1 C2 C4 C5 C7 E2 | CoAP 状态机, listener, admin, CI | 批 2 有基准数据后 |

进度 (2026-10-18): 批 2 的 B1 B2(部分) B3(a) B4 B5 B6 B7 已完成 (ADR-012 至
ADR-015), 批 3 的 C1 C2 C4 C5 已完成 (ADR-016). 其余 (A 类 5 项, B2 的端到端
证明, C3 C6 C7, D1 D2, E2) 未开始, 见各条目下的备注.

不做: B8 (已记录决策), C3 视 LwM2M 业务需求, C6 只做可热更子集.

### 7.2 统一验收规则

- 性能项必须给出 before/after 的 ns/op, B/op, allocs/op, 且在 **Linux 云主机**采集
  (Windows 每 goroutine 栈 8,219 B 对 Linux 2,112 B, 见 261017 第 9 节)
- 资源项用 `BenchmarkTCPIdleConnectionFootprint`, 断言写成 "不升" 而不是
  "下降 X%"; 门限维持 64 KiB/连接 的宽松守卫, 不贴着实测值设
- 安全项必须能构造出攻击流量, 并断言**资源上限**而不是"没崩"
- 协议项 (C1/C2) 以 RFC 条款 + 标准客户端互通为验收, 不以自测通过为验收
- 涉及合同变更的 (B3(b), C4 首帧语义) 先写 ADR, 再改代码, 与现有流程一致

### 7.3 建议的下一步 (单点)

先做 **B1 + D2**: 一个改读缓冲, 一个改直方图导出. 前者是本报告唯一能直接降低
单位成本且不改合同的项; 后者让后续所有优化都有线上分位数可证. 两项都不触碰
ADR, 适合作为下一批的开口.

---

## 8. 边界与不确定项

- 第 4 节的 syscall 数是**代码结构计数** (看代码数 Read/Write 调用), 不是 strace 实测.
  真实 syscall 数受 TCP 分段, Go runtime 缓冲, 写合并影响, 可能小于计数
- 5 个对标项目的机制描述来自其公开文档与 README, 未逐行审阅源码; 具体版本差异
  (尤其 Netty 的 native transport 与 t-io 的流控实现) 需在批 2 前核对
- CoAP 的 RFC 结论基于 RFC 7252 第 4.2 节文本. 若项目有意简化 (仅内网 IoT),
  应在 `docs/architecture/TRANSPORT.md` 写明 "非完全合规", 而不是静默
- 本报告未评估 QUIC 传输的流控与 0-RTT 能力, 也未评估 LwM2M 1.1 (SenML CBOR,
  Registration 接口 Bootstrap). 这两块需要单独的协议符合性报告
- 覆盖率数据未在本轮重采, 沿用 79.6-79.8% 的区间口径 (261017 已论证)
- 未评估 Windows 目标平台: 若生产含 Windows 边缘节点, gnet/netpoll 的对比口径
  需要重写 (netpoll 不支持 Windows)

---

## 9. 与既有报告的关系

| 报告 | 覆盖 | 本报告的关系 |
| --- | --- | --- |
| `COMPARISON-261017-SMARTBOOT.md` | smart-socket 单对象 + 资源基线 | 继承其资源数据与"不并列 msg/s"口径; 其第 8 节的 P0/P1 建议在本报告 B2/B3/B5/B8 中细化并补验收 |
| `PROJECT-REVIEW-261016-163106.md` | 缺陷登记 (D1-D14) 与 CI 修复 | 互补: 该报告管"已知缺陷是否修完", 本报告管"机制上还缺什么" |
| `ARCHITECTURE-ANALYSIS-260626.md` | 架构与分层 | 本报告不改分层结论, 只在数据面与协议完备性上追加 |
| `PROTOCOL-TEST-261017.md` | 7 协议规范符合性 | 本报告 C1/C2/C3 是其未覆盖的 CoAP 侧条款, 建议并入下一轮协议测试 |

本报告新增的, 261017 与 V10 审查都未覆盖的三类: **A 类安全一致性**,
**C 类协议完备性**, **D 类可观测真实性**.
