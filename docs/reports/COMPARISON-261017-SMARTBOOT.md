# smart-socket 对比分析与 shark-socket 性能/资源占用基线

## meta-info

- 报告编号: COMPARISON-261017-SMARTBOOT
- 日期: 2026-10-17
- 任务: 对比 gitee.com/smartboot/smart-socket, 分析 shark-socket, 完善性能和资源占用数据
- 代码基线: main @ 123de600d + 本次新增的 `tests/benchmark/bench_resource_test.go`
  与 `scripts/run_benchmarks.go` 的资源基准组 (基准文件 sha256
  `225d2b25d9abdc2bf79da565ed4f77aaeb0629481f41c31429e1755ad4716845`, 两台云主机校验一致)
- **基线主机 (第 5 节数据来源):** 阿里云 Ubuntu 26.04.1, 各 2 vCPU / 3622 MB,
  Intel Xeon 6982P-C, 内核 7.0.0-31-generic, go1.26.9 linux/amd64
  - srv2 8.130.182.119 (私网 172.29.189.109)
  - srv3 8.130.95.37 (私网 172.29.189.108)
- 对照主机 (仅作平台差异参照): 本机 Windows 11 / Ryzen 7 8845HS / go1.26.9
- 对比对象: smartboot/smart-socket (Java, AIO/NIO.2, 2000 余行核心)
- 采集方式: `run_baseline.sh` 与 `run_ladder.sh` (`.workbuddy/cloud/`),
  经 SFTP 传输源码并在云端编译; 10k 档 `-count=3` 重复 3 轮
- 工具链: Go 1.26.9 官方 linux-amd64 包, sha256
  `42d158b4d8f7b61ac0a830567c940a86098fb7aac52e467a5ebec03ef5cc2f8d` (两台一致)

---

## 1. 结论摘要 TL;DR

1. **两者不是同类项目, 性能数字不可直接对比。** smart-socket 是单一
   TCP+AIO 微内核 (核心约 2000 行), 目标场景是百万级长连接; shark-socket 是
   多协议网关 (生产代码 11804 行, 8 种传输)。smart-socket 的 "500W msg/s"
   与 shark-socket 的 "126925.7 msg/s" 测的不是同一件事, 把两者并列成排名是
   错误的比较。本报告因此把对比限定在**设计取向与资源模型**上。
2. **shark-socket 原有性能数据存在结构性缺口: 没有任何资源占用数据。**
   `BENCHMARK-260614-102513.md` 全篇只有 ns/op 与 msg/s, 资源一节仅记录
   MemAvailable 与 Load1 两个**主机级**指标 -- 即被测程序自己占多少, 一次都没测。
3. **设计文档早已识别该缺口, 但四项基准全部未落地。**
   `REARCHITECTURE-261016-v2.md` 第 10.3/10.5 节要求补 `MemStats` 输出与常驻内存
   守卫基准, 里程碑 M0 列为前置任务; 第 1955 与 2039 行两处自记
   "仍未做"。本报告完成了其中可做的部分。
4. **补齐后测得的首个资源基线 (阿里云 Linux 2C, 10k 空闲 TCP 连接, 两台一致):**
   - 常驻堆 **6219 B/连接** (HeapInuse 增量 62.2 MB)
   - **2.001 goroutine/连接** (20008 个 goroutine)
   - **栈内存 40.3 MB, 栈/堆比 0.66 : 1** (Linux 上堆是主项)
   - 进程 VmRSS 稳态约 **87 MB** (每连接约 8.7 KB)
   - 6 轮重复离散度 < 0.3%, 可作基线
5. **一处初版结论已被云端数据推翻并修正**: Windows 上每 goroutine 栈 8219 B,
   Linux 上仅 2112 B, 相差 3.89 倍。初版据 Windows 数据得出"栈是堆的 2.47 倍,
   百万连接需 23 GB, 应优先减少 goroutine 数", 该判断在 Linux 上不成立:
   实际百万连接约需 **10.3 GB 且堆占 60%**, 优先级回到堆分配本身。
