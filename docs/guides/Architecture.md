# Shark-Socket 综合架构设计文档

> **[!] 此文档为历史归档,内容已过时.** 请参考新文档:
> - [架构概述](../architecture/ARCHITECTURE.md)
> - [接口契约](../architecture/CONTRACTS.md)
> - [Gateway & 运行时](../architecture/GATEWAY.md)
> - [部署指南](../architecture/DEPLOYMENT.md)

> 高性能,可扩展的多协议服务端网络框架,采用 Go 1.26+ 开发.在 `shark-socket` 正确架构基础上,融合 `shark-socket` 成熟工程积累,提供 TCP,UDP,HTTP,WebSocket,CoAP,LwM2M,QUIC,gRPC-Web 协议的统一 Gateway 运行时.MQTT 3.1.1/5.0 由外部项目 [shark-MQTT](https://github.com/X1aSheng/shark-MQTT) 作为独立 Broker 提供,二者通过数据库,缓存,消息契约互通.

---

## 1. 项目定位与设计哲学

### 1.1 第一原则

**先正确,再完整,再高效.**

1. 运行时所有权,协议生命周期,插件执行和关闭流程必须可解释,可测试,可恢复.
2. 公共 API 小而稳定,内部实现可演进.
3. Gateway 统一编排多协议运行时,SessionManager,PluginRunner,Logger,Metrics,Tracer 的所有权显式.
4. 各协议只负责自己的网络细节,不私自创建或关闭共享运行时资源.
5. 类型化消息放在协议原始字节层之上,通过 Codec 适配,不污染核心 Session.
6. 优雅关闭分阶段执行:停止接收,等待读写,关闭会话,收尾释放.
7. 测试,脚本,部署清单和文档保持同步.
8. 用 benchmark 驱动性能优化,不凭直觉改结构.

### 1.2 设计原则

| 原则 | 实践 |
| --- | --- |
| 接口契约优先 | 所有模块通过 interface 交互,依赖倒置 |
| 显式所有权 | Gateway 创建并注入 Runtime,协议通过 UseRuntime 接收 |
| 分阶段关闭 | StagedServer 接口,StopAccept / Drain / CloseSessions 三阶段 |
| 零值可用 | Functional Options 模式,默认值合理 |
| 失败隔离 | 单连接 / 单协程 panic 不影响整体,插件 panic 在 PluginRunner 隔离 |
| 可观测优先 | 所有关键路径内置 metrics / trace / log,采集不阻塞业务 |
| benchmark 驱动 | 建立基准后再优化,pprof 定位热点 |
| 编译期验证 | `var _ Interface = (*Impl)(nil)` 接口满足检查 |

### 1.3 当前阶段非目标与转换条件

| 当前非目标 | 转换为目标的阶段 | 转换条件 |
| --- | --- | --- |
| 泛型 Session 作为核心接口 | v1.0 API 稳定前评估 | 现有 core.Session 无法承载至少两个真实业务协议的类型安全需求 |
| 六级 BufferPool,时间轮,分片 LRU | P2 性能与防御 | benchmark 证明分配,超时扫描或 Session 锁竞争成为瓶颈 |
| 完整防御体系(OverloadProtector,BackPressure) | P2 性能与防御 | 基准压测建立,热路径分配点已定位 |
| Gateway 直接耦合数据库,Redis,消息队列 | P2/P3 集群与持久化 | 外部 adapter 接口稳定,完成至少一种真实后端集成测试 |
| 内建完整 MQTT Broker | P1/P2 MQTT 专项 | 明确选择内建 Broker 而不是外部 Broker 适配 |
| 每个协议一次性实现完整标准 | 按协议专项推进 | 当前 smoke / 边界测试稳定,且有明确互操作目标 |

---

## 2. 总体分层架构

### 2.1 层次划分

```
+---------------------------------------------------------------------+
| Layer 0: api/                                                        |
|   对外门面层.导出类型别名,工厂函数,Option 透传,Codec 接口定义   |
+---------------------------------------------------------------------+
| Layer 1: cmd/ + internal/app/                                        |
|   可运行入口 + 应用装配层                                           |
|   配置加载(JSON + env)-> Gateway 装配 -> 健康 / 指标服务 -> 信号处理 |
|   配置验证在 app 层统一执行,不在 Start() 中执行                     |
+---------------------------------------------------------------------+
| Layer 2: internal/runtime/                                           |
|   运行时层:Gateway,SessionManager,PluginChain,Runtime 容器       |
+---------------------------------------------------------------------+
| Layer 3: internal/transport/ + internal/plugin/ + internal/protocol/ |
|   传输协议层:TCP,UDP,HTTP,WebSocket,CoAP,QUIC,gRPC-Web       |
|   应用协议层:LwM2M 生命周期模型                                    |
|   插件层:黑名单,限流,心跳,持久化,自动封禁,慢处理,集群广播    |
+---------------------------------------------------------------------+
| Layer 4: internal/core/                                              |
|   稳定契约层:Protocol,Session,Server,Runtime,Plugin,Codec      |
|   可观测接口:Logger,Metrics,Tracer                               |
|   错误体系:失败信号哨兵(13 个)+ 插件控制错误(2 个)                |
+---------------------------------------------------------------------+
| Layer 5: internal/infra/                                             |
|   基础设施层:cache,store,pubsub,circuitbreaker,mqtt,tlsutil        |
|   observability:logger,metrics,prometheus,otel                 |
+---------------------------------------------------------------------+
```

### 2.2 依赖矩阵

依赖只能从上层指向下层,同层内部保持局部依赖:

```
api / cmd / application
        v
runtime + transport + plugin + protocol
        v
core + infrastructure
```

**严格禁止事项:**

- `core` 不依赖任何具体协议实现.
- `runtime` 不依赖 `cmd` 或 `application`.
- `transport` 不直接创建全局 Gateway.
- `plugin` 不直接控制协议 listener 生命周期.
- `infrastructure` 不反向调用业务层.
- `protocol` 不依赖 `transport` 具体实现.

**允许关系总览([OK]=允许 [X]=禁止):**

| | api | cmd | app | runtime | transport | plugin | protocol | core | infra |
|---|---|---|---|---|---|---|---|---|---|
| api | - | [X] | [X] | [OK] | [OK] | [OK] | [OK] | [OK] | [OK] |
| cmd | [OK] | - | [OK] | [X] | [X] | [X] | [X] | [OK] | [X] |
| application | [OK] | [X] | - | [OK] | [OK] | [OK] | [OK] | [OK] | [OK] |
| runtime | [X] | [X] | [X] | - | [X] | [OK] | [X] | [OK] | [OK] |
| transport | [X] | [X] | [X] | [X] | - | [X] | [OK] | [OK] | [OK] |
| plugin | [X] | [X] | [X] | [X] | [X] | - | [X] | [OK] | [OK] |
| protocol | [X] | [X] | [X] | [X] | [X] | [X] | - | [OK] | [OK] |
| core | [X] | [X] | [X] | [X] | [X] | [X] | [X] | - | [X] |
| infrastructure | [X] | [X] | [X] | [X] | [X] | [X] | [X] | [X] | - |

---

## 3. 目录结构

```
shark-socket/
+-- api/
|   +-- api.go                          # 统一对外门面:类型别名,工厂函数,Option 透传
|
+-- cmd/
|   +-- shark-socket/
|       +-- main.go                     # 可运行入口:加载配置,启动 App,处理信号
|
+-- internal/
|   +-- core/                           # 稳定契约层
|   |   +-- types.go                    # Protocol 类型 + 常量 + SessionState
|   |   +-- session.go                  # Session 接口 + SessionManager 接口
|   |   +-- server.go                   # Server 接口 + RuntimeConfigurable + StagedServer
|   |   +-- message.go                  # Message + Handler / TypedHandler / AdaptTyped
|   |   +-- plugin.go                   # Plugin 接口 + PluginRunner 接口 + 控制错误
|   |   +-- errors.go                   # 失败信号哨兵(13 个)
|   |   +-- observability.go            # Logger / Metrics / Tracer / Span + StageTimeouts
|   |
|   +-- runtime/                        # 运行时层
|   |   +-- gateway.go                  # Gateway:多协议编排,三阶段关闭,插件启停编排
|   |   +-- options.go                  # GatewayOption + gatewayConfig + 阶段超时默认值
|   |   +-- session_manager.go          # SessionManager 实现(单锁)
|   |   +-- session_metrics.go          # metricSessionManager:会话指标装饰器
|   |   +-- plugin_chain.go             # PluginChain:copy-on-write 排序,执行,panic 隔离,启停
|   |   +-- runtime.go                  # Runtime:core.Runtime 的实现
|   |   +-- runtime_options.go          # RuntimeOption(非导出,Gateway 内部使用)
|   |
|   +-- transport/                      # 传输层,每协议一个包
|   |   +-- tcp/                        # server / session / client / framer / policy / options
|   |   +-- udp/                        # server / session / options
|   |   +-- http/                       # server / session / options
|   |   +-- websocket/                  # server / session / options
|   |   +-- coap/                       # server / session / message / observe / options
|   |   +-- quic/                       # server / session / options
|   |   +-- grpcweb/                    # server / session / websocket_session / framing / options
|   |   +-- shared/                     # 传输层共享组件(不被 runtime 反向依赖)
|   |       +-- worker_pool.go          # WorkerPool:固定 worker + 三种队列满策略(仅 TCP 使用)
|   |       +-- acceptor.go             # Acceptor:并发上限 + 接受速率令牌桶
|   |       +-- backoff.go              # Backoff:accept / read 循环失败退避
|   |       +-- handler.go              # CallHandler:handler panic 隔离
|   |       +-- timeout.go              # IsTimeout:net.Error 超时判定
|   |       +-- dtls.go                 # DTLS 配置桥接 + 客户端证书强制校验
|   |
|   +-- protocol/
|   |   +-- lwm2m/                      # server / client / model / coap / codec_tlv
|   |
|   +-- plugin/
|   |   +-- blacklist.go                # IP/CIDR 黑名单
|   |   +-- ratelimit.go                # 令牌桶限流
|   |   +-- heartbeat.go                # 心跳超时清理
|   |   +-- autoban.go                  # 自动封禁
|   |   +-- persistence.go              # 异步持久化
|   |   +-- cluster.go                  # 跨节点路由
|   |   +-- slow_handler.go             # 慢处理日志
|   |   +-- lifecycle.go                # lifecycle:插件 Start/Stop 周期的可重入串行器
|   |
|   +-- app/                            # 应用装配层
|   |   +-- app.go                      # App:装配 Gateway + Server + 健康 / 指标服务
|   |   +-- config.go                   # Config:JSON + env 覆盖 + 统一验证 + TLS 构造
|   |
|   +-- infra/                          # 基础设施层
|       +-- cache/                      # cache.go:Cache 接口 + MemoryCache
|       +-- store/                      # store / bolt / message_log / session_store
|       +-- pubsub/                     # pubsub.go:PubSub 接口 + ChannelPubSub
|       +-- circuitbreaker/             # circuitbreaker.go:Closed / Open / HalfOpen
|       +-- mqtt/                       # adapter / options:MQTT 3.1.1 / 5.0 客户端适配
|       +-- tlsutil/                    # cert_cache / watcher:证书与客户端 CA 热加载
|       +-- observability/              # logger / metrics / prometheus / otel
|
+-- tests/
|   +-- cross_protocol_test.go          # 跨协议端到端测试
|   +-- benchmark/                      # 吞吐 / 延迟 / 内存基准
|   +-- deploy/                         # 部署资产语义验证
|   +-- stress/                         # 多协议压测
|
+-- examples/                           # basic_tcp / basic_udp / basic_coap / basic_lwm2m
|                                       # basic_websocket / tls_server / typed_handler
|                                       # config / custom_plugin / graceful_shutdown
|                                       # multi-protocol / mqtt-bridge
|
+-- scripts/                            # run_tests / run_stress / run_benchmarks
|                                       # parse_test_log
|
+-- deploy/
|   +-- docker/                         # Dockerfile / docker-compose.yml / mosquitto.conf
|   +-- k8s/                            # namespace / deployment / service / configmap / hpa
|   |                                   # pdb / networkpolicy / serviceaccount / kustomization
|   +-- helm/shark-socket/              # Chart.yaml / values.yaml / templates/
|
+-- docs/
|   +-- architecture/                   # ARCHITECTURE / CONTRACTS / GATEWAY / LIFECYCLE
|   |                                   # PLUGIN / TRANSPORT / PROTOCOL / ERRORS / SECURITY
|   |                                   # OBSERVABILITY / DEPLOYMENT / TROUBLESHOOTING
|   +-- guides/                         # Architecture.md / EXAMPLES.md / MQTT-INTEGRATION.md
|   |                                   # CONFIGURATION-*.md / TEST-STRATEGY-*.md
|   +-- decisions/                      # ADR-001 .. ADR-011
|   +-- planning/                       # PROJECT-PLAN-*.md / IMPLEMENTATION-GOALS-*.md
|   +-- reports/                        # PROJECT-REVIEW-*.md / DEPLOYMENT-VALIDATION-*.md
|
+-- .github/workflows/ci.yml            # 单一 CI 工作流(Ubuntu / Windows 双矩阵)
+-- go.mod                              # module shark-socket, go 1.26
+-- go.sum
+-- README.md
+-- README.zh-CN.md
+-- CHANGELOG.md
```


---

## 4. 核心契约层(internal/core/)

### 4.1 Protocol(protocol.go)

```go
// Protocol 是协议身份标识,uint8 底层,编译期常量.
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

// String() 满足 fmt.Stringer,用于日志和指标标签.
// 协议标签字符串通过 unique 包(Go 1.26)池化,减少分配.
```

协议标识用于:Gateway 注册去重,Session 标记来源,插件和指标标签,日志和追踪属性.

### 4.2 Message(message.go)

```go
type Message struct {
    SessionID uint64
    Protocol  Protocol
    Payload   []byte            // 始终为原始字节,业务结构不进入核心层
    Meta      map[string]string // 协议级元数据(预分配容量 4)
}
```

**设计原则:**

- `Payload` 始终为 `[]byte`,业务类型通过 `Codec[M]` 和 `AdaptTyped[M]` 在 Handler 层适配.
- `Meta` 用于协议级透传字段(如 CoAP Token,HTTP Header),不用于业务属性.
- 业务属性通过 `Session.SetMeta` 存储在会话维度.

**类型化适配路径:**

```
网络帧 -> []byte -> core.Message -> Codec[M].Decode -> TypedHandler[M]
TypedHandler 返回值 -> Codec[M].Encode -> Session.Send([]byte)
```

### 4.3 Session(session.go)

```go
// SessionState 会话状态机.
type SessionState uint8

const (
    Connecting SessionState = 0 // 连接建立中
    Active     SessionState = 1 // 正常通信
    Draining   SessionState = 2 // 排空写队列中
    Closed     SessionState = 3 // 已关闭
)

// Session 是运行时统一会话接口.
// 核心字节层:Send 只接收 []byte,类型安全通过 Codec 在上层实现.
type Session interface {
    // 身份与元信息(不可变)
    ID()           uint64
    Protocol()     Protocol
    RemoteAddr()   net.Addr
    LocalAddr()    net.Addr
    CreatedAt()    time.Time

    // 状态(原子操作)
    State()        SessionState
    IsAlive()      bool           // State() == Active
    LastActiveAt() time.Time      // 心跳,TTL,清理使用

    // 生命周期
    Context()      context.Context  // 关闭时 cancel
    Send([]byte)   error            // 关闭后返回 ErrSessionClosed
    Close(context.Context) error    // 幂等关闭,ctx 控制 drain 超时

    // 元数据(线程安全 KV,sync.Map 实现)
    SetMeta(key string, val any)
    GetMeta(key string) (any, bool)
    DelMeta(key string)
}
```

**会话状态机:**

```
Connecting --accept 成功---> Active --Close()---> Draining --drain 完成---> Closed
    |                          |                                              ^
    +--error / fatal-----------+----------------------------------------------+

状态转换:atomic.Int32 CAS 保证并发唯一性
幂等关闭:sync.Once 封装 Close 核心逻辑
```

**关键实现要求:**

- `Close` 必须幂等,多次调用等价一次.
- `Send` 在 Draining / Closed 状态下必须返回 `ErrSessionClosed`.
- `LastActiveAt` 由协议层在收到任意数据时更新(`TouchActive` 内部方法).
- 元数据实现必须线程安全(`sync.Map`).
- 协议实现可以有自己的 session 结构,但必须满足统一接口.

**编译期验证:**

```go
var _ Session = (*TCPSession)(nil)
var _ Session = (*UDPSession)(nil)
var _ Session = (*WSSession)(nil)
```

### 4.4 SessionManager(session.go)

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

    // 广播(内部快照,不持锁发送)
    Broadcast([]byte) error

    // 关闭所有当前会话(不永久关闭 Manager,Gateway Stop 后可复用)
    CloseAll(context.Context) error
}
```

**设计边界:**

- Manager 不创建协议连接,不拥有 listener.
- `CloseAll` 只清理当前会话,不永久关闭 Manager.
- Gateway 停止后再次启动时,Manager 仍应可用(Start->Stop->Start 可重入).
- `Broadcast` 先快照 session 列表再释放锁,不持锁发送(防广播阻塞).

**并发安全保证:**

- `NextID`:`atomic.Uint64` 全局递增,跨重启 ID 可能复用,Session 不依赖 ID 的全局唯一性.
- `Count`:`atomic.Int64` 无锁读取.
- P0 实现:全局 `sync.RWMutex`,功能正确优先.
- P2 演进:32 分片锁 + per-shard LRU 淘汰(benchmark 证明锁竞争为瓶颈后引入).

### 4.5 Server 接口族(server.go)

```go
// Server 是所有协议服务的基础接口.
type Server interface {
    Protocol() Protocol
    Start(context.Context) error
    Stop(context.Context) error
}

