# Shark-Socket

[![CI](https://github.com/X1aSheng/shark-socket/actions/workflows/ci.yml/badge.svg)](https://github.com/X1aSheng/shark-socket/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/Go-1.26%2B-blue)](https://go.dev)
[![License](https://img.shields.io/badge/License-MIT-green)](LICENSE)
[![Tests](https://img.shields.io/badge/Tests-627%20passed-brightgreen)](./tests)
[![Fuzz](https://img.shields.io/badge/Fuzz-7%20tests-brightgreen)](./tests)
[![Coverage](https://img.shields.io/badge/Coverage-37%20pkgs-brightgreen)](./tests)
[![Docker](https://img.shields.io/badge/Docker-ready-blue)](./deploy/docker/Dockerfile)
[![Kubernetes](https://img.shields.io/badge/Kubernetes-ready-blue)](./deploy/k8s)
[![Helm](https://img.shields.io/badge/Helm-ready-0F1689)](./deploy/k8s/helm/shark-socket)

[English](./README_EN.md) | 中文文档

高性能、可扩展的**多协议网络框架**，采用 Go 语言开发，支持 **TCP**、**TLS**、**UDP**、**HTTP**、**HTTP/2**、**WebSocket**、**CoAP**、**QUIC** 和 **gRPC-Web** 协议的统一抽象与网关集成。

---

## ✨ 特性

| 特性 | 说明 |
|------|------|
| **多协议支持** | TCP、TLS、UDP、HTTP、HTTP/2、WebSocket、CoAP、QUIC、gRPC-Web 统一抽象 |
| **类型安全** | 泛型 `Session[M]`，编译期保证消息类型安全，`SendTyped` + `Send([]byte)` |
| **高性能** | 32 分片 SessionManager、Worker Pool、6 级 BufferPool、零 GC 分配 |
| **插件系统** | OnAccept / OnMessage / OnClose 全生命周期插件钩子，优先级排序，panic 保护 |
| **可观测性** | Prometheus 指标采集、结构化日志 (slog)、OpenTelemetry 追踪、访问日志 |
| **中间件对接** | Cache / Store / PubSub 接口层，支持 Redis、SQL、NATS 等适配 |
| **分布式扩展** | ClusterPlugin 跨节点会话感知，PubSub 集群事件广播 |
| **安全防护** | Blacklist、RateLimit、AutoBan、CircuitBreaker、OverloadProtector、连接限流 |
| **优雅关闭** | 6 阶段优雅关闭，SIGTERM 信号处理，连接排空 |
| **云原生** | Docker、docker-compose、Kubernetes 完整部署（HPA/PDB/NetworkPolicy/Ingress） |

---

## 🚀 快速开始

### 安装

```bash
go get github.com/X1aSheng/shark-socket
```

### 单协议服务器

```go
package main

import (
    "log"
    "github.com/X1aSheng/shark-socket/api"
    "github.com/X1aSheng/shark-socket/internal/protocol/tcp"
)

func main() {
    handler := func(sess api.RawSession, msg api.RawMessage) error {
        return sess.Send(msg.Payload) // echo
    }

    srv := api.NewTCPServer(handler, api.WithTCPAddr("0.0.0.0", 18000))
    log.Fatal(srv.Start())
}
```

### 多协议网关

```go
package main

import (
    "time"
    "github.com/X1aSheng/shark-socket/api"
    "github.com/X1aSheng/shark-socket/internal/protocol/tcp"
    "github.com/X1aSheng/shark-socket/internal/protocol/websocket"
)

func main() {
    handler := func(sess api.RawSession, msg api.RawMessage) error {
        return sess.Send(msg.Payload)
    }

    tcpSrv := api.NewTCPServer(handler, api.WithTCPAddr("0.0.0.0", 18000))
    wsSrv := api.NewWebSocketServer(handler,
        websocket.WithAddr("0.0.0.0", 18600),
        websocket.WithPath("/ws"),
    )

    gw := api.NewGateway(
        api.WithShutdownTimeout(15*time.Second),
        api.WithMetricsAddr(":9091"),
        api.WithMetricsEnabled(true),
    )
    gw.Register(tcpSrv)
    gw.Register(wsSrv)

    gw.Run() // 阻塞直到 SIGINT/SIGTERM，然后优雅关闭
}
```

### 带插件的 TCP 服务器

```go
tcpSrv := api.NewTCPServer(handler,
    api.WithTCPAddr("0.0.0.0", 18000),
    api.WithTCPPlugins(
        api.NewBlacklistPlugin("10.0.0.0/8"),
        api.NewRateLimitPlugin(100, time.Second),
    ),
)
```

### 带访问日志的 HTTP 服务器

```go
srv := api.NewHTTPServer(
    api.WithHTTPAddr("0.0.0.0", 18400),
    api.WithHTTPAccessLogger(myAccessLogger),
    api.WithHTTP2(),
)
```

---

## 🏗️ 架构概览

```
┌──────────────────────────────────────────────────────────────┐
│                       API 层                                  │
│              (统一入口，类型别名，工厂函数)                      │
├──────────────────────────────────────────────────────────────┤
│                    Gateway (网关)                              │
│         ┌──────────────────────────────────────┐             │
│         │   多协议服务器编排，共享 SessionManager  │             │
│         │   TCP/TLS/UDP/HTTP/WS/CoAP/QUIC/gRPC  │             │
│         └──────────────────────────────────────┘             │
├──────────────────────────────────────────────────────────────┤
│              Session Manager (会话管理)                        │
│         ┌────────────────────────────────────┐               │
│         │ • 32 分片锁  • LRU 淘汰  • 原子 ID    │               │
│         │ • CAS 状态机 • Broadcast            │               │
│         └────────────────────────────────────┘               │
├──────────────────────────────────────────────────────────────┤
│   Plugin System (OnAccept/OnMessage/OnClose 钩子)             │
│   ┌───────┬───────┬───────┬───────┬───────┬───────┐          │
│   │Black  │Rate   │Auto   │Heart  │Cluster│Persist│          │
│   │list   │Limit  │Ban    │beat   │       │ence   │          │
│   │ P:0   │ P:10  │ P:20  │ P:30  │ P:40  │ P:50  │          │
│   └───────┴───────┴───────┴───────┴───────┴───────┘          │
├──────────────────────────────────────────────────────────────┤
│    Infrastructure (基础设施)                                   │
│   ┌──────────┬──────────┬──────────┬──────────┐              │
│   │ Logger   │ Metrics  │  Cache   │  Store   │              │
│   ├──────────┼──────────┼──────────┼──────────┤              │
│   │BufferPool│  PubSub  │CircuitBkr│ Tracing  │              │
│   │          │          │          │(OTel)    │              │
│   └──────────┴──────────┴──────────┴──────────┘              │
├──────────────────────────────────────────────────────────────┤
│    Defense (防护)                                              │
│   ┌──────────────┬──────────────┬──────────────┐             │
│   │  Overload    │ Backpressure │  LogSampler  │             │
│   │  Protector   │ Controller   │              │             │
│   └──────────────┴──────────────┴──────────────┘             │
└──────────────────────────────────────────────────────────────┘
```

完整架构文档见 [docs/shark-socket ARCHITECTURE.md](docs/shark-socket%20ARCHITECTURE.md)

---

## 📦 支持的协议

| 协议 | 默认端口 | 类型 | 特性 |
|------|---------|------|------|
| **TCP** | 18000 | 流式 | 4 种 Framer、WorkerPool (4 策略)、writeQueue、drain、TLS、热加载 |
| **UDP** | 18200 | 数据报 | 伪会话 (按地址)、TTL 自动清理 |
| **HTTP** | 18400 | 请求响应 | 模式 A (轻量包装) + 模式 B (会话+插件)、HTTP/2 (TLS/h2c) |
| **WebSocket** | 18600 | 全双工 | Ping/Pong 保活、Origin 校验、访问日志 |
| **CoAP** | 18800 | 受限应用 | CON 重传、ACK、MessageID 去重 (RFC 7252) |
| **QUIC** | 18900 | 多流 | UDP 多路复用、流控、0-RTT 连接 |
| **gRPC-Web** | 18650 | 网关 | WebSocket/Direct 双模式、Protobuf 序列化 |

### TCP Framer 类型

| Framer | 说明 |
|--------|------|
| `LengthPrefixFramer` | 4 字节长度前缀（默认，最大 1MB） |
| `LineFramer` | 换行符分隔 |
| `FixedSizeFramer` | 固定大小帧 |
| `RawFramer` | 原始读取 |

### Worker Pool 策略

| 策略 | 队列满时行为 |
|------|------------|
| `PolicyBlock` | 阻塞等待 |
| `PolicyDrop` | 丢弃消息 |
| `PolicySpawnTemp` | 创建临时 Worker |
| `PolicyClose` | 关闭连接 |

### HTTP/2 支持

| 模式 | 说明 |
|------|------|
| **TLS (ALPN)** | 生产环境推荐，HTTP/2 协商通过 TLS ALPN |
| **h2c (Cleartext)** | 开发环境，支持 HTTP/2 明文升级 |

### CoAP 消息类型

| 类型 | 说明 |
|------|------|
| CON | 可确认 — 需 ACK，自动重传 |
| NON | 不可确认 — 即发即忘 |
| ACK | 确认 |
| RST | 重置 |

### QUIC 特性

| 特性 | 说明 |
|------|------|
| 多路复用 | 单连接多流，无队头阻塞 |
| 0-RTT | 零往返时间连接建立 |
| 连接迁移 | 连接 ID 保持，移动网络切换 |
| 拥塞控制 | 可插拔拥塞控制算法 |

---

## 🔌 插件系统

插件拦截会话生命周期事件，按优先级排序执行（数字越小越先执行）。

### 插件接口

```go
type Plugin interface {
    Name()     string
    Priority() int
    OnAccept(sess RawSession) error
    OnMessage(sess RawSession, data []byte) ([]byte, error)
    OnClose(sess RawSession)
}

// BasePlugin 提供所有方法的空实现，嵌入后只需覆写需要的方法
type BasePlugin struct{}
```

### 流程控制

通过返回特殊错误控制插件链执行：

| 错误 | 效果 | 判断方法 |
|------|------|----------|
| `ErrSkip` | 跳过剩余插件，继续正常处理 | `api.IsPluginSkip(err)` |
| `ErrDrop` | 静默丢弃消息 | `api.IsPluginDrop(err)` |
| `ErrBlock` | 关闭连接 | `api.IsPluginBlock(err)` |

### 内置插件

| 插件 | 优先级 | 用途 |
|------|--------|------|
| `BlacklistPlugin` | 0 | IP/CIDR 黑名单，O(1) 精确匹配 + CIDR 扫描，支持 TTL 和外部缓存 |
| `RateLimitPlugin` | 10 | 双层令牌桶（全局 + 单 IP），连接级和消息级限流 |
| `AutoBanPlugin` | 20 | 阈值违规自动封禁（速率、协议错误，空闲） |
| `HeartbeatPlugin` | 30 | TimeWheel 空闲超时检测，单 goroutine 管理全部会话 |
| `ClusterPlugin` | 40 | 跨节点会话路由，PubSub 广播 |
| `PersistencePlugin` | 50 | 异步批量写入，熔断保护 |
| `SlowQueryPlugin` | -10 | 慢请求日志（默认 100ms 阈值） |

所有插件钩子都带有 **panic 保护** 和 **执行延迟指标采集**，插件崩溃不会影响主流程。

---

## 🧩 会话管理

### Session 状态机

```
Connecting ──→ Active ──→ Closing ──→ Closed
     │            │
     └────────────┘ (连接失败)
```

状态转换通过 CAS 原子操作保证并发安全，只允许合法转换：

| 从 → 到 | 允许 |
|---------|------|
| Connecting → Active | ✓ |
| Connecting → Closed | ✓ |
| Active → Closing | ✓ |
| Active → Closed | ✓ |
| Closing → Closed | ✓ |
| 其他转换 | ✗ |

### 分片 SessionManager

- **32 分片**：按 session ID 分片，减少锁争用
- **LRU 淘汰**：跨分片 LRU 驱逐（TryLock 避免死锁）
- **原子 ID**：`atomic.Uint64` 全局递增
- **零分配查询**：`Get` 仅一次 RLock，7.6 ns/op

---

## 🗄️ 基础设施

### 6 级 BufferPool

基于 `sync.Pool` 的零 GC 缓冲区分配，按请求大小自动选择级别。

| 级别 | 名称 | 大小 | 使用场景 |
|------|------|------|----------|
| 0 | Micro | ≤512B | CoAP ACK、控制帧 |
| 1 | Tiny | ≤2KB | 小消息、心跳 |
| 2 | Small | ≤4KB | 常规消息 |
| 3 | Medium | ≤32KB | HTTP body、批量消息 |
| 4 | Large | ≤256KB | 大消息、文件分片 |
| 5 | Huge | >256KB | 直接分配（无池化） |

### 熔断器

三态保护：Closed → Open → HalfOpen。

```go
cb := api.NewCircuitBreaker(5, 30*time.Second)
err := cb.Do(func() error {
    return riskyOperation()
})
```

### OpenTelemetry 追踪

```go
// 创建 OTel tracer
tracer, _ := tracing.NewOTelTracer(
    tracing.WithServiceName("shark-socket"),
)

// 集成到服务器
tcpSrv := api.NewTCPServer(handler,
    api.WithTCPAddr("0.0.0.0", 18000),
    tcp.WithTracer(tracer),
)
```

### Store / Cache / PubSub

```go
// Key-Value 存储
store := api.NewMemoryStore()
store.Save(ctx, "key", []byte("value"))
result, _ := store.Load(ctx, "key")

// TTL 缓存
cache := api.NewMemoryCache()
cache.Set(ctx, "session:123", data, 5*time.Minute)

// 发布订阅
ps := api.NewChannelPubSub()
sub, _ := ps.Subscribe(ctx, "events", func(data []byte) {
    log.Println("received:", string(data))
})
ps.Publish(ctx, "events", []byte("hello"))
sub.Unsubscribe()
ps.Close()
```

### Prometheus 指标

框架自动采集以下指标（通过 `promhttp.Handler()` 暴露）：

| 指标 | 说明 |
|------|------|
| `shark_connections_total` | 连接总数（按协议、状态、方向） |
| `shark_connections_active` | 活跃连接数 |
| `shark_messages_total` | 消息总数 |
| `shark_message_bytes` | 消息字节数分布 |
| `shark_message_duration_seconds` | 消息处理延迟 |
| `shark_errors_total` | 错误总数（按协议分类） |
| `shark_session_lru_evictions_total` | LRU 淘汰次数 |
| `shark_bufferpool_hits_total` | BufferPool 命中/未命中 |
| `shark_plugin_duration_seconds` | 插件执行延迟直方图 |
| `shark_worker_panics_total` | Worker panic 计数 |

### 访问日志

```go
type AccessLogEntry struct {
    Timestamp  time.Time
    Protocol   string
    Method     string
    Path       string
    StatusCode int
    BytesIn    int64
    BytesOut   int64
    Duration   time.Duration
    ClientIP   string
    UserAgent  string
}

// HTTP 服务器集成
srv := api.NewHTTPServer(
    api.WithHTTPAccessLogger(myLogger),
)

// WebSocket 服务器集成
wsSrv := api.NewWebSocketServer(handler,
    websocket.WithAccessLogger(myLogger),
)
```

---

## 📂 项目结构

```
shark-socket/
├── api/                    # 公共 API — 类型别名、工厂函数
│   ├── api.go              # 核心入口：NewGateway、NewTCPServer 等
│   └── api_test.go
├── internal/
│   ├── gateway/            # 多协议网关（6 阶段关闭，指标端点，配置热加载）
│   ├── protocol/           # 协议实现
│   │   ├── tcp/            # TCP/TLS：Framer、WorkerPool、writeQueue、TLS热加载
│   │   ├── udp/            # UDP：伪会话、TTL 清理
│   │   ├── http/           # HTTP：双模式、HTTP/2、访问日志、追踪
│   │   ├── websocket/      # WebSocket：Ping/Pong、Origin、访问日志
│   │   ├── coap/           # CoAP：CON 重传、MessageID 去重
│   │   ├── quic/           # QUIC：多流、0-RTT、连接迁移
│   │   └── grpcgw/         # gRPC-Web：WebSocket/Direct 双模式
│   ├── session/            # 会话管理
│   │   ├── session.go      # BaseSession：CAS 状态机、元数据
│   │   ├── manager.go      # 32 分片 Manager、LRU 淘汰
│   │   └── lru.go          # 双向链表 LRU 实现
│   ├── plugin/             # 插件系统
│   │   ├── chain.go        # 插件链：优先级排序、panic 保护、慢查询日志
│   │   ├── blacklist.go    # IP/CIDR 黑名单
│   │   ├── ratelimit.go    # 双层令牌桶
│   │   ├── autoban.go      # 自动封禁
│   │   ├── heartbeat.go    # TimeWheel 心跳
│   │   ├── cluster.go       # 跨节点路由
│   │   ├── persistence.go   # 异步持久化
│   │   └── slow_query.go    # 慢查询日志
│   ├── infra/              # 基础设施
│   │   ├── bufferpool/     # 6 级 sync.Pool 缓冲池
│   │   ├── cache/          # 内存 TTL 缓存（后台清理）
│   │   ├── circuitbreaker/ # 三态熔断器
│   │   ├── logger/         # 结构化日志 (slog)、访问日志
│   │   ├── metrics/        # Prometheus 集成（全局 Collector）
│   │   ├── pubsub/         # 基于通道的发布订阅
│   │   ├── store/          # Key-Value 存储
│   │   ├── tracing/        # OpenTelemetry 兼容追踪接口
│   │   └── config/         # 配置文件热加载
│   ├── defense/            # 过载保护、背压控制、日志采样
│   ├── types/              # 核心类型：枚举、Message[T]、Session[M]、Plugin
│   ├── errs/               # 错误分类体系（含 IsRetryable/IsFatal 谓词）
│   └── utils/              # ShardedMap[K,V]、AtomicBool、IP 解析
├── examples/               # 8 个可运行示例
│   ├── basic_tcp/          # TCP echo 服务器
│   ├── basic_udp/          # UDP echo 服务器
│   ├── basic_http/         # HTTP 服务器
│   ├── basic_websocket/    # WebSocket echo
│   ├── basic_coap/         # CoAP 服务器
│   ├── multi_protocol/     # 多协议网关（TCP+HTTP+WS+CoAP+QUIC）
│   ├── graceful_shutdown/  # 优雅关闭演示
│   └── session_plugins/    # 插件链使用示例
├── tests/
│   ├── unit/               # 跨包单元测试
│   ├── integration/        # 多协议系统集成测试
│   └── benchmark/          # 性能基准测试
├── cmd/
│   └── shark-socket/       # 生产入口（环境变量配置）
├── deploy/                 # 部署清单
│   ├── README.md           #   部署文档
│   ├── docker/             #   Dockerfile + docker-compose
│   └── k8s/                #   Kubernetes 清单 + Helm Chart
│       ├── app/            #     应用清单 (Deployment/Service/Ingress/HPA)
│       ├── infra/          #     基础设施 (Prometheus)
│       └── helm/           #     Helm Chart
├── scripts/                # 跨平台测试脚本
│   ├── run_tests.go        #   Go 运行器（所有平台，推荐）
│   ├── run_tests.sh        #   Bash 运行器（Linux/macOS/Git Bash）
│   ├── run_tests.bat       #   CMD 运行器（Windows）
│   └── parse_test_log.go   #   JSON 日志解析器
├── docs/                   # 架构设计文档
└── Makefile                # 常用构建命令
```

---

## 📊 性能报告

AMD Ryzen 7 8845HS (Windows 11, Go 1.26.1) 上的基准测试结果。

### 性能亮点

| 指标 | 数值 | 说明 |
|------|------|------|
| **TCP 并行吞吐** | ~166K msg/s | 多连接并行处理 |
| **TCP 单连接吞吐** | ~31.5K msg/s | 单连接 echo |
| **UDP echo 吞吐** | ~68K msg/s | 数据报 echo |
| **CoAP 消息解析** | 77.2 ns/op | 消息反序列化 |
| **CoAP 序列化** | 28.5 ns/op | 消息序列化 |
| **Session 注册** | 162.4 ns/op | 32 分片并发安全 |
| **Session 查询** | 7.6 ns/op | 分片 Map 读取 |
| **NextID 并发** | 10.1 ns/op | 原子递增 |
| **BufferPool Small** | 98.9 ns/op | 4KB 池化分配 |
| **插件链 (5 插件)** | 167.0 ns/op | 5 个插件顺序执行 |
| **并发连接** | 100K | 测试验证 |

### TCP 协议

| 基准 | ns/op | B/op | allocs/op |
|------|-------|------|-----------|
| TCPEcho | ~31,700 | ~4,900 | 14 |
| TCPEcho_Parallel | ~6,030 | ~4,900 | 14 |
| TCPEcho_SmallMessage | ~40,400 | ~4,850 | 14 |
| TCPEcho_LargeMessage | ~58,600 | ~72,500 | 14 |

### Session Manager

| 基准 | ns/op | B/op | allocs/op |
|------|-------|------|-----------|
| SessionRegister | 162.4 | 412 | 8 |
| ManagerGet | 7.6 | 0 | 0 |
| ManagerNextID | 1.6 | 0 | 0 |
| ManagerNextIDParallel | 10.1 | 0 | 0 |
| ManagerCount | 0.2 | 0 | 0 |

### BufferPool

| 基准 | ns/op | B/op | allocs/op | 说明 |
|------|-------|------|-----------|------|
| Pool/Micro_64 | 73.2 | 24 | 2 | 池化分配 |
| Pool/Tiny_1024 | 74.4 | 24 | 2 | |
| Pool/Small_4096 | 98.9 | 24 | 2 | |
| Pool/Medium_16384 | 180.7 | 24 | 2 | |
| Pool/Large_131072 | 943.9 | 24 | 2 | |
| Parallel/Micro_64 | 23.1 | 24 | 2 | 并发池化 |
| DirectAlloc/Small | 640.7 | 4,096 | 1 | 对比：直接分配 |
| DirectAlloc/Large | 10,563 | 131,072 | 1 | 对比：直接分配 |

### 插件链

| 基准 | ns/op | B/op | allocs/op |
|------|-------|------|-----------|
| Chain_Empty | 1.9 | 0 | 0 |
| Chain_5Plugins | 167.0 | 80 | 5 |
| Chain_10Plugins | 335.1 | 160 | 10 |
| Chain_OnAccept_5 | 163.2 | 80 | 5 |
| Chain_OnClose_5 | 23.4 | 0 | 0 |
| Chain_Parallel | 45.2 | 80 | 5 |

---

## 🧪 测试

### 测试规模

- **627 个测试函数**（620 Test + 7 Fuzz），**55 个基准测试**
- **75 个测试文件**，覆盖 **37 个包**
- 全部通过，零失败
- 全面 Fuzz 测试：所有 Framer 和 CoAP 解析器覆盖

### 手动运行

```bash
# 全部测试
go test ./... -v

# 带覆盖率
go test ./... -cover

# 基准测试
go test -bench=. -benchmem -run=^$ ./...

# 竞态检测
go test -race ./...

# 指定包
go test ./internal/protocol/tcp/... -v
```

### 自动化测试脚本（推荐）

项目提供三个跨平台测试脚本，功能完全一致，按平台选用：

| 脚本 | 平台 | 运行方式 |
|------|------|----------|
| `scripts/run_tests.go` | **所有平台** | `go run scripts/run_tests.go` |
| `scripts/run_tests.sh` | Linux / macOS / Git Bash | `bash scripts/run_tests.sh` |
| `scripts/run_tests.bat` | Windows CMD | `scripts\run_tests.bat` |

#### 支持的模式

| 参数 | 说明 |
|------|------|
| `--all` | 运行全部：单元 + 集成 + 基准（默认） |
| `--unit` | 仅单元测试 |
| `--integration` | 仅集成测试 |
| `--benchmark` | 仅基准测试 |
| `--cover` | 覆盖率报告 |

#### 使用示例

```bash
# --- 所有平台（推荐） ---
go run scripts/run_tests.go                      # 全部测试
go run scripts/run_tests.go -mode unit           # 仅单元测试
go run scripts/run_tests.go -mode cover          # 覆盖率

# --- Linux / macOS / Git Bash ---
bash scripts/run_tests.sh                        # 全部测试
bash scripts/run_tests.sh --benchmark            # 仅基准测试

# --- Windows CMD ---
scripts\run_tests.bat                            # 全部测试
scripts\run_tests.bat --integration              # 仅集成测试
```

#### 日志文件

所有测试日志自动保存到 `logs/` 目录，文件名带时间戳前缀：

```
logs/
├── 20260426_190627_unit.json            # 原始 JSON 输出（go test -json）
├── 20260426_190627_unit.log             # 可读测试报告（解析后）
├── 20260426_190627_integration.json
├── 20260426_190627_integration.log
├── 20260426_190627_benchmark.json
├── 20260426_190627_benchmark.log
└── 20260426_190627_cover.log            # 覆盖率报告
```

每个测试阶段生成两个文件：
- `.json` — `go test -json` 原始输出，可用于 CI 分析或自定义解析
- `.log` — 经 `parse_test_log.go` 解析后的可读报告，包含按包分组的结果、耗时、通过/失败统计

### 测试覆盖率

| 包 | 覆盖率 |
|---|--------|
| errs | 100.0% |
| infra/cache | 100.0% |
| infra/store | 100.0% |
| defense | 95.3% |
| infra/pubsub | 94.0% |
| types | 92.3% |
| infra/config | 87.5% |
| infra/bufferpool | 86.6% |
| utils | 84.7% |
| session | 80.1% |
| infra/circuitbreaker | 79.4% |
| protocol/tcp | 79.0% |
| protocol/coap | 76.4% |
| protocol/websocket | 75.3% |
| protocol/udp | 75.0% |
| infra/ratelimit | 66.2% |
| protocol/http | 65.0% |
| api | 58.1% |
| plugin | 58.0% |
| infra/metrics | 52.5% |
| gateway | 31.5% |
| infra/logger | 29.2% |
| protocol/quic | 28.0% |
| protocol/grpcgw | 30.3% |
| infra/tracing | 12.5% |

测试方法论：使用轮询检测 (`waitForTCPServer` / `waitForUDPServer`) 替代固定睡眠，确保 CI 和本地环境的稳定性。

---

## 🛡️ 安全防护

框架提供多层防护机制：

| 层级 | 机制 | 说明 |
|------|------|------|
| 1 | MaxSessions | 硬性连接数上限，LRU 淘汰最旧会话 |
| 2 | BlacklistPlugin | IP/CIDR 黑名单，OnAccept 阶段拦截 |
| 3 | RateLimitPlugin | 双层令牌桶（全局 + 单 IP） |
| 4 | AutoBanPlugin | 速率违规自动封禁 |
| 5 | OverloadProtector | 高/低水位线，降级模式 |
| 6 | BackpressureController | 写队列水位监控，广播限速 |
| 7 | Timeouts | 读/写/空闲超时，防 Slowloris |
| 8 | ConnectionRateLimit | 连接速率限制（按 IP） |
| 9 | TLS Hot Reload | 证书自动更新，无需重启 |

---

## 📖 API 参考

### 核心类型

```go
// 会话接口（泛型）
type Session[M MessageConstraint] interface {
    ID() uint64
    Protocol() ProtocolType
    RemoteAddr() net.Addr
    State() SessionState
    IsAlive() bool
    Send(data []byte) error
    SendTyped(msg M) error
    Close() error
    Context() context.Context
    SetMeta(key string, val any)
    GetMeta(key string) (any, bool)
    DelMeta(key string)
}

type RawSession = Session[[]byte]    // 最常用类型别名
type RawMessage = Message[[]byte]    // 最常用消息类型
type RawHandler = func(sess RawSession, msg RawMessage) error
```

### 工厂函数

```go
// 协议服务器
api.NewTCPServer(handler, opts...)
api.NewUDPServer(handler, opts...)
api.NewHTTPServer(opts...)
api.NewWebSocketServer(handler, opts...)
api.NewCoAPServer(handler, opts...)
api.NewQUICServer(handler, opts...)
api.NewGRPCWebGateway(opts...)
api.NewTCPRawClient(addr, opts...)

// 网关
api.NewGateway(opts...)

// 插件
api.NewBlacklistPlugin(ips...)
api.NewRateLimitPlugin(rate, burst, opts...)
api.NewHeartbeatPlugin(interval, timeout, mgr)
api.NewPersistencePlugin(store, opts...)
api.NewAutoBanPlugin(blacklist, opts...)

// 基础设施
api.NewMemoryCache(opts...)
api.NewMemoryStore()
api.NewChannelPubSub()
api.NewCircuitBreaker(threshold, timeout)
```

### 错误分类

```go
// 控制流错误
api.ErrSkip    // 跳过剩余插件
api.ErrDrop    // 丢弃消息
api.ErrBlock   // 阻断连接

// 错误判断谓词
errs.IsRetryable(err)          // 可重试错误
errs.IsFatal(err)              // 致命错误
errs.IsSecurityRejection(err)  // 安全拦截
errs.IsPluginControl(err)      // 插件控制流
```

---

## 🐳 部署

完整部署文档见 [deploy/README.md](./deploy/README.md)。

### Docker

```bash
# 构建镜像
make docker-build

# Docker Compose（生产模式）
docker compose -f deploy/docker/docker-compose.yml up -d

# Docker Compose（开发模式）
docker compose -f deploy/docker/docker-compose.yml --profile dev up

# 带 Prometheus 监控
docker compose -f deploy/docker/docker-compose.yml --profile prod up -d

# 验证
curl http://localhost:18400/hello
curl http://localhost:9091/metrics
curl http://localhost:9091/healthz
```

**暴露端口：**

| 端口 | 协议 | 服务 |
|------|------|------|
| 18000 | TCP | TCP echo |
| 18200 | UDP | UDP echo |
| 18400 | TCP | HTTP API |
| 18600 | TCP | WebSocket |
| 18800 | UDP | CoAP |
| 18900 | UDP | QUIC（默认禁用） |
| 18650 | TCP | gRPC-Web（默认禁用） |
| 9091 | TCP | 指标 / 健康检查 |

### Kubernetes

#### kubectl + Kustomize

```bash
kubectl apply -k deploy/k8s/app/
```

#### Helm

```bash
# 默认配置
helm install shark-socket deploy/k8s/helm/shark-socket/ \
  --namespace shark-socket --create-namespace

# 生产配置
helm install shark-socket deploy/k8s/helm/shark-socket/ \
  --namespace shark-socket --create-namespace \
  --values deploy/k8s/helm/shark-socket/values-prod.yaml
```

#### 健康检查

| 探针 | 端点 | 端口 | 用途 |
|------|------|------|------|
| Liveness | `/healthz` | 9091 | 运行状态、协议列表、会话数、goroutine 数 |
| Readiness | `/readyz` | 9091 | 就绪状态（503/200） |

#### 优雅关闭

1. `preStop: sleep 10` — 等待负载均衡器摘除 Pod
2. SIGTERM 触发 Gateway 6 阶段关闭
3. `terminationGracePeriodSeconds: 60` — 60s 后强制终止

#### 验证部署

```bash
# 查看资源状态
kubectl get all -n shark-socket

# 本地端口转发测试
kubectl port-forward -n shark-socket svc/shark-socket-http 18400:18400
curl http://localhost:18400/hello

# 查看日志
kubectl logs -n shark-socket -l app.kubernetes.io/name=shark-socket --tail=50

# 查看指标
kubectl port-forward -n shark-socket svc/shark-socket-metrics 9091:9091
curl http://localhost:9091/metrics
```

### Makefile 命令

```bash
make build       # 编译所有包
make test        # 运行测试
make vet         # 静态分析
make lint        # golangci-lint
make benchmark   # 基准测试
make race        # 竞态检测
make examples    # 编译所有示例
make docker-build       # 构建 Docker 镜像
make docker-compose-up  # 启动 Docker Compose
make helm-lint           # 验证 Helm Chart
make build-production    # 构建生产二进制
make all         # vet + build + test
```

---

## 📖 文档

- [架构设计文档](docs/shark-socket%20ARCHITECTURE.md) — 完整设计决策与实现细节
- [测试覆盖文档](docs/TEST_COVERAGE.md) — 全部 627 个测试详解
- [代码审查与改进计划](docs/CODE_REVIEW_PLAN.md) — 4 轮专家审查，55 个问题追踪
- [CI/CD 工作流](.github/workflows/ci.yml) — 7 作业矩阵（多 OS × 多 Go 版本）
- [API 文档](https://pkg.go.dev/github.com/X1aSheng/shark-socket) — GoDoc
- [示例代码](examples/) — 8 个可运行示例
- [English Documentation](./README_EN.md)

---

## 📈 项目统计

| 指标 | 数值 |
|------|------|
| Go 代码行数 | 32,000+ |
| Go 文件数 | 176 |
| 包数量 | 37 |
| 测试函数 | 627 (620 Test + 7 Fuzz) |
| 基准测试 | 55 |
| 测试文件 | 75 |
| 示例 | 8 |
| 外部依赖 | 4 个核心模块 (prometheus, quic-go, gorilla/websocket, opentelemetry) |
| 代码审查 | 25/25 全部完成 (3 轮专家审查) |

---

## 🤝 贡献

欢迎提交 Issue 和 Pull Request！

1. Fork 本仓库
2. 创建特性分支 (`git checkout -b feature/amazing-feature`)
3. 提交变更 (`git commit -m 'Add amazing feature'`)
4. 推送到分支 (`git push origin feature/amazing-feature`)
5. 提交 Pull Request

---

## 📄 许可证

本项目采用 [MIT 许可证](LICENSE)。