6. **与 smart-socket 相比, shark-socket 的资源模型有一个结构性差异** (第 4 节):
   每连接 2 个 goroutine (readLoop + writeLoop) 对 1 个 (AIO 事件驱动);
   帧解码每次分配 payload, AIO 路径为缓冲区复用。这是零拷贝重构
   (第 3.4/3.6 节) 真正要解决的问题, 现有数据为该重构提供了量化基准。

---

## 2. 项目定位对比

| 维度 | smart-socket | shark-socket |
|---|---|---|
| 语言/运行时 | Java, JDK NIO.2 (AIO) | Go 1.26.9, goroutine + netpoll |
| 定位 | 通信微内核, 单协议 (TCP) | 多协议网关 (TCP/UDP/WS/HTTP/gRPC-Web/QUIC/CoAP/LWM2M) |
| 核心规模 | 约 2000 行 | 生产 11804 行, 测试 20174 行 |
| 扩展方式 | 实现 2 个接口 (Protocol, MessageProcessor) | 插件链 + 会话契约 + 传输层 |
| IO 模型 | JDK AIO 规范, 单线程事件驱动 | 每连接 2 goroutine, 工作池处理消息 |
| 内核依赖 | 100% 遵循 JDK AIO 接口规范, 自实现替换 | 无 (标准库 net + 少量传输库) |
| 目标场景 | 百万级长连接, IM/RPC/IoT | 边缘/受限节点, 多协议统一接入 |
| 资源观 | 强调"更少资源开销, IO 线程 0 感知" | 原无资源数据, 本报告补齐 |

**代码规模对比的意义**: smart-socket 用 2000 行换性能, 代价是只能做一种 IO 模型;
shark-socket 用 11804 行覆盖 8 种协议。行数不是优劣, 但它解释了为什么
shark-socket 在资源管理上必然更复杂 -- 8 个传输各自持有会话状态, 没有统一的
常驻内存策略。

---

## 3. shark-socket 原有数据的缺口

### 3.1 已有数据 (无需重采的部分)

以下数据有效且可重复, 本报告直接引用:

| 指标 | 数值 | 来源 |
|---|---|---|
| TCP 延迟 (8c) | 18,881 ns/op | BENCHMARK-260614 |
| TCP 吞吐 (50 conn) | 316,375 msg/s | BENCHMARK-260614 |
| TCPConnections | 126,925.7 msg/s, P99 1.18ms | CLOUD-VALIDATION-261016-SRV23 |
| HTTPRequests | 69,088.0 msg/s, P99 3.20ms | 同上 |
| 弹性池 vs 固定池 | 12000/s 供给下多投递 54.0% | 同上 |
| 插件链开销 | < 5% (4 插件仅 +1.7%) | BENCHMARK-260614 |

### 3.2 缺口一: 无任何资源占用数据

`BENCHMARK-260614-102513.md` 第六节名为"云服务器资源使用", 但记录的是
MemAvailable 与 Load1 -- 这两个是**主机级**指标, 度量的是"这台机器还剩多少资源",
不是"被测程序用了多少"。以该节 Server1 一行为例:

```
medium: MemAvailable 1210 MB -> 1212 MB, Load1 0.35 -> 0.50
```

MemAvailable 在测量前后**上升了 2 MB**, 这既可能是程序释放了内存, 也可能只是
其他进程的退出。无法从中分离出 shark-socket 的占用。

**缺失的量**: 进程 RSS、堆常驻量、每连接内存、goroutine 数、栈内存、GC 次数与暂停、
分配次数。这些都无法从 ns/op 或 msg/s 推导。

### 3.3 缺口二: 设计文档已要求补测但未落地

`REARCHITECTURE-261016-v2.md` 10.3 第 4 条:

