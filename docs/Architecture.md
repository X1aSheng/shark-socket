# Shark-Socket-New 架构设计文档

> `shark-socket-new` 是对 `shark-socket` 的重新设计版本。目标不是简单搬运旧项目，而是在吸收旧项目多协议、插件化、可观测、部署化经验的基础上，先建立一个边界清晰、生命周期正确、可测试可演进的服务端网络框架。

更新时间：2026-05-30T09:10:00

最新审查参考：`docs/PROJECT-REVIEW-260530-094810.md`

---

## 1. 项目定位

`shark-socket-new` 面向高并发、多协议、插件化服务端场景，采用 Go 1.26+ 开发，提供统一 Gateway 运行时，用于编排 TCP、UDP、HTTP、WebSocket、CoAP、LwM2M、QUIC、gRPC-Web 等协议。

与旧 `shark-socket` 的关系：

| 维度 | `shark-socket` | `shark-socket-new` |
| --- | --- | --- |
| 定位 | 成熟功能集合，目标覆盖面广 | 重新设计，先保证核心架构正确 |
| 架构风格 | 分层较重，目标性能和生产特性全面 | 小核心、显式所有权、逐步增强 |
| Session | 设计中偏泛型化和高性能池化 | 核心统一 `[]byte`，类型层通过 Codec 适配 |
| Gateway | 多协议统一入口 | Gateway 明确拥有 Runtime、SessionManager、PluginRunner |
| 插件 | 丰富生产插件 | 保留插件链核心语义，逐步补齐生产插件 |
| 测试 | 单元、集成、缺陷、benchmark 较完整 | 从核心回归测试开始，保持脚本化验证 |
| 部署 | Docker/K8s/Helm 生产基线 | 保留部署基线，持续补云端实测记录 |

本项目的第一原则：先让运行时所有权、协议生命周期、插件执行和关闭流程可解释、可测试、可恢复，再逐步追求极限性能和功能宽度。

---

## 2. 设计目标

### 2.1 核心目标

1. 公共 API 小而稳定。
2. Gateway 统一编排多协议运行时。
3. SessionManager、PluginRunner、Logger、Metrics、Tracer 的所有权显式。
4. 各协议只负责自己的网络细节，不私自关闭共享运行时资源。
5. 插件链统一处理 accept、message、close 生命周期。
6. 优雅关闭分阶段执行：停止接收、等待读写、关闭会话、收尾释放。
7. 类型化消息放在协议原始字节层之上，通过 Codec 适配，而不是污染核心 Session。
8. 测试、脚本、部署清单和文档保持同步。

### 2.2 当前阶段非目标与转换条件

当前阶段不追求一次性实现旧项目全部复杂能力：

| 当前非目标 | 转换为目标的阶段 | 转换条件 |
| --- | --- | --- |
| 泛型 Session 作为核心接口 | v1.0 API 稳定前评估 | 现有 `core.Session` 无法承载至少两个真实业务协议的类型安全需求 |
| 复杂 BufferPool、时间轮、分片 LRU、全量防御体系 | P2 性能与防御 | 已有基准压测证明分配、超时扫描或 Session 锁竞争成为瓶颈 |
| Gateway 直接耦合数据库、Redis、消息队列或注册中心 | P2/P3 集群与持久化 | 外部 adapter 接口稳定，并完成至少一种真实后端集成测试 |
| 内建完整 MQTT Broker | P1/P2 MQTT 专项 | 明确选择内建 Broker 而不是外部 Broker 适配，并建立 MQTT 3.1.1/5.0 合规测试 |
| 每个协议一次性实现完整标准 | 按协议专项推进 | 当前 smoke/边界测试稳定，且已有明确互操作目标或生产场景 |

---

## 3. 总体分层

```text
api/
  对外门面层。导出类型别名、工厂函数、Option 透传。

cmd/
  可运行入口。负责加载配置、启动 App、处理系统信号。

internal/app/
  应用装配层。把配置映射为 Gateway、协议服务、健康检查、指标服务。

internal/core/
  稳定契约层。定义 Protocol、Session、Server、Runtime、Plugin、Codec、可观测接口和错误。

internal/runtime/
  运行时层。实现 Gateway、SessionManager、PluginChain、Runtime 依赖容器。

internal/transport/
  传输协议层。TCP、UDP、HTTP、WebSocket、CoAP、QUIC、gRPC-Web。

internal/protocol/
  应用协议层。当前包含 LwM2M 生命周期模型和 CoAP 文本命令绑定。

internal/plugin/
  通用插件层。黑名单、限流、心跳、持久化、自动封禁、慢处理日志、集群广播。

internal/infra/
  基础设施层。cache、store、pubsub、circuitbreaker、observability。

tests/
  跨包部署和集成语义测试。

scripts/
  统一测试、日志解析、部署校验脚本。

deploy/
  Docker、docker-compose、Kubernetes、Helm 部署资产。

docs/
  架构、配置、测试策略、审查报告、示例说明。
```