// RuntimeConfigurable:支持 Gateway 运行时注入.
// 协议实现此接口后,Gateway 在启动前自动调用 UseRuntime.
type RuntimeConfigurable interface {
    UseRuntime(Runtime)
}

// StagedServer:支持分阶段关闭的协议实现(推荐长连接协议实现此接口).
type StagedServer interface {
    StopAccept(context.Context) error   // 停止接收新连接 / 新请求
    Drain(context.Context) error        // 等待读写 goroutine 收敛
    CloseSessions(context.Context) error // 关闭协议持有的活跃会话
}
```

**StagedServer 各协议实现说明:**

| 协议 | StopAccept | Drain | CloseSessions |
| --- | --- | --- | --- |
| TCP | listener.Close() + 停止 acceptLoop | WaitGroup 等待 readLoop/writeLoop | 遍历 Close 所有 TCPSession |
| WebSocket | 停止 HTTP Upgrade Mux | 等待 pingLoop/readLoop | 发送 Close 帧后关闭 WSSession |
| QUIC | quic.Listener.Close() | 等待 stream goroutine | connection.CloseWithError() |
| UDP | 不适用(无 accept) | 不适用(单 goroutine) | 清理所有伪会话 |
| HTTP | 关闭 http.Server(Mode B) | 不适用 | 不适用(per-request session) |

**Drain 失败降级策略:**

- Drain 超时后强制关闭所有 goroutine(通过 context cancel).
- 记录 Warn 日志:`"drain timeout, forcing close, goroutine_count=N"`.
- 不阻塞后续 CloseSessions 阶段.

### 4.6 Runtime 接口(runtime.go)

```go
// Runtime 是协议运行所需共享依赖容器.
// Gateway 创建并注入,协议通过 UseRuntime 接收,不得自行创建全局替代.
type Runtime interface {
    Sessions() SessionManager
    Plugins()  PluginRunner
    Logger()   Logger
    Metrics()  Metrics
    Tracer()   Tracer
}
```

**使用规则:**

- 协议层只依赖 `Runtime` 接口,不依赖具体实现.
- 单独启动协议服务时(测试场景),`runtime.NewRuntime(nil, nil)` 即可:两个参数为 nil 时
  分别退化为 `NewSessionManager()` 与 `NewPluginChain()`,可观测组件退化为 Nop 实现.
- 通过 Gateway 启动时,必须接收 Gateway 注入的 Runtime.
- 各协议实际使用的 Runtime 子集应在实现注释中标注(便于评估后续拆分需要).

### 4.7 Plugin 与 PluginRunner(plugin.go)

```go
// Plugin 是插件基础接口.
type Plugin interface {
    Name()     string
    Priority() int  // 数字越小越先执行

    // OnAccept:连接建立后执行,返回 ErrPluginBlock 拒绝连接.
    OnAccept(Session) error

    // OnMessage:收到消息后执行,支持改写 payload.
    // 返回 ErrPluginDrop 丢弃消息(连接可继续).
    // 返回 ErrPluginBlock 关闭连接.
    // 返回普通 error 按协议策略处理.
    OnMessage(Session, []byte) ([]byte, error)

    // OnClose:会话关闭时执行,逆序执行,不可中断.
    OnClose(Session)
}

// BasePlugin 提供空实现,自定义插件只需覆盖关心的方法.
type BasePlugin struct{}

// PluginRunner 是插件链执行契约(不含注册:注册属于具体实现的装配职责).
type PluginRunner interface {
    // 执行入口:以下方法在热路径调用,panic 必须隔离.
    OnAccept(Session) error
    OnMessage(Session, []byte) ([]byte, error)
    OnClose(Session)

    // StartAll 按优先级正序启动所有 LifecyclePlugin,失败时回滚已启动者.
    // Gateway.Start 在任何 Server 启动之前调用.
    StartAll() error

    // StopAll 按优先级逆序停止所有 LifecyclePlugin,汇总各自失败.
    // Gateway.Stop 在全部会话关闭之后调用.重复或未配对调用是 no-op.
    StopAll() error
}

// 特殊控制错误(非业务错误,控制插件链行为)
var (
    ErrPluginDrop  = errors.New("shark: plugin drop message")
    ErrPluginBlock = errors.New("shark: plugin block session")
)
```

注册由具体类型 `internal/runtime.PluginChain` 的 `Append(plugins ...Plugin)` 完成,
不在接口内:接口描述"如何执行",注册是装配阶段细节.

**PluginRunner 执行规则:**

```
OnAccept(按 Priority 升序):
  -> nil:继续下一个插件
  -> ErrPluginBlock / 普通 error:立即返回,链不继续(关闭动作由传输层执行)
  -> panic:recover + 记录 error + 返回 ErrPluginPanic
  任一插件返回错误时,序号在它之前且已成功执行的插件按逆序收到 OnClose

OnMessage(按 Priority 升序,支持 payload 改写):
  data = originalPayload
  for each plugin:
    out, err = plugin.OnMessage(sess, data)
    -> nil:data = out(允许 plugin 改写 payload),继续
    -> ErrPluginDrop:停止链,不调用 Handler,返回 nil + err
    -> ErrPluginBlock / 普通 error:停止链,返回 nil + err
    -> panic:返回 nil + ErrPluginPanic
  return data, nil

OnClose(按 Priority 逆序,不可中断):
  for each plugin(逆序):
    即使 panic 也继续执行后续插件(safeClose 只记日志,不改控制流)
```

**没有 `stopOnError` 开关:** 错误一律终止链并上抛,由传输层决定关闭会话还是只记录.
行为只有一种,便于推理与测试.

### 4.8 Codec 接口(codec.go)

```go
// Codec[M] 在 []byte 与业务类型 M 之间转换,定义在 core 层作为稳定契约.
type Codec[M any] interface {
    Encode(M) ([]byte, error)
    Decode([]byte) (M, error)
    ContentType() string // "application/json" / "application/protobuf" 等
}