> 增加可测量性, 这是唯一应该进代码的部分: `scripts/run_benchmarks.go`
> 输出 `runtime.ReadMemStats` 的 `HeapAlloc` / `HeapInuse` / `NumGC` /
> `PauseTotalNs` / `Mallocs`, 并新增 10.5 的常驻内存基准。
> **GC 行为必须可测量, 不能靠猜。**

10.5 补充基准列了四项。经全文检索 (`10kIdleConnections` / `PacketLifecycle` /
`FrameSplitAcrossChunk` / `MemStats`), 四项**在代码树中均无对应实现**, 全部只存在于
设计文档。第 1955 行与第 2039 行两处自记该缺口未闭合。

### 3.4 缺口三: 一处架构前提需修正

设计文档 10.5 的四项基准是按**尚未实现的零拷贝设计**写的:

| 设计文档设想 | 当前代码实况 |
|---|---|
| `ReadChunkSize` 块尺寸选项 (4.2) | `tcp.Options` 无此字段, 块尺寸尚未解耦 |
| Chunk slab 引用计数 (3.1) | 未实现, 无 Packet 引用计数 |
| 跨块窗口解码 (3.3) | 未实现, 当前用 `io.ReadFull` 直接读满帧 |
| 每连接预留 MaxFrame (3.4 缺陷) | 未出现 -- 现状是**按帧分配**, 不是预留 |

因此直接照抄 10.5 会写出无法编译的基准。本报告按**当前真实实现**落地, 断言按实测
基线设定; 零拷贝目标仍由 7.1 节的 B/op 目标承载, 两套断言互不干扰。

---

## 4. 与 smart-socket 的资源模型对比

smart-socket 的公开数据 (来自其 README 与作者博客, 均为 **Java/i7-4790 3.6GHz/8G**
环境, 与本机不可直接比较):

| smart-socket 公开数据 | 数值 |
|---|---|
| 消息吞吐 (标题党口径) | 500W msg/s; 按流入+流出累加约 1000W msg/s |
| 一分钟流量 | 流入 7064 MB, 流出约 7064 MB |
| 消息数 (一分钟) | 224,484,842 条, 单条 33 B |
| 长连接建立 | 8000 连接/秒, 总连接数 18 万 (8 核 E3-1231 v3 / 16 G) |
| 内存占用 | **官方未公布具体数值** |

**关键事实: smart-socket 同样没有公开的内存占用数值。** 它宣传 "更少的资源开销"
与 "IO 线程 0 感知", 但未给出可核对的 MB/连接 数据。因此第 6 节的对比只能落在
**结构层面** (每连接多少 goroutine、解码是否分配), 无法落在数值层面 --
后者的差距是"双方都缺", 不是 shark-socket 单方面缺失。

### 4.1 每连接 goroutine 数 (结构性差异)

| 维度 | smart-socket | shark-socket |
|---|---|---|
| 每连接 goroutine | 0 (AIO 完成事件回调, 复用 IO 线程) | 2 (readLoop + writeLoop) |
| 实测 (10k 连接, Linux) | 不适用 | **2.001 goroutine/连接** |
| 栈内存 (Linux) | 由 IO 线程栈承担 | 40.3 MB (每 goroutine 2.1 KB) |
| 栈占总量比例 | - | 40% (40.3 MB 堆外 + 62.2 MB 堆) |

这是 shark-socket 与 smart-socket 最大的结构性差异, 但**在 Linux 上它不是内存主项**:
10k 连接时栈 40.3 MB 小于堆 62.2 MB。smart-socket 的零 goroutine 模型省下的是
每连接 4.2 KB, 即总占用的约 40%。数量级上这是显著的, 但它不是"决定百万连接能否
落地"的那一项 -- 堆分配才是。详见第 5.2 节。

### 4.2 帧解码分配

smart-socket 通过 AIO 直接读入缓冲区, 解码后把缓冲交还 (零拷贝是其设计前提)。

shark-socket 现状 (`internal/transport/tcp/framer.go:45`) 每帧一次 `make([]byte, raw)`:

