# OBSERVABILITY.md

> Shark-Socket 可观测性体系  
> 版本:v0.2.x-alpha  
> 最后更新:2026-06-01

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

Shark-Socket 的可观测性由三个正交接口组成,全部定义在 `internal/core/observability.go`:

| 接口 | 职责 | 默认实现 |
|------|------|----------|
| `Logger` | 结构化日志 | `slogLogger`(基于 `log/slog`) |
| `Metrics` | 计数器,仪表,直方图 | `nopMetrics`(空操作) |
| `Tracer` | 分布式链路追踪 | `nopTracer`(空操作) |

**设计原则:**

- 接口极简:只有业务层真正需要的方法,不暴露供应商类型
- 零值可用:不注入时自动降级为 nop 实现,不 panic
- 不阻塞业务:Metrics 和 Tracer 采集均不阻塞热路径

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

#### slogLogger(生产用)

`core.NewSlogLogger(logger *slog.Logger)` 将标准库 `slog.Logger` 适配为 `core.Logger`.传入 nil 时使用 `slog.Default()`.

```go
logger := core.NewSlogLogger(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
```

#### nopLogger

`core.NopLogger()` 返回空操作 Logger,所有方法为 no-op.在未注入 Logger 时自动使用.

#### MemoryLogger(测试用)

`observability.NewMemoryLogger()` 捕获所有日志条目到内存,支持 `Entries()` 读取.用于单元测试验证日志输出.

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

Labels 以 key-value 交替传入:`IncCounter("name", "protocol", "tcp", "status", "ok")`

### 3.2 实现

#### PrometheusMetrics(生产用)

`observability.NewPrometheusMetrics()` 实现 `core.Metrics` + `http.Handler`,同时作为指标收集器和 Prometheus 端点.

特点:
- `sync.RWMutex` 并发安全
- 输出标准 Prometheus text exposition 格式(`# TYPE` 声明,label 转义,
  histogram 的 `_bucket`/`_count`/`_sum` 三元组)
- 直接用作 HTTP Handler:`http.Handle("/metrics", promMetrics)`

#### MemoryMetrics(测试用)

`observability.NewMemoryMetrics()` 提供 `Counter(name, labels...)`,`Gauge(name, labels...)`,`Histogram(name, labels...)` 方法读取当前值.

#### nopMetrics

`core.NopMetrics()` 返回空操作 Metrics,所有方法为 no-op.

### 3.3 内置指标

框架在关键路径内置以下指标(由各组件调用):

| 指标名 | 类型 | 标签 | 触发位置 |
|--------|------|------|---------|
| `sessions_accepted_total` | Counter | - | `SessionManager.Register` 成功 |
| `sessions_closed_total` | Counter | - | `SessionManager.Unregister` |
| `sessions_active` | Gauge | - | 会话表当前条目数 |
| `handler_duration_seconds` | Histogram | `protocol` | `shared.CallHandlerObserved`,8 路传输共用 |
| `messages_total` | Counter | `protocol` | 同上,投递给用户 handler 的消息数 |
| `sessions_reclaimed_total` | Counter | - | 读截止时间到期(对端消失),5 路传输共用 |
| `sessions_rejected_total` | Counter | - | 准入拒绝(TCP/WS/QUIC/HTTP/gRPC-Web/UDP/CoAP 共用) |
| `tcp_task_queue_full_total` | Counter | - | TCP WorkerPool 队列满 |
| `accept_errors_total` | Counter | - | TCP accept 返回错误(不含 `net.ErrClosed`) |
| `accept_resource_errors_total` | Counter | - | 其中 `EMFILE`/`ENFILE`/`ENOMEM` 子集 |
| `tls_handshake_failed_total` | Counter | - | TCP TLS 握手失败或超时 |
| `proxy_header_rejected_total` | Counter | - | PROXY protocol 头缺失或非法 |