// Handler 函数类型(原始字节层)
type Handler func(Session, Message) error

// TypedHandler 函数类型(类型化层,通过 AdaptTyped 适配为 Handler)
type TypedHandler[M any] func(Session, M) error

// AdaptTyped 将 TypedHandler[M] 包装为 Handler;codec.Decode 的错误原样返回.
func AdaptTyped[M any](h TypedHandler[M], codec Codec[M]) Handler {
    return func(s Session, msg Message) error {
        typed, err := codec.Decode(msg.Payload)
        if err != nil {
            return err
        }
        return h(s, typed)
    }
}

// 内置 Codec 实现
type RawCodec struct{}   // []byte 透传,无转换开销
type JSONCodec[M any] struct{} // encoding/json
```

### 4.9 错误体系(errors.go)

**设计取向:** 错误变量只是"失败信号"哨兵,供调用方用 `errors.Is` 判定;框架不提供
`IsRetryable` / `IsFatal` 之类的分类包装函数.能否重试由调用场景决定,不由错误类型决定.
完整说明见 `docs/architecture/ERRORS.md`.

**失败信号哨兵(13 个):**

```go
// 关闭与生命周期
var (
    ErrClosed        = errors.New("shark: closed")
    ErrServerClosed  = errors.New("shark: server closed")
    ErrSessionClosed = errors.New("shark: session closed")
)

// 参数与注册
var (
    ErrInvalidArgument   = errors.New("shark: invalid argument")
    ErrDuplicateProtocol = errors.New("shark: duplicate protocol")
    ErrDuplicateSession  = errors.New("shark: duplicate session")
    ErrNoServers         = errors.New("shark: no servers registered")
    ErrSessionCapacity   = errors.New("shark: session manager at capacity")
)

// 消息与背压
var (
    ErrWriteQueueFull = errors.New("shark: write queue full")
    ErrFrameTooLarge  = errors.New("shark: frame too large")
)

// 能力与 panic
var (
    ErrUnsupportedFeature = errors.New("shark: unsupported feature")
    ErrHandlerPanic       = errors.New("shark: handler panic")
    ErrPluginPanic        = errors.New("shark: plugin panic")
)
```

**插件控制错误(定义在 `plugin.go`,是控制流而非失败信号):**

```go
var (
    ErrPluginDrop  = errors.New("shark: plugin drop message")
    ErrPluginBlock = errors.New("shark: plugin block session")
)
```

`ErrPluginDrop` 表示"消息已被插件消费,不要再交给 handler";`ErrPluginBlock` 表示"该会话
应被拒绝或关闭".二者是插件链与传输层之间的约定.

**判定方式:**

```go
if errors.Is(err, core.ErrSessionClosed) { ... }   // 哨兵判定
if shared.IsTimeout(err) { ... }                    // net.Error 超时判定(shared/timeout.go)
var ne net.Error
if errors.As(err, &ne) { ... }                      // 提取具体错误类型
```

### 4.10 可观测接口(observability.go)

```go
// Logger 结构化日志接口.
type Logger interface {
    Debug(msg string, attrs ...any)
    Info(msg string, attrs ...any)
    Warn(msg string, attrs ...any)
    Error(msg string, attrs ...any)
}

// Metrics 指标抽象:调用方给出指标名与标签串,后端自行决定聚合方式.
type Metrics interface {
    IncCounter(name string, labels ...string)
    SetGauge(name string, value float64, labels ...string)
    ObserveHistogram(name string, value float64, labels ...string)
}

// Tracer 追踪抽象(兼容 OpenTelemetry).
type Tracer interface {
    Start(ctx context.Context, name string, attrs ...any) (context.Context, Span)
}

type Span interface {
    End()
    RecordError(error)
}
```

**接口比常见设计更窄,这是有意的:**

- `Logger` 只有四个方法,没有 `With(args...)`,也没有 `WithContext(ctx)`.需要附加字段时把
  键值对直接传给 `Info` / `Error` 的 `attrs`,或在业务侧自行包装一层子 logger.
- `Metrics` 只有三个"记录"方法,没有 `Counter()/Gauge()/Histogram()` 工厂方法;后端
  (MemoryMetrics / PrometheusMetrics)在首次写入时按 `name+labels` 建条目.
- `Span` 只有 `End` 与 `RecordError`,没有 `SetAttribute`.

**StageTimeouts(同文件):**

```go
type StageTimeouts struct {
    StopAccept    time.Duration
    Drain         time.Duration
    CloseSessions time.Duration
    Finalize      time.Duration
}
```

**日志关键字段规范:**

```
session_id, protocol, remote_addr, local_addr,
plugin_name, error, duration_ms, trace_id, request_id,
msg_size, queue_depth, state, reason
```

**日志级别语义:**

| 级别 | 场景 |
| --- | --- |
| Debug | 帧解析细节,插件执行过程(生产关闭) |
| Info | 连接建立/断开,Server 启动/停止 |
| Warn | 限流触发,重传,降级,Drain 超时 |
| Error | 异常断连,panic recover,基础设施错误 |


---

## 5. 运行时层(internal/runtime/)

### 5.1 Gateway(gateway.go)

**结构:**

```go
type Gateway struct {
    mu        sync.RWMutex
    startMu   sync.Mutex                   // 串行化 Start / Stop / Register
    servers   map[core.Protocol]core.Server
    order     []core.Protocol              // 注册顺序
    rt        *Runtime
    timeouts  core.StageTimeouts
    startedAt atomic.Pointer[time.Time]
    started   atomic.Bool
}
```

**职责:**

1. `Register` 注册协议 Server;同一协议重复注册返回 `ErrDuplicateProtocol`,`Start` 之后再
   注册直接报错(必须先注册再启动).
2. `Start` 前向实现 `RuntimeConfigurable` 的 Server 注入 Runtime.
3. 先启动插件生命周期(`Runtime.Plugins().StartAll`),再按注册顺序启动各协议.
4. 任一 `srv.Start` 失败:逆序回滚已启动的 Server,再 `Plugins().StopAll()`,返回原始错误.
5. `Ready()` 直接读 `started`;`Health()` 返回 started / sessions / started_at / uptime /
   protocols.

**启动流程:**

```
Gateway.Start(ctx)
  -> startMu 加锁(与 Stop / Register 互斥)
  -> started 已为 true -> 返回 "gateway: already started"
  -> servers 为空 -> 返回 ErrNoServers
  -> 对 RuntimeConfigurable server 调 UseRuntime(g.rt)
  -> Runtime.Plugins().StartAll()(失败即返回,此时尚未启动任何 server)
  -> 按注册顺序 srv.Start(ctx)
       失败 -> 用 timeouts.Finalize 建独立 context,逆序 Stop 已启动 server
              再 Plugins().StopAll(),返回该错误
  -> startedAt = now;started = true
```

**停止流程(三阶段 + 收尾):**

```
Gateway.Stop(ctx)
  -> startMu 加锁;started 先置 false(整个关闭窗口内 Ready / readyz 都报未就绪)
  -> 阶段 1 StopAccept:  对每个 StagedServer 调 StopAccept
  -> 阶段 2 Drain:       对每个 StagedServer 调 Drain
  -> 阶段 3 CloseSessions:
       StagedServer    -> CloseSessions
       非 StagedServer -> Stop(ctx),共用 CloseSessions 超时
       最后 Runtime.Sessions().CloseAll(ctx)
  -> 收尾:Runtime.Plugins().StopAll()(错误记录日志并并入返回错误)
  -> startedAt = nil;返回首个错误(其余错误只记日志)

同一阶段内按注册顺序串行执行,阶段之间严格串行.runStage 为每个阶段派生子 context;
对应的超时字段 <= 0 时直接使用父 context.
```

**生命周期不变量:**

- `Start` 失败必须回滚,不留半启动状态.
- `Stop` 可重复调用;`Start -> Stop -> Start` 保持可用(共享 SessionManager 不永久关闭).
- `Ready()` 只表示 Gateway 成功启动,不代表外部依赖健康.
- 插件 `StopAll` 必须在所有会话关闭之后执行,避免后台循环与传输层拆除竞争.
- `CloseAll` 在 StopAccept / Drain / CloseSessions 之后执行,保证不会有新 session 逃逸清理.

### 5.2 SessionManager 实现(session_manager.go)

**当前实现(单锁,功能正确优先):**

```go
type SessionManager struct {
    mu       sync.RWMutex
    sessions map[uint64]core.Session
    idGen    atomic.Uint64
    total    atomic.Int64
    maxCount int64            // 0 = 不限制
}
```

`NewSessionManager(opts ...SessionManagerOption)`,目前只有一个选项
`WithMaxSessions(max int64)`(默认 1000000).`Register` 在超容时返回 `ErrSessionCapacity`,
重复 ID 返回 `ErrDuplicateSession`,参数非法返回 `ErrInvalidArgument`.

`Broadcast` 先持读锁做快照,释放锁后再逐个 `Send`,**只保留第一个错误返回**:广播是尽力
而为语义,单条会话失败不应掩盖其余会话的结果.

`CloseAll` 循环执行"快照 -> 关闭 -> 注销"直到集合为空或 ctx 取消,因此可以在 `Stop` 之后
再次调用而不报错,满足 `Start -> Stop -> Start` 的可重入约束.

**规划中(P2,尚未实现):** 分片锁 + per-shard LRU 的 `shardedManager` 只在 benchmark 证明
锁竞争成为瓶颈后才引入,当前代码中不存在该类型.

### 5.3 PluginChain 实现(plugin_chain.go)

```go
type PluginChain struct {
    mu      sync.RWMutex
    plugins []Plugin  // 按 Priority 稳定升序
    logger  Logger
}