| payload | ns/op | B/op | allocs/op |
|---|---|---|---|
| 64 B | 51.0 | 68 | 2 |
| 1 KB | 571.7 | 1028 | 2 |
| 4 KB | 2115 | 4100 | 2 |

2 allocs/op = 1 次 payload 分配 + 1 次 4 字节 header 因取址逃逸。后者是一个
**可低成本消除的固定开销**: `framer.go:33` 的 `var header [4]byte` 被传给
`io.ReadFull`, 因此逃逸到堆。改为从池中取或复用会话内缓冲即可去掉。

第 7.1 节把 65507 场景的 B/op 目标定为 < 8 KiB, 前提正是消除这两次复制。

---

## 5. 本次补齐的资源占用数据

基线以 **Linux 云主机 (2 台, 阿里云, 各 2 vCPU / 3622 MB, Xeon 6982P-C,
Ubuntu 26.04.1, go1.26.9)** 为准, Windows 数据降为对照。

选 Linux 作基线是必要的: goroutine 初始栈在两个平台差 3.89 倍, 用 Windows 数字
外推生产容量会高估近 4 倍。第 5.2 节记录了这个修正。

两台同规格同地域, 结论取两台一致值; 10k 档另做 3 轮重复 (见 5.6 节)。

### 5.1 每空闲连接常驻堆 (新增基准 `BenchmarkTCPIdleConnectionFootprint`)

TCP 空闲连接, `ReadTimeout=0` 关闭空闲回收, 避免测量中途连接被释放。

| 连接数 | 常驻堆/连接 (srv2) | (srv3) | goroutine/连接 | GC 周期 |
|---|---|---|---|---|
| 100 | 7,454 B | 7,208 B | 2.080 | 2 |
| 1,000 | 6,266 B | 6,291 B | 2.008 | 3-4 |
| 10,000 | **6,219 B** | **6,157 B** | 2.001 | 7-8 |

**6,219 B/连接** (Linux) 是当前实现的真实常驻堆开销, 由会话结构体 +
`writeCh` channel (容量 128, 每槽一个 slice header 24 B, 约 3 KB) + 会话元数据构成。

1000 与 10000 两档相差 0.75% 以内, **线性且稳定**, 可直接外推。
100 档偏高是样本太小时 listener 与 harness 的固定开销摊薄不足, 不改变斜率。

两台独立测得 6,219 与 6,157 B (差 1.0%), 属同构机器正常波动。

Windows 对照值 6,647 B 与 Linux 6,219 B 相差 6.6%: 堆部分基本可移植,
说明堆占用由代码结构决定而非平台。这与栈部分的巨大差异 (5.2 节) 形成对照。

### 5.2 goroutine 与栈内存 (Linux 基线, 修正 Windows 结论)

| 连接数 | goroutine/连接 | 总 goroutine | stackInuse (Linux) | stackInuse (Windows 对照) |
|---|---|---|---|---|
| 100 | 2.080 | 208 | 0.78 MB | 1.93 MB |
| 1,000 | 2.008 | 2008 | 4.35 MB | 16.71 MB |
| 10,000 | 2.001 | 20008 | **40.31 MB** | 164.46 MB |

goroutine/连接 在两个平台都收敛到 2.0, 证实每连接恰有 readLoop + writeLoop 两环。

**每 goroutine 栈字节数**:

| 平台 | stackInuse / goroutine | 说明 |
|---|---|---|
| Linux (2C) | **2,112 B** | Go 的 `_StackMin` = 2 KB, 空闲读循环不增长 |
| Windows | 8,219 B | Windows 上 goroutine 栈按 8 KB 预留 |

同一份代码、同一个 Go 版本, 两个平台每 goroutine 相差 **3.89 倍**。

**因此本报告初版基于 Windows 得出的"栈内存是堆的 2.47 倍, 百万连接需要 23 GB"
这一结论不成立。** Linux 上栈/堆比为 **0.66 : 1** (40.31 MB vs 62.2 MB),
栈是次要项, 堆才是主项。初版据 Windows 数据把优化优先级指向"减少 goroutine 数",
这个判断在 Linux 上是错的。

