# Shark-Socket

[![Go Version](https://img.shields.io/badge/Go-1.24%2B-blue)](https://go.dev)
[![CI](https://img.shields.io/badge/CI-golangci--lint%20%7C%20test%20%7C%20bench-brightgreen)](.github/workflows/ci.yml)
[![License](https://img.shields.io/badge/License-MIT-green)](LICENSE)
[![Tests](https://img.shields.io/badge/Tests-14%20packages%20passed-brightgreen)](./tests)
[![Docker](https://img.shields.io/badge/Docker-ready-blue)](./Dockerfile)
[![Kubernetes](https://img.shields.io/badge/Kubernetes-ready-blue)](./k8s)

A high-performance, extensible **multi-protocol networking framework** developed in Go, supporting **TCP**, **TLS**, **UDP**, **HTTP**, **WebSocket**, and **CoAP** protocols with unified abstraction and gateway integration.

[中文文档](README.md) | English

## ✨ Features

| Feature | Description |
|---------|-------------|
| 🔄 **Multi-Protocol Support** | Unified abstraction for TCP, TLS, UDP, HTTP, WebSocket, and CoAP |
| 🛡️ **Type Safety** | Generic protocol stack with compile-time message type safety |
| ⚡ **High Performance** | Worker Pool, read buffer reuse, lock-free counters |
| 🔌 **Plugin System** | Full lifecycle hooks: OnAccept / OnMessage / OnClose |
| 📊 **Observability** | Built-in logging and Prometheus metrics collection |
| 🗄️ **Middleware Integration** | Cache / Store / PubSub interface layer with Redis, SQL, NATS adapters |
| 🌐 **Distributed Extension** | ClusterPlugin for cross-node session awareness via PubSub broadcasting |
| 🧩 **Extensibility** | Custom protocols, custom transport layers, flexible Session management |
| 🎯 **Clear Boundaries** | Unidirectional dependencies with clear interface contracts |
| 🐳 **Cloud Native** | Docker, Kubernetes, Helm deployment support |

## 🚀 Quick Start

### Installation

```bash
go get github.com/X1aSheng/shark-socket
```

### Docker

```bash
# Build image
docker build -t shark-socket .

# Run container
docker run -d -p 8080:8080 shark-socket
```

### Docker Compose

```bash
docker-compose up -d
```

### Kubernetes

```bash
kubectl apply -f k8s/

# Or use Helm
helm install shark-socket k8s/helm/shark-socket
```

### Basic Example

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

// Define your message type
type MyMessage struct {
    Data []byte
}

// Implement the Protocol[T] interface
type MyProtocol struct{}

func (p *MyProtocol) Decode(data []byte) (MyMessage, error) {
    return MyMessage{Data: data}, nil
}

func (p *MyProtocol) Encode(msg MyMessage) ([]byte, error) {
    return msg.Data, nil
}

// Implement the MessageProcessor[T] interface
type MyProcessor struct{}

func (p *MyProcessor) Process(sess session.NetworkSession, msg MyMessage) error {
    fmt.Printf("Received message: %s\n", string(msg.Data))
    return sess.Send(msg.Data)
}

func main() {
    // Create session manager
    sessionMgr := session.NewManager(30 * time.Minute)

    // Create gateway
    gw := gateway.NewGateway(
        gateway.WithSessionManager(sessionMgr),
    )

    // Add TCP server
    proto := &MyProtocol{}
    processor := &MyProcessor{}

    server := tcp.NewServer(":8080", proto, processor,
        tcp.WithSessionManager(sessionMgr),
        tcp.WithWriteQueueSize(1024),
    )
    gw.AddServer(server)

    // Start
    if err := gw.Start(); err != nil {
        log.Fatal(err)
    }
    defer gw.Stop()

    fmt.Println("TCP server started on :8080")

    // Block waiting
    select {}
}
```

## 📦 Supported Protocols

| Protocol | Type | Features |
|----------|------|----------|
| **TCP** | Streaming | Worker Pool, non-blocking write queue, TLS support, generic client |
| **UDP** | Datagram | Connectionless session tracking, automatic session timeout |
| **HTTP** | Request-Response | Standard library `net/http` wrapper |
| **WebSocket** | Full-Duplex | Gorilla WebSocket, automatic Ping/Pong |
| **CoAP** | Constrained Application | UDP-based IoT protocol support |

## 🏗️ Architecture Overview

```
┌─────────────────────────────────────────────────────────┐
│                     API Layer                            │
│              (Unified entry, type aliases)               │
├─────────────────────────────────────────────────────────┤
│                   Gateway (Gateway)                      │
│         ┌─────────────────────────────────────┐         │
│         │     Multi-protocol orchestration      │         │
│         │  TCP / UDP / HTTP / WS / CoAP        │         │
│         └─────────────────────────────────────┘         │
├─────────────────────────────────────────────────────────┤
│              Session Manager (Session Management)         │
│         ┌───────────────────────────────┐               │
│         │ • Atomic ID  • LRU cleanup   │               │
│         │ • Statistics                 │               │
│         └───────────────────────────────┘               │
├─────────────────────────────────────────────────────────┤
│   Plugin System (OnAccept/OnMessage/OnClose hooks)       │
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

See [Architecture Design Document](docs/shark-socket%20architecture.md) for full details.

## 📂 Project Structure

```
shark-socket/
├── api/                    # HTTP API endpoint definitions
├── docs/                   # Documentation
│   ├── performance/       # Performance test reports
│   ├── DEPLOYMENT.md     # Deployment guide
│   └── ...
├── examples/               # Example code
│   ├── basic_tcp/         # Basic TCP server
│   ├── basic_udp/         # Basic UDP server
│   ├── basic_http/         # Basic HTTP server
│   ├── basic_websocket/    # Basic WebSocket server
│   ├── basic_coap/        # Basic CoAP server
│   ├── multi_protocol/     # Multi-protocol combined server
│   ├── tls_server/        # TLS encrypted server
│   ├── tcp_client/        # TCP client example
│   ├── custom_protocol/    # Custom protocol implementation
│   ├── session_plugins/    # Plugin system example
│   └── graceful_shutdown/  # Graceful shutdown example
├── internal/              # Internal implementation
│   ├── gateway/           # Unified gateway, protocol orchestration
│   ├── protocol/          # Protocol implementations
│   │   ├── tcp/           # TCP protocol server (with Worker Pool)
│   │   ├── udp/           # UDP protocol server
│   │   ├── http/          # HTTP protocol server
│   │   ├── websocket/     # WebSocket protocol server
│   │   └── coap/          # CoAP protocol server
│   ├── session/           # Session management (lifecycle, state tracking)
│   ├── errs/              # Framework-level error definitions
│   ├── plugin/            # Plugin system (lifecycle hooks)
│   ├── types/             # Core type definitions (interfaces, enums)
│   └── infra/             # Infrastructure
│       ├── bufferpool/    # Object pool (buffer reuse)
│       ├── logger/        # Logging system
│       ├── metrics/       # Metrics collection (Prometheus)
│       ├── cache/         # Cache interface (Redis adapter etc.)
│       ├── store/         # Persistence interface (SQL adapter etc.)
│       └── pubsub/        # Pub/Sub interface (NATS adapter etc.)
├── k8s/                   # Kubernetes configuration
│   ├── deployment.yaml     # Deployment config
│   ├── service.yaml        # Service config
│   ├── hpa.yaml           # Auto-scaling
│   ├── config/             # ConfigMap
│   └── helm/              # Helm Chart
├── monitoring/             # Monitoring configuration
│   └── prometheus.yml     # Prometheus config
├── tests/                  # Integration and stress tests
│   ├── tcp_stress_test.go
│   ├── udp_stress_test.go
│   ├── http_stress_test.go
│   ├── websocket_stress_test.go
│   └── benchmark_test.go
├── Dockerfile              # Docker image build
├── docker-compose.yml      # Docker Compose config
├── go.mod                  # Go module config
├── README.md               # Chinese documentation
├── README_EN.md            # English documentation
├── CHANGELOG.md            # Changelog
└── CONTRIBUTING.md         # Contributing guide
```

## 🧪 Testing

Run unit tests (skip stress tests):

```bash
go test -short ./... -v
```

Run all tests including stress tests:

```bash
go test ./... -v
```

Run stress tests:

```bash
go test ./tests -v -run "Stress"
```

Run benchmarks:

```bash
go test ./tests -bench=. -benchmem
```

Lint:

```bash
golangci-lint run --timeout=5m ./...
```

### Test Coverage

| Module | Tests | Status |
|--------|-------|--------|
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
| tests (stress) | 11 | ✅ |

**Total**: 78 test files, 14 packages passed ✅

## 📊 Performance Report

See [docs/performance/PERFORMANCE.md](docs/performance/PERFORMANCE.md) for detailed performance report.

### Performance Highlights

| Metric | Value | Description |
|--------|-------|-------------|
| **Session Register** | 1.65M ops/sec | 1.65 million registrations per second |
| **Session Unregister** | 144M ops/sec | 144 million unregistrations per second |
| **Buffer Operations** | 4.6M ops/sec | Zero memory allocation |
| **TCP Connections** | 1,289 conn/sec | 1289 connections per second |
| **TCP Messages** | 101K msg/sec | 101 thousand messages per second |

---

## 🐳 Deployment

### Docker

```bash
# Build
docker build -t shark-socket:latest .

# Run
docker run -d -p 8080:8080 shark-socket

# Docker Compose
docker-compose up -d
```

### Kubernetes

```bash
# Deploy all components
kubectl apply -f k8s/

# Check pods
kubectl get pods -l app=shark-socket

# View logs
kubectl logs -f deployment/shark-socket-tcp
```

### Helm

```bash
# Install
helm install shark-socket k8s/helm/shark-socket

# Custom configuration
helm install shark-socket k8s/helm/shark-socket \
  --set replicaCount=5 \
  --set autoscaling.enabled=true
```

See [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) for detailed deployment guide.

---

## 🔌 Plugin System

Shark-Socket provides full lifecycle plugin hooks:

| Hook | Trigger | Use Case |
|------|---------|----------|
| `OnAccept` | Before connection establishment | IP whitelist/blacklist, connection limits |
| `OnConnected` | After session creation | Session initialization, state setup |
| `OnMessage` | Before message processing | Message filtering, transformation, routing |
| `OnClose` | After session close | Resource cleanup, logging |

All plugin hooks are protected against **panics**, plugin crashes won't affect the main flow.

### Built-in Plugins

```go
// IP Blacklist Plugin
blacklist := plugin.NewBlacklistPlugin(nil)
blacklist.AddIP("192.168.1.100")

// Rate Limiter Plugin (100 requests per second)
rateLimiter := plugin.NewRateLimiterPlugin(100)

// Heartbeat Plugin (30 second timeout)
heartbeat := plugin.NewHeartbeatPlugin(30 * time.Second)
```

### Middleware Integration Plugins

```go
// Session Persistence — auto-save session lifecycle to Store
persist := plugin.NewSessionPersistPlugin(myStore, "session:")
registry.Register(persist)

// Message Deduplication — Cache-Aside pattern, SHA256 hash dedup
dedup := plugin.NewCacheAsidePlugin(myCache, 5 * time.Minute)
registry.Register(dedup)

// Cluster Awareness — broadcast session events via PubSub
cluster := plugin.NewClusterPlugin(myPubSub, logger, "node-1", "cluster:events")
registry.Register(cluster)
cluster.Start()
defer cluster.Stop()
```

## 📊 Observability

Built-in logging and metrics collection:

### Logging

```go
// Custom logger
gw := gateway.NewGateway(
    gateway.WithLogger(logger.NewCustom("my-service")),
)
```

### Metrics

The framework automatically collects the following metrics (Prometheus format):

| Metric | Description |
|--------|-------------|
| `shark_socket_active_connections` | Current active connections |
| `shark_socket_received_bytes_total` | Total bytes received |
| `shark_socket_sent_bytes_total` | Total bytes sent |
| `shark_socket_message_processing_duration_ms` | Message processing latency (ms) |
| `shark_socket_errors_total` | Total errors (by protocol) |

## 🧵 Worker Pool

TCP server supports optional Worker Pool for high-concurrency scenarios:

```go
server := tcp.NewServer(":8080", proto, processor,
    tcp.WithWorkerPool(100), // 100 workers
)
```

Worker Pool Features:
- Fixed number of goroutines processing messages
- Reduced goroutine creation/destruction overhead
- Task submission and completion waiting support
- Built-in statistics

## 🛠️ TCP Client

Shark-Socket also provides a generic TCP client:

```go
client := tcp.NewClient(":8080", &MyProtocol{})

if err := client.Connect(context.Background()); err != nil {
    log.Fatal(err)
}

// Send message
msg := MyMessage{Data: []byte("hello")}
if err := client.Send(msg); err != nil {
    log.Fatal(err)
}

// Receive response
resp, err := client.Receive(context.Background())
```

---

## 📖 Documentation

- [Architecture Design Document](docs/shark-socket%20architecture.md)
- [Deployment Guide](docs/DEPLOYMENT.md)
- [Performance Report](docs/performance/PERFORMANCE.md)
- [中文文档](README.md)
- [API Documentation](https://pkg.go.dev/github.com/X1aSheng/shark-socket)
- [Examples](examples/)
- [Contributing Guide](CONTRIBUTING.md)
- [Changelog](CHANGELOG.md)

## 🤝 Contributing

We welcome Issues and Pull Requests!

1. Fork this repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Submit a Pull Request

See [CONTRIBUTING.md](CONTRIBUTING.md) for detailed contribution guidelines.

## 📄 License

This project is licensed under the [MIT License](LICENSE).
