# OBSERVABILITY.md

> Shark-Socket 可观测性体系  
> 版本：v0.2.x-alpha  
> 最后更新：2026-06-01

---

## 目录

1. [概述](#1-概述)
2. [Logger 接口](#2-logger-接口)
3. [Metrics 接口](#3-metrics-接口)
4. [Tracer 接口](#4-tracer-接口)
5. [Prometheus 指标导出](#5-prometheus-指标导出)
6. [健康端点](#6-健康端点)
7. [配置方式](#7-配置方式)
8. [指标命名规范](#8-指标命名规范)

---

## 1. 概述

Shark-Socket 的可观测性由三个正交接口组成，全部定义在 `internal/core/observability.go`：

| 接口 | 职责 | 默认实现 |
|------|------|----------|
| `Logger` | 结构化日志 | `slogLogger`（基于 `log/slog`） |
| `Metrics` | 计数器、仪表、直方图 | `nopMetrics`（空操作） |
| `Tracer` | 分布式链路追踪 | `nopTracer`（空操作） |

**设计原则：**

- 接口极简：只有业务层真正需要的方法，不暴露供应商类型
- 零值可用：不注入时自动降级为 nop 实现，不 panic
- 不阻塞业务：Metrics 和 Tracer 采集均不阻塞热路径

---

## 2. Logger 接口

### 2.1 接口定义

```go
type Logger interface {
    Debug(msg string, attrs ...any)
    Info(msg string, attrs ...any)
    Warn(msg string, attrs ...any)
    Error(msg string, attrs ...any)
}
```

### 2.2 实现

#### slogLogger（生产用）

`core.NewSlogLogger(logger *slog.Logger)` 将标准库 `slog.Logger` 适配为 `core.Logger`。传入 nil 时使用 `slog.Default()`。

```go
logger := core.NewSlogLogger(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
```

#### nopLogger

`core.NopLogger()` 返回空操作 Logger，所有方法为 no-op。在未注入 Logger 时自动使用。

#### MemoryLogger（测试用）

`observability.NewMemoryLogger()` 捕获所有日志条目到内存，支持 `Entries()` 读取。用于单元测试验证日志输出。

```go
logger := observability.NewMemoryLogger()
// ... 执行业务逻辑 ...
for _, entry := range logger.Entries() {
    // entry.Level, entry.Msg, entry.Attrs
}
```

### 2.3 使用位置

| 组件 | 日志级别 | 场景 |
|------|---------|------|
| Gateway | Error | 协议 Start 失败 |
| Gateway | Warn | Drain 超时 |
| TCP Server | Warn | Accept 失败 |
| PluginChain | Error | 插件 panic |
| HeartbeatPlugin | Warn | 会话超时关闭 |
| SlowHandler | Warn | 处理耗时超过阈值 |

---

## 3. Metrics 接口

### 3.1 接口定义

```go
type Metrics interface {
    IncCounter(name string, labels ...string)
    SetGauge(name string, value float64, labels ...string)
    ObserveHistogram(name string, value float64, labels ...string)
}
```

Labels 以 key-value 交替传入：`IncCounter("name", "protocol", "tcp", "status", "ok")`

### 3.2 实现

#### PrometheusMetrics（生产用）

`observability.NewPrometheusMetrics()` 实现 `core.Metrics` + `http.Handler`，同时作为指标收集器和 Prometheus 端点。

特点：
- `sync.RWMutex` 并发安全
- 输出标准 Prometheus text exposition 格式（`# TYPE` 声明、label 转义、histogram `_count`/`_sum`）
- 直接用作 HTTP Handler：`http.Handle("/metrics", promMetrics)`

#### MemoryMetrics（测试用）

`observability.NewMemoryMetrics()` 提供 `Counter(name, labels...)`、`Gauge(name, labels...)`、`Histogram(name, labels...)` 方法读取当前值。

#### nopMetrics

`core.NopMetrics()` 返回空操作 Metrics，所有方法为 no-op。

### 3.3 内置指标

框架在关键路径内置以下指标（由各组件调用）：

| 指标名 | 类型 | 标签 | 触发位置 |
|--------|------|------|---------|
| `tcp_task_queue_full_total` | Counter | — | TCP WorkerPool 队列满 |

> 当前 v0.2.x-alpha 内置指标较少，P1 阶段将补充连接数、消息吞吐、延迟等全量指标。

---

## 4. Tracer 接口

### 4.1 接口定义

```go
type Tracer interface {
    Start(ctx context.Context, name string, attrs ...any) (context.Context, Span)
}

type Span interface {
    End()
    RecordError(error)
}
```

### 4.2 实现

#### OpenTelemetryTracer（生产用）

`observability.NewOpenTelemetryTracer(tracer trace.Tracer)` 将 OpenTelemetry SDK 的 `trace.Tracer` 适配为 `core.Tracer`。

特性：
- 自动将 `key-value` 交替的 attrs 转换为 `attribute.KeyValue`
- 支持 `string`、`bool`、`int`、`int64`、`float64` 类型
- `RecordError` 同时调用 `span.RecordError(err)` 和 `span.SetStatus(codes.Error, ...)`
- nil 安全：传入 nil tracer 时自动降级为 `NopTracer`

```go
import "go.opentelemetry.io/otel"

tracer := otel.Tracer("shark-socket")
t := observability.NewOpenTelemetryTracer(tracer)
ctx, span := t.Start(ctx, "tcp.handleConn", "remote_addr", conn.RemoteAddr().String())
defer span.End()
```

#### nopTracer / nopSpan

`core.NopTracer()` 返回空操作 Tracer，`Start` 直接返回原始 ctx 和空 Span。

---

## 5. Prometheus 指标导出

### 5.1 输出格式

`PrometheusMetrics.ExportText()` 生成标准 Prometheus 文本格式：

```
# TYPE shark_messages_total counter
shark_messages_total{protocol="tcp",direction="in"} 1234
# TYPE shark_sessions_active gauge
shark_sessions_active{protocol="tcp"} 42
# TYPE shark_request_duration_seconds summary
shark_request_duration_seconds_count{protocol="tcp"} 500
shark_request_duration_seconds_sum{protocol="tcp"} 12.345
```

### 5.2 HTTP 端点

`PrometheusMetrics` 直接实现 `http.Handler`，`ServeHTTP` 设置 `Content-Type: text/plain; version=0.0.4` 并输出 `ExportText()`。

```go
metrics := observability.NewPrometheusMetrics()
http.Handle("/metrics", metrics)
http.ListenAndServe(":18080", nil)
```

### 5.3 Label 规范

- Label 名称自动净化：非 `[a-zA-Z0-9_]` 字符替换为 `_`，首字符不允许数字
- Label 值自动转义：`\` → `\\`，`\n` → `\n`，`"` → `\"`
- 奇数个 labels 时自动命名为 `label_0`、`label_1`...

---

## 6. 健康端点

App 启动时注册两个 HTTP 端点（默认 `:18081`）：

### GET /healthz

存活探针。始终返回 `200 OK`。

```
ok
```

### GET /readyz

就绪探针。Gateway 启动完成后返回 `200 OK`，否则返回 `503 Service Unavailable`。

```
ready
```

### Health JSON（程序化）

`Gateway.Health()` 返回 JSON：

```json
{
  "started": true,
  "sessions": 42,
  "started_at": "2026-05-30T12:00:00Z",
  "uptime": "2h30m",
  "protocols": ["tcp", "websocket", "coap"]
}
```

---

## 7. 配置方式

### 7.1 JSON 配置

```json
{
  "health_addr": "0.0.0.0:18081",
  "metrics_addr": "0.0.0.0:18080"
}
```

### 7.2 环境变量

| 变量 | 作用 |
|------|------|
| `SHARK_HEALTH_ADDR` | 健康端点监听地址 |
| `SHARK_METRICS_ADDR` | Prometheus 指标端点监听地址 |

### 7.3 编程接入

```go
metrics := api.NewPrometheusMetrics()
gateway := api.NewGateway(
    api.WithMetrics(metrics),
    api.WithLogger(core.NewSlogLogger(slog.Default())),
    api.WithTracer(observability.NewOpenTelemetryTracer(tracer)),
)
```

Gateway 可观测性注入通过 Functional Options 完成：

| Option | 作用 |
|--------|------|
| `WithLogger(logger)` | 注入 Logger |
| `WithMetrics(metrics)` | 注入 Metrics |
| `WithTracer(tracer)` | 注入 Tracer |

---

## 8. 指标命名规范

| 规则 | 示例 |
|------|------|
| 前缀 `shark_` | `shark_sessions_active` |
| 使用 snake_case | `shark_tcp_connections_total` |
| Counter 以 `_total` 结尾 | `shark_messages_total` |
| Gauge 描述当前状态 | `shark_sessions_active` |
| Histogram 使用物理单位 | `shark_request_duration_seconds` |
| Label 值小写、snake_case | `protocol="tcp"`, `status="ok"` |

---

**文档职责边界：** 本文档描述可观测性接口、实现、导出和配置。插件内部的日志行为详见 PLUGIN.md，错误分类与日志级别详见 ERRORS.md。
