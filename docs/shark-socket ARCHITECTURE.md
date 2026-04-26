# Shark-Socket 架构设计文档

> 高性能、可扩展的多协议网络框架，采用 Go 语言开发（Go >= 1.24），支持 TCP、TLS、UDP、HTTP、WebSocket 和 CoAP 协议的统一抽象与网关集成。

---

## 1. 设计原则与性能目标

### 1.1 设计原则

| 原则 | 实践 |
|------|------|
| 接口契约优先 | 所有模块通过 interface 交互，依赖倒置 |
| 零拷贝 / 池化 | 六级 BufferPool 复用 []byte，关键路径 0 alloc |
| 无锁优先 | atomic 计数器、分片锁、单 goroutine 写 |
| 协程可控 | Worker Pool 限制并发 + 弹性扩容，防 goroutine 泄漏 |
| 单向依赖 | 严格层间依赖矩阵，禁止反向和循环 |
| 零值可用 | Functional Options 模式，默认值合理 |
| 失败隔离 | 单连接/单协程 panic 不影响整体，插件链失败可配置中断 |
| 可观测优先 | 所有关键路径内置 metrics / trace / log，采集不阻塞业务 |
| 编译期验证 | `var _ Interface = (*Impl)(nil)` + MessageConstraint 泛型约束 |

### 1.2 性能目标

| 指标 | 目标值 | 实现手段 |
|------|--------|----------|
| TCP 吞吐 | >= 100K msg/s（单核） | Worker Pool + 写队列 + Buffer 复用 |
| 连接延迟 | P99 <= 1ms | 无锁热路径 + atomic + 单 goroutine 写 |
| 内存分配 | 关键路径 0 alloc | sync.Pool + 预分配 + 零拷贝引用 |
| 连接规模 | >= 100K 并发 | Goroutine 复用 + LRU/TTL + 分片锁 |
| 插件开销 | <= 200ns/hop | 静态排序列表 + 接口内联 |

### 1.3 源文件融合决策

| 维度 | 最终决策 | 说明 |
|------|----------|------|
| Session | **泛型 Session[M]，但核心接口统一 Send([]byte)** | Session[M] 保留泛型，便于类型安全；底层统一 Send([]byte) 易于互操作 |
| Handler | 函数类型为主，Handler[M] 接口保留高级场景 | 轻量，一个函数即可作为处理器 |
| Plugin OnMessage | 增强版：([]byte, error) + 特殊错误语义 | 支持消息内容变换（解密、压缩解压） |
| SessionManager | 分片锁 32 shard | 降低全局锁竞争，线性扩展 |
| BufferPool | **6 级**（Micro/Tiny/Small/Medium/Large/Huge） | 更细粒度覆盖 IoT/控制帧到大文件场景 |
| HTTP Server | 可选 Session + Plugin | 双模式：轻量模式 A + 完整模式 B |

### 1.4 Go 1.24 特性适配

- **iter 包**：Range / 广播遍历使用 `iter.Seq` 迭代器替代回调函数
- **sync.Map 增强**：Go 1.24 `sync.Map` 支持 `Swap`、`CompareAndDelete`，替换部分手写 CAS
- **crypto/tls**：Go 1.24 默认 TLS 1.3，弃用旧密码套件，证书热加载基于新 `GetConfigForClient`
- **unique 包**：协议标签字符串池化，减少字符串分配
- **slices / maps 包**：Plugin 排序、分片遍历使用标准泛型工具函数
- **泛型增强**：Go 1.24 类型推断改进，`Session[M]` 泛型约束更简洁

### 1.5 Session 泛型设计说明

```
Session[M MessageConstraint] 设计哲学：

┌─────────────────────────────────────────────────────┐
│  泛型层（编译期类型安全）                             │
│  Session[[]byte]  ←── 最常用，等价 RawSession        │
│  Session[MyMsg]   ←── 业务自定义消息类型              │
│                                                     │
│  统一底层接口（运行时多态，混合协议管理）              │
│  Send(data []byte) error  ←── 始终可用              │
│                                                     │
│  泛型方法（可选增强）                                 │
│  SendTyped(msg M) error   ←── 编译期类型检查         │
└─────────────────────────────────────────────────────┘

转换路径：
  Session[M].SendTyped(msg M)
    → encode(msg) → []byte
    → Session[M].Send([]byte)   ← 统一写队列路径
```

---

## 2. 分层架构与依赖矩阵

```
╔══════════════════════════════════════════════════════════════╗
║ Layer 0: api/            统一入口，类型别名，工厂函数         ║
║ 依赖 → 所有下层                                              ║
╠══════════════════════════════════════════════════════════════╣
║ Layer 1: gateway/        多协议编排，全局插件，共享 SM        ║
║ 依赖 → protocol, session, plugin, types, infra              ║
╠══════════════════════════════════════════════════════════════╣
║ Layer 2: protocol/       各协议 Server / Client / Session    ║
║ 依赖 → session, plugin, types, infra                        ║
╠══════════════════════════════════════════════════════════════╣
║ Layer 3: session/ + plugin/   会话管理 + 插件责任链          ║
║ 依赖 → types, infra                                         ║
╠══════════════════════════════════════════════════════════════╣
║ Layer 4: infra/ + types/ + errs/   基础设施、类型、错误码    ║
║ 仅依赖 Go 标准库 + gorilla/websocket + prometheus           ║
╚══════════════════════════════════════════════════════════════╝
```

**严格依赖矩阵（✓=允许 ✗=禁止）**

|  | API | GW | Proto | Sess | Plugin | Types | Infra | Errs |
|--|-----|----|----|----|----|----|----|---|
| API | — | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| Gateway | ✗ | — | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| Protocol | ✗ | ✗ | — | ✓ | ✓ | ✓ | ✓ | ✓ |
| Session | ✗ | ✗ | ✗ | — | ✗ | ✓ | ✓ | ✓ |
| Plugin | ✗ | ✗ | ✗ | ✓ | — | ✓ | ✓ | ✓ |
| Types | ✗ | ✗ | ✗ | ✗ | ✗ | — | ✗ | ✓ |
| Infra | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | — | ✓ |
| Errs | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | — |

---

## 3. 目录结构

```
shark-socket/
├── api/
│   └── api.go                    # 统一导出 + 工厂函数
├── internal/
│   ├── types/
│   │   ├── enums.go              # Protocol / MessageType / SessionState 枚举
│   │   ├── message.go            # Message[T] + RawMessage + MessageConstraint
│   │   ├── session.go            # Session[M] 泛型接口 + RawSession + SessionManager
│   │   ├── handler.go            # MessageHandler[M] 泛型函数类型 + RawHandler + Server 接口
│   │   └── plugin.go             # Plugin + BasePlugin + Priority + 特殊错误语义
│   ├── errs/
│   │   └── errors.go             # 完整错误体系 + 分类判断函数
│   ├── infra/
│   │   ├── bufferpool/
│   │   │   └── pool.go           # 六级 BufferPool（Micro/Tiny/Small/Medium/Large/Huge）
│   │   ├── logger/
│   │   │   └── logger.go         # Logger 接口 + slogLogger（Go 1.21+ JSON）+ Nop
│   │   ├── metrics/
│   │   │   └── metrics.go        # Metrics 接口 + Prometheus 实现 + Timer
│   │   ├── cache/
│   │   │   └── cache.go          # Cache 接口（Get/Set/Del/Exists/TTL/MGet）+ MemoryCache
│   │   ├── store/
│   │   │   └── store.go          # Store 接口（Save/Load/Delete/Query）+ MemoryStore
│   │   ├── pubsub/
│   │   │   └── pubsub.go         # PubSub 接口 + Subscription + ChannelPubSub
│   │   └── circuitbreaker/
│   │       └── circuitbreaker.go # 熔断器（Closed / Open / HalfOpen）
│   ├── session/
│   │   ├── session.go            # BaseSession（状态机 + writeQueue drain）
│   │   ├── manager.go            # 分片锁 SessionManager（32 shard）
│   │   └── lru.go                # LRUList 双向链表（per-shard，sync.Pool 节点复用）
│   ├── plugin/
│   │   ├── chain.go              # PluginChain（ErrSkip / ErrDrop / ErrBlock 语义）
│   │   ├── blacklist.go          # IP/CIDR 黑名单（精确 O(1) + CIDR 遍历 + Cache 双查）
│   │   ├── ratelimit.go          # 令牌桶（per-IP + 全局双层）
│   │   ├── heartbeat.go          # 时间轮心跳（单 goroutine 管理全部会话）
│   │   ├── persistence.go        # 异步持久化（Channel 缓冲 + 批量写入）
│   │   ├── cluster.go            # 跨节点路由 + 会话目录 + 节点心跳
│   │   ├── autoban.go            # 自动封禁（触发限流/协议错误 → 加入黑名单）
│   │   └── options.go            # 插件通用选项
│   ├── protocol/
│   │   ├── tcp/
│   │   │   ├── framer.go         # Framer 接口 + 4 种内置实现
│   │   │   ├── session.go        # TCPSession（writeQueue + drain）
│   │   │   ├── server.go         # TCP Server（accept + Framer + WorkerPool）
│   │   │   ├── client.go         # TCP Client（自动重连 + 连接池）
│   │   │   ├── worker_pool.go    # WorkerPool（弹性扩容 SpawnTemp）
│   │   │   └── options.go        # 完整 Functional Options
│   │   ├── udp/
│   │   │   ├── session.go        # UDP Session（伪会话，UDPAddr）
│   │   │   ├── server.go         # UDP Server（readLoop + sweepLoop）
│   │   │   └── options.go
│   │   ├── http/
│   │   │   ├── session.go        # HTTPSession（per-request，可选）
│   │   │   ├── server.go         # HTTP Server（net/http + 可选 Plugin）
│   │   │   └── options.go
│   │   ├── websocket/
│   │   │   ├── session.go        # WS Session（writeMutex + Ping/Pong）
│   │   │   ├── server.go         # WS Server（upgrade + pingLoop）
│   │   │   └── options.go
│   │   └── coap/
│   │       ├── message.go        # CoAP 帧解析 + Block-wise 分块
│   │       ├── session.go        # CoAP Session（CON 重传 + MessageID 去重）
│   │       ├── server.go         # CoAP Server（插件链 + ACK）
│   │       └── options.go
│   ├── gateway/
│   │   ├── gateway.go            # Gateway（全局插件 + 共享 SessionManager + 6 段优雅关闭）
│   │   └── options.go
│   ├── defense/
│   │   ├── overload.go           # OverloadProtector（水位检测 + 降级矩阵）
│   │   ├── backpressure.go       # 背压控制（写队列水位监控）
│   │   └── sampler.go            # 高频日志采样器
│   └── utils/
│       ├── atomic.go             # 自定义 atomic 辅助工具
│       ├── sync.go               # 扩展 sync 工具（ShardedMap 等）
│       └── net.go                # IP / CIDR 解析工具
├── tests/
│   ├── unit/                     # 单元测试
│   ├── integration/              # 集成测试
│   └── benchmark/                # 基准测试
├── examples/
│   ├── basic_tcp/main.go
│   ├── basic_udp/main.go
│   ├── basic_http/main.go
│   ├── basic_websocket/main.go
│   ├── basic_coap/main.go
│   ├── multi_protocol/main.go
│   ├── tls_server/main.go
│   ├── tcp_client/main.go
│   ├── session_plugins/main.go
│   ├── custom_protocol/main.go
│   └── graceful_shutdown/main.go
├── scripts/
│   ├── build.sh
│   └── docker-build.sh
├── Dockerfile
├── docker-compose.yml
├── go.mod                        # Go 1.24，外部依赖仅 2 个
├── go.sum
└── README.md
```

---

