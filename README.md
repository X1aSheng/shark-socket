# Shark-Socket

[![Go Version](https://img.shields.io/badge/Go-1.26%2B-blue)](https://go.dev)
[![CI](https://img.shields.io/badge/CI-golangci--lint%20%7C%20test%20%7C%20bench-brightgreen)](.github/workflows/ci.yml)
[![License](https://img.shields.io/badge/License-MIT-green)](LICENSE)
[![Tests](https://img.shields.io/badge/Tests-600%2B%20passed-brightgreen)](./tests)
[![Docker](https://img.shields.io/badge/Docker-ready-blue)](./Dockerfile)
[![Kubernetes](https://img.shields.io/badge/Kubernetes-ready-blue)](./k8s)

[English](./README_EN.md) | 中文文档

高性能、可扩展的**多协议网络框架**，采用 Go 语言开发，支持 **TCP**、**TLS**、**UDP**、**HTTP**、**WebSocket** 和 **CoAP** 协议的统一抽象与网关集成。

---

## ✨ 特性

| 特性 | 说明 |
|------|------|
| 🔄 **多协议支持** | TCP、TLS、UDP、HTTP、WebSocket、CoAP 统一抽象，共享 Handler 接口 |
| 🛡️ **类型安全** | 泛型 `Session[M]`，编译期保证消息类型安全，`SendTyped` + `Send([]byte)` |
| ⚡ **高性能** | 32 分片 SessionManager、Worker Pool、6 级 BufferPool、零 GC 分配 |
| 🔌 **插件系统** | OnAccept / OnMessage / OnClose 全生命周期插件钩子，优先级排序执行 |
| 📊 **可观测性** | 内置 Prometheus 指标采集、结构化日志 (slog)、健康检查端点 |
| 🗄️ **中间件对接** | Cache / Store / PubSub 接口层，支持 Redis、SQL、NATS 等适配 |
| 🌐 **分布式扩展** | ClusterPlugin 跨节点会话感知，PubSub 集群事件广播 |
| 🛡️ **安全防护** | Blacklist、RateLimit、AutoBan、CircuitBreaker、OverloadProtector |
| 🎯 **优雅关闭** | 6 阶段优雅关闭，SIGTERM 信号处理，连接排空 |
| 🐳 **云原生** | Docker、docker-compose、Kubernetes 完整部署（HPA/PDB/NetworkPolicy/Ingress） |

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
    "github.com/X1aSheng/shark-socket/internal/protocol/tcp"
    "github.com/X1aSheng/shark-socket/internal/types"
)

func main() {
    handler := func(sess types.RawSession, msg types.RawMessage) error {
        return sess.Send(msg.Payload) // echo
    }

    srv := tcp.NewServer(handler, tcp.WithAddr("0.0.0.0", 18000))
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

    tcpSrv := api.NewTCPServer(handler, tcp.WithAddr("0.0.0.0", 18000))
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
    tcp.WithAddr("0.0.0.0", 18000),
    tcp.WithPlugins(
        api.NewBlacklistPlugin(api.WithBlacklistCIDRs("10.0.0.0/8")),
        api.NewRateLimitPlugin(api.WithRateLimitPerIP(100, time.Second)),
        api.NewHeartbeatPlugin(api.WithHeartbeatTimeout(30*time.Second)),
    ),
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
│         │   TCP / UDP / HTTP / WS / CoAP        │             │
│         └──────────────────────────────────────┘             │
├──────────────────────────────────────────────────────────────┤
│              Session Manager (会话管理)                        │
│         ┌────────────────────────────────────┐               │
│         │ • 32 分片锁  • LRU 淘汰  • 原子 ID    │               │
│         │ • Broadcast  • 泛型 Session[M]       │               │
│         └────────────────────────────────────┘               │
├──────────────────────────────────────────────────────────────┤
│   Plugin System (OnAccept/OnMessage/OnClose 钩子)             │
│   ┌───────┬───────┬───────┬───────┬───────┬───────┐          │
│   │Black  │Rate   │Auto   │Heart  │Persist│Clust  │          │
│   │list   │Limit  │Ban    │beat   │ence   │er     │          │
│   │ P:0   │ P:10  │ P:20  │ P:30  │ P:40  │ P:50  │          │
│   └───────┴───────┴───────┴───────┴───────┴───────┘          │
├──────────────────────────────────────────────────────────────┤
│    Infrastructure (基础设施)                                   │
│   ┌──────────┬──────────┬──────────┬──────────┐              │
│   │ Logger   │ Metrics  │  Cache   │  Store   │              │
│   ├──────────┼──────────┼──────────┼──────────┤              │
│   │BufferPool│  PubSub  │CircuitBkr│ Tracing  │              │
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
| **TCP** | 18000 | 流式 | 4 种 Framer、WorkerPool (4 策略)、writeQueue、drain、TLS |
| **UDP** | 18200 | 数据报 | 伪会话 (按地址)、TTL 自动清理 |
| **HTTP** | 18400 | 请求响应 | 模式 A (轻量包装) + 模式 B (会话+插件) |
| **WebSocket** | 18600 | 全双工 | Ping/Pong 保活、Origin 校验、gorilla/websocket |
| **CoAP** | 18800 | 受限应用 | CON 重传、ACK、MessageID 去重 (RFC 7252) |

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

### CoAP 消息类型

| 类型 | 说明 |
|------|------|
| CON | 可确认 — 需 ACK，自动重传 |
| NON | 不可确认 — 即发即忘 |
| ACK | 确认 |
| RST | 重置 |

---

## 🔌 插件系统

插件拦截会话生命周期事件，按优先级排序执行（数字越小越先执行）。

### 插件接口

```go
type Plugin interface {
    Name()     string
    Priority() int
    OnAccept(sess Session[[]byte]) error
    OnMessage(sess Session[[]byte], msg Message[[]byte]) error
    OnClose(sess Session[[]byte]) error
}
```

### 流程控制

通过返回特殊错误控制插件链执行：

| 错误 | 效果 |
|------|------|
| `ErrSkip` | 跳过剩余插件，继续正常处理 |
| `ErrDrop` | 静默丢弃消息 |
| `ErrBlock` | 关闭连接 |

### 内置插件

| 插件 | 优先级 | 用途 |
|------|--------|------|
| `BlacklistPlugin` | 0 | IP/CIDR 黑名单，支持 TTL |
| `RateLimitPlugin` | 10 | 双层令牌桶（全局 + 单 IP） |
| `AutoBanPlugin` | 20 | 阈值违规自动封禁 |
| `HeartbeatPlugin` | 30 | TimeWheel 空闲超时检测 |
| `ClusterPlugin` | 40 | 跨节点会话路由，PubSub 广播 |
| `PersistencePlugin` | 50 | 异步批量写入，熔断保护 |

所有插件钩子都带有 **panic 保护**，插件崩溃不会影响主流程。

---

## 🗄️ 基础设施

### 6 级 BufferPool

基于 `sync.Pool` 的零 GC 缓冲区分配，按请求大小自动选择级别。

| 级别 | 名称 | 大小 | 使用场景 |
|------|------|------|----------|
| 0 | Micro | 512B | 控制消息 |
| 1 | Tiny | 2KB | CoAP、小负载 |
| 2 | Small | 4KB | 常规消息 |
| 3 | Medium | 32KB | 大消息 |
| 4 | Large | 256KB | 批量传输 |
| 5 | Huge | >256KB | 直接分配 |

池化 vs 直接分配：Micro 快 ~10x，Small 快 ~20x。

### 熔断器

三态保护：Closed → Open → HalfOpen。

```go
cb := api.NewCircuitBreaker(
    api.WithCBFailureThreshold(5),
    api.WithCBTimeout(30*time.Second),
)
err := cb.Do(func() error {
    return riskyOperation()
})
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
ch := ps.Subscribe(ctx, "events")
ps.Publish(ctx, "events", []byte("hello"))
ps.Close()
```

### Prometheus 指标

框架自动采集以下指标（Prometheus 格式）：

| 指标 | 说明 |
|------|------|
| `shark_connections_total` | 连接总数 |
| `shark_connections_active` | 活跃连接数 |
| `shark_messages_total` | 消息总数 |
| `shark_message_bytes` | 消息字节数分布 |
| `shark_message_duration_seconds` | 消息处理延迟 |
| `shark_errors_total` | 错误总数（按协议分类） |
| `shark_session_lru_evictions_total` | LRU 淘汰次数 |
| `shark_bufferpool_hits_total` | BufferPool 命中次数 |
| `shark_plugin_duration_seconds` | 插件执行延迟 |

---

## 📂 项目结构

```
shark-socket/
├── api/                    # 公共 API — 类型别名、工厂函数
├── internal/
│   ├── gateway/            # 多协议网关（6 阶段关闭）
│   ├── protocol/           # TCP、UDP、HTTP、WebSocket、CoAP 实现
│   ├── session/            # BaseSession、分片 Manager、LRU 淘汰
│   ├── plugin/             # 插件链 + 6 个内置插件
│   ├── infra/              # 基础设施
│   │   ├── bufferpool/     # 6 级 sync.Pool 缓冲池
│   │   ├── cache/          # 内存 TTL 缓存
│   │   ├── circuitbreaker/ # 熔断器
│   │   ├── logger/         # 结构化日志 (slog)
│   │   ├── metrics/        # Prometheus 集成
│   │   ├── pubsub/         # 基于通道的发布订阅
│   │   ├── store/          # Key-Value 存储
│   │   └── tracing/        # 最小化追踪接口
│   ├── defense/            # 过载保护、背压、日志采样
│   ├── types/              # 枚举、Message[T]、Session[M]、Plugin 接口
│   ├── errs/               # 错误分类体系
│   └── utils/              # ShardedMap[K,V]、AtomicBool、IP 解析
├── examples/               # 可运行的示例代码
├── tests/
│   ├── unit/               # 跨包单元测试
│   ├── integration/        # 多协议系统集成测试
│   └── benchmark/          # 性能基准测试
├── k8s/                    # Kubernetes 部署清单
├── scripts/                # 构建、测试、日志脚本
└── docs/                   # 架构设计文档
```

---

## 🧪 测试

### 运行测试

```bash
# 全部测试（25 个包，600+ 测试用例，全部通过）
go test ./... -v

# 带覆盖率
go test ./... -cover

# 基准测试（49 个基准，5 个包）
go test -bench=. -benchmem -run=^$ ./...

# 竞态检测
go test -race ./...

# 指定包
go test ./internal/protocol/tcp/... -v
```

### 测试日志

自动记录结构化测试日志：

```bash
bash scripts/run_tests.sh              # 全部测试
bash scripts/run_tests.sh --unit       # 仅单元测试
bash scripts/run_tests.sh --integration # 仅集成测试
bash scripts/run_tests.sh --benchmark  # 仅基准测试
```

日志保存至 `logs/`，包含 JSON 原始数据 + 可读文本报告。

### 测试覆盖率

22 个内部包 + 3 个测试包，600+ 测试用例：

| 包 | 覆盖率 |
|---|--------|
| defense | 100.0% |
| errs | 100.0% |
| infra/cache | 100.0% |
| infra/store | 100.0% |
| infra/tracing | 100.0% |
| infra/pubsub | 93.9% |
| api | 92.6% |
| session | 92.2% |
| types | 92.3% |
| utils | 89.0% |
| infra/bufferpool | 87.5% |
| protocol/tcp | 87.0% |
| protocol/websocket | 83.4% |
| protocol/http | 83.0% |
| protocol/udp | 81.4% |
| infra/circuitbreaker | 82.8% |
| protocol/coap | 77.8% |
| infra/logger | 73.9% |
| infra/metrics | 60.4% |
| plugin | 62.7% |
| gateway | 48.5% |

测试方法论：使用轮询检测 (`waitForTCPServer` / `waitForUDPServer`) 替代固定睡眠，确保 CI 和本地环境的稳定性。

---

## 📊 性能报告

AMD Ryzen 7 8845HS (Windows 11, Go 1.26.1) 上的测试结果：

### 性能亮点

| 指标 | 数值 | 说明 |
|------|------|------|
| **TCP 并行吞吐** | ~166K msg/s | 多连接并行处理 |
| **TCP 单连接吞吐** | ~31.5K msg/s | 单连接 echo |
| **TCP 大消息吞吐** | 139.8 MB/s | 大帧传输 |
| **UDP echo 吞吐** | ~68K msg/s | 数据报 echo |
| **CoAP 解析** | 119.5 ns/op | 消息反序列化 |
| **Session 注册** | 144.5 ns/op | 32 分片并发安全 |
| **Session 查询** | 7.7 ns/op | 分片 Map 读取 |
| **BufferPool Get+Put** | 12.5 ns/op | 零内存分配 |
| **插件链开销** | ~1.9 ns/hop | 5 个插件 |
| **并发连接** | 100K | 测试验证 |

### TCP 协议

| 基准 | ns/op | B/op | allocs/op | 吞吐量 |
|------|-------|------|-----------|--------|
| TCPEcho | 31,703 | 4,937 | 14 | ~31.5K msg/s |
| TCPEcho_SmallMessage | 40,374 | 4,850 | 14 | ~24.8K msg/s |
| TCPEcho_LargeMessage | 58,582 | 72,531 | 14 | 139.8 MB/s |
| TCPEcho_Parallel | 6,030 | 4,930 | 14 | ~166K msg/s |

### Session Manager

| 基准 | ns/op | B/op | allocs/op |
|------|-------|------|-----------|
| SessionRegister | 144.5 | 400 | 8 |
| ManagerGet | 7.7 | 0 | 0 |
| ManagerNextID | 1.6 | 0 | 0 |
| ManagerNextIDParallel | 9.9 | 0 | 0 |
| ManagerCount | 0.4 | 0 | 0 |

### BufferPool

| 基准 | ns/op | B/op | allocs/op |
|------|-------|------|-----------|
| Pool/Micro_64 | 12.5 | 0 | 0 |
| Pool/Tiny_1024 | 16.1 | 0 | 0 |
| Pool/Small_4096 | 44.0 | 0 | 0 |
| Pool/Medium_16384 | 129.9 | 0 | 0 |
| Pool/Large_131072 | 909.2 | 0 | 0 |
| Parallel/Micro_64 | 11.5 | 0 | 0 |
| DirectAlloc/Micro_64 | 24.0 | 64 | 1 |
| DirectAlloc/Large_131072 | 13,817 | 131,072 | 1 |

### 插件链

| 基准 | ns/op | B/op | allocs/op |
|------|-------|------|-----------|
| Chain_Empty | 1.5 | 0 | 0 |
| Chain_5Plugins | 9.6 | 0 | 0 |
| Chain_10Plugins | 17.2 | 0 | 0 |
| Chain_OnAccept_5 | 8.1 | 0 | 0 |
| Chain_OnClose_5 | 7.0 | 0 | 0 |
| Chain_Parallel | 1.3 | 0 | 0 |

---

## 🐳 部署

### Docker

```bash
# 构建镜像
docker build -t shark-socket .

# Docker Compose（含 Prometheus）
docker-compose up -d

# 验证
curl http://localhost:18400/health
curl http://localhost:9091/metrics
```

**暴露端口：**

| 端口 | 协议 | 服务 |
|------|------|------|
| 18000 | TCP | TCP echo |
| 18200 | UDP | UDP echo |
| 18400 | TCP | HTTP API |
| 18600 | TCP | WebSocket |
| 18800 | UDP | CoAP |
| 9091 | TCP | 指标 / 健康检查 |

### Kubernetes

`k8s/` 目录提供完整的 Kubernetes 生产部署清单。

```bash
# 一键部署
kubectl apply -k k8s/
```

#### K8s 资源清单

| 资源 | 文件 | 说明 |
|------|------|------|
| Namespace | `namespace.yaml` | 独立命名空间 `shark-socket` |
| Deployment | `deployment.yaml` | 2 副本、反亲和、健康检查、preStop |
| Service | `service.yaml` | 每个协议独立 ClusterIP + NodePort 模板 |
| HPA + PDB | `hpa.yaml` | CPU 50%/Memory 70%，2-10 副本，minAvailable: 1 |
| NetworkPolicy | `networkpolicy.yaml` | 命名空间内 + Ingress + 监控白名单 |
| Prometheus | `prometheus/` | Deployment + Service + ConfigMap |
| Ingress | `ingress.yaml` | HTTP + WebSocket 路由（nginx） |
| ConfigMap | `configmap.yaml` | Prometheus 抓取配置 |

#### 健康检查

| 探针 | 端点 | 端口 | 用途 |
|------|------|------|------|
| Liveness | `/healthz` | 9091 | 重启不健康 Pod |
| Readiness | `/readyz` | 9091 | 从 Service 移除 |

#### 优雅关闭

Kubernetes 生命周期配置实现零停机滚动更新：

1. `preStop: sleep 5` — 等待负载均衡器摘除 Pod
2. SIGTERM 触发 Gateway 6 阶段关闭（15s 超时）
3. `terminationGracePeriodSeconds: 30` — 30s 后强制终止

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

---

## 📖 文档

- [架构设计文档](docs/shark-socket%20ARCHITECTURE.md)
- [API 文档](https://pkg.go.dev/github.com/X1aSheng/shark-socket)
- [示例代码](examples/)
- [English Documentation](./README_EN.md)

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
