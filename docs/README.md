# Shark-Socket

[![Go Version](https://img.shields.io/badge/Go-1.24%2B-blue)](https://go.dev)
[![CI](https://img.shields.io/badge/CI-golangci--lint%20%7C%20test%20%7C%20bench-brightgreen)](.github/workflows/ci.yml)
[![License](https://img.shields.io/badge/License-MIT-green)](LICENSE)
[![Tests](https://img.shields.io/badge/Tests-14%20packages%20passed-brightgreen)](./tests)
[![Docker](https://img.shields.io/badge/Docker-ready-blue)](./Dockerfile)
[![Kubernetes](https://img.shields.io/badge/Kubernetes-ready-blue)](./k8s)

[English](README_EN.md) | 中文文档

高性能、可扩展的**多协议网络框架**，采用 Go 语言开发，支持 **TCP**、**TLS**、**UDP**、**HTTP**、**WebSocket** 和 **CoAP** 协议的统一抽象与网关集成。

## ✨ 特性

| 特性 | 说明 |
|------|------|
| 🔄 **多协议支持** | TCP、TLS、UDP、HTTP、WebSocket、CoAP 统一抽象 |
| 🛡️ **类型安全** | 泛型协议栈，编译期保证消息类型安全 |
| ⚡ **高性能** | Worker Pool、读缓冲复用、无锁计数器 |
| 🔌 **插件系统** | OnAccept / OnMessage / OnClose 全生命周期插件钩子 |
| 📊 **可观测性** | 内置日志和 Prometheus 指标采集 |
| 🗄️ **中间件对接** | Cache / Store / PubSub 接口层，支持 Redis、SQL、NATS 等适配 |
| 🌐 **分布式扩展** | ClusterPlugin 跨节点会话感知，PubSub 集群事件广播 |
| 🧩 **可扩展** | 自定义协议、自定义传输层、灵活的 Session 管理 |
| 🎯 **边界清晰** | 各层单向依赖，接口契约明确 |
| 🐳 **云原生** | Docker、Kubernetes、Helm 部署支持 |

## 🚀 快速开始

### 安装

```bash
go get github.com/X1aSheng/shark-socket
```

### Docker 部署

```bash
# 构建镜像
docker build -t shark-socket .

# 运行容器
docker run -d -p 8080:8080 shark-socket
```

### Docker Compose

```bash
docker-compose up -d
```

### Kubernetes 部署

```bash
kubectl apply -f k8s/

# 或使用 Helm
helm install shark-socket k8s/helm/shark-socket
```

### 基础示例

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/X1aSheng/shark-socket/internal/gateway"
    "github.com/X1aSheng/shark-socket/internal/protocol/tcp"
    "github.com/X1aSheng/shark-socket/internal/session"
)

// 定义你的消息类型
type MyMessage struct {
    Data []byte
}

// 实现 Protocol[T] 接口
type MyProtocol struct{}

func (p *MyProtocol) Decode(data []byte) (MyMessage, error) {
    return MyMessage{Data: data}, nil
}

func (p *MyProtocol) Encode(msg MyMessage) ([]byte, error) {
    return msg.Data, nil
}

// 实现 MessageProcessor[T] 接口
type MyProcessor struct{}

func (p *MyProcessor) Process(sess session.NetworkSession, msg MyMessage) error {
    fmt.Printf("收到消息: %s\n", string(msg.Data))
    return sess.Send(msg.Data)
}

func main() {
    // 创建会话管理器
    sessionMgr := session.NewManager(30 * time.Minute)

    // 创建网关
    gw := gateway.NewGateway(
        gateway.WithSessionManager(sessionMgr),
    )

    // 添加 TCP 服务器
    proto := &MyProtocol{}
    processor := &MyProcessor{}

    server := tcp.NewServer(":8080", proto, processor,
        tcp.WithSessionManager(sessionMgr),
        tcp.WithWriteQueueSize(1024),
    )
    gw.AddServer(server)

    // 启动
    if err := gw.Start(); err != nil {
        log.Fatal(err)
    }
    defer gw.Stop()

    fmt.Println("TCP 服务器已启动 :8080")

    // 阻塞等待
    select {}
}
```

## 📦 支持的协议

| 协议 | 类型 | 特性 |
|------|------|------|
| **TCP** | 流式 | Worker Pool、非阻塞写队列、TLS 支持、泛型客户端 |
| **UDP** | 数据报 | 无连接会话跟踪、会话自动超时 |
| **HTTP** | 请求响应 | 标准库 `net/http` 包装 |
| **WebSocket** | 全双工 | Gorilla WebSocket、自动 Ping/Pong |
| **CoAP** | 受限应用 | 基于 UDP 的 IoT 协议支持 |

## 🏗️ 架构概览

```
┌─────────────────────────────────────────────────────────┐
│                     API 层                               │
│              (统一入口，类型别名导出)                      │
├─────────────────────────────────────────────────────────┤
│                   Gateway (网关)                         │
│         ┌─────────────────────────────────────┐         │
│         │     多协议服务器编排                   │         │
│         │  TCP / UDP / HTTP / WS / CoAP        │         │
│         └─────────────────────────────────────┘         │
├─────────────────────────────────────────────────────────┤
│              Session Manager (会话管理)                  │
│         ┌───────────────────────────────┐               │
│         │ • 原子ID生成  • LRU清理  • 统计 │               │
│         └───────────────────────────────┘               │
├─────────────────────────────────────────────────────────┤
│   Plugin System (OnAccept/OnMessage/OnClose 钩子)        │
│   ┌──────┬──────┬──────┬──────┬──────┬──────┐           │
│   │Black │Rate  │Cache │Heart │Sess  │Clust │           │
│   │list  │Limit │Aside │beat  │Pers  │er    │           │
│   └──────┴──────┴──────┴──────┴──────┴──────┘           │
├─────────────────────────────────────────────────────────┤
│    Infrastructure                                       │
│   ┌──────────┬──────────┬──────────┬──────────┐         │
│   │ Logger   │ Metrics  │  Cache   │  Store   │         │
│   ├──────────┼──────────┼──────────┼──────────┤         │
│   │BufferPool│  PubSub  │ Options  │  Errors  │         │
│   └──────────┴──────────┴──────────┴──────────┘         │
└─────────────────────────────────────────────────────────┘
```

完整架构文档见 [docs/shark-socket architecture.md](docs/shark-socket%20architecture.md)

## 📂 项目结构

```
shark-socket/
├── api/                    # HTTP API 端点定义
├── docs/                   # 架构设计文档
│   ├── performance/       # 性能测试报告
│   ├── DEPLOYMENT.md      # 部署指南
│   └── ...
├── examples/               # 示例代码
│   ├── basic_tcp/         # 基础 TCP 服务器
│   ├── basic_udp/         # 基础 UDP 服务器
│   ├── basic_http/        # 基础 HTTP 服务器
│   ├── basic_websocket/   # 基础 WebSocket 服务器
│   ├── basic_coap/        # 基础 CoAP 服务器
│   ├── multi_protocol/    # 多协议组合服务器
│   ├── tls_server/        # TLS 加密服务器
│   ├── tcp_client/        # TCP 客户端示例
│   ├── custom_protocol/   # 自定义协议实现
│   ├── session_plugins/   # 插件系统示例
│   └── graceful_shutdown/ # 优雅关闭示例
├── internal/              # 内部实现
│   ├── gateway/           # 统一网关，协议服务器编排
│   ├── protocol/          # 各协议实现
│   │   ├── tcp/           # TCP 协议服务器 (含 Worker Pool)
│   │   ├── udp/           # UDP 协议服务器
│   │   ├── http/          # HTTP 协议服务器
│   │   ├── websocket/     # WebSocket 协议服务器
│   │   └── coap/          # CoAP 协议服务器
│   ├── session/           # 会话管理（生命周期、状态跟踪）
│   ├── errs/              # 框架级错误定义
│   ├── plugin/            # 插件系统（生命周期钩子）
│   ├── types/             # 核心类型定义（接口、枚举）
│   └── infra/             # 基础设施
│       ├── bufferpool/    # 对象池（缓冲区复用）
│       ├── logger/        # 日志系统
│       ├── metrics/       # 指标采集（Prometheus）
│       ├── cache/         # 缓存接口（Redis 等适配）
│       ├── store/         # 持久化接口（SQL 等适配）
│       └── pubsub/        # 发布订阅接口（NATS 等适配）
├── k8s/                   # Kubernetes 配置
│   ├── deployment.yaml    # 部署配置
│   ├── service.yaml       # 服务配置
│   ├── hpa.yaml           # 自动扩缩容
│   ├── config/            # ConfigMap
│   └── helm/              # Helm Chart
├── monitoring/            # 监控配置
│   └── prometheus.yml     # Prometheus 配置
├── tests/                  # 集成与压力测试
│   ├── tcp_stress_test.go      # TCP 压力测试
│   ├── udp_stress_test.go      # UDP 压力测试
│   ├── http_stress_test.go     # HTTP 压力测试
│   ├── websocket_stress_test.go # WebSocket 压力测试
│   └── benchmark_test.go       # 基准测试
├── Dockerfile             # Docker 镜像构建
├── docker-compose.yml     # Docker Compose 配置
├── go.mod                  # Go 模块配置
├── README.md               # 中文文档
├── README_EN.md            # 英文文档
├── CHANGELOG.md            # 更新日志
└── CONTRIBUTING.md         # 贡献指南
```

## 🧪 测试

运行所有单元测试（跳过压力测试）：

```bash
go test -short ./... -v
```

运行包含压力测试的完整测试：

```bash
go test ./... -v
```

运行压力测试：

```bash
go test ./tests -v -run "Stress"
```

运行基准测试：

```bash
go test ./tests -bench=. -benchmem
```

代码检查：

```bash
golangci-lint run --timeout=5m ./...
```

### 测试覆盖

| 模块 | 测试数 | 状态 |
|------|--------|------|
| gateway | 5 | ✅ |
| bufferpool | 4 | ✅ |
| logger | 3 | ✅ |
| metrics | 2 | ✅ |
| plugin | 17 | ✅ |
| protocol/tcp | 4 | ✅ |
| protocol/udp | 2 | ✅ |
| protocol/http | 2 | ✅ |
| protocol/websocket | 2 | ✅ |
| protocol/coap | 4 | ✅ |
| errs | 1 | ✅ |
| session | 15 | ✅ |
| types | 3 | ✅ |
| tests (压力测试) | 11 | ✅ |

**总计**: 78 个测试文件，14 个包全部通过 ✅

## 📊 性能报告

详细性能报告见 [docs/performance/PERFORMANCE.md](docs/performance/PERFORMANCE.md)

### 性能亮点

| 指标 | 数值 | 说明 |
|------|------|------|
| **Session 注册** | 1.65M ops/sec | 每秒处理 165 万次注册 |
| **Session 注销** | 144M ops/sec | 每秒处理 1.44 亿次注销 |
| **Buffer 操作** | 4.6M ops/sec | 零内存分配 |
| **TCP 连接** | 1,289 conn/sec | 每秒建立 1289 个连接 |
| **TCP 消息** | 101K msg/sec | 每秒处理 10 万条消息 |

---

## 🐳 部署

### Docker

```bash
# 构建
docker build -t shark-socket:latest .

# 运行
docker run -d -p 8080:8080 shark-socket

# Docker Compose
docker-compose up -d
```

### Kubernetes

```bash
# 部署所有组件
kubectl apply -f k8s/

# 查看 Pod
kubectl get pods -l app=shark-socket

# 查看日志
kubectl logs -f deployment/shark-socket-tcp
```

### Helm

```bash
# 安装
helm install shark-socket k8s/helm/shark-socket

# 自定义配置
helm install shark-socket k8s/helm/shark-socket \
  --set replicaCount=5 \
  --set autoscaling.enabled=true
```

详细部署指南见 [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md)

---

## 🔌 插件系统

Shark-Socket 提供全生命周期的插件钩子：

| 钩子 | 触发时机 | 用途 |
|------|----------|------|
| `OnAccept` | 连接建立前 | IP 白名单/黑名单、连接限制 |
| `OnConnected` | 会话创建后 | 会话初始化、状态设置 |
| `OnMessage` | 消息处理前 | 消息过滤、转换、路由 |
| `OnClose` | 会话关闭后 | 清理资源、记录日志 |

所有插件钩子都带有 **panic 保护**，插件崩溃不会影响主流程。

### 内置插件

```go
// IP 黑名单插件
blacklist := plugin.NewBlacklistPlugin(nil)
blacklist.AddIP("192.168.1.100")

// 限流插件 (每秒100请求)
rateLimiter := plugin.NewRateLimiterPlugin(100)

// 心跳插件 (30秒超时)
heartbeat := plugin.NewHeartbeatPlugin(sessionMgr, 30 * time.Second)
```

### 中间件集成插件

```go
// 会话持久化 — 自动保存会话生命周期到 Store
persist := plugin.NewSessionPersistPlugin(myStore, "session:")
registry.Register(persist)

// 消息去重 — Cache-Aside 模式，SHA256 哈希去重
dedup := plugin.NewCacheAsidePlugin(myCache, 5 * time.Minute)
registry.Register(dedup)

// 集群感知 — 通过 PubSub 广播会话事件
cluster := plugin.NewClusterPlugin(myPubSub, logger, "node-1", "cluster:events")
registry.Register(cluster)
cluster.Start()
defer cluster.Stop()
```

## 📊 可观测性

内置日志和指标采集：

### 日志

```go
// 自定义日志器
gw := gateway.NewGateway(
    gateway.WithLogger(logger.NewCustom("my-service")),
)
```

### 指标

框架自动采集以下指标（Prometheus 格式）：

| 指标名 | 说明 |
|--------|------|
| `shark_socket_active_connections` | 当前活跃连接数 |
| `shark_socket_received_bytes_total` | 接收字节总数 |
| `shark_socket_sent_bytes_total` | 发送字节总数 |
| `shark_socket_message_processing_duration_ms` | 消息处理延迟（毫秒） |
| `shark_socket_errors_total` | 错误总数（按协议分类） |

## 🧵 Worker Pool

TCP 服务器支持可选的 Worker Pool，用于高并发场景：

```go
server := tcp.NewServer(":8080", proto, processor,
    tcp.WithWorkerPool(100), // 100 个 worker
)
```

Worker Pool 特性：
- 固定数量的 goroutine 处理消息
- 减少 goroutine 创建销毁开销
- 支持任务提交和等待完成
- 内置统计信息

## 🛠️ TCP 客户端

Shark-Socket 还提供泛型 TCP 客户端：

```go
client := tcp.NewClient(":8080", &MyProtocol{})

if err := client.Connect(context.Background()); err != nil {
    log.Fatal(err)
}

// 发送消息
msg := MyMessage{Data: []byte("hello")}
if err := client.Send(msg); err != nil {
    log.Fatal(err)
}

// 接收响应
resp, err := client.Receive(context.Background())
```

---

## 📖 文档

- [架构设计文档](docs/shark-socket%20architecture.md)
- [部署指南](docs/DEPLOYMENT.md)
- [性能报告](docs/performance/PERFORMANCE.md)
- [英文文档](README_EN.md)
- [API 文档](https://pkg.go.dev/github.com/X1aSheng/shark-socket)
- [示例代码](examples/)
- [贡献指南](CONTRIBUTING.md)
- [更新日志](CHANGELOG.md)

## 🤝 贡献

欢迎提交 Issue 和 Pull Request！详情请参阅 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 📄 许可证

本项目采用 [MIT 许可证](LICENSE)。