## 4. 核心类型系统（internal/types/）

### 4.1 枚举（enums.go）

```
ProtocolType uint8:
  TCP(1) | TLS(2) | UDP(3) | HTTP(4) | WebSocket(5) | CoAP(6) | Custom(99)

MessageType uint8:
  Text(1) | Binary(2) | Ping(3) | Pong(4) | Close(5)
  CoAPGet(10) | CoAPPost(11) | CoAPPut(12) | CoAPDelete(13) | CoAPACK(14)

SessionState uint8:
  Connecting(0) | Active(1) | Closing(2) | Closed(3)

所有枚举实现 String() 方法，底层 uint8，编译期常量。
使用 unique 包池化协议标签字符串（Go 1.24）。
```

### 4.2 消息（message.go）

```
Message[T any] struct {
    ID        string           // 消息唯一标识（UUID v7）
    SessionID uint64           // 所属会话 ID
    Protocol  ProtocolType     // 来源协议
    Type      MessageType      // 消息类型
    Payload   T                // 泛型载荷
    Timestamp time.Time        // 创建时间
    Metadata  map[string]any   // 扩展元数据（预分配容量 4）
}

type RawMessage = Message[[]byte]  // 90% 场景的类型别名

func NewRawMessage(sessionID uint64, proto ProtocolType, payload []byte) RawMessage

// 泛型约束：编译期类型安全
type MessageConstraint interface {
    comparable
}
```

### 4.3 会话接口（session.go）

**核心设计原则：泛型 Session[M] 保留编译期类型安全，Send([]byte) 统一底层写路径**

```
// 泛型会话接口：提供编译期类型安全
Session[M MessageConstraint] interface {
    // === 身份与元信息（不可变）===
    ID()           uint64
    Protocol()     ProtocolType
    RemoteAddr()   net.Addr
    LocalAddr()    net.Addr
    CreatedAt()    time.Time

    // === 状态（原子操作）===
    State()        SessionState
    IsAlive()      bool
    LastActiveAt() time.Time

    // === 核心发送（两条路径，底层共享 writeQueue）===
    Send(data []byte) error      // 通用路径：直接发送字节，零转换开销
    SendTyped(msg M) error       // 类型安全路径：encode(msg) → Send([]byte)

    // === 生命周期 ===
    Close() error                // 幂等关闭（drain 写队列）
    Context() context.Context    // 关闭时 cancel

    // === 元数据（线程安全 KV）===
    SetMeta(key string, val any)
    GetMeta(key string) (any, bool)
    DelMeta(key string)
}

// RawSession：最常用的类型别名，等价于非泛型版本
type RawSession = Session[[]byte]

// 编译期验证：RawSession.Send 与 Session[[]byte].Send 完全等价
// 外部代码可直接使用 RawSession，无需关心泛型
```

**SendTyped 与 Send 关系说明：**

```
SendTyped(msg M) 内部实现路径：
  1. 调用注册的 Encoder[M]：encode(msg M) → ([]byte, error)
  2. 调用 Send([]byte)          ← 统一写队列路径，0 额外 goroutine
  3. 归还编码 buffer 到 BufferPool

// 当 M = []byte 时，SendTyped 与 Send 完全等价，编译器内联消除开销
// 当 M = CustomMsg 时，提供编译期类型检查，避免运行时类型断言
```

**SessionManager 接口：**

```
SessionManager interface {
    Register(sess RawSession) error
    Unregister(id uint64)
    Get(id uint64) (RawSession, bool)
    Count() int64
    // Go 1.24 iter.Seq 风格遍历（替代回调函数）
    All() iter.Seq[RawSession]
    Range(fn func(RawSession) bool)   // 兼容旧风格
    Broadcast(data []byte)
    Close() error
}
```

**Session 状态机：**

```
Connecting ──accept──→ Active ──Close()──→ Closing ──drain──→ Closed
    │                    │                                        ▲
    └──error/fatal───────┴────────────────────────────────────────┘

状态转换：atomic.Int32 CAS 保证并发唯一性
幂等关闭：sync.Once 封装 Close 逻辑
```

### 4.4 处理器（handler.go）

```
// 函数类型（最常用路径）
type MessageHandler[T any] func(sess Session[T], msg Message[T]) error
type RawHandler = MessageHandler[[]byte]

// Server 基础接口
type Server interface {
    Start() error
    Stop(ctx context.Context) error
    Protocol() ProtocolType
}

// 泛型 Server 接口（高级场景，保留编译期类型信息）
type TypedServer[M MessageConstraint] interface {
    Start() error
    Stop(ctx context.Context) error
    Handler() MessageHandler[M]
}
```

### 4.5 插件接口（plugin.go）

```
Plugin interface {
    Name()     string
    Priority() int           // 数字越小越先执行

    OnAccept(sess RawSession) error                          // 返回 ErrBlock 拒绝连接
    OnMessage(sess RawSession, data []byte) ([]byte, error)  // 支持消息变换
    OnClose(sess RawSession)                                 // 清理资源
}

BasePlugin struct{}  // 空实现基类，嵌入后按需覆盖

// 特殊控制错误（非真正错误，控制插件链行为）
var (
    ErrSkip  = errors.New("shark: plugin skip")   // 消息被截获，停止后续插件
    ErrDrop  = errors.New("shark: plugin drop")   // 丢弃消息，不传给 Handler
    ErrBlock = errors.New("shark: plugin block")  // 拒绝连接
)
```

**设计决策：**
- `OnMessage` 使用 `RawSession`（即 `Session[[]byte]`），保证插件层无需感知业务消息类型
- 插件 Plugin 接口不引入泛型，保持运行时多态，支持动态注册
- `Priority()` 替代 OrderedPlugin —— 统一接口，启动时静态排序，热路径零开销
- `BasePlugin` 嵌入式空实现 —— 自定义插件只需覆盖关心的方法

---

## 5. 错误体系（internal/errs/errors.go）

```
系统错误：
  ErrServerClosed / ErrServerNotStarted / ErrListenFailed

会话错误：
  ErrSessionNotFound / ErrSessionClosed / ErrSessionCapacity / ErrSessionLimit

消息错误：
  ErrMessageTooLarge / ErrInvalidMessage / ErrWriteQueueFull
  ErrInvalidFrame / ErrFrameTooLarge

超时错误：
  ErrReadTimeout / ErrWriteTimeout / ErrIdleTimeout / ErrHeartbeatTimeout

协议错误：
  ErrCoAPInvalidMessage / ErrUnsupportedVersion

插件控制（非错误，控制流语义）：
  ErrSkip / ErrDrop / ErrBlock
  ErrPluginRejected / ErrPluginDuplicate

安全错误：
  ErrRateLimited / ErrBlacklisted / ErrAutoBanned / ErrMessageRateExceeded

资源错误：
  ErrResourceExhausted / ErrFDLimit / ErrMemoryLimit / ErrOverloaded

基础设施错误：
  ErrCacheMiss / ErrStoreNotFound / ErrPubSubClosed
  ErrCircuitOpen / ErrInfrastructure / ErrDegraded

网关错误：
  ErrNoServerRegistered / ErrDuplicateProtocol
  ErrGracefulShutdown（关闭信号，Handler 可感知）

编码错误：
  ErrEncodeFailure / ErrDecodeFailure（SendTyped 路径）

分类判断函数：
  IsRetryable(err)          → ErrWriteQueueFull / ErrCircuitOpen
  IsFatal(err)              → ErrSessionClosed / ErrServerClosed
  IsRecoverable(err)        → ErrCircuitOpen / ErrInfrastructure
  IsSecurityRejection(err)  → ErrBlacklisted / ErrAutoBanned / ErrRateLimited
  IsPluginControl(err)      → ErrSkip / ErrDrop / ErrBlock
```

---

## 6. 基础设施层（internal/infra/）

### 6.1 BufferPool（bufferpool/pool.go）

**六级 sync.Pool：**

| 级别 | 大小范围 | 编号 | 典型场景 |
|------|----------|------|----------|
| Micro | <= 128B | pool[0] | CoAP/心跳控制帧、ACK 包、UDP 小包 |
| Tiny | <= 512B | pool[1] | 短文本消息、命令帧、KeepAlive |
| Small | <= 4KB | pool[2] | 普通业务消息、JSON 请求 |
| Medium | <= 32KB | pool[3] | HTTP Body、批量数据 |
| Large | <= 256KB | pool[4] | 大消息、文件块、流媒体分片 |
| Huge | > 256KB | 不入池 | 直接 make，避免大对象长期驻留 |

**接口：**

```
Get(size int) *Buffer      获取 >= size 的缓冲区（自动选择最小合适级别）
Put(buf *Buffer)           归还（自动清零敏感数据）
Stats() PoolStats          命中率 / 分配次数 / 总内存 / 各级统计

全局单例：Default / GetDefault() / Put()
超大 buffer 归还时直接丢弃（防池内存膨胀）
TotalMemoryCap：可配置池总内存上限（超限强制 GC）
```

**级别选择算法：**

```
GetLevel(size int) int:
  size <= 512   → 0 (Micro)
  size <= 2048   → 1 (Tiny)
  size <= 4096  → 2 (Small)
  size <= 32768 → 3 (Medium)
  size <= 262144→ 4 (Large)
  else          → 直接 make（不入池）
```

### 6.2 Logger（logger/logger.go）

```
Logger interface：
  Debug(msg string, args ...any)
  Info(msg string, args ...any)
  Warn(msg string, args ...any)
  Error(msg string, args ...any)
  With(args ...any) Logger
  WithContext(ctx context.Context) Logger

默认实现：slogLogger（Go 1.21+ slog，JSON 格式）
测试实现：NopLogger（零操作，基准测试用）
可选适配：ZapLogger（高性能场景）

特性：
  With(args...)        创建子 logger，附加固定字段
  WithContext(ctx)     自动提取 trace_id / request_id
  SetDefault(l)        运行时全局替换

关键字段规范：
  session_id, protocol, remote_addr, plugin_name, error, duration_ms, trace_id

高频日志采样：
  logSampler：相同消息键每秒汇总（第 1 次完整输出 + "已省略 N 次"）
异步写入：带缓冲 channel → 单 goroutine 刷盘
日志级别动态调整：POST /admin/log-level
```

### 6.3 Metrics（metrics/metrics.go）

```
接口抽象：
  Counter(name string, labels ...string) CounterVec   → Inc() / Add(n float64)
  Gauge(name string, labels ...string) GaugeVec       → Set(v) / Inc() / Dec()
  Histogram(name string, labels ...string) HistogramVec → Observe(v float64)
  Timer(name string, labels ...string) TimerVec       → ObserveDuration(start time.Time)

内置 Prometheus 指标（静态预注册，避免运行时动态 map 分配）：
  shark_connections_total{protocol}             Counter
  shark_connections_active{protocol}            Gauge
  shark_connection_errors_total{protocol}       Counter
  shark_messages_total{protocol,type}           Counter
  shark_message_bytes{protocol,direction}       Histogram
  shark_message_duration_seconds{protocol}      Histogram
  shark_plugin_duration_seconds{plugin}         Histogram
  shark_errors_total{protocol,kind}             Counter
  shark_worker_queue_depth{protocol}            Gauge
  shark_worker_panics_total{protocol}           Counter
  shark_session_lru_evictions_total             Counter
  shark_rejected_connections_total{reason}      Counter
  shark_write_queue_full_total{protocol}        Counter
  shark_fd_usage_ratio                          Gauge
  shark_overloaded                              Gauge
  shark_autoban_total{ip}                       Counter
  shark_bufferpool_hits_total{level}            Counter   ← 六级 pool 各自统计
  shark_bufferpool_misses_total{level}          Counter

实现：热路径 atomic 计数，异步批量上报 Prometheus
```