### 3.1 依赖方向

依赖只能从上层指向下层，或者在同层内部保持局部依赖：

```text
cmd/app/api
    ↓
runtime + transport + plugin + protocol
    ↓
core + infra
```

禁止事项：

- `core` 不依赖任何具体协议。
- `runtime` 不依赖 `cmd` 或 `app`。
- `transport` 不直接创建全局 Gateway。
- `plugin` 不直接控制协议 listener 生命周期。
- `infra` 不反向调用业务层。

---

## 4. 核心契约

### 4.1 Protocol

`core.Protocol` 是协议身份标识，当前内置：

```text
tcp
udp
http
websocket
coap
quic
grpc-web
custom
```

协议标识用于：

- Gateway 注册去重。
- Session 标记来源。
- 插件和指标标签。
- 日志和追踪属性。

### 4.2 Message

核心消息只保存传输层必要信息：

```go
type Message struct {
    SessionID uint64
    Protocol  Protocol
    Payload   []byte
    Meta      map[string]string
}
```

设计原则：

- `Payload` 始终为 `[]byte`。
- 协议解析后的业务结构不进入核心层。
- 业务类型通过 `Codec[M]` 和 `AdaptTyped[M]` 适配。

类型化适配路径：

```text
网络帧 -> []byte -> core.Message -> Codec.Decode -> TypedHandler[M]
TypedHandler 输出 -> Codec.Encode -> Session.Send([]byte)
```

### 4.3 Session

`core.Session` 是运行时统一会话接口：

```go
type Session interface {
    ID() uint64
    Protocol() Protocol
    RemoteAddr() net.Addr
    LocalAddr() net.Addr
    State() SessionState
    CreatedAt() time.Time
    LastActiveAt() time.Time
    Context() context.Context
    Send([]byte) error
    Close(context.Context) error
    SetMeta(string, any)
    GetMeta(string) (any, bool)
    DelMeta(string)
}
```

会话状态：

```text
Connecting -> Active -> Draining -> Closed
```

关键要求：

- `Close` 必须幂等。
- `Send` 必须在关闭后返回 `ErrSessionClosed` 或等价错误。
- `LastActiveAt` 用于心跳、TTL、清理和观测。
- 元数据必须线程安全。
- 协议实现可以有自己的 session 结构，但必须满足统一接口。

### 4.4 SessionManager

`SessionManager` 是 Gateway 共享会话索引。

职责：

- 分配全局递增 Session ID。
- 注册、注销、查询、遍历会话。
- 广播消息。
- 在 Gateway 停止时关闭所有剩余会话。

设计边界：

- Manager 不创建协议连接。
- Manager 不拥有 listener。
- Manager 的 `CloseAll` 只清理当前会话，不永久关闭 Manager。
- Gateway 停止后再次启动时，Manager 仍应可用。

### 4.5 Server

所有协议服务实现：

```go
type Server interface {
    Protocol() Protocol
    Start(context.Context) error
    Stop(context.Context) error
}
```

参与 Gateway 运行时注入的协议实现：

```go
type RuntimeConfigurable interface {
    UseRuntime(Runtime)
}
```

支持分阶段关闭的协议实现：

```go
type StagedServer interface {
    StopAccept(context.Context) error
    Drain(context.Context) error
    CloseSessions(context.Context) error
}
```

### 4.6 Runtime

`Runtime` 是协议运行所需共享依赖：

```go
type Runtime interface {
    Sessions() SessionManager
    Plugins() PluginRunner
    Logger() Logger
    Metrics() Metrics
    Tracer() Tracer
}
```

协议层不得自己创建替代性的全局依赖。若单独启动协议服务，可使用默认空 Runtime；通过 Gateway 启动时必须接收 Gateway 注入的 Runtime。

---

## 5. Gateway 设计

Gateway 是整个框架的运行时编排器。