按 Linux 实测重算外推 (6,219 B/连接 堆 + 2.1 KB x 2 栈):

| 目标连接数 | 堆 | 栈 | 合计 | 每连接 |
|---|---|---|---|---|
| 1 万 | 62 MB | 40 MB | ~102 MB | 10.2 KB |
| 10 万 | 622 MB | 403 MB | ~1.03 GB | 10.3 KB |
| 100 万 | 6.22 GB | 4.03 GB | **~10.3 GB** | 10.3 KB |

修正后的结论: **百万连接约需 10.3 GB, 堆占 60% (初版说 29%)**。
优化优先级回到堆分配 (每帧一次 `make`), 即零拷贝重构本身要解决的问题;
"每连接 2 goroutine" 降为次级项 (省 2.1 KB/连接, 不到总量两成)。

上表未计入内核态 socket 缓冲 (`net.Conn` 读写缓冲在 Go 堆之外),
真实进程占用高于此。实测进程 VmRSS 见 5.5 节。

### 5.3 帧分片解码 (新增基准 `BenchmarkFrameSplitAcrossSegments`)

512 B 帧被拆成多个小段送达, 模拟慢速/分片网络:

| 分片大小 | ns/op | B/op | allocs/op |
|---|---|---|---|
| 1 B | 1802 | 564 | 3 |
| 7 B | 485.6 | 564 | 3 |
| 64 B | 169.1 | 564 | 3 |

`io.ReadFull` 保证帧完整, 分片正确性由基准内的 `len(got) == len(payload)` 断言
守护。成本随分片数线性上升, 符合"每分片一次 Read 调用"的预期。3 allocs/op
= payload 1 次 + header 2 次 (4 字节 header 与 4 字节长度变量均逃逸)。

### 5.4 写路径拷贝成本 (`BenchmarkSendCopy`)

| payload | ns/op | B/op | allocs/op |
|---|---|---|---|
| 64 B | 37.0 | 64 | 1 |
| 1 KB | 351.7 | 1024 | 1 |
| 4 KB | 711.2 | 4096 | 1 |

`Send` 契约要求拷贝, 使调用方可在返回后立即释放缓冲区。这是零拷贝写
(设计文档 3.6) 必须放弃的保证, 上述数字即为放弃它的价格。

### 5.5 MemStats 可测量性与进程 RSS (10.3 第 4 项)

新增 `snapshotMem` / `reportMemStats`, 在每个资源基准内输出增量与绝对快照:

```
MEMSTATS idle/10000conns delta heapAlloc=+56271608B heapInuse=+62201856B
  totalAlloc=+69670360B mallocs=+471527 numGC=+8 pauseTotalNs=+248427ns
  goroutines=+20000
```

同时输出绝对快照 (含 `heapSys` / `heapReleased` / `stackInuse`), 因为 GC 行为
不能只看堆 -- 一次 `HeapInuse` 平坦而 `TotalAlloc` 上升的运行意味着"常驻稳定但
churn 很高", 只报堆会把它读成干净结果。

**Linux 云端实测 GC 表现** (10k 连接建立窗口): 8 次 GC, 暂停总时长 **248 us**,
`mallocs` +471527, `totalAlloc` +69.7 MB。GC 压力在建连阶段很轻, 这是空闲连接
的预期形态 (无业务流量, 无 churn)。

**进程级 VmRSS** (`/proc/<pid>/status`, Go 堆之外的数据):

| 主机 | 轮次 | peak VmRSS | 说明 |
|---|---|---|---|
| srv2 | 1 | 183,784 kB | 含建连期临时对象 |
| srv2 | 2 | 87,032 kB | 稳态 |
| srv2 | 3 | 88,160 kB | 稳态 |
| srv3 | 1 | 175,200 kB | 含建连期临时对象 |
| srv3 | 2 | 90,076 kB | 稳态 |
| srv3 | 3 | 85,420 kB | 稳态 |