### 6.4 Cache（cache/cache.go）

```
Cache interface：
  Get(ctx, key string) ([]byte, error)
  Set(ctx, key string, val []byte, ttl time.Duration) error
  Del(ctx, key string) error
  Exists(ctx, key string) (bool, error)
  TTL(ctx, key string) (time.Duration, error)
  MGet(ctx, keys []string) (map[string][]byte, error)

内置：MemoryCache（sync.RWMutex + TTL 惰性过期）
适配器：RedisCache（预留）
```

### 6.5 Store（store/store.go）

```
Store interface：
  Save(ctx, key string, val []byte) error
  Load(ctx, key string) ([]byte, error)
  Delete(ctx, key string) error
  Query(ctx, prefix string) ([][]byte, error)

内置：MemoryStore
适配器：RedisStore / SQLStore / BoltDBStore（预留）
```

### 6.6 PubSub（pubsub/pubsub.go）

```
PubSub interface：
  Publish(ctx, topic string, data []byte) error
  Subscribe(ctx, topic string, handler func([]byte)) (Subscription, error)

Subscription interface：
  Unsubscribe() error
  Topic() string

内置：ChannelPubSub（Go channel + fan-out goroutine）
适配器：RedisPubSub / NATSPubSub / KafkaPubSub（预留）
```

### 6.7 CircuitBreaker（circuitbreaker/circuitbreaker.go）

```
CircuitBreaker struct：
  state     atomic.Int32    // Closed(0) / Open(1) / HalfOpen(2)
  failures  atomic.Int64    // 连续失败计数
  threshold int64           // 连续失败阈值（默认 5）
  timeout   time.Duration   // 熔断恢复时间（默认 30s）
  openAt    atomic.Int64    // Open 时间戳（UnixNano）

状态机：
  Closed   → 正常调用；连续失败 > threshold → Open
  Open     → 直接返回 ErrCircuitOpen；超过 timeout → HalfOpen
  HalfOpen → 允许一次探测调用；成功 → Closed；失败 → Open

应用场景：PersistencePlugin(store) / ClusterPlugin(pubsub) / Cache 访问保护
```

---

## 7. 会话管理层（internal/session/）

### 7.1 BaseSession（session.go）

```
BaseSession struct {
    id         uint64           // 不可变，全局唯一
    protocol   ProtocolType     // 不可变
    remoteAddr net.Addr         // 不可变
    localAddr  net.Addr         // 不可变
    state      atomic.Int32     // SessionState，CAS 状态机
    lastActive atomic.Int64     // UnixNano，TouchActive 无锁更新
    ctx        context.Context
    cancel     context.CancelFunc
    meta       sync.Map         // 任意元数据，并发安全
    closeOnce  sync.Once        // Close 幂等保证
}

核心方法：
  NewBase(id, proto, remote, local) → 初始化，State=Connecting
  TouchActive()                     → atomic.Store(lastActive, now)
  SetState(new SessionState) bool   → CAS 转换，返回是否成功
  CancelContext()                   → cancel()，通知所有 <-ctx.Done()
```

**各协议 Session 嵌入 BaseSession，并持有 Encoder[M] 用于 SendTyped：**

```
TCPSession[M MessageConstraint] struct {
    *BaseSession
    conn       net.Conn
    framer     Framer
    writeQueue chan []byte           // 统一字节写队列
    encoder    func(M) ([]byte, error)  // SendTyped 编码器（nil 时 M=[]byte）
    closeOnce  sync.Once
}

Send(data []byte) error       → 直接入队，零转换
SendTyped(msg M) error        → encoder(msg) → Send([]byte)

编译期验证：var _ types.RawSession = (*TCPSession[[]byte])(nil)
```

### 7.2 SessionManager（manager.go）

```
Manager struct {
    shards   [32]shard          // 分片锁，32 路并行（位运算选 shard）
    idGen    atomic.Uint64      // 全局原子 ID，从 1 开始
    maxSess  int64              // 最大会话数（默认 1,000,000）
    totalAct atomic.Int64       // 全局活跃计数（无锁）
}

shard struct {
    mu       sync.RWMutex
    sessions map[uint64]RawSession
    lru      *LRUList            // 每分片独立 LRU
}

分片函数：shardIndex(id) = id & 31（等价 id % 32，位运算更快）

核心操作：
  NextID()          → atomic.Add(1)，确保全局单调递增
  Register(sess)    → shard.mu.Lock → sessions[id] = sess → lru.Touch(id)
                    → totalAct.Add(1)
                    → 超容：LRU Evict 最旧会话 → 被淘汰会话调用 Close
  Unregister(id)    → shard.mu.Lock → delete(sessions, id) → lru.Remove(id)
                    → totalAct.Add(-1)
  Get(id)           → shard.mu.RLock → sessions[id]，O(1)
  Count()           → totalAct.Load（无锁）
  All()             → iter.Seq[RawSession]（Go 1.24 iter 包）
  Range(fn)         → 依次对 32 shard RLock 遍历
  Broadcast(data)   → Range + sess.Send，扇出限速
  Close()           → Range → sess.Close() → lru.stop

编译期验证：var _ types.SessionManager = (*Manager)(nil)
```

### 7.3 LRUList（lru.go）

```
LRUList struct {
    head     *lruNode             // 最近活跃（链表头）
    tail     *lruNode             // 最久未活跃（链表尾）
    index    map[uint64]*lruNode  // ID → 节点，O(1) 查找
    nodePool sync.Pool            // lruNode 对象复用，减少 GC 压力
    mu       sync.Mutex           // 配合 shard.mu 使用，不单独加锁
}

操作：
  Touch(id)   O(1)  移动节点到链表头（表示最近活跃）
  Evict(n)    O(n)  从链表尾部淘汰 n 个节点，返回被淘汰 ID 列表
  Remove(id)  O(1)  删除指定节点

淘汰保护：正在执行 Send 操作的会话通过引用计数跳过淘汰
```

---

## 8. 插件系统（internal/plugin/）

### 8.1 PluginChain（chain.go）

```
Chain struct {
    plugins    []types.Plugin     // 按 Priority 升序静态排序
    nameIndex  map[string]int     // 名称 → 索引，去重用
    stopOnError bool              // 默认 true
}

注册阶段（Server 启动时，一次性操作）：
  1. 按 Priority() 升序排列（使用 slices.SortFunc，Go 1.21+）
  2. 预构建 []Plugin 切片（热路径直接索引，无排序开销）
  3. 同名插件后注册覆盖，ErrPluginDuplicate 记录 Warn 日志

执行阶段（热路径）：

  OnAccept(sess)：
    for each plugin（顺序）：
      → ErrBlock  → 中断 + Close(sess) + 记录 metrics
      → 其他 err + stop  → 中断 + Close(sess)
      → 其他 err + !stop → 记录日志 + 继续
      → nil → 下一个

  OnMessage(sess, data) → ([]byte, error)：
    for each plugin（顺序）：
      out, err = plugin.OnMessage(sess, data)
      → ErrSkip → 消息被截获，停止后续插件，返回原 data
      → ErrDrop → 丢弃消息，不传给 Handler
      → 其他 err + stop  → 中断
      → 其他 err + !stop → 记录 + 继续
      → nil → data = out，传入下一个插件

  OnClose(sess)：
    for each plugin（逆序，确保资源全部释放）：
      plugin.OnClose(sess)  // 始终全部执行，忽略错误
```

### 8.2 内置插件与推荐 Priority

| Priority | 插件 | 职责 |
|----------|------|------|
| 0 | BlacklistPlugin | IP/CIDR 黑名单过滤（最高优先级） |
| 10 | RateLimitPlugin | 令牌桶限流（连接 + 消息双层） |
| 20 | AutoBanPlugin | 自动封禁（触发限流/错误阈值） |
| 30 | HeartbeatPlugin | 时间轮心跳超时检测 |
| 40 | ClusterPlugin | 集群事件广播 + 跨节点路由 |
| 50 | PersistencePlugin | 会话状态异步持久化 |

**BlacklistPlugin（Priority=0）**

```
存储结构：
  exactMap  map[string]expireEntry  // 精确 IP，O(1) 查找
  cidrList  []net.IPNet             // CIDR 段，顺序遍历
  cache     infra.Cache             // 分布式黑名单缓存（Cache Miss 降级本地）

OnAccept：提取纯 IP → exactMap O(1) → CIDR 遍历 → Cache 双查
动态管理：Add(ip, ttl) / Remove(ip) / Reload(list) API
TTL 过期：惰性过期 + 后台清理 goroutine（每分钟）
```

**RateLimitPlugin（Priority=10）**

```
双层令牌桶：
  globalBucket  *tokenBucket          // 全局速率上限
  perIPBuckets  sync.Map              // per-IP 独立桶（Go 1.24 CompareAndDelete 优化清理）

算法：漏桶补充（按实际时间差补充令牌）→ 检查 >= 1 → 原子扣减
OnAccept：连接速率限流（返回 ErrBlock）
OnMessage：消息速率限流（返回 ErrDrop）
后台：cleanupLoop，每 2 分钟清理空闲 IP 桶
连续触发 N 次限流 → 通知 AutoBanPlugin
```

**HeartbeatPlugin（Priority=30）**

```
时间轮方案（替代 per-session time.Ticker）：
  TimeWheel：单 goroutine 管理全部会话定时器
  10 万连接仅需 1 个系统 goroutine
  精度：1 秒（slot 间隔），轮大小 = ceil(timeout/slot)
  Add/Remove/Reset 均 O(1)

OnAccept：timeWheel.Add(sess.ID(), timeout)
OnMessage：收到任意消息 → timeWheel.Reset(sess.ID())
超时回调：sess.Close() + 记录 shark_heartbeat_timeout_total

协议适配：
  TCP/UDP：发送自定义 pingData 字节序列
  WebSocket：调用 sendPing()（使用协议内置 PingMessage）
```

**PersistencePlugin（Priority=50）**

```
OnAccept：Store.Load(sess.ID()) → sess.SetMeta("history", data)
OnMessage：序列化消息 → 写入本地 writeCh（有界 channel，容量 1024）
         → 后台 goroutine 批量 Store.Save（每 100 条或 500ms 刷一次）
OnClose：同步写入最终快照 → Store.Save

序列化：默认 encoding/json，可替换 protobuf / msgpack
熔断保护：Store 调用包裹 CircuitBreaker
```

**ClusterPlugin（Priority=40）**

```
OnAccept：
  PubSub.Publish("session.joined", {sessID, nodeID})
  Cache.Set("session:route:"+sessID, nodeID, sessionTTL)

OnClose：
  PubSub.Publish("session.left", {sessID})
  Cache.Del("session:route:"+sessID)

跨节点路由：
  本地 Manager.Get(targetID) → not found
  → Cache.Get("session:route:"+targetID) → nodeID
  → PubSub.Publish("node."+nodeID+".route", {targetID, payload})

节点心跳：
  每 heartbeatTTL/2 执行 Cache.Set("node:"+nodeID, meta, heartbeatTTL)
```

**AutoBanPlugin（Priority=20）**

```
触发条件：
  单 IP 限流超 N 次（默认 10 次/分钟）
  单 IP 协议错误超 M 次（默认 5 次/分钟）
  单 IP 空连接超 K 次（默认 20 次/分钟，Slowloris 防御）

动作：调用 BlacklistPlugin.Add(ip, banTTL)，banTTL 默认 30 分钟
指标：shark_autoban_total{ip, reason}
```

---

## 9. 协议层（internal/protocol/）

### 9.1 TCP 协议

**Options 完整配置（options.go）**