### 5.1 Gateway 职责

1. 注册多个协议 Server。
2. 拒绝重复协议注册。
3. 在启动前向协议注入 Runtime。
4. 顺序启动协议，启动失败时回滚已启动协议。
5. 暴露 Ready 状态和 Health 快照。
6. 按阶段停止所有协议。
7. 最后关闭共享 SessionManager 中残留的会话。

### 5.2 启动流程

```text
Gateway.Start(ctx)
  -> snapshot servers
  -> ensure at least one server
  -> inject Runtime into RuntimeConfigurable servers
  -> start servers in registration order
  -> if any start fails:
       stop already-started servers in reverse order
       keep ready=false
       return error
  -> set started_at
  -> ready=true
```

### 5.3 停止流程

```text
Gateway.Stop(ctx)
  -> for staged servers: StopAccept
  -> for staged servers: Drain
  -> for staged servers: CloseSessions
  -> for non-staged servers: Stop
  -> Runtime.Sessions().CloseAll
  -> ready=false
```

阶段含义：

| 阶段 | 目的 |
| --- | --- |
| StopAccept | 停止接收新连接、新请求或新数据流 |
| Drain | 等待 accept/read/write goroutine 收敛 |
| CloseSessions | 关闭协议持有的活跃会话 |
| CloseAll | 清理 Manager 中可能遗留的共享会话 |
| Finalize | 释放资源、记录错误、更新状态 |

### 5.4 生命周期不变量

- `Start` 失败必须回滚。
- `Stop` 必须可重复调用。
- `Start -> Stop -> Start` 应保持可用。
- `Ready()` 只表示 Gateway 已成功启动，不代表外部依赖健康。
- 协议停止不能永久关闭共享 SessionManager。

---

## 6. 插件系统

### 6.1 Plugin 接口

```go
type Plugin interface {
    Name() string
    Priority() int
    OnAccept(Session) error
    OnMessage(Session, []byte) ([]byte, error)
    OnClose(Session)
}
```

执行顺序：

- `OnAccept`：按 Priority 从小到大。
- `OnMessage`：按 Priority 从小到大，允许改写 payload。
- `OnClose`：按 Priority 从大到小，逆序释放。

特殊错误：

| 错误 | 语义 |
| --- | --- |
| `ErrPluginDrop` | 丢弃当前消息，不调用业务 Handler，但连接可继续 |
| `ErrPluginBlock` | 拒绝连接或会话 |
| 普通 error | 按协议策略返回错误、关闭会话或终止当前请求 |

### 6.2 PluginChain 要求

- 插件启动时排序，热路径不再排序。
- 插件 panic 必须隔离，不能拖垮协议服务。
- `OnClose` 必须尽量执行，不因单个插件 panic 中断。
- 协议层只调用 `Runtime.Plugins()`，不自行维护另一套插件链。

### 6.3 当前插件

| 插件 | 作用 |
| --- | --- |
| Blacklist | IP/CIDR 黑名单 |
| RateLimit | 按远端地址限流 |
| Heartbeat | 清理空闲会话 |
| Persistence | 保存会话生命周期事件 |
| AutoBan | 按错误计数自动封禁 |
| SlowHandler | 记录慢处理调用 |
| Cluster | 通过 PubSub 广播跨节点消息 |

后续增强：

- 令牌桶限流。
- 动态黑名单 TTL。
- 协议错误计数接入 AutoBan。
- 插件指标和 trace span。
- 插件配置文件化。

---

## 7. 协议层设计

### 7.1 TCP

职责：

- 监听 TCP 地址。
- 为每个连接创建 Session。
- 使用 Framer 解析消息边界。
- 将消息交给插件链和 Handler。
- 通过写队列串行发送响应。
- 支持 WorkerPool 控制 handler 并发。

内置 Framer：

| Framer | 用途 |
| --- | --- |
| LengthPrefixFramer | 4 字节大端长度前缀，默认推荐 |
| LineFramer | 文本行协议 |
| FixedSizeFramer | 固定长度帧 |
| RawFramer | 原始读写，适合简单场景 |

关键约束：

- 读取超大帧返回 `ErrFrameTooLarge`。
- 写队列满时按策略阻塞、丢弃或关闭。
- Handler 错误可关闭 session。
- Stop 时先停 listener，再关闭 session，再等待 goroutine。

### 7.2 UDP

UDP 无真实连接，因此使用伪会话：

