# shark-socket 架构方法论

> 分析日期: 2026-06-26  
> 基于: 对 ~40 个 Go 源文件的完整静态分析 + 运行时行为审查  
> 目的: 揭示项目的架构哲学、模式选择、设计折衷

---

## 一、架构风格: 端口-适配器 (Hexagonal) + 分层

shark-socket 采用了两层架构风格的融合:

```
                    ┌──────────────────────────────┐
                    │         cmd/shark-socket      │  ← 入口
                    │         internal/app/          │  ← 组装层
                    └──────────────┬───────────────┘
                                   │
              ┌────────────────────┼────────────────────┐
              │                    │                    │
     ┌────────▼────────┐  ┌───────▼────────┐  ┌───────▼────────┐
     │  internal/core/  │  │ internal/runtime│  │ internal/plugin │
     │  (端口/契约)      │  │ (编排)          │  │ (横切关注点)     │
     └────────┬────────┘  └───────┬────────┘  └───────┬────────┘
              │                    │                    │
              └────────────────────┼────────────────────┘
                                   │
                    ┌──────────────▼───────────────┐
                    │   internal/transport/         │  ← 适配器
                    │   (tcp|udp|http|ws|coap|quic| │
                    │    grpcweb)                    │
                    └──────────────────────────────┘
```

### 为什么是端口-适配器?

项目核心 (`internal/core/`) 定义的是**接口契约**而非具体实现。所有外部依赖 (网络协议、日志、指标、追踪) 都通过接口抽象:

```go
// core 层只定义接口，不导入任何外部实现
type Server interface { Protocol() Protocol; Start(ctx) error; Stop(ctx) error }
type Logger interface { Debug/Info/Warn/Error(msg, attrs...) }
type Metrics interface { IncCounter/SetGauge/ObserveHistogram }
```

适配器层 (`internal/transport/`, `internal/infra/`) 实现这些接口, 对接具体技术: TCP、QUIC、Prometheus、OpenTelemetry、BoltDB。

### 为什么同时用分层?

端口-适配器解决的是**依赖方向**问题 (外层依赖内层), 分层解决的是**抽象层级**问题:

```
Layer 0: core/        — 零依赖，纯接口 + 纯数据类型
Layer 1: infra/       — 基础设施实现 (store, cache, mqtt, pubsub)
Layer 2: runtime/     — 依赖注入容器 + 编排逻辑
Layer 3: plugin/      — 横切关注点 (安全, 限流, 持久化)
Layer 4: transport/   — 协议适配器
Layer 5: app/         — 组装一切
```

**关键约束**: 高层可以依赖低层, 低层绝不依赖高层。`core/` 是唯一零依赖的包。

---

## 二、核心设计决策深度解析

### 决策 1: Protocol 类型 — string 而非 uint8

**选择**: `type Protocol string` (例如 `"tcp"`, `"websocket"`)

**为什么不选 uint8 枚举?**

| 维度 | string | uint8 (iota) |
|------|--------|-------------|
| 扩展性 | 任何包可创建 `Protocol("my-proto")` | 需修改 core 包, 添加 const |
| 日志/指标 | 直接可读 `"tcp"` | 需要映射表 `{1:"tcp",...}` |
| 配置绑定 | JSON `"tcp"` 可直接比较 | 需解析 + 查表 |
| 内存开销 | 16 字节 (string header) | 1 字节 |
| 类型安全 | 命名类型, 不混入普通 string | 命名类型, 不混入普通 uint8 |

**折衷**: 16 字节开销换取零摩擦扩展。对于 session 元数据 (每个连接一个), 这个开销可忽略。

**SessionState 为何不同?** SessionState 用 `uint8`:
- 状态机是**封闭集合** (Connecting/Active/Draining/Closed), 不应被用户扩展
- 热路径比较 (`if s.State() == StateActive`) 需要整型效率
- `String()` 方法提供人类可读输出

两者分离是**开闭原则**的精确体现: "对扩展开放 (Protocol), 对修改封闭 (SessionState)"。

### 决策 2: 三阶段关闭 — 工业级优雅终止

```
StagedServer 接口:
  StopAccept(ctx)    →  关闭监听器, 不再接受新连接
  Drain(ctx)         →  等待进行中的请求完成
  CloseSessions(ctx) →  强制关闭剩余会话

每个阶段有独立超时:
  StageTimeouts{StopAccept, Drain, CloseSessions, Finalize}
```

**为什么需要三阶段?**

传统 `Stop()` 的困境:
```go
func (s *Server) Stop() {
    s.listener.Close()  // 拒绝新连接
    s.closeAllSessions() // 立即断开所有客户端 ← 丢失进行中的请求
}
```