| 配置项 | 默认值 | 说明 |
|--------|--------|------|
| Host | "0.0.0.0" | 监听地址 |
| Port | 8080 | 监听端口 |
| WorkerCount | NumCPU×2 | 核心 Worker 数量 |
| TaskQueueSize | WorkerCount×128 | 任务队列容量 |
| MaxWorkers | WorkerCount×4 | 最大 Worker（含临时扩容） |
| FullPolicy | PolicyDrop | 队列满策略（Block/Drop/SpawnTemp） |
| WriteQueueSize | 128 | 每连接写队列容量 |
| WriteFullPolicy | PolicyBlock | 写队列满策略 |
| MaxSessions | 100,000 | 最大会话数 |
| MaxMessageSize | 1MB | 单消息最大字节 |
| ReadTimeout | 0（不限） | 读超时 |
| WriteTimeout | 10s | 写超时 |
| IdleTimeout | 0（不限） | 空闲超时 |
| HandlerTimeout | 0（不限） | Handler 执行超时 |
| DrainTimeout | 5s | 关闭时写队列 drain 超时 |
| ShutdownTimeout | 10s | 服务关闭超时 |
| Framer | LengthPrefixFramer | 帧解析器 |
| MaxConsecutiveErrors | 100 | 连续错误上限（超限断连） |
| TLSConfig | nil | TLS 配置（nil=不启用） |
| Plugins | nil | 协议级插件列表 |

**Framer 接口（framer.go）**

```
Framer interface {
    ReadFrame(r io.Reader) (payload []byte, err error)
    WriteFrame(w io.Writer, payload []byte) error
}

4 种内置实现：
  LengthPrefixFramer   4 字节大端长度前缀（默认）
  LineFramer           换行符 \n 分隔（文本协议）
  FixedSizeFramer      固定大小帧（帧大小编译时指定）
  RawFramer            直接透传（无帧边界，自行处理粘包）

所有实现使用 bufio.Reader 减少系统调用次数。
```

**WorkerPool（worker_pool.go）**

```
WorkerPool struct {
    workers    []*worker
    taskQueue  chan task           // 有界 channel（阻塞生产者）
    wg         sync.WaitGroup
    maxWorkers int                // 最大 Worker（含临时）
    tempCount  atomic.Int32       // 当前临时 Worker 数量
    closed     atomic.Bool
}

FullPolicy 策略：
  PolicyBlock     → 阻塞等待队列空间（金融场景，不可丢消息）
  PolicyDrop      → 丢弃消息 + metrics（推荐默认）
  PolicySpawnTemp → 动态扩容临时 Worker（突发流量，处理完自动退出）
  PolicyClose     → 持续过载 30s → 主动关闭连接（极端情况）

安全：每个 worker 执行 safeRun，内置 recover(panic)
```

**TCPSession（session.go）**

```
TCPSession[M MessageConstraint] struct {
    *BaseSession
    conn       net.Conn
    framer     Framer
    writeQueue chan []byte         // 有界写队列
    encoder    func(M) ([]byte, error)
    closeOnce  sync.Once
}

并发模型（每连接 2 个 goroutine）：
  readLoop：bufpool.Get → Framer.ReadFrame(conn) → chain.OnMessage → pool.Submit
  writeLoop：for data := range writeQueue → conn.Write → bufpool.Put

Send(data []byte)：
  1. IsAlive() 检查
  2. copy(buf, data)（隔离池化 buffer）
  3. select { case writeQueue <- buf: default: ErrWriteQueueFull }

SendTyped(msg M)：
  1. encoder(msg) → []byte（使用 Micro/Tiny BufferPool）
  2. Send([]byte)

Close() 状态机（6 步）：
  1. CAS Active → Closing（失败说明已在关闭，直接返回）
  2. 关闭新消息写入 writeQueue（关闭 channel 信号）
  3. 等待 writeQueue 全部发送（DrainTimeout 超时强制继续）
  4. CAS Closing → Closed
  5. CancelContext() → 通知 writeLoop / pingLoop 退出
  6. conn.Close()
```

**TCPServer（server.go）**

```
Start()：
  1. net.Listen → listener
  2. pool.Start()（启动 Worker goroutine）
  3. go acceptLoop()

acceptLoop()：
  for {
    conn, err = listener.Accept()
    → err: 指数退避（5ms → 1s → 10s）→ 超过 MaxConsecutiveErrors → Stop
    → ok: metrics.connections_total++ → go handleConn(conn)
  }

handleConn(conn)：
  newSession → manager.Register(sess)
  → 超容: LRU Evict
  chain.OnAccept(sess) → ErrBlock: Close + return
  go sess.readLoop()
  go sess.writeLoop()

Stop(ctx)：
  listener.Close() → pool.Stop() → manager.Close() → wg.Wait(ctx)
```

**TCPClient（client.go）**

```
Client[T any] struct {
    addr    string
    conn    net.Conn
    encoder func(T) ([]byte, error)
    decoder func([]byte) (T, error)
    timeout time.Duration
    reconnect bool
    backoff    ExponentialBackoff  // 初始 100ms，最大 30s，Jitter ±20%
    pool       *ConnPool
}

RawClient = Client[[]byte]

方法：
  Connect() error
  Send(msg T) error         // T=[]byte 时零开销
  Receive() (T, error)
  Close() error
```

### 9.2 UDP 协议

```
UDPSession struct {
    *BaseSession
    conn *net.UDPConn
    addr *net.UDPAddr
    mu   sync.Mutex         // 并发写保护（UDP 无内置写队列）
}

Send(data)：conn.WriteToUDP(data, addr)（直接写，无队列）
Close()：SetState(Closed) → CancelContext()（无需 drain）

UDPServer struct {
    conn      *net.UDPConn                // 单 conn 复用
    sessions  sync.Map                   // addr.String() → Session（伪会话）
    sweepTTL  time.Duration              // Session 空闲 TTL（默认 60s）
}

readLoop()：
  bufpool.Get(65535) → conn.ReadFromUDP
  → 查找或新建 Session（by addr）→ chain.OnMessage → handler（直接调用）

sweepLoop()：
  定期遍历 sessions → lastActive 超过 TTL → Close + Unregister
```

### 9.3 HTTP 协议

```
两种模式：

模式 A（默认）：纯 net/http 包装
  server.Handle(pattern, handler)
  server.HandleFunc(pattern, func(w, r))
  → 不涉及 Session / Plugin，最轻量

模式 B（可选）：per-request Session + Plugin 集成
  HTTPSession struct：
    *BaseSession
    w http.ResponseWriter
    r *http.Request
    // Close() = 完成响应（WriteHeader + Flush）
    // Send(data) = w.Write(data)

  流程：
    OnAccept(httpSess)
    → 读 Body → chain.OnMessage(sess, body)
    → handler(sess, msg)
    → OnClose(httpSess)

  注意：HTTPSession 不注册到 SessionManager
        （per-request 开销高，HTTP 无持久会话语义）

Options：Host, Port, ReadTimeout, WriteTimeout, IdleTimeout, TLSConfig
```

### 9.4 WebSocket 协议

```
WSSession struct {
    *BaseSession
    conn    *websocket.Conn
    writeMu sync.Mutex         // gorilla/websocket 写操作非并发安全
}

Send(data)：    writeMu.Lock → WriteMessage(BinaryMessage, data) → Unlock
SendText(data)：writeMu.Lock → WriteMessage(TextMessage, data) → Unlock
sendPing()：    writeMu.Lock → WriteMessage(PingMessage, nil) → Unlock

WSServer 流程：
  HTTP Upgrade → conn.SetReadLimit(MaxMessageSize)
  → newWSSession → manager.Register
  → handleSession：
    PongHandler = TouchActive + 重设 ReadDeadline
    go pingLoop：ticker → sendPing → Context().Done() 退出
    readLoop：ReadMessage → chain.OnMessage → pool.Submit(handler)

ReadDeadline = now + PingInterval + PongTimeout

Options：
  Host, Port, Path, MaxSessions
  PingInterval(30s), PongTimeout(10s)
  MaxMessageSize(1MB)
  AllowedOrigins（生产必须配置）
  UpgradeTimeout(5s)
  TLSConfig
```

### 9.5 CoAP 协议

```
CoAPMessage struct {
    Version   uint8         // 必须为 1
    Type      CoAPMsgType   // CON(0) / NON(1) / ACK(2) / RST(3)
    TokenLen  uint8         // TKL <= 8
    Code      CoAPCode      // 方法码或响应码
    MessageID uint16        // 消息去重 ID
    Token     []byte        // 关联请求/响应
    Options   []CoAPOption  // CoAP 选项（Uri-Path / Content-Format 等）
    Payload   []byte        // 应用层数据
}

帧校验：len >= 4, TKL <= 8, Version == 1, Token 长度一致性

CoAPSession（嵌入 UDPSession）：
  pendingACKs map[uint16]*pendingMsg   // CON 消息等待确认状态
  msgCache    lru.Cache[uint16, bool]  // MessageID 去重（最近 500 条）

CON 可靠性机制：
  收到 CON → 立即回复 ACK → 交给 handler 处理
  超时未收 ACK → 重传（AckTimeout × 2^attempt，最多 MaxRetransmit=4 次）
  收到 RST → 取消对应 MessageID 的重传计划
  MessageID 重复 → 静默丢弃（msgCache 命中）

Block-wise 分块传输：
  大消息分块发送（Block1 请求 / Block2 响应）
  自动重组分块

Options：
  Host, Port
  MaxSessions, SessionTTL(5m)
  AckTimeout(2s), MaxRetransmit(4)
  MessageIDCacheSize(500)
  Plugins
  DTLSConfig（可选，基于 pion/dtls）
```

---

## 10. 网关层（internal/gateway/）

```
Gateway struct {
    servers       map[ProtocolType]types.Server
    globalPlugins []types.Plugin      // 全局插件（优先于各协议插件）
    sharedManager *session.Manager    // 共享 SessionManager
    logger        infra.Logger
    metrics       infra.Metrics
    opts          Options
    startTime     time.Time
}

全局插件注入：
  合并：globalPlugins + serverPlugins → 按 Priority 统一排序
  同名：后注册覆盖，记录 Warn 日志

共享 SessionManager（跨协议感知）：
  TCP / WS / UDP Session 统一注册到同一 Manager
  跨协议查询：manager.Get(sessID) 可找到任意协议的 Session
  跨协议广播：manager.Broadcast(data) 一次性发送到所有协议连接

生命周期：
  Start()：
    1. 检查 servers 非空
    2. 启动 Metrics HTTP 服务：go serveMetrics()
    3. 并发启动所有 Server
    4. 等待所有启动完成；任一失败 → 停止已启动的 → 返回聚合错误

  Stop(ctx)：6 阶段优雅关闭
    阶段1：各 Server 关闭 Listener，停止 Accept 新连接
    阶段2：广播 ErrGracefulShutdown 信号，业务 Handler 可感知
    阶段3：per-session writeQueue drain + WorkerPool drain（等待处理中任务）
    阶段4：PluginChain.OnClose(sess, ...) 逆序执行（持久化 / 集群注销）
    阶段5：SessionManager 全部 Unregister + conn.Close + Flush Logger/Metrics
    阶段6：ctx 超时 → 强制关闭（不等待）

  Run()：Start() → signal.NotifyContext(SIGTERM, SIGINT) → Stop()

  serveMetrics()（独立 HTTP 服务）：
    GET /metrics      → Prometheus 标准格式
    GET /healthz      → JSON { status, uptime, protocols, sessions, system }
    GET /readyz       → JSON { status: "ready" }
    POST /admin/log-level → 动态调整日志级别

Options：
  ShutdownTimeout(15s)
  MetricsAddr(":9090")
  EnableMetrics(true)
  GlobalPlugins
  SharedManager(true)
  OverloadConfig
```

---

## 11. API 层（api/api.go）