`handler_duration_seconds` 与 `messages_total` 记在 **handler 边界**
(`shared.CallHandlerObserved`)而不是各传输的读循环里: 边界是所有协议唯一的汇合点,
数值含 handler 自己的 `Send`、不含队列等待, 因此跨协议可比; 队列问题由
`tcp_task_queue_full_total` 与池的伸缩曲线负责, 两者混在一起会让两个故障源
给出一个数(ADR-020). 计时包住无 panic 隔离之后的调用, 所以 panic 的 handler
也会留下一条长尾样本和一次计数 -- 计数度量的是流量, 不是成功率.

`accept_resource_errors_total` 是 `accept_errors_total` 的子集而不是替代:
前者是容量事故信号,后者含瞬时抖动. 混在一个计数器里, "fd 用尽"会被噪声淹没.

`sessions_rejected_total` 不按协议加前缀, 尽管报告 (`COMPARISON-261018-OSS-PEERS.md`)
曾建议 `udp_sessions_rejected_total`. 传输层已共用 `sessions_*` 前缀, 加协议前缀会让
"跨协议汇总拒绝数"退化成一次正则匹配; 需要按协议区分时应使用 label(见 5.3), 而不是
把协议编进指标名.

> 尚无 `protocol` label 的是 `sessions_*` 系列与各传输自己的计数器. 按协议的
> `sessions_active` 需要在**抓取时**向运行时取值(导出器当前没有 collector 回调),
> 而在热路径上维护第二份计数会与 `Count()` 漂移 -- 这是 ADR-017 明确否掉的形态.
> 补齐计划见 ADR-020 的"重新评估条件".

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

#### OpenTelemetryTracer(生产用)

`observability.NewOpenTelemetryTracer(tracer trace.Tracer)` 将 OpenTelemetry SDK 的 `trace.Tracer` 适配为 `core.Tracer`.

特性:
- 自动将 `key-value` 交替的 attrs 转换为 `attribute.KeyValue`
- 支持 `string`,`bool`,`int`,`int64`,`float64` 类型
- `RecordError` 同时调用 `span.RecordError(err)` 和 `span.SetStatus(codes.Error, ...)`
- nil 安全:传入 nil tracer 时自动降级为 `NopTracer`

```go
import "go.opentelemetry.io/otel"

tracer := otel.Tracer("shark-socket")
t := observability.NewOpenTelemetryTracer(tracer)
ctx, span := t.Start(ctx, "tcp.handleConn", "remote_addr", conn.RemoteAddr().String())
defer span.End()
```

#### nopTracer / nopSpan

`core.NopTracer()` 返回空操作 Tracer,`Start` 直接返回原始 ctx 和空 Span.

---

## 5. Prometheus 指标导出

### 5.1 输出格式

`PrometheusMetrics.ExportText()` 生成标准 Prometheus 文本格式:

```
# TYPE sessions_accepted_total counter
sessions_accepted_total 1234
# TYPE sessions_active gauge
sessions_active 42
# TYPE handler_duration_seconds histogram
handler_duration_seconds_bucket{protocol="tcp",le="0.0001"} 120
handler_duration_seconds_bucket{protocol="tcp",le="0.00025"} 480
...
handler_duration_seconds_bucket{protocol="tcp",le="+Inf"} 500
handler_duration_seconds_count{protocol="tcp"} 500
handler_duration_seconds_sum{protocol="tcp"} 0.0123
```

直方图是**真的 histogram 而不是 summary**: summary 不支持跨实例聚合, 且在没写
quantile 时连单实例 P99 都算不出. 每档 `_bucket` 是累计值, `+Inf` 桶恒等于
`_count` -- 超出最后一档的样本只记在 `count`/`sum` 里, 用梯度累加值写 `+Inf`
会让直方图不自洽, `histogram_quantile()` 返回 NaN.

默认梯度是秒,**1µs 到 10s 共 22 档**(`DefaultHistogramBuckets`). 不是 Prometheus
的 `DefBuckets`(从 5ms 起): 本项目的 echo 路径在微秒级, 5ms 起步会让所有样本落进
第一档. 其它单位的指标(字节数, 计数)必须显式声明:

