## 11. L2 层: WorkerPool 弹性伸缩

> **状态: 已并入 v2 主文档。** 本文件为原始独立版本, 内容与
> `REARCHITECTURE-261016-v2.md` 第 11 节一致。后续修订请改主文档,
> 本文件仅作留档。

### meta-info

version: 0.1
date: 2026-10-16
status: draft
scope: L2 运行时层增量设计 (补充 v2 第 5 节)

---

## 11.1 设计依据: 实测数据

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

环境 `120.76.44.233` (2 核 / 1.6GB / Go 1.26.9), 服务端与客户端分离进程, 丢弃率取服务端自报指标 `tcp_task_queue_full_total` (server.go:213), 不靠客户端推断。

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

---

## 11.2 现状约束: 为什么现在不能伸缩

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

## 11.3 设计: 可伸缩 WorkerPool

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
    defer p.wg.Done()
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

---

## 11.4 API 设计

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

对应 §11.3.3 的 `ScalablePool` 需要新增字段:

```go
scaleUpRatio   float64  // 默认 0.75
scaleDownRatio float64  // 默认 0.25
downStreak     atomic.Int32  // 连续低于下界的次数
```

**`WorkerPool` 与 `ScalablePool` 的关系**: 不让 `ScalablePool` 继承 `WorkerPool`。继承会让 `handle` / `Submit` 的 `submitMu` 语义被子类重写, 是脆弱基类。两者共享一个未导出的 `poolCore` (含 queue / handler / policy / submitMu / done / handle), 各自组合。

---

## 11.5 必须保持的现有语义

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

## 11.6 一处顺带发现的问题 (与伸缩无关, 独立记录)

`tcp/server.go:54-60`:

```go
s.pool = shared.NewWorkerPool(...)
s.pool.Start(s.opts.WorkerCount)      // :55  先启动 worker
ln, err := net.Listen("tcp", s.opts.Addr)   // :56  后监听
if err != nil {
    s.pool.Stop()                // :59  失败才 Stop
```

worker 在 `net.Listen` **之前**启动。若 listen 成功但后续步骤失败并重试 `Start`, `s.pool` 会被 `NewWorkerPool` 重新赋值, 而旧的 pool 的 worker goroutine 仍在运行且**永远等不到 `Stop`** (没有任何句柄指向它了)。

这不是伸缩引入的, 是既有问题, 但伸缩会放大它 —— 每个泄漏的 pool 都带着一整组 worker。修复方向: `Start` 开头先 `Stop` 旧 pool, 或把 `NewWorkerPool` 移到 listen 成功之后。

---

## 11.7 测试计划

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

---

## 11.8 实施位置

**L2 依赖 M1 (kernel.ConnState) 吗? 不依赖。** `ScalablePool` 只用 `core.Handler` / `core.Session` / `core.Message`, 全是既有稳定契约。

因此可以**独立于 M0-M9 主序列执行**, 建议编号:

```
L2-a  poolCore 抽取 + ScalablePool 骨架 (纯新增, 不改WorkerPool)
L2-b  workerRef 身份 + run() 退休分支
L2-c  shouldScale + Submit 内取模触发
L2-d  伸缩行为测试 + race 检测
L2-e  tcp.Option 接线 (WithElasticPool / WithScaleRatio)
```

L2-a 到 L2-d 全部是纯新增, 不修改任何现有文件, 符合 v2 §7 的 "任何提交保持 CI 绿灯" 原则。L2-e 改 `tcp/options.go` 一个文件。

**可以与 M1/M2/M3 并行。**

---

## 11.9 预期效果与局限

**预期**: 在 handler 耗时波动时, 有效吞吐天花板从固定的 `worker 数 / handler 耗时` 变为 `maxWorker / handler 耗时`。按 11.1.2 的数据, 4 worker 配 1ms handler 天花板是 4000 msg/s; 若 max 设为 16 则变为 16000 msg/s, 且低谷时自动回落。

**局限, 必须承认**:

1. **不解决跨 session 饿死**。见 11.5 最后一行。
2. **不解决 PolicyBlock 的反压风险**。伸缩让池不容易满, 但一旦满了, 阻塞读 goroutine 的行为不变 (11.1.1 已实测)。缓解此风险需改 Submit 的调用方式 (如在独立 goroutine 里 Submit), 那是 M5 的议题。
3. **扩容有延迟**。取模触发 + goroutine 创建, 从突发到 worker 就绪有毫秒级延迟。突发持续时间短于该延迟时, 伸缩帮不上忙。
4. **2 核环境下 maxWorker 的意义有限**。本次压测机只有 2 核, 真实效果需在多核机器复测。本节数据只能证明机制正确, 不能作为性能上限的定量依据。
5. **参数需要按业务调**。`up/down` 阈值与 `min/max` 没有通用最优值, §11.4 的默认值只保证不会抖动, 不保证最优。