**类型别名（公开导出）**

```
// Session 相关
Session[M]     → types.Session[M]
RawSession     → types.RawSession      // = Session[[]byte]
SessionManager → types.SessionManager
SessionState   → types.SessionState

// 消息相关
Message[T]     → types.Message[T]
RawMessage     → types.RawMessage      // = Message[[]byte]
MessageConstraint → types.MessageConstraint

// 处理器
MessageHandler[M] → types.MessageHandler[M]
RawHandler        → types.RawHandler   // = MessageHandler[[]byte]

// 服务端
Server         → types.Server
TypedServer[M] → types.TypedServer[M]

// 插件
Plugin         → types.Plugin
BasePlugin     → types.BasePlugin

// 枚举
ProtocolType   → types.ProtocolType
MessageType    → types.MessageType

// 基础设施
Logger         → infra.Logger
Cache          → infra.Cache
Store          → infra.Store
PubSub         → infra.PubSub
Subscription   → infra.Subscription
CircuitBreaker → infra.CircuitBreaker

// 协议常量
TCP, TLS, UDP, HTTP, WebSocket, CoAP
```

**工厂函数**

```
服务端：
  NewGateway(opts ...GatewayOption) *Gateway
  NewTCPServer(handler RawHandler, opts ...TCPOption) *tcp.Server
  NewUDPServer(handler RawHandler, opts ...UDPOption) *udp.Server
  NewHTTPServer(opts ...HTTPOption) *http.Server
  NewWebSocketServer(handler RawHandler, opts ...WSOption) *ws.Server
  NewCoAPServer(handler RawHandler, opts ...CoAPOption) *coap.Server

// 泛型服务端（编译期类型安全）
  NewTypedTCPServer[M MessageConstraint](
    handler MessageHandler[M],
    enc func(M)([]byte,error),
    dec func([]byte)(M,error),
    opts ...TCPOption,
  ) *tcp.Server

客户端：
  NewTCPRawClient(addr string, opts ...TCPClientOption) *tcp.Client[[]byte]
  NewTCPClient[T any](
    addr string,
    enc func(T)([]byte,error),
    dec func([]byte)(T,error),
    opts ...TCPClientOption,
  ) *tcp.Client[T]

插件工厂：
  NewBlacklistPlugin(ips ...string) Plugin
  NewRateLimitPlugin(rate, burst float64) Plugin
  NewHeartbeatPlugin(interval, timeout time.Duration) Plugin
  NewPersistencePlugin(store Store) Plugin
  NewClusterPlugin(nodeID string, pubsub PubSub, cache Cache) Plugin
  NewAutoBanPlugin(opts ...AutoBanOption) Plugin

基础设施：
  SetLogger(l Logger)
  DefaultMetrics() Metrics
  NewMemoryCache(opts ...CacheOption) Cache
  NewMemoryStore() Store
  NewChannelPubSub() PubSub
  NewCircuitBreaker(threshold int64, timeout time.Duration) *CircuitBreaker

配置透传（Functional Options）：
  网关：WithShutdownTimeout / WithMetricsAddr / WithMetricsEnabled / WithGlobalPlugins
  TCP： WithTCPAddr / WithTCPTLS / WithTCPWorkerPool / WithTCPFramer / WithTCPPlugins
        WithTCPMaxSessions / WithTCPMaxMessageSize / WithTCPTimeouts / WithTCPDrainTimeout
  UDP： WithUDPAddr / WithUDPSessionTTL / WithUDPMaxSessions
  HTTP：WithHTTPAddr / WithHTTPTimeouts / WithHTTPTLS / WithHTTPPluginMode
  WS：  WithWSAddr / WithWSPath / WithWSPingInterval / WithWSPongTimeout
        WithWSMaxMessageSize / WithWSOrigins / WithWSTLS
  CoAP：WithCoAPAddr / WithCoAPAckTimeout / WithCoAPMaxRetransmit / WithCoAPDTLS
```

---

## 12. 数据流

### 12.1 TCP 完整处理流

```
网络 → readLoop：
  bufpool.Get(Small=4KB)                       // 六级 pool 选择
  → Framer.ReadFrame(conn)                     // 帧解析，处理粘包
  → sess.TouchActive()                         // 更新 lastActive（atomic）
  → WorkerPool.Submit(sess, payload)           // 提交到任务队列

Worker goroutine（隔离 panic）：
  PluginChain.OnMessage(sess, payload)
    → BlacklistPlugin（Priority=0）            // 黑名单二次检查（消息级）
    → RateLimitPlugin（Priority=10）           // 消息速率令牌桶
    → HeartbeatPlugin（Priority=30）           // 重置心跳计时器
    → PersistencePlugin（Priority=50）         // 异步序列化写入 Store
    → ClusterPlugin（Priority=40）             // 跨节点事件广播
    → ErrSkip / ErrDrop → 停止，不调用 Handler
  Handler(sess, RawMessage) → 业务逻辑
    → sess.Send(response)                      // 非阻塞写入 writeQueue
    // 或 sess.SendTyped(typedMsg)             // 泛型路径：encode → Send

writeLoop（单 goroutine 独占写）：
  for data := range writeQueue：
    conn.Write(data)
    bufpool.Put(buf)
```

### 12.2 连接生命周期

```
listener.Accept()
  → newTCPSession(conn)
  → manager.Register(sess)
    → 超容? → LRU Evict 最旧会话 → 被淘汰 sess.Close()
  → PluginChain.OnAccept(sess)
    → ErrBlock? → sess.Close() + return（拒绝连接）
  → go sess.readLoop()
  → go sess.writeLoop()
  → （WS 额外）go sess.pingLoop()

[运行中]
  readLoop → Framer → chain.OnMessage → pool.Submit → handler → sess.Send
  writeLoop → writeQueue → conn.Write

[断开 / 超时 / 错误]
  defer（readLoop 退出时）：
    sess.Close()：drain writeQueue → CAS Closing→Closed → conn.Close
    PluginChain.OnClose(sess)（逆序）
    manager.Unregister(sess.ID())
```

### 12.3 跨节点路由（集群模式）

```
节点1 业务 Handler：
  target, ok = manager.Get(targetID)
  → ok: 本地直接 sess.Send(data)
  → !ok: Cache.Get("session:route:"+targetID) → nodeID="node2"
         PubSub.Publish("node2.route", {targetID, data})

节点2 ClusterPlugin（后台订阅）：
  PubSub.Subscribe("node2.route", func(msg))
  → localManager.Get(targetID) → found → sess.Send(payload)
  → !found: 记录 Warn（session 已迁移或下线）
```

---

## 13. 并发模型

```
┌─────────────────────────────────────────────────────────────┐
│ Gateway Main Goroutine                                       │
│ Run: Start() → signal.NotifyContext → Stop()                │
├──────────────┬──────────────────┬──────────────────────────┤
│ TCP Acceptor │ WS / HTTP Server │ UDP/CoAP readLoop         │
│ Accept → go  │ Serve + Upgrade  │ ReadFromUDP（单 goroutine）│
├──────────────┴──────────────────┴──────────────────────────┤
│ WorkerPool（NumCPU×2 核心 Worker，有界 taskQueue）           │
│ Worker 1..N：for task := range taskQueue → safeRun          │
│ SpawnTemp：临时扩容，最多 MaxWorkers 个，处理完自动退出       │
├────────────────────────────────────────────────────────────┤
│ Per-Session Goroutines：                                     │
│   TCP：readLoop + writeLoop（每连接 2 个）                  │
│   WS： readLoop + writeLoop + pingLoop（每连接 3 个）       │
│   UDP：无 per-session goroutine（复用 readLoop）            │
├────────────────────────────────────────────────────────────┤
│ System Goroutines（固定数量，与连接数无关）：                 │
│   LRU Cleaner × 32（每 shard 独立，TTL 扫描）               │
│   Heartbeat TimeWheel（1 个管理所有会话心跳）                │
│   RateLimit cleanupLoop（清理空闲 IP 桶）                   │
│   OverloadProtector（定期检查资源水位）                      │
│   Metrics HTTP Server（/metrics + /healthz + /readyz）      │
│   PubSub fan-out goroutine（集群模式）                      │
│   PersistencePlugin batch writer（异步刷盘）                │
└────────────────────────────────────────────────────────────┘

规模估算：
  10K TCP 连接，16 Workers  → ~20K+40 goroutines
  100K TCP 连接，32 Workers → ~200K+45 goroutines
```

---

## 14. 性能优化策略

### 14.1 内存

| 策略 | 位置 | 实现 |
|------|------|------|
| **六级 BufferPool** | bufferpool | 128B/512B/4KB/32KB/256KB 分级复用，Micro 级专为 CoAP/ACK |
| 读缓冲复用 | readLoop | bufpool.Get → defer bufpool.Put |
| 数据拷贝隔离 | readLoop/Send | copy 防池化 buffer 被并发污染 |
| 超大不入池 | bufferpool.Put | >256KB 直接丢弃，防内存膨胀 |
| LRUList 节点复用 | lru | sync.Pool 复用 lruNode，减少 GC 压力 |
| 预分配 map | Message | Metadata 预分配容量 4 |
| TotalMemoryCap | bufferpool | 池总内存上限，超限触发 GC |

### 14.2 CPU

| 策略 | 位置 | 实现 |
|------|------|------|
| 分片锁 | SessionManager | 32 shard，位运算选 shard |
| 无锁 ID/状态/计数 | BaseSession/Manager | atomic 操作，消除 mutex |
| 单 goroutine 写 | TCPSession | writeQueue channel 消除写锁竞争 |
| 静态排序插件链 | PluginChain | 启动时 slices.SortFunc，热路径直接索引 |
| 时间轮 | HeartbeatPlugin | 10 万连接仅 1 goroutine |
| unique 字符串池 | 协议标签 | Go 1.24 unique 包，减少 string 分配 |
| SendTyped 内联 | M=[]byte 时 | 编译器内联消除 encoder 调用开销 |

### 14.3 I/O

| 策略 | 位置 | 实现 |
|------|------|------|
| 非阻塞写队列 | TCPSession.Send | select default 分支，不阻塞 readLoop |
| writeQueue drain | TCPSession.Close | 优雅关闭等待队列清空（DrainTimeout） |
| bufio.Reader | Framer | 批量读取，减少 syscall 次数 |
| Accept 退避 | acceptLoop | 错误后指数退避（5ms → 1s → 10s） |
| WS ReadDeadline | WSSession | PingInterval + PongTimeout 动态设置 |
| MiniBatch（可选）| WorkerPool | 累积 N 条或 1ms 后批量提交，减少调度开销 |

---

## 15. 异常防御体系

### 15.1 威胁模型

```
超载：连接洪峰 / 消息洪峰 / 广播风暴 / WorkerPool 饱和 / 写队列堆积
资源：FD 耗尽 / 内存 OOM / Goroutine 泄漏 / CPU 热点
攻击：SYN Flood / Slowloris / 大包炸弹 / 小包高频 / 畸形帧 / 放大攻击
```

### 15.2 连接层防御（6 层）

```
L1 OS 内核参数（部署时配置）：
  net.core.somaxconn = 65535
  net.ipv4.tcp_syncookies = 1
  net.ipv4.tcp_max_syn_backlog = 65535
  fs.file-max = 1048576
  ulimit -n = 1048576

L2 MaxSessions 硬上限：
  count >= maxSess → 拒绝连接 + Warn 日志 + shark_rejected_connections_total++

L3 令牌桶限流（RateLimitPlugin）：
  双层：全局 rate + per-IP rate
  超限连接：ErrBlock（拒绝）
  超限消息：ErrDrop（丢弃）

L4 IP 黑名单（BlacklistPlugin）：
  精确 IP O(1) + CIDR 段遍历
  动态 Add/Remove + Cache 双查 + TTL 过期自动解除

L5 自动封禁（AutoBanPlugin）：
  触发：限流超 N 次 / 协议错误超 M 次 / 空连接超 K 次
  动作：自动加入黑名单，banTTL 默认 30 分钟

L6 Slowloris 防御：
  ReadTimeout(30s) + IdleTimeout(10s)
  HeartbeatPlugin 超时断开
  MinDataRate 可选（最少每秒字节数阈值）
```