func NewPluginChain(plugins ...Plugin) *PluginChain
func (c *PluginChain) Append(plugins ...Plugin)     // copy-on-write
func (c *PluginChain) SetLogger(logger Logger)       // 加锁写
func (c *PluginChain) snapshot() ([]Plugin, Logger)  // 取切片后在锁外执行回调
```

**注册阶段(Append 时,一次性排序):**

1. 复制现有切片,追加非 nil 插件,整体换入(copy-on-write,`c.plugins` 从不原地修改).
2. `slices.SortStableFunc` 按 `Priority()` 升序;同优先级保持 Append 顺序,跨运行可复现.
3. **不去重**:同名插件共存并都被执行,名称唯一性由调用方保证.

**copy-on-write 的三个收益:**

- `snapshot()` 可以直接交出切片引用,`OnMessage` 热路径零分配.
- 插件在回调内调用 `SetLogger` / `Append` 不会死锁(RWMutex 不可重入).
- 回调全程在锁外执行,只有取切片与 logger 时持锁.

**panic 隔离(五个钩子各一个包装):**

| 包装 | 钩子 | panic 后 |
|------|------|----------|
| `safeAccept` | `OnAccept` | 返回 `ErrPluginPanic`,链逆序回滚 |
| `safeMessage` | `OnMessage` | 返回 `ErrPluginPanic`,链终止 |
| `safeClose` | `OnClose` | 记录日志,继续下一个插件 |
| `safeStart` | `Start` | 返回 `ErrPluginPanic`,`StartAll` 回滚 |
| `safeStop` | `Stop` | 返回 `ErrPluginPanic`,`StopAll` 继续 |

`Start` / `Stop` 的包装是 V9 修复项:此前它们没有 recover,插件后台循环的启动 panic 会绕过
网关错误路径直接终止进程.

### 5.4 WorkerPool(transport/shared/worker_pool.go)

**位于 transport 层而非 runtime 层:** 池是传输层的背压设施,只在 shared 包中实现一次,
供任意传输层复用.**当前只有 TCP Server 实例化它**;UDP / CoAP / WebSocket 直接在各自
的 readLoop 上经 `shared.CallHandler` 内联调用 handler.

**三种队列满策略(`FullPolicy`):**

| 策略 | 行为 | 适用场景 | 风险 |
| --- | --- | --- | --- |
| `PolicyDrop` | 返回 `ErrWriteQueueFull`,消息丢弃(默认) | 通用场景,防雪崩 | 消息丢失 |
| `PolicyBlock` | 提交方阻塞,同时监听池停止与会话 Context | 不可丢消息场景 | readLoop 队首延迟 |
| `PolicyClose` | 返回 `ErrWriteQueueFull` 并关闭会话 | 极端过载保护 | 连接中断 |

设计草案中的 `PolicySpawnTemp`、`HandlerTimeout`、`OverloadWindow` 均**未实现**:
前者在雪崩时会让 goroutine 数量失控;后两者改由会话 Context 与调用方超时表达,
池不引入第二套并发语义.

**安全保证:**

- `Stop` 先 `CompareAndSwap` 置位并 `close(done)`,再取写锁等待全部 worker 退出,
  最后兜底排空.不关闭 `queue` channel,避免阻塞发送与 `Stop` 竞争时 panic.
- `PolicyBlock` 同时 select 队列、`done`、`sess.Context().Done()`:对端断连时提交方立即
  返回 `ErrSessionClosed`,不会为每条卡住的连接泄漏 goroutine.
- handler panic 由 `shared.CallHandler` 捕获,记录日志并返回 `ErrHandlerPanic`,
  随后关闭该会话,worker 继续消费队列.

---

## 6. 传输协议层(internal/transport/)

### 6.1 TCP 协议(transport/tcp/)

**Framer 接口(framer.go):**

```go
type Framer interface {
    // ReadFrame 从 r 读取一个完整帧,返回 payload.
    // 实现必须无状态(不跨调用保持预读缓冲),避免丢帧.
    ReadFrame(r io.Reader) ([]byte, error)

    // WriteFrame 将 payload 写入 w,必须保证完整写入(短写重试).
    WriteFrame(w io.Writer, payload []byte) error
}
```

**4 种内置 Framer:**

| Framer | 实现方式 | 适用场景 |
| --- | --- | --- |
| LengthPrefixFramer | 4 字节大端长度前缀,`io.ReadFull` 读头和 payload | 默认推荐,二进制协议 |
| LineFramer | 逐字节读到 `\n`,无 bufio 跨调用预读 | 文本行协议 |
| FixedSizeFramer | 固定长度帧,`io.ReadFull` | 硬件协议,传感器数据 |
| RawFramer | 直接透传,单次 `Read` | 简单场景,自行处理粘包 |

**注意**:`LengthPrefixFramer` 和 `LineFramer` 基于 `io.ReadFull` 或逐字节读,保持无状态接口语义,避免 `bufio.Reader` 跨调用预读丢弃同一 TCP 流中的后续帧.

**TCPSession 关闭 6 步状态机(session.go):**

```
步骤1:CAS Active -> Draining(失败说明已在关闭或已关闭,直接返回)
步骤2:若 writeLoop 已启动:close(draining) 信号触发排空
步骤3:等待 writeQueue 排空(等待受 ctx 约束,ctx 到期返回其 Err() 并强制继续)
步骤4:CAS Draining -> Closed
步骤5:CancelContext() -> 通知所有 <-ctx.Done() 的 goroutine 退出
步骤6:conn.Close()

若 writeLoop 未启动(连接在 Accept 后 Close 被调用):
  步骤2-3 跳过,直接释放待写 Buffer 后执行步骤4-6
```

**Send 并发安全:**

```go
func (s *TCPSession) Send(data []byte) error {
    if !s.IsAlive() {
        return ErrSessionClosed
    }
    // 复制调用方 data,防止 writeQueue 中的 buffer 被调用方复用覆盖
    buf := make([]byte, len(data))
    copy(buf, data)

    select {
    case s.writeQueue <- buf:
        return nil
    default:
        return ErrWriteQueueFull // 非阻塞,立即返回
    }
}
```

**TCPServer 完整配置(options.go):**

| 配置项 | 默认值 | 说明 |
| --- | --- | --- |
| Addr | `"0.0.0.0:18000"` | 监听地址 |
| WorkerCount | `NumCPUx2` | 核心 Worker 数量 |
| Addr | `127.0.0.1:18000` | 监听地址 |
| WorkerCount | `4` | 固定 worker 数 |
| TaskQueueSize | `512` | 任务队列容量 |
| FullPolicy | `PolicyDrop` | 队列满策略 |
| WriteQueue | `128` | 每连接写队列容量 |
| WriteQueueHighWater | `0.8` | 写队列高水位比例 |
| MaxConnections | `0`(不限制) | 并发连接上限 |
| AcceptRate | `0`(不限制) | 接受速率(连接/秒) |
| MaxFrameBytes | `1MB` | 单帧最大字节 |
| ReadTimeout | `5m` | 每帧读超时(0 关闭) |
| WriteTimeout | `30s` | 写超时 |
| DrainTimeout | `5s` | 关闭时写队列 drain 超时 |
| Framer | `LengthPrefixFramer` | 帧解析器 |
| TLSConfig | `nil` | TLS 配置(nil=不启用) |

`MaxSessions` / `MaxMessageSize` / `IdleTimeout` / `HandlerTimeout` /
`MaxConsecutiveErrors` / `ShutdownTimeout` 不在 TCP 选项里:会话上限由 `SessionManager`
统一持有,消息上限与帧解析合并为 `MaxFrameBytes` + `Framer`,空闲回收由 `ReadTimeout`
与 `HeartbeatPlugin` 表达,handler 超时由 handler 自身观察会话 Context,
关停超时由 `Gateway` 的 `core.StageTimeouts` 下发.

### 6.2 UDP 协议(transport/udp/)

**伪会话模型:**

```
remote UDPAddr -> 伪会话 -> 共享 SessionManager

伪会话创建时机(推荐方案B):
  1. 收到 datagram,提取 remote UDPAddr
  2. 构造轻量临时 session(仅含必要字段)
  3. OnAccept(tempSess):
     -> ErrPluginBlock:丢弃 datagram,不注册 session(不留计数)
     -> nil:Register(sess) -> 正式伪会话
  4. 后续 datagram 复用已注册的伪会话

好处:
  Manager 计数准确(Block 的连接不进入计数)
  与 HTTP Mode B 临时 session 模式一致
```

**ErrPluginDrop 语义:**

- 只丢弃当前 datagram,不删除伪会话,连接可继续接收后续 datagram.
- 与 ErrPluginBlock 的区别:Block 关闭伪会话,Drop 只丢弃一条消息.

**sweepLoop:**

```
每 sweepInterval(默认 30s)遍历所有伪会话:
  LastActiveAt + sessionTTL < now -> Close(sess) + Unregister
  TTL 默认 60s(可配置)
```

**UDP 发送:** 直接 `WriteToUDP`,无写队列(UDP 无序,无需串行化).

### 6.3 HTTP 协议(transport/http/)

**两种模式:**

| 模式 | 描述 | Session | Plugin |
| --- | --- | --- | --- |
| Mode A(默认) | 纯 `net/http` router | 无 | 无 |
| Mode B(可选) | per-request 临时 Session | HTTPSession | 执行完整插件链 |

**Mode B 流程:**

```
MaxBytesReader 限制请求体大小(超限 -> 413)
-> 创建 HTTPSession
-> Register(sess)
-> defer Unregister(sess.ID())
-> defer OnClose(sess)      <- 所有退出路径都触发 OnClose
-> OnAccept(sess)           -> ErrPluginBlock -> 403 + return
-> 读 Body
-> OnMessage(sess, body)    -> ErrPluginDrop -> 200 空响应
-> Handler(sess, msg)        -> error -> 500
-> (响应已在 Handler 中写入)
```

**注意**:`HTTPSession` 不注册到共享 `SessionManager`(per-request 语义,无持久会话).

**HTTPSession.Send:** `w.Write(data)`,`Close` 完成响应(`WriteHeader + Flush`).

### 6.4 WebSocket 协议(transport/websocket/)

**并发安全:**

`gorilla/websocket` 写操作非并发安全,所有写操作(Send / SendText / sendPing)必须加 `writeMu sync.Mutex`.

**OnClose 单次执行保证:**

```
OnClose 在 Close 内部通过 sync.Once 调用:
  closeOnce.Do(func() {
      OnClose(sess)          <- 在 Once 内部,天然保证单次执行
      manager.Unregister(id)
      cancelContext()
      conn.Close()
  })

两条并发退出路径(Gateway shutdown + read EOF)都通过 sess.Close() 触发,
sync.Once 保证 OnClose 只执行一次.
```

**ReadDeadline 动态设置:**

```
ReadDeadline = now + PingInterval + PongTimeout
PongHandler 收到 Pong 后:
  TouchActive()
  重设 ReadDeadline
```

**关键配置(options.go):**

| 配置项 | 默认值 |
| --- | --- |
| PingInterval | `30s` |
| PongTimeout | `10s` |
| MaxMessageSize | `1MB` |
| AllowedOrigins | `[]`(空列表=拒绝所有跨域,生产必须配置) |
| UpgradeTimeout | `5s` |

### 6.5 CoAP 协议(transport/coap/)

**CoAP 帧结构(message.go):**

```go
type Message struct {
    Version   uint8       // 必须为 1
    Type      MsgType     // CON(0) / NON(1) / ACK(2) / RST(3)
    TokenLen  uint8       // TKL <= 8
    Code      Code        // 方法码或响应码
    MessageID uint16      // 消息去重 ID
    Token     []byte      // 关联请求/响应(len == TokenLen)
    Options   []Option    // Uri-Path / Content-Format 等
    Payload   []byte
}

帧校验:
  len(raw) >= 4
  TKL <= 8
  Version == 1
  Token 实际长度与 TKL 一致
```

**CON 可靠性(RFC 7252 sec.4.2):**

```
收到 CON:
  CheckAndRecord(msgID) 去重:
    -> 重复:GetCachedResponse -> 重发缓存 ACK(避免重复执行 Handler)
    -> 首次:OnMessage -> Handler -> 选择 ACK Code -> Send ACK -> CacheResponse(msgID, ack)

收到 RST:
  ResetCON(msgID) 取消对应重传

ACK Code 选择:
  GET     -> 2.05 Content
  POST    -> 2.01 Created
  PUT     -> 2.04 Changed
  DELETE  -> 2.02 Deleted
  其他    -> 2.05 Content

Message ID 去重:最近 500 条 LRU 缓存(可配置)
```

**当前边界:** CON 基础 ACK 和去重已实现;Block-wise,Observe,完整 option 编解码和重传状态机为后续目标(P1 协议增强).

**CoAP retransmitLoop 活跃索引(ADR 修复的死锁问题):**

```
错误方案:retransmitLoop 持 pendingACKs 锁遍历,超时删除也需同锁 -> 死锁
正确方案:维护独立 activeIDs []uint16 索引,retransmitLoop 只读 activeIDs,
         删除时先释放遍历锁再获取写锁(或使用独立 cleanup channel)
```

### 6.6 LwM2M 协议(internal/protocol/lwm2m/)

**注意**:LwM2M 位于 `internal/protocol/`(应用协议层),不是 `internal/transport/`(传输层).它基于 CoAP transport 提供设备管理语义.

**Server 职责:**

- 维护 endpoint 注册表(线程安全).
- 支持 register / update / deregister 生命周期.
- 管理 object/resource path(`/OID/IID/RID`).
- 支持 resource read / write / execute.
- lifetime expiry sweep(每 `lifetimeCheckInterval` 扫描过期注册).

**注册状态不变量(防浅拷贝污染):**

```go
// Registration() 和 Registrations() 返回深拷贝,调用方修改不影响内部状态
func (s *Server) Registration(endpoint string) (Registration, bool)
func (s *Server) Registrations() []Registration

