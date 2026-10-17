# ERRORS.md

> Shark-Socket 错误体系完整定义  
> 版本:v0.2.x-alpha  
> 最后更新:2026-06-01

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

`internal/core/errors.go` 定义了框架的**完整错误体系**,包括:

- **错误变量**:所有预定义错误,使用 `errors.New` 创建,支持 `errors.Is` 判断
- **控制信号**:`ErrPluginDrop` / `ErrPluginBlock`(定义在 `internal/core/plugin.go`)
- **命名规范**:`Err` 前缀 + 驼峰(如 `ErrSessionClosed`),不使用缩写(如 `ErrSessClosed`)
- **不做分类包装**:框架不再提供 IsRetryable / IsFatal 之类的聚合判断,调用点直接
  `errors.Is` / `errors.As`,理由见第 4 节

**设计原则:**

| 原则 | 说明 |
|------|------|
| 可识别性 | 所有框架错误用 `errors.Is` 判断,业务错误用 `errors.As` 提取 |
| 语义清晰 | 错误名称直接表达失败原因,不需要查看错误信息 |
| 分层归属 | 错误变量定义在 `core/errors.go`,具体错误包装在各层实现中 |
| 控制流分离 | 插件控制错误(ErrPluginDrop/ErrPluginBlock)不是真正的业务错误 |

---

## 2. 错误分类

框架错误只集中在两个文件,按"是不是失败"分成两类:

| 文件 | 内容 | 用途 |
|------|------|------|
| `internal/core/errors.go` | 13 个失败信号 | 用 `errors.Is` 判断,表示传输或装配失败 |
| `internal/core/plugin.go` | 2 个插件控制信号 | 不是失败,是插件链的控制流指令 |

按语义分组:

| 类别 | 变量 | 说明 |
|------|------|------|
| 会话 | `ErrSessionClosed` `ErrSessionCapacity` `ErrDuplicateSession` | 已关闭 / 到容量 / 重复注册 |
| 网关编排 | `ErrNoServers` `ErrDuplicateProtocol` | 无 Server 可启动 / 协议重复注册 |
| 服务 | `ErrServerClosed` | Server 已关闭 |
| 参数 | `ErrInvalidArgument` | nil 或非法入参 |
| 通用关闭 | `ErrClosed` | 资源已关闭(池,通道) |
| 背压 | `ErrWriteQueueFull` | 写队列或任务队列满 |
| 帧 | `ErrFrameTooLarge` | 帧长度超过限制 |
| 能力 | `ErrUnsupportedFeature` | 当前构建不支持该能力 |
| panic 隔离 | `ErrPluginPanic` `ErrHandlerPanic` | panic 被 recover 后转成的错误 |
| 插件控制 | `ErrPluginDrop` `ErrPluginBlock` | 控制流语义,见第 6 节 |

早期草案里设计过 `ErrSessionNotFound`、`ErrListenFailed`、`ErrReadTimeout`、
`ErrDecodeFailure`、`ErrRateLimited`、`ErrCircuitOpen` 等约 45 个变量,它们**没有实现**.
实际代码在这些场景中直接返回底层错误(`net.OpError`、`os.SyscallError` 等)或用
`fmt.Errorf("%w: ...")` 包装,不做二次归一化.归一化会丢掉底层诊断信息,而调用方真正需要
判断的是"是不是超时/是不是对端断开",这由错误自身是否实现 `net.Error` 决定.

---

## 3. 完整错误变量列表

### 3.1 会话错误

```go
// internal/core/errors.go
var (
    ErrSessionClosed    = errors.New("session closed")
    ErrSessionCapacity  = errors.New("session capacity reached")
    ErrDuplicateSession = errors.New("duplicate session")
)
```

| 错误 | 返回时机 | 调用方处理 |
|------|---------|-----------|
| `ErrSessionClosed` | `Session.Send()` 时会话处于 Draining / Closed;WorkerPool 阻塞提交期间会话被关闭 | 停止发送,不再重试 |
| `ErrSessionCapacity` | `SessionManager.Register()` 达到上限(默认 100 万) | 传输层关闭该连接,不影响已有会话 |
| `ErrDuplicateSession` | `Register()` 遇到已存在的 ID | 调用方 ID 生成有误,关闭连接并记 Error |

`Get(id)` 未找到返回 `(nil, false)` 而不返回错误:`SessionManager` 的契约里"没有这个会话"
是正常答案,不是失败.

### 3.2 网关与服务错误

```go
var (
    ErrNoServers          = errors.New("no servers registered")
    ErrDuplicateProtocol  = errors.New("duplicate protocol")
    ErrServerClosed       = errors.New("server closed")
    ErrInvalidArgument    = errors.New("invalid argument")
    ErrClosed             = errors.New("closed")
    ErrUnsupportedFeature = errors.New("unsupported feature")
)
```