### 15.3 消息层防御

```
大包攻击：
  TCP：六级 pool 匹配读缓冲 + MaxMessageSize(1MB) + Framer MaxFrameSize
  WS：conn.SetReadLimit(MaxMessageSize)
  UDP：物理限制 65535B，实际 MTU 约 1500B
  响应包大小不超过请求包 N 倍（防反射放大攻击）

小包高频：
  消息速率令牌桶（RateLimitPlugin OnMessage）
  连续触发限流 N 次 → AutoBan 断开连接

畸形帧：
  Decoder 内 recover(panic) + 连续错误计数 → MaxConsecutiveErrors 断连
  CoAP 帧校验（len/TKL/Version/Token）
  WS gorilla/websocket 内置帧校验

WorkerPool 背压（4 种策略，推荐默认 Drop）：
  PolicyDrop：丢弃消息 + metrics（默认）
  PolicyClose：持续过载 30s → 主动关闭连接
  PolicyBlock：阻塞等待（不可丢消息场景）
  PolicySpawnTemp：临时扩容（突发流量缓冲）
```

### 15.4 资源限制防御

```
FD 耗尽：
  MaxSessions = ulimit × 80%（保留 20% 余量）
  FD > 95%：暂停 Accept + 清理最老 10% 空闲连接 + FD < 85% 恢复

内存 OOM：
  BufferPool TotalMemoryCap 可配置（超限强制 GC）
  MaxMessageSize 硬限制
  HeapInuse > 80%：降级模式（加速 LRU 淘汰 + 拒绝新连接）

Goroutine 泄漏：
  每个 goroutine 至少 3 个退出路径（ctx.Done / LRU 淘汰 / 主动关闭）
  HandlerTimeout 保护 Handler 阻塞（可选）
  WorkerPool safeRun recover(panic)

写端背压：
  WriteQueueSize 硬上限（有界 channel）
  Send 非阻塞（立即返回 ErrWriteQueueFull）
  WriteTimeout(10s) 保护写操作不永久阻塞
  广播限速：BroadcastMaxSize(64KB) + BroadcastMinInterval(100ms)
```

### 15.5 优雅降级矩阵

| 触发条件 | 降级动作 |
|----------|----------|
| Sessions > HighWater | 拒绝新连接 + 加速 LRU 淘汰 |
| WorkerPool 使用率 > 90% | 消息丢弃（PolicyDrop） |
| WorkerPool 满载持续 30s | 主动关闭最慢连接（PolicyClose） |
| FD 使用率 > 95% | 暂停 Accept + 清理最老 10% 连接 |
| Memory > 80% | 跳过 PersistencePlugin + 加速 LRU + 拒绝新连接 |
| PubSub 不可用 | 集群事件静默丢弃（本地功能不受影响） |
| Store 不可用 | PersistencePlugin 跳过 + CircuitBreaker 熔断保护 |

---

## 16. 安全加固

```
TLS（Go 1.24 crypto/tls）：
  MinVersion = tls.VersionTLS13（Go 1.24 默认，弃用 TLS 1.2 弱密码套件）
  强密码套件 + ECDHE 密钥交换（前向保密）
  证书热加载：监听 SIGHUP 信号 → GetConfigForClient 回调 → 不中断已有连接
  支持 ACME / Let's Encrypt 自动证书（可选）

WebSocket：
  AllowedOrigins 白名单（生产环境必须配置，空列表=拒绝所有跨域）
  Upgrade 鉴权（Cookie / Authorization Token 校验，业务层实现）
  只处理 Text / Binary 消息，控制帧由框架统一处理

CoAP：
  DTLS 支持（可选，基于 pion/dtls）
  Token 验证（Request Token 与 Response Token 一致性）
  MessageID 去重（最近 500 条 LRU 缓存）

日志脱敏规范：
  Payload 最多输出前 64 字节（截断后标注 "...truncated"）
  敏感 Metadata 字段（password / token / key）不输出
  对外错误响应不暴露内部实现细节（仅返回错误码）
```

---

## 17. 可观测性

### 17.1 日志

```
级别语义：
  Debug → 帧解析细节、插件执行过程（生产关闭）
  Info  → 连接建立/断开、Server 启动/停止
  Warn  → 限流触发、LRU 淘汰、重传、降级
  Error → 异常断连、panic recover、基础设施错误

关键字段（结构化 JSON）：
  session_id, protocol, remote_addr, local_addr
  plugin_name, error, duration_ms, trace_id, request_id
  msg_size, queue_depth, state

高频日志采样（sampler.go）：
  相同 (level + key) 每秒：第 1 次完整输出，后续汇总为 "已省略 N 次"

异步写入：
  带缓冲 channel（容量 4096）→ 单 goroutine 刷盘
  channel 满时降级为同步写（防日志丢失）
```

### 17.2 Tracing

```
context.Context 贯穿全链路：
  Accept → readLoop → PluginChain.OnMessage → Handler → sess.Send → writeLoop

Span 预留注入点（兼容 OpenTelemetry）：
  tcp.accept    → plugin.OnAccept → blacklist.check
  tcp.read_frame → plugin.OnMessage (per plugin)
  handler.execute → session.send

当前实现：ctx 携带 request_id（UUID v7）/ trace_id
未来扩展：用户可注入 OTel TracerProvider，框架不引入强依赖
```

### 17.3 健康检查

```
GET /healthz（状态感知）：
  正常：  {"status":"healthy","uptime":"2h30m","protocols":{...},"system":{...}}
  过载：  {"status":"overloaded","reason":"fd_usage_96%",...}
  降级：  {"status":"degraded","reason":"store_circuit_open",...}

GET /readyz：
  就绪：  {"status":"ready"}
  未就绪：{"status":"not_ready","reason":"..."}（启动中 / 关闭中）

GET /metrics：
  Prometheus 文本格式，包含全部 shark_* 指标
```

---

## 18. 测试策略

### 18.1 单元测试（tests/unit/）

```
types：
  枚举 String() 全覆盖
  MessageConstraint 泛型约束编译期验证
  Session[M] 状态机 CAS 并发安全
  SendTyped → Send 转换路径正确性

infra：
  BufferPool：Get/Put 全六级、Micro 级 CoAP 场景、超大不入池、TotalMemoryCap
  Logger：WithContext trace_id 提取、采样器频率
  CircuitBreaker：状态机转换、并发安全

session：
  BaseSession：状态机 CAS 并发、TouchActive 原子性、Close 幂等
  SessionManager：Register/Unregister/Get/Count CRUD、LRU 淘汰触发
  LRUList：Touch/Evict/Remove 正确性、nodePool 复用

plugin：
  PluginChain：执行顺序 Priority 验证、ErrSkip/ErrDrop/ErrBlock 语义
  BlacklistPlugin：精确 IP / CIDR 匹配、TTL 过期
  RateLimitPlugin：令牌桶补充算法、双层限流
  HeartbeatPlugin：时间轮 Add/Reset/Remove

protocol：
  Framer 四种实现：正常帧 / 超大帧 / 畸形帧 / 空帧
  错误分类函数：IsRetryable / IsFatal / IsSecurityRejection
```

### 18.2 集成测试（tests/integration/）

```
TCP：
  Echo 单连接 / 多连接并发 / 压力（1K 客户端）
  会话计数准确性 / MaxSessions 边界 / 连续错误断连
  TLS 握手 / 证书热加载
  泛型 SendTyped 端到端验证

UDP：Echo Session 复用 / sweepLoop TTL 清理
HTTP：模式A Ping / 模式B Plugin 集成
WebSocket：Text / Binary Echo / Ping-Pong 超时断开 / AllowedOrigins 拒绝
CoAP：GET / POST 请求响应 / ACK 重传（模拟丢包）/ MessageID 去重

Gateway：
  多协议并发启动 / 共享 SessionManager 跨协议查询
  6 阶段优雅关闭（SIGTERM）
```

### 18.3 基准测试（tests/benchmark/）

```
BenchmarkSessionRegister     并行 CRUD（验证分片锁效果）
BenchmarkBufferPool          六级 Get+Put vs 直接 make（验证 0 alloc）
BenchmarkBufferPoolMicro     Micro 级 128B CoAP 场景
BenchmarkPluginChain         空链 / 5 插件链（验证 200ns/hop 目标）
BenchmarkTCPEcho             Echo 吞吐（验证 100K msg/s 目标）
BenchmarkLRUTouch            LRU Touch 并发（验证 O(1)）
BenchmarkWorkerPool          Submit 吞吐（有界队列）
BenchmarkBroadcast           10K Session 广播延迟
BenchmarkSendTyped           SendTyped vs Send 开销对比（M=[]byte 验证零开销）

基准目标：
  TCP Echo >= 100K msg/s（单核）
  并发连接 >= 100K
  GC 暂停 P99 < 1ms
  BufferPool Get+Put < 10ns/op（0 alloc）
  PluginChain < 200ns/hop
  SendTyped（M=[]byte）与 Send 开销差异 < 1ns
```

### 18.4 异常测试矩阵（24 项）

| # | 场景 | 验证点 |
|---|------|--------|
| 1 | 连接超 MaxSessions | 新连接被拒绝，计数正确 |
| 2 | IP 黑名单 | OnAccept ErrBlock，连接关闭 |
| 3 | 慢连接（Slowloris） | ReadTimeout 超时断开 |
| 4 | 超大消息 | MaxMessageSize 拒绝，不影响其他连接 |
| 5 | 畸形帧 | Framer panic recover，连续错误计数 |
| 6 | WorkerPool 满载 | PolicyDrop 丢弃消息，metrics 正确 |
| 7 | 写队列满 | ErrWriteQueueFull，不阻塞 readLoop |
| 8 | Handler panic | safeRun recover，Worker 继续运行 |
| 9 | Handler 阻塞 | HandlerTimeout 超时，Worker 释放 |
| 10 | Store 不可用 | CircuitBreaker 熔断，Persistence 跳过 |
| 11 | PubSub 不可用 | 集群事件静默丢弃，本地功能正常 |
| 12 | 内存超限 | MemoryPressure 降级模式触发 |
| 13 | FD 耗尽 | 暂停 Accept，恢复后继续 |
| 14 | SIGTERM 优雅关闭 | 6 阶段完成，连接无丢失 |
| 15 | LRU 淘汰 | 最旧会话正确关闭，计数正确 |
| 16 | 并发 Close | sync.Once 保证幂等 |
| 17 | 跨协议广播 | TCP + WS + UDP 全部收到 |
| 18 | CoAP CON 重传 | 模拟丢包，4 次重传后放弃 |
| 19 | CoAP MessageID 重复 | 静默丢弃，不重复处理 |
| 20 | WS Origin 拒绝 | Upgrade 失败，返回 403 |
| 21 | TLS 握手失败 | 记录错误，不泄漏内部信息 |
| 22 | 限流超阈值 AutoBan | IP 加入黑名单，TTL 过期自动解除 |
| 23 | Gateway 启动部分失败 | 已启动的 Server 回滚停止 |
| 24 | Context 取消 | 所有监听 ctx.Done 的路径正确退出 |

---

## 19. 部署架构

### 19.1 单机部署

