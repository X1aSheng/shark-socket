# shark-socket 完整架构分析报告

> 分析日期: 2026-06-26  
> 范围: 全部 Go 源码 (`internal/` + `api/` + `cmd/`)  
> 方法: 3 代理并行深度分析 (core 层 / transport 层 / plugin+infra+runtime 层)  
> 代码规模: ~40 个 Go 源文件, ~3500 行生产代码

---

## 一、架构总览

```
cmd/shark-socket/main.go          ← 入口
api/api.go                         ← 公共 API 封装
internal/
├── app/                           ← 应用组装 (Config → Gateway → Servers)
├── core/                          ← 稳定契约层 (8 文件, 312 行)
│   ├── types.go                   ← Protocol(string) + SessionState(uint8)
│   ├── server.go                  ← Server / StagedServer / Runtime 接口
│   ├── session.go                 ← Session + SessionManager 接口
│   ├── message.go                 ← Message / Handler / Codec[M] / AdaptTyped
│   ├── plugin.go                  ← Plugin 接口 + BasePlugin + 控制错误
│   ├── observability.go           ← Logger / Metrics / Tracer 抽象
│   └── errors.go                  ← 12 个 sentinel 错误
├── runtime/                       ← 依赖注入 + 编排
│   ├── gateway.go                 ← 注册 / 启动 / 分阶段关闭 / 健康检查
│   ├── plugin_chain.go            ← 优先级排序 + panic 隔离
│   ├── runtime.go                 ← Runtime 容器 (Sessions/Plugins/Logger/Metrics/Tracer)
│   ├── session_manager.go         ← ID 分配 / 注册 / 广播 / CloseAll
│   └── options.go                 ← GatewayOption 函数式选项
├── transport/                     ← 7 种协议实现
│   ├── tcp/                       ← TCP (Framer 接口, WorkerPool, Client)
│   ├── udp/                       ← UDP + DTLS (pion/dtls v3)
│   ├── http/                      ← HTTP (CORS, 会话模式)
│   ├── websocket/                 ← WebSocket (gorilla, WSS, ping/pong)
│   ├── coap/                      ← CoAP (RFC 7252, Observer RFC 7641, DTLS)
│   ├── quic/                      ← QUIC (quic-go, 每消息一个流)
│   ├── grpcweb/                   ← gRPC-Web (自定义帧, WebSocket 隧道)
│   └── shared/                    ← Acceptor (令牌桶 + 最大连接数)
├── plugin/                        ← 7 个内置插件
│   ├── blacklist.go               ← IP/CIDR 黑名单 (Priority 0)
│   ├── autoban.go                 ← 自动封禁 (Priority 5)
│   ├── ratelimit.go               ← 固定窗口限流 (Priority 10)
│   ├── heartbeat.go               ← 会话超时清理 (Priority 50)
│   ├── persistence.go             ← 持久化 (Priority 90)
│   ├── cluster.go                 ← 跨节点消息分发 (Priority 95)
│   └── slow_handler.go            ← 慢处理日志
├── infra/                         ← 基础设施
│   ├── store/                     ← Store 接口 + Memory/BoltDB/MessageLog/SessionStore
│   ├── cache/                     ← TTL 缓存
│   ├── circuitbreaker/            ← 熔断器 (Closed→Open→HalfOpen)
│   ├── mqtt/                      ← MQTT 适配器 (Eclipse Paho)
│   ├── observability/             ← Prometheus / OpenTelemetry
│   ├── pubsub/                    ← 发布-订阅 (⚠ 有并发 bug)
│   └── tlsutil/                   ← 证书缓存 + 热重载
└── protocol/
    └── lwm2m/                     ← LwM2M TLV 编解码
```

---

## 二、核心设计决策

### 2.1 Protocol 类型: `string` alias 而非 `uint8`

```go
type Protocol string  // "tcp", "websocket", etc.
```

**选择的理由:**
- **可扩展性**: 无需修改 core 包即可添加新协议
- **可读性**: 日志和指标中直接显示协议名，无需映射表
- **配置友好**: 配置文件中的 `"tcp"` 可直接比较

**SessionState 为何不同?** SessionState 使用 `uint8` 枚举，因为它是封闭集合 (Connecting→Active→Draining→Closed)，不可由用户扩展。两者分离体现了 "开闭原则"：标识符开放，状态机封闭。

### 2.2 分阶段关闭 — 项目最核心的架构决策

```
StopAccept → Drain → CloseSessions
   ↓            ↓          ↓
关闭监听器   等待进行中   强制关闭
不接受新连接  任务完成    剩余会话
```

每个阶段有独立超时 (`StageTimeouts`)，通过 `StagedServer` 可选接口实现。Gateway 的 `Stop()` 遍历所有 server 依次执行三个阶段。这模仿了 Kubernetes 的优雅终止模式 (preStop → SIGTERM → SIGKILL)。

### 2.3 依赖注入 (无框架)

```
Gateway 创建 Runtime → 注入 Server (UseRuntime)
Runtime 包含: Sessions + Plugins + Logger + Metrics + Tracer
```