| 错误 | 返回时机 | 调用方处理 |
|------|---------|-----------|
| `ErrNoServers` | `Gateway.Start()` 时未注册任何 Server;`Gateway.Register(nil)` | 装配错误,直接失败 |
| `ErrDuplicateProtocol` | `Gateway.Register()` 同一协议注册两次 | 装配错误,直接失败 |
| `ErrServerClosed` | 已关闭的 Server 再次被使用 | 停止使用该 Server |
| `ErrInvalidArgument` | `SessionManager.Register(nil)` 等 | 调用方 bug,记 Error |
| `ErrClosed` | 已 Stop 的 WorkerPool 收到 `Submit` | 停止提交 |
| `ErrUnsupportedFeature` | 配置请求了当前构建不支持的能力 | 提示配置错误 |

### 3.3 背压与帧错误

```go
var (
    ErrWriteQueueFull = errors.New("write queue full")
    ErrFrameTooLarge  = errors.New("frame too large")
)
```

| 错误 | 返回时机 | 调用方处理 |
|------|---------|-----------|
| `ErrWriteQueueFull` | 会话写队列满;或 WorkerPool 在 `PolicyDrop` / `PolicyClose` 下拒绝任务 | 由 QoS 决定丢弃或关闭会话;`PolicyBlock` 下不会出现该错误 |
| `ErrFrameTooLarge` | 帧长度超过 Framer 限制 | 关闭连接,计入连续错误 |

### 3.4 panic 隔离错误

```go
var (
    ErrPluginPanic  = errors.New("plugin panicked")
    ErrHandlerPanic = errors.New("user handler panicked")
)
```

| 错误 | 返回时机 | 调用方处理 |
|------|---------|-----------|
| `ErrPluginPanic` | 插件任一钩子 panic,被 `PluginChain` 的 `safe*` 包装 recover | 会话级失败处理,进程继续 |
| `ErrHandlerPanic` | 用户 handler panic(`shared.CallHandler`);或 `SessionManager.Broadcast` / `CloseAll` 中会话实现 panic(`safeSessionCall`) | 关闭该会话,进程继续 |

这两个错误存在的意义是把"进程崩溃"降级为"一条会话失败",因此它们**必须**被记录一次日志.
`shared.CallHandler` 会记录 `user handler panic`;`safeSessionCall` 只返回错误,由调用方决定
是否记录.

### 3.5 插件控制错误

```go
// internal/core/plugin.go
var (
    ErrPluginDrop  = errors.New("plugin drop")
    ErrPluginBlock = errors.New("plugin block")
)
```

| 错误 | 返回时机 | 调用方处理 |
|------|---------|-----------|
| `ErrPluginDrop` | `Plugin.OnMessage()` | 停止链,不调用 Handler,会话继续 |
| `ErrPluginBlock` | `Plugin.OnAccept()` 或 `OnMessage()` | 停止链,传输层关闭会话 |

这两个不是失败,是控制流指令,详见第 6 节.

---

## 4. 错误判断方式

框架**没有**提供 `IsRetryable` / `IsFatal` / `IsSecurityRejection` / `IsTransient`
这类分类函数.分类函数把判断逻辑与调用场景解耦,结果是每个调用点都要先理解函数内部的错误
集合,反而比直接判断更难维护.实际的判断方式只有三种:

**1. `errors.Is` 判断哨兵错误:**

```go
if errors.Is(err, core.ErrPluginDrop) {
    return // 丢弃消息,连接继续
}
if errors.Is(err, core.ErrWriteQueueFull) {
    s.rt.Metrics().IncCounter("tcp_task_queue_full_total")
}
```

**2. `shared.IsTimeout` 判断网络超时:**

```go
// internal/transport/shared/timeout.go
func IsTimeout(err error) bool {
    var netErr net.Error
    return errors.As(err, &netErr) && netErr.Timeout()
}
```

这是框架里唯一存在的分类辅助函数,被 TCP / UDP / WebSocket / CoAP / gRPC-Web 五个服务端
用来区分"空闲超时"与"真实故障":前者只计指标,后者记 Error 日志.

**3. `errors.As` 提取底层错误:**

```go
var netErr net.Error
if errors.As(err, &netErr) { ... }
```

**为什么没有 `IsRetryable`:** 能否重试取决于调用场景而非错误类型.同一个
`ErrWriteQueueFull`,在可丢消息的场景下应当丢弃,在不可丢消息的场景下应当阻塞等待;把它归入
统一的 retryable 集合必然在其中一种场景下给出错误答案.

---

## 5. 错误使用规范

### 5.1 错误包装

用 `fmt.Errorf("%w: ...")` 保留可判定的原因,同时附加定位上下文:

