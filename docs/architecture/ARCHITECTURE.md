# ARCHITECTURE.md

> Shark-Socket 架构总览文档  
> 版本:v0.2.x-alpha  
> 最后更新:2026-06-01

---

## 目录

1. [项目定位](#1-项目定位)
2. [设计哲学](#2-设计哲学)
3. [分层架构](#3-分层架构)
4. [依赖矩阵](#4-依赖矩阵)
5. [目录结构](#5-目录结构)
6. [非目标与转换条件](#6-非目标与转换条件)
7. [测试与覆盖率](#7-测试与覆盖率)
8. [文档导航](#8-文档导航)

---

## 1. 项目定位

Shark-Socket 是**高性能,可扩展的多协议服务端网络框架**,采用 Go 1.26+ 开发,提供以下核心价值:

| 能力 | 说明 |
|------|------|
| 统一 Gateway 运行时 | TCP,UDP,HTTP,WebSocket,CoAP,QUIC,gRPC-Web 共享 SessionManager,插件链,Metrics,Logger |
| 跨协议会话管理 | 统一 Session 抽象,支持跨协议查询,广播,路由 |
| 可扩展插件系统 | 黑名单,限流,心跳,持久化,集群,自动封禁等内置插件,panic 隔离 |
| 分阶段优雅关闭 | StopAccept -> Drain -> CloseSessions 三阶段,连接无丢失 |
| IoT 场景优化 | CoAP/LwM2M 原生支持,设备管理语义完整 |

**与成熟软件的边界:**

- **不与 Nginx/Envoy 竞争 HTTP 反代**:HTTP 仅作轻量支持(Mode A:纯 router;Mode B:可选 Session+Plugin)
- **不实现完整 MQTT Broker**:MQTT 3.1.1/5.0 由外部项目 [shark-MQTT](https://github.com/X1aSheng/shark-MQTT) 提供,通过数据契约互通(详见 `MQTT-INTEGRATION.md`)
- **不实现完整 gRPC 服务端**:gRPC-Web 仅支持 Unary 和 Server Streaming,不替代 `google.golang.org/grpc`

**目标场景:**

- IoT 平台(设备使用 CoAP/LwM2M,Web 端使用 WebSocket,管理 API 使用 HTTP)
- 实时通信网关(WebSocket 长连接 + TCP 自定义协议 + UDP 广播)
- 边缘计算节点(资源受限设备通过 CoAP 接入,统一 SessionManager 跨协议路由)

---

## 2. 设计哲学

### 2.1 第一原则

**先正确,再完整,再高效.**

| 优先级 | 含义 | 实践 |
|--------|------|------|
| 正确 | 运行时所有权,协议生命周期,插件执行,关闭流程可解释,可测试,可恢复 | Gateway 显式拥有 Runtime,协议通过 `UseRuntime` 接收;StagedServer 分阶段关闭 |
| 完整 | 覆盖核心协议(TCP/UDP/CoAP/LwM2M/WebSocket)功能完整可用 | P0 只实现基础功能,P1 补齐生产特性 |
| 高效 | 性能优化有 benchmark 证据支撑 | P2 阶段引入 BufferPool,分片锁,时间轮,必须先有基准测试 |

### 2.2 核心原则

| 原则 | 实践 |
|------|------|
| 接口契约优先 | 所有模块通过 `interface` 交互,依赖倒置;`core/` 层只定义接口,不依赖具体实现 |
| 显式所有权 | Gateway 创建并注入 Runtime(SessionManager/PluginRunner/Logger/Metrics/Tracer),协议不自行创建全局资源 |
| 分阶段关闭 | StagedServer 接口定义 StopAccept / Drain / CloseSessions 三阶段,每阶段语义清晰 |
| 零值可用 | Functional Options 模式,所有配置项有合理默认值 |
| 失败隔离 | 单连接/单协程 panic 不影响整体;插件 panic 在插件链 PluginChain 内隔离,不传播到协议层 |
| 可观测优先 | 所有关键路径内置 metrics/trace/log,采集不阻塞业务;指标按业务点需要命名,标签维度保持收敛 |
| benchmark 驱动 | 性能优化必须有基准测试和 pprof 热点证据,禁止凭直觉改结构 |
| 编译期验证 | `var _ Interface = (*Impl)(nil)` 接口满足检查;关键约束用类型系统表达 |

### 2.3 当前阶段的克制

以下**不是当前目标**,避免过度设计:

- 泛型 `Session[M]` 作为核心接口(业务类型污染运行时层,当前用 `Codec[M]` 适配)
- 六级 BufferPool,时间轮,分片 SessionManager(P2 阶段,benchmark 证明后引入)
- 完整防御体系(OverloadProtector,BackPressure 在 P2)
- Gateway 直接耦合数据库,Redis,消息队列(通过 `Cache`/`Store`/`PubSub` 接口抽象,具体 adapter 在 P2/P3)
- 每个协议一次性实现完整标准(按 P0->P1->P2 分阶段推进)

---

## 3. 分层架构

### 3.1 层次划分

```
+-----------------------------------------------------------------+
| Layer 0: api/                                                    |
|   对外门面层:类型别名,工厂函数,Codec 接口定义                 |
+-----------------------------------------------------------------+
| Layer 1: cmd/ + internal/app/                            |
|   可运行入口 + 应用装配层                                        |
|   配置加载(JSON + env)-> Gateway 装配 -> 健康/指标服务 -> 信号处理|
|   配置验证在 app 层统一执行,不在 Start() 中执行                 |
+-----------------------------------------------------------------+
| Layer 2: internal/runtime/                                       |
|   运行时层:Gateway,SessionManager,PluginChain,Runtime 容器    |
+-----------------------------------------------------------------+
| Layer 3: internal/transport/ + internal/plugin/ + internal/protocol/|
|   传输协议层:TCP,UDP,HTTP,WebSocket,CoAP,QUIC,gRPC-Web       |
|   应用协议层:LwM2M 生命周期模型(基于 CoAP transport)          |
|   插件层:黑名单,限流,心跳,持久化,自动封禁,慢处理,集群广播 |
+-----------------------------------------------------------------+
| Layer 4: internal/core/                                          |
|   稳定契约层:Protocol,Session,Server,Runtime,Plugin,Codec   |
|   可观测接口:Logger,Metrics,Tracer                            |
|   错误体系:失败信号哨兵(13 个)+ 插件控制错误(2 个)          |
+-----------------------------------------------------------------+
| Layer 5: internal/infra/                                |
|   基础设施层:cache,store,pubsub,circuitbreaker,mqtt,tlsutil |
|   observability:logger,metrics,prometheus,otel              |
+-----------------------------------------------------------------+
```

### 3.2 层间依赖规则

**依赖只能从上层指向下层,同层内部保持局部依赖.**

```
api / cmd / app
        v
runtime + transport + plugin + protocol
        v
core + infra
```

**严格禁止事项:**

- `core/` 不依赖任何具体协议实现
- `runtime/` 不依赖 `cmd/` 或 `app/`
- `transport/` 不直接创建全局 Gateway
- `plugin/` 不直接控制协议 listener 生命周期
- `infra/` 不反向调用业务层
- `protocol/` 不依赖 `transport/` 具体实现(只能依赖 `core/` 接口)

---

## 4. 依赖矩阵

| | api | cmd | app | runtime | transport | plugin | protocol | core | infra |
|---|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|
| **api** | - | [X] | [X] | [OK] | [OK] | [OK] | [OK] | [OK] | [OK] |
| **cmd** | [OK] | - | [OK] | [X] | [X] | [X] | [X] | [OK] | [X] |
| **app** | [OK] | [X] | - | [OK] | [OK] | [OK] | [OK] | [OK] | [OK] |
| **runtime** | [X] | [X] | [X] | - | [X] | [OK] | [X] | [OK] | [OK] |
| **transport** | [X] | [X] | [X] | [X] | - | [X] | [X] | [OK] | [OK] |
| **plugin** | [X] | [X] | [X] | [X] | [X] | - | [X] | [OK] | [OK] |
| **protocol** | [X] | [X] | [X] | [X] | [X] | [X] | - | [OK] | [OK] |
| **core** | [X] | [X] | [X] | [X] | [X] | [X] | [X] | - | [X] |
| **infra** | [X] | [X] | [X] | [X] | [X] | [X] | [X] | [X] | - |

**说明:**

- [OK] = 允许依赖
- [X] = 禁止依赖
- `core/` 和 `infra/` 是最底层,任何包不得反向依赖
- `runtime/` 只依赖 `core/`(插件由 `PluginChain` 承载),不依赖 `transport/`(协议由 app 层组装)

---

## 5. 目录结构

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
|   +-- transport/
|   |   +-- tcp/                        # server / session / client / framer / policy / options
|   |   +-- udp/                        # server / session / options
|   |   +-- http/                       # server / session / options
|   |   +-- websocket/                  # server / session / options
|   |   +-- coap/                       # server / session / message / observe / options
|   |   +-- quic/                       # server / session / options
|   |   +-- grpcweb/                    # server / session / websocket_session / framing / options
|   |   +-- shared/                     # 传输层共享组件
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
|       +-- pubsub/                     # pubsub.go:PubSub 结构 + ChannelPubSub
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
|   +-- k8s/                            # namespace / serviceaccount / configmap / deployment
|   |                                   # service / networkpolicy / pdb / hpa / kustomization
|   +-- helm/shark-socket/              # Chart.yaml / values.yaml / templates/
|
+-- docs/
|   +-- architecture/                   # 本目录:总览 / 契约 / 生命周期 / 各层设计文档
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

## 6. 非目标与转换条件

以下**当前不是目标**,避免过度设计,但定义了明确的转换条件:

| 非目标 | 转换为目标的阶段 | 转换条件 |
|--------|-----------------|---------|
| 泛型 `Session[M]` 作为核心接口 | v0.1.0 API 稳定前评估 | 现有 `core.Session` 无法承载至少两个真实业务协议的类型安全需求 |
| 六级 BufferPool,时间轮,分片 LRU | P2 性能与防御 | benchmark 证明分配,超时扫描或 Session 锁竞争成为瓶颈 |
| 完整防御体系(OverloadProtector,BackPressure) | P2 性能与防御 | 基准压测建立,热路径分配点已定位 |
| Gateway 直接耦合数据库,Redis,消息队列 | P2/P3 集群与持久化 | 外部 adapter 接口稳定,完成至少一种真实后端集成测试 |
| 内建完整 MQTT Broker | P1/P2 MQTT 专项 | 明确选择内建 Broker 而不是外部 Broker 适配 |
| 每个协议一次性实现完整标准 | 按协议专项推进 | 当前 smoke / 边界测试稳定,且有明确互操作目标 |

---

## 7. 测试与覆盖率

| 包 | 覆盖率 | 测试文件 | 说明 |
|------|--------|---------|------|
| Core | 100% | `core_test.go` | 接口,错误,类型 |
| Cache | 97.2% | `cache_test.go` | TTL 缓存 |
| PubSub | 100% | `pubsub_test.go` | 发布订阅 |
| Shared | 95.7% | `acceptor_test.go` | 连接限流 |
| TLS Util | 94.1% | `cert_cache_test.go` | 证书管理 |
| Runtime | 88.2% | `runtime_test.go` | 网关编排 |
| API | 77.4% | `api_test.go` | 公共接口 |
| Plugin | 79.3% | 4 测试文件 | 插件系统 |
| Store | 77.3% | 3 测试文件 | 存储后端 |
| UDP | 71.5% | 3 测试文件 | DTLS+session |
| CoAP | 76.1% | 4 测试文件 | 协议编解码 |
| TCP | 70.6% | 3 测试文件 | TLS+mTLS+pool |
| QUIC | 76.8% | 1 测试文件 | QUIC 流传输 |
| gRPC-Web | 73.1% | 2 测试文件 | TLS+WS 模式 |
| MQTT | 59.1% | 2 测试文件 | 适配器单元测试 |

总体:覆盖率为实测快照,随代码变化;CI 的 coverage job 通过
`go run scripts/run_tests.go -mode cover` 校验,下限由脚本常量 `minCoverage` 控制
(当前 70.0%).

---

## 8. 文档导航

### 8.1 读者路径建议

| 目标 | 阅读顺序 |
|------|---------|
| 理解整体架构 | ARCHITECTURE(本文)-> CONTRACTS -> LIFECYCLE |
| 实现新协议 | CONTRACTS -> LIFECYCLE -> TRANSPORT(参考 TCP/UDP/CoAP) |
| 实现插件 | CONTRACTS -> PLUGIN -> ERRORS |
| 配置部署 | `docs/guides/CONFIGURATION-*.md` -> DEPLOYMENT |
| 性能优化 | `docs/reports/BENCHMARK-*.md`(需先有 benchmark 基线) |
| 安全加固 | SECURITY -> `docs/guides/CONFIGURATION-*.md`(TLS 字段) |

### 8.2 文档职责边界

| 文档 | 写什么 | 不写什么 |
|------|--------|---------|
| ARCHITECTURE | 设计哲学,分层图,依赖矩阵,目录结构 | 各层具体实现细节 |
| CONTRACTS | `core/` 所有接口定义,约束,状态机 | 具体实现代码 |
| LIFECYCLE | 连接生命周期,Gateway 启停,状态机汇总 | 协议网络细节 |
| ERRORS | 失败信号哨兵列表,判定方式,使用规范 | 错误处理的业务逻辑 |
| GATEWAY | Gateway 启动/停止流程,SessionManager,PluginChain,WorkerPool | 协议实现细节 |
| TRANSPORT | TCP/UDP/CoAP/WebSocket 实现细节 | LwM2M 语义,插件实现 |
| PROTOCOL | LwM2M 注册生命周期,Resource 模型 | CoAP 帧解析细节 |
| PLUGIN | 内置插件详细设计,Priority,执行规则 | 运行时 Gateway 细节 |
| OBSERVABILITY | Logger/Metrics/Tracer 接口,Prometheus 导出,健康端点 | 业务日志内容 |
| SECURITY | 六层防御,TLS/mTLS 配置,攻击面缓解矩阵 | 插件具体实现 |
| DEPLOYMENT | Docker/K8s/Helm,环境变量,CI/CD,端口规划 | 应用业务配置 |
| TROUBLESHOOTING | 诊断工具,常见故障排查,错误码速查 | 错误处理业务逻辑 |

### 8.3 文档间交叉引用示例

```
GATEWAY.md 中描述 SessionManager 接口时:
  "SessionManager 接口定义详见 CONTRACTS.md sec.Session 与 SessionManager."

TRANSPORT.md 中描述 CoAP 时:
  "CoAP 作为传输层实现,帧解析在本文档;LwM2M 应用层语义详见 PROTOCOL.md."

PLUGIN.md 中描述插件执行规则时:
  "插件 panic 隔离机制详见 GATEWAY.md sec.PluginChain 实现."
```

---

**版权声明:** 本文档属于 Shark-Socket 项目,遵循项目许可证.
