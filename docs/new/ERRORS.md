# ERRORS.md

> Shark-Socket 错误体系完整定义  
> 版本：v0.1.0  
> 最后更新：2026-06-01

---

## 目录

1. [概述](#1-概述)
2. [错误分类](#2-错误分类)
3. [完整错误变量列表](#3-完整错误变量列表)
4. [分类判断函数](#4-分类判断函数)
5. [错误使用规范](#5-错误使用规范)
6. [控制错误与业务错误](#6-控制错误与业务错误)

---

## 1. 概述

`internal/core/errors.go` 定义了框架的**完整错误体系**，包括：

- **错误变量**：所有预定义错误，使用 `errors.New` 创建，支持 `errors.Is` 判断
- **分类函数**：IsRetryable / IsFatal / IsSecurityRejection / IsPluginControl / IsTransient
- **命名规范**：`Err` 前缀 + 驼峰（如 `ErrSessionClosed`），不使用缩写（如 `ErrSessClosed`）

**设计原则：**

| 原则 | 说明 |
|------|------|
| 可识别性 | 所有框架错误用 `errors.Is` 判断，业务错误用 `errors.As` 提取 |
| 语义清晰 | 错误名称直接表达失败原因，不需要查看错误信息 |
| 分层归属 | 错误变量定义在 `core/errors.go`，具体错误包装在各层实现中 |
| 控制流分离 | 插件控制错误（ErrPluginDrop/ErrPluginBlock）不是真正的业务错误 |

---

## 2. 错误分类

错误按**职责边界**分为以下类别：

| 类别 | 前缀 | 说明 |
|------|------|------|
| 会话错误 | `ErrSession*` | Session 生命周期、容量限制 |
| 消息错误 | `ErrMessage*` / `ErrFrame*` | 消息大小、帧格式、队列 |
| 编解码错误 | `ErrEncode*` / `ErrDecode*` | Codec 转换失败 |
| 超时错误 | `Err*Timeout` | 读写、空闲、心跳、关闭超时 |
| 服务错误 | `ErrServer*` / `ErrListen*` | Server 生命周期、监听失败 |
| 插件控制错误 | `ErrPlugin*` | 插件链控制流（非业务错误） |
| 安全错误 | `ErrRateLimited` / `ErrBlacklisted` / `ErrAutoBanned` | 安全策略拒绝 |
| 协议错误 | `Err{Protocol}*` | 协议特定错误（CoAP、gRPC-Web 等） |
| 网关错误 | `ErrGateway*` / `ErrNoServer*` | Gateway 编排错误 |
| 基础设施错误 | `ErrCache*` / `ErrStore*` / `ErrCircuit*` / `ErrPubSub*` | 外部依赖失败 |
| 配置错误 | `ErrInvalidConfig` | 配置验证失败 |

---

## 3. 完整错误变量列表

### 3.1 会话错误

```go
// 会话生命周期与状态
var (
    ErrSessionNotFound  = errors.New("shark: session not found")
    ErrSessionClosed    = errors.New("shark: session closed")
    ErrSessionCapacity  = errors.New("shark: session manager at capacity")
    ErrSessionLimit     = errors.New("shark: session limit reached") // 协议级限制
)
```

**使用场景：**

| 错误 | 返回时机 | 调用方处理 |
|------|---------|-----------|
| `ErrSessionNotFound` | `SessionManager.Get(id)` 未找到 | 跨节点路由场景，降级查询 Cache |
| `ErrSessionClosed` | `Session.Send()` 在 Draining/Closed 状态 | 停止发送，记录日志 |
| `ErrSessionCapacity` | `SessionManager.Register()` 超过 MaxSessions | 拒绝连接，触发 LRU 淘汰（P2） |
| `ErrSessionLimit` | 协议级 MaxSessions 限制（如 UDP Server） | 拒绝新伪会话 |

### 3.2 消息错误

```go
// 消息大小与格式
var (
    ErrMessageTooLarge  = errors.New("shark: message too large")
    ErrInvalidMessage   = errors.New("shark: invalid message")
    ErrWriteQueueFull   = errors.New("shark: write queue full")
)

// 帧解析
var (
    ErrFrameTooLarge    = errors.New("shark: frame too large")
    ErrInvalidFrame     = errors.New("shark: invalid frame")
)
```

**使用场景：**

| 错误 | 返回时机 | 调用方处理 |
|------|---------|-----------|
| `ErrMessageTooLarge` | 消息超过 `MaxMessageSize` | HTTP 返回 413，TCP/WS 关闭连接 |
| `ErrInvalidMessage` | 协议帧校验失败（如 CoAP 帧长度不一致） | 丢弃消息，记录错误计数 |
| `ErrWriteQueueFull` | `Session.Send()` 写队列满 | 调用方重试或丢弃（根据 QoS） |
| `ErrFrameTooLarge` | 帧长度超过 Framer 限制 | 关闭连接，计入连续错误 |
| `ErrInvalidFrame` | 帧格式错误（如长度前缀损坏） | 关闭连接，计入连续错误 |

### 3.3 编解码错误

```go
// Codec 转换失败
var (
    ErrEncodeFailure    = errors.New("shark: encode failure")
    ErrDecodeFailure    = errors.New("shark: decode failure")
)
```

**使用场景：**

| 错误 | 返回时机 | 调用方处理 |
|------|---------|-----------|
| `ErrEncodeFailure` | `Codec.Encode(msg)` 失败（如 JSON 序列化错误） | 返回 500 或关闭连接，记录业务错误 |
| `ErrDecodeFailure` | `Codec.Decode(data)` 失败（如 JSON 反序列化错误） | 返回 400 或丢弃消息，记录格式错误 |

### 3.4 超时错误

```go
// 各类超时
var (
    ErrReadTimeout      = errors.New("shark: read timeout")
    ErrWriteTimeout     = errors.New("shark: write timeout")
    ErrIdleTimeout      = errors.New("shark: idle timeout")
    ErrHeartbeatTimeout = errors.New("shark: heartbeat timeout")
    ErrDrainTimeout     = errors.New("shark: drain timeout")
)
```

**使用场景：**

| 错误 | 返回时机 | 调用方处理 |
|------|---------|-----------|
| `ErrReadTimeout` | `conn.SetReadDeadline()` 超时 | 关闭连接，记录超时 |
| `ErrWriteTimeout` | `conn.SetWriteDeadline()` 超时 | 关闭连接，记录超时 |
| `ErrIdleTimeout` | `LastActiveAt() + IdleTimeout < now` | HeartbeatPlugin 关闭连接 |
| `ErrHeartbeatTimeout` | 心跳超时（WebSocket PongTimeout） | 关闭连接，记录心跳超时 |
| `ErrDrainTimeout` | `Session.Close()` drain 超时 | 记录 Warn，强制关闭 |

### 3.5 服务错误

```go
// Server 生命周期
var (
    ErrServerClosed     = errors.New("shark: server closed")
    ErrServerNotStarted = errors.New("shark: server not started")
    ErrListenFailed     = errors.New("shark: listen failed")
)
```

**使用场景：**

| 错误 | 返回时机 | 调用方处理 |
|------|---------|-----------|
| `ErrServerClosed` | `Server.Start()` 在已关闭的 Server 上调用 | 返回错误，不启动 |
| `ErrServerNotStarted` | `Server.Stop()` 在未启动的 Server 上调用 | 直接返回 nil（幂等） |
| `ErrListenFailed` | `net.Listen()` 失败（端口占用、权限不足） | Gateway 启动失败，回滚已启动的 Server |

### 3.6 插件控制错误

```go
// 插件控制流（非业务错误）
var (
    ErrPluginDrop       = errors.New("shark: plugin drop message")  // 丢弃消息
    ErrPluginBlock      = errors.New("shark: plugin block session") // 拒绝连接或关闭连接
    ErrPluginDuplicate  = errors.New("shark: plugin duplicate name") // 重复注册（Warn 日志）
)
```

**使用场景：**

| 错误 | 返回时机 | 调用方处理 |
|------|---------|-----------|
| `ErrPluginDrop` | `Plugin.OnMessage()` 返回 | 停止插件链，不调用 Handler，连接继续 |
| `ErrPluginBlock` | `Plugin.OnAccept()` 或 `OnMessage()` 返回 | 关闭连接，记录拒绝原因 |
| `ErrPluginDuplicate` | `PluginRunner.Register()` 重复名称 | 覆盖旧插件，记录 Warn |

**注意：** 这些错误是**控制流语义**，不是真正的业务错误，详见 [§6 控制错误与业务错误](#6-控制错误与业务错误)。

### 3.7 安全错误

```go
// 安全策略拒绝
var (
    ErrRateLimited      = errors.New("shark: rate limited")
    ErrBlacklisted      = errors.New("shark: ip blacklisted")
    ErrAutoBanned       = errors.New("shark: auto banned")
    ErrMessageRateExceeded = errors.New("shark: message rate exceeded")
)
```

**使用场景：**

| 错误 | 返回时机 | 调用方处理 |
|------|---------|-----------|
| `ErrRateLimited` | RateLimitPlugin 连接速率超限 | OnAccept 返回 ErrPluginBlock，拒绝连接 |
| `ErrBlacklisted` | BlacklistPlugin IP 在黑名单 | OnAccept 返回 ErrPluginBlock，拒绝连接 |
| `ErrAutoBanned` | AutoBanPlugin 触发自动封禁 | 加入黑名单，拒绝连接 |
| `ErrMessageRateExceeded` | RateLimitPlugin 消息速率超限 | OnMessage 返回 ErrPluginDrop，丢弃消息 |

### 3.8 协议错误

```go
// 协议特定错误
var (
    ErrCoAPInvalidMessage      = errors.New("shark: coap invalid message")
    ErrGRPCWebMalformedFrame   = errors.New("shark: grpc-web malformed frame")
    ErrUnsupportedVersion      = errors.New("shark: unsupported version")
)
```

**使用场景：**

| 错误 | 返回时机 | 调用方处理 |
|------|---------|-----------|
| `ErrCoAPInvalidMessage` | CoAP 帧校验失败（Version != 1 / TKL > 8） | 丢弃消息，计入协议错误 |
| `ErrGRPCWebMalformedFrame` | gRPC-Web 帧标志位非法 | 返回 400，关闭连接 |
| `ErrUnsupportedVersion` | 协议版本不支持 | 返回协议错误码，拒绝连接 |

### 3.9 网关错误

```go
// Gateway 编排
var (
    ErrNoServerRegistered   = errors.New("shark: no server registered")
    ErrDuplicateProtocol    = errors.New("shark: duplicate protocol")
    ErrGatewayNotStarted    = errors.New("shark: gateway not started")
    ErrGracefulShutdown     = errors.New("shark: graceful shutdown") // 关闭信号（非错误）
)
```

**使用场景：**

| 错误 | 返回时机 | 调用方处理 |
|------|---------|-----------|
| `ErrNoServerRegistered` | `Gateway.Start()` 时 servers 为空 | 返回错误，不启动 |
| `ErrDuplicateProtocol` | 注册重复 Protocol 的 Server | 返回错误，拒绝注册 |
| `ErrGatewayNotStarted` | `Gateway.Stop()` 在未启动的 Gateway 上调用 | 直接返回 nil（幂等） |
| `ErrGracefulShutdown` | Handler 可感知的关闭信号 | Handler 返回此错误表示主动关闭 |

### 3.10 基础设施错误

```go
// 外部依赖失败
var (
    ErrCacheMiss        = errors.New("shark: cache miss")
    ErrStoreNotFound    = errors.New("shark: store key not found")
    ErrCircuitOpen      = errors.New("shark: circuit breaker open")
    ErrPubSubClosed     = errors.New("shark: pubsub closed")
    ErrInfrastructure   = errors.New("shark: infrastructure error") // 通用基础设施错误
    ErrDegraded         = errors.New("shark: service degraded")     // 降级状态
)
```

**使用场景：**

| 错误 | 返回时机 | 调用方处理 |
|------|---------|-----------|
| `ErrCacheMiss` | `Cache.Get(key)` 未找到 | 降级到本地查找或返回默认值 |
| `ErrStoreNotFound` | `Store.Load(key)` 未找到 | 返回 404 或初始化新记录 |
| `ErrCircuitOpen` | CircuitBreaker 熔断状态 | 快速失败，跳过调用 |
| `ErrPubSubClosed` | PubSub 已关闭 | 停止发布，记录错误 |
| `ErrInfrastructure` | 通用基础设施错误（包装具体错误） | 降级或重试 |
| `ErrDegraded` | 服务进入降级状态 | 记录告警，跳过非核心功能 |

### 3.11 配置错误

```go
// 配置验证
var (
    ErrInvalidConfig    = errors.New("shark: invalid configuration")
)
```

**使用场景：**

| 错误 | 返回时机 | 调用方处理 |
|------|---------|-----------|
| `ErrInvalidConfig` | `application.Config.Validate()` 失败 | 拒绝启动，输出具体字段错误 |

---

## 4. 分类判断函数

### 4.1 IsRetryable

```go
// IsRetryable：调用方可安全重试
func IsRetryable(err error) bool {
    return errors.Is(err, ErrWriteQueueFull) ||
           errors.Is(err, ErrCircuitOpen) ||
           errors.Is(err, ErrRateLimited) ||
           errors.Is(err, ErrCacheMiss)
}
```

**使用场景：**

```go
if err := sess.Send(data); err != nil {
    if IsRetryable(err) {
        time.Sleep(10 * time.Millisecond)
        return sess.Send(data) // 重试一次
    }
    return err
}
```

### 4.2 IsFatal

```go
// IsFatal：连接或服务不可继续
func IsFatal(err error) bool {
    return errors.Is(err, ErrSessionClosed) ||
           errors.Is(err, ErrServerClosed) ||
           errors.Is(err, ErrFrameTooLarge) ||
           errors.Is(err, ErrMessageTooLarge)
}
```

**使用场景：**

```go
if err := handleMessage(sess, msg); err != nil {
    if IsFatal(err) {
        sess.Close(context.Background()) // 立即关闭连接
        return err
    }
    logger.Warn("non-fatal error", "error", err)
}
```

### 4.3 IsSecurityRejection

```go
// IsSecurityRejection：安全策略拒绝
func IsSecurityRejection(err error) bool {
    return errors.Is(err, ErrBlacklisted) ||
           errors.Is(err, ErrAutoBanned) ||
           errors.Is(err, ErrRateLimited) ||
           errors.Is(err, ErrMessageRateExceeded)
}
```

**使用场景：**

```go
if err := pluginChain.RunAccept(sess); err != nil {
    if IsSecurityRejection(err) {
        metrics.Counter("shark_rejected_connections_total",
            "protocol", sess.Protocol().String(),
            "reason", err.Error()).Inc()
    }
    return err
}
```

### 4.4 IsPluginControl

```go
// IsPluginControl：插件控制流（非业务错误）
func IsPluginControl(err error) bool {
    return errors.Is(err, ErrPluginDrop) ||
           errors.Is(err, ErrPluginBlock)
}
```

**使用场景：**

```go
data, err := pluginChain.RunMessage(sess, payload)
if err != nil {
    if IsPluginControl(err) {
        // 正常控制流，不记录业务错误
        return err
    }
    logger.Error("plugin error", "error", err)
    return err
}
```

### 4.5 IsTransient

```go
// IsTransient：临时故障，可降级处理
func IsTransient(err error) bool {
    return errors.Is(err, ErrCircuitOpen) ||
           errors.Is(err, ErrCacheMiss) ||
           errors.Is(err, ErrPubSubClosed) ||
           errors.Is(err, ErrInfrastructure)
}
```

**使用场景：**

```go
if err := store.Save(ctx, key, data); err != nil {
    if IsTransient(err) {
        logger.Warn("transient failure, skipping persistence", "error", err)
        return nil // 降级处理，不阻塞主流程
    }
    return err
}
```

---

## 5. 错误使用规范

### 5.1 错误包装

使用 `fmt.Errorf` 包装错误，保留原始错误信息：

```go
// ✓ 正确
if err := conn.Read(buf); err != nil {
    return fmt.Errorf("%w: %v", ErrReadTimeout, err)
}

// ✗ 错误（丢失原始错误）
if err := conn.Read(buf); err != nil {
    return ErrReadTimeout
}
```

### 5.2 错误判断

使用 `errors.Is` 判断预定义错误：

```go
// ✓ 正确
if errors.Is(err, ErrSessionClosed) {
    // 处理逻辑
}

// ✗ 错误（字符串比较不可靠）
if err != nil && err.Error() == "shark: session closed" {
    // 处理逻辑
}
```

### 5.3 错误返回优先级

多个错误同时发生时，按优先级返回：

```
1. Fatal 错误（ErrSessionClosed / ErrFrameTooLarge）
2. 安全错误（ErrBlacklisted / ErrRateLimited）
3. 插件控制错误（ErrPluginDrop / ErrPluginBlock）
4. 业务错误
5. 临时错误（ErrCircuitOpen / ErrCacheMiss）
```

示例：

```go
if errors.Is(err, ErrSessionClosed) {
    return err // 优先返回 Fatal 错误
}
if IsSecurityRejection(err) {
    return err // 其次返回安全错误
}
// 其他错误...
```

### 5.4 错误日志级别

| 错误类型 | 日志级别 | 说明 |
|---------|---------|------|
| Fatal 错误 | Error | 连接不可继续，记录完整堆栈 |
| 安全错误 | Warn | 记录 IP、原因、触发时间 |
| 插件控制错误 | Debug | 正常控制流，不记录 Error |
| 临时错误 | Warn | 降级处理，记录降级原因 |
| 业务错误 | Error | 记录业务上下文 |

---

## 6. 控制错误与业务错误

### 6.1 控制错误定义

**控制错误**是插件链用于控制消息流的**语义标记**，不是真正的业务失败：

| 错误 | 语义 | 后果 |
|------|------|------|
| `ErrPluginDrop` | 消息被截获，丢弃但不关闭连接 | 停止插件链，不调用 Handler，连接可继续接收后续消息 |
| `ErrPluginBlock` | 拒绝连接或关闭会话 | 停止插件链，关闭连接 |

### 6.2 控制错误与业务错误的区分

**错误判断流程：**

```go
data, err := pluginChain.RunMessage(sess, payload)
if err != nil {
    if errors.Is(err, ErrPluginDrop) {
        // 控制流：正常丢弃消息，不记录业务错误
        metrics.Counter("shark_dropped_messages_total",
            "protocol", sess.Protocol().String(),
            "reason", "plugin_drop").Inc()
        return nil // 不向上传播
    }
    if errors.Is(err, ErrPluginBlock) {
        // 控制流：关闭连接
        sess.Close(context.Background())
        metrics.Counter("shark_rejected_connections_total",
            "protocol", sess.Protocol().String(),
            "reason", "plugin_block").Inc()
        return err
    }
    // 业务错误：记录 Error 日志
    logger.Error("plugin error", "error", err, "session_id", sess.ID())
    return err
}

// 调用 Handler
if err := handler(sess, Message{Payload: data}); err != nil {
    // 业务错误
    logger.Error("handler error", "error", err)
    return err
}
```

### 6.3 控制错误的指标记录

控制错误应单独记录到专用指标，不计入业务错误：

```go
// ✓ 正确
if errors.Is(err, ErrPluginDrop) {
    metrics.Counter("shark_dropped_messages_total",
        "protocol", proto.String(),
        "reason", "plugin_drop").Inc()
    return nil
}

// ✗ 错误（计入业务错误，导致告警误报）
if errors.Is(err, ErrPluginDrop) {
    metrics.Counter("shark_errors_total",
        "protocol", proto.String(),
        "kind", "plugin").Inc()
    return err
}
```