```text
remote UDPAddr -> pseudo session -> shared SessionManager
```

职责：

- 单 UDPConn 读取数据报。
- 按远端地址创建或复用伪会话。
- 通过 TTL sweep 清理空闲伪会话。
- 插件链可改写或丢弃数据报。

关键约束：

- `ErrPluginDrop` 只丢弃当前 datagram，不删除伪会话。
- 关闭服务时清理所有伪会话。
- UDP 发送直接 `WriteToUDP`，不建立写队列。

### 7.3 HTTP

HTTP 保留两种模式：

| 模式 | 描述 |
| --- | --- |
| Mode A | 轻量 net/http router，不创建 Session，不走插件 |
| Mode B | 每个请求创建临时 Session，执行插件和 Handler |

Mode B 流程：

```text
Read body with MaxBytesReader
-> create HTTPSession
-> Register
-> OnAccept
-> OnMessage
-> Handler
-> OnClose
-> Unregister
```

关键约束：

- 请求体超限返回 413。
- Handler 错误返回 500。
- 插件错误按语义映射状态码。
- 每个请求结束必须注销 session。

### 7.4 WebSocket

职责：

- HTTP Upgrade。
- 每个连接一个长生命周期 Session。
- 读循环处理 binary message。
- 写操作加锁，避免并发写破坏 gorilla/websocket 约束。
- ping loop 保持连接活性。
- origin check 可配置。

关键约束：

- 最大消息限制必须生效。
- 关闭路径必须幂等。
- Gateway shutdown 和 read loop 同时退出时，`OnClose` 只能执行一次。

### 7.5 CoAP

CoAP 基于 UDP，当前实现重点：

- 基础 CoAP header 解析和 marshal。
- Token 长度校验。
- CON 请求 ACK。
- Message ID 去重。
- 伪会话 TTL。
- 可接入 LwM2M responder。

当前边界：

- 已覆盖基础 request/ACK/duplicate 行为。
- Block-wise、Observe、完整 option 编解码和重传状态机仍属后续目标。

### 7.6 LwM2M

LwM2M 位于 `internal/protocol/lwm2m`，不是 transport。

职责：

- 维护 endpoint 注册表。
- 支持 register、update、deregister。
- 管理 object/resource path。
- 支持 resource read/write。
- 支持 lifetime expiry sweep。
- 提供 CoAP 文本命令 responder。

当前 CoAP 文本绑定：

```text
register <endpoint> <lifetime-seconds> [object-path...]
update <endpoint> <lifetime-seconds>
deregister <endpoint>
write <endpoint> <resource-path> <value>
read <endpoint> <resource-path>
```

后续目标：

- 正式 LwM2M URI/Query/Content-Format 编解码。
- Observe/Notify。
- Bootstrap。
- DTLS/OSCORE 安全配置。

### 7.7 QUIC

职责：

- 基于 quic-go 提供 stream transport。
- 强制 TLS config。
- 每个 stream 映射为 Session。
- 读取 stream payload，执行插件链和 Handler。
- Handler 可回写 stream。

关键约束：

- 没有 TLS 不允许启动。
- MaxMessageSize 必须限制读取。
- oversize stream 不应调用 Handler。
- Shutdown 必须清理 active stream session。

### 7.8 gRPC-Web

支持两种入口：

| 模式 | 描述 |
| --- | --- |
| Direct HTTP | HTTP POST，支持 raw payload 和 framed gRPC-Web payload |
| WebSocket Mode | WebSocket 传输，Protocol 仍标记为 `grpc-web` |

职责：

- MaxMessageBytes 限制。
- gRPC-Web data frame 解析。
- strict malformed frame 返回 400。
- framed response 写 data frame 和 trailer frame。
- WebSocket 模式复用 Session 和插件链。

后续目标：

- 完整 content-type 协商。
- base64 grpc-web-text。
- method/service 路由。
- trailer metadata 扩展。

---

## 8. 应用装配与配置

`internal/app` 是可运行应用层，负责把配置转为运行时对象。

### 8.1 配置来源

1. 默认配置。
2. JSON 配置文件。
3. 环境变量覆盖。
4. 命令行 `-config` 指定配置文件路径。

### 8.2 配置字段

顶层：

| 字段 | 说明 |
| --- | --- |
| `shutdown_timeout` | 优雅关闭超时 |
| `health_addr` | 健康检查 HTTP 地址 |
| `metrics_addr` | Prometheus metrics HTTP 地址 |
| `protocols` | 协议监听列表 |