```go
metrics.RegisterHistogram("payload_bytes", []float64{1024, 4096, 65536})
```

注册必须在首次观测之前: 换梯度要把已记录样本重新分桶, 而导出器为了
O(buckets) 的内存不保留原始样本.

于是 P99 在线上可自证:

```promql
histogram_quantile(0.99, sum by (le) (rate(handler_duration_seconds_bucket[5m])))
histogram_quantile(0.99, sum by (le, protocol) (rate(handler_duration_seconds_bucket[5m])))
```

框架自身目前写入 12 个指标, 完整清单见 `docs/architecture/OBSERVABILITY.md` 3.3 节.
序列键是结构体 `(name, label0, value0, rest)` 而不是拼接字符串, 因此每消息两次
观测是**零分配**的; 实测代价与门禁见 ADR-020.

### 5.2 HTTP 端点

`PrometheusMetrics` 直接实现 `http.Handler`,`ServeHTTP` 设置 `Content-Type: text/plain; version=0.0.4` 并输出 `ExportText()`.

```go
metrics := observability.NewPrometheusMetrics()
http.Handle("/metrics", metrics)
http.ListenAndServe(":18080", nil)
```

### 5.3 Label 规范

- Label 名称自动净化:非 `[a-zA-Z0-9_]` 字符替换为 `_`,首字符不允许数字
- Label 值自动转义:`\` -> `\\`,`\n` -> `\n`,`"` -> `\"`
- 奇数个 labels 时自动命名为 `label_0`,`label_1`...
- **协议用 `protocol` label, 不用指标名前缀.** 取值是封闭枚举
  (`tcp`/`udp`/`http`/`websocket`/`coap`/`quic`/`grpc-web`/`custom`),
  基数上界确定; 编进名字会让"跨协议汇总"退化成一次正则
- 序列身份是 `(name, 第一对 label 名, 第一对 label 值)` 的结构体键, 第一对之后
  的 label 走拼接形式. 因此**改变 label 名称也是一条新序列** -- 不会与旧数据混在
  一起, 也不会串号

---

## 6. 健康端点

App 启动时注册两个 HTTP 端点(默认 `:18081`):

### GET /healthz

存活探针.始终返回 `200 OK`.

```
ok
```

### GET /readyz

就绪探针.Gateway 启动完成后返回 `200 OK`,否则返回 `503 Service Unavailable`.

```
ready
```

### Health JSON(程序化)

`Gateway.Health()` 返回 JSON:

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

Gateway 可观测性注入通过 Functional Options 完成:

| Option | 作用 |
|--------|------|
| `WithLogger(logger)` | 注入 Logger |
| `WithMetrics(metrics)` | 注入 Metrics |
| `WithTracer(tracer)` | 注入 Tracer |

---

## 8. 指标命名规范

**没有全局前缀。** 指标名由写入点直接给出,不强制统一前缀(实现中不存在 `shark_` 前缀)。

| 规则 | 示例 |
|------|------|
| 用 snake_case | `sessions_active` |
| Counter 以 `_total` 结尾 | `sessions_accepted_total` |
| Gauge 描述当前状态 | `sessions_active` |
| Histogram 使用物理单位后缀 | `handler_duration_seconds` |
| Label 值小写,snake_case | `protocol="tcp"`, `status="ok"` |
| 标签维度保持有界 | 避免把 session_id 这类高基数值做成标签 |

标签不是必填:实现里除 `session_metrics.go` 与 `session_metrics.go` 的会话指标外,多数写入点
只传指标名。新增指标时请先确认它不在上表 5 个已记录指标之内,避免重复计数。

---

**文档职责边界:** 本文档描述可观测性接口,实现,导出和配置.插件内部的日志行为详见 PLUGIN.md,错误分类与日志级别详见 ERRORS.md.