三阶段模型:
```
客户端视角:
  StopAccept → [新连接被拒] → Drain → [请求正常完成] → CloseSessions → [连接断开]
  
服务端视角:
  时间线: ──StopAccept──[timeout]──Drain──[timeout]──CloseSessions──[timeout]──Finalize─→
```

**这模仿了 Kubernetes Pod 终止:**
- `preStop` hook → StopAccept
- `SIGTERM` → Drain
- `SIGKILL` (terminationGracePeriodSeconds 超时后) → CloseSessions

### 决策 3: 可选接口 — Go 的隐式满足

`StagedServer` 是**可选接口**, 通过 type assertion 检查:

```go
if staged, ok := srv.(core.StagedServer); ok {
    staged.StopAccept(ctx)
    staged.Drain(ctx)
    staged.CloseSessions(ctx)
} else {
    srv.Stop(ctx)  // 回退到简单 Stop
}
```

这种模式贯穿整个项目:
- `RuntimeConfigurable` — transport 可选接受 Runtime 注入
- `BulkDeleter` — Store 可选支持批量删除
- `io.WriterTo` 风格的 "能力接口"

**优势**: UDP (无连接协议) 不需要实现三阶段关闭; HTTP (无状态) 的 Drain 是 no-op。无需用空实现污染接口。

### 决策 4: 依赖注入 — 无框架手工 DI

```go
// Gateway 是 DI 容器
type Gateway struct {
    rt *Runtime  // 持有所有依赖
}

// Runtime 是依赖 bundle
type Runtime struct {
    sessions SessionManager
    plugins  PluginRunner
    logger   Logger
    metrics  Metrics
    tracer   Tracer
}

// 注入到 transport
server.UseRuntime(rt)
```

**为什么不用 wire/dig/fx?**

- 项目规模 ~3500 行生产代码, 手工 DI 零认知负担
- 显式注入调用链易于调试: `Gateway → NewRuntime → UseRuntime`
- 无代码生成步骤, 构建速度不受影响
- 测试时可直接构造 mock Runtime 注入

**注入时机**: Transport 在 `Gateway.Start()` 时接收 Runtime, 早于 `server.Start()`:
```go
for _, srv := range servers {
    if c, ok := srv.(RuntimeConfigurable); ok {
        c.UseRuntime(g.rt)  // 注入
    }
    srv.Start(ctx)          // 启动
}
```

### 决策 5: PluginRunner 与 Plugin 分离

```
Plugin (单个行为):           PluginRunner (编排):
  Name()                      OnAccept(Session) error
  Priority()                  OnMessage(Session,[]byte) ([]byte,error)
  OnAccept(Session) error     OnClose(Session)
  OnMessage(Session,[]byte)
  OnClose(Session)
```

**为什么分离?**

Transport 调用 `runner.OnMessage(sess, data)`, 不需要知道链中有几个 plugin、优先级如何、panic 如何处理。

PluginChain 实现 PluginRunner, 负责:
1. **排序**: 按 Priority() 升序 (Accept/Message) 或降序 (Close)
2. **panic 隔离**: defer recover 捕获每个 plugin 的 panic
3. **控制流**: ErrPluginDrop (静默丢弃), ErrPluginBlock (拒绝), 普通 error (中断)

**优先级分配**:
```
Blacklist(0) → AutoBan(5) → RateLimit(10) → Heartbeat(50) → Persistence(90) → Cluster(95)
   安全           封禁          流量           会话           持久化          跨节点
```

低数字 = 高优先级。安全检查最先执行, 持久化和集群通信最后执行。

### 决策 6: Codec[M] — 泛型桥接

```go
type Codec[M any] interface {
    Encode(M) ([]byte, error)
    Decode([]byte) (M, error)
}

func AdaptTyped[M any](codec Codec[M], h TypedHandler[M]) Handler
```

**为什么是泛型?**

在 Go 1.18 之前, 类型化处理器需要反射或 interface{}:
```go
// 旧方式 (无泛型):
func AdaptTyped(codec interface{}, handler interface{}) Handler { ... }
```

泛型让类型安全在编译时保证:
```go
type MyMsg struct { Name string }
codec := JSONCodec[MyMsg]{}
handler := func(sess Session, msg MyMsg) error { ... }

AdaptTyped(codec, handler)  // 类型检查: Codec[MyMsg] + TypedHandler[MyMsg] ✓
```

---

## 三、数据流模式

### 入站消息路径

```
网络字节 → transport.readLoop()
    │
    ├→ Framer/Datagram/Stream 解码
    │
    ├→ core.Message{SessionID, Protocol, Payload}
    │
    ├→ PluginChain.OnMessage(sess, data)  ← 插件链按 Priority 升序执行
    │   ├→ Blacklist (不适用, 在 OnAccept)
    │   ├→ AutoBan (不适用)
    │   ├→ RateLimit → 可能返回 ErrPluginDrop
    │   ├→ Heartbeat → 更新 LastActiveAt
    │   ├→ Persistence → 追加到 MessageLog
    │   └→ Cluster → 发布到 PubSub
    │
    └→ Handler(sess, msg) → sess.Send(response)
```