协议：

| 字段 | 说明 |
| --- | --- |
| `name` | `tcp`、`udp`、`http`、`websocket`、`coap`、`grpc-web` |
| `enabled` | 是否启用，默认 true |
| `addr` | 监听地址 |
| `path` | WebSocket 或 gRPC-Web WebSocket path |
| `mode` | CoAP 使用 `lwm2m` 时接入 LwM2M responder |
| `max_message_bytes` | gRPC-Web 最大消息大小，必须非负 |

### 8.3 健康与指标

健康端点：

```text
GET /healthz -> 进程存活
GET /readyz  -> Gateway Ready 状态
```

指标端点：

```text
GET /metrics -> Prometheus text format
```

---

## 9. 可观测设计

核心可观测接口位于 `internal/core`：

| 接口 | 用途 |
| --- | --- |
| Logger | 结构化日志 |
| Metrics | Counter/Gauge/Histogram 抽象 |
| Tracer | Span 创建和错误记录 |

当前实现位于 `internal/infra/observability`：

- MemoryLogger。
- MemoryMetrics。
- PrometheusMetrics。
- OpenTelemetryTracer adapter。

设计原则：

- core 不依赖具体观测供应商。
- 协议和插件只依赖 core 接口。
- Prometheus exporter 可作为 HTTP handler 挂载。
- 后续在热路径上要避免观测造成阻塞。

后续指标建议：

```text
shark_sessions{protocol,state}
shark_messages_total{protocol,direction}
shark_message_bytes_total{protocol,direction}
shark_handler_duration_seconds{protocol}
shark_plugin_errors_total{plugin,type}
shark_transport_errors_total{protocol,type}
shark_dropped_messages_total{protocol,reason}
```

---

## 10. 基础设施组件

### 10.1 Cache

当前为内存 TTL cache。

职责：

- Set/Get/Delete/Has。
- TTL 惰性过期。
- Sweep/Clear/Len 维护能力。

后续可接 Redis adapter，但 core 和 runtime 不直接依赖 Redis。

### 10.2 Store

当前为内存 bucket/key store。

用途：

- persistence plugin 保存生命周期事件。
- 测试和示例持久化。

后续可接数据库或对象存储 adapter。

### 10.3 PubSub

当前为进程内 PubSub。

用途：

- cluster plugin 广播节点消息。

后续可接 NATS、Redis Pub/Sub、Kafka、MQTT Broker 事件总线。

### 10.4 CircuitBreaker

当前支持 Closed/Open/HalfOpen。

用途：

- 包裹外部依赖调用。
- 防止插件持久化或集群外部组件拖垮主流程。

---

## 11. 部署架构

### 11.1 Docker

Dockerfile 采用多阶段构建：

```text
golang:1.26-alpine -> build binary
alpine:3.22        -> run binary as non-root user
```

构建支持：

```text
ARG GOPROXY=https://goproxy.cn,direct
```

原因：云环境内访问 `proxy.golang.org` 可能超时，构建代理必须可配置。

### 11.2 docker-compose

compose 默认暴露：

```text
18000 TCP
18080 metrics
18081 health/readiness
```

容器环境变量默认绑定：

```text
SHARK_TCP_ADDR=0.0.0.0:18000
SHARK_METRICS_ADDR=0.0.0.0:18080
SHARK_HEALTH_ADDR=0.0.0.0:18081
```

### 11.3 Kubernetes

K8s 资产包括：

- Deployment。
- Service。
- Kustomization。

生产默认：

- 非 root 运行。
- 禁止权限提升。
- drop ALL capabilities。
- read-only root filesystem。
- readinessProbe 使用 `/readyz`。
- livenessProbe 使用 `/healthz`。
- requests/limits 已设置。

### 11.4 Helm

Helm chart 用于参数化：

- image repository/tag/pullPolicy。
- replicaCount。
- service type/ports。
- env listener 地址。
- resources。
- securityContext。
- probes。

---

## 12. 测试与验证策略

测试层次：