// WithClientObjects 保留调用方 attributes map 的深拷贝
// Client.AddResource 保留调用方内容切片的深拷贝
```

**当前 CoAP 文本命令绑定(responder.go):**

```
register <endpoint> <lifetime-seconds> [object-path...]
update <endpoint> <lifetime-seconds>
deregister <endpoint>
write <endpoint> <resource-path> <value>
read <endpoint> <resource-path>
```

**后续目标(P1):** 正式 LwM2M URI/Query/Content-Format 编解码,Observe/Notify,Bootstrap.

### 6.7 QUIC 协议(transport/quic/)

**强制约束:** 没有 TLS config 不允许启动(`Start` 返回配置错误).

**Stream 生命周期:**

```
连接建立 -> 注册 QUICSession(连接维度)
Stream 到达 -> handleStream:
  读取 stream payload(MaxMessageSize 限制)
  超限:不调用 Handler,关闭 stream,返回 core.ErrFrameTooLarge
  正常:OnMessage -> Handler(Handler 可写回 stream)
  Stream 关闭 -> 不注销 QUICSession(连接仍存在)
连接断开 -> 注销 QUICSession,OnClose

stream 尾块处理:
  Read 可能同时返回 n>0 和 io.EOF(尾块场景)
  必须先处理 n>0 的 payload,再处理 err==io.EOF
```

**QUICSession 写队列 payload 切片别名问题:**

```
Send(data []byte) 必须复制 data,防止异步 writeLoop 发送时 data 被调用方复用覆盖:
  buf := make([]byte, len(data))
  copy(buf, data)
  select { case s.writeQueue <- buf: ... }
```

**Stop 幂等:** 未 Start 时调用 Stop 应安全返回 nil,不触发 manager/listener 空指针.

### 6.8 gRPC-Web 协议(transport/grpcweb/)

**两种入口模式:**

| 模式 | 入口 | Session |
| --- | --- | --- |
| Direct HTTP | HTTP POST | per-request 临时 Session |
| WebSocket | WS 长连接 | 持久 Session |

**MaxMessageBytes 约束:**

```
Direct 模式:超限 -> 413(请求体截断返回)
WebSocket 模式:超限 -> 在进入 Handler 前关闭连接,记录错误
配置值为负数或超出合理范围 -> 拒绝启动(app 层统一验证)
```

**OnClose 清理完整性:**

```
Direct 模式:所有返回路径(正常/超限/插件错误/Handler 错误)必须触发 OnClose
  使用 defer 保证:
    defer func() {
        OnClose(sess)
        Unregister(sess.ID())
    }()

WebSocket 模式:
  读循环退出后:Unregister -> sess.Close()(触发 OnClose via sync.Once)
```

**gRPC-Web 帧格式(frame.go):**

```
Data frame:    [0x00][4字节大端长度][payload]
Trailer frame: [0x80][4字节大端长度][trailer 键值对]
畸形帧:严格返回 400,不静默跳过
```

---

## 7. 插件层(internal/plugin/)

### 7.1 内置插件与推荐 Priority

| Priority | 插件 | 职责 |
| --- | --- | --- |
| 0 | BlacklistPlugin | IP/CIDR 黑名单(最高优先级,最早拦截) |
| 10 | RateLimitPlugin | 令牌桶限流(连接 + 消息双层) |
| 20 | AutoBanPlugin | 自动封禁(触发限流/错误阈值 -> 加入黑名单) |
| 30 | HeartbeatPlugin | 心跳超时检测(P0:ticker 扫描;P2:时间轮) |
| 40 | ClusterPlugin | 集群事件广播 + 跨节点路由 |
| 50 | PersistencePlugin | 会话状态异步持久化 |
| 60 | SlowHandlerPlugin | 记录慢处理调用(Handler 耗时超阈值) |

### 7.2 BlacklistPlugin(Priority=0)

```
存储结构:
  exactMap  map[string]time.Time  // 精确 IP -> 过期时间,O(1) 查找
  cidrList  []net.IPNet           // CIDR 段,顺序遍历
  cache     Cache                 // 分布式黑名单缓存(Cache Miss 降级本地查找)

OnAccept:提取纯 IP -> exactMap O(1) -> CIDR 遍历 -> Cache 双查
动态管理:Add(ip, ttl) / Remove(ip) / Reload(list)
TTL 过期:惰性过期(查询时检查)+ 后台 cleanupLoop(每分钟)
```

### 7.3 RateLimitPlugin(Priority=10)

```
双层令牌桶:
  globalBucket  *tokenBucket              // 全局速率上限
  perIPBuckets  sync.Map                  // per-IP 独立桶(CompareAndDelete 清理,Go 1.26)

算法:按实际时间差补充令牌 -> 检查 >= 1 -> 原子扣减
OnAccept:连接速率限流 -> ErrPluginBlock(超限返回 ErrRateLimited)
OnMessage:消息速率限流 -> ErrPluginDrop(超限丢弃消息)
后台:cleanupLoop 每 2 分钟清理空闲 IP 桶(使用 sync.Map.CompareAndDelete)
连续触发 N 次 -> 通知 AutoBanPlugin(通过共享计数器或 channel)
```

### 7.4 HeartbeatPlugin(Priority=30)

**P0 实现(ticker 扫描):**

```go
// 后台 goroutine 每 checkInterval 扫描 SessionManager
// LastActiveAt + idleTimeout < now -> sess.Close()
// 开销:O(N) 每次扫描,10万连接时约 1ms(可接受)
```

**P2 演进(时间轮,benchmark 证明扫描成为瓶颈后引入):**

```
TimeWheel:单 goroutine 管理全部会话定时器
10万连接仅需 1 个系统 goroutine(替代 N 个 ticker)
精度:1 秒(slot 间隔),轮大小 = ceil(timeout/slot)
Add/Remove/Reset 均 O(1)
OnMessage 触发 timeWheel.Reset(sess.ID())
```

### 7.5 PersistencePlugin(Priority=50)

```
OnAccept:Store.Load(sess.ID()) -> sess.SetMeta("history", data)
OnMessage:序列化 -> writeCh(有界 channel,容量 1024)
         -> 后台 batchWriter(每 100 条或 500ms flush)
OnClose:同步 Store.Save(最终快照)

CircuitBreaker 包裹 Store 调用,Store 不可用时跳过(记录 Warn)
幂等保护:Close() 通过 sync.Once 确保最终快照只写一次
```

### 7.6 ClusterPlugin(Priority=40)

```
OnAccept:
  Cache.Set("session:route:"+sessID, nodeID, sessionTTL)
  PubSub.Publish("cluster.session.joined", {sessID, nodeID})

OnClose:
  Cache.Del("session:route:"+sessID)
  PubSub.Publish("cluster.session.left", {sessID})

跨节点路由(业务层调用):
  本地 Manager.Get(targetID) -> 未找到
  -> Cache.Get("session:route:"+targetID) -> nodeID
  -> PubSub.Publish("node."+nodeID+".route", {targetID, payload})

节点心跳:每 heartbeatTTL/2 执行 Cache.Set("node:"+nodeID, meta, heartbeatTTL)
CircuitBreaker 包裹 PubSub 和 Cache 调用(不可用时静默丢弃)
```

---

## 8. 应用装配层(internal/app/)

### 8.1 配置结构(config.go)

**加载顺序:** 内置默认值 -> JSON 配置文件 -> 环境变量覆盖(优先级从低到高)-> `Validate()`。

**顶层 Config:**

| 字段 | JSON 键 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `ShutdownTimeout` | `shutdown_timeout` | string(duration) | `10s` | 优雅关闭总超时,必须为正 |
| `HealthAddr` | `health_addr` | string | `127.0.0.1:18081` | 健康检查 HTTP 地址 |
| `MetricsAddr` | `metrics_addr` | string | `127.0.0.1:18080` | Prometheus metrics 地址 |
| `Protocols` | `protocols` | []ProtocolConfig | 一个默认 tcp | 协议监听列表 |

**ProtocolConfig:**

| 字段 | JSON 键 | 类型 | 说明 |
| --- | --- | --- | --- |
| `Name` | `name` | string | `tcp/udp/http/websocket/coap/quic/grpc-web` |
| `Enabled` | `enabled` | *bool | nil 视为 true(`IsEnabled()`) |
| `Addr` | `addr` | string | 监听地址 |
| `Path` | `path` | string | WebSocket / gRPC-Web 的 HTTP path |
| `Mode` | `mode` | string | CoAP 传 `lwm2m` 时接入 LwM2M responder |
| `MaxMessageBytes` | `max_message_bytes` | int64 | 消息大小上限,不得为负 |
| `TLSCertFile` | `tls_cert_file` | string | 服务端证书;与 key 必须成对 |
| `TLSKeyFile` | `tls_key_file` | string | 服务端私钥 |
| `TLSClientCAFile` | `tls_client_ca_file` | string | mTLS 客户端 CA bundle |
| `TLSClientAuth` | `tls_client_auth` | string | `none/request/require_any/verify_if_given/require_and_verify` |
| `TLSMinVersion` | `tls_min_version` | string | `1.2`(默认)/ `1.3` |
| `AllowedOrigins` | `allowed_origins` | []string | WebSocket / gRPC-Web 的 Origin 白名单 |

**`Validate()` 规则(app 层统一执行,不在 Server.Start 里执行):**

```
shutdown_timeout 可解析且 > 0
至少有一个 enabled 协议;protocol name 非空且在已知集合内且不重复
enabled 协议 addr 非空
max_message_bytes >= 0
tls_cert_file 与 tls_key_file 必须同时给或同时不给
tls_client_ca_file / tls_client_auth 需要 cert+key;tls_client_auth 取值必须合法
tls_cert_file / tls_client_ca_file / tls_client_auth 仅支持 tcp / quic / coap / udp
quic 强制要求 tls_cert_file + tls_key_file
```

环境变量覆盖:`SHARK_CONFIG`(配置路径)、`SHARK_SHUTDOWN_TIMEOUT`、`SHARK_HEALTH_ADDR`、
`SHARK_METRICS_ADDR`,以及按协议的 `SHARK_TCP_*` / `SHARK_WS_*` / `SHARK_HTTP_*` /
`SHARK_GRPCWEB_*` / `SHARK_QUIC_*`。环境变量通过 `upsertProtocol` 与 JSON 中的同名协议
按字段合并.

### 8.2 App 装配(app.go)

```go
type App struct { /* 未导出字段 */ }

func New(cfg Config) (*App, error)     // 装配,不启动
func (a *App) Start(ctx context.Context) error
func (a *App) ServeErrors() []error    // 健康 / 指标服务的异步错误
func (a *App) Stop(ctx context.Context) error
```

**装配流程(`New`):**

```
app.New(cfg)
  1. cfg.Validate()(统一校验,单次)
  2. api.NewGateway(注入 plugins / logger / metrics / tracer)
  3. 逐个 enabled 协议 registerProtocols:
       构造 Server Options
       需要 TLS 时用 loadServerTLSConfig 建 CertCache(证书 + 客户端 CA 池)
       调 gateway.Register(server)
  4. 起证书热加载 watcher(tlsutil.WatchFilesWithWG)
  5. 建健康 / 指标 http.Server(healthHandler + PrometheusMetrics)