```
Docker Container：
  TCP:8080 / TLS:8443 / WS:8081 / UDP:5683（CoAP）/ HTTP:8082 / Metrics:9090

资源建议（100K 连接）：
  CPU：4 核
  内存：2GB（含 BufferPool 上限）
  FD：ulimit -n 131072
```

### 19.2 集群部署

```
        ┌─────────────┐
        │   LB / DNS  │
        └──────┬──────┘
   ┌───────────┼───────────┐
┌──┴──┐    ┌──┴──┐    ┌──┴──┐
│Node1│    │Node2│    │NodeN│
│shark│    │shark│    │shark│
└──┬──┘    └──┬──┘    └──┬──┘
   └───────────┼───────────┘
          ┌────┴────┐
          │Redis/NATS│
          │PubSub+Store│
          └─────────┘
          ┌─────────┐
          │Prometheus│
          │+Grafana  │
          └─────────┘

集群配置：
  ClusterPlugin：nodeID = hostname
  Cache：RedisCache（会话路由目录）
  PubSub：RedisPubSub 或 NATSPubSub（节点间消息路由）
  Store：RedisStore 或 SQLStore（会话持久化）
```

### 19.3 外部依赖

```
生产必需（2 个）：
  github.com/gorilla/websocket      v1.5.x
  github.com/prometheus/client_golang v1.x

可选（按功能启用）：
  github.com/pion/dtls/v3           CoAP DTLS
  go.etcd.io/etcd/client/v3         集群协调（可选替代 PubSub）

Go 版本要求：>= 1.24
```

---

## 20. 设计决策记录（ADR）

| # | 决策 | 原因 | 权衡 |
|---|------|------|------|
| 1 | Session[M] 泛型 + Send([]byte) 统一底层 | 泛型层提供编译期类型检查；底层统一写路径保证混合协议管理 | SendTyped 额外一次 encode 开销（M=[]byte 时编译器内联消除） |
| 2 | Handler 函数类型为主 | 轻量，一个函数即可作为处理器 | 无 OnOpen/OnClose 生命周期回调 |
| 3 | 分片锁 SessionManager（32 shard） | 降低全局锁竞争，线性扩展 | 内存略增（32 个 map） |
| 4 | writeLoop 独占写 | 统一并发写模型，消除 writeMutex | 增加一次 channel 延迟（~100ns） |
| 5 | WorkerPool 共享（非 per-session） | 大幅减少 goroutine 数量 | 消息可能跨 session 处理顺序与到达顺序不一致 |
| 6 | Plugin Priority 静态排序 | 热路径零开销，直接切片索引 | 运行时无法动态调整顺序 |
| 7 | **六级 BufferPool** | Micro 级（128B）专门覆盖 CoAP/心跳/ACK 等超小包，更精细 | 多一个 sync.Pool 实例，内存管理略复杂 |
| 8 | Infra 全接口化 | 适配不同部署环境（内存/Redis/NATS） | 每个适配器需要额外实现工作 |
| 9 | 不引入 OTel 强依赖 | 保持核心轻量（仅 2 个外部依赖） | 用户需自行适配 OTel |
| 10 | HTTP Session 不注册 Manager | per-request 代价高，HTTP 无持久会话 | HTTP 无法使用会话级插件状态 |
| 11 | LRU + TTL 双淘汰策略 | LRU 控总量，TTL 控制空闲连接 | 需后台扫描 goroutine |
| 12 | Framer 接口抽象 | 支持多种帧协议（长度前缀/换行/固定/透传） | 多一层接口调用开销（可内联消除） |
| 13 | 时间轮替代 per-session Ticker | 10 万连接仅 1 个 goroutine，资源最优 | 小规模场景略显过度设计 |
| 14 | Go 1.24（非 1.21） | iter 包、sync.Map 增强、unique 包、TLS 默认更安全 | 需升级构建环境 |
| 15 | ErrSkip/Drop/Block 语义分离 | 精确控制插件链行为，便于调试 | 错误语义需文档明确说明 |

---

## 21. Roadmap

### P0 核心（框架可运行）

- [ ] types/ 完整类型系统（枚举 / Message[T] / Session[M] / Handler[M] / Plugin）
- [ ] errs/ 完整错误体系 + 分类判断函数
- [ ] **infra/bufferpool 六级 BufferPool + TotalMemoryCap**
- [ ] infra/logger slogLogger + Nop
- [ ] session/BaseSession 状态机 + sync.Once Close
- [ ] session/Manager 分片锁 32 shard + LRUList
- [ ] plugin/PluginChain ErrSkip/ErrDrop/ErrBlock 语义
- [ ] protocol/tcp Framer 4 种内置实现
- [ ] protocol/tcp TCPSession[M] writeQueue + drain 机制 + SendTyped
- [ ] protocol/tcp TCPServer accept + WorkerPool
- [ ] api/api.go 工厂函数统一导出

### P1 功能（生产可用）

- [ ] infra/metrics Prometheus 全量指标（含六级 pool 各自统计）
- [ ] infra/cache MemoryCache + TTL 惰性过期
- [ ] infra/store MemoryStore
- [ ] infra/pubsub ChannelPubSub fan-out
- [ ] infra/circuitbreaker 熔断器三状态
- [ ] plugin/blacklist IP/CIDR + Cache 双查 + TTL
- [ ] plugin/ratelimit 双层令牌桶 + cleanupLoop
- [ ] plugin/heartbeat 时间轮实现
- [ ] plugin/autoban 触发阈值 + 加入黑名单
- [ ] plugin/persistence 异步批量写入 + CircuitBreaker
- [ ] plugin/cluster 跨节点路由 + 节点心跳
- [ ] protocol/udp UDPSession + sweepLoop
- [ ] protocol/websocket WSSession writeMutex + pingLoop
- [ ] protocol/http 模式A + 模式B（可选 Session）
- [ ] protocol/tcp TCPClient 自动重连 + 连接池
- [ ] gateway Gateway 全局插件 + 共享 Manager + 6 段关闭
- [ ] defense/overload OverloadProtector 水位检测
- [ ] Go 1.24 适配（iter.Seq / unique / sync.Map 增强）

### P2 生产加固

- [ ] protocol/coap 完整实现（CON 重传 + Block-wise + MessageID 去重）
- [ ] defense/backpressure 写队列水位监控
- [ ] 健康检查 JSON 结构化响应（healthy/overloaded/degraded）
- [ ] TLS 证书热加载（SIGHUP + GetConfigForClient）
- [ ] 日志采样器 + 异步写入 + 动态级别调整
- [ ] Tracing OTel 预留接入点
- [ ] Dockerfile + docker-compose + Helm Chart
- [ ] 完整测试套件（单元 + 集成 + 基准 + 异常矩阵 24 项）
- [ ] README.md + API 文档 + 使用示例（11 个 examples）

---

## 22. 推进步骤（分阶段实施计划）

### 阶段 0：项目初始化（Day 1）

**目标：建立项目骨架，确保可编译**

```
步骤：
  1. go mod init github.com/youorg/shark-socket（go 1.24）
  2. 创建完整目录结构（mkdir -p）
  3. 添加外部依赖：
       go get github.com/gorilla/websocket@latest
       go get github.com/prometheus/client_golang@latest
  4. 编写 go.mod / go.sum
  5. 创建所有包的空 doc.go（确保包可编译）
  6. 配置 .gitignore / Makefile / scripts/build.sh

验收：go build ./... 无报错
```

### 阶段 1：基础类型系统（Day 2-3）

**目标：完成 Layer 4（types + errs），所有接口定义完毕**

```
步骤：
  1. internal/errs/errors.go
     → 定义全量错误变量（含 ErrEncodeFailure / ErrDecodeFailure）
     → 实现 IsRetryable / IsFatal / IsRecoverable / IsSecurityRejection / IsPluginControl

  2. internal/types/enums.go
     → ProtocolType / MessageType / SessionState
     → String() 方法
     → unique.Make 池化字符串标签（Go 1.24）

  3. internal/types/message.go
     → Message[T any] struct
     → RawMessage 类型别名
     → NewRawMessage 构造函数
     → MessageConstraint 约束

  4. internal/types/session.go
     → Session[M MessageConstraint] interface（含 Send + SendTyped）
     → RawSession = Session[[]byte] 类型别名
     → SessionManager interface（含 All() iter.Seq[RawSession]）

  5. internal/types/handler.go
     → MessageHandler[T] / RawHandler
     → Server interface / TypedServer[M] interface

  6. internal/types/plugin.go
     → Plugin interface（使用 RawSession）
     → BasePlugin 空实现
     → ErrSkip / ErrDrop / ErrBlock

验收：
  go vet ./internal/types/...
  go vet ./internal/errs/...
  编写枚举 String() 单元测试，100% 覆盖
  编译期验证 Session[[]byte] 可赋值给 RawSession
```

### 阶段 2：基础设施层（Day 4-6）

**目标：完成 Layer 4（infra），基础能力就位**

```
步骤：
  1. internal/infra/bufferpool/pool.go
     → Buffer struct（[]byte + len）
     → BufferPool struct（6 个 sync.Pool：Micro/Tiny/Small/Medium/Large + Huge 直接分配）
     → GetLevel(size int) int 分级函数（128B/512B/4KB/32KB/256KB 阈值）
     → Get(size) / Put(buf) / Stats()
     → 全局 Default 单例
     → TotalMemoryCap 原子计数
     → 各级命中/未命中 metrics 接入点

  2. internal/infra/logger/logger.go
     → Logger interface
     → slogLogger 实现（JSON handler）
     → NopLogger 实现
     → SetDefault / GetDefault

  3. internal/infra/metrics/metrics.go
     → Metrics 接口抽象
     → PrometheusMetrics 实现
     → 注册全量 shark_* 指标（静态预注册，含六级 pool 指标）
     → Timer.ObserveDuration

  4. internal/infra/cache/cache.go
     → Cache interface
     → MemoryCache（sync.RWMutex + TTL 惰性过期）
     → 后台 cleanExpired goroutine

  5. internal/infra/store/store.go
     → Store interface
     → MemoryStore（sync.RWMutex + prefix 查询）

  6. internal/infra/pubsub/pubsub.go
     → PubSub / Subscription interface
     → ChannelPubSub（fan-out goroutine 模式）

  7. internal/infra/circuitbreaker/circuitbreaker.go
     → CircuitBreaker 三状态机
     → Do(fn) 方法，自动计数 + 状态转换

  8. internal/utils/atomic.go / sync.go / net.go
     → IP 解析工具 / 分片 map 辅助

验收：
  BufferPool 基准：< 10ns/op，0 alloc（所有六级）
  Micro 级 128B 场景：分配到 pool[0] 而非 pool[1]
  MemoryCache TTL 过期单元测试
  CircuitBreaker 状态机并发测试
```

### 阶段 3：会话管理层（Day 7-9）

**目标：完成 Layer 3（session），会话生命周期正确**

```
步骤：
  1. internal/session/session.go（BaseSession）
     → 嵌入 atomic.Int32 state / atomic.Int64 lastActive
     → sync.Map meta / sync.Once closeOnce
     → context.WithCancel
     → NewBase / TouchActive / SetState(CAS) / CancelContext
     → 编译期验证（部分方法）

  2. internal/session/lru.go（LRUList）
     → 双向链表 lruNode（prev/next/id）
     → index map[uint64]*lruNode
     → nodePool sync.Pool
     → Touch(id) O(1) / Evict(n) O(n) / Remove(id) O(1)

  3. internal/session/manager.go（Manager）
     → [32]shard（每 shard = RWMutex + map[uint64]RawSession + LRUList）
     → atomic.Uint64 idGen / atomic.Int64 totalAct
     → NextID / Register / Unregister / Get / Count
     → All() iter.Seq[RawSession]（Go 1.24 iter 包）
     → Range / Broadcast / Close
     → 位运算 shardIndex（id & 31）
     → 超容 LRU Evict 逻辑
     → 编译期验证：var _ types.SessionManager = (*Manager)(nil)

验收：
  SessionManager 并发 Register/Unregister 竞争检测（go test -race）
  LRU Evict 正确性（10 个 session，cap=5，验证淘汰顺序）
  Broadcast 100 个 session 全部收到
  iter.Seq 遍历与 Range 结果一致性验证
```

