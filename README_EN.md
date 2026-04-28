# Shark-Socket

[![Go Version](https://img.shields.io/badge/Go-1.26%2B-blue)](https://go.dev)
[![License](https://img.shields.io/badge/License-MIT-green)](LICENSE)
[![Tests](https://img.shields.io/badge/Tests-627%20passed-brightgreen)](./tests)
[![Fuzz](https://img.shields.io/badge/Fuzz-7%20tests-brightgreen)](./tests)
[![Coverage](https://img.shields.io/badge/Coverage-37%20pkgs-brightgreen)](./tests)
[![Docker](https://img.shields.io/badge/Docker-ready-blue)](./Dockerfile)
[![Kubernetes](https://img.shields.io/badge/Kubernetes-ready-blue)](./k8s)

[中文文档](./README.md) | English

A high-performance, extensible **multi-protocol networking framework** developed in Go, supporting **TCP**, **TLS**, **UDP**, **HTTP**, **HTTP/2**, **WebSocket**, **CoAP**, **QUIC**, and **gRPC-Web** protocols with unified abstraction and gateway integration.

---

## ✨ Features

| Feature | Description |
|---------|-------------|
| **Multi-Protocol** | Unified abstraction for TCP, TLS, UDP, HTTP, HTTP/2, WebSocket, CoAP, QUIC, gRPC-Web |
| **Type Safety** | Generic `Session[M]` with compile-time message type safety, `SendTyped` + `Send([]byte)` |
| **High Performance** | 32-shard SessionManager, Worker Pool, 6-level BufferPool, zero-GC allocation |
| **Plugin System** | Full lifecycle hooks: OnAccept / OnMessage / OnClose, priority-ordered execution, panic protection |
| **Observability** | Prometheus metrics, structured logging (slog), OpenTelemetry tracing, access logging |
| **Middleware** | Cache / Store / PubSub interface layer, ready for Redis, SQL, NATS adapters |
| **Distributed** | ClusterPlugin for cross-node session awareness via PubSub broadcasting |
| **Security** | Blacklist, RateLimit, AutoBan, CircuitBreaker, OverloadProtector, connection limiting |
| **Graceful Shutdown** | 6-stage graceful shutdown, SIGTERM handling, connection draining |
| **Cloud Native** | Docker, docker-compose, Kubernetes manifests (HPA/PDB/NetworkPolicy/Ingress) |

---

## 🚀 Quick Start

### Installation

```bash
go get github.com/X1aSheng/shark-socket
```

### Single Protocol Server

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

### Multi-Protocol Gateway

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

    gw.Run() // blocks until SIGINT/SIGTERM, then graceful shutdown
}
```

### TCP Server with Plugins

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

## 🏗️ Architecture Overview

```
┌──────────────────────────────────────────────────────────────┐
│                       API Layer                               │
│          (Unified entry, type aliases, factory functions)     │
├──────────────────────────────────────────────────────────────┤
│                    Gateway                                     │
│         ┌──────────────────────────────────────┐             │
│         │ Multi-protocol orchestration          │             │
│         │ Shared SessionManager                 │             │
│         │ TCP / UDP / HTTP / WS / CoAP          │             │
│         └──────────────────────────────────────┘             │
├──────────────────────────────────────────────────────────────┤
│              Session Manager                                   │
│         ┌────────────────────────────────────┐               │
│         │ • 32 shards  • LRU eviction         │               │
│         │ • Atomic ID  • Generic Session[M]   │               │
│         │ • Broadcast                          │               │
│         └────────────────────────────────────┘               │
├──────────────────────────────────────────────────────────────┤
│   Plugin System (OnAccept/OnMessage/OnClose hooks)            │
│   ┌───────┬───────┬───────┬───────┬───────┬───────┐          │
│   │Black  │Rate   │Auto   │Heart  │Persist│Clust  │          │
│   │list   │Limit  │Ban    │beat   │ence   │er     │          │
│   │ P:0   │ P:10  │ P:20  │ P:30  │ P:40  │ P:50  │          │
│   └───────┴───────┴───────┴───────┴───────┴───────┘          │
├──────────────────────────────────────────────────────────────┤
│    Infrastructure                                              │
│   ┌──────────┬──────────┬──────────┬──────────┐              │
│   │ Logger   │ Metrics  │  Cache   │  Store   │              │
│   ├──────────┼──────────┼──────────┼──────────┤              │
│   │BufferPool│  PubSub  │CircuitBkr│ Tracing  │              │
│   └──────────┴──────────┴──────────┴──────────┘              │
├──────────────────────────────────────────────────────────────┤
│    Defense                                                     │
│   ┌──────────────┬──────────────┬──────────────┐             │
│   │  Overload    │ Backpressure │  LogSampler  │             │
│   │  Protector   │ Controller   │              │             │
│   └──────────────┴──────────────┴──────────────┘             │
└──────────────────────────────────────────────────────────────┘
```

See [Architecture Design Document](docs/shark-socket%20ARCHITECTURE.md) for full details.

---

## 📦 Supported Protocols

| Protocol | Default Port | Type | Features |
|----------|-------------|------|----------|
| **TCP** | 18000 | Streaming | 4 framer types, WorkerPool (4 policies), writeQueue, drain, TLS, hot reload |
| **UDP** | 18200 | Datagram | Pseudo-sessions (by address), TTL auto-sweep |
| **HTTP** | 18400 | Request-Response | Mode A (thin wrapper) + Mode B (session + plugins), HTTP/2 (TLS/h2c) |
| **WebSocket** | 18600 | Full-Duplex | Ping/Pong keepalive, Origin check, access logging |
| **CoAP** | 18800 | Constrained Application | CON retransmit, ACK, MessageID dedup (RFC 7252) |
| **QUIC** | 18900 | Multi-Stream | UDP multiplexing, stream control, 0-RTT |
| **gRPC-Web** | 18650 | Gateway | WebSocket/Direct dual mode, Protobuf serialization |

### TCP Framer Types

| Framer | Description |
|--------|-------------|
| `LengthPrefixFramer` | 4-byte length prefix (default, max 1MB) |
| `LineFramer` | Newline-delimited frames |
| `FixedSizeFramer` | Fixed-size frames |
| `RawFramer` | Raw read with buffer |

### Worker Pool Policies

| Policy | Behavior on queue full |
|--------|----------------------|
| `PolicyBlock` | Blocks until queue has room |
| `PolicyDrop` | Drops the message |
| `PolicySpawnTemp` | Spawns temporary workers |
| `PolicyClose` | Closes the connection |

### CoAP Message Types

| Type | Description |
|------|-------------|
| CON | Confirmable — requires ACK, auto-retransmit |
| NON | Non-confirmable — fire-and-forget |
| ACK | Acknowledgement |
| RST | Reset |

---

## 🔌 Plugin System

Plugins intercept session lifecycle events with priority-ordered execution (lower numbers first).

### Plugin Interface

```go
type Plugin interface {
    Name()     string
    Priority() int
    OnAccept(sess Session[[]byte]) error
    OnMessage(sess Session[[]byte], msg Message[[]byte]) error
    OnClose(sess Session[[]byte]) error
}
```

### Flow Control

Return special errors to control chain execution:

| Error | Effect |
|-------|--------|
| `ErrSkip` | Skip remaining plugins, continue normal processing |
| `ErrDrop` | Drop the message silently |
| `ErrBlock` | Close the connection |

### Built-in Plugins

| Plugin | Priority | Purpose |
|--------|----------|---------|
| `BlacklistPlugin` | 0 | IP/CIDR blocking with configurable TTL |
| `RateLimitPlugin` | 10 | Dual-layer token bucket (global + per-IP) |
| `AutoBanPlugin` | 20 | Auto-ban on threshold violations |
| `HeartbeatPlugin` | 30 | TimeWheel-based idle timeout |
| `ClusterPlugin` | 40 | Cross-node session routing via PubSub |
| `PersistencePlugin` | 50 | Async batch writes with circuit breaker |
| `SlowQueryPlugin` | -10 | Slow request logging (default 100ms threshold) |

All plugin hooks are protected against **panics** — plugin crashes won't affect the main flow.

---

## 🗄️ Infrastructure

### 6-Level BufferPool

Zero-GC buffer allocation via `sync.Pool`. Automatic level selection based on request size.

| Level | Name | Size | Use Case |
|-------|------|------|----------|
| 0 | Micro | 512B | Small control messages |
| 1 | Tiny | 2KB | CoAP, small payloads |
| 2 | Small | 4KB | Typical messages |
| 3 | Medium | 32KB | Large messages |
| 4 | Large | 256KB | Bulk transfers |
| 5 | Huge | >256KB | Direct allocation |

Pooled vs direct allocation: ~10x faster for Micro, ~20x faster for Small.

### Circuit Breaker

Three-state protection: Closed → Open → HalfOpen.

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
// Key-Value store
store := api.NewMemoryStore()
store.Save(ctx, "key", []byte("value"))
result, _ := store.Load(ctx, "key")

// TTL cache
cache := api.NewMemoryCache()
cache.Set(ctx, "session:123", data, 5*time.Minute)

// Pub/Sub
ps := api.NewChannelPubSub()
ch := ps.Subscribe(ctx, "events")
ps.Publish(ctx, "events", []byte("hello"))
ps.Close()
```

### Prometheus Metrics

The framework automatically collects the following metrics (Prometheus format):

| Metric | Description |
|--------|-------------|
| `shark_connections_total` | Total connections |
| `shark_connections_active` | Active connections |
| `shark_messages_total` | Total messages |
| `shark_message_bytes` | Message bytes distribution |
| `shark_message_duration_seconds` | Message processing latency |
| `shark_errors_total` | Total errors (by protocol) |
| `shark_session_lru_evictions_total` | LRU eviction count |
| `shark_bufferpool_hits_total` | BufferPool hit count |
| `shark_plugin_duration_seconds` | Plugin execution latency |
| `shark_worker_panics_total` | Worker panic count |

---

## 📂 Project Structure

```
shark-socket/
├── api/                    # Public API — type aliases, factory functions
├── internal/
│   ├── gateway/            # Multi-protocol gateway (6-stage shutdown)
│   ├── protocol/           # TCP, UDP, HTTP, WebSocket, CoAP implementations
│   ├── session/            # BaseSession, sharded Manager, LRU eviction
│   ├── plugin/             # Plugin chain + 6 built-in plugins
│   ├── infra/              # Infrastructure
│   │   ├── bufferpool/     # 6-level sync.Pool buffer pool
│   │   ├── cache/          # In-memory TTL cache
│   │   ├── circuitbreaker/ # Circuit breaker
│   │   ├── logger/         # Structured logger (slog)
│   │   ├── metrics/        # Prometheus integration
│   │   ├── pubsub/         # Channel-based pub/sub
│   │   ├── store/          # Key-value store
│   │   └── tracing/        # Minimal tracing interface
│   ├── defense/            # Overload protection, backpressure, log sampling
│   ├── types/              # Enums, Message[T], Session[M], Plugin interface
│   ├── errs/               # Error taxonomy
│   └── utils/              # ShardedMap[K,V], AtomicBool, IP parsing
├── examples/               # Runnable example code
├── tests/
│   ├── unit/               # Cross-package unit tests
│   ├── integration/        # Multi-protocol system integration tests
│   └── benchmark/          # Performance benchmarks
├── k8s/                    # Kubernetes deployment manifests
├── scripts/                # Build, test, and log scripts
└── docs/                   # Architecture documentation
```

---

## 🧪 Testing

### Test Scale

- **627 test functions** (620 Test + 7 Fuzz), **55 benchmarks**
- **75 test files** across **37 packages**
- All passing, zero failures
- Comprehensive fuzz testing: all Framer and CoAP parser coverage

### Running Tests

```bash
# All tests
go test ./... -v

# With coverage
go test ./... -cover

# Benchmarks
go test -bench=. -benchmem -run=^$ ./...

# Race detector
go test -race ./...

# Specific package
go test ./internal/protocol/tcp/... -v
```

### Automated Test Scripts (recommended)

Three cross-platform test scripts with identical functionality:

| Script | Platform | Usage |
|--------|----------|-------|
| `scripts/run_tests.go` | **All platforms** | `go run scripts/run_tests.go` |
| `scripts/run_tests.sh` | Linux / macOS / Git Bash | `bash scripts/run_tests.sh` |
| `scripts/run_tests.bat` | Windows CMD | `scripts\run_tests.bat` |

Supported modes: `--all` (default), `--unit`, `--integration`, `--benchmark`, `--cover`.

### Test Coverage

| Package | Coverage |
|---------|----------|
| errs | 100.0% |
| infra/cache | 100.0% |
| infra/store | 100.0% |
| infra/tracing | 100.0% |
| infra/pubsub | 96.1% |
| defense | 95.1% |
| api | 92.6% |
| types | 92.3% |
| session | 87.8% |
| infra/bufferpool | 86.8% |
| protocol/tcp | 85.4% |
| utils | 84.7% |
| protocol/websocket | 80.2% |
| infra/circuitbreaker | 79.4% |
| protocol/udp | 78.4% |
| protocol/coap | 75.2% |
| protocol/http | 75.2% |
| infra/logger | 73.9% |
| plugin | 62.7% |
| infra/metrics | 53.3% |
| gateway | 52.1% |

Test methodology: Server readiness is verified via polling (`waitForTCPServer` / `waitForUDPServer`) instead of fixed sleeps, ensuring stability across CI and local environments.

---

## 📊 Performance Report

Benchmark results on AMD Ryzen 7 8845HS (Windows 11, Go 1.26.1):

### Performance Highlights

| Metric | Value | Description |
|--------|-------|-------------|
| **TCP parallel throughput** | ~166K msg/s | Multi-connection parallel |
| **TCP single-conn throughput** | ~31.5K msg/s | Single connection echo |
| **TCP large msg throughput** | 139.8 MB/s | Large frame transfer |
| **UDP echo throughput** | ~68K msg/s | Datagram echo |
| **CoAP parse** | 119.5 ns/op | Message deserialization |
| **Session register** | 144.5 ns/op | 32-shard concurrent safe |
| **Session get** | 7.7 ns/op | Sharded Map lookup |
| **BufferPool Get+Put** | 12.5 ns/op | Zero allocation |
| **Plugin chain overhead** | ~1.9 ns/hop | 5 plugins |
| **Concurrent connections** | 100K | Tested |

### TCP Protocol

| Benchmark | ns/op | B/op | allocs/op | Throughput |
|-----------|-------|------|-----------|------------|
| TCPEcho | 31,703 | 4,937 | 14 | ~31.5K msg/s |
| TCPEcho_SmallMessage | 40,374 | 4,850 | 14 | ~24.8K msg/s |
| TCPEcho_LargeMessage | 58,582 | 72,531 | 14 | 139.8 MB/s |
| TCPEcho_Parallel | 6,030 | 4,930 | 14 | ~166K msg/s |

### Session Manager

| Benchmark | ns/op | B/op | allocs/op |
|-----------|-------|------|-----------|
| SessionRegister | 144.5 | 400 | 8 |
| ManagerGet | 7.7 | 0 | 0 |
| ManagerNextID | 1.6 | 0 | 0 |
| ManagerNextIDParallel | 9.9 | 0 | 0 |
| ManagerCount | 0.4 | 0 | 0 |

### BufferPool

| Benchmark | ns/op | B/op | allocs/op |
|-----------|-------|------|-----------|
| Pool/Micro_64 | 12.5 | 0 | 0 |
| Pool/Tiny_1024 | 16.1 | 0 | 0 |
| Pool/Small_4096 | 44.0 | 0 | 0 |
| Pool/Medium_16384 | 129.9 | 0 | 0 |
| Pool/Large_131072 | 909.2 | 0 | 0 |
| Parallel/Micro_64 | 11.5 | 0 | 0 |
| DirectAlloc/Micro_64 | 24.0 | 64 | 1 |
| DirectAlloc/Large_131072 | 13,817 | 131,072 | 1 |

### Plugin Chain

| Benchmark | ns/op | B/op | allocs/op |
|-----------|-------|------|-----------|
| Chain_Empty | 1.5 | 0 | 0 |
| Chain_5Plugins | 9.6 | 0 | 0 |
| Chain_10Plugins | 17.2 | 0 | 0 |
| Chain_OnAccept_5 | 8.1 | 0 | 0 |
| Chain_OnClose_5 | 7.0 | 0 | 0 |
| Chain_Parallel | 1.3 | 0 | 0 |

---

## 🐳 Deployment

### Docker

```bash
# Build image
docker build -t shark-socket .

# Docker Compose (includes Prometheus)
docker-compose up -d

# Verify
curl http://localhost:18400/health
curl http://localhost:9091/metrics
```

**Exposed ports:**

| Port | Protocol | Service |
|------|----------|---------|
| 18000 | TCP | TCP echo |
| 18200 | UDP | UDP echo |
| 18400 | TCP | HTTP API |
| 18600 | TCP | WebSocket |
| 18650 | TCP | gRPC-Web |
| 18800 | UDP | CoAP |
| 18900 | UDP | QUIC |
| 9091 | TCP | Metrics / Health |

### Kubernetes

Complete Kubernetes manifests are provided in `k8s/` for production deployment.

```bash
# One-command deployment
kubectl apply -k k8s/
```

#### K8s Resources

| Resource | File | Description |
|----------|------|-------------|
| Namespace | `namespace.yaml` | Dedicated `shark-socket` namespace |
| Deployment | `deployment.yaml` | 2 replicas, anti-affinity, health checks, preStop |
| Service | `service.yaml` | ClusterIP per protocol + NodePort template |
| HPA + PDB | `hpa.yaml` | CPU 50%/Memory 70%, 2-10 replicas, minAvailable: 1 |
| NetworkPolicy | `networkpolicy.yaml` | Intra-namespace + Ingress + monitoring whitelist |
| Prometheus | `prometheus/` | Deployment + Service + ConfigMap |
| Ingress | `ingress.yaml` | HTTP + WebSocket routes (nginx) |
| ConfigMap | `configmap.yaml` | Prometheus scrape configuration |

#### Health Checks

| Probe | Endpoint | Port | Purpose |
|-------|----------|------|---------|
| Liveness | `/healthz` | 9091 | Restart unhealthy pods |
| Readiness | `/readyz` | 9091 | Remove from service rotation |

#### Graceful Shutdown

Kubernetes lifecycle is configured for zero-downtime rolling updates:

1. `preStop: sleep 5` — wait for load balancer to remove pod
2. SIGTERM triggers Gateway's 6-stage shutdown (15s timeout)
3. `terminationGracePeriodSeconds: 30` — hard kill after 30s

#### Verify Deployment

```bash
# Check resources
kubectl get all -n shark-socket

# Port-forward for local testing
kubectl port-forward -n shark-socket svc/shark-socket-http 18400:18400
curl http://localhost:18400/hello

# Check logs
kubectl logs -n shark-socket -l app.kubernetes.io/name=shark-socket --tail=50

# Check metrics
kubectl port-forward -n shark-socket svc/shark-socket-metrics 9091:9091
curl http://localhost:9091/metrics
```

---

## 📖 Documentation

- [Architecture Design Document](docs/shark-socket%20ARCHITECTURE.md) — Full design decisions and implementation details
- [Test Coverage Document](docs/TEST_COVERAGE.md) — All 627 tests detailed
- [Code Review Report](docs/REVIEW_PHASE3.md) — 3-round expert review, 25/25 issues resolved
- [API Documentation](https://pkg.go.dev/github.com/X1aSheng/shark-socket)
- [Example Code](examples/) — 9 runnable examples
- [中文文档](./README.md)

---

## 📈 Project Statistics

| Metric | Value |
|--------|-------|
| Go Lines of Code | 32,000+ |
| Go Files | 176 |
| Packages | 37 |
| Test Functions | 627 (620 Test + 7 Fuzz) |
| Benchmarks | 55 |
| Test Files | 75 |
| Examples | 9 |
| External Dependencies | 3 (prometheus/client_golang, quic-go, gorilla/websocket) |
| Code Review | 25/25 complete (3-round expert review) |

---

## 🤝 Contributing

We welcome Issues and Pull Requests!

1. Fork this repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Submit a Pull Request

---

## 📄 License

This project is licensed under the [MIT License](LICENSE).