```

**运行流程(`cmd/shark-socket/main.go`):**

```
flag -config / env SHARK_CONFIG -> app.LoadConfig
app.New(cfg)
ctx, stop := signal.NotifyContext(SIGINT, SIGTERM)
app.Start(ctx)
<-ctx.Done()
shutdownCtx := WithTimeout(cfg.ShutdownDuration())
app.Stop(shutdownCtx)
```

关闭顺序是"先停止接客再清理":`App.Stop` 调 `Gateway.Stop`(三阶段 + 插件收尾),
随后关闭健康 / 指标 HTTP 服务.


---

## 9. 可观测设计

### 9.1 Prometheus 指标

**注册模型:按需首写注册,不是静态预注册.** `PrometheusMetrics` 与 `MemoryMetrics` 共用
`core.Metrics` 的三方法契约,首次以某个 `name+labels` 写入时建立条目,之后按 key 自增;
没有启动期预注册的指标名单,也没有"锁定标签集合"的机制(这是早先草案的描述,与实现不符).

**当前代码实际写入的指标名(全部来自调用点):**

| 指标 | 类型 | 写入点 | 说明 |
| --- | --- | --- | --- |
| `sessions_accepted_total` | Counter | `runtime/session_metrics.go` | 会话注册总数 |
| `sessions_closed_total` | Counter | `runtime/session_metrics.go` | 会话注销总数 |
| `sessions_active` | Gauge | `runtime/session_metrics.go` | 当前活跃会话数 |
| `sessions_reclaimed_total` | Counter | TCP / UDP / CoAP / WS / gRPC-Web server | 伪会话/空闲会话回收数 |
| `tcp_task_queue_full_total` | Counter | `transport/tcp/server.go` | TCP 任务队列满次数 |

**导出:** `PrometheusMetrics` 自身实现 `http.Handler`(`ServeHTTP`),输出 Prometheus 文本
格式;`ExportText()` 返回同样的字符串,便于测试断言.`/metrics` 端点即挂载该 handler.

**标签约定:** 标签以 `labels ...string` 传入,键值交替;`metricKey` 把
`name + 标签串`拼成 map key,标签名经 `sanitizeLabelName` 规范化.当前调用点基本只传
指标名、不传标签,因此实际的标签维度远小于早期草案设想的规模.

### 9.2 健康检查端点

```
GET /healthz -> 进程存活(200 = 存活)
响应体(P1 扩展):
{
  "status": "healthy | degraded",
  "uptime": "2h30m15s",
  "protocols": {"tcp": true, "websocket": true, "coap": false},
  "sessions": 12345
}

GET /readyz -> Gateway Ready 状态
响应体:
  200: {"status": "ready"}
  503: {"status": "not_ready", "reason": "gateway not started | gateway stopping"}

GET /metrics -> Prometheus text format(包含全部 shark_* 指标)
```

### 9.3 Tracing

```
context.Context 贯穿全链路:
  Accept -> OnAccept -> OnMessage -> Handler -> sess.Send

Span 注入点(兼容 OpenTelemetry):
  transport.accept     -> 连接建立
  plugin.on_accept     -> 插件链 accept
  plugin.on_message    -> 插件链消息处理(per plugin)
  handler.execute      -> Handler 执行
  session.send         -> 写队列投递

当前实现:ctx 携带 request_id(UUID 格式)/ trace_id
后续扩展:用户注入 OTel TracerProvider,框架不引入强依赖
```

---

## 10. 基础设施层(internal/infra/)

实现直接暴露具体类型(`*MemoryCache`、`*PubSub`、`*Breaker`),只对需要替换的部分抽接口.

### 10.1 Cache(internal/infra/cache/cache.go)

```go
type Cache interface {
    Get(string) ([]byte, bool)
    Set(string, []byte, time.Duration)
    Delete(string)
    Has(string) bool
}
```

**MemoryCache:** `Set` 深拷贝调用方 `[]byte`,`Get` 返回副本;TTL 惰性过期 + 后台清理.
接口刻意不带 `context` 与 `error`:未命中是正常分支(`bool` 返回),缓存故障不该逼调用方
多写一层错误处理.

### 10.2 Store(internal/infra/store/)

```go
type Store interface {
    Save(bucket, key string, value []byte) error
    Load(bucket, key string) ([]byte, bool, error)   // 未找到 -> (nil, false, nil)
    Delete(bucket, key string) error
    List(bucket string) ([]string, error)
    Close() error
}
```

| 实现 | 文件 | 说明 |
| --- | --- | --- |
| `MemoryStore` | `store.go` | 进程内 bucket/key 存储 |
| `BoltStore` | `bolt.go` | bbolt 落盘;`withLock` 串行化写,`DeleteBatch` 批量删 |
| `SessionStore` | `session_store.go` | `SaveSnapshot` / `LoadSnapshot` / `ListSnapshots` / `DeleteSnapshot` |
| `MessageLog` | `message_log.go` | `Append` / `Replay` / `Prune` / `Len`,单调序号日志 |

### 10.3 PubSub(internal/infra/pubsub/pubsub.go)

```go
type Message struct {
    Topic string
    Data  []byte
}

type PubSub struct { /* 未导出字段 */ }

func New() *PubSub
func (p *PubSub) Subscribe(topic string, buffer int) (<-chan Message, func())
func (p *PubSub) Publish(topic string, data []byte) int
func (p *PubSub) Dropped(topic string) uint64
```

`Subscribe` 返回只读 channel 与取消函数;`Publish` 返回成功投递的订阅者数量.订阅者缓冲满
时消息被丢弃并计入 `Dropped(topic)`,发布方永不被阻塞(集群广播不能反压业务路径).

### 10.4 CircuitBreaker(internal/infra/circuitbreaker/circuitbreaker.go)

```
状态(State string):
  closed     Allow() 放行;连续失败 > threshold -> open
  open       Allow() 返回 ErrOpen;超过 timeout -> half-open
  half-open  只放行一次探测;Success() -> closed;Failure() -> open 并刷新 openedAt

API:
  New(threshold int, timeout time.Duration) *Breaker   // threshold<=0 -> 1, timeout<=0 -> 1s
  Allow() error
  Success()
  Failure()
  Snapshot() Snapshot    // State / OpenedAt / ...

应用场景:PersistencePlugin(Store)/ ClusterPlugin(PubSub)
```

### 10.5 Observability(internal/core/ + internal/infra/observability/)

| 组件 | 位置 | 说明 |
| --- | --- | --- |
| `slogLogger` | `internal/core/observability.go` | 未导出;`core.NewSlogLogger(*slog.Logger)` 构造,`attrs` 直接透传 slog |
| `NopLogger` / `NopMetrics` / `NopTracer` | `internal/core/observability.go` | 默认注入的空实现 |
| `MemoryLogger` | `internal/infra/observability/logger.go` | 测试用,`Entries()` 取回日志 |
| `MemoryMetrics` | `internal/infra/observability/metrics.go` | 进程内聚合,单指标观测数上限 65536 |
| `PrometheusMetrics` | `internal/infra/observability/prometheus.go` | 实现 `http.Handler`,输出文本格式 |
| `OpenTelemetryTracer` | `internal/infra/observability/otel.go` | 包装 `trace.Tracer` |

**P2 候选组件(当前均未实现):**

| 组件 | 触发条件 | 计划位置 |
| --- | --- | --- |
| 多级 BufferPool | benchmark 证明 readLoop alloc 为瓶颈 | `internal/infra/bufferpool/` |
| 时间轮 | benchmark 证明心跳扫描为瓶颈 | `internal/infra/timewheel/` |
| 日志采样器 | 高频日志场景压测发现日志成为瓶颈 | `internal/infra/logsampler/` |


---

## 11. Gateway 数据流

### 11.1 TCP 完整处理流

```
网络 -> readLoop(每连接 1 goroutine):
  io.ReadFull / 逐字节读 -> Framer.ReadFrame(conn)
  -> sess.TouchActive()(原子更新 lastActiveAt)
  -> WorkerPool.Submit(sess, payload)

Worker goroutine(隔离 panic):
  PluginRunner.OnMessage(sess, payload)   // PluginChain 的入口
    -> BlacklistPlugin(0) -> RateLimitPlugin(10) -> AutoBanPlugin(20)
    -> HeartbeatPlugin(30) -> ClusterPlugin(40) -> PersistencePlugin(50)
    -> ErrPluginDrop -> 停止,不调用 Handler
    -> ErrPluginBlock -> 停止,Close(sess)
  -> Handler(sess, Message) -> 业务逻辑
    -> sess.Send(response) -> 非阻塞写入 writeQueue

writeLoop(每连接 1 goroutine,单 goroutine 独占写):
  for data := range writeQueue:
    完整写入 conn(短写重试)-> 写完成
  writeQueue 关闭 -> drain 完成 -> goroutine 退出
```

### 11.2 连接生命周期

```
listener.Accept()
  -> newTCPSession(conn)
  -> manager.Register(sess)   -> 超容:ErrSessionCapacity -> 拒绝连接
  -> OnAccept(sess)          -> ErrPluginBlock:Close(sess) + return
  -> go sess.readLoop()
  -> go sess.writeLoop()

[运行中]
  readLoop -> Framer -> OnMessage -> WorkerPool -> Handler -> sess.Send
  writeLoop -> writeQueue -> conn.Write

[断开 / 超时 / 错误]
  readLoop 退出时 defer:
    sess.Close(ctx):
      CAS Active -> Draining
      close(draining) -> writeLoop drain
      等待 drain(DrainTimeout)
      CAS Draining -> Closed
      CancelContext()
      conn.Close()
    OnClose(sess)(逆序,via sync.Once)
    manager.Unregister(sess.ID())
```

### 11.3 类型化消息处理流

```
网络帧 -> []byte -> core.Message
  v
AdaptTyped[M](handler, codec)(在 application 层装配)
  v
codec.Decode(payload) -> M
  v
TypedHandler[M](sess, typedMsg) -> 业务逻辑
  v
codec.Encode(response) -> []byte
  v
sess.Send([]byte)
```

### 11.4 跨节点路由(集群模式)

```
节点1 业务 Handler:
  manager.Get(targetID) -> 未找到(跨节点)
  -> Cache.Get("session:route:"+targetID) -> nodeID="node2"
  -> PubSub.Publish("node.node2.route", {targetID, payload})

节点2 ClusterPlugin(后台订阅):
  PubSub.Subscribe("node.node2.route", func(msg))
  -> 解析 {targetID, payload}
  -> localManager.Get(targetID) -> found -> sess.Send(payload)
  -> not found -> Warn(session 已迁移或下线)
```

---

## 12. 并发模型

```
+------------------------------------------------------------------+
| cmd/shark-socket Main Goroutine                                   |
| Run: app.Build -> app.Run -> signal.NotifyContext -> Stop           |
+------------------------------------------------------------------+
| Protocol Acceptors(每协议 1 goroutine)                          |
|   TCP:acceptLoop(listener.Accept -> go handleConn)             |
|   UDP:readLoop(单 goroutine 复用 UDPConn)                     |
|   WS:net/http Serve(内部 goroutine 池)                        |
|   CoAP:readLoop(基于 UDP)                                     |
|   QUIC:acceptLoop(quic.Listener.Accept)                       |
+------------------------------------------------------------------+
| WorkerPool(固定 NumCPUx2 Worker,仅 TCP 使用)                     |
|   for task := range taskQueue -> shared.CallHandler(handler)     |
+------------------------------------------------------------------+
| Per-Session Goroutines:                                          |
|   TCP:readLoop + writeLoop(每连接 2 个)                       |
|   WS: readLoop + writeLoop + pingLoop(每连接 3 个)            |
|   UDP:无 per-session goroutine(复用 acceptor readLoop)        |
|   QUIC:per-stream goroutine(每 stream 1 个读 goroutine)       |
+------------------------------------------------------------------+
| 系统 Goroutine(固定数量,与连接数无关):                        |
|   HeartbeatPlugin:ticker 扫描(P0)/ 时间轮(P2)              |
|   RateLimitPlugin:cleanupLoop(清理空闲 IP 桶)                 |
|   BlacklistPlugin:cleanupLoop(清理过期 IP)                    |
|   PersistencePlugin:batchWriter(批量刷盘)                     |
|   ClusterPlugin:订阅 goroutine + 节点心跳 goroutine            |
|   SessionManager:sweepLoop(UDP/CoAP 伪会话 TTL 清理)          |
|   MemoryCache:cleanupLoop                                       |
|   Metrics HTTP Server                                            |
|   Health HTTP Server                                             |
|   slogLogger:异步写入 goroutine                                 |
+------------------------------------------------------------------+