稳态 VmRSS 约 **85-90 MB**, 而 Go 堆占 62-64 MB, 栈占 40 MB。
两者相加已超过 VmRSS, 因为 `stackInuse` 统计的是栈span 总量而非已提交页,
未使用的部分并未驻留。这解释了为什么 MemStats 各分项相加会超过进程 RSS:
**三个口径不能简单相加**。

VmRSS 才是部署时应看的数字: 10k 连接的实际进程占用约 87 MB,
即每连接约 **8.7 KB** (含内核态 socket 缓冲), 低于 5.2 节按 MemStats 推算的
10.3 KB。

### 5.6 重复性

10k 档在两台主机各跑 3 轮 (`-count=3`, 每轮重启进程):

| 指标 | srv2 三轮 | srv3 三轮 | 波动 |
|---|---|---|---|
| 常驻堆/连接 | 6,311 / 6,305 / 6,293 B | 6,302 / 6,311 / 6,310 B | < 0.3% |
| stackInuse | 42.27 / 42.27 / 42.30 MB | 42.24 / 42.24 / 42.27 MB | < 0.2% |
| goroutine/连接 | 2.001 x3 | 2.001 x3 | 0 |
| numGC | 7 / 7 / 7 | 7 / 7 / 7 | 0 |

6 轮读数的最大离散度 0.3%, 且 srv2 与 srv3 之间差 1.0%。**资源基线是可重复的**,
这与项目历史记录的 ns/op 类数据 (偏差 < 1.5%) 同一量级, 因此可直接作为基线使用。

唯一显著波动项是 VmRSS (175-184 MB vs 85-90 MB), 原因是建连期临时对象是否
被回收的时机不同。**读 VmRSS 应在稳态下读, 建连过程中读会高估 2 倍。**

---

## 6. 本次改动

| 文件 | 改动 |
|---|---|
| `tests/benchmark/bench_resource_test.go` | 新增。4 个基准 + `snapshotMem`/`reportMemStats` + 64 KiB/连接 预算守卫; 阶梯可用 `BENCH_IDLE_CONNS` 覆写 |
| `scripts/run_benchmarks.go` | 新增 `iterations` 字段 (支持 `-benchtime=1x`); 注册 2 个资源基准组; `-list` 显示真实 benchtime |
| `README.md` / `README.zh-CN.md` | 资源占用段落 (按云端 Linux 数据) |
| `docs/reports/README.md` | 索引登记 + 资源数据归属说明 |

验收: `go vet ./tests/benchmark/` 通过, `gofmt -l` 为空,
两台云主机上 `go build ./...` 与 `go vet ./tests/benchmark/` 均通过,
`run_baseline.sh` 与 `run_ladder.sh` 端到端 rc=0。

**守卫的取向**: 预算断言设在 64 KiB/连接, 约为 Linux 实测 6,219 B 的 10.5 倍
(Windows 实测 6,647 B 的 9.8 倍)。刻意不贴着实测值设阈值 -- 过紧的守卫会在
Go 版本或栈初值变化时反复误报, 而这类噪声会让守卫整体被忽略。这个余量仍能拦住
第 4.2 节所述的"每连接预留 1 MB MaxFrame"量级回归 (那会使 10k 连接变成 10 GB)。

---

## 7. 复现方式

云端 (Linux, 本报告第 5 节数据来源):

```bash
# 驱动脚本位于 .workbuddy/cloud/ (gitignore 内), 经 SFTP 上传后远端执行
bash /root/run_baseline.sh   # LADDER=100,1000,10000 COUNT=3, 采样 VmRSS
bash /root/run_ladder.sh     # 完整阶梯 + stackInuse 逐档读数
```

等价的直接命令:

