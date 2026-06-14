# GATEWAY.md

> Shark-Socket 运行时层详细设计  
> 版本：v0.1.0  
> 最后更新：2026-06-01

---

## 目录

1. [概述](#1-概述)
2. [Gateway](#2-gateway)
3. [SessionManager 实现](#3-sessionmanager-实现)
4. [PluginRunner 实现](#4-pluginrunner-实现)
5. [WorkerPool 实现](#5-workerpool-实现)
6. [DefaultRuntime 实现](#6-defaultruntime-实现)
7. [应用装配层](#7-应用装配层)

---

## 1. 概述

`internal/runtime/` 是**运行时层**，负责：

- **Gateway**：多协议编排、Runtime 注入、分阶段关闭
- **SessionManager**：跨协议会话注册、查询、广播
- **PluginRunner**：插件链排序、执行、panic 隔离
- **WorkerPool**：消息处理并发控制
- **DefaultRuntime**：Runtime 接口的默认实现

**所有权原则：**

```
Gateway 创建并持有：
  ├── DefaultRuntime（组装以下四者）
  │   ├── SessionManager（跨协议共享）
  │   ├── PluginRunner（跨协议共享）
  │   ├── Logger（全局）
  │   ├── Metrics（全局）
  │   └── Tracer（全局）
  └── []Server（各协议实例）

协议 Server 通过 UseRuntime 接收 Runtime，不自行创建全局资源
```

---

## 2. Gateway

### 2.1 结构定义

```go
// internal/runtime/gateway.go
type Gateway struct {
    servers     []core.Server           // 按注册顺序保存
    serverIndex map[core.Protocol]int   // Protocol → servers 下标，去重用
    runtime     core.Runtime            // 共享运行时依赖容器
    options     GatewayOptions

    started   atomic.Bool
    ready     atomic.Bool
    startedAt time.Time

    logger  core.Logger
    metrics core.Metrics
}
```

### 2.2 构造与 Server 注册

```go
func NewGateway(opts ...GatewayOption) *Gateway {
    options := defaultGatewayOptions()
    for _, opt := range opts {
        opt(&options)
    }
    return &Gateway{
        serverIndex: make(map[core.Protocol]int),
        options:     options,
        logger:      options.logger,
        metrics:     options.metrics,
    }
}

// RegisterServer 注册协议 Server，重复 Protocol 返回 ErrDuplicateProtocol。
func (g *Gateway) RegisterServer(srv core.Server) error {
    if g.started.Load() {
        return errors.New("shark: cannot register server after gateway started")
    }
    if _, exists := g.serverIndex[srv.Protocol()]; exists {
        return fmt.Errorf("%w: %s", core.ErrDuplicateProtocol, srv.Protocol())
    }
    g.serverIndex[srv.Protocol()] = len(g.servers)
    g.servers = append(g.servers, srv)
    return nil
}
```

### 2.3 Start 流程

详见 `LIFECYCLE.md §3.2 启动流程`，关键约束：

- 注入 Runtime 必须在 `Start()` 之前完成
- 任一 Server 启动失败，逆序回滚已启动的 Server
- 启动成功后 `ready.Store(true)`

### 2.4 Stop 流程

详见 `LIFECYCLE.md §3.3 停止流程`，关键约束：

- 5 阶段串行执行，每阶段内并发
- `Stop()` 幂等，多次调用安全
- Drain 超时不阻塞后续阶段

### 2.5 GatewayOptions

```go
// internal/runtime/gateway_options.go
type GatewayOptions struct {
    // 关闭阶段超时配置（各阶段独立，不共用同一个超时）
    StopAcceptTimeout  time.Duration // 默认 5s
    DrainTimeout       time.Duration // 默认 10s
    CloseSessionsTimeout time.Duration // 默认 5s
    TotalShutdownTimeout time.Duration // 默认 30s，整体上限

    // 运行时组件（由 application 层注入）
    runtime core.Runtime

    // 可观测
    logger  core.Logger
    metrics core.Metrics
}

func defaultGatewayOptions() GatewayOptions {
    return GatewayOptions{
        StopAcceptTimeout:    5 * time.Second,
        DrainTimeout:         10 * time.Second,
        CloseSessionsTimeout: 5 * time.Second,
        TotalShutdownTimeout: 30 * time.Second,
        logger:               observability.NopLogger{},
        metrics:              observability.NopMetrics{},
    }
}

// Functional Options
func WithRuntime(rt core.Runtime) GatewayOption {
    return func(o *GatewayOptions) { o.runtime = rt }
}

func WithGatewayLogger(l core.Logger) GatewayOption {
    return func(o *GatewayOptions) { o.logger = l }
}

func WithGatewayMetrics(m core.Metrics) GatewayOption {
    return func(o *GatewayOptions) { o.metrics = m }
}

func WithShutdownTimeout(total time.Duration) GatewayOption {
    return func(o *GatewayOptions) {
        o.TotalShutdownTimeout = total
        // 按比例分配各阶段超时
        o.StopAcceptTimeout    = total / 6
        o.DrainTimeout         = total / 2
        o.CloseSessionsTimeout = total / 6
    }
}
```

### 2.6 Run 便捷方法

```go
// Run 启动 Gateway 并阻塞直到接收到 SIGTERM / SIGINT 信号。
func (g *Gateway) Run() error {
    ctx, stop := signal.NotifyContext(context.Background(),
        syscall.SIGTERM, syscall.SIGINT)
    defer stop()

    if err := g.Start(ctx); err != nil {
        return err
    }

    <-ctx.Done()

    shutdownCtx, cancel := context.WithTimeout(
        context.Background(),
        g.options.TotalShutdownTimeout,
    )
    defer cancel()

    return g.Stop(shutdownCtx)
}
```

### 2.7 健康与就绪接口

```go
// Ready 返回 Gateway 是否成功启动。
// Ready() == true 只表示 Gateway 启动成功，不代表外部依赖健康。
func (g *Gateway) Ready() bool {
    return g.ready.Load()
}

// Health 返回详细健康信息（供 /healthz 端点使用）。
func (g *Gateway) Health() map[string]any {
    protocols := make(map[string]bool)
    for proto := range g.serverIndex {
        protocols[proto.String()] = true
    }
    status := "healthy"
    if !g.Ready() {
        status = "not_ready"
    }
    return map[string]any{
        "status":    status,
        "uptime":    time.Since(g.startedAt).String(),
        "protocols": protocols,
        "sessions":  g.runtime.Sessions().Count(),
    }
}
```

---

## 3. SessionManager 实现

### 3.1 P0 实现（单锁，功能正确优先）

```go
// internal/runtime/session_manager.go
type manager struct {
    mu       sync.RWMutex
    sessions map[uint64]core.Session
    idGen    atomic.Uint64
    total    atomic.Int64
    maxCount int64 // 0 = 不限制
}

func newManager(maxCount int64) *manager {
    return &manager{
        sessions: make(map[uint64]core.Session),
        maxCount: maxCount,
    }
}

// 编译期验证
var _ core.SessionManager = (*manager)(nil)
```

### 3.2 核心方法实现

```go
func (m *manager) NextID() uint64 {
    return m.idGen.Add(1)
}

func (m *manager) Register(sess core.Session) error {
    // 容量检查（无锁快速路径）
    if m.maxCount > 0 && m.total.Load() >= m.maxCount {
        return core.ErrSessionCapacity
    }

    m.mu.Lock()
    defer m.mu.Unlock()

    // 双重检查（加锁后再检查，防止并发 Register 超限）
    if m.maxCount > 0 && int64(len(m.sessions)) >= m.maxCount {
        return core.ErrSessionCapacity
    }

    m.sessions[sess.ID()] = sess
    m.total.Add(1)
    return nil
}

func (m *manager) Unregister(id uint64) {
    m.mu.Lock()
    defer m.mu.Unlock()

    if _, exists := m.sessions[id]; exists {
        delete(m.sessions, id)
        m.total.Add(-1)
    }
}

func (m *manager) Get(id uint64) (core.Session, bool) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    sess, ok := m.sessions[id]
    return sess, ok
}

func (m *manager) Count() int64 {
    return m.total.Load() // 无锁读取
}
```

### 3.3 遍历与广播

```go
func (m *manager) Range(fn func(core.Session) bool) {
    m.mu.RLock()
    // 快照后释放锁
    snapshot := make([]core.Session, 0, len(m.sessions))
    for _, s := range m.sessions {
        snapshot = append(snapshot, s)
    }
    m.mu.RUnlock()

    for _, s := range snapshot {
        if !fn(s) {
            break
        }
    }
}

func (m *manager) All() iter.Seq[core.Session] {
    return func(yield func(core.Session) bool) {
        m.Range(yield)
    }
}

func (m *manager) Broadcast(data []byte) error {
    m.mu.RLock()
    snapshot := make([]core.Session, 0, len(m.sessions))
    for _, s := range m.sessions {
        snapshot = append(snapshot, s)
    }
    m.mu.RUnlock()

    var errs []error
    for _, s := range snapshot {
        if err := s.Send(data); err != nil {
            if !errors.Is(err, core.ErrSessionClosed) {
                errs = append(errs, err)
            }
        }
    }
    return errors.Join(errs...)
}

func (m *manager) CloseAll(ctx context.Context) error {
    m.mu.RLock()
    snapshot := make([]core.Session, 0, len(m.sessions))
    for _, s := range m.sessions {
        snapshot = append(snapshot, s)
    }
    m.mu.RUnlock()

    var wg sync.WaitGroup
    for _, s := range snapshot {
        wg.Add(1)
        go func(sess core.Session) {
            defer wg.Done()
            sess.Close(ctx)
        }(s)
    }

    done := make(chan struct{})
    go func() {
        wg.Wait()
        close(done)
    }()

    select {
    case <-done:
        return nil
    case <-ctx.Done():
        return core.ErrDrainTimeout
    }
}
```

### 3.4 P2 演进方案（分片锁）

**触发条件：** benchmark 证明 `Register/Unregister/Get` 锁竞争为瓶颈。

```go
// P2 实现
type shardedManager struct {
    shards   [32]shard
    idGen    atomic.Uint64
    total    atomic.Int64
    maxCount int64
}

type shard struct {
    mu       sync.RWMutex
    sessions map[uint64]core.Session
    // P2：per-shard LRU 淘汰（benchmark 证明需要后引入）
}

// 分片函数：位运算替代取模
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

**P0 → P2 迁移策略：**

- `core.SessionManager` 接口不变
- 只替换 `internal/runtime/session_manager.go` 中的实现
- 迁移前必须有 P0 基准测试数据作为对比基线
- 迁移后 `go test -race ./...` 无竞争

---

## 4. PluginRunner 实现

### 4.1 结构定义

```go
// internal/runtime/plugin_runner.go
type pluginRunner struct {
    plugins     []core.Plugin        // 按 Priority 升序静态排序
    nameIndex   map[string]int       // 名称 → plugins 下标，去重用
    stopOnError bool                 // 普通 error 是否中断链（默认 true）

    logger  core.Logger
    metrics core.Metrics
}

func newPluginRunner(logger core.Logger, metrics core.Metrics) *pluginRunner {
    return &pluginRunner{
        nameIndex:   make(map[string]int),
        stopOnError: true,
        logger:      logger,
        metrics:     metrics,
    }
}

// 编译期验证
var _ core.PluginRunner = (*pluginRunner)(nil)
```

### 4.2 Register 实现

```go
func (r *pluginRunner) Register(p core.Plugin) error {
    if _, exists := r.nameIndex[p.Name()]; exists {
        r.logger.Warn("plugin duplicate name, overwriting",
            "plugin_name", p.Name(),
            "priority", p.Priority())
        // 找到旧位置并替换
        oldIndex := r.nameIndex[p.Name()]
        r.plugins[oldIndex] = p
    } else {
        r.nameIndex[p.Name()] = len(r.plugins)
        r.plugins = append(r.plugins, p)
    }

    // 按 Priority 升序排序（启动时执行，热路径无排序开销）
    slices.SortFunc(r.plugins, func(a, b core.Plugin) int {
        return a.Priority() - b.Priority()
    })

    // 重建 nameIndex（排序后下标变化）
    clear(r.nameIndex)
    for i, plugin := range r.plugins {
        r.nameIndex[plugin.Name()] = i
    }

    return nil
}
```

### 4.3 RunAccept 实现

```go
func (r *pluginRunner) RunAccept(sess core.Session) error {
    for _, p := range r.plugins {
        start := time.Now()
        err := r.safeRun(p.Name(), func() error {
            return p.OnAccept(sess)
        })
        r.metrics.Histogram("shark_plugin_duration_seconds",
            "plugin", p.Name()).Observe(time.Since(start).Seconds())

        if err == nil {
            continue
        }

        if errors.Is(err, core.ErrPluginBlock) {
            r.metrics.Counter("shark_rejected_connections_total",
                "protocol", sess.Protocol().String(),
                "reason", "plugin_block").Inc()
            sess.Close(context.Background())
            return err
        }

        r.metrics.Counter("shark_plugin_errors_total",
            "plugin", p.Name(),
            "error_type", "accept").Inc()

        if r.stopOnError {
            sess.Close(context.Background())
            return err
        }

        r.logger.Warn("plugin OnAccept non-fatal error",
            "plugin_name", p.Name(),
            "error", err,
            "session_id", sess.ID())
    }
    return nil
}
```

### 4.4 RunMessage 实现

```go
func (r *pluginRunner) RunMessage(sess core.Session, data []byte) ([]byte, error) {
    current := data
    for _, p := range r.plugins {
        start := time.Now()
        var out []byte
        err := r.safeRun(p.Name(), func() error {
            var e error
            out, e = p.OnMessage(sess, current)
            return e
        })
        r.metrics.Histogram("shark_plugin_duration_seconds",
            "plugin", p.Name()).Observe(time.Since(start).Seconds())

        if err == nil {
            if out != nil {
                current = out // plugin 改写 payload
            }
            continue
        }

        if errors.Is(err, core.ErrPluginDrop) {
            r.metrics.Counter("shark_dropped_messages_total",
                "protocol", sess.Protocol().String(),
                "reason", "plugin_drop").Inc()
            return nil, err
        }

        if errors.Is(err, core.ErrPluginBlock) {
            sess.Close(context.Background())
            return nil, err
        }

        r.metrics.Counter("shark_plugin_errors_total",
            "plugin", p.Name(),
            "error_type", "message").Inc()

        if r.stopOnError {
            return nil, err
        }

        r.logger.Warn("plugin OnMessage non-fatal error",
            "plugin_name", p.Name(),
            "error", err,
            "session_id", sess.ID())
    }
    return current, nil
}
```

### 4.5 RunClose 实现

```go
func (r *pluginRunner) RunClose(sess core.Session) {
    // 逆序执行，不可中断
    for i := len(r.plugins) - 1; i >= 0; i-- {
        p := r.plugins[i]
        r.safeRun(p.Name(), func() error {
            p.OnClose(sess)
            return nil
        })
    }
}
```

### 4.6 safeRun panic 隔离

```go
func (r *pluginRunner) safeRun(name string, fn func() error) (err error) {
    defer func() {
        if rec := recover(); rec != nil {
            stack := debug.Stack()
            err = fmt.Errorf("plugin panic recovered: %v", rec)
            r.logger.Error("plugin panic",
                "plugin_name", name,
                "panic", rec,
                "stack", string(stack))
            r.metrics.Counter("shark_plugin_panics_total",
                "plugin", name).Inc()
        }
    }()
    return fn()
}
```

---

## 5. WorkerPool 实现

### 5.1 结构定义

```go
// internal/runtime/worker_pool.go
type task struct {
    session core.Session
    message core.Message
    handler core.Handler
}

type WorkerPool struct {
    taskQueue  chan task
    options    WorkerPoolOptions

    wg         sync.WaitGroup
    closed     atomic.Bool
    tempCount  atomic.Int32 // 当前临时 Worker 数量

    logger  core.Logger
    metrics core.Metrics
}

type WorkerPoolOptions struct {
    WorkerCount     int           // 核心 Worker 数量（默认 NumCPU×2）
    MaxWorkers      int           // 最大 Worker 数量（含临时，默认 WorkerCount×4）
    TaskQueueSize   int           // 任务队列容量（默认 WorkerCount×128）
    QueueFullPolicy QueuePolicy   // 队列满策略（默认 PolicyDrop）
    HandlerTimeout  time.Duration // Handler 执行超时（默认 0，不限制）
    OverloadWindow  time.Duration // PolicyClose 的持续过载窗口（默认 30s）
}
```

### 5.2 队列满策略

```go
type QueuePolicy uint8

const (
    PolicyDrop      QueuePolicy = 0 // 丢弃消息 + 记录 metrics（默认）
    PolicyBlock     QueuePolicy = 1 // 阻塞等待队列空间
    PolicySpawnTemp QueuePolicy = 2 // 动态扩容临时 Worker
    PolicyClose     QueuePolicy = 3 // 持续过载后关闭连接
)
```

**策略对比：**

| 策略 | 行为 | 适用场景 | 风险 |
|------|------|---------|------|
| PolicyDrop | 丢弃消息，记录 `shark_dropped_messages_total` | 通用场景，防雪崩 | 消息丢失 |
| PolicyBlock | 阻塞 Send，写满时等待 | 不可丢消息（金融交易） | readLoop 阻塞，影响其他消息 |
| PolicySpawnTemp | 临时扩容 Worker，处理完退出 | 突发流量缓冲 | Goroutine 数量激增 |
| PolicyClose | 持续过载 30s 后关闭连接 | 极端情况保护 | 连接中断，客户端感知 |

### 5.3 Submit 实现

```go
func (p *WorkerPool) Submit(sess core.Session, msg core.Message, handler core.Handler) error {
    if p.closed.Load() {
        return core.ErrServerClosed
    }

    t := task{session: sess, message: msg, handler: handler}

    switch p.options.QueueFullPolicy {
    case PolicyDrop:
        select {
        case p.taskQueue <- t:
            return nil
        default:
            p.metrics.Counter("shark_dropped_messages_total",
                "protocol", sess.Protocol().String(),
                "reason", "worker_queue_full").Inc()
            return core.ErrWriteQueueFull
        }

    case PolicyBlock:
        p.taskQueue <- t // 阻塞等待
        return nil

    case PolicySpawnTemp:
        select {
        case p.taskQueue <- t:
            return nil
        default:
            return p.spawnTemp(t)
        }

    case PolicyClose:
        select {
        case p.taskQueue <- t:
            p.overloadTracker.Reset() // 成功提交，重置过载计时
            return nil
        default:
            if p.overloadTracker.Exceeded(p.options.OverloadWindow) {
                sess.Close(context.Background())
                return core.ErrSessionClosed
            }
            return core.ErrWriteQueueFull
        }
    }

    return nil
}
```

### 5.4 SpawnTemp 实现

```go
func (p *WorkerPool) spawnTemp(t task) error {
    total := int(p.tempCount.Load()) + p.options.WorkerCount
    if total >= p.options.MaxWorkers {
        // 已达最大 Worker 数，降级为 Drop
        p.metrics.Counter("shark_dropped_messages_total",
            "protocol", t.session.Protocol().String(),
            "reason", "max_workers_reached").Inc()
        return core.ErrWriteQueueFull
    }

    p.tempCount.Add(1)
    p.wg.Add(1)
    go func() {
        defer p.wg.Done()
        defer p.tempCount.Add(-1)
        // 临时 Worker 只处理当前任务后退出
        p.runTask(t)
    }()
    return nil
}
```

### 5.5 worker 核心循环

```go
func (p *WorkerPool) worker() {
    defer p.wg.Done()
    for t := range p.taskQueue {
        p.runTask(t)
    }
}

func (p *WorkerPool) runTask(t task) {
    start := time.Now()
    defer func() {
        p.metrics.Histogram("shark_handler_duration_seconds",
            "protocol", t.session.Protocol().String(),
        ).Observe(time.Since(start).Seconds())
    }()

    // panic 隔离
    defer func() {
        if rec := recover(); rec != nil {
            p.logger.Error("worker panic recovered",
                "session_id", t.session.ID(),
                "panic", rec,
                "stack", string(debug.Stack()))
            p.metrics.Counter("shark_worker_panics_total",
                "protocol", t.session.Protocol().String()).Inc()
        }
    }()

    // HandlerTimeout 保护
    ctx := t.session.Context()
    if p.options.HandlerTimeout > 0 {
        var cancel context.CancelFunc
        ctx, cancel = context.WithTimeout(ctx, p.options.HandlerTimeout)
        defer cancel()
    }

    // 执行 Handler
    if err := t.handler(t.session, t.message); err != nil {
        if !core.IsPluginControl(err) {
            p.logger.Error("handler error",
                "session_id", t.session.ID(),
                "protocol", t.session.Protocol(),
                "error", err)
        }
    }
}
```

### 5.6 Start 与 Stop

```go
func (p *WorkerPool) Start() {
    for i := 0; i < p.options.WorkerCount; i++ {
        p.wg.Add(1)
        go p.worker()
    }
}

func (p *WorkerPool) Stop() {
    if p.closed.CompareAndSwap(false, true) {
        close(p.taskQueue) // 触发所有 worker 退出（for range 结束）
    }
    p.wg.Wait() // 等待所有 worker（含临时）完成
}
```

---

## 6. DefaultRuntime 实现

### 6.1 结构定义

```go
// internal/runtime/runtime_impl.go
type DefaultRuntime struct {
    sessions core.SessionManager
    plugins  core.PluginRunner
    logger   core.Logger
    metrics  core.Metrics
    tracer   core.Tracer
}

// 编译期验证
var _ core.Runtime = (*DefaultRuntime)(nil)

func NewDefaultRuntime(opts ...RuntimeOption) *DefaultRuntime {
    r := &DefaultRuntime{
        logger:  observability.NopLogger{},
        metrics: observability.NopMetrics{},
        tracer:  observability.NoopTracer{},
    }
    for _, opt := range opts {
        opt(r)
    }

    // 延迟初始化依赖 logger/metrics 的组件
    if r.sessions == nil {
        r.sessions = newManager(0)
    }
    if r.plugins == nil {
        r.plugins = newPluginRunner(r.logger, r.metrics)
    }
    return r
}
```

### 6.2 RuntimeOption

```go
type RuntimeOption func(*DefaultRuntime)

func WithSessionManager(sm core.SessionManager) RuntimeOption {
    return func(r *DefaultRuntime) { r.sessions = sm }
}

func WithPluginRunner(pr core.PluginRunner) RuntimeOption {
    return func(r *DefaultRuntime) { r.plugins = pr }
}

func WithLogger(l core.Logger) RuntimeOption {
    return func(r *DefaultRuntime) { r.logger = l }
}

func WithMetrics(m core.Metrics) RuntimeOption {
    return func(r *DefaultRuntime) { r.metrics = m }
}

func WithTracer(t core.Tracer) RuntimeOption {
    return func(r *DefaultRuntime) { r.tracer = t }
}
```

### 6.3 接口方法

```go
func (r *DefaultRuntime) Sessions() core.SessionManager { return r.sessions }
func (r *DefaultRuntime) Plugins()  core.PluginRunner   { return r.plugins }
func (r *DefaultRuntime) Logger()   core.Logger         { return r.logger }
func (r *DefaultRuntime) Metrics()  core.Metrics        { return r.metrics }
func (r *DefaultRuntime) Tracer()   core.Tracer         { return r.tracer }
```

---

## 7. 应用装配层

### 7.1 Config 结构

```go
// internal/application/config.go
type Config struct {
    ShutdownTimeout time.Duration    `json:"shutdown_timeout"` // 默认 30s
    HealthAddr      string           `json:"health_addr"`      // 默认 ":18081"
    MetricsAddr     string           `json:"metrics_addr"`     // 默认 ":18080"
    MaxSessions     int64            `json:"max_sessions"`     // 默认 100000（0=不限制）
    Protocols       []ProtocolConfig `json:"protocols"`
}

type ProtocolConfig struct {
    Name           string `json:"name"`             // tcp/udp/coap/lwm2m/websocket
    Enabled        bool   `json:"enabled"`          // 默认 true
    Addr           string `json:"addr"`             // 监听地址
    Path           string `json:"path"`             // WebSocket path
    Mode           string `json:"mode"`             // CoAP 模式（lwm2m）
    MaxMessageBytes int   `json:"max_message_bytes"` // 消息大小限制（>= 0）
    TLSCertFile    string `json:"tls_cert_file"`
    TLSKeyFile     string `json:"tls_key_file"`
    TLSClientCAFile string `json:"tls_client_ca_file"`
    TLSClientAuth   string `json:"tls_client_auth"`
}
```

### 7.2 统一配置验证

```go
// Validate 统一验证，在 app.Build() 时执行，不在 Start() 中执行。
func (c *Config) Validate() error {
    var errs []string

    if c.ShutdownTimeout <= 0 {
        errs = append(errs, "shutdown_timeout must be > 0")
    }
    if c.MaxSessions < 0 {
        errs = append(errs, "max_sessions must be >= 0")
    }

    knownProtocols := map[string]bool{
        "tcp": true, "udp": true, "coap": true,
        "lwm2m": true, "websocket": true,
    }

    for i, proto := range c.Protocols {
        prefix := fmt.Sprintf("protocols[%d](%s)", i, proto.Name)

        if !knownProtocols[proto.Name] {
            errs = append(errs, prefix+": unknown protocol name")
        }
        if proto.Enabled && proto.Addr == "" {
            errs = append(errs, prefix+": addr required when enabled")
        }
        if proto.MaxMessageBytes < 0 {
            errs = append(errs, prefix+": max_message_bytes must be >= 0")
        }
        // TLS 字段必须成对出现
        if (proto.TLSCertFile == "") != (proto.TLSKeyFile == "") {
            errs = append(errs, prefix+
                ": tls_cert_file and tls_key_file must be provided together")
        }
    }

    if len(errs) > 0 {
        return fmt.Errorf("%w: %s", core.ErrInvalidConfig,
            strings.Join(errs, "; "))
    }
    return nil
}
```

### 7.3 App 装配流程

```go
// internal/application/app.go
type App struct {
    gateway     *runtime.Gateway
    healthSrv   *http.Server
    metricsSrv  *http.Server
    config      Config
}

func Build(config Config, opts ...AppOption) (*App, error) {
    // 1. 统一配置验证
    if err := config.Validate(); err != nil {
        return nil, err
    }

    // 2. 创建基础设施
    logger  := observability.NewSlogLogger()
    metrics := observability.NewPrometheusMetrics()
    tracer  := observability.NoopTracer{}

    // 3. 创建运行时
    rt := runtime.NewDefaultRuntime(
        runtime.WithLogger(logger),
        runtime.WithMetrics(metrics),
        runtime.WithTracer(tracer),
        runtime.WithSessionManager(runtime.NewManager(config.MaxSessions)),
    )

    // 4. 注册全局插件
    rt.Plugins().Register(plugin.NewBlacklistPlugin())
    rt.Plugins().Register(plugin.NewRateLimitPlugin(
        plugin.WithGlobalRate(10000),
        plugin.WithPerIPRate(100),
    ))
    rt.Plugins().Register(plugin.NewHeartbeatPlugin(
        plugin.WithIdleTimeout(60 * time.Second),
    ))

    // 5. 创建 Gateway
    gw := runtime.NewGateway(
        runtime.WithRuntime(rt),
        runtime.WithGatewayLogger(logger),
        runtime.WithGatewayMetrics(metrics),
        runtime.WithShutdownTimeout(config.ShutdownTimeout),
    )

    // 6. 按配置创建并注册各协议 Server
    for _, proto := range config.Protocols {
        if !proto.Enabled {
            continue
        }
        srv, err := buildServer(proto, rt)
        if err != nil {
            return nil, fmt.Errorf("build server %s: %w", proto.Name, err)
        }
        if err := gw.RegisterServer(srv); err != nil {
            return nil, err
        }
    }

    // 7. 创建健康 / 指标 HTTP 服务
    app := &App{gateway: gw, config: config}
    app.healthSrv  = buildHealthServer(config.HealthAddr, gw)
    app.metricsSrv = buildMetricsServer(config.MetricsAddr)

    return app, nil
}

func (a *App) Run(ctx context.Context) error {
    // 启动健康 / 指标服务
    go a.healthSrv.ListenAndServe()
    go a.metricsSrv.ListenAndServe()

    // 启动 Gateway
    if err := a.gateway.Start(ctx); err != nil {
        return err
    }

    // 阻塞等待 ctx 取消（信号处理在 cmd/server/main.go）
    <-ctx.Done()

    shutdownCtx, cancel := context.WithTimeout(
        context.Background(),
        a.config.ShutdownTimeout,
    )
    defer cancel()

    // 按序关闭
    _ = a.gateway.Stop(shutdownCtx)
    _ = a.healthSrv.Shutdown(shutdownCtx)
    _ = a.metricsSrv.Shutdown(shutdownCtx)
    return nil
}
```

### 7.4 cmd/server/main.go

```go
// cmd/server/main.go
func main() {
    // 加载配置
    configPath := flag.String("config", "config.json", "path to config file")
    flag.Parse()

    config, err := application.LoadConfig(*configPath)
    if err != nil {
        log.Fatalf("load config: %v", err)
    }

    // 信号处理
    ctx, stop := signal.NotifyContext(context.Background(),
        syscall.SIGTERM, syscall.SIGINT)
    defer stop()

    // 装配并运行
    app, err := application.Build(config)
    if err != nil {
        log.Fatalf("build app: %v", err)
    }

    if err := app.Run(ctx); err != nil {
        log.Fatalf("run app: %v", err)
    }
}
```

---

**版权声明：** 本文档属于 Shark-Socket 项目，遵循项目许可证。