无第三方 DI 框架。`RuntimeConfigurable` 接口 + `UseRuntime` 方法实现手动注入。`PluginRunner` 从 `Plugin` 分离，使得 transport 调用 `runner.OnMessage()` 时无需感知具体插件。

### 2.4 代码生成器适配路径

```go
type Codec[M any] interface { Encode(M) ([]byte, error); Decode([]byte) (M, error) }
type TypedHandler[M any] func(Session, M) error
func AdaptTyped[M any](codec Codec[M], h TypedHandler[M]) Handler
```

`AdaptTyped` 是类型化业务逻辑与无类型传输层之间的泛型桥梁。`Codec[M]` 是 core 层唯一的泛型接口。

---

## 三、深度分析: 各层质量评估

### 3.1 Core 层 (A+)

| 维度 | 评价 |
|------|------|
| 接口最小化 | ✅ 每个接口只包含必要方法 |
| 依赖方向 | ✅ 无循环依赖 (types → session → server, 单向) |
| 错误语义 | ✅ 12 个 sentinel 错误覆盖完整生命周期 |
| SessionManager | ✅ 快照模式 (Snapshot/Range) 避免持锁发送 |
| PluginRunner 分离 | ✅ Runner 管理排序，Plugin 定义钩子 |
| Nop 实现 | ✅ Logger/Metrics/Tracer 全有 nop |

**无缺陷。**

### 3.2 Transport 层 (A-)

**一致的实现模式:**
- 所有 8 个 transport 实现 `core.Server` + `core.StagedServer`
- 统一使用 `started atomic.Bool` (CAS) 防双重启动
- 统一使用 `closed atomic.Bool` 信号关闭
- 统一 Session 实现 (`atomic.Uint32 state`, `sync.Map meta`, `sync.Once close`)

**发现的不一致性:**

| 问题 | 严重度 | 位置 |
|------|--------|------|
| TCP/QUIC Stop 顺序与其余 5 个 transport 不同 | 低 | tcp/server.go, quic/server.go |
| WebSocket/gRPC-Web `Start()` 中重复 `s.closed.Store(false)` | 已修复 | V4 commit |

**对比矩阵:**

| Transport | 接受器 | 写队列 | 工作池 | TTL | TLS | Client | 去重 | 帧类型 |
|-----------|--------|--------|--------|-----|-----|--------|------|--------|
| TCP | ✅ | chan | ✅ | - | TLS | ✅ | - | Framer 接口 |
| UDP | - | sync | - | 2min | DTLS | - | - | 数据报 |
| HTTP | - | resp.Write | - | - | - | - | - | HTTP body |
| WebSocket | ✅ | WriteMessage | - | - | WSS | - | - | WS frames |
| CoAP | - | sync | - | 2min | DTLS | - | ✅ | CoAP msg |
| QUIC | ✅ | chan | - | - | 必需 | - | - | QUIC stream |
| gRPC-Web | ✅ | resp.Write | - | - | HTTPS | - | - | gRPC-Web |

**测试覆盖最佳**: CoAP (消息 fuzz, 观察者测试, 选项边缘用例)

### 3.3 Plugin 层 (B+)

**优先级链设计合理:**
```
Blacklist(0) → AutoBan(5) → RateLimit(10) → Heartbeat(50) → Persistence(90) → Cluster(95)
  安全检查       自动封禁       流量控制        会话管理       持久化          跨节点
```

**发现的问题:**
- **RateLimit 非真正滑动窗口**: 固定窗口计数器在边界处存在 2x 突发漏洞
- **AutoBan 全局清除**: 每 30 分钟清除所有封禁，无逐 IP 过期
- **PubSub 有并发 panic 风险** (见 infra 层)

### 3.4 Infra 层 (B)

**发现的关键缺陷:**

| # | 严重度 | 位置 | 描述 |
|---|--------|------|------|
| 1 | **Critical** | `infra/pubsub/pubsub.go:46` | **Publish 向已关闭 channel 发送** — 订阅者取消订阅时关闭 channel，但 Publish 的快照可能包含已关闭的 channel，导致 panic。需用 select 或 ref-count 防护。 |
| 2 | Medium | `infra/observability/prometheus.go` | **标签切片复用数据竞争** — `IncCounter/ObserveHistogram` 复用标签切片 (`[:0]`)，若 `ExportText` 并发读取会导致 race。 |
| 3 | Low | `infra/mqtt/adapter.go` | **CleanSession=true 时重连丢失订阅** — 自动重连后主题订阅不恢复。 |

---

## 四、Goroutine 追踪完整性

共 **35+ goroutine**，全部追踪。完整矩阵如下：