### 出站消息路径

```
sess.Send(payload)
    │
    ├→ TCP: 推送到 writeCh → writeLoop → Framer.WriteFrame(conn)
    ├→ UDP: 直接 conn.WriteToUDP
    ├→ HTTP: w.Write(response)
    ├→ WebSocket: conn.WriteMessage(Binary, payload)
    ├→ CoAP: 直接 conn.Write
    ├→ QUIC: 推送到 writeCh → writeLoop → OpenStream → Write → Close
    └→ gRPC-Web: w.Write(framed)
```

### 会话生命周期

```
连接建立
    │
    ├→ SessionManager.NextID()     ← 分配全局唯一 ID
    ├→ SessionManager.Register(s)  ← 注册到管理器
    ├→ PluginChain.OnAccept(sess)  ← 插件有机会拒绝 (ErrPluginBlock)
    ├→ Session.State = Active
    │
    ├→ ... 消息处理 ...
    │
    ├→ Session.State = Draining
    ├→ 排空写队列
    ├→ PluginChain.OnClose(sess)   ← 逆序执行
    ├→ Session.State = Closed
    ├→ SessionManager.Unregister(id)
    └→ conn.Close()
```

---

## 四、并发模型

### 分层并发策略

| 层级 | 并发模型 | 同步原语 |
|------|---------|---------|
| Session | 每个 session 独立 goroutine(s) | sync.Once (Close), atomic (state) |
| Transport | acceptLoop + N×connHandler + N×writeLoop | sync.WaitGroup, atomic.Bool (closed/started) |
| Plugin | 共享数据结构 + 锁 | sync.Mutex, sync.RWMutex |
| Gateway | 顺序编排 | sync.Mutex (stopMu), sync.RWMutex (server map) |
| Store | 读写锁 + BoltDB 事务 | sync.RWMutex, BoltDB 自身 MVCC |

### Goroutine 生命周期管理

**原则: 每个 `go` 语句必须有对应的退出路径和同步点。**

```go
// 正确模式 (所有 transport 都遵循):
s.wg.Add(1)
go func() {
    defer s.wg.Done()   // ← 退出时通知
    for {                // ← 无限循环有明确的退出条件
        select {
        case <-ctx.Done():
            return       // ← 退出路径 1: 上下文取消
        case msg := <-ch:
            handle(msg)
        }
    }
}()

// 关闭时:
cancel()                 // ← 触发退出路径 1
s.wg.Wait()              // ← 等待 goroutine 实际退出
```

**不正确的反模式 (项目中不存在):**
```go
go func() {
    for {
        msg := <-ch  // ← 如果 ch 从不关闭, 永久阻塞
        handle(msg)
    }
}()
```

### Channel 关闭安全

所有可能被多次关闭的 channel 都受 `sync.Once` 保护:

```go
type Server struct {
    stopCh   chan struct{}
    stopOnce sync.Once
}

func (s *Server) Stop() {
    s.stopOnce.Do(func() { close(s.stopCh) })  // ← 保证只关闭一次
}
```

---

## 五、错误处理哲学

### 分层错误策略

```
Layer 0 (core):   sentinel errors (errors.New)  → 身份标识
Layer 1 (infra):  包装 sentinel (fmt.Errorf("...: %w", err))  → 附加上下文
Layer 2 (transport): 选择性返回 / log / 丢弃  → 按场景决策
Layer 3 (plugin):  控制流错误 (ErrPluginDrop/ErrPluginBlock)  → 改变链行为
```

### "丢弃即有意" 原则

项目大量使用 `_ = err` 模式, 但**每个位置都有明确理由**:

| 模式 | 场景 | 理由 |
|------|------|------|
| `_ = sess.Close(ctx)` | defer cleanup | 主错误已确定, Close 失败不可操作 |
| `_ = conn.Close()` | 关闭已损坏的连接 | 连接可能已半关闭 |
| `_ = w.Write(...)` | 健康检查端点 | 断开的客户端无需关注 |
| `_ = s.sendACK(...)` | CoAP 去重 | 已记录 Warn (V4 修复) |

**不是** "忽略错误", 而是 "识别不可操作的错误路径并显式丢弃"。

### Plugin 控制流错误

```go
var (
    ErrPluginDrop  = errors.New("plugin drop")   // 静默丢弃消息, 不通知客户端
    ErrPluginBlock = errors.New("plugin block")  // 拒绝消息, 向客户端返回错误
)
```