规模估算:
  10K TCP 连接,16 Workers -> ~20K+30 goroutines
  100K TCP 连接,32 Workers -> ~200K+35 goroutines
```

---

## 13. 安全与防御设计

### 13.1 当前已具备(P0)

- WebSocket / gRPC-Web Origin Check(AllowedOrigins 白名单).
- HTTP / gRPC-Web body / message size 限制.
- QUIC 强制 TLS.
- Docker / K8s 非 root 和最小权限.
- Blacklist / RateLimit / AutoBan 插件基础能力.
- MaxSessions 硬上限.

### 13.2 六层连接防御(P0-P2 逐步补齐)

```
L1(OS,部署时配置):
  net.core.somaxconn = 65535
  net.ipv4.tcp_syncookies = 1
  fs.file-max = 1048576

L2(MaxSessions 硬上限):
  count >= maxSessions -> ErrSessionCapacity -> 拒绝连接 + metrics

L3(RateLimitPlugin,P0):
  双层令牌桶:全局 + per-IP
  超限连接:ErrPluginBlock,超限消息:ErrPluginDrop

L4(BlacklistPlugin,P0):
  精确 IP O(1) + CIDR 遍历 + Cache 双查 + TTL 自动过期

L5(AutoBanPlugin,P0):
  限流超 N 次 / 协议错误超 M 次 -> 自动加入黑名单(banTTL 默认 30 分钟)

L6(慢连接防御,P1):
  ReadTimeout + IdleTimeout(防 Slowloris)
  HeartbeatPlugin 超时断开
  MinDataRate(可选,最少每秒字节数)
```

### 13.3 消息层防御

```
大包攻击:
  TCP:MaxMessageSize(LengthPrefixFramer 检查帧长度,超限返回 ErrFrameTooLarge)
  WS:conn.SetReadLimit(MaxMessageSize)
  UDP:物理限制 65535B,实际约 1500B(MTU)
  QUIC:MaxMessageSize 限制 stream 读取

小包高频:
  消息速率令牌桶(RateLimitPlugin OnMessage)
  连续触发 -> AutoBan

畸形帧:
  Framer decode recover(panic) + 连续错误计数 -> MaxConsecutiveErrors 断连
  CoAP 帧严格校验(len/TKL/Version/Token)
  gRPC-Web 畸形帧返回 400

WorkerPool 背压(四种策略):
  PolicyDrop(默认)/ PolicyBlock / PolicyClose
```

### 13.4 P1 必须补齐的安全项

1. TLS/mTLS 配置文件化(TCP / QUIC).
2. TCP / QUIC TLS 证书热加载(SIGHUP 信号 -> 重新加载证书,不中断已有连接).
3. QUIC 证书轮换和热加载.
4. 请求级 deadline 和 idle timeout 统一配置.
5. 敏感配置字段(证书路径,密钥内容)不进入日志和审查报告.

### 13.5 P2 防御体系

1. CoAP DTLS 或 OSCORE 方案设计.
2. OverloadProtector(水位检测 + 降级矩阵).
3. BackPressure(写队列水位监控).
4. FD 使用率监控(`/proc/self/fd` 计数).

**优雅降级矩阵(P2):**

| 触发条件 | 降级动作 |
| --- | --- |
| Sessions > HighWater | 拒绝新连接 + 加速 LRU 淘汰 |
| WorkerPool 使用率 > 90% | 消息丢弃(PolicyDrop) |
| WorkerPool 满载持续 30s | 主动关闭最慢连接(PolicyClose) |
| Memory > 80% | 跳过 PersistencePlugin + 拒绝新连接 |
| PubSub 不可用 | 集群事件静默丢弃(CircuitBreaker 保护) |
| Store 不可用 | PersistencePlugin 跳过(CircuitBreaker 熔断) |

---

## 14. 性能演进路线

### 14.1 长期性能目标

| 指标 | 目标值 | 实现手段 |
| --- | --- | --- |
| TCP 吞吐 | >= 100K msg/s(单核) | WorkerPool + 写队列 + BufferPool(P2) |
| 连接延迟 | P99 <= 1ms | 无锁热路径 + atomic + 单 goroutine 写 |
| 内存分配 | 热路径 0 alloc | sync.Pool + BufferPool(P2) |
| 并发连接 | >= 100K | 分片 SessionManager(P2)+ LRU 淘汰 |
| 插件开销 | <= 200ns/hop | 静态排序列表 + 接口内联 |

### 14.2 benchmark 驱动演进步骤

```
Step 1:建立基准(P0 完成后立即执行)
  BenchmarkTCPEcho          单核 Echo 吞吐基线
  BenchmarkSessionRegister  并发 Register/Get 基线
  BenchmarkPluginChain      每 hop 延迟基线(0/1/5/10 插件)
  BenchmarkBroadcast        10K session 广播延迟基线

Step 2:定位热点(pprof 驱动)
  go tool pprof -> 通常热点:readLoop alloc,Manager 锁竞争

Step 3:按热点引入优化(不提前优化)
  alloc 热点 -> 引入 BufferPool(internal/infra/bufferpool/)
  锁竞争热点 -> SessionManager 升级为 32 分片锁 + per-shard LRU
  定时扫描热点 -> HeartbeatPlugin 升级为时间轮

Step 4:验证优化效果
  对比 benchmark(count=5,取中位数)
  race detector 验证无竞态
  pprof 确认热点消除
  长时间运行验证无内存泄漏(heap 增长趋势)
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
| CI | GitHub Actions(Windows + Ubuntu) | 跨平台回归 |

### 15.2 测试编写规范

- **端口:** 所有协议测试使用 `127.0.0.1:0`(内核分配),禁止固定端口(防冲突).
- **缺陷回归:** 每个生产缺陷先写最小失败测试,修复后保留.
- **并发测试:** 所有涉及 Session / PluginChain / SessionManager 的测试必须使用 `-race` 验证.
- **云端验证:** 实机测试结果写入 `docs/reports/PROJECT-REVIEW-YYMMDD-HHMMSS.md`.
- **部署测试:** 检查语义(字段值,安全配置),不只检查文件存在.

### 15.3 Fuzz 测试目标

**TCP Framer(5 个目标):**

| 目标 | 验证点 |
| --- | --- |
| FuzzLengthPrefixFramer | 任意字节输入不 panic |
| FuzzLineFramer | 任意字节输入不 panic |
| FuzzFixedSizeFramer | 任意字节输入不 panic |
| FuzzRawFramer | 任意字节输入不 panic |
| FuzzLengthPrefixRoundtrip | WriteFrame -> ReadFrame 往返一致性 |

**CoAP(2 个目标):**

| 目标 | 验证点 |
| --- | --- |
| FuzzParseMessage | 任意字节解析不 panic,合法结果往返验证 |
| FuzzCoAPRoundtrip | 构造消息 -> Serialize -> Parse 往返一致性 |

### 15.4 关键回归测试清单

**必须持续维护的回归测试:**

```
Gateway 层:
  [ ] Start -> Stop -> Start 复用 SessionManager(生命周期可重入)
  [ ] Gateway 启动部分失败逆序回滚(已启动的 Server 被停止)
  [ ] Gateway.Stop() 可重复调用(幂等)
  [ ] CloseAll 在 StopAccept 完成后执行(不遗漏 session)

WebSocket 层:
  [ ] OnClose 只执行一次(并发路径:Gateway shutdown + read EOF)
  [ ] Ping-Pong 超时断开(PongTimeout 内未收到 Pong)
  [ ] AllowedOrigins 空列表拒绝跨域(返回 403)

CoAP 层:
  [ ] 重复 CON 消息不重复执行 Handler(MessageID 去重)
  [ ] retransmitLoop 不产生自锁死锁(活跃索引方案)

TCP 层:
  [ ] Plugin Drop 后连接仍可复用(后续消息正常处理)
  [ ] LengthPrefixFramer 不因短写丢失后续帧(完整写入保证)
  [ ] 并发 Close 幂等(sync.Once)
  [ ] writeLoop 未启动时 Close 安全(不等待 drain timeout)

gRPC-Web 层:
  [ ] max_message_bytes 非法值拒绝启动(配置验证)
  [ ] Direct 模式所有返回路径触发 OnClose
  [ ] WebSocket 模式读循环退出触发 OnClose

UDP 层:
  [ ] ErrPluginDrop 不删除伪会话(后续 datagram 可继续)
  [ ] ErrPluginBlock 不注册伪会话(Manager 计数准确)

插件层:
  [ ] PersistencePlugin.Close() 重复调用不 panic(sync.Once)
  [ ] ClusterPlugin.Close() 重复调用不 panic(sync.Once)
  [ ] PluginChain panic 隔离(单插件 panic 不影响其他插件)
  [ ] OnClose 逆序执行(单插件 panic 不中断后续插件)

配置层:
  [ ] max_message_bytes < 0 拒绝启动
  [ ] tls_cert_file 和 tls_key_file 必须成对出现
  [ ] 协议 addr 格式非法拒绝启动
```

### 15.5 基准测试目标

```
BenchmarkTCPEcho             -> >= 100K msg/s(单核,长期目标)
BenchmarkSessionRegister     -> 并发 Register/Get CRUD(分片锁效果对比)
BenchmarkPluginChain         -> < 200ns/hop(5 插件链)
BenchmarkBroadcast           -> 10K session 广播延迟
BenchmarkBufferPool          -> < 10ns/op,0 alloc(P2 引入后)
BenchmarkHeartbeatScan       -> P0 ticker vs P2 时间轮对比
BenchmarkLRUTouch            -> O(1) 验证(P2 引入后)
```

---

## 16. 部署架构

### 16.1 Docker(deploy/docker/)

**多阶段构建:**

```dockerfile
# 阶段1:构建
FROM golang:1.26-alpine AS builder
ARG GOPROXY=https://goproxy.cn,direct  # 云环境构建代理可配置
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o shark-socket ./cmd/shark-socket

# 阶段2:运行
FROM alpine:3.22
RUN addgroup -S shark && adduser -S shark -G shark
USER shark
WORKDIR /app
COPY --from=builder /app/shark-socket .
EXPOSE 18000 18080 18081
ENTRYPOINT ["./shark-socket"]
```

**docker-compose 默认暴露端口:**

```
18000 TCP(主协议端口)
18080 Prometheus metrics
18081 Health / Readiness
```

### 16.2 Kubernetes(deploy/k8s/)

**生产默认安全配置:**

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

**Kustomization 结构:**

```
deploy/k8s/                        <- 单层平面目录,由 kustomization.yaml 聚合
  namespace.yaml                # Namespace
  serviceaccount.yaml           # ServiceAccount
  configmap.yaml                # 配置(JSON)
  deployment.yaml               # Deployment(securityContext + probes)
  service.yaml                  # Service(18000 / 18080 / 18081)
  networkpolicy.yaml            # 入方向 NetworkPolicy
  pdb.yaml                      # PodDisruptionBudget
  hpa.yaml                      # HorizontalPodAutoscaler
  kustomization.yaml            # resources: 上述文件清单
```

### 16.3 Helm(deploy/helm/shark-socket/)

可参数化配置:image / replicaCount / service ports / env listener 地址 / resources / securityContext / probes / HPA.

### 16.4 集群部署拓扑

```
        +-----------------+
        |   LB / DNS      |
        +--------+--------+
    +------------+------------+
+---+---+    +--+----+    +--+----+
| Node1 |    | Node2 |    | NodeN |
| shark |    | shark |    | shark |
+---+---+    +---+---+    +---+---+
    +------------+------------+
           +-----+------+
           | Redis/NATS  |
           | PubSub+Cache|
           +-------------+
           +-------------+
           | Prometheus  |
           | + Grafana   |
           +-------------+

集群配置要点:
  ClusterPlugin:nodeID = hostname / Pod name
  Cache:RedisCache adapter(会话路由目录)
  PubSub:RedisPubSub 或 NATSPubSub adapter(节点间消息路由)
  Store:RedisStore 或 SQLStore adapter(会话持久化)

HPA 与会话亲和性:
  建议 LB 配置 sticky session(IP Hash 或 Cookie),
  避免同一客户端路由到不同节点造成跨节点路由压力.
  Pod 扩缩容时,ClusterPlugin 通过 PubSub 通知其他节点会话迁移.
```