| 组件 | Goroutine | 追踪方式 |
|------|-----------|---------|
| tcp | acceptLoop, handleConn×N, writeLoop×N, workerPool×N | acceptWG, connWG, wg |
| udp | readLoop, sweepLoop, dtlsAcceptLoop, handleDTLSConn×N | s.wg |
| http | Serve, Drain helper | s.wg, 本地 |
| websocket | Serve, readLoop×N, pingLoop×N, CloseSessions helper | s.wg |
| coap | readLoop, sweepLoop, seenCleanupLoop, dtlsAcceptLoop, handleDTLSConn×N | s.wg |
| quic | acceptLoop, handleConn×N, writeLoop×N, handleStream×N, Drain helper | s.wg |
| grpcweb | Serve, readWebSocketLoop×N, Drain helper | s.wg |
| plugin | rateLimit, autoBan, heartbeat, cluster consume | p.wg |
| tlsutil | file watcher | 可选 wg (WatchFilesWithWG) |
| app | health serveHTTP, metrics serveHTTP | http.Server.Shutdown 隐式 |

---

## 五、安全性评估

| 检查项 | 状态 |
|--------|------|
| WebSocket CheckOrigin 默认拒绝 | ✅ 返回 `false` |
| gRPC-Web CheckOrigin 默认拒绝 | ✅ 返回 `false` |
| HTTP CORS 默认不启用 | ✅ 无 CORS 头 |
| TLS MinVersion 强制 ≥1.2 | ✅ parseTLSMinVersion 拒绝 1.0/1.1 |
| DTLS MinVersion 映射 | ⚠️ pion/dtls v3 无此字段 (已文档化) |
| 所有 http.Server 有超时 | ✅ 含 Health/Metrics (V4 修复) |
| 无硬编码凭据 | ✅ |
| 消息大小限制 | ✅ 所有 transport 有上限 |
| QUIC 强制 TLS | ✅ 无 TLS 则 Start 失败 |

---

## 六、测试覆盖评估

| 包 | 覆盖率 | 质量 | 缺失 |
|----|--------|------|------|
| core | 100% | 优秀 | 无 |
| pubsub | 100% | 不足 | 无并发测试 |
| cache | 97.2% | 良好 | 并发 Get/Set race |
| shared | 95.7% | 良好 | - |
| tlsutil | 91.1% | 良好 | - |
| circuitbreaker | 90.2% | 良好 | 并发状态转换 |
| runtime | 88.1% | 良好 | 分阶段关闭超时 |
| app | 85.8% | 良好 | parseTLSMinVersion 新函数 |
| mqtt | 85.1% | 良好 | 重连 + 恢复订阅 |
| store | 83.6% | 良好 | 并发 BoltDB 操作 |
| quic | 80.5% | 良好 | writeLoop 错误路径 |
| http | 78.4% | 良好 | TLS 测试 |
| websocket | 78.9% | 良好 | Ping/Pong, PongTimeout |
| lwm2m | 77.2% | 良好 | 完整注册流程 |
| scripts | 76.1% | 良好 | - |
| grpcweb | 75.4% | 良好 | WS 隧道 TLS |
| coap | 74.5% | 优秀 | NON 消息, E2E 观察者 |
| tcp | 75.7% | 良好 | CloseSessions 超时 |
| plugin | 72.2% | 良好 | Cluster with nil bus |
| udp | 68.9% | 最低 | 多并发, 高吞吐 |

---

## 七、缺陷优先级

### Critical (1)

| # | 文件 | 描述 | 修复 |
|---|------|------|------|
| 1 | `infra/pubsub/pubsub.go:46` | Publish 向已关闭 channel 发送导致 panic | 使用 `select + default` 或在持锁期间发送 |

### Medium (3)

| # | 文件 | 描述 |
|---|------|------|
| 2 | `infra/observability/prometheus.go` | 标签切片复用数据竞争 |
| 3 | `infra/mqtt/adapter.go` | CleanSession=true 时自动重连丢失订阅 |
| 4 | `internal/plugin/ratelimit.go` | 固定窗口 → 建议换为滑动窗口 |

### Low (4)

| # | 文件 | 描述 |
|---|------|------|
| 5 | tcp/quic server.go | Stop 顺序不一致 (先 CloseSessions 后 Drain) |
| 6 | `infra/cache/cache.go` | Get 过期删除有 TOCTOU race |
| 7 | `infra/store/message_log.go` | Replay/Prune 与 Append 并发不安全 |
| 8 | `internal/plugin/cluster.go` | 跨节点可能形成广播放大环 |

---

## 八、架构评价

| 维度 | 评分 | 备注 |
|------|------|------|
| 接口设计 | A+ | 最小化、正交、无循环依赖 |
| 分阶段关闭 | A | 唯一三阶段设计，工业级优雅终止 |
| 插件链 | A- | panic 隔离良好，排序合理；窗口算法可改进 |
| 依赖注入 | A | 无框架手工 DI，清晰可测 |
| goroutine 管理 | A | 100% 追踪，channel 零 double-close 风险 |
| 错误处理 | A- | 核心 sentinel 完整；Persistence 吞错有据 |
| 安全性 | A | 默认拒绝、TLS≥1.2、所有超时已设置 |
| 测试覆盖 | B+ | 75.0% 覆盖率；并发/压力/集成深度不足 |
| 文档同步 | B+ | README/CONTRACTS 准确；ARCHITECTURE 仍有差异 |

**总评: A-** — 架构清晰、接口稳定、安全性良好。1 个 Critical bug (PubSub) 需立即修复，3 个 Medium 建议近期处理。