| 层次 | 目录/命令 | 目的 |
| --- | --- | --- |
| 包单元测试 | `go test ./internal/...` | 验证模块内行为 |
| 全量测试 | `go test ./...` | 验证所有 package |
| 脚本化测试 | `go run scripts/run_tests.go -mode all` | 输出 JSON 和可读报告 |
| Race | `go run scripts/run_tests.go -mode race` | 并发安全 |
| Coverage | `go run scripts/run_tests.go -mode cover` | 覆盖率 smoke |
| Deploy | `scripts/validate_deploy.ps1` | 部署资产静态/可选渲染 |
| CI | GitHub Actions Windows + Ubuntu | 跨平台回归 |

回归测试原则：

- 每个生产缺陷先写最小失败测试。
- 修复后保留测试。
- 协议测试优先使用 `127.0.0.1:0`，避免固定端口冲突。
- 部署测试检查语义，不只检查文件存在。
- 云端验证记录写入 `docs/PROJECT-REVIEW-*.md`。

当前关键回归：

- Gateway stop/start 复用 SessionManager。
- WebSocket shutdown `OnClose` 只执行一次。
- gRPC-Web max message 配置非法值拒绝启动。
- CoAP duplicate CON 不重复执行 handler。
- TCP plugin drop 后连接仍可复用。

---

## 13. 安全与防御设计

当前已具备：

- WebSocket/gRPC-Web Origin Check。
- HTTP/gRPC-Web body/message size 限制。
- QUIC 强制 TLS。
- Docker/K8s 非 root 和最小权限。
- Blacklist/RateLimit/AutoBan 插件基础能力。

后续必须补齐：

1. TLS/mTLS 配置文件化。
2. TCP/QUIC 证书热加载。
3. QUIC mTLS、证书轮换和热加载。
4. CoAP DTLS 或 OSCORE 方案设计。
5. HTTP CORS 策略配置化；WebSocket/gRPC-Web Origin allowlist 已接入。
6. 请求级 deadline 和 idle timeout。
7. 慢连接、空连接、异常帧防御。
8. OverloadProtector 和 backpressure。
9. 敏感配置不进入日志和审查报告。

---

## 14. 性能演进路线

旧 `shark-socket` 的性能目标仍是长期方向，但不作为当前最小架构前提。

长期目标：

| 指标 | 目标 |
| --- | --- |
| TCP 吞吐 | 单核 100K msg/s 级别 |
| 并发连接 | 100K 级别 |
| 热路径分配 | 尽量 0 alloc |
| 插件开销 | 低于 200ns/hop |
| P99 延迟 | 毫秒级以内 |

演进步骤：

1. 用 benchmark 建立真实基线。
2. 找出热路径分配点。
3. 引入 BufferPool，但限制在协议内部或明确接口边界。
4. SessionManager 从单锁演进到分片锁。
5. WorkerPool 加入弹性临时 worker。
6. 写队列背压指标化。
7. pprof 驱动优化，不凭直觉改结构。

---

## 15. 目录演进建议

当前目录已经可用，但如果要继续向旧项目成熟度靠拢，建议分阶段演进：

### 阶段 A：保持现有结构

```text
internal/core
internal/runtime
internal/transport
internal/protocol
internal/plugin
internal/infra
```

适合当前项目，改动最小。

### 阶段 B：补充防御和测试分层

新增：

```text
internal/defense
tests/defects
tests/integration
tests/benchmark
```

用于承载生产缺陷回归、端到端测试和压测基准。

### 阶段 C：协议标准化

当 CoAP/LwM2M/gRPC-Web 继续深入后，可将协议标准编解码和 transport 分离：

```text
internal/transport/coap
internal/protocol/coap
internal/protocol/lwm2m
internal/protocol/grpcweb
```

是否拆分以复杂度为准，不提前抽象。

---

## 16. 近期实施路线

### P0：架构地基

1. 中文架构文档稳定。
2. 配置、README、测试策略与架构一致。
3. 保持 `go test ./...`、`go vet ./...`、脚本化 all-mode 通过。
4. 保持 CI Windows + Ubuntu 通过。

### P1：生产配置

1. mTLS 配置模型和证书热加载。
2. QUIC mTLS、证书轮换和热加载。
3. 插件配置文件化。
4. HTTP CORS 策略配置化；WebSocket/gRPC-Web Origin allowlist 已完成。
5. 统一 shutdown stage timeout 配置。

### P1：协议增强

1. CoAP option 完整编解码。
2. CoAP block-wise 和 retransmit。
3. LwM2M 标准路径、content-format、observe。
4. gRPC-Web text/base64 和 method 路由。
5. TCP TLS 已完成；后续补充 mTLS client-auth 和证书热加载。

### P2：性能与防御