这些是**控制流信号**, 不是真正的 "错误"。Plugin 返回它们来影响 PluginChain 的行为, 而不会产生 error 日志。

---

## 六、测试策略

### 测试金字塔

```
         ┌──────┐
         │ E2E  │  tests/deploy, tests/stress
         ├──────┤
         │ 集成  │  *_integration_test.go (每个 transport)
         ├──────┤
         │ 单元  │  *_test.go (与源码同目录)
         └──────┘
```

### 接口测试模式

Core 层接口通过 compile-time assertion 验证:

```go
var _ core.Server              = (*tcp.Server)(nil)
var _ core.StagedServer        = (*tcp.Server)(nil)
var _ core.RuntimeConfigurable = (*tcp.Server)(nil)
```

**优势**: 在编译时就能发现接口不匹配, 无需运行测试。

### Mock/假实现模式

- **MemoryStore**: 用于测试需要持久化的代码, 无外部依赖
- **MemoryLogger**: 捕获日志条目用于断言 (`logger.Entries()`)
- **MemoryMetrics**: 可检查指标值, 无需 Prometheus
- **NopLogger/NopMetrics/NopTracer**: 用于不需要观测性的测试
- **fakeSession**: 实现 `core.Session` 用于测试 plugin

### 当前覆盖弱点

- 无并发/压力测试 (race detector 启用但无专门的并发测试)
- 无模糊测试 (除 tcp framer 和 coap message 外)
- 集成测试仅覆盖 "happy path" e2e, 不覆盖网络故障注入

---

## 七、扩展机制

### 添加新协议

1. 在 `internal/core/types.go` 定义 `ProtocolMyProto Protocol = "myproto"` (可选, 可直接用字面量)
2. 实现 `core.Server` + 可选 `core.StagedServer`
3. 实现 `core.Session`
4. 在 `internal/app/config.go` 添加协议分支

**不需要修改 core 层** — 这是 `Protocol` 用 string 的关键优势。

### 添加新插件

1. 嵌入 `core.BasePlugin`
2. 重写需要的钩子 (`OnAccept`/`OnMessage`/`OnClose`)
3. 设置 `Priority()` 决定在链中的位置
4. 通过 `GatewayOption` 注册

插件不需要了解 transport 实现, 只依赖 `core.Session` 接口。

### 添加新 Store 后端

1. 实现 `store.Store` 接口 (5 个方法)
2. 可选实现 `store.BulkDeleter` (1 个方法)

现有 Memory 和 BoltDB 实现可作为参考。

---

## 八、架构优势

1. **零外部依赖的 core 层** — 可独立编译、测试、版本化
2. **标准化关闭** — 所有 transport 共享相同的三阶段生命周期
3. **插件正交性** — 插件通过 `core.Session` 接口工作, 与协议无关
4. **手工 DI** — 无魔法, 易于理解和调试
5. **并发安全** — 所有 goroutine 追踪, 所有 channel 关闭保护
6. **安全默认** — CheckOrigin=false, TLS≥1.2, 所有 Server 有超时
7. **编译时接口验证** — `var _ Interface = (*Impl)(nil)` 模式

## 九、架构可改进点

1. **UDP/CoAP 共享 DTLS 代码重复** — `dtlsConfig()` 函数在两个包中重复, 应提取到 `shared/`
2. **HTTP/WebSocket/gRPC-Web 共享 HTTP Server 模式** — 三个 transport 有几乎相同的 `Start()` 和 `StopAccept()` 逻辑
3. **Plugin 链不支持条件执行** — 所有 plugin 对所有 session/message 执行, 无路由/过滤机制
4. **SessionManager 无分片** — 全局 RWMutex 在高并发下可能成为瓶颈 (但目前足够)
5. **缺少断路器在 transport 层** — 每个 transport 的 accept 错误退避是硬编码 100ms, 未使用 `circuitbreaker` 包
6. **测试中无网络故障注入** — 所有测试假设网络可靠

---

## 十、关键设计原则总结

| 原则 | 体现 |
|------|------|
| **依赖倒置** | core 定义接口, transport/infra 实现 |
| **接口隔离** | Server (3 methods), StagedServer (3), Plugin (5) — 小而专注 |
| **开闭原则** | Protocol 开放扩展 (string), SessionState 封闭修改 (uint8) |
| **单一职责** | Gateway 编排, PluginChain 排序, Transport 协议适配 |
| **显式优于隐式** | 手工 DI, 显式 goroutine 追踪, 显式 channel 关闭 |
| **安全默认** | 拒绝所有 CheckOrigin, 最小 TLS 版本, 全超时设置 |
| **优雅降级** | 三阶段关闭, ErrPluginDrop/Block 控制流, 熔断器 |