### 16.5 外部依赖清单

```
生产必需:
  github.com/gorilla/websocket          v1.5.x
  github.com/prometheus/client_golang   v1.x
  github.com/quic-go/quic-go           v0.x

可选(按功能启用):
  github.com/pion/dtls/v3              CoAP DTLS(P2)
  go.opentelemetry.io/otel             OpenTelemetry Tracer(P1)

Go 版本要求:>= 1.26
```

---

## 17. MQTT 外部集成

MQTT 不在本仓库实现,协议栈由外部项目 [X1aSheng/shark-MQTT](https://github.com/X1aSheng/shark-MQTT) 负责.

### 17.1 职责边界

| 边界 | shark-socket | shark-MQTT |
| --- | --- | --- |
| 网络编排 | 多协议 Gateway,统一启动/停止,共享 SessionManager | MQTT TCP/TLS 接入与 Broker 生命周期 |
| 协议语义 | 不解析 MQTT packet,不维护 QoS 状态机 | MQTT 3.1.1 / 5.0 codec,15 类报文,属性编解码 |
| 会话模型 | core.Session / SessionManager 抽象和跨协议管理 | ClientID 会话,CleanSession,Session Expiry;不与 shark-socket SessionManager 双写 |
| 消息语义 | 统一 []byte 消息通道,插件,指标 | QoS 0/1/2,inflight,retained,will message |
| 数据互通 | Cache / Store / PubSub 抽象层,消费/发布业务事件 | Memory / Redis / BadgerDB 存储后端,按契约投递 MQTT 事件 |
| 安全 | 连接限流,黑名单,全局插件 | MQTT Authenticator / Authorizer,ACL,TLS |

### 17.2 互通模式

```
shark-socket                  shared data plane              shark-MQTT
+------------------+         +------------------+          +------------------+
| Gateway / API    |         | Redis / DB       |          | Broker Factory   |
| TCP/UDP/HTTP/WS  |<------->| PubSub / Outbox  |<-------->| MQTT Codec       |
| CoAP/QUIC/gRPC-W |         | Versioned Schema |          | QoS State Machine|
| Plugin / Metrics |         | Idempotency Keys |          | Session Store    |
+------------------+         +------------------+          +------------------+
```

**数据契约要求:**

- 所有事件消息必须包含:`schema_version / source / event_id / timestamp / idempotency_key`.
- 消费者必须幂等并能容忍重试.
- MQTT topic 通配符,`$SYS` 保护,保留消息等协议细节只在 shark-MQTT 内维护.

**在线状态权威数据源:**

- 设备通过 MQTT 连接时:shark-MQTT 为权威数据源,发布 `mqtt.client.connected` 事件.
- 设备通过 WebSocket/TCP 连接时:shark-socket SessionManager 为权威数据源.
- 跨协议聚合在线状态:由独立业务服务消费两端事件后聚合,不放入任一核心层.

---

## 18. 架构决策记录

### ADR-001:核心 Session 保持原始字节

**决策:** `core.Session.Send` 只接收 `[]byte`.

**原因:**

- Gateway 必须管理混合协议 session,泛型 Session 会把业务类型泄露到运行时层.
- 插件链天然处理原始 payload,无需感知业务消息类型.
- 类型安全通过 `Codec[M]` 和 `AdaptTyped[M]` 在 Handler 层实现,边界清晰.

**转换条件:** 若 `core.Session` 无法承载至少两个真实业务协议的类型安全需求,在 v1.0 API 稳定前评估引入泛型 Session.

### ADR-002:Gateway 拥有共享 Runtime

**决策:** SessionManager,插件链(`PluginChain`,实现 `core.PluginRunner`),Logger,Metrics,Tracer 由 Gateway 创建或注入,协议通过 `UseRuntime` 接收.

**原因:**

- 避免每个协议各自维护插件和 session,保证跨协议统计,广播和关闭一致.
- 显式所有权,便于测试替换 Runtime 组件.
- 协议层不得自行创建全局替代依赖.
- `NewGateway` 内置默认值(单锁 SessionManager、空 PluginChain、三个 Nop 实现),调用方按需覆盖.

### ADR-003:关闭流程分阶段(StagedServer)

**决策:** 协议可实现 `StagedServer`,Gateway 按 StopAccept -> Drain -> CloseSessions 三阶段执行.

**原因:**

- 直接 `Stop` 容易混淆 listener,goroutine,session 的释放顺序.
- 分阶段关闭更容易定位超时和泄漏,每个阶段语义明确.
- 对 TCP / WebSocket / QUIC 等长连接协议尤其重要.
- `Start -> Stop -> Start` 可重入约束通过分阶段关闭更容易保证.

### ADR-004:插件 panic 在插件链内隔离

**决策:** panic recover 放在插件链实现 `PluginChain`(`core.PluginRunner` 的实现),不散落在各协议实现中.

**原因:**

- 插件失败策略一致,协议代码更专注网络生命周期.
- 五个钩子各有独立包装(`safeAccept` / `safeMessage` / `safeClose` / `safeStart` / `safeStop`),panic 转为 `ErrPluginPanic`.
- `OnClose` 必须尽量全部执行:某个插件 panic 后仍继续调用后续插件.
- 链本身不调用 `sess.Close()`:是否关闭会话由传输层决定.

### ADR-005:Codec 适配层作为稳定契约

**决策:** `Codec[M]` 接口定义在 `core` 层,`AdaptTyped[M]` 定义在 `core` 层,对外通过 `api` 导出.

**原因:**

- 类型化消息与传输层解耦,`core.Message` 始终为 `[]byte`.
- 业务代码通过 `AdaptTyped` 装配类型化 Handler,无需修改核心层.
- Codec 接口稳定,业务可自由实现 JSON / Protobuf / MessagePack 等 Codec.

### ADR-006:benchmark 驱动优化

**决策:** 性能优化必须有 benchmark 基线和 pprof 热点证据,禁止凭直觉改结构.

**原因:**

- 过早优化是复杂度来源(BufferPool,时间轮,分片锁均有实现复杂度).
- benchmark 驱动确保优化有实测效果,而非理论收益.
- 当前阶段架构正确性优先,P2 阶段按证据引入复杂优化.

### ADR-007:构建代理可配置

**决策:** Dockerfile 暴露 `GOPROXY` build arg,docker-compose 提供默认代理(`goproxy.cn`).

**原因:**

- 云服务器构建环境可能无法稳定访问 `proxy.golang.org`,构建网络问题不应阻塞镜像生产.
- 默认值可工作,仍允许用户覆盖为企业内部代理.

---

## 19. 实施路线

### P0:架构地基(先让框架可运行,可测试)

1. `internal/core/` 完整类型系统(Protocol / Message / Session / Server / Runtime / Plugin / Codec / 错误体系 / 可观测接口).
2. `internal/runtime/` Gateway(StagedServer 三阶段关闭 + 启动回滚)+ SessionManager(单锁)+ PluginChain(panic 隔离)+ WorkerPool(三策略,位于 transport/shared).
3. `internal/transport/tcp/` Framer 4 种 + TCPSession 6 步关闭 + TCPServer acceptLoop + TCPClient.
4. `internal/infra/observability/` MemoryLogger + MemoryMetrics + PrometheusMetrics + OpenTelemetryTracer,默认空实现在 `internal/core`.
5. `internal/app/` Config(JSON+env+统一验证)+ App 装配.
6. `cmd/shark-socket/main.go` 信号处理 + 优雅关闭.
7. `api/api.go` 类型别名 + 工厂函数.
8. 基础插件:BlacklistPlugin + RateLimitPlugin + HeartbeatPlugin(ticker 扫描).
9. 健康 / 就绪 / Metrics 端点.
10. `tests/defects/` 关键回归测试.

**P0 验收:** `go test ./...` 通过 + `go vet ./...` 通过 + TCP Echo 集成测试通过 + Gateway Start->Stop->Start 回归通过.

### P1:生产协议与配置

**协议增强:**

1. UDP(伪会话 + sweepLoop + ErrPluginBlock 不注册).
2. WebSocket(OnClose sync.Once + AllowedOrigins).
3. HTTP Mode A + Mode B(defer OnClose).
4. CoAP(CON ACK + MessageID 去重 + retransmitLoop 活跃索引).
5. QUIC(强制 TLS + stream 尾块 + Stop 幂等).
6. gRPC-Web(Direct + WebSocket 模式,OnClose 全路径).

**配置与安全:**

1. TLS / mTLS 配置文件化(TCP / QUIC).
2. 证书热加载(SIGHUP).
3. 统一 shutdown stage timeout 配置.
4. 完整 Prometheus 指标静态预注册(锁定标签集合).
5. /healthz 扩展响应体(status / uptime / protocols / sessions).

**插件完善:**

1. AutoBanPlugin.
2. PersistencePlugin(CircuitBreaker 保护).
3. SlowHandlerPlugin.
4. ClusterPlugin(PubSub + Cache + 节点心跳).

**验收:** 所有协议 Echo 集成测试通过 + 异常测试矩阵关键项通过 + race detector 零报告.

### P2:性能与防御(benchmark 驱动)

1. 建立全量 benchmark 基线.
2. 六级 BufferPool(benchmark 证明 alloc 为瓶颈后引入).
3. 分片 SessionManager(32 shard + per-shard LRU).
4. 时间轮心跳(替换 ticker 扫描).
5. OverloadProtector + BackPressure.
6. 高频日志采样器.
7. FD 使用率监控.
8. CoAP Block-wise 传输.
9. LwM2M 正式标准路径 + Observe/Notify.
10. gRPC-Web base64 text 模式 + method 路由.

**验收:** benchmark 目标达成 + pprof 热点消除 + 100K 并发连接压测通过.

### P3:集群与持久化

1. RedisCache / RedisPubSub / RedisStore adapter.
2. 多节点跨节点路由实测验证.
3. 滚动升级无损会话验证.
4. Grafana Dashboard(shark-socket 全量指标可视化).
5. Prometheus / Grafana 云端实机记录.

---

## 20. 验收标准

每次架构级改动必须满足:

1. `go test ./... -count=1` 通过.
2. `go vet ./...` 通过.
3. 若是缺陷修复:相关 focused test 先失败后通过.
4. `go run scripts/run_tests.go -mode all -timeout 5m` 通过.
5. 部署相关改动必须更新 `tests/deploy` 并通过 `scripts/run_tests.go (via -mode deploy)`.
6. 公共行为改变必须同步 README,配置文档,测试策略或审查报告.
7. 云端实测结果写入 `docs/reports/PROJECT-REVIEW-YYMMDD-HHMMSS.md`.
8. 新协议或新功能必须有对应的 example.

---

## 21. 文档维护规则

- 架构文档(本文)描述设计边界,契约和长期方向,不记录测试流水账.
- 具体验证命令和结果写入 `docs/reports/PROJECT-REVIEW-*.md`.
- 配置字段完整参考写入 `docs/guides/CONFIGURATION-*.md`.
- 测试方法写入 `docs/guides/TEST-STRATEGY-*.md` 和 `docs/guides/PROTOCOL-TEST-GUIDE-*.md`.
- 重要架构决策拆分到 `docs/decisions/ADR-NNN-*.md`.
- 各层契约与机制细节写入 `docs/architecture/` 下的对应文档.
- 审查报告按 `PROJECT-REVIEW-YYMMDD-HHMMSS.md` 命名,保留历史.

---

*文档版本:v2.0 综合设计版.融合 `shark-socket` 成熟工程积累与 `shark-socket` 正确架构原则,以显式所有权,分阶段关闭,benchmark 驱动优化为核心,Go >= 1.26.*