### 阶段 4：插件系统（Day 10-12）

**目标：完成 Layer 3（plugin），插件链语义正确**

```
步骤：
  1. internal/plugin/chain.go（PluginChain）
     → slices.SortFunc 按 Priority 排序（Go 1.21+）
     → OnAccept / OnMessage（消息变换链）/ OnClose（逆序）
     → ErrSkip / ErrDrop / ErrBlock 精确处理
     → StopOnError 配置

  2. internal/plugin/blacklist.go
     → exactMap + cidrList 双结构
     → OnAccept IP 提取与匹配
     → Cache 双查（Miss 降级本地）
     → TTL 过期 cleanupLoop

  3. internal/plugin/ratelimit.go
     → tokenBucket（按时间差补充令牌，atomic 操作）
     → sync.Map per-IP 桶
       （使用 Go 1.24 sync.Map.CompareAndDelete 优化清理）
     → 全局桶 + per-IP 桶双层
     → cleanupLoop（idleTTL 2 分钟）

  4. internal/plugin/heartbeat.go
     → TimeWheel（slot 环形数组 + 单 goroutine tick）
     → Add / Reset / Remove O(1)
     → 超时回调 sess.Close()

  5. internal/plugin/persistence.go
     → writeCh channel（容量 1024）
     → 后台 batchWriter（每 100 条或 500ms flush）
     → CircuitBreaker 包裹 Store 调用

  6. internal/plugin/cluster.go
     → OnAccept：Cache.Set 路由 + PubSub.Publish joined
     → OnClose：Cache.Del + PubSub.Publish left
     → 节点心跳 goroutine
     → 路由订阅 goroutine（转发跨节点消息）

  7. internal/plugin/autoban.go
     → per-IP 计数器（限流/错误/空连接）
     → 触发阈值 → BlacklistPlugin.Add(ip, banTTL)

  8. internal/plugin/options.go
     → 各插件 Functional Options

验收：
  PluginChain 基准：< 200ns/hop（5 插件链）
  ErrSkip 后续插件不执行（单元测试）
  ErrDrop Handler 不被调用（单元测试）
  ErrBlock 连接被关闭（集成测试）
  时间轮 10 万会话心跳超时精度测试
```

### 阶段 5：TCP 协议（Day 13-17）

**目标：TCP 完整可用，通过 Echo 基准**

```
步骤：
  1. internal/protocol/tcp/framer.go
     → Framer interface
     → LengthPrefixFramer（bufio.Reader + 4 字节大端）
     → LineFramer（bufio.Scanner）
     → FixedSizeFramer
     → RawFramer

  2. internal/protocol/tcp/session.go（TCPSession[M]）
     → 嵌入 BaseSession
     → writeQueue chan []byte（有界）
     → encoder func(M)([]byte,error)（nil 时 M=[]byte 零开销）
     → readLoop / writeLoop goroutine
     → Send([]byte) 非阻塞实现
     → SendTyped(msg M)：encoder → Send，使用 Micro/Tiny pool
     → Close 6 步状态机
     → 编译期验证：
         var _ types.RawSession = (*TCPSession[[]byte])(nil)

  3. internal/protocol/tcp/worker_pool.go
     → 核心 Worker goroutine + safeRun（panic recover）
     → taskQueue chan task（有界）
     → PolicyBlock / PolicyDrop / PolicySpawnTemp 实现
     → SpawnTemp：atomic 计数，处理完减 1，退出 goroutine
     → 指标接入

  4. internal/protocol/tcp/server.go（TCPServer）
     → Start：Listen → pool.Start → go acceptLoop
     → acceptLoop：Accept → go handleConn（指数退避）
     → handleConn：newTCPSession → Register → OnAccept → readLoop + writeLoop
     → Stop：listener.Close → pool.Stop → manager.Close → wg.Wait
     → 编译期验证：var _ types.Server = (*Server)(nil)

  5. internal/protocol/tcp/client.go（TCPClient[T]）
     → Connect / Send(T) / Receive()(T,error) / Close
     → RawClient = Client[[]byte]
     → 自动重连（指数退避 + Jitter）
     → 连接池

  6. internal/protocol/tcp/options.go
     → 完整 Functional Options（>20 个配置项）

验收：
  TCP Echo 单元测试（loopback）
  TCP Echo 基准：>= 100K msg/s（单核）
  SendTyped（M=[]byte）与 Send 性能无差异
  10K 并发连接稳定性测试（30s）
  MaxSessions 边界测试
  Framer 四种实现单元测试（含畸形帧 recover）
  go test -race 无竞争
```

### 阶段 6：其他协议（Day 18-24）

**目标：UDP / WS / HTTP / CoAP 全部可用**

```
步骤：
  1. internal/protocol/udp/（3 天）
     → UDPSession：BaseSession + net.UDPConn + addr
     → Send([]byte) 直接写（无队列）
     → UDPServer：单 conn + sync.Map + readLoop + sweepLoop

  2. internal/protocol/websocket/（3 天）
     → WSSession：BaseSession + gorilla Conn + writeMutex
     → Send([]byte) / SendText([]byte) / sendPing()
     → WSServer：Upgrader + Register + handleSession + pingLoop
     → PongHandler 设置 ReadDeadline

  3. internal/protocol/http/（1 天）
     → 模式 A：net/http 薄包装
     → 模式 B：HTTPSession per-request + Plugin 集成
     → Send([]byte) = w.Write(data)

  4. internal/protocol/coap/（3 天）
     → CoAPMessage 解析（帧校验严格）
     → CoAPSession：嵌入 UDPSession + pendingACKs + msgCache
     → CON 重传状态机（指数退避，最多 4 次）
     → MessageID 去重（LRU 500 条）
     → Block-wise 分块重组
     → Micro BufferPool 用于 ACK 响应帧（<= 128B）

验收：
  各协议 Echo 集成测试
  CoAP CON 重传（模拟 50% 丢包）
  WS Ping-Pong 超时断开
  CoAP ACK 帧使用 Micro 级 pool 验证
  go test -race 全协议
```

### 阶段 7：网关层 + API 层（Day 25-27）

**目标：多协议统一编排，Gateway 可生产运行**

```
步骤：
  1. internal/gateway/gateway.go
     → 并发 Start 各 Server + 错误聚合
     → 全局插件注入（合并 + 排序）
     → 共享 SessionManager 注入
     → 6 阶段 Stop 优雅关闭
     → Run（signal.NotifyContext）
     → serveMetrics（/metrics + /healthz + /readyz）

  2. internal/gateway/options.go
     → GatewayOption Functional Options

  3. api/api.go
     → 全量类型别名（含泛型别名）
     → 全量工厂函数（含 NewTypedTCPServer[M]）
     → 全量配置透传 Option 函数
     → 编译期验证（确保别名正确）

验收：
  multi_protocol 示例运行（TCP+WS+UDP+HTTP 同时监听）
  SIGTERM 优雅关闭测试（验证 6 阶段）
  /healthz 响应格式验证
  /metrics 指标完整性验证（含六级 pool 各自指标）
```

### 阶段 8：防御体系（Day 28-30）

**目标：异常防御全量覆盖**

```
步骤：
  1. internal/defense/overload.go（OverloadProtector）
     → 水位检测 goroutine（每 5s）
     → HighWater / LowWater 双阈值
     → 进入/退出过载状态原子切换
     → shark_overloaded Gauge

  2. internal/defense/backpressure.go
     → writeQueue 水位监控（> 80% warn，100% close）
     → 广播限速（BroadcastMaxSize + BroadcastMinInterval）

  3. internal/defense/sampler.go
     → logSampler（相同 key 每秒汇总）

  4. 完善 AutoBan 三维触发（限流/协议错误/空连接）

  5. FD 使用率监控接入（/proc/self/fd 计数）

验收：
  异常测试矩阵 24 项全量执行
  OverloadProtector 压测触发验证
  内存压力测试（go tool pprof 验证无泄漏）
```

### 阶段 9：测试完善与性能验证（Day 31-35）

**目标：全量测试，达到性能目标**

```
步骤：
  1. tests/unit/ 补齐所有单元测试（覆盖率 > 80%）
  2. tests/integration/ 全量集成测试
  3. tests/benchmark/ 全量基准测试
     → 对比 P0 目标：100K msg/s / 100K 并发 / P99 < 1ms
     → SendTyped vs Send 性能对比（验证 M=[]byte 零开销）
     → 六级 BufferPool 各级分配统计
     → pprof CPU + Memory 分析
     → go test -bench=. -benchmem -count=5
  4. go test -race ./... 无竞争
  5. golangci-lint 静态分析
  6. 性能调优迭代（根据 pprof 结果）

验收：
  所有基准目标达成
  race detector 零报告
  lint 零 error
```

### 阶段 10：生产化（Day 36-40）

**目标：可部署到生产环境**

```
步骤：
  1. 编写 11 个 examples（基础 + 进阶 + 运维场景）
     → basic_tcp / basic_udp / basic_http / basic_websocket / basic_coap
     → multi_protocol / tls_server / tcp_client
     → session_plugins / custom_protocol / graceful_shutdown
     （含泛型 SendTyped 使用示例）

  2. Dockerfile（多阶段构建，最终镜像 < 20MB）
  3. docker-compose.yml（shark-socket + redis + prometheus + grafana）
  4. Helm Chart（K8s 部署，HPA 配置）
  5. README.md（快速开始 + 架构说明 + 配置参考 + 泛型 Session 使用指南）
  6. 完整 API 文档（godoc 标准注释）
  7. TLS 证书热加载测试（SIGHUP）
  8. scripts/build.sh + docker-build.sh
  9. CI/CD 配置（GitHub Actions：lint + test + benchmark + build）

验收：
  docker-compose up 一键启动所有服务
  Grafana Dashboard 导入（shark-socket 全量指标可视化，含六级 pool 图表）
  单机 100K 连接压测通过
  集群 3 节点跨节点路由验证
```

---

## 附录：关键变更汇总（相对原文档）

| 变更点 | 原设计 | 修订后 | 变更原因 |
|--------|--------|--------|----------|
| Go 版本 | >= 1.26 | >= 1.24 | 约束要求 |
| Session | 非泛型，统一 Send([]byte) | **泛型 Session[M]，底层 Send([]byte)** | 约束：泛型 + 易于转换 Send([]byte) |
| BufferPool | 5 级（Tiny/Small/Medium/Large/Huge） | **6 级（Micro/Tiny/Small/Medium/Large/Huge）** | 约束：分 6 级；Micro 专为 128B 超小包 |
| RawSession | 无 | = Session[[]byte]，最常用别名 | 简化 90% 场景使用 |
| SendTyped | 无 | Session[M].SendTyped(msg M) error | 泛型类型安全路径 |
| SessionManager | 存储 Session 接口 | 存储 RawSession（= Session[[]byte]） | 统一运行时多态，跨协议管理 |
| Plugin 接口参数 | Session | RawSession | 插件层无需感知业务消息类型 |
| Metrics | 无 pool 级别统计 | 含 shark_bufferpool_hits/misses{level} | 六级 pool 可观测 |
| ErrEncodeFailure | 无 | 新增 | SendTyped 编码失败路径 |

---

*文档版本：2025 v3.0（融合修订版，Go >= 1.24，BufferPool 6 级，Session 泛型 + Send([]byte) 统一底层）*