```bash
export PATH=/usr/local/go/bin:$PATH GOPROXY=https://goproxy.cn,direct GOSUMDB=off
go test -run='^$' -bench='BenchmarkTCPIdleConnectionFootprint' \
  -benchmem -benchtime=1x -count=1 -v ./tests/benchmark/
go test -run='^$' \
  -bench='BenchmarkReadLoopFrameAllocation|BenchmarkSendCopy|BenchmarkFrameSplitAcrossSegments' \
  -benchmem -benchtime=2000x -count=1 ./tests/benchmark/
```

本机 (Windows, 仅作平台对照):

```bash
go run scripts/run_benchmarks.go -bench resource -profile local
```

日志: `logs/2026-10-17T12-24-01_bench_resource-idle-footprint.log`

---

## 8. 后续建议

优先级已按云端 Linux 数据重排 (初版据 Windows 数据的排序不再成立):

| 优先级 | 事项 | 依据 |
|---|---|---|
| P0 | 消除 framer 的 4 字节 header 逃逸 | 每帧固定省 1 次分配, 见 4.2 |
| P0 | 推进零拷贝解码 (每帧一次 `make` 是堆主项) | 堆占总量 60%, 见 5.2 修正结论 |
| P1 | 降低 `writeCh` 容量 (默认 128 槽约 3 KB/连接) | 占 6219 B/连接 的近一半, 见 5.1 |
| P1 | 评估每连接 2 goroutine 的模型 | 省 4.2 KB/连接, 约占总量 40%, 见 4.1 |
| P2 | 按实测斜率建立容量规划表 | 见 5.2 外推表 (百万连接约 10.3 GB) |
| P2 | 补资源基准进 CI | 目前只在 `.workbuddy/cloud/` 手工跑, CI 不跑 |

**已完成**: "在 Linux 上重采本基线" 已于本轮完成 (5.6 节, 6 轮重复离散度 < 0.3%)。

**不建议**做的事: 不应把 shark-socket 与 smart-socket 的 msg/s 数字并列排名。
两者协议栈、IO 模型、语言运行时、测试负载全部不同, 数字对比没有信息量。
smart-socket 自身也未公布内存数值 (第 4 节), 因此不存在可核对的横向基准。
若需横向对比, 唯一严谨的做法是在同一台机器上分别跑两者的官方压测程序。

---

## 9. 数据可信度与边界

- **第 5 节数据来自两台阿里云 Linux 主机的 6 轮独立运行**, 离散度 < 0.3%,
  可重复性已验证 (见 5.6)。两台主机规格相同, 差异 1.0%, 属同构机器正常波动。
- 2 vCPU / 3622 MB 是**小型规格**。10k 连接的 40 MB 栈与 62 MB 堆在该规格下
  未触及压力点, 因此数据可外推到更大连接数; 但若某项行为依赖核数 (如 GC
  并行度), 2C 与生产常见的 8C+ 不等价。
- 栈内存的平台差异是本轮最重要的方法论结论: **Windows 与 Linux 每 goroutine
  相差 3.89 倍** (8219 B vs 2112 B)。任何资源基线若在 Windows 上采集,
  栈相关的外推都不能用于 Linux 部署。
- VmRSS 有明显的采样时机敏感性: 建连期读到 175-184 MB, 稳态读到 85-90 MB,
  相差 2 倍。**应在稳态读 VmRSS**。
- MemStats 各分项 (HeapInuse / stackInuse) 不能相加后与 VmRSS 比对:
  `stackInuse` 含未提交的栈 span, 相加会超过进程实际驻留 (40.3 + 62.2 > 87)。
- 5.1 的常驻堆值不含内核态 socket 缓冲, 因此真实进程占用高于表中数字;
  部署应以 VmRSS 为准 (实测每连接约 8.7 KB)。
- 第 4 节的 smart-socket 数值来自其官方 README 与作者博客, 环境为
  i7-4790 3.6GHz / 8G, 与本项目无对应关系, 仅作**设计取向**对照, 未用于任何数值换算。
- 第 5.3/5.4 节的分配类基准为跨平台值 (由代码结构决定), 未在 Linux 复采;
  它们的量级 (每帧 2 allocs) 不受平台影响。