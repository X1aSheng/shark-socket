# GATEWAY.md

> Shark-Socket 运行时层详细设计  
> 版本:v0.2.x-alpha  
> 最后更新:2026-06-01

---

## 目录

1. [概述](#1-概述)
2. [Gateway](#2-gateway)
3. [SessionManager 实现](#3-sessionmanager-实现)
4. [PluginChain 实现](#4-pluginchain-实现)
5. [WorkerPool 实现](#5-workerpool-实现)
6. [Runtime 实现](#6-runtime-实现)
7. [应用装配层](#7-应用装配层)

---

## 1. 概述

`internal/runtime/` 是**运行时层**,负责:

- **Gateway**:多协议编排,Runtime 注入,分阶段关闭
- **SessionManager**:跨协议会话注册,查询,广播
- **PluginChain**:插件链排序,执行,panic 隔离(实现 `core.PluginRunner` 契约)
- **Runtime**:`core.Runtime` 的默认实现,持有会话管理器与插件链
- **WorkerPool**:消息处理并发控制,位于 `internal/transport/shared`,由三个服务端共享

**所有权原则:**

```
Gateway 创建并持有:
  +-- Runtime(组装并共享以下成员)
  |   +-- SessionManager(跨协议共享,经 metricSessionManager 装饰)
  |   +-- PluginChain(跨协议共享)
  |   +-- Logger(全局)
  |   +-- Metrics(全局)
  |   +-- Tracer(全局)
  +-- map[Protocol]Server(各协议实例)

协议 Server 通过 RuntimeConfigurable.UseRuntime 接收 Runtime,不自行创建全局资源
```

---

## 2. Gateway

### 2.1 结构定义

```go
// internal/runtime/gateway.go
type Gateway struct {
    mu        sync.RWMutex                 // 保护 servers / order
    startMu   sync.Mutex                   // 串行化 Start,Stop 与 Register
    servers   map[core.Protocol]core.Server
    order     []core.Protocol              // 保持注册顺序,不影响功能但便于调试
    rt        *Runtime                     // 共享运行时依赖容器
    timeouts  core.StageTimeouts           // 三阶段关闭各自独立超时

    startedAt atomic.Pointer[time.Time]    // nil 表示未启动或已停止
    started   atomic.Bool
}
```

`startedAt` 用 `atomic.Pointer[time.Time]` 而非记录时间戳加布尔量,是为了让 `Health()`
在关停窗口内不会同时报出 `started=false` 与一个活的 `uptime`.

### 2.2 构造与 Server 注册

```go
// internal/runtime/gateway.go
func NewGateway(opts ...GatewayOption) *Gateway {
    cfg := gatewayConfig{
        sessions: NewSessionManager(),
        plugins:  NewPluginChain(),
        logger:   core.NopLogger(),
        metrics:  core.NopMetrics(),
        tracer:   core.NopTracer(),
        timeouts: defaultStageTimeouts(),
    }
    for _, opt := range opts {
        opt(&cfg)
    }
    // 插件链要拿到网关的 logger 才能记录 panic.
    if chain, ok := cfg.plugins.(*PluginChain); ok {
        chain.SetLogger(cfg.logger)
    }
    // 装上指标装饰器:各传输层的会话注册 / 注销自动汇入配置的
    // metrics 后端(如 Prometheus),无需每个 Server 自行埋点.
    sessions := cfg.sessions
    if cfg.metrics != nil {
        sessions = &metricSessionManager{SessionManager: cfg.sessions, metrics: cfg.metrics}
    }
    return &Gateway{
        servers:  make(map[core.Protocol]core.Server),
        rt:       NewRuntime(sessions, cfg.plugins,
            withRuntimeLogger(cfg.logger),
            withRuntimeMetrics(cfg.metrics),
            withRuntimeTracer(cfg.tracer)),
        timeouts: normalizeTimeouts(cfg.timeouts),
    }
}

// Register 注册协议 Server,重复 Protocol 返回 core.ErrDuplicateProtocol.
func (g *Gateway) Register(server core.Server) error {
    if server == nil {
        return core.ErrNoServers
    }
    g.startMu.Lock()
    defer g.startMu.Unlock()
    if g.started.Load() {
        return fmt.Errorf("gateway: cannot register %q after Start", server.Protocol())
    }
    g.mu.Lock()
    defer g.mu.Unlock()
    proto := server.Protocol()
    if _, exists := g.servers[proto]; exists {
        return core.ErrDuplicateProtocol
    }
    g.servers[proto] = server
    g.order = append(g.order, proto)
    return nil
}

// Runtime 返回网关持有的运行时,供 application 层组装其它组件.
func (g *Gateway) Runtime() *Runtime { return g.rt }
```

`startMu` 同时保护 `Start`、`Stop` 与 `Register`:注册在启动后被拒绝,启动与关停不会交错.
遍历服务列表一律经 `snapshot()`,在锁内构造切片后在锁外调用 `srv.Start/Stop`,避免持有
`mu` 期间调用外部代码.

### 2.3 Start 流程

详见 `LIFECYCLE.md sec.3.2 启动流程`.实际顺序:

1. `startMu` 加锁,若已启动返回错误;`snapshot()` 取服务列表,空列表返回 `core.ErrNoServers`.
2. 对每个实现 `core.RuntimeConfigurable` 的 Server 调用 `UseRuntime(g.rt)` -- 注入必须发生在
   任何 `Start` 之前,Server 之后才能持有共享会话管理器与插件链.
3. `g.rt.Plugins().StartAll()`:启动所有实现 `core.LifecyclePlugin` 的插件.失败即返回,
   此时还没有任何 Server 启动过,`StartAll` 自身回滚已启动的插件.
4. 按注册顺序逐个 `srv.Start(ctx)`;任一失败则以 `Finalize` 超时构造回滚上下文,
   **逆序**停止已启动的 Server,随后 `Plugins().StopAll()`,最后返回原始错误.
5. 全部成功后 `startedAt.Store(&now)` 与 `started.Store(true)`,`Ready()` 开始返回 true.

插件先于 Server 启动,是为了确保自动封禁、心跳扫荡、集群消费等后台循环在第一条消息到达前
就已在运行;关停则相反(见 2.4).

### 2.4 Stop 流程

详见 `LIFECYCLE.md sec.3.3 停止流程`.三个阶段 + 一个收尾,阶段内对全部 Server 并发:

```
1. 立即 started.Store(false)
     让整个关停窗口内 Ready() 为 false,Register() 被拒绝

2. StopAccept     (core.StageTimeouts.StopAccept,默认 5s)
     停止接受新连接

3. Drain          (默认 5s)
     等待在途请求处理完

4. CloseSessions  (默认 10s)
     关闭剩余会话;未实现 StagedServer 的 Server 在此阶段整体 Stop
     随后 rt.Sessions().CloseAll(ctx) 兜底

5. 收尾
     rt.Plugins().StopAll()
     插件后台循环在全部会话关闭之后才停止,不会与传输层拆除竞争
     startedAt.Store(nil)
```

- 阶段超时彼此独立(`core.StageTimeouts`),某一阶段超时不会缩短后续阶段的预算.
- 首个错误被保留并作为 `Stop` 的返回值,但不中断后续阶段;`Plugins().StopAll()` 的失败也
  会计入返回值,关停故障不会被静默丢弃.
- `Stop` 幂等,且与 `Start` / `Register` 通过 `startMu` 互斥.

### 2.5 GatewayOption 与阶段超时

```go
// internal/runtime/options.go
type gatewayConfig struct {
    sessions core.SessionManager
    plugins  core.PluginRunner
    logger   core.Logger
    metrics  core.Metrics
    tracer   core.Tracer
    timeouts core.StageTimeouts
}

type GatewayOption func(*gatewayConfig)

func WithSessionManager(manager core.SessionManager) GatewayOption  // nil 忽略
func WithPlugins(plugins ...core.Plugin) GatewayOption              // 构造 NewPluginChain
func WithLogger(logger core.Logger) GatewayOption                   // nil 忽略
func WithMetrics(metrics core.Metrics) GatewayOption                // nil 忽略
func WithTracer(tracer core.Tracer) GatewayOption                   // nil 忽略
func WithStageTimeouts(timeouts core.StageTimeouts) GatewayOption   // <=0 字段回落默认值
```

```go
// internal/core/observability.go
type StageTimeouts struct {
    StopAccept    time.Duration
    Drain         time.Duration
    CloseSessions time.Duration
    Finalize      time.Duration // 启动失败时回滚 Stop 的预算
}
```

默认值(`defaultStageTimeouts`)为 `StopAccept=5s`、`Drain=5s`、`CloseSessions=10s`、
`Finalize=2s`;`normalizeTimeouts` 把任一非正字段替换为对应默认值,因此零值 `StageTimeouts`
等价于默认配置.没有"总超时按比例拆分"的机制:每个阶段各自计时,总时长上限由调用方传入的
`ctx` 决定.

### 2.6 启动与关停的调用方

`Gateway` 不提供内建的 `Run` 便捷方法,也不自行处理信号.信号监听、健康/指标 HTTP 服务的
生命周期都在 `cmd/shark-socket/main.go` 与 `internal/app` 中:

```go
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
defer stop()

if err := gateway.Start(ctx); err != nil { ... }
<-ctx.Done()
_ = gateway.Stop(shutdownCtx) // shutdownCtx 由 app 依配置超时构造
```

### 2.7 健康与就绪接口

```go
// Ready 只表示网关已完成启动;不代表外部依赖健康.
func (g *Gateway) Ready() bool { return g.started.Load() }

func (g *Gateway) Health() map[string]any {
    started := g.started.Load()
    resp := map[string]any{
        "started":  started,
        "sessions": g.rt.Sessions().Count(),
    }
    // 只在已启动时报告启动时间与 uptime,避免关停期间出现
    // started=false 却带一个活的 uptime.
    if started {
        if startedAt := g.startedAt.Load(); startedAt != nil {
            resp["started_at"] = startedAt.Format(time.RFC3339Nano)
            resp["uptime"] = time.Since(*startedAt).String()
        }
    }
    resp["protocols"] = g.Protocols()
    return resp
}

func (g *Gateway) Protocols() []core.Protocol // 按注册顺序返回副本
```

`Health()` 返回 `started` 布尔量而不是 `status` 字符串,`readyz` 直接映射 `Ready()`.
`Protocols()` 返回副本,调用方修改不会影响网关状态.

---

## 3. SessionManager 实现

### 3.1 P0 实现(单锁,功能正确优先)

```go
// internal/runtime/session_manager.go
type SessionManager struct {
    mu       sync.RWMutex
    nextID   atomic.Uint64              // NextID 单调递增,无锁
    count    atomic.Int64               // Count 无锁读取
    max      int64                      // 0 或负数 = 不限制
    sessions map[uint64]core.Session
}

type SessionManagerOption func(*SessionManager)

func WithMaxSessions(max int64) SessionManagerOption

// 默认上限 100 万.
func NewSessionManager(opts ...SessionManagerOption) *SessionManager

// 编译期验证
var _ core.SessionManager = (*SessionManager)(nil)
```

单锁换来的是推理成本最低的正确性:所有变更路径共用一把 `RWMutex`,读多写少的会话表在此
规模下足够;分片方案见 3.4,只有在基准测试证明锁竞争成为瓶颈后才引入.

### 3.2 核心方法实现

```go
func (m *SessionManager) NextID() uint64 { return m.nextID.Add(1) }

func (m *SessionManager) Register(sess core.Session) error {
    if sess == nil {
        return core.ErrInvalidArgument
    }
    m.mu.Lock()
    defer m.mu.Unlock()
    if _, ok := m.sessions[sess.ID()]; ok {
        return core.ErrDuplicateSession   // 同 ID 重复注册是调用方的 bug,显式报错
    }
    if m.max > 0 && int64(len(m.sessions)) >= m.max {
        return core.ErrSessionCapacity
    }
    m.sessions[sess.ID()] = sess
    m.count.Add(1)
    return nil
}

// Unregister 返回是否真正删除了条目,便于调用方区分重复注销.
func (m *SessionManager) Unregister(id uint64) bool { ... }

func (m *SessionManager) Get(id uint64) (core.Session, bool) { ... }

func (m *SessionManager) Count() int64 { return m.count.Load() }
```

容量判断用 `len(m.sessions)` 而不是 `m.count`,因为前者在锁内一定是最新值;`count` 是给
无锁读路径用的计数器.`Register(nil)` 返回 `core.ErrInvalidArgument` 而不是 panic.

### 3.3 遍历与广播

```go
// Snapshot 在 RLock 内构造切片并立即释放锁.
func (m *SessionManager) Snapshot() []core.Session

// Range 在持读锁期间就地遍历,省掉一次全量快照分配.
// fn 不得调用会取写锁的方法(Register / Unregister),否则死锁.
func (m *SessionManager) Range(fn func(core.Session) bool) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    for _, sess := range m.sessions {
        if !fn(sess) {
            return
        }
    }
}

func (m *SessionManager) Broadcast(data []byte) error {
    // 在锁外 Send:某条会话的 Send 可能阻塞在 I/O,也可能经 Unregister
    // 重入本管理器,持读锁时会死锁.Snapshot 在 RLock 内完成拷贝.
    var firstErr error
    for _, sess := range m.Snapshot() {
        if err := safeSessionCall(func() error { return sess.Send(data) }); err != nil && firstErr == nil {
            firstErr = err
        }
    }
    return firstErr
}
```

`Broadcast` 只保留**首个**错误:广播的目标是尽力送达,汇总所有失败既无助于定位也无助于恢复.
与 `CloseAll` 中挂起的会话相比,单条会话发送失败不值得中断整轮广播.

```go
func (m *SessionManager) CloseAll(ctx context.Context) error {
    var firstErr error
    for {
        if ctx.Err() != nil {
            return firstErr
        }
        sessions := m.Snapshot()
        if len(sessions) == 0 {
            return firstErr
        }
        for _, sess := range sessions {
            if err := safeSessionCall(func() error { return sess.Close(ctx) }); err != nil && firstErr == nil {
                firstErr = err
            }
            m.Unregister(sess.ID())
        }
    }
}

// safeSessionCall 隔离用户会话实现的 panic,使 Broadcast / CloseAll 中
// 一次 Send/Close panic 不会带走整个进程.
func safeSessionCall(fn func() error) (err error) {
    defer func() {
        if r := recover(); r != nil {
            err = core.ErrHandlerPanic
        }
    }()
    return fn()
}
```

`CloseAll` 用"快照 -> 关闭 -> 注销 -> 再检查"的循环而非一次性快照:

- 关停期间新注册的会话不会被漏掉,循环直到表为空.
- `ctx` 到期立即退出,一个卡住的 `Close` 不会让关停无限自旋.
- 关闭在锁外进行,避免持写锁阻塞 I/O.
- 该实现同时替代了早期草案里"并发 goroutine + WaitGroup + ErrDrainTimeout"的写法:
  阶段超时由 `Gateway` 的 `CloseSessions` 预算统一表达,管理器不再自带第二种超时语义.

### 3.4 P2 演进方案(分片锁)

**触发条件:** benchmark 证明 `Register/Unregister/Get` 锁竞争为瓶颈.当前基准测试尚未显示
该瓶颈,因此以下方案仅为预案,未实现.

```go
// P2 预案(未实现)
type shardedManager struct {
    shards   [32]shard
    nextID   atomic.Uint64
    count    atomic.Int64
    max      int64
}

type shard struct {
    mu       sync.RWMutex
    sessions map[uint64]core.Session
    // P2:per-shard LRU 淘汰(benchmark 证明需要后引入)
}

// 分片函数:位运算替代取模
func shardIndex(id uint64) int {
    return int(id & 31)
}

func (m *shardedManager) Get(id uint64) (core.Session, bool) {
    s := &m.shards[shardIndex(id)]
    s.mu.RLock()
    defer s.mu.RUnlock()
    sess, ok := s.sessions[id]
    return sess, ok
}
```

分片后 `Snapshot` / `Range` / `Broadcast` / `CloseAll` 需按分片加锁,不能再依赖一把全局锁保证
一致性,这是迁移的主要成本.

**P0 -> P2 迁移策略:**

- `core.SessionManager` 接口不变
- 只替换 `internal/runtime/session_manager.go` 中的实现
- 迁移前必须有 P0 基准测试数据作为对比基线
- 迁移后 `go test -race ./...` 无竞争
- `CloseAll` 的"循环直到清空"语义必须保留

---

## 4. PluginChain 实现

插件链的执行器是 `internal/runtime/plugin_chain.go` 中的 `PluginChain`,它实现
`core.PluginRunner` 接口.插件注册由具体类型的 `Append` 完成,不在 `PluginRunner`
契约内.

### 4.1 与设计草案的差异

本节早期版本描述了一个 `pluginRunner` 类型(`plugin_runner.go`),该类型从未落地.
实际实现有意去掉了三处设计:

- **无 `nameIndex` 去重**:同名插件不再覆盖,`Append` 按调用顺序追加,名称唯一性由调用方负责.
- **无 `stopOnError` 开关**:插件返回的普通业务错误一律终止链并上抛给传输层,链内没有
  "记录 Warn 后继续"的分支,行为只有一种,便于推理.
- **无链内 metrics 埋点**:插件耗时与错误计数由插件自身及传输层上报,链只负责顺序与隔离.
- **排序改为 `slices.SortStableFunc`**:同优先级插件按 `Append` 顺序执行,多次运行与
  多平台之间行为一致.

### 4.2 结构定义

```go
// internal/runtime/plugin_chain.go
type PluginChain struct {
    mu      sync.RWMutex
    plugins []core.Plugin // 按 Priority 稳定升序
    logger  core.Logger
}

func NewPluginChain(plugins ...core.Plugin) *PluginChain

// 编译期验证
var _ core.PluginRunner = (*PluginChain)(nil)
```

### 4.3 Append 实现(copy-on-write)

```go
func (c *PluginChain) Append(plugins ...core.Plugin) {
    c.mu.Lock()
    defer c.mu.Unlock()
    // Copy-on-write: 构造新切片后整体换入,c.plugins 从不原地修改,
    // 因此 snapshot() 可以直接交出当前切片引用,无需逐条消息复制.
    next := append([]core.Plugin(nil), c.plugins...)
    for _, p := range plugins {
        if p == nil {
            continue
        }
        next = append(next, p)
    }
    slices.SortStableFunc(next, func(a, b core.Plugin) int {
        return a.Priority() - b.Priority()
    })
    c.plugins = next
}
```

copy-on-write 换来两件事:`OnMessage` 热路径零分配;插件在自己的
`OnAccept/OnMessage/OnClose` 回调里调用 `SetLogger` 或 `Append` 不会死锁
(RWMutex 不可重入).

### 4.4 snapshot(回调在锁外执行)

```go
func (c *PluginChain) snapshot() ([]core.Plugin, core.Logger) {
    c.mu.RLock()
    defer c.mu.RUnlock()
    return c.plugins, c.logger
}
```

每个钩子先取一次快照,随后全程在锁外调用插件代码.

### 4.5 OnAccept 实现

```go
func (c *PluginChain) OnAccept(sess core.Session) error {
    plugins, logger := c.snapshot()
    for i, p := range plugins {
        if err := c.safeAccept(p, logger, sess); err != nil {
            // 回滚:已成功 accept 的插件按逆序收到 OnClose,
            // 释放它们在 OnAccept 中分配的资源.
            for j := i - 1; j >= 0; j-- {
                c.safeClose(plugins[j], logger, sess)
            }
            return err
        }
    }
    return nil
}
```

链本身不调用 `sess.Close()`:关闭动作由传输层根据返回的错误决定,链只表达"拒绝"语义.
中途失败时逆序回滚是 V9 的修复项,此前已 accept 的插件收不到 `OnClose`,会泄漏其资源.

### 4.6 OnMessage 实现

```go
func (c *PluginChain) OnMessage(sess core.Session, data []byte) ([]byte, error) {
    plugins, logger := c.snapshot()
    var err error
    for _, p := range plugins {
        data, err = c.safeMessage(p, logger, sess, data)
        if err != nil {
            return nil, err
        }
    }
    return data, nil
}
```

非 nil 返回即作为下一个插件的输入(支撑 payload 改写链).任何错误立即终止链并返回
`nil` payload,由传输层区分 `ErrPluginDrop`(丢弃该消息,会话继续)与普通错误.

### 4.7 OnClose 实现

```go
func (c *PluginChain) OnClose(sess core.Session) {
    plugins, logger := c.snapshot()
    for i := len(plugins) - 1; i >= 0; i-- {
        c.safeClose(plugins[i], logger, sess)
    }
}
```

逆序执行且不可中断:某个插件 panic 后,序号更靠前的插件仍然会收到 `OnClose`.

### 4.8 panic 隔离(safe 包装)

五个钩子各有一个 `safe*` 包装,回调一律经由它们进入:

```go
func (c *PluginChain) safeAccept(p core.Plugin, logger core.Logger, sess core.Session) (err error) {
    defer func() {
        if r := recover(); r != nil {
            logger.Error("plugin accept panic", "plugin", p.Name(), "panic", r)
            err = core.ErrPluginPanic
        }
    }()
    return p.OnAccept(sess)
}
```

| 包装 | 覆盖钩子 | panic 后的行为 |
|------|----------|----------------|
| `safeAccept` | `OnAccept` | 返回 `ErrPluginPanic`,链逆序回滚 |
| `safeMessage` | `OnMessage` | 返回 `ErrPluginPanic`,链终止 |
| `safeClose` | `OnClose` | 记录日志后继续下一个插件 |
| `safeStart` | `Start` | 返回 `ErrPluginPanic`,`StartAll` 回滚 |
| `safeStop` | `Stop` | 返回 `ErrPluginPanic`,`StopAll` 继续 |

### 4.9 StartAll 与 StopAll

```go
func (c *PluginChain) StartAll() error {
    plugins, logger := c.snapshot()
    var started []core.Plugin
    for _, p := range plugins {
        if _, ok := p.(core.LifecyclePlugin); !ok {
            continue
        }
        if err := c.safeStart(p, logger); err != nil {
            var rollbackErr error
            for i := len(started) - 1; i >= 0; i-- {
                rollbackErr = errors.Join(rollbackErr, c.safeStop(started[i], logger))
            }
            return errors.Join(fmt.Errorf("plugin %q start: %w", p.Name(), err), rollbackErr)
        }
        started = append(started, p)
    }
    return nil
}
```

- `StartAll` 在任何服务开始 accept 之前由 `Gateway.Start` 调用,失败即拒绝启动网关.
- `StopAll` 逆序遍历,`errors.Join` 汇总每个插件的失败(不吞掉关停故障).
- 空链、未实现 `LifecyclePlugin` 的链、以及未启动就调 `StopAll` 都是安全 no-op.
- 回滚自身的失败与原始启动错误一起返回:启动失败后仍有插件在跑,这个事实必须对调用方可见.

详见 `PLUGIN.md sec.1.2.1` 与 `LIFECYCLE.md sec.5`.

---

## 5. WorkerPool 实现

`WorkerPool` 位于 **`internal/transport/shared/worker_pool.go`**(不在 `internal/runtime`).
它放在 shared 包是为了让任意传输层复用:**当前只有 TCP Server 实际实例化它**
(`internal/transport/tcp/server.go`),UDP 与 CoAP 直接经 `shared.CallHandler` 内联调用
handler.因此三者的 handler panic 隔离语义一致,但只有 TCP 具备池带来的背压能力.

### 5.1 结构定义

```go
// internal/transport/shared/worker_pool.go
type WorkerPool struct {
    handler core.Handler
    logger  core.Logger
    queue   chan PoolTask
    policy  FullPolicy
    wg      sync.WaitGroup
    closed  atomic.Bool
    done    chan struct{}
    // submitMu 串行化 Submit 与 Stop:Submit 在整个入队期间持读锁,
    // 使 Stop 无法在任务仍在途中时排空队列并返回.
    submitMu sync.RWMutex
}

type PoolTask struct {
    Session  core.Session
    Protocol core.Protocol
    Payload  []byte
}

func NewWorkerPool(handler core.Handler, logger core.Logger,
    workers, queueSize int, policy FullPolicy) *WorkerPool
```

构造归一化:`workers <= 0` 视为 1;`queueSize <= 0` 视为 `workers * 128`;
`handler` 为 nil 时 `Enabled()` 返回 false,调用方据此跳过全部 `Submit`.

### 5.2 队列满策略

```go
type FullPolicy int

const (
    PolicyBlock FullPolicy = iota // 阻塞提交方,等待队列空间
    PolicyDrop                    // 立即返回 core.ErrWriteQueueFull
    PolicyClose                   // 拒绝并关闭该会话
)
```

| 策略 | 行为 | 适用场景 | 风险 |
|------|------|----------|------|
| `PolicyBlock` | 提交方阻塞,同时监听 `done` 与会话 Context | 不可丢消息 | readLoop 阻塞,队首延迟 |
| `PolicyDrop` | 返回 `ErrWriteQueueFull`,消息丢弃 | 通用场景,防雪崩 | 消息丢失 |
| `PolicyClose` | 返回 `ErrWriteQueueFull` 并关闭会话 | 极端过载保护 | 连接中断,客户端可感知 |

设计草案里的 `PolicySpawnTemp`(临时扩容 worker)、`HandlerTimeout`(handler 执行超时)
与 `OverloadWindow`(持续过载窗口)**未实现**,`internal/transport/tcp/policy.go` 只是
把 `shared.FullPolicy` 及其三个常量再导出,便于传输层配置:

- `PolicySpawnTemp` 在雪崩时会让 goroutine 数量失控,收益不足以抵消风险.
- handler 超时改由会话 Context 与调用方超时表达,池不引入第二套超时语义.

### 5.3 Submit 实现

```go
func (p *WorkerPool) Submit(sess core.Session, protocol core.Protocol, payload []byte) error {
    p.submitMu.RLock()
    defer p.submitMu.RUnlock()
    if p.closed.Load() {
        return core.ErrClosed
    }
    t := PoolTask{Session: sess, Protocol: protocol, Payload: payload}
    // 同时监听会话 Context:队列满且对端断连时,提交方必须能被唤醒,
    // 否则每条卡住的连接都会泄漏一个 goroutine.测试中 sess 可为 nil.
    var sessDone <-chan struct{}
    if sess != nil {
        if ctx := sess.Context(); ctx != nil {
            sessDone = ctx.Done()
        }
    }
    switch p.policy {
    case PolicyDrop:
        select {
        case p.queue <- t:
            return nil
        default:
            return core.ErrWriteQueueFull
        }
    case PolicyClose:
        select {
        case p.queue <- t:
            return nil
        default:
            if sess != nil {
                _ = sess.Close(context.Background())
            }
            return core.ErrWriteQueueFull
        }
    default: // PolicyBlock
        select {
        case p.queue <- t:
            return nil
        case <-p.done:
            return core.ErrClosed
        case <-sessDone:
            return core.ErrSessionClosed
        }
    }
}
```

三个关键点:

1. 读锁覆盖整个入队,包括 `PolicyBlock` 的阻塞发送;`Stop` 取写锁时必须等待所有在途
   `Submit` 结束,任务不会在排空之后才落队.
2. **不关闭 `queue` channel**:终止信号走 `done`,否则阻塞发送与 `Stop` 竞争时向已关闭
   channel 发送会 panic.
3. `PolicyBlock` 监听 `sess.Context().Done()`,对端断连时立即返回 `ErrSessionClosed`.

### 5.4 worker 循环与排空

```go
func (p *WorkerPool) run() {
    defer p.wg.Done()
    for {
        select {
        case t := <-p.queue:
            p.handle(t)
        case <-p.done:
            // 退出前排空队列,保证 Stop 之前提交的任务仍然完成.
            for {
                select {
                case t := <-p.queue:
                    p.handle(t)
                default:
                    return
                }
            }
        }
    }
}
```

### 5.5 handler 调用与 panic 隔离

```go
func (p *WorkerPool) handle(t PoolTask) {
    if p.handler == nil || t.Session == nil {
        return
    }
    msg := core.Message{SessionID: t.Session.ID(), Protocol: t.Protocol, Payload: t.Payload}
    if err := p.callHandler(t.Session, msg); err != nil {
        _ = t.Session.Close(context.Background())
    }
}

func (p *WorkerPool) callHandler(sess core.Session, msg core.Message) error {
    return CallHandler(func() error { return p.handler(sess, msg) }, p.logger)
}
```

`shared.CallHandler` 捕获 handler panic,记录日志并返回 `core.ErrHandlerPanic`,
随后关闭该会话.于是 panic 只影响一条连接,不会带走 worker goroutine(V9 修复项:
此前 panic 会终止进程,且操作者拿不到任何诊断).

### 5.6 Start 与 Stop

```go
func (p *WorkerPool) Start(workers int) // workers <= 0 归一为 1
func (p *WorkerPool) Stop()             // 幂等,可重复调用
```

`Stop` 先 `CompareAndSwap` 置位并 `close(p.done)`,再取写锁等待全部 worker 退出,
最后做一次兜底排空.可重复 `Start` / `Stop` 与并发调用都安全,使传输层保持可重入.

---

## 6. Runtime 实现

`core.Runtime` 的默认实现是 `internal/runtime/runtime.go` 中的 `Runtime`.它不叫
`DefaultRuntime`,也不在 `runtime_impl.go`:只有一个实现,名字里就不需要 "Default" 前缀.

### 6.1 结构定义

```go
// internal/runtime/runtime.go
type Runtime struct {
    sessions core.SessionManager
    plugins  core.PluginRunner
    logger   core.Logger
    metrics  core.Metrics
    tracer   core.Tracer
}

func NewRuntime(sessions core.SessionManager, plugins core.PluginRunner,
    opts ...RuntimeOption) *Runtime {
    if sessions == nil {
        sessions = NewSessionManager()
    }
    if plugins == nil {
        plugins = NewPluginChain()
    }
    r := &Runtime{
        sessions: sessions,
        plugins:  plugins,
        logger:   core.NopLogger(),
        metrics:  core.NopMetrics(),
        tracer:   core.NopTracer(),
    }
    for _, opt := range opts {
        opt(r)
    }
    return r
}

// 编译期验证
var _ core.Runtime = (*Runtime)(nil)
```

零依赖可用是刻意的:任何成员缺失都退化到 Nop 实现或默认构造,因此单独测试某一个协议时
不必装配整套观测组件.

### 6.2 RuntimeOption

```go
// internal/runtime/runtime_options.go
type RuntimeOption func(*Runtime)

func withRuntimeLogger(logger core.Logger) RuntimeOption
func withRuntimeMetrics(metrics core.Metrics) RuntimeOption
func withRuntimeTracer(tracer core.Tracer) RuntimeOption
```

三个 option 都是**非导出**的,只有 `Gateway` 能构造它们.这是有意的封装:
`internal/app` 通过 `GatewayOption`(`WithLogger` / `WithMetrics` / `WithTracer`)配置可观测性,
再由 `NewGateway` 转交给 `NewRuntime`,外部无法在 `Gateway` 之外另造一个持有不同 logger 的
`Runtime`.三个 option 都忽略 nil 参数,保留默认 Nop 实现.

### 6.3 接口方法

```go
func (r *Runtime) Sessions() core.SessionManager { return r.sessions }
func (r *Runtime) Plugins() core.PluginRunner    { return r.plugins }
func (r *Runtime) Logger() core.Logger           { return r.logger }
func (r *Runtime) Metrics() core.Metrics         { return r.metrics }
func (r *Runtime) Tracer() core.Tracer           { return r.tracer }
```

`Sessions()` 返回的可能是 `metricSessionManager`(当配置了 metrics 时由 `NewGateway` 包上),
它在 `Register` / `Unregister` 时上报指标,其余方法透传.

---

## 7. 应用装配层

装配层位于 `internal/app`(不是 `internal/application`),入口是 `cmd/shark-socket/main.go`.

### 7.1 Config 结构

```go
// internal/app/config.go
type Config struct {
    ShutdownTimeout string           `json:"shutdown_timeout"` // 形如 "10s",由 ShutdownDuration 解析
    HealthAddr      string           `json:"health_addr"`
    MetricsAddr     string           `json:"metrics_addr"`
    Protocols       []ProtocolConfig `json:"protocols"`
}

type ProtocolConfig struct {
    Name            string   `json:"name"`
    Enabled         *bool    `json:"enabled,omitempty"` // nil 视为启用,故用指针
    Addr            string   `json:"addr"`
    Path            string   `json:"path,omitempty"`
    Mode            string   `json:"mode,omitempty"` // coap 的 "lwm2m" 走 LwM2M responder
    MaxMessageBytes int64    `json:"max_message_bytes,omitempty"`
    TLSCertFile     string   `json:"tls_cert_file,omitempty"`
    TLSKeyFile      string   `json:"tls_key_file,omitempty"`
    TLSClientCAFile string   `json:"tls_client_ca_file,omitempty"`
    TLSClientAuth   string   `json:"tls_client_auth,omitempty"`
    TLSMinVersion   string   `json:"tls_min_version,omitempty"`
    AllowedOrigins  []string `json:"allowed_origins,omitempty"` // http / websocket / grpc-web
}
```

两个刻意的选择:

- `ShutdownTimeout` 是**字符串**而非 `time.Duration`:JSON 无法直接表达 Go 的 duration,
  保持字符串让配置与 `time.ParseDuration` 语义一致.`ShutdownDuration()` 负责解析并给出
  默认值 10s.
- `Enabled` 是 `*bool`:缺省(未写该字段)必须等同于启用,用值类型会把零值 `false` 和
  "未配置"混为一谈.对外读用 `IsEnabled()`.

`DefaultConfig()` 返回 `ShutdownTimeout="10s"`、`HealthAddr="127.0.0.1:18081"`、
`MetricsAddr="127.0.0.1:18080"`,以及一个监听 `127.0.0.1:18000` 的 TCP 协议项.

### 7.2 统一配置验证

`LoadConfig(path)` 的顺序是:默认值 -> 读取 JSON(若给了路径)-> `applyEnv` 环境变量覆盖
-> `Validate()`.验证在装配前一次性完成,`Start()` 不再重复校验.

`Validate()` 的规则:

- `shutdown_timeout` 必须能被解析为正时长.
- 至少启用一个协议.
- 协议名不得为空、不得重复.
- 启用的协议必须有 `addr`;`max_message_bytes` 不得为负.
- `tls_cert_file` / `tls_key_file` 必须成对提供.
- `tls_cert_file`、`tls_client_ca_file`、`tls_client_auth` 只允许出现在
  `tcp` / `udp` / `coap` / `quic` 上.
- `tls_client_ca_file` 与 `tls_client_auth` 都要求同时提供 cert/key.
- `tls_client_auth` 的取值必须能被解析为合法 `tls.ClientAuthType`.
- `quic` 必须提供 cert/key.
- 协议名必须属于 `tcp` / `udp` / `http` / `websocket` / `coap` / `grpc-web` / `quic`.

失败时返回带协议名与序号前缀的错误,便于在配置文件中定位.

### 7.3 App 装配流程

```go
// internal/app/app.go
type App struct {
    Config      Config
    Gateway     *api.Gateway
    Metrics     *api.PrometheusMetrics
    Health      *http.Server
    MetricsHTTP *http.Server
    Protocols   []string

    serveErrors  []error   // health / metrics 服务的启动错误
    certCaches   []*tlsutil.CertCache
    certWatchers []context.CancelFunc
    ...
}

func New(cfg Config) (*App, error) {
    if err := cfg.Validate(); err != nil {
        return nil, err
    }
    metrics := api.NewPrometheusMetrics()
    gateway := api.NewGateway(api.WithMetrics(metrics))
    app := &App{Config: cfg, Gateway: gateway, Metrics: metrics}
    if err := app.registerProtocols(cfg.Protocols); err != nil {
        return nil, err
    }
    // health / metrics 两个 HTTP 服务:显式设置四个超时,
    // 避免慢连接占住 server.
    ...
}

func (a *App) Start(ctx context.Context) error {
    a.appCtx, a.appCancel = context.WithCancel(ctx)
    go a.serveHTTP("health", a.Health)     // 各自计入 serveWG
    go a.serveHTTP("metrics", a.MetricsHTTP)
    return a.Gateway.Start(ctx)
}
```

`registerProtocols` 按配置逐个构造并注册 Server,`coap` 在 `mode=lwm2m` 时挂 LwM2M responder,
并把 `LwM2MServer.OnWrite` 接到 CoAP 的 `NotifyObservers`.

**证书热加载:** 需要 TLS 的协议统一经 `loadServerTLSConfig` 构造 `tls.Config`,返回的
`*tlsutil.CertCache` 被收集到 `certCaches`;随后为每个 cache 启动一个 30s 轮询的
`tlsutil.WatchFilesWithWG` 观察者,文件 mtime 变化时调用 `cache.Load()`.详见
`SECURITY.md sec.2.5`.

### 7.4 关停顺序

```go
func (a *App) Stop(ctx context.Context) error {
    a.appCancel()                       // 停掉后台 goroutine
    for _, cancel := range a.certWatchers { cancel() }
    a.certWG.Wait()                     // 等证书观察者退出
    // 1. 先关网关:停收 -> 排空 -> 关会话 -> 停插件
    a.Gateway.Stop(ctx)
    // 2. 再关 health / metrics,最后等 serveHTTP 协程退出
    a.Health.Shutdown(ctx)
    a.MetricsHTTP.Shutdown(ctx)
    a.serveWG.Wait()
    return firstErr
}
```

先关网关再关健康端点,是为了让 `readyz` 在网关已经开始关停之后仍然可以被读到(返回
503),负载均衡器据此摘流量.

### 7.5 cmd/shark-socket/main.go

```go
func main() {
    configPath := flag.String("config", "config.json", "path to config file")
    flag.Parse()

    cfg, err := app.LoadConfig(*configPath)
    if err != nil {
        log.Fatalf("load config: %v", err)
    }

    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
    defer stop()

    a, err := app.New(cfg)
    if err != nil {
        log.Fatalf("build app: %v", err)
    }
    if err := a.Start(ctx); err != nil {
        log.Fatalf("start app: %v", err)
    }

    <-ctx.Done()
    shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
    defer cancel()
    _ = a.Stop(shutdownCtx)
}
```

---
