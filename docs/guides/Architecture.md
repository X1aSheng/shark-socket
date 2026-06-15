# Shark-Socket 综合架构设计文档

> 高性能、可扩展的多协议服务端网络框架，采用 Go 1.26+ 开发。在 `shark-socket` 正确架构基础上，融合 `shark-socket` 成熟工程积累，提供 TCP、UDP、HTTP、WebSocket、CoAP、LwM2M、QUIC、gRPC-Web 协议的统一 Gateway 运行时。MQTT 3.1.1/5.0 由外部项目 [shark-MQTT](https://github.com/X1aSheng/shark-MQTT) 作为独立 Broker 提供，二者通过数据库、缓存、消息契约互通。

---

## 1. 项目定位与设计哲学

### 1.1 第一原则

**先正确，再完整，再高效。**

1. 运行时所有权、协议生命周期、插件执行和关闭流程必须可解释、可测试、可恢复。
2. 公共 API 小而稳定，内部实现可演进。
3. Gateway 统一编排多协议运行时，SessionManager、PluginRunner、Logger、Metrics、Tracer 的所有权显式。
4. 各协议只负责自己的网络细节，不私自创建或关闭共享运行时资源。
5. 类型化消息放在协议原始字节层之上，通过 Codec 适配，不污染核心 Session。
6. 优雅关闭分阶段执行：停止接收、等待读写、关闭会话、收尾释放。
7. 测试、脚本、部署清单和文档保持同步。
8. 用 benchmark 驱动性能优化，不凭直觉改结构。

### 1.2 设计原则

| 原则 | 实践 |
| --- | --- |
| 接口契约优先 | 所有模块通过 interface 交互，依赖倒置 |
| 显式所有权 | Gateway 创建并注入 Runtime，协议通过 UseRuntime 接收 |
| 分阶段关闭 | StagedServer 接口，StopAccept / Drain / CloseSessions 三阶段 |
| 零值可用 | Functional Options 模式，默认值合理 |
| 失败隔离 | 单连接 / 单协程 panic 不影响整体，插件 panic 在 PluginRunner 隔离 |
| 可观测优先 | 所有关键路径内置 metrics / trace / log，采集不阻塞业务 |
| benchmark 驱动 | 建立基准后再优化，pprof 定位热点 |
| 编译期验证 | `var _ Interface = (*Impl)(nil)` 接口满足检查 |

### 1.3 当前阶段非目标与转换条件

| 当前非目标 | 转换为目标的阶段 | 转换条件 |
| --- | --- | --- |
| 泛型 Session 作为核心接口 | v1.0 API 稳定前评估 | 现有 core.Session 无法承载至少两个真实业务协议的类型安全需求 |
| 六级 BufferPool、时间轮、分片 LRU | P2 性能与防御 | benchmark 证明分配、超时扫描或 Session 锁竞争成为瓶颈 |
| 完整防御体系（OverloadProtector、BackPressure） | P2 性能与防御 | 基准压测建立，热路径分配点已定位 |
| Gateway 直接耦合数据库、Redis、消息队列 | P2/P3 集群与持久化 | 外部 adapter 接口稳定，完成至少一种真实后端集成测试 |
| 内建完整 MQTT Broker | P1/P2 MQTT 专项 | 明确选择内建 Broker 而不是外部 Broker 适配 |
| 每个协议一次性实现完整标准 | 按协议专项推进 | 当前 smoke / 边界测试稳定，且有明确互操作目标 |

---

## 2. 总体分层架构

### 2.1 层次划分

```
┌─────────────────────────────────────────────────────────────────────┐
│ Layer 0: api/                                                        │
│   对外门面层。导出类型别名、工厂函数、Option 透传、Codec 接口定义   │
├─────────────────────────────────────────────────────────────────────┤
│ Layer 1: cmd/ + internal/application/                                │
│   可运行入口 + 应用装配层                                           │
│   配置加载（JSON + env）→ Gateway 装配 → 健康 / 指标服务 → 信号处理 │
│   配置验证在 application 层统一执行，不在 Start() 中执行            │
├─────────────────────────────────────────────────────────────────────┤
│ Layer 2: internal/runtime/                                           │
│   运行时层：Gateway、SessionManager、PluginRunner、Runtime 容器     │
├─────────────────────────────────────────────────────────────────────┤
│ Layer 3: internal/transport/ + internal/plugin/ + internal/protocol/ │
│   传输协议层：TCP、UDP、HTTP、WebSocket、CoAP、QUIC、gRPC-Web       │
│   应用协议层：LwM2M 生命周期模型                                    │
│   插件层：黑名单、限流、心跳、持久化、自动封禁、慢处理、集群广播    │
├─────────────────────────────────────────────────────────────────────┤
│ Layer 4: internal/core/                                              │
│   稳定契约层：Protocol、Session、Server、Runtime、Plugin、Codec      │
│   可观测接口：Logger、Metrics、Tracer                               │
│   错误体系：完整错误变量 + 分类判断函数                             │
├─────────────────────────────────────────────────────────────────────┤
│ Layer 5: internal/infrastructure/                                    │
│   基础设施层：cache、store、pubsub、circuitbreaker、observability   │
│   P2 引入：bufferpool、timewheel、logsampler                        │
└─────────────────────────────────────────────────────────────────────┘
```

### 2.2 依赖矩阵

依赖只能从上层指向下层，同层内部保持局部依赖：

```
api / cmd / application
        ↓
runtime + transport + plugin + protocol
        ↓
core + infrastructure
```

**严格禁止事项：**

- `core` 不依赖任何具体协议实现。
- `runtime` 不依赖 `cmd` 或 `application`。
- `transport` 不直接创建全局 Gateway。
- `plugin` 不直接控制协议 listener 生命周期。
- `infrastructure` 不反向调用业务层。
- `protocol` 不依赖 `transport` 具体实现。

**允许关系总览（✓=允许 ✗=禁止）：**

| | api | cmd | app | runtime | transport | plugin | protocol | core | infra |
|---|---|---|---|---|---|---|---|---|---|
| api | — | ✗ | ✗ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| cmd | ✓ | — | ✓ | ✗ | ✗ | ✗ | ✗ | ✓ | ✗ |
| application | ✓ | ✗ | — | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| runtime | ✗ | ✗ | ✗ | — | ✗ | ✓ | ✗ | ✓ | ✓ |
| transport | ✗ | ✗ | ✗ | ✗ | — | ✗ | ✓ | ✓ | ✓ |
| plugin | ✗ | ✗ | ✗ | ✗ | ✗ | — | ✗ | ✓ | ✓ |
| protocol | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | — | ✓ | ✓ |
| core | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | — | ✗ |
| infrastructure | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | — |

---

## 3. 目录结构

```
shark-socket/
├── api/
│   └── api.go                          # 统一对外门面：类型别名、工厂函数、Codec 接口
│
├── cmd/
│   └── server/
│       └── main.go                     # 可运行入口：加载配置、启动 App、处理信号
│
├── internal/
│   ├── core/
│   │   ├── protocol.go                 # Protocol 类型 + 内置常量
│   │   ├── session.go                  # Session 接口 + SessionState + SessionManager 接口
│   │   ├── server.go                   # Server 接口 + RuntimeConfigurable + StagedServer
│   │   ├── runtime.go                  # Runtime 接口（Sessions/Plugins/Logger/Metrics/Tracer）
│   │   ├── message.go                  # Message 结构体（SessionID/Protocol/Payload/Meta）
│   │   ├── handler.go                  # Handler 函数类型 + TypedHandler + AdaptTyped
│   │   ├── plugin.go                   # Plugin 接口 + PluginRunner 接口 + 控制错误
│   │   ├── codec.go                    # Codec[M] 泛型接口 + 内置 RawCodec / JSONCodec
│   │   ├── errors.go                   # 完整错误体系 + 分类判断函数
│   │   └── observability.go            # Logger / Metrics / Tracer 接口定义
│   │
│   ├── runtime/
│   │   ├── gateway.go                  # Gateway：多协议编排、分阶段关闭、Runtime 注入
│   │   ├── gateway_options.go          # GatewayOption Functional Options
│   │   ├── session_manager.go          # SessionManager 实现：单锁（P0）→ 分片锁（P2）
│   │   ├── plugin_runner.go            # PluginRunner：排序、执行、panic 隔离、指标
│   │   ├── runtime_impl.go             # DefaultRuntime 实现 Runtime 接口
│   │   └── worker_pool.go              # WorkerPool：固定 + 弹性临时 Worker，四种队列策略
│   │
│   ├── transport/
│   │   ├── tcp/
│   │   │   ├── server.go               # TCP Server：accept + Framer + WorkerPool + StagedServer
│   │   │   ├── session.go              # TCPSession：写队列 + drain + 6 步 Close
│   │   │   ├── client.go               # TCP Client：自动重连 + 指数退避
│   │   │   ├── framer.go               # Framer 接口 + 4 种内置实现
│   │   │   └── options.go              # TCPOption Functional Options
│   │   ├── udp/
│   │   │   ├── server.go               # UDP Server：单 conn + 伪会话 + sweep
│   │   │   ├── session.go              # UDPSession：伪会话，直接 WriteToUDP
│   │   │   └── options.go
│   │   ├── http/
│   │   │   ├── server.go               # HTTP Server：Mode A（轻量）+ Mode B（Session+Plugin）
│   │   │   ├── session.go              # HTTPSession：per-request 临时会话
│   │   │   └── options.go
│   │   ├── websocket/
│   │   │   ├── server.go               # WS Server：Upgrade + pingLoop + StagedServer
│   │   │   ├── session.go              # WSSession：writeMutex + Ping/Pong
│   │   │   └── options.go
│   │   ├── coap/
│   │   │   ├── server.go               # CoAP Server：UDP 基础 + ACK + 去重
│   │   │   ├── session.go              # CoAP Session：伪会话 + pendingACK + msgCache
│   │   │   ├── message.go              # CoAP 帧解析 + marshal
│   │   │   ├── framer.go               # CoAP Framer（UDP 数据报边界）
│   │   │   └── options.go
│   │   ├── quic/
│   │   │   ├── server.go               # QUIC Server：强制 TLS + stream 映射 Session
│   │   │   ├── session.go              # QUICSession：写队列 + stream 生命周期
│   │   │   └── options.go
│   │   └── grpcweb/
│   │       ├── server.go               # gRPC-Web：Direct HTTP + WebSocket 双模式
│   │       ├── session.go              # gRPC-Web Session（WebSocket 模式）
│   │       ├── frame.go                # gRPC-Web 数据帧解析与序列化
│   │       └── options.go
│   │
│   ├── protocol/
│   │   └── lwm2m/
│   │       ├── server.go               # LwM2M Server：注册表 + Read/Write/Execute
│   │       ├── client.go               # LwM2M Client：Register/Update/Deregister
│   │       ├── model.go                # ObjectPath / ObjectLink / Resource / Registration
│   │       ├── responder.go            # CoAP 文本命令 responder（CoAP 模式接入）
│   │       ├── constants.go            # Content-Format 常量
│   │       └── options.go
│   │
│   ├── plugin/
│   │   ├── blacklist.go                # IP/CIDR 黑名单（精确 O(1) + CIDR + Cache 双查）
│   │   ├── ratelimit.go                # 令牌桶限流（per-IP + 全局双层）
│   │   ├── heartbeat.go                # 心跳超时清理（P0：ticker 扫描；P2：时间轮）
│   │   ├── persistence.go              # 异步持久化（Channel 缓冲 + 批量写入 + CircuitBreaker）
│   │   ├── autoban.go                  # 自动封禁（触发限流/协议错误 → 加入黑名单）
│   │   ├── slowhandler.go              # 慢处理日志（记录超时 Handler 调用）
│   │   ├── cluster.go                  # 跨节点路由（PubSub 广播 + Cache 会话目录）
│   │   └── options.go                  # 插件通用 Options
│   │
│   ├── application/
│   │   ├── app.go                      # App：配置 → Gateway + Server 装配 + 健康 / 指标服务
│   │   ├── config.go                   # Config 结构体：JSON 反序列化 + env 覆盖 + 统一验证
│   │   └── options.go                  # AppOption Functional Options
│   │
│   └── infrastructure/
│       ├── cache/
│       │   └── cache.go                # Cache 接口 + MemoryCache（TTL 惰性过期）
│       ├── store/
│       │   └── store.go                # Store 接口 + MemoryStore（bucket/key）
│       ├── pubsub/
│       │   └── pubsub.go               # PubSub 接口 + Subscription + ChannelPubSub
│       ├── circuitbreaker/
│       │   └── circuitbreaker.go       # CircuitBreaker：Closed / Open / HalfOpen
│       ├── observability/
│       │   ├── logger.go               # slogLogger（JSON）+ MemoryLogger + NopLogger
│       │   ├── metrics.go              # PrometheusMetrics（静态预注册）+ MemoryMetrics
│       │   └── tracer.go               # OpenTelemetryTracer adapter + NoopTracer
│       ├── bufferpool/
│       │   └── pool.go                 # 六级 BufferPool（P2 启用）
│       ├── timewheel/
│       │   └── timewheel.go            # 时间轮（P2 启用，替换 heartbeat ticker 扫描）
│       └── logsampler/
│           └── sampler.go              # 高频日志采样器（P2 启用）
│
├── tests/
│   ├── unit/                           # 包级单元测试（补充 internal 包内测试）
│   ├── integration/                    # 跨包端到端集成测试
│   ├── defects/                        # 已修复缺陷的最小复现回归测试
│   └── benchmark/                      # 吞吐、延迟、内存基准测试
│
├── examples/
│   ├── basic_tcp/main.go
│   ├── basic_udp/main.go
│   ├── basic_http/main.go
│   ├── basic_websocket/main.go
│   ├── basic_coap/main.go
│   ├── basic_lwm2m/main.go
│   ├── basic_quic/main.go
│   ├── multi_protocol/main.go
│   ├── tls_server/main.go
│   ├── typed_handler/main.go           # Codec + AdaptTyped 类型化 Handler 示例
│   ├── custom_plugin/main.go
│   └── graceful_shutdown/main.go
│
├── scripts/
│   ├── run_tests.go                    # 跨平台脚本化测试入口（unit/race/cover/all 模式）
│   ├── run_tests.go (via -mode deploy)              # 部署资产语义验证
│   └── build.sh
│
├── deploy/
│   ├── docker/
│   │   ├── Dockerfile                  # 多阶段构建，支持 GOPROXY build arg
│   │   ├── docker-compose.yml
│   │   └── .dockerignore
│   ├── kubernetes/
│   │   ├── application/
│   │   │   ├── deployment.yaml
│   │   │   ├── service.yaml
│   │   │   └── kustomization.yaml
│   │   ├── monitoring/
│   │   │   ├── servicemonitor.yaml
│   │   │   └── kustomization.yaml
│   │   └── kustomization.yaml          # 根聚合入口
│   └── helm/
│       └── shark-socket/
│           ├── Chart.yaml
│           ├── values.yaml
│           └── templates/
│               ├── deployment.yaml
│               ├── service.yaml
│               ├── configmap.yaml
│               ├── serviceaccount.yaml
│               └── _helpers.tpl
│
├── docs/
│   ├── ARCHITECTURE.md                 # 本文档
│   ├── CONFIGURATION.md                # 配置字段完整参考
│   ├── TEST-STRATEGY.md                # 测试层次、回归原则、覆盖目标
│   ├── PROTOCOL-GUIDE.md               # 各协议接入指南
│   ├── adr/
│   │   ├── ADR-001-session-raw-bytes.md
│   │   ├── ADR-002-gateway-owns-runtime.md
│   │   ├── ADR-003-staged-shutdown.md
│   │   ├── ADR-004-plugin-panic-isolation.md
│   │   ├── ADR-005-codec-adaptation-layer.md
│   │   ├── ADR-006-benchmark-driven-optimization.md
│   │   └── ADR-007-build-proxy-configurable.md
│   └── PROJECT-REVIEW-*.md             # 云端实测验证记录
│
├── go.mod                              # module shark-socket, go 1.26
├── go.sum
├── .github/
│   └── workflows/
│       ├── ci-ubuntu.yml
│       └── ci-windows.yml
└── README.md
```

---

## 4. 核心契约层（internal/core/）

### 4.1 Protocol（protocol.go）

```go
// Protocol 是协议身份标识，uint8 底层，编译期常量。
type Protocol uint8

const (
    TCP      Protocol = 1
    UDP      Protocol = 2
    HTTP     Protocol = 3
    WebSocket Protocol = 4
    CoAP     Protocol = 5
    QUIC     Protocol = 6
    GRPCWeb  Protocol = 7
    LwM2M    Protocol = 8
    Custom   Protocol = 99
)

// String() 满足 fmt.Stringer，用于日志和指标标签。
// 协议标签字符串通过 unique 包（Go 1.26）池化，减少分配。
```

协议标识用于：Gateway 注册去重、Session 标记来源、插件和指标标签、日志和追踪属性。

### 4.2 Message（message.go）

```go
type Message struct {
    SessionID uint64
    Protocol  Protocol
    Payload   []byte            // 始终为原始字节，业务结构不进入核心层
    Meta      map[string]string // 协议级元数据（预分配容量 4）
}
```

**设计原则：**

- `Payload` 始终为 `[]byte`，业务类型通过 `Codec[M]` 和 `AdaptTyped[M]` 在 Handler 层适配。
- `Meta` 用于协议级透传字段（如 CoAP Token、HTTP Header），不用于业务属性。
- 业务属性通过 `Session.SetMeta` 存储在会话维度。

**类型化适配路径：**

```
网络帧 → []byte → core.Message → Codec[M].Decode → TypedHandler[M]
TypedHandler 返回值 → Codec[M].Encode → Session.Send([]byte)
```

### 4.3 Session（session.go）

```go
// SessionState 会话状态机。
type SessionState uint8

const (
    Connecting SessionState = 0 // 连接建立中
    Active     SessionState = 1 // 正常通信
    Draining   SessionState = 2 // 排空写队列中
    Closed     SessionState = 3 // 已关闭
)

// Session 是运行时统一会话接口。
// 核心字节层：Send 只接收 []byte，类型安全通过 Codec 在上层实现。
type Session interface {
    // 身份与元信息（不可变）
    ID()           uint64
    Protocol()     Protocol
    RemoteAddr()   net.Addr
    LocalAddr()    net.Addr
    CreatedAt()    time.Time

    // 状态（原子操作）
    State()        SessionState
    IsAlive()      bool           // State() == Active
    LastActiveAt() time.Time      // 心跳、TTL、清理使用

    // 生命周期
    Context()      context.Context  // 关闭时 cancel
    Send([]byte)   error            // 关闭后返回 ErrSessionClosed
    Close(context.Context) error    // 幂等关闭，ctx 控制 drain 超时

    // 元数据（线程安全 KV，sync.Map 实现）
    SetMeta(key string, val any)
    GetMeta(key string) (any, bool)
    DelMeta(key string)
}
```

**会话状态机：**

```
Connecting ──accept 成功──→ Active ──Close()──→ Draining ──drain 完成──→ Closed
    │                          │                                              ▲
    └──error / fatal───────────┴──────────────────────────────────────────────┘

状态转换：atomic.Int32 CAS 保证并发唯一性
幂等关闭：sync.Once 封装 Close 核心逻辑
```

**关键实现要求：**

- `Close` 必须幂等，多次调用等价一次。
- `Send` 在 Draining / Closed 状态下必须返回 `ErrSessionClosed`。
- `LastActiveAt` 由协议层在收到任意数据时更新（`TouchActive` 内部方法）。
- 元数据实现必须线程安全（`sync.Map`）。
- 协议实现可以有自己的 session 结构，但必须满足统一接口。

**编译期验证：**

```go
var _ Session = (*TCPSession)(nil)
var _ Session = (*UDPSession)(nil)
var _ Session = (*WSSession)(nil)
```

### 4.4 SessionManager（session.go）

```go
type SessionManager interface {
    // 分配全局递增 Session ID
    NextID() uint64

    // 注册 / 注销 / 查询
    Register(Session) error
    Unregister(id uint64)
    Get(id uint64) (Session, bool)

    // 统计与遍历
    Count() int64
    Range(func(Session) bool)           // 可中断遍历
    All() iter.Seq[Session]             // Go 1.26 iter 包

    // 广播（内部快照，不持锁发送）
    Broadcast([]byte) error

    // 关闭所有当前会话（不永久关闭 Manager，Gateway Stop 后可复用）
    CloseAll(context.Context) error
}
```

**设计边界：**

- Manager 不创建协议连接，不拥有 listener。
- `CloseAll` 只清理当前会话，不永久关闭 Manager。
- Gateway 停止后再次启动时，Manager 仍应可用（Start→Stop→Start 可重入）。
- `Broadcast` 先快照 session 列表再释放锁，不持锁发送（防广播阻塞）。

**并发安全保证：**

- `NextID`：`atomic.Uint64` 全局递增，跨重启 ID 可能复用，Session 不依赖 ID 的全局唯一性。
- `Count`：`atomic.Int64` 无锁读取。
- P0 实现：全局 `sync.RWMutex`，功能正确优先。
- P2 演进：32 分片锁 + per-shard LRU 淘汰（benchmark 证明锁竞争为瓶颈后引入）。

### 4.5 Server 接口族（server.go）

```go
// Server 是所有协议服务的基础接口。
type Server interface {
    Protocol() Protocol
    Start(context.Context) error
    Stop(context.Context) error
}

// RuntimeConfigurable：支持 Gateway 运行时注入。
// 协议实现此接口后，Gateway 在启动前自动调用 UseRuntime。
type RuntimeConfigurable interface {
    UseRuntime(Runtime)
}

// StagedServer：支持分阶段关闭的协议实现（推荐长连接协议实现此接口）。
type StagedServer interface {
    StopAccept(context.Context) error   // 停止接收新连接 / 新请求
    Drain(context.Context) error        // 等待读写 goroutine 收敛
    CloseSessions(context.Context) error // 关闭协议持有的活跃会话
}
```

**StagedServer 各协议实现说明：**

| 协议 | StopAccept | Drain | CloseSessions |
| --- | --- | --- | --- |
| TCP | listener.Close() + 停止 acceptLoop | WaitGroup 等待 readLoop/writeLoop | 遍历 Close 所有 TCPSession |
| WebSocket | 停止 HTTP Upgrade Mux | 等待 pingLoop/readLoop | 发送 Close 帧后关闭 WSSession |
| QUIC | quic.Listener.Close() | 等待 stream goroutine | connection.CloseWithError() |
| UDP | 不适用（无 accept） | 不适用（单 goroutine） | 清理所有伪会话 |
| HTTP | 关闭 http.Server（Mode B） | 不适用 | 不适用（per-request session） |

**Drain 失败降级策略：**

- Drain 超时后强制关闭所有 goroutine（通过 context cancel）。
- 记录 Warn 日志：`"drain timeout, forcing close, goroutine_count=N"`。
- 不阻塞后续 CloseSessions 阶段。

### 4.6 Runtime 接口（runtime.go）

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

**使用规则：**

- 协议层只依赖 `Runtime` 接口，不依赖具体实现。
- 单独启动协议服务时（测试场景），可使用 `DefaultRuntime`（空实现）。
- 通过 Gateway 启动时，必须接收 Gateway 注入的 Runtime。
- 各协议实际使用的 Runtime 子集应在实现注释中标注（便于评估后续拆分需要）。

### 4.7 Plugin 与 PluginRunner（plugin.go）

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

// BasePlugin 提供空实现，自定义插件只需覆盖关心的方法。
type BasePlugin struct{}

// PluginRunner 是插件链运行器接口。
type PluginRunner interface {
    // 注册插件，重复 Name 后注册覆盖（记录 Warn 日志）。
    Register(Plugin) error

    // 执行入口：以下方法在热路径调用，panic 必须隔离。
    RunAccept(Session) error
    RunMessage(Session, []byte) ([]byte, error)
    RunClose(Session)
}

// 特殊控制错误（非业务错误，控制插件链行为）
var (
    ErrPluginDrop  = errors.New("shark: plugin drop message")
    ErrPluginBlock = errors.New("shark: plugin block session")
)
```

**PluginRunner 执行规则：**

```
RunAccept（按 Priority 升序）：
  → ErrPluginBlock：中断 + Close(sess) + 记录 metrics
  → 普通 error + stopOnError=true：中断 + Close(sess)
  → 普通 error + stopOnError=false：记录日志 + 继续
  → panic：recover + 记录 error + 继续或中断（可配置）

RunMessage（按 Priority 升序，支持 payload 改写）：
  data = originalPayload
  for each plugin:
    out, err = plugin.OnMessage(sess, data)
    → ErrPluginDrop：停止链，不调用 Handler，返回 nil
    → ErrPluginBlock：停止链，Close(sess)，返回 error
    → 普通 error：按策略处理
    → nil：data = out（允许 plugin 改写 payload）
  return data, nil

RunClose（按 Priority 逆序，不可中断）：
  for each plugin（逆序）:
    defer-style：即使 panic 也继续执行后续插件
    plugin.OnClose(sess)
```

### 4.8 Codec 接口（codec.go）

```go
// Codec[M] 在 []byte 与业务类型 M 之间转换，定义在 core 层作为稳定契约。
type Codec[M any] interface {
    Encode(M) ([]byte, error)
    Decode([]byte) (M, error)
    ContentType() string // "application/json" / "application/protobuf" 等
}

// Handler 函数类型（原始字节层）
type Handler func(Session, Message) error

// TypedHandler 函数类型（类型化层，通过 AdaptTyped 适配为 Handler）
type TypedHandler[M any] func(Session, M) error

// AdaptTyped 将 TypedHandler[M] 包装为 Handler，解码失败返回 ErrDecodeFailure。
func AdaptTyped[M any](h TypedHandler[M], codec Codec[M]) Handler {
    return func(s Session, msg Message) error {
        typed, err := codec.Decode(msg.Payload)
        if err != nil {
            return fmt.Errorf("%w: %v", ErrDecodeFailure, err)
        }
        return h(s, typed)
    }
}

// 内置 Codec 实现
type RawCodec struct{}   // []byte 透传，无转换开销
type JSONCodec[M any] struct{} // encoding/json
```

### 4.9 错误体系（errors.go）

**完整错误变量：**

```go
// 会话错误
var (
    ErrSessionNotFound  = errors.New("shark: session not found")
    ErrSessionClosed    = errors.New("shark: session closed")
    ErrSessionCapacity  = errors.New("shark: session manager at capacity")
)

// 消息错误
var (
    ErrMessageTooLarge  = errors.New("shark: message too large")
    ErrFrameTooLarge    = errors.New("shark: frame too large")
    ErrInvalidFrame     = errors.New("shark: invalid frame")
    ErrWriteQueueFull   = errors.New("shark: write queue full")
    ErrInvalidMessage   = errors.New("shark: invalid message")
)

// 编解码错误
var (
    ErrEncodeFailure    = errors.New("shark: encode failure")
    ErrDecodeFailure    = errors.New("shark: decode failure")
)

// 超时错误
var (
    ErrReadTimeout      = errors.New("shark: read timeout")
    ErrWriteTimeout     = errors.New("shark: write timeout")
    ErrIdleTimeout      = errors.New("shark: idle timeout")
    ErrHeartbeatTimeout = errors.New("shark: heartbeat timeout")
    ErrDrainTimeout     = errors.New("shark: drain timeout")
)

// 服务错误
var (
    ErrServerClosed     = errors.New("shark: server closed")
    ErrServerNotStarted = errors.New("shark: server not started")
    ErrListenFailed     = errors.New("shark: listen failed")
)

// 插件控制（非业务错误）
var (
    ErrPluginDrop       = errors.New("shark: plugin drop message")
    ErrPluginBlock      = errors.New("shark: plugin block session")
    ErrPluginDuplicate  = errors.New("shark: plugin duplicate name")
)

// 安全错误
var (
    ErrRateLimited      = errors.New("shark: rate limited")
    ErrBlacklisted      = errors.New("shark: ip blacklisted")
    ErrAutoBanned       = errors.New("shark: auto banned")
)

// 协议错误
var (
    ErrCoAPInvalidMessage   = errors.New("shark: coap invalid message")
    ErrGRPCWebMalformedFrame = errors.New("shark: grpc-web malformed frame")
)

// 网关错误
var (
    ErrNoServerRegistered   = errors.New("shark: no server registered")
    ErrDuplicateProtocol    = errors.New("shark: duplicate protocol")
    ErrGatewayNotStarted    = errors.New("shark: gateway not started")
)

// 基础设施错误
var (
    ErrCacheMiss        = errors.New("shark: cache miss")
    ErrStoreNotFound    = errors.New("shark: store key not found")
    ErrCircuitOpen      = errors.New("shark: circuit breaker open")
    ErrPubSubClosed     = errors.New("shark: pubsub closed")
)

// 配置错误
var (
    ErrInvalidConfig    = errors.New("shark: invalid configuration")
)
```

**分类判断函数：**

```go
// IsRetryable：调用方可安全重试
func IsRetryable(err error) bool {
    return errors.Is(err, ErrWriteQueueFull) ||
           errors.Is(err, ErrCircuitOpen) ||
           errors.Is(err, ErrRateLimited)
}

// IsFatal：连接或服务不可继续
func IsFatal(err error) bool {
    return errors.Is(err, ErrSessionClosed) ||
           errors.Is(err, ErrServerClosed) ||
           errors.Is(err, ErrFrameTooLarge)
}

// IsSecurityRejection：安全策略拒绝
func IsSecurityRejection(err error) bool {
    return errors.Is(err, ErrBlacklisted) ||
           errors.Is(err, ErrAutoBanned) ||
           errors.Is(err, ErrRateLimited)
}

// IsPluginControl：插件控制流（非业务错误）
func IsPluginControl(err error) bool {
    return errors.Is(err, ErrPluginDrop) ||
           errors.Is(err, ErrPluginBlock)
}

// IsTransient：临时故障，可降级处理
func IsTransient(err error) bool {
    return errors.Is(err, ErrCircuitOpen) ||
           errors.Is(err, ErrCacheMiss) ||
           errors.Is(err, ErrPubSubClosed)
}
```

### 4.10 可观测接口（observability.go）

```go
// Logger 结构化日志接口。
type Logger interface {
    Debug(msg string, args ...any)
    Info(msg string, args ...any)
    Warn(msg string, args ...any)
    Error(msg string, args ...any)
    With(args ...any) Logger
    WithContext(ctx context.Context) Logger // 提取 trace_id / request_id
}

// Metrics 指标抽象。
type Metrics interface {
    Counter(name string, labels ...string) Counter
    Gauge(name string, labels ...string) Gauge
    Histogram(name string, labels ...string) Histogram
}

// Tracer 追踪抽象（兼容 OpenTelemetry）。
type Tracer interface {
    Start(ctx context.Context, name string) (context.Context, Span)
}

type Span interface {
    End()
    RecordError(err error)
    SetAttribute(key string, val any)
}
```

**日志关键字段规范：**

```
session_id, protocol, remote_addr, local_addr,
plugin_name, error, duration_ms, trace_id, request_id,
msg_size, queue_depth, state, reason
```

**日志级别语义：**

| 级别 | 场景 |
| --- | --- |
| Debug | 帧解析细节、插件执行过程（生产关闭） |
| Info | 连接建立/断开、Server 启动/停止 |
| Warn | 限流触发、LRU 淘汰、重传、降级、Drain 超时 |
| Error | 异常断连、panic recover、基础设施错误 |

---

## 5. 运行时层（internal/runtime/）

### 5.1 Gateway（gateway.go）

**职责：**

1. 注册多个协议 Server，拒绝重复协议注册。
2. 在启动前向支持 `RuntimeConfigurable` 的协议注入 Runtime。
3. 顺序启动协议，任一失败时逆序回滚已启动协议。
4. 暴露 `Ready()` 状态和健康快照。
5. 按阶段停止所有协议（StagedServer 三阶段 + 非 StagedServer 直接 Stop）。
6. 最后调用 `Runtime.Sessions().CloseAll` 清理残留会话。

**启动流程：**

```
Gateway.Start(ctx)
  → 检查 servers 非空（ErrNoServerRegistered）
  → 向 RuntimeConfigurable server 注入 Runtime
  → 按注册顺序启动 servers
    → 若某 server.Start() 失败：
        逆序停止已启动的 server
        ready = false
        return 聚合错误
  → startedAt = now
  → ready = true
  → return nil
```

**停止流程（5 阶段）：**

```
Gateway.Stop(ctx)
  → 阶段1 StopAccept：StagedServer.StopAccept（停止新连接入口）
  → 阶段2 Drain：StagedServer.Drain（等待读写 goroutine 收敛）
  → 阶段3 CloseSessions：StagedServer.CloseSessions（关闭协议会话）
  → 阶段4 StopNonStaged：非 StagedServer 的 Stop（HTTP Mode A 等）
  → 阶段5 CloseAll：Runtime.Sessions().CloseAll（清理 Manager 残留会话）
  → ready = false

各阶段并发执行（所有相同阶段的 server 并发执行，阶段间串行）
各阶段使用独立的 context，支持分阶段超时配置
```

**生命周期不变量：**

- `Start` 失败必须回滚，不留半启动状态。
- `Stop` 必须可重复调用（幂等）。
- `Start → Stop → Start` 保持可用（Manager 不永久关闭）。
- `Ready()` 只表示 Gateway 成功启动，不代表外部依赖健康。
- 协议停止不能永久关闭共享 SessionManager。
- `CloseAll` 执行前必须确保所有 StopAccept 完成（防止新 session 逃脱清理）。

**CloseAll 并发安全保证：**

```
阶段顺序保证了 CloseAll 的安全性：
  StopAccept 完成后：所有新连接入口已关闭，不会产生新 session
  Drain 完成后：所有 readLoop goroutine 已退出，不会注册新 session
  CloseSessions 完成后：协议层 session 已关闭
  CloseAll：清理 Manager 中可能遗留的 session（应为空或极少数）
```

### 5.2 SessionManager 实现（session_manager.go）

**P0 实现（功能正确优先）：**

```go
type manager struct {
    mu       sync.RWMutex
    sessions map[uint64]Session
    idGen    atomic.Uint64
    total    atomic.Int64
    maxCount int64           // 0 = 不限制
}
```

**P2 演进（benchmark 证明锁竞争为瓶颈后引入）：**

```go
type shardedManager struct {
    shards  [32]shard       // 分片锁
    idGen   atomic.Uint64
    total   atomic.Int64
    maxCount int64
}

type shard struct {
    mu       sync.RWMutex
    sessions map[uint64]Session
    lru      *LRUList        // per-shard LRU，超容时淘汰最旧会话
}

// 分片函数：位运算，比取模快
func shardIndex(id uint64) int { return int(id & 31) }
```

**Broadcast 实现（快照发送，不持锁）：**

```go
func (m *manager) Broadcast(data []byte) error {
    // 1. 快照：持锁收集所有 session 引用
    m.mu.RLock()
    snapshot := make([]Session, 0, len(m.sessions))
    for _, s := range m.sessions { snapshot = append(snapshot, s) }
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

### 5.3 PluginRunner 实现（plugin_runner.go）

```go
type runner struct {
    plugins   []Plugin        // 按 Priority 升序静态排序（启动时排序，热路径直接索引）
    nameIndex map[string]int  // 去重索引
    stopOnError bool          // 普通 error 是否中断链
    logger    Logger
    metrics   Metrics
}
```

**注册阶段（Server 启动时，一次性操作）：**

1. 按 `Priority()` 升序排列（`slices.SortFunc`，Go 1.26 标准库）。
2. 同名插件后注册覆盖，记录 Warn 日志。
3. 预构建 `[]Plugin` 切片，热路径直接索引，无排序开销。

**panic 隔离：**

```go
func safeRunPlugin(name string, fn func()) (panicked bool) {
    defer func() {
        if r := recover(); r != nil {
            panicked = true
            // 记录 panic 堆栈 + plugin_name 指标
        }
    }()
    fn()
    return false
}
```

### 5.4 WorkerPool（worker_pool.go）

**四种队列满策略：**

| 策略 | 行为 | 适用场景 |
| --- | --- | --- |
| PolicyDrop | 丢弃消息 + metrics（默认） | 通用场景，防雪崩 |
| PolicyBlock | 阻塞等待队列空间 | 不可丢消息场景（金融） |
| PolicySpawnTemp | 动态扩容临时 Worker | 突发流量缓冲 |
| PolicyClose | 持续过载 30s 后关闭连接 | 极端情况保护 |

**SpawnTemp 约束：**

```
core workers + temp workers <= MaxWorkers（严格约束，非各自独立计数）
temp worker 处理完当前任务后自动退出（不归还到 core pool）
atomic.Int32 tempCount 计数，SpawnTemp 前检查 total < MaxWorkers
```

**安全保证：**

- 每个 worker 执行 `safeRun`，内置 `recover(panic)`。
- worker panic 后记录指标 `shark_worker_panics_total`，worker 继续运行。

---

## 6. 传输协议层（internal/transport/）

### 6.1 TCP 协议（transport/tcp/）

**Framer 接口（framer.go）：**

```go
type Framer interface {
    // ReadFrame 从 r 读取一个完整帧，返回 payload。
    // 实现必须无状态（不跨调用保持预读缓冲），避免丢帧。
    ReadFrame(r io.Reader) ([]byte, error)

    // WriteFrame 将 payload 写入 w，必须保证完整写入（短写重试）。
    WriteFrame(w io.Writer, payload []byte) error
}
```

**4 种内置 Framer：**

| Framer | 实现方式 | 适用场景 |
| --- | --- | --- |
| LengthPrefixFramer | 4 字节大端长度前缀，`io.ReadFull` 读头和 payload | 默认推荐，二进制协议 |
| LineFramer | 逐字节读到 `\n`，无 bufio 跨调用预读 | 文本行协议 |
| FixedSizeFramer | 固定长度帧，`io.ReadFull` | 硬件协议、传感器数据 |
| RawFramer | 直接透传，单次 `Read` | 简单场景，自行处理粘包 |

**注意**：`LengthPrefixFramer` 和 `LineFramer` 基于 `io.ReadFull` 或逐字节读，保持无状态接口语义，避免 `bufio.Reader` 跨调用预读丢弃同一 TCP 流中的后续帧。

**TCPSession 关闭 6 步状态机（session.go）：**

```
步骤1：CAS Active → Draining（失败说明已在关闭或已关闭，直接返回）
步骤2：若 writeLoop 已启动：close(draining) 信号触发排空
步骤3：等待 writeQueue 排空（DrainTimeout 超时后强制继续，记录 ErrDrainTimeout）
步骤4：CAS Draining → Closed
步骤5：CancelContext() → 通知所有 <-ctx.Done() 的 goroutine 退出
步骤6：conn.Close()

若 writeLoop 未启动（连接在 Accept 后 Close 被调用）：
  步骤2-3 跳过，直接释放待写 Buffer 后执行步骤4-6
```

**Send 并发安全：**

```go
func (s *TCPSession) Send(data []byte) error {
    if !s.IsAlive() {
        return ErrSessionClosed
    }
    // 复制调用方 data，防止 writeQueue 中的 buffer 被调用方复用覆盖
    buf := make([]byte, len(data))
    copy(buf, data)

    select {
    case s.writeQueue <- buf:
        return nil
    default:
        return ErrWriteQueueFull // 非阻塞，立即返回
    }
}
```

**TCPServer 完整配置（options.go）：**

| 配置项 | 默认值 | 说明 |
| --- | --- | --- |
| Addr | `"0.0.0.0:18000"` | 监听地址 |
| WorkerCount | `NumCPU×2` | 核心 Worker 数量 |
| MaxWorkers | `WorkerCount×4` | 最大 Worker（含临时扩容） |
| TaskQueueSize | `WorkerCount×128` | 任务队列容量 |
| QueueFullPolicy | `PolicyDrop` | 队列满策略 |
| WriteQueueSize | `128` | 每连接写队列容量 |
| WriteFullPolicy | `PolicyBlock` | 写队列满策略 |
| MaxSessions | `100,000` | 最大会话数（0=不限制） |
| MaxMessageSize | `1MB` | 单消息最大字节 |
| ReadTimeout | `0`（不限） | 读超时 |
| WriteTimeout | `10s` | 写超时 |
| IdleTimeout | `0`（不限） | 空闲超时 |
| HandlerTimeout | `0`（不限） | Handler 执行超时 |
| DrainTimeout | `5s` | 关闭时写队列 drain 超时 |
| ShutdownTimeout | `10s` | 服务关闭超时 |
| Framer | `LengthPrefixFramer` | 帧解析器 |
| MaxConsecutiveErrors | `100` | 连续错误上限（超限断连） |
| TLSConfig | `nil` | TLS 配置（nil=不启用） |

### 6.2 UDP 协议（transport/udp/）

**伪会话模型：**

```
remote UDPAddr → 伪会话 → 共享 SessionManager

伪会话创建时机（推荐方案B）：
  1. 收到 datagram，提取 remote UDPAddr
  2. 构造轻量临时 session（仅含必要字段）
  3. RunAccept(tempSess)：
     → ErrPluginBlock：丢弃 datagram，不注册 session（不留计数）
     → nil：Register(sess) → 正式伪会话
  4. 后续 datagram 复用已注册的伪会话

好处：
  Manager 计数准确（Block 的连接不进入计数）
  与 HTTP Mode B 临时 session 模式一致
```

**ErrPluginDrop 语义：**

- 只丢弃当前 datagram，不删除伪会话，连接可继续接收后续 datagram。
- 与 ErrPluginBlock 的区别：Block 关闭伪会话，Drop 只丢弃一条消息。

**sweepLoop：**

```
每 sweepInterval（默认 30s）遍历所有伪会话：
  LastActiveAt + sessionTTL < now → Close(sess) + Unregister
  TTL 默认 60s（可配置）
```

**UDP 发送：** 直接 `WriteToUDP`，无写队列（UDP 无序，无需串行化）。

### 6.3 HTTP 协议（transport/http/）

**两种模式：**

| 模式 | 描述 | Session | Plugin |
| --- | --- | --- | --- |
| Mode A（默认） | 纯 `net/http` router | 无 | 无 |
| Mode B（可选） | per-request 临时 Session | HTTPSession | 执行完整插件链 |

**Mode B 流程：**

```
MaxBytesReader 限制请求体大小（超限 → 413）
→ 创建 HTTPSession
→ Register(sess)
→ defer Unregister(sess.ID())
→ defer RunClose(sess)      ← 所有退出路径都触发 OnClose
→ RunAccept(sess)           → ErrPluginBlock → 403 + return
→ 读 Body
→ RunMessage(sess, body)    → ErrPluginDrop → 200 空响应
→ Handler(sess, msg)        → error → 500
→ （响应已在 Handler 中写入）
```

**注意**：`HTTPSession` 不注册到共享 `SessionManager`（per-request 语义，无持久会话）。

**HTTPSession.Send：** `w.Write(data)`，`Close` 完成响应（`WriteHeader + Flush`）。

### 6.4 WebSocket 协议（transport/websocket/）

**并发安全：**

`gorilla/websocket` 写操作非并发安全，所有写操作（Send / SendText / sendPing）必须加 `writeMu sync.Mutex`。

**OnClose 单次执行保证：**

```
OnClose 在 Close 内部通过 sync.Once 调用：
  closeOnce.Do(func() {
      RunClose(sess)          ← 在 Once 内部，天然保证单次执行
      manager.Unregister(id)
      cancelContext()
      conn.Close()
  })

两条并发退出路径（Gateway shutdown + read EOF）都通过 sess.Close() 触发，
sync.Once 保证 OnClose 只执行一次。
```

**ReadDeadline 动态设置：**

```
ReadDeadline = now + PingInterval + PongTimeout
PongHandler 收到 Pong 后：
  TouchActive()
  重设 ReadDeadline
```

**关键配置（options.go）：**

| 配置项 | 默认值 |
| --- | --- |
| PingInterval | `30s` |
| PongTimeout | `10s` |
| MaxMessageSize | `1MB` |
| AllowedOrigins | `[]`（空列表=拒绝所有跨域，生产必须配置） |
| UpgradeTimeout | `5s` |

### 6.5 CoAP 协议（transport/coap/）

**CoAP 帧结构（message.go）：**

```go
type Message struct {
    Version   uint8       // 必须为 1
    Type      MsgType     // CON(0) / NON(1) / ACK(2) / RST(3)
    TokenLen  uint8       // TKL <= 8
    Code      Code        // 方法码或响应码
    MessageID uint16      // 消息去重 ID
    Token     []byte      // 关联请求/响应（len == TokenLen）
    Options   []Option    // Uri-Path / Content-Format 等
    Payload   []byte
}

帧校验：
  len(raw) >= 4
  TKL <= 8
  Version == 1
  Token 实际长度与 TKL 一致
```

**CON 可靠性（RFC 7252 §4.2）：**

```
收到 CON：
  CheckAndRecord(msgID) 去重：
    → 重复：GetCachedResponse → 重发缓存 ACK（避免重复执行 Handler）
    → 首次：RunMessage → Handler → 选择 ACK Code → Send ACK → CacheResponse(msgID, ack)

收到 RST：
  ResetCON(msgID) 取消对应重传

ACK Code 选择：
  GET     → 2.05 Content
  POST    → 2.01 Created
  PUT     → 2.04 Changed
  DELETE  → 2.02 Deleted
  其他    → 2.05 Content

Message ID 去重：最近 500 条 LRU 缓存（可配置）
```

**当前边界：** CON 基础 ACK 和去重已实现；Block-wise、Observe、完整 option 编解码和重传状态机为后续目标（P1 协议增强）。

**CoAP retransmitLoop 活跃索引（ADR 修复的死锁问题）：**

```
错误方案：retransmitLoop 持 pendingACKs 锁遍历，超时删除也需同锁 → 死锁
正确方案：维护独立 activeIDs []uint16 索引，retransmitLoop 只读 activeIDs，
         删除时先释放遍历锁再获取写锁（或使用独立 cleanup channel）
```

### 6.6 LwM2M 协议（internal/protocol/lwm2m/）

**注意**：LwM2M 位于 `internal/protocol/`（应用协议层），不是 `internal/transport/`（传输层）。它基于 CoAP transport 提供设备管理语义。

**Server 职责：**

- 维护 endpoint 注册表（线程安全）。
- 支持 register / update / deregister 生命周期。
- 管理 object/resource path（`/OID/IID/RID`）。
- 支持 resource read / write / execute。
- lifetime expiry sweep（每 `lifetimeCheckInterval` 扫描过期注册）。

**注册状态不变量（防浅拷贝污染）：**

```go
// Registration() 和 Registrations() 返回深拷贝，调用方修改不影响内部状态
func (s *Server) Registration(endpoint string) (Registration, bool)
func (s *Server) Registrations() []Registration

// WithClientObjects 保留调用方 attributes map 的深拷贝
// Client.AddResource 保留调用方内容切片的深拷贝
```

**当前 CoAP 文本命令绑定（responder.go）：**

```
register <endpoint> <lifetime-seconds> [object-path...]
update <endpoint> <lifetime-seconds>
deregister <endpoint>
write <endpoint> <resource-path> <value>
read <endpoint> <resource-path>
```

**后续目标（P1）：** 正式 LwM2M URI/Query/Content-Format 编解码、Observe/Notify、Bootstrap。

### 6.7 QUIC 协议（transport/quic/）

**强制约束：** 没有 TLS config 不允许启动（`Start` 返回配置错误）。

**Stream 生命周期：**

```
连接建立 → 注册 QUICSession（连接维度）
Stream 到达 → handleStream：
  读取 stream payload（MaxMessageSize 限制）
  超限：不调用 Handler，关闭 stream，记录 ErrMessageTooLarge
  正常：RunMessage → Handler（Handler 可写回 stream）
  Stream 关闭 → 不注销 QUICSession（连接仍存在）
连接断开 → 注销 QUICSession，RunClose

stream 尾块处理：
  Read 可能同时返回 n>0 和 io.EOF（尾块场景）
  必须先处理 n>0 的 payload，再处理 err==io.EOF
```

**QUICSession 写队列 payload 切片别名问题：**

```
Send(data []byte) 必须复制 data，防止异步 writeLoop 发送时 data 被调用方复用覆盖：
  buf := make([]byte, len(data))
  copy(buf, data)
  select { case s.writeQueue <- buf: ... }
```

**Stop 幂等：** 未 Start 时调用 Stop 应安全返回 nil，不触发 manager/listener 空指针。

### 6.8 gRPC-Web 协议（transport/grpcweb/）

**两种入口模式：**

| 模式 | 入口 | Session |
| --- | --- | --- |
| Direct HTTP | HTTP POST | per-request 临时 Session |
| WebSocket | WS 长连接 | 持久 Session |

**MaxMessageBytes 约束：**

```
Direct 模式：超限 → 413（请求体截断返回）
WebSocket 模式：超限 → 在进入 Handler 前关闭连接，记录错误
配置值为负数或超出合理范围 → 拒绝启动（app 层统一验证）
```

**OnClose 清理完整性：**

```
Direct 模式：所有返回路径（正常/超限/插件错误/Handler 错误）必须触发 OnClose
  使用 defer 保证：
    defer func() {
        RunClose(sess)
        Unregister(sess.ID())
    }()

WebSocket 模式：
  读循环退出后：Unregister → sess.Close()（触发 OnClose via sync.Once）
```

**gRPC-Web 帧格式（frame.go）：**

```
Data frame:    [0x00][4字节大端长度][payload]
Trailer frame: [0x80][4字节大端长度][trailer 键值对]
畸形帧：严格返回 400，不静默跳过
```

---

## 7. 插件层（internal/plugin/）

### 7.1 内置插件与推荐 Priority

| Priority | 插件 | 职责 |
| --- | --- | --- |
| 0 | BlacklistPlugin | IP/CIDR 黑名单（最高优先级，最早拦截） |
| 10 | RateLimitPlugin | 令牌桶限流（连接 + 消息双层） |
| 20 | AutoBanPlugin | 自动封禁（触发限流/错误阈值 → 加入黑名单） |
| 30 | HeartbeatPlugin | 心跳超时检测（P0：ticker 扫描；P2：时间轮） |
| 40 | ClusterPlugin | 集群事件广播 + 跨节点路由 |
| 50 | PersistencePlugin | 会话状态异步持久化 |
| 60 | SlowHandlerPlugin | 记录慢处理调用（Handler 耗时超阈值） |

### 7.2 BlacklistPlugin（Priority=0）

```
存储结构：
  exactMap  map[string]time.Time  // 精确 IP → 过期时间，O(1) 查找
  cidrList  []net.IPNet           // CIDR 段，顺序遍历
  cache     Cache                 // 分布式黑名单缓存（Cache Miss 降级本地查找）

OnAccept：提取纯 IP → exactMap O(1) → CIDR 遍历 → Cache 双查
动态管理：Add(ip, ttl) / Remove(ip) / Reload(list)
TTL 过期：惰性过期（查询时检查）+ 后台 cleanupLoop（每分钟）
```

### 7.3 RateLimitPlugin（Priority=10）

```
双层令牌桶：
  globalBucket  *tokenBucket              // 全局速率上限
  perIPBuckets  sync.Map                  // per-IP 独立桶（CompareAndDelete 清理，Go 1.26）

算法：按实际时间差补充令牌 → 检查 >= 1 → 原子扣减
OnAccept：连接速率限流 → ErrPluginBlock（超限返回 ErrRateLimited）
OnMessage：消息速率限流 → ErrPluginDrop（超限丢弃消息）
后台：cleanupLoop 每 2 分钟清理空闲 IP 桶（使用 sync.Map.CompareAndDelete）
连续触发 N 次 → 通知 AutoBanPlugin（通过共享计数器或 channel）
```

### 7.4 HeartbeatPlugin（Priority=30）

**P0 实现（ticker 扫描）：**

```go
// 后台 goroutine 每 checkInterval 扫描 SessionManager
// LastActiveAt + idleTimeout < now → sess.Close()
// 开销：O(N) 每次扫描，10万连接时约 1ms（可接受）
```

**P2 演进（时间轮，benchmark 证明扫描成为瓶颈后引入）：**

```
TimeWheel：单 goroutine 管理全部会话定时器
10万连接仅需 1 个系统 goroutine（替代 N 个 ticker）
精度：1 秒（slot 间隔），轮大小 = ceil(timeout/slot)
Add/Remove/Reset 均 O(1)
OnMessage 触发 timeWheel.Reset(sess.ID())
```

### 7.5 PersistencePlugin（Priority=50）

```
OnAccept：Store.Load(sess.ID()) → sess.SetMeta("history", data)
OnMessage：序列化 → writeCh（有界 channel，容量 1024）
         → 后台 batchWriter（每 100 条或 500ms flush）
OnClose：同步 Store.Save（最终快照）

CircuitBreaker 包裹 Store 调用，Store 不可用时跳过（记录 Warn）
幂等保护：Close() 通过 sync.Once 确保最终快照只写一次
```

### 7.6 ClusterPlugin（Priority=40）

```
OnAccept：
  Cache.Set("session:route:"+sessID, nodeID, sessionTTL)
  PubSub.Publish("cluster.session.joined", {sessID, nodeID})

OnClose：
  Cache.Del("session:route:"+sessID)
  PubSub.Publish("cluster.session.left", {sessID})

跨节点路由（业务层调用）：
  本地 Manager.Get(targetID) → 未找到
  → Cache.Get("session:route:"+targetID) → nodeID
  → PubSub.Publish("node."+nodeID+".route", {targetID, payload})

节点心跳：每 heartbeatTTL/2 执行 Cache.Set("node:"+nodeID, meta, heartbeatTTL)
CircuitBreaker 包裹 PubSub 和 Cache 调用（不可用时静默丢弃）
```

---

## 8. 应用装配层（internal/application/）

### 8.1 配置结构（config.go）

**加载顺序：** 默认值 → JSON 配置文件 → 环境变量覆盖（优先级从低到高）。

**顶层配置：**

| 字段 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `shutdown_timeout` | duration | `15s` | 优雅关闭总超时 |
| `health_addr` | string | `":18081"` | 健康检查 HTTP 地址 |
| `metrics_addr` | string | `":18080"` | Prometheus metrics 地址 |
| `max_sessions` | int | `100000` | SessionManager 全局上限（0=不限制） |
| `protocols` | []ProtocolConfig | - | 协议监听列表 |

**协议配置：**

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `name` | string | `tcp/udp/http/websocket/coap/quic/grpc-web` |
| `enabled` | bool | 默认 true |
| `addr` | string | 监听地址（host:port） |
| `path` | string | WebSocket 或 gRPC-Web WebSocket path |
| `mode` | string | CoAP 使用 `lwm2m` 时接入 LwM2M responder |
| `max_message_bytes` | int | 消息大小限制（必须 >= 0） |
| `tls_cert_file` | string | 服务端证书路径 |
| `tls_key_file` | string | 服务端私钥路径 |
| `tls_client_ca_file` | string | mTLS 客户端 CA bundle |
| `tls_client_auth` | string | 客户端证书校验策略 |

**统一配置验证规则（app 层统一执行，不在 Start() 中执行）：**

```
必须在 app.Build() 时验证：
  max_message_bytes >= 0
  addr 格式合法（host:port，空 host 允许）
  tls_cert_file 和 tls_key_file 必须同时存在或同时缺失
  shutdown_timeout > 0
  protocol name 在已知列表中
  enabled=true 的协议 addr 非空
  max_sessions >= 0

验证失败返回 ErrInvalidConfig，包含具体字段和原因
```

### 8.2 App 装配（app.go）

```
App.Build(config) 流程：
  1. 验证配置（统一，单次）
  2. 创建基础设施：Logger / Metrics / Tracer
  3. 创建 SessionManager
  4. 创建 PluginRunner + 注册插件
  5. 创建 DefaultRuntime（组装1-4）
  6. 按配置创建各协议 Server（传入 Options）
  7. 创建 Gateway（注册 Server + Runtime）
  8. 创建健康 / 指标 HTTP 服务
  9. return App（持有 Gateway + 健康服务引用）

App.Run(ctx) 流程：
  1. 启动健康 / 指标服务
  2. gateway.Start(ctx)
  3. 阻塞等待 ctx.Done()（信号处理在 cmd/server/main.go）
  4. gateway.Stop(shutdownCtx)
  5. 关闭健康 / 指标服务
```

---

## 9. 可观测设计

### 9.1 Prometheus 指标（静态预注册）

**静态预注册原因：** 防止运行时动态 label 导致 Prometheus cardinality 爆炸，所有指标在启动时一次性注册。

**锁定的指标集合（P1 必须完成）：**

```
连接与会话：
  shark_sessions_total{protocol}               Counter  连接建立总数
  shark_sessions_active{protocol}              Gauge    当前活跃会话数
  shark_session_errors_total{protocol,reason}  Counter  连接错误总数
  shark_session_rejected_total{protocol,reason} Counter 被拒绝连接总数

消息：
  shark_messages_total{protocol,direction}     Counter  消息总数（in/out）
  shark_message_bytes_total{protocol,direction} Counter 消息字节总数
  shark_dropped_messages_total{protocol,reason} Counter 丢弃消息总数

Handler 与插件：
  shark_handler_duration_seconds{protocol}     Histogram Handler 执行耗时
  shark_plugin_duration_seconds{plugin}        Histogram 插件执行耗时
  shark_plugin_errors_total{plugin,error_type} Counter   插件错误总数
  shark_plugin_panics_total{plugin}            Counter   插件 panic 总数

传输层：
  shark_transport_errors_total{protocol,type}  Counter  传输错误总数
  shark_write_queue_full_total{protocol}        Counter  写队列满次数
  shark_worker_panics_total{protocol}           Counter  Worker panic 总数

资源（P2 引入 BufferPool 后补充）：
  shark_bufferpool_hits_total{level}            Counter  各级 pool 命中次数
  shark_bufferpool_misses_total{level}          Counter  各级 pool 未命中次数

网关：
  shark_gateway_start_duration_seconds         Histogram 启动耗时
  shark_gateway_stop_duration_seconds          Histogram 停止耗时

指标标签约束（防 cardinality 爆炸）：
  protocol：固定 8 个值（tcp/udp/http/websocket/coap/quic/grpc-web/custom）
  direction：固定 2 个值（in/out）
  reason：固定枚举（blacklisted/rate_limited/capacity/malformed/...）
  level：固定 5 个值（micro/tiny/small/medium/large）
  error_type：固定枚举（timeout/closed/frame_too_large/...）
```

### 9.2 健康检查端点

```
GET /healthz → 进程存活（200 = 存活）
响应体（P1 扩展）：
{
  "status": "healthy | degraded",
  "uptime": "2h30m15s",
  "protocols": {"tcp": true, "websocket": true, "coap": false},
  "sessions": 12345
}

GET /readyz → Gateway Ready 状态
响应体：
  200: {"status": "ready"}
  503: {"status": "not_ready", "reason": "gateway not started | gateway stopping"}

GET /metrics → Prometheus text format（包含全部 shark_* 指标）
```

### 9.3 Tracing

```
context.Context 贯穿全链路：
  Accept → RunAccept → RunMessage → Handler → sess.Send

Span 注入点（兼容 OpenTelemetry）：
  transport.accept     → 连接建立
  plugin.on_accept     → 插件链 accept
  plugin.on_message    → 插件链消息处理（per plugin）
  handler.execute      → Handler 执行
  session.send         → 写队列投递

当前实现：ctx 携带 request_id（UUID 格式）/ trace_id
后续扩展：用户注入 OTel TracerProvider，框架不引入强依赖
```

---

## 10. 基础设施层（internal/infrastructure/）

### 10.1 Cache（cache/cache.go）

```go
type Cache interface {
    Get(ctx context.Context, key string) ([]byte, error)  // Miss → ErrCacheMiss
    Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
    Del(ctx context.Context, key string) error
    Has(ctx context.Context, key string) (bool, error)
    TTL(ctx context.Context, key string) (time.Duration, error)
    MGet(ctx context.Context, keys []string) (map[string][]byte, error)
    Sweep() int  // 清理过期条目，返回清理数量
    Len() int
    Clear()
}
```

**MemoryCache 实现要求：**

- `Set` 写入时深拷贝调用方 `[]byte`，防止外部切片复用污染缓存。
- `Get/MGet` 返回副本，防止调用方修改影响缓存内容。
- TTL 惰性过期（读取时检查），后台 `cleanupLoop` 定期主动清理。

### 10.2 Store（store/store.go）

```go
type Store interface {
    Save(ctx context.Context, key string, val []byte) error
    Load(ctx context.Context, key string) ([]byte, error)  // 未找到 → ErrStoreNotFound
    Delete(ctx context.Context, key string) error
    Query(ctx context.Context, prefix string) ([][]byte, error)
    Keys(ctx context.Context, prefix string) ([]string, error)
}
```

### 10.3 PubSub（pubsub/pubsub.go）

```go
type PubSub interface {
    Publish(ctx context.Context, topic string, data []byte) error
    Subscribe(ctx context.Context, topic string, handler func([]byte)) (Subscription, error)
    Close() error
}

type Subscription interface {
    Unsubscribe() error
    Topic() string
}
```

**ChannelPubSub 并发安全：** 

`Publish` 发布前复制消息（防止并发读写）；`Unsubscribe` 和 `Publish` 之间通过 `sync.RWMutex` 协调，防止向已关闭 channel 发送导致 panic。

### 10.4 CircuitBreaker（circuitbreaker/circuitbreaker.go）

```
状态机：
  Closed   → 正常调用；连续失败 > threshold → Open
  Open     → 直接返回 ErrCircuitOpen；超过 timeout → HalfOpen
  HalfOpen → 允许一次探测；成功 → Closed；失败 → Open

配置：
  threshold int64        // 连续失败阈值（默认 5）
  timeout   time.Duration // 熔断恢复时间（默认 30s）

应用场景：PersistencePlugin(Store) / ClusterPlugin(PubSub/Cache)
```

### 10.5 Observability（observability/）

**slogLogger（logger.go）：**

- 基于 Go 1.21+ `log/slog`，默认 JSON 格式输出。
- `WithContext(ctx)` 自动提取 `trace_id` / `request_id`。
- `With(args...)` 创建子 logger，附加固定字段。
- 异步写入：带缓冲 channel（容量 4096）→ 单 goroutine 刷盘（channel 满时降级同步写）。

**PrometheusMetrics（metrics.go）：**

- 启动时静态注册所有 `shark_*` 指标。
- 热路径 atomic 计数，异步批量上报。
- 暴露标准 Prometheus HTTP handler。

**P2 引入的基础设施组件：**

| 组件 | 触发条件 | 位置 |
| --- | --- | --- |
| 六级 BufferPool | benchmark 证明 readLoop alloc 为瓶颈 | `infrastructure/bufferpool/` |
| 时间轮 | benchmark 证明心跳扫描为瓶颈 | `infrastructure/timewheel/` |
| 日志采样器 | 高频日志场景压测发现日志成为瓶颈 | `infrastructure/logsampler/` |

**六级 BufferPool 分级（P2）：**

| 级别 | 大小阈值 | 典型场景 |
| --- | --- | --- |
| Micro | <= 128B | CoAP/心跳控制帧、ACK 包 |
| Tiny | <= 512B | 短文本消息、命令帧 |
| Small | <= 4KB | 普通业务消息、JSON 请求 |
| Medium | <= 32KB | HTTP Body、批量数据 |
| Large | <= 256KB | 大消息、文件块 |
| Huge | > 256KB | 直接 make，不入池（防内存膨胀） |

---

## 11. Gateway 数据流

### 11.1 TCP 完整处理流

```
网络 → readLoop（每连接 1 goroutine）：
  io.ReadFull / 逐字节读 → Framer.ReadFrame(conn)
  → sess.TouchActive()（原子更新 lastActiveAt）
  → WorkerPool.Submit(sess, payload)

Worker goroutine（隔离 panic）：
  PluginRunner.RunMessage(sess, payload)
    → BlacklistPlugin(0) → RateLimitPlugin(10) → AutoBanPlugin(20)
    → HeartbeatPlugin(30) → ClusterPlugin(40) → PersistencePlugin(50)
    → ErrPluginDrop → 停止，不调用 Handler
    → ErrPluginBlock → 停止，Close(sess)
  → Handler(sess, Message) → 业务逻辑
    → sess.Send(response) → 非阻塞写入 writeQueue

writeLoop（每连接 1 goroutine，单 goroutine 独占写）：
  for data := range writeQueue：
    完整写入 conn（短写重试）→ 写完成
  writeQueue 关闭 → drain 完成 → goroutine 退出
```

### 11.2 连接生命周期

```
listener.Accept()
  → newTCPSession(conn)
  → manager.Register(sess)   → 超容：ErrSessionCapacity → 拒绝连接
  → RunAccept(sess)          → ErrPluginBlock：Close(sess) + return
  → go sess.readLoop()
  → go sess.writeLoop()

[运行中]
  readLoop → Framer → RunMessage → WorkerPool → Handler → sess.Send
  writeLoop → writeQueue → conn.Write

[断开 / 超时 / 错误]
  readLoop 退出时 defer：
    sess.Close(ctx)：
      CAS Active → Draining
      close(draining) → writeLoop drain
      等待 drain（DrainTimeout）
      CAS Draining → Closed
      CancelContext()
      conn.Close()
    RunClose(sess)（逆序，via sync.Once）
    manager.Unregister(sess.ID())
```

### 11.3 类型化消息处理流

```
网络帧 → []byte → core.Message
  ↓
AdaptTyped[M](handler, codec)（在 application 层装配）
  ↓
codec.Decode(payload) → M
  ↓
TypedHandler[M](sess, typedMsg) → 业务逻辑
  ↓
codec.Encode(response) → []byte
  ↓
sess.Send([]byte)
```

### 11.4 跨节点路由（集群模式）

```
节点1 业务 Handler：
  manager.Get(targetID) → 未找到（跨节点）
  → Cache.Get("session:route:"+targetID) → nodeID="node2"
  → PubSub.Publish("node.node2.route", {targetID, payload})

节点2 ClusterPlugin（后台订阅）：
  PubSub.Subscribe("node.node2.route", func(msg))
  → 解析 {targetID, payload}
  → localManager.Get(targetID) → found → sess.Send(payload)
  → not found → Warn（session 已迁移或下线）
```

---

## 12. 并发模型

```
┌──────────────────────────────────────────────────────────────────┐
│ cmd/server Main Goroutine                                         │
│ Run: app.Build → app.Run → signal.NotifyContext → Stop           │
├──────────────────────────────────────────────────────────────────┤
│ Protocol Acceptors（每协议 1 goroutine）                          │
│   TCP：acceptLoop（listener.Accept → go handleConn）             │
│   UDP：readLoop（单 goroutine 复用 UDPConn）                     │
│   WS：net/http Serve（内部 goroutine 池）                        │
│   CoAP：readLoop（基于 UDP）                                     │
│   QUIC：acceptLoop（quic.Listener.Accept）                       │
├──────────────────────────────────────────────────────────────────┤
│ WorkerPool（NumCPU×2 核心 Worker + 弹性临时 Worker）             │
│   for task := range taskQueue → safeRun(handler)                │
├──────────────────────────────────────────────────────────────────┤
│ Per-Session Goroutines：                                          │
│   TCP：readLoop + writeLoop（每连接 2 个）                       │
│   WS： readLoop + writeLoop + pingLoop（每连接 3 个）            │
│   UDP：无 per-session goroutine（复用 acceptor readLoop）        │
│   QUIC：per-stream goroutine（每 stream 1 个读 goroutine）       │
├──────────────────────────────────────────────────────────────────┤
│ 系统 Goroutine（固定数量，与连接数无关）：                        │
│   HeartbeatPlugin：ticker 扫描（P0）/ 时间轮（P2）              │
│   RateLimitPlugin：cleanupLoop（清理空闲 IP 桶）                 │
│   BlacklistPlugin：cleanupLoop（清理过期 IP）                    │
│   PersistencePlugin：batchWriter（批量刷盘）                     │
│   ClusterPlugin：订阅 goroutine + 节点心跳 goroutine            │
│   SessionManager：sweepLoop（UDP/CoAP 伪会话 TTL 清理）          │
│   MemoryCache：cleanupLoop                                       │
│   Metrics HTTP Server                                            │
│   Health HTTP Server                                             │
│   slogLogger：异步写入 goroutine                                 │
└──────────────────────────────────────────────────────────────────┘

规模估算：
  10K TCP 连接，16 Workers → ~20K+30 goroutines
  100K TCP 连接，32 Workers → ~200K+35 goroutines
```

---

## 13. 安全与防御设计

### 13.1 当前已具备（P0）

- WebSocket / gRPC-Web Origin Check（AllowedOrigins 白名单）。
- HTTP / gRPC-Web body / message size 限制。
- QUIC 强制 TLS。
- Docker / K8s 非 root 和最小权限。
- Blacklist / RateLimit / AutoBan 插件基础能力。
- MaxSessions 硬上限。

### 13.2 六层连接防御（P0-P2 逐步补齐）

```
L1（OS，部署时配置）：
  net.core.somaxconn = 65535
  net.ipv4.tcp_syncookies = 1
  fs.file-max = 1048576

L2（MaxSessions 硬上限）：
  count >= maxSessions → ErrSessionCapacity → 拒绝连接 + metrics

L3（RateLimitPlugin，P0）：
  双层令牌桶：全局 + per-IP
  超限连接：ErrPluginBlock，超限消息：ErrPluginDrop

L4（BlacklistPlugin，P0）：
  精确 IP O(1) + CIDR 遍历 + Cache 双查 + TTL 自动过期

L5（AutoBanPlugin，P0）：
  限流超 N 次 / 协议错误超 M 次 → 自动加入黑名单（banTTL 默认 30 分钟）

L6（慢连接防御，P1）：
  ReadTimeout + IdleTimeout（防 Slowloris）
  HeartbeatPlugin 超时断开
  MinDataRate（可选，最少每秒字节数）
```

### 13.3 消息层防御

```
大包攻击：
  TCP：MaxMessageSize（LengthPrefixFramer 检查帧长度，超限返回 ErrFrameTooLarge）
  WS：conn.SetReadLimit(MaxMessageSize)
  UDP：物理限制 65535B，实际约 1500B（MTU）
  QUIC：MaxMessageSize 限制 stream 读取

小包高频：
  消息速率令牌桶（RateLimitPlugin OnMessage）
  连续触发 → AutoBan

畸形帧：
  Framer decode recover(panic) + 连续错误计数 → MaxConsecutiveErrors 断连
  CoAP 帧严格校验（len/TKL/Version/Token）
  gRPC-Web 畸形帧返回 400

WorkerPool 背压（四种策略）：
  PolicyDrop（默认）/ PolicyBlock / PolicySpawnTemp / PolicyClose
```

### 13.4 P1 必须补齐的安全项

1. TLS/mTLS 配置文件化（TCP / QUIC）。
2. TCP / QUIC TLS 证书热加载（SIGHUP 信号 → 重新加载证书，不中断已有连接）。
3. QUIC 证书轮换和热加载。
4. 请求级 deadline 和 idle timeout 统一配置。
5. 敏感配置字段（证书路径、密钥内容）不进入日志和审查报告。

### 13.5 P2 防御体系

1. CoAP DTLS 或 OSCORE 方案设计。
2. OverloadProtector（水位检测 + 降级矩阵）。
3. BackPressure（写队列水位监控）。
4. FD 使用率监控（`/proc/self/fd` 计数）。

**优雅降级矩阵（P2）：**

| 触发条件 | 降级动作 |
| --- | --- |
| Sessions > HighWater | 拒绝新连接 + 加速 LRU 淘汰 |
| WorkerPool 使用率 > 90% | 消息丢弃（PolicyDrop） |
| WorkerPool 满载持续 30s | 主动关闭最慢连接（PolicyClose） |
| Memory > 80% | 跳过 PersistencePlugin + 拒绝新连接 |
| PubSub 不可用 | 集群事件静默丢弃（CircuitBreaker 保护） |
| Store 不可用 | PersistencePlugin 跳过（CircuitBreaker 熔断） |

---

## 14. 性能演进路线

### 14.1 长期性能目标

| 指标 | 目标值 | 实现手段 |
| --- | --- | --- |
| TCP 吞吐 | >= 100K msg/s（单核） | WorkerPool + 写队列 + BufferPool（P2） |
| 连接延迟 | P99 <= 1ms | 无锁热路径 + atomic + 单 goroutine 写 |
| 内存分配 | 热路径 0 alloc | sync.Pool + BufferPool（P2） |
| 并发连接 | >= 100K | 分片 SessionManager（P2）+ LRU 淘汰 |
| 插件开销 | <= 200ns/hop | 静态排序列表 + 接口内联 |

### 14.2 benchmark 驱动演进步骤

```
Step 1：建立基准（P0 完成后立即执行）
  BenchmarkTCPEcho          单核 Echo 吞吐基线
  BenchmarkSessionRegister  并发 Register/Get 基线
  BenchmarkPluginChain      每 hop 延迟基线（0/1/5/10 插件）
  BenchmarkBroadcast        10K session 广播延迟基线

Step 2：定位热点（pprof 驱动）
  go tool pprof → 通常热点：readLoop alloc、Manager 锁竞争

Step 3：按热点引入优化（不提前优化）
  alloc 热点 → 引入 BufferPool（infrastructure/bufferpool/）
  锁竞争热点 → SessionManager 升级为 32 分片锁 + per-shard LRU
  定时扫描热点 → HeartbeatPlugin 升级为时间轮

Step 4：验证优化效果
  对比 benchmark（count=5，取中位数）
  race detector 验证无竞态
  pprof 确认热点消除
  长时间运行验证无内存泄漏（heap 增长趋势）
```

---

## 15. 测试策略

### 15.1 测试层次

| 层次 | 目录 / 命令 | 目的 |
| --- | --- | --- |
| 包单元测试 | `go test ./internal/...` | 模块内行为验证 |
| 全量测试 | `go test ./...` | 所有 package |
| 脚本化测试 | `go run scripts/run_tests.go -mode all` | JSON + 可读报告 |
| Race 检测 | `go run scripts/run_tests.go -mode race` | 并发安全 |
| 覆盖率 | `go run scripts/run_tests.go -mode cover` | 覆盖率 smoke |
| 集成测试 | `tests/integration/` | 跨包端到端语义 |
| Fuzz 测试 | `go test -fuzz` | 畸形帧安全 |
| 基准测试 | `tests/benchmark/` | 性能回归 |
| 部署验证 | `scripts/run_tests.go (via -mode deploy)` | 部署资产语义检查 |
| CI | GitHub Actions（Windows + Ubuntu） | 跨平台回归 |

### 15.2 测试编写规范

- **端口：** 所有协议测试使用 `127.0.0.1:0`（内核分配），禁止固定端口（防冲突）。
- **缺陷回归：** 每个生产缺陷先写最小失败测试，修复后保留。
- **并发测试：** 所有涉及 Session / PluginChain / SessionManager 的测试必须使用 `-race` 验证。
- **云端验证：** 实机测试结果写入 `docs/PROJECT-REVIEW-YYMMDD-HHMMSS.md`。
- **部署测试：** 检查语义（字段值、安全配置），不只检查文件存在。

### 15.3 Fuzz 测试目标

**TCP Framer（5 个目标）：**

| 目标 | 验证点 |
| --- | --- |
| FuzzLengthPrefixFramer | 任意字节输入不 panic |
| FuzzLineFramer | 任意字节输入不 panic |
| FuzzFixedSizeFramer | 任意字节输入不 panic |
| FuzzRawFramer | 任意字节输入不 panic |
| FuzzLengthPrefixRoundtrip | WriteFrame → ReadFrame 往返一致性 |

**CoAP（2 个目标）：**

| 目标 | 验证点 |
| --- | --- |
| FuzzParseMessage | 任意字节解析不 panic，合法结果往返验证 |
| FuzzCoAPRoundtrip | 构造消息 → Serialize → Parse 往返一致性 |

### 15.4 关键回归测试清单

**必须持续维护的回归测试：**

```
Gateway 层：
  □ Start → Stop → Start 复用 SessionManager（生命周期可重入）
  □ Gateway 启动部分失败逆序回滚（已启动的 Server 被停止）
  □ Gateway.Stop() 可重复调用（幂等）
  □ CloseAll 在 StopAccept 完成后执行（不遗漏 session）

WebSocket 层：
  □ OnClose 只执行一次（并发路径：Gateway shutdown + read EOF）
  □ Ping-Pong 超时断开（PongTimeout 内未收到 Pong）
  □ AllowedOrigins 空列表拒绝跨域（返回 403）

CoAP 层：
  □ 重复 CON 消息不重复执行 Handler（MessageID 去重）
  □ retransmitLoop 不产生自锁死锁（活跃索引方案）

TCP 层：
  □ Plugin Drop 后连接仍可复用（后续消息正常处理）
  □ LengthPrefixFramer 不因短写丢失后续帧（完整写入保证）
  □ 并发 Close 幂等（sync.Once）
  □ writeLoop 未启动时 Close 安全（不等待 drain timeout）

gRPC-Web 层：
  □ max_message_bytes 非法值拒绝启动（配置验证）
  □ Direct 模式所有返回路径触发 OnClose
  □ WebSocket 模式读循环退出触发 OnClose

UDP 层：
  □ ErrPluginDrop 不删除伪会话（后续 datagram 可继续）
  □ ErrPluginBlock 不注册伪会话（Manager 计数准确）

插件层：
  □ PersistencePlugin.Close() 重复调用不 panic（sync.Once）
  □ ClusterPlugin.Close() 重复调用不 panic（sync.Once）
  □ PluginChain panic 隔离（单插件 panic 不影响其他插件）
  □ OnClose 逆序执行（单插件 panic 不中断后续插件）

配置层：
  □ max_message_bytes < 0 拒绝启动
  □ tls_cert_file 和 tls_key_file 必须成对出现
  □ 协议 addr 格式非法拒绝启动
```

### 15.5 基准测试目标

```
BenchmarkTCPEcho             → >= 100K msg/s（单核，长期目标）
BenchmarkSessionRegister     → 并发 Register/Get CRUD（分片锁效果对比）
BenchmarkPluginChain         → < 200ns/hop（5 插件链）
BenchmarkBroadcast           → 10K session 广播延迟
BenchmarkBufferPool          → < 10ns/op，0 alloc（P2 引入后）
BenchmarkHeartbeatScan       → P0 ticker vs P2 时间轮对比
BenchmarkLRUTouch            → O(1) 验证（P2 引入后）
```

---

## 16. 部署架构

### 16.1 Docker（deploy/docker/）

**多阶段构建：**

```dockerfile
# 阶段1：构建
FROM golang:1.26-alpine AS builder
ARG GOPROXY=https://goproxy.cn,direct  # 云环境构建代理可配置
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o shark-socket ./cmd/server

# 阶段2：运行
FROM alpine:3.22
RUN addgroup -S shark && adduser -S shark -G shark
USER shark
WORKDIR /app
COPY --from=builder /app/shark-socket .
EXPOSE 18000 18080 18081
ENTRYPOINT ["./shark-socket"]
```

**docker-compose 默认暴露端口：**

```
18000 TCP（主协议端口）
18080 Prometheus metrics
18081 Health / Readiness
```

### 16.2 Kubernetes（deploy/kubernetes/）

**生产默认安全配置：**

```yaml
securityContext:
  runAsNonRoot: true
  runAsUser: 1000
  allowPrivilegeEscalation: false
  readOnlyRootFilesystem: true
  capabilities:
    drop: ["ALL"]

readinessProbe:
  httpGet:
    path: /readyz
    port: 18081
  initialDelaySeconds: 5
  periodSeconds: 10

livenessProbe:
  httpGet:
    path: /healthz
    port: 18081
  initialDelaySeconds: 15
  periodSeconds: 20
```

**Kustomization 结构：**

```
deploy/kubernetes/
  application/kustomization.yaml   ← 应用资源
  monitoring/kustomization.yaml    ← Prometheus ServiceMonitor
  kustomization.yaml               ← 根聚合（避免跨目录引用被 load restrictor 拒绝）
```

### 16.3 Helm（deploy/helm/shark-socket/）

可参数化配置：image / replicaCount / service ports / env listener 地址 / resources / securityContext / probes / HPA。

### 16.4 集群部署拓扑

```
        ┌─────────────────┐
        │   LB / DNS      │
        └────────┬────────┘
    ┌────────────┼────────────┐
┌───┴───┐    ┌──┴────┐    ┌──┴────┐
│ Node1 │    │ Node2 │    │ NodeN │
│ shark │    │ shark │    │ shark │
└───┬───┘    └───┬───┘    └───┬───┘
    └────────────┼────────────┘
           ┌─────┴──────┐
           │ Redis/NATS  │
           │ PubSub+Cache│
           └─────────────┘
           ┌─────────────┐
           │ Prometheus  │
           │ + Grafana   │
           └─────────────┘

集群配置要点：
  ClusterPlugin：nodeID = hostname / Pod name
  Cache：RedisCache adapter（会话路由目录）
  PubSub：RedisPubSub 或 NATSPubSub adapter（节点间消息路由）
  Store：RedisStore 或 SQLStore adapter（会话持久化）

HPA 与会话亲和性：
  建议 LB 配置 sticky session（IP Hash 或 Cookie），
  避免同一客户端路由到不同节点造成跨节点路由压力。
  Pod 扩缩容时，ClusterPlugin 通过 PubSub 通知其他节点会话迁移。
```

### 16.5 外部依赖清单

```
生产必需：
  github.com/gorilla/websocket          v1.5.x
  github.com/prometheus/client_golang   v1.x
  github.com/quic-go/quic-go           v0.x

可选（按功能启用）：
  github.com/pion/dtls/v3              CoAP DTLS（P2）
  go.opentelemetry.io/otel             OpenTelemetry Tracer（P1）

Go 版本要求：>= 1.26
```

---

## 17. MQTT 外部集成

MQTT 不在本仓库实现，协议栈由外部项目 [X1aSheng/shark-MQTT](https://github.com/X1aSheng/shark-MQTT) 负责。

### 17.1 职责边界

| 边界 | shark-socket | shark-MQTT |
| --- | --- | --- |
| 网络编排 | 多协议 Gateway、统一启动/停止、共享 SessionManager | MQTT TCP/TLS 接入与 Broker 生命周期 |
| 协议语义 | 不解析 MQTT packet，不维护 QoS 状态机 | MQTT 3.1.1 / 5.0 codec、15 类报文、属性编解码 |
| 会话模型 | core.Session / SessionManager 抽象和跨协议管理 | ClientID 会话、CleanSession、Session Expiry；不与 shark-socket SessionManager 双写 |
| 消息语义 | 统一 []byte 消息通道、插件、指标 | QoS 0/1/2、inflight、retained、will message |
| 数据互通 | Cache / Store / PubSub 抽象层，消费/发布业务事件 | Memory / Redis / BadgerDB 存储后端，按契约投递 MQTT 事件 |
| 安全 | 连接限流、黑名单、全局插件 | MQTT Authenticator / Authorizer、ACL、TLS |

### 17.2 互通模式

```
shark-socket                  shared data plane              shark-MQTT
┌──────────────────┐         ┌──────────────────┐          ┌──────────────────┐
│ Gateway / API    │         │ Redis / DB       │          │ Broker Factory   │
│ TCP/UDP/HTTP/WS  │◄───────►│ PubSub / Outbox  │◄────────►│ MQTT Codec       │
│ CoAP/QUIC/gRPC-W │         │ Versioned Schema │          │ QoS State Machine│
│ Plugin / Metrics │         │ Idempotency Keys │          │ Session Store    │
└──────────────────┘         └──────────────────┘          └──────────────────┘
```

**数据契约要求：**

- 所有事件消息必须包含：`schema_version / source / event_id / timestamp / idempotency_key`。
- 消费者必须幂等并能容忍重试。
- MQTT topic 通配符、`$SYS` 保护、保留消息等协议细节只在 shark-MQTT 内维护。

**在线状态权威数据源：**

- 设备通过 MQTT 连接时：shark-MQTT 为权威数据源，发布 `mqtt.client.connected` 事件。
- 设备通过 WebSocket/TCP 连接时：shark-socket SessionManager 为权威数据源。
- 跨协议聚合在线状态：由独立业务服务消费两端事件后聚合，不放入任一核心层。

---

## 18. 架构决策记录

### ADR-001：核心 Session 保持原始字节

**决策：** `core.Session.Send` 只接收 `[]byte`。

**原因：**

- Gateway 必须管理混合协议 session，泛型 Session 会把业务类型泄露到运行时层。
- 插件链天然处理原始 payload，无需感知业务消息类型。
- 类型安全通过 `Codec[M]` 和 `AdaptTyped[M]` 在 Handler 层实现，边界清晰。

**转换条件：** 若 `core.Session` 无法承载至少两个真实业务协议的类型安全需求，在 v1.0 API 稳定前评估引入泛型 Session。

### ADR-002：Gateway 拥有共享 Runtime

**决策：** SessionManager、PluginRunner、Logger、Metrics、Tracer 由 Gateway 创建或注入，协议通过 `UseRuntime` 接收。

**原因：**

- 避免每个协议各自维护插件和 session，保证跨协议统计、广播和关闭一致。
- 显式所有权，便于测试替换 Runtime 组件。
- 协议层不得自行创建全局替代依赖。

### ADR-003：关闭流程分阶段（StagedServer）

**决策：** 协议可实现 `StagedServer`，Gateway 按 StopAccept → Drain → CloseSessions 三阶段执行。

**原因：**

- 直接 `Stop` 容易混淆 listener、goroutine、session 的释放顺序。
- 分阶段关闭更容易定位超时和泄漏，每个阶段语义明确。
- 对 TCP / WebSocket / QUIC 等长连接协议尤其重要。
- `Start → Stop → Start` 可重入约束通过分阶段关闭更容易保证。

### ADR-004：插件 panic 在 PluginRunner 隔离

**决策：** panic recover 放在 PluginRunner，不散落在各协议实现中。

**原因：**

- 插件失败策略一致，协议代码更专注网络生命周期。
- 统一记录 plugin panic 指标（`shark_plugin_panics_total`）。
- `OnClose` 必须尽量全部执行，即使某个插件 panic 也不中断后续插件。

### ADR-005：Codec 适配层作为稳定契约

**决策：** `Codec[M]` 接口定义在 `core` 层，`AdaptTyped[M]` 定义在 `core` 层，对外通过 `api` 导出。

**原因：**

- 类型化消息与传输层解耦，`core.Message` 始终为 `[]byte`。
- 业务代码通过 `AdaptTyped` 装配类型化 Handler，无需修改核心层。
- Codec 接口稳定，业务可自由实现 JSON / Protobuf / MessagePack 等 Codec。

### ADR-006：benchmark 驱动优化

**决策：** 性能优化必须有 benchmark 基线和 pprof 热点证据，禁止凭直觉改结构。

**原因：**

- 过早优化是复杂度来源（BufferPool、时间轮、分片锁均有实现复杂度）。
- benchmark 驱动确保优化有实测效果，而非理论收益。
- 当前阶段架构正确性优先，P2 阶段按证据引入复杂优化。

### ADR-007：构建代理可配置

**决策：** Dockerfile 暴露 `GOPROXY` build arg，docker-compose 提供默认代理（`goproxy.cn`）。

**原因：**

- 云服务器构建环境可能无法稳定访问 `proxy.golang.org`，构建网络问题不应阻塞镜像生产。
- 默认值可工作，仍允许用户覆盖为企业内部代理。

---

## 19. 实施路线

### P0：架构地基（先让框架可运行、可测试）

1. `internal/core/` 完整类型系统（Protocol / Message / Session / Server / Runtime / Plugin / Codec / 错误体系 / 可观测接口）。
2. `internal/runtime/` Gateway（StagedServer 三阶段关闭 + 启动回滚）+ SessionManager（P0 单锁）+ PluginRunner（panic 隔离）+ WorkerPool（四策略）。
3. `internal/transport/tcp/` Framer 4 种 + TCPSession 6 步关闭 + TCPServer acceptLoop + TCPClient。
4. `internal/infrastructure/observability/` slogLogger + PrometheusMetrics（静态预注册指标子集）+ NoopTracer。
5. `internal/application/` Config（JSON+env+统一验证）+ App 装配。
6. `cmd/server/main.go` 信号处理 + 优雅关闭。
7. `api/api.go` 类型别名 + 工厂函数。
8. 基础插件：BlacklistPlugin + RateLimitPlugin + HeartbeatPlugin（ticker 扫描）。
9. 健康 / 就绪 / Metrics 端点。
10. `tests/defects/` 关键回归测试。

**P0 验收：** `go test ./...` 通过 + `go vet ./...` 通过 + TCP Echo 集成测试通过 + Gateway Start→Stop→Start 回归通过。

### P1：生产协议与配置

**协议增强：**

1. UDP（伪会话 + sweepLoop + ErrPluginBlock 不注册）。
2. WebSocket（OnClose sync.Once + AllowedOrigins）。
3. HTTP Mode A + Mode B（defer OnClose）。
4. CoAP（CON ACK + MessageID 去重 + retransmitLoop 活跃索引）。
5. QUIC（强制 TLS + stream 尾块 + Stop 幂等）。
6. gRPC-Web（Direct + WebSocket 模式，OnClose 全路径）。

**配置与安全：**

1. TLS / mTLS 配置文件化（TCP / QUIC）。
2. 证书热加载（SIGHUP）。
3. 统一 shutdown stage timeout 配置。
4. 完整 Prometheus 指标静态预注册（锁定标签集合）。
5. /healthz 扩展响应体（status / uptime / protocols / sessions）。

**插件完善：**

1. AutoBanPlugin。
2. PersistencePlugin（CircuitBreaker 保护）。
3. SlowHandlerPlugin。
4. ClusterPlugin（PubSub + Cache + 节点心跳）。

**验收：** 所有协议 Echo 集成测试通过 + 异常测试矩阵关键项通过 + race detector 零报告。

### P2：性能与防御（benchmark 驱动）

1. 建立全量 benchmark 基线。
2. 六级 BufferPool（benchmark 证明 alloc 为瓶颈后引入）。
3. 分片 SessionManager（32 shard + per-shard LRU）。
4. 时间轮心跳（替换 ticker 扫描）。
5. OverloadProtector + BackPressure。
6. 高频日志采样器。
7. FD 使用率监控。
8. CoAP Block-wise 传输。
9. LwM2M 正式标准路径 + Observe/Notify。
10. gRPC-Web base64 text 模式 + method 路由。

**验收：** benchmark 目标达成 + pprof 热点消除 + 100K 并发连接压测通过。

### P3：集群与持久化

1. RedisCache / RedisPubSub / RedisStore adapter。
2. 多节点跨节点路由实测验证。
3. 滚动升级无损会话验证。
4. Grafana Dashboard（shark-socket 全量指标可视化）。
5. Prometheus / Grafana 云端实机记录。

---

## 20. 验收标准

每次架构级改动必须满足：

1. `go test ./... -count=1` 通过。
2. `go vet ./...` 通过。
3. 若是缺陷修复：相关 focused test 先失败后通过。
4. `go run scripts/run_tests.go -mode all -timeout 5m` 通过。
5. 部署相关改动必须更新 `tests/deploy` 并通过 `scripts/run_tests.go (via -mode deploy)`。
6. 公共行为改变必须同步 README、配置文档、测试策略或审查报告。
7. 云端实测结果写入 `docs/PROJECT-REVIEW-YYMMDD-HHMMSS.md`。
8. 新协议或新功能必须有对应的 example。

---

## 21. 文档维护规则

- 架构文档（本文）描述设计边界、契约和长期方向，不记录测试流水账。
- 具体验证命令和结果写入 `docs/PROJECT-REVIEW-*.md`。
- 配置字段完整参考写入 `docs/CONFIGURATION.md`。
- 测试方法写入 `docs/TEST-STRATEGY.md` 和 `docs/PROTOCOL-GUIDE.md`。
- 重要架构决策拆分到 `docs/adr/ADR-NNN-*.md`。
- 审查报告按 `PROJECT-REVIEW-YYMMDD-HHMMSS.md` 命名，保留历史。

---

*文档版本：v2.0 综合设计版。融合 `shark-socket` 成熟工程积累与 `shark-socket` 正确架构原则，以显式所有权、分阶段关闭、benchmark 驱动优化为核心，Go >= 1.26。*