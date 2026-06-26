# CONTRACTS.md

> Shark-Socket 核心契约层完整定义  
> 版本：v0.2.x-alpha  
> 最后更新：2026-06-01

---

## 目录

1. [概述](#1-概述)
2. [Protocol 类型](#2-protocol-类型)
3. [Message 结构](#3-message-结构)
4. [Session 接口](#4-session-接口)
5. [SessionManager 接口](#5-sessionmanager-接口)
6. [Server 接口族](#6-server-接口族)
7. [Runtime 接口](#7-runtime-接口)
8. [Plugin 接口](#8-plugin-接口)
9. [Handler 类型](#9-handler-类型)
10. [Codec 接口](#10-codec-接口)
11. [可观测接口](#11-可观测接口)
12. [编译期验证要求](#12-编译期验证要求)

---

## 1. 概述

`internal/core/` 是**稳定契约层**，只定义接口、类型和约束，不包含任何具体实现。所有上层模块（runtime、transport、plugin、protocol）必须通过这些接口交互，实现依赖倒置。

**核心文件清单：**

| 文件 | 职责 |
|------|------|
| `types.go` | Protocol 类型 (string alias) + 协议常量 |
| `session.go` | Session 接口 + SessionState 枚举 + SessionManager 接口 |
| `server.go` | Server 接口 + RuntimeConfigurable + StagedServer + PluginRunner 接口 |
| `message.go` | Message 结构体 + Handler 类型 + AdaptTyped + Codec[M] 接口 |
| `plugin.go` | Plugin 接口 + BasePlugin + 控制错误 |
| `errors.go` | 完整错误体系（详见 ERRORS.md） |
| `observability.go` | Logger / Metrics / Tracer 接口 |

---

## 2. Protocol 类型

### 2.1 定义

```go
// Protocol is a string alias identifying the transport protocol.
// Defined in internal/core/types.go.
type Protocol string

const (
    ProtocolTCP     Protocol = "tcp"
    ProtocolUDP     Protocol = "udp"
    ProtocolCoAP    Protocol = "coap"
    ProtocolWS      Protocol = "websocket"
    ProtocolHTTP    Protocol = "http"
    ProtocolQUIC    Protocol = "quic"
    ProtocolGRPCWeb Protocol = "grpc-web"
    ProtocolCustom  Protocol = "custom"
)
```

### 2.2 用途

Protocol 是 string 别名，无需显式 String() 方法。Go 原生支持 string 类型的 fmt.Stringer。


### 2.3 用途

| 场景 | 使用方式 |
|------|---------|
| Gateway 注册去重 | `gateway.servers` map key |
| Session 标记来源 | `session.Protocol()` |
| 插件和指标标签 | `metrics.Counter("shark_messages_total", "protocol", proto.String())` |
| 日志和追踪属性 | `logger.Info("connection accepted", "protocol", proto)` |

---

## 3. Message 结构

### 3.1 定义

```go
// Message 是统一消息结构，核心字节层。
type Message struct {
    SessionID uint64            // 所属会话 ID
    Protocol  Protocol          // 来源协议
    Payload   []byte            // 原始字节，业务类型不进入核心层
    Meta      map[string]string // 协议级元数据（预分配容量 4）
}
```

### 3.2 设计约束

| 字段 | 约束 | 说明 |
|------|------|------|
| `SessionID` | 由 SessionManager.NextID() 分配，全局递增 | 用于跨协议查询、日志关联 |
| `Protocol` | 不可变，创建时确定 | 用于指标标签、插件过滤 |
| `Payload` | 始终为 `[]byte` | 业务结构通过 `Codec[M]` 在 Handler 层适配，核心层不感知业务类型 |
| `Meta` | 协议级透传字段 | 用于 CoAP Token、HTTP Header 等，**不用于业务属性**（业务属性用 `Session.SetMeta`） |

### 3.3 类型化消息适配路径

```
网络帧 → []byte → core.Message
           ↓
       Codec[M].Decode
           ↓
    TypedHandler[M](sess, typedMsg)
           ↓
       业务逻辑处理
           ↓
       Codec[M].Encode
           ↓
    Session.Send([]byte)
```

详见 [§10 Codec 接口](#10-codec-接口)。

---

## 4. Session 接口

### 4.1 SessionState 枚举

```go
// SessionState 会话状态机。
type SessionState uint8

const (
    Connecting SessionState = 0 // 连接建立中
    Active     SessionState = 1 // 正常通信
    Draining   SessionState = 2 // 排空写队列中
    Closed     SessionState = 3 // 已关闭
)
```

**状态转换规则：**

```
Connecting ──accept成功──→ Active ──Close()──→ Draining ──drain完成──→ Closed
    │                        │                                            ▲
    └──error/fatal───────────┴────────────────────────────────────────────┘
```

详见 `LIFECYCLE.md §会话状态机`。

### 4.2 Session 接口定义

```go
// Session 是运行时统一会话接口。
// 核心字节层：Send 只接收 []byte，类型安全通过 Codec 在上层实现。
type Session interface {
    // === 身份与元信息（不可变） ===
    ID()           uint64
    Protocol()     Protocol
    RemoteAddr()   net.Addr
    LocalAddr()    net.Addr
    CreatedAt()    time.Time

    // === 状态（原子操作） ===
    State()        SessionState
    IsAlive()      bool           // State() == Active
    LastActiveAt() time.Time      // 用于心跳、TTL、清理

    // === 生命周期 ===
    Context()      context.Context  // 关闭时 cancel
    Send([]byte)   error            // 关闭后返回 ErrSessionClosed
    Close(context.Context) error    // 幂等关闭，ctx 控制 drain 超时

    // === 元数据（线程安全 KV） ===
    SetMeta(key string, val any)
    GetMeta(key string) (any, bool)
    DelMeta(key string)
}
```

### 4.3 关键实现要求

| 方法 | 约束 |
|------|------|
| `Send` | **非阻塞**：队列满时立即返回 `ErrWriteQueueFull`，不阻塞调用方；Draining/Closed 状态返回 `ErrSessionClosed` |
| `Close` | **幂等**：多次调用等价一次，必须通过 `sync.Once` 保证；**drain**：长连接协议必须等待写队列排空（超时受 ctx 控制） |
| `State` | **原子读取**：内部用 `atomic.Int32` 实现，无锁 |
| `LastActiveAt` | **原子更新**：协议层收到任意数据时调用内部 `TouchActive()` 方法（原子存储 `time.Now().UnixNano()`） |
| `SetMeta/GetMeta/DelMeta` | **线程安全**：内部用 `sync.Map` 实现 |

### 4.4 Close 六步状态机（长连接协议）

详见 `LIFECYCLE.md §Session 关闭流程`，此处仅列出约束：

```
步骤1：CAS Active → Draining（失败说明已在关闭，直接返回）
步骤2：若 writeLoop 已启动：close(draining) 信号触发排空
步骤3：等待 writeQueue 排空（DrainTimeout 超时后强制继续）
步骤4：CAS Draining → Closed
步骤5：CancelContext() → 通知所有 <-ctx.Done() 的 goroutine 退出
步骤6：conn.Close()
```

### 4.5 编译期验证示例

```go
// 在各协议 session.go 中
var _ Session = (*TCPSession)(nil)
var _ Session = (*UDPSession)(nil)
var _ Session = (*CoAPSession)(nil)
var _ Session = (*WSSession)(nil)
```

---

## 5. SessionManager 接口

### 5.1 定义

```go
type SessionManager interface {
    // 分配全局递增 Session ID
    NextID() uint64

    // 注册 / 注销 / 查询
    Register(Session) error       // 超容返回 ErrSessionCapacity
    Unregister(id uint64)
    Get(id uint64) (Session, bool)

    // 统计与遍历
    Count() int64
    Range(func(Session) bool)     // 可中断遍历，返回 false 停止
    Snapshot() []Session  -- returns a copy of all active sessions

    // 广播（内部快照，不持锁发送）
    Broadcast([]byte) error

    // 关闭所有当前会话（不永久关闭 Manager，Gateway Stop 后可复用）
    CloseAll(context.Context) error
}
```

### 5.2 设计边界

| 约束 | 说明 |
|------|------|
| Manager 不创建协议连接 | 不拥有 listener，只管理已注册的 Session |
| `CloseAll` 不永久关闭 Manager | Gateway Stop 后再 Start 时，Manager 仍可复用（Start→Stop→Start 可重入） |
| `Broadcast` 快照发送 | 先持锁复制 session 列表，释放锁后遍历发送，避免持锁 Send 阻塞 |
| `NextID` 跨重启可能复用 | `atomic.Uint64` 全局递增，重启后从 1 开始，Session 不依赖 ID 的全局唯一性 |

### 5.3 并发安全保证

| 方法 | 实现要求 |
|------|---------|
| `NextID` | `atomic.Add(1)`，无锁 |
| `Count` | `atomic.Load`，无锁 |
| `Register/Unregister/Get` | P0：全局 `sync.RWMutex`；P2：32 分片锁（benchmark 证明后引入） |
| `Broadcast` | 见下文 Broadcast 实现模式 |

### 5.4 Broadcast 实现模式

```go
func (m *manager) Broadcast(data []byte) error {
    // 1. 快照：持锁收集所有 session 引用
    m.mu.RLock()
    snapshot := make([]Session, 0, len(m.sessions))
    for _, s := range m.sessions {
        snapshot = append(snapshot, s)
    }
    m.mu.RUnlock()

    // 2. 释放锁后发送，避免持锁 Send 阻塞
    var errs []error
    for _, s := range snapshot {
        if err := s.Send(data); err != nil {
            errs = append(errs, err)
        }
    }
    return errors.Join(errs...)
}
```

### 5.5 P0 与 P2 实现对比

| 维度 | P0（当前） | P2（演进） | 触发条件 |
|------|-----------|-----------|---------|
| 锁粒度 | 全局 `sync.RWMutex` | 32 分片锁 | benchmark 证明锁竞争为瓶颈 |
| LRU 淘汰 | 不实现 | per-shard LRU | 明确需要限制 MaxSessions |
| 分片函数 | 不分片 | `id & 31` | - |

---

## 6. Server 接口族

### 6.1 基础 Server 接口

```go
// Server 是所有协议服务的基础接口。
type Server interface {
    Protocol() Protocol
    Start(context.Context) error
    Stop(context.Context) error
}
```

### 6.2 RuntimeConfigurable 接口

```go
// RuntimeConfigurable：支持 Gateway 运行时注入。
// 协议实现此接口后，Gateway 在启动前自动调用 UseRuntime。
type RuntimeConfigurable interface {
    UseRuntime(Runtime)
}
```

**使用规则：**
- Gateway 在 `Start()` 前遍历所有 Server，检测是否实现 `RuntimeConfigurable`
- 若实现，调用 `UseRuntime(gateway.runtime)`
- 协议层只依赖 `Runtime` 接口，不依赖具体实现
- 单独启动协议服务时（测试场景），可使用 `DefaultRuntime`（空实现）

### 6.3 StagedServer 接口

```go
// StagedServer：支持分阶段关闭的协议实现（推荐长连接协议实现此接口）。
type StagedServer interface {
    StopAccept(context.Context) error    // 停止接收新连接 / 新请求
    Drain(context.Context) error         // 等待读写 goroutine 收敛
    CloseSessions(context.Context) error // 关闭协议持有的活跃会话
}
```

**各协议实现说明：**

| 协议 | StopAccept | Drain | CloseSessions |
|------|-----------|-------|---------------|
| TCP | `listener.Close()` + 停止 acceptLoop | `WaitGroup` 等待 readLoop/writeLoop | 遍历 Close 所有 TCPSession |
| WebSocket | 停止 HTTP Upgrade Mux | 等待 pingLoop/readLoop | 发送 Close 帧后关闭 WSSession |
| UDP | 不适用（无 accept） | 不适用（单 goroutine） | 清理所有伪会话 |
| CoAP | 不适用（基于 UDP） | 不适用（单 goroutine） | 清理所有伪会话 + 停止 retransmitLoop |
| HTTP | `http.Server.Shutdown()` | 不适用（http.Server 内部已处理） | 不适用（per-request session） |

详见 `GATEWAY.md §5.3 Gateway 停止流程` 和 `TRANSPORT.md` 各协议章节。

### 6.4 Drain 失败降级策略

```go
// 伪代码
func (s *TCPServer) Drain(ctx context.Context) error {
    done := make(chan struct{})
    go func() {
        s.wg.Wait() // 等待所有 readLoop/writeLoop 退出
        close(done)
    }()

    select {
    case <-done:
        return nil
    case <-ctx.Done():
        // 超时后强制关闭所有 goroutine（通过 context cancel）
        s.logger.Warn("drain timeout, forcing close",
            "goroutine_count", runtime.NumGoroutine())
        return ErrDrainTimeout
    }
}
```

---

## 7. Runtime 接口

### 7.1 定义

```go
// Runtime 是协议运行所需共享依赖容器。
// Gateway 创建并注入，协议通过 UseRuntime 接收，不得自行创建全局替代。
type Runtime interface {
    Sessions() SessionManager
    Plugins()  PluginRunner
    Logger()   Logger
    Metrics()  Metrics
    Tracer()   Tracer
}
```

### 7.2 使用规则

| 规则 | 说明 |
|------|------|
| 协议层只依赖 `Runtime` 接口 | 不依赖 `runtime/gateway.go` 具体实现 |
| 单独启动协议时 | 可使用 `DefaultRuntime`（空实现或测试桩） |
| 通过 Gateway 启动时 | 必须接收 Gateway 注入的 Runtime |
| 各协议实际使用的子集 | 应在实现注释中标注（便于评估后续拆分需要） |

### 7.3 DefaultRuntime 示例（测试场景）

```go
// internal/runtime/runtime_impl.go
type DefaultRuntime struct {
    sessions SessionManager
    plugins  PluginRunner
    logger   Logger
    metrics  Metrics
    tracer   Tracer
}

func NewDefaultRuntime(opts ...RuntimeOption) *DefaultRuntime {
    r := &DefaultRuntime{
        sessions: newManager(),
        plugins:  newRunner(),
        logger:   slogLogger{},
        metrics:  PrometheusMetrics{},
        tracer:   NoopTracer{},
    }
    for _, opt := range opts {
        opt(r)
    }
    return r
}
```

---

## 8. Plugin 接口

### 8.1 Plugin 基础接口

```go
// Plugin 是插件基础接口。
type Plugin interface {
    Name()     string
    Priority() int  // 数字越小越先执行

    // OnAccept：连接建立后执行，返回 ErrPluginBlock 拒绝连接。
    OnAccept(Session) error

    // OnMessage：收到消息后执行，支持改写 payload。
    // 返回 ErrPluginDrop 丢弃消息（连接可继续）。
    // 返回 ErrPluginBlock 关闭连接。
    // 返回普通 error 按协议策略处理。
    OnMessage(Session, []byte) ([]byte, error)

    // OnClose：会话关闭时执行，逆序执行，不可中断。
    OnClose(Session)
}
```

### 8.2 BasePlugin 空实现

```go
// BasePlugin 提供空实现，自定义插件只需覆盖关心的方法。
type BasePlugin struct{}

func (BasePlugin) Name() string                                     { return "base" }
func (BasePlugin) Priority() int                                    { return 0 }
func (BasePlugin) OnAccept(Session) error                           { return nil }
func (BasePlugin) OnMessage(Session, []byte) ([]byte, error)        { return nil, nil }
func (BasePlugin) OnClose(Session)                                  {}
```

### 8.3 PluginRunner 接口

```go
// PluginRunner is the plugin chain execution interface.
// Defined in internal/core/server.go.
type PluginRunner interface {
    OnAccept(Session) error
    OnMessage(Session, []byte) ([]byte, error)
    OnClose(Session)
}
```

**Note:** Plugin registration is handled by the concrete `PluginChain` type, not the `PluginRunner` interface.

### 8.4 特殊控制错误

```go
// 特殊控制错误（非业务错误，控制插件链行为）
var (
    ErrPluginDrop  = errors.New("shark: plugin drop message")  // 丢弃消息，不传给 Handler
    ErrPluginBlock = errors.New("shark: plugin block session") // 拒绝连接或关闭连接
)
```

详见 `ERRORS.md §控制错误` 和 `PLUGIN.md §执行规则`。

### 8.5 PluginRunner 执行规则

**OnAccept（按 Priority 升序）：**

```
→ Plugin returns non-nil error：中断 + Close(sess)
→ panic：recover + log via configured logger + return ErrPluginPanic
→ nil：continue to next plugin
```

**OnMessage（按 Priority 升序，支持 payload 改写）：**

```
data = originalPayload
for each plugin:
  out, err = plugin.OnMessage(sess, data)
  → ErrPluginDrop：停止链，不调用 Handler，返回 nil error
  → Other error：停止链，返回 error
  → nil：data = out（允许 plugin 改写 payload）
return data, nil
```

**OnClose（按 Priority 逆序，不可中断）：**

```
for each plugin（逆序）:
  panic-safe：即使 panic 也继续执行后续插件
  plugin.OnClose(sess)
```

详见 `PLUGIN.md §执行规则` 和 `GATEWAY.md §PluginRunner 实现`。

---

## 9. Handler 类型

### 9.1 Handler 函数类型（原始字节层）

```go
// Handler 是消息处理器函数类型。
type Handler func(Session, Message) error
```

### 9.2 TypedHandler 函数类型（类型化层）

```go
// TypedHandler 是类型化消息处理器，通过 AdaptTyped 适配为 Handler。
type TypedHandler[M any] func(Session, M) error
```

### 9.3 AdaptTyped 适配函数

```go
// AdaptTyped 将 TypedHandler[M] 包装为 Handler，解码失败返回 ErrDecodeFailure。
func AdaptTyped[M any](codec Codec[M], h TypedHandler[M]) Handler {
    return func(s Session, msg Message) error {
        typed, err := codec.Decode(msg.Payload)
        if err != nil {
            return fmt.Errorf("%w: %v", ErrDecodeFailure, err)
        }
        return h(s, typed)
    }
}
```

### 9.4 使用示例

```go
// 业务层
type MyMessage struct {
    Action string `json:"action"`
    Data   string `json:"data"`
}

func handleTyped(sess Session, msg MyMessage) error {
    log.Println("received", msg.Action, msg.Data)
    // 业务逻辑...
    return nil
}

// 装配层（application/app.go）
codec := JSONCodec[MyMessage]{}
handler := AdaptTyped(handleTyped, codec)
server := NewTCPServer(handler, WithAddr(":8080"))
```

---

## 10. Codec 接口

### 10.1 Codec[M] 泛型接口

```go
// Codec[M] 在 []byte 与业务类型 M 之间转换，定义在 core 层作为稳定契约。
type Codec[M any] interface {
    Encode(M) ([]byte, error)
    Decode([]byte) (M, error)
    ContentType() string // "application/json" / "application/protobuf" 等
}
```

### 10.2 内置 Codec 实现

**RawCodec（零转换开销）：**

```go
// RawCodec 用于 []byte 透传，无转换开销。
type RawCodec struct{}

func (RawCodec) Encode(data []byte) ([]byte, error) { return data, nil }
func (RawCodec) Decode(data []byte) ([]byte, error) { return data, nil }
func (RawCodec) ContentType() string                { return "application/octet-stream" }
```

**JSONCodec（通用 JSON 编解码）：**

```go
// JSONCodec 使用 encoding/json 编解码。
type JSONCodec[M any] struct{}

func (JSONCodec[M]) Encode(msg M) ([]byte, error) {
    return json.Marshal(msg)
}

func (JSONCodec[M]) Decode(data []byte) (M, error) {
    var msg M
    err := json.Unmarshal(data, &msg)
    return msg, err
}

func (JSONCodec[M]) ContentType() string { return "application/json" }
```

### 10.3 业务自定义 Codec

用户可实现 Protobuf、MessagePack、CBOR 等 Codec，只需满足 `Codec[M]` 接口。

---

## 11. 可观测接口

### 11.1 Logger 接口

```go
// Logger 结构化日志接口。
// 实现参考 internal/core/observability.go slogLogger。
type Logger interface {
    Debug(msg string, attrs ...any)
    Info(msg string, attrs ...any)
    Warn(msg string, attrs ...any)
    Error(msg string, attrs ...any)
}
```

**日志关键字段规范：**

```
session_id, protocol, remote_addr, local_addr,
plugin_name, error, duration_ms, trace_id, request_id,
msg_size, queue_depth, state, reason
```

详见 `OBSERVABILITY.md §Logger`。

### 11.2 Metrics 接口

```go
// Metrics 指标抽象。
// 实现参考 internal/infra/observability/prometheus.go PrometheusMetrics。
// Labels 以 key-value 交替传入：IncCounter("name", "protocol", "tcp", "status", "ok")
type Metrics interface {
    IncCounter(name string, labels ...string)
    SetGauge(name string, value float64, labels ...string)
    ObserveHistogram(name string, value float64, labels ...string)
}
```

**Prometheus 指标命名规范：**

```
shark_{子系统}_{动作/状态}_{单位后缀}
后缀：_total（Counter）/ _active（Gauge）/ _seconds（Histogram）/ _bytes（Counter/Histogram）
```

详见 `OBSERVABILITY.md §Prometheus 指标导出`。

### 11.3 Tracer 接口

```go
// Tracer 追踪抽象（兼容 OpenTelemetry）。
// 实现参考 internal/infra/observability/otel.go OpenTelemetryTracer。
// Attrs 以 key-value 交替传入：Start(ctx, "tcp.handleConn", "remote_addr", addr)
type Tracer interface {
    Start(ctx context.Context, name string, attrs ...any) (context.Context, Span)
}

type Span interface {
    End()
    RecordError(err error)
}
```

> **设计说明：** Span 仅保留 `End()` 和 `RecordError()`。属性通过 `Tracer.Start` 的 variadic attrs 一次性传入，
> 不提供单独的 `SetAttribute` 方法，以保持接口极简。OTel 适配层在 `Start` 时将 attrs 转换为 `attribute.KeyValue`。

详见 `OBSERVABILITY.md §Tracer`。

---

## 12. 编译期验证要求

### 12.1 接口满足检查

所有实现必须在包内声明编译期验证：

```go
// internal/runtime/session_manager.go
var _ core.SessionManager = (*manager)(nil)

// internal/transport/tcp/session.go
var _ core.Session = (*TCPSession)(nil)

// internal/transport/tcp/server.go
var _ core.Server = (*Server)(nil)
var _ core.RuntimeConfigurable = (*Server)(nil)
var _ core.StagedServer = (*Server)(nil)

// internal/plugin/blacklist.go
var _ core.Plugin = (*BlacklistPlugin)(nil)
```

### 12.2 Codec 泛型约束（可选）

若需要限制 Codec 类型参数，可定义约束：

```go
// 示例：限制 M 必须是 struct 类型（可选，当前未使用）
type StructConstraint interface {
    comparable
}
```