```go
// [OK] 正确:保留 %w,errors.Is 仍然可用,同时补上出错的协议与地址
return fmt.Errorf("listen %s %s: %w", proto, addr, err)

// [OK] 正确:附加协议名,保留可判定的原因
return fmt.Errorf("plugin %q start: %w", p.Name(), err)

// [X] 错误:把底层错误整个丢掉,运维拿不到真正的原因(端口占用?权限?)
return core.ErrServerClosed

// [X] 错误:用 %v 包装会切断错误链,errors.Is 失效
return fmt.Errorf("listen failed: %v", err)
```

只在确实需要一个**新语义**时才用 `%w` 包哨兵错误(例如 `StartAll` 把插件启动失败包成
`plugin %q start: %w`);纯透传的场景直接 `return err`.

### 5.2 错误判断

使用 `errors.Is` 判断预定义错误:

```go
// [OK] 正确
if errors.Is(err, core.ErrSessionClosed) {
    // 处理逻辑
}

// [X] 错误(字符串比较不可靠,错误信息可能被包装)
if err != nil && err.Error() == "session closed" {
    // 处理逻辑
}
```

### 5.3 错误返回优先级

一个函数同时可能返回多个错误时,按"最先发生的真实原因"返回,不做优先级重排.实践中形成的
习惯顺序是:

```
1. panic 隔离错误(ErrPluginPanic / ErrHandlerPanic):最需要被看见
2. 会话 / 服务终止错误(ErrSessionClosed / ErrServerClosed)
3. 插件控制错误(ErrPluginDrop / ErrPluginBlock):控制流,调用方必须能区分
4. 背压错误(ErrWriteQueueFull)
5. 业务错误
```

关停路径是唯一的例外:`Gateway.Stop` 保留**首个**错误作为返回值,同时继续走完所有阶段,
因为关停必须完成;其余阶段的失败只记日志.

```go
if err := runStage(ctx, g.timeouts.Drain, staged.Drain); err != nil && firstErr == nil {
    firstErr = err     // 记住第一个失败,但不中断后续阶段
}
```

### 5.4 错误日志级别

| 错误类型 | 日志级别 | 说明 |
|---------|---------|------|
| `ErrPluginPanic` / `ErrHandlerPanic` | Error | 原本会导致进程崩溃,必须留下诊断 |
| 业务错误 | Error | 记录业务上下文 |
| 背压错误(`ErrWriteQueueFull`) | Warn + 专用指标 | 单独计数,避免污染错误率 |
| 会话 / 服务终止错误 | Debug 或 Warn | 对端断开是常态,不是故障 |
| 插件控制错误 | Debug | 正常控制流,不计入错误指标 |

---

## 6. 控制错误与业务错误

### 6.1 控制错误定义

**控制错误**是插件链用于控制消息流的**语义标记**,不是真正的业务失败:

| 错误 | 语义 | 后果 |
|------|------|------|
| `ErrPluginDrop` | 消息被截获,丢弃但不关闭连接 | 停止插件链,不调用 Handler,连接可继续接收后续消息 |
| `ErrPluginBlock` | 拒绝连接或关闭会话 | 停止插件链,关闭连接 |

### 6.2 控制错误与业务错误的区分

**错误判断流程:**

```go
data, err := pluginChain.OnMessage(sess, payload)
if err != nil {
    if errors.Is(err, ErrPluginDrop) {
        // 控制流:正常丢弃消息,不记录业务错误
        metrics.IncCounter("dropped_messages_total", "protocol", sess.Protocol().String(),
            "reason", "plugin_drop")
        return nil // 不向上传播
    }
    if errors.Is(err, ErrPluginBlock) {
        // 控制流:关闭连接
        sess.Close(context.Background())
        metrics.IncCounter("rejected_connections_total", "protocol", sess.Protocol().String(),
            "reason", "plugin_block")
        return err
    }
    // 业务错误:记录 Error 日志
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

控制错误应单独记录到专用指标,不计入业务错误:

```go
// [OK] 正确
if errors.Is(err, ErrPluginDrop) {
    metrics.IncCounter("dropped_messages_total", "protocol", proto.String(),
        "reason", "plugin_drop")
    return nil
}

// [X] 错误(计入业务错误,导致告警误报)
if errors.Is(err, ErrPluginDrop) {
    metrics.IncCounter("errors_total", "protocol", proto.String(),
        "kind", "plugin")
    return err
}
```

---

## 附:指标录制 API 约定

框架的 `core.Metrics` 只有三个方法,没有 `Counter()/.Inc()` 之类的链式工厂:

```go
IncCounter(name string, labels ...string)
SetGauge(name string, value float64, labels ...string)
ObserveHistogram(name string, value float64, labels ...string)
```

`labels` 以 key/value 交替传入.本文档中的指标名是**示意名**,用于说明接入方式的形状;
框架自身实际录制的指标只有 5 个(`sessions_accepted_total` / `sessions_closed_total` /
`sessions_active` / `sessions_reclaimed_total` / `tcp_task_queue_full_total`),
清单见 `docs/architecture/OBSERVABILITY.md` 3.3 节.实现参考
`internal/infra/observability/prometheus.go`.