1. BufferPool。
2. 分片 SessionManager。
3. Backpressure。
4. OverloadProtector。
5. 高频日志采样。
6. benchmark 和 pprof 报告沉淀。

### P2：部署与运维

1. 云端 K8s kubeconfig/context 验证。
2. Helm install/upgrade 实机记录。
3. Prometheus/Grafana 示例。
4. 多副本滚动升级验证。
5. 镜像发布和 tag 策略。

---

## 17. 架构决策记录

### ADR-001：核心 Session 保持原始字节

决策：`core.Session.Send` 只接收 `[]byte`。

原因：

- Gateway 必须管理混合协议 session。
- 插件链天然处理原始 payload。
- 泛型 Session 会把业务类型泄露到运行时层。
- 类型安全可以通过 `Codec[M]` 在上层实现。

### ADR-002：Gateway 拥有共享 Runtime

决策：SessionManager、PluginRunner、Logger、Metrics、Tracer 由 Gateway 创建或注入，协议通过 `UseRuntime` 接收。

原因：

- 避免每个协议各自维护插件和 session。
- 保证跨协议统计、广播和关闭一致。
- 便于测试替换 Runtime 组件。

### ADR-003：关闭流程分阶段

决策：协议可实现 `StagedServer`，Gateway 按 StopAccept、Drain、CloseSessions 执行。

原因：

- 直接 `Stop` 容易混淆 listener、goroutine、session 的释放顺序。
- 分阶段关闭更容易定位超时和泄漏。
- 对 TCP/WebSocket/QUIC 等长连接协议尤其重要。

### ADR-004：插件 panic 在 PluginChain 隔离

决策：panic recover 放在 PluginChain，而不是散落在各协议实现中。

原因：

- 插件失败策略一致。
- 协议代码更专注网络生命周期。
- 后续可统一记录 plugin panic 指标。

### ADR-005：部署构建代理可配置

决策：Dockerfile 暴露 `GOPROXY` build arg，compose 提供默认代理。

原因：

- 云服务器构建环境可能无法稳定访问 `proxy.golang.org`。
- 构建网络问题不应阻塞镜像生产。
- 默认值可工作，仍允许用户覆盖。

---

## 18. 验收标准

每次架构级改动必须满足：

1. `go test ./... -count=1` 通过。
2. `go vet ./...` 通过。
3. 相关 focused tests 先失败后通过，若是缺陷修复。
4. `go run scripts/run_tests.go -mode all -timeout 5m` 通过。
5. 部署相关改动必须更新 `tests/deploy`。
6. 公共行为改变必须同步 README、配置文档、测试策略或审查报告。
7. 云端实测结果写入 `PROJECT-REVIEW-YYMMDD-HHMMSS.md`。

---

## 19. 当前状态摘要

当前已经具备：

- Gateway/runtime/session/plugin 核心。
- TCP、UDP、HTTP、WebSocket、CoAP、QUIC、gRPC-Web transport。
- LwM2M 内存生命周期模型和 CoAP 文本绑定。
- 基础插件和基础设施组件。
- JSON/env 配置入口。
- 健康、就绪、Prometheus metrics。
- Docker、docker-compose、K8s、Helm 部署基线。
- Windows + Ubuntu GitHub Actions。
- 本地测试、race、coverage、脚本化日志。
- 双云服务器验证记录：跨机多协议测试、Docker Compose、kind Kubernetes、Helm 部署均已通过。

当前未完成：

- 外部生产 Kubernetes 集群接入；kind 集群实机 apply 与 Helm 安装已通过。
- TLS/mTLS 完整配置模型和证书热加载。
- QUIC mTLS、证书轮换和热加载。
- CoAP/LwM2M 完整标准特性。
- 性能池化和分片 SessionManager。
- 完整防御体系。

---

## 20. 文档维护规则

- 架构文档描述设计边界和长期方向，不记录冗长测试流水账。
- 具体验证命令和结果写入 `PROJECT-REVIEW-*.md`。
- 配置字段写入 `CONFIGURATION-*.md`。
- 测试方法写入 `TEST-STRATEGY-*.md` 和 `PROTOCOL-TEST-GUIDE-*.md`。
- 重要架构决策可后续拆分到 `docs/adr/`。

---

*文档版本：v1.0 中文重设计版。参考 `shark-socket` 成熟架构，但以 `shark-socket-new` 当前代码边界和后续演进为准。*
