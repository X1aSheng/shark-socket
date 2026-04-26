# Shark-Socket

High-performance, extensible multi-protocol networking framework in Go (>= 1.26).

Supports **TCP, TLS, UDP, HTTP, WebSocket, CoAP** with a unified API, shared session management, and a plugin system.

---

## Table of Contents

- [Quick Start](#quick-start)
- [Features](#features)
- [Architecture](#architecture)
- [Protocols](#protocols)
- [Plugin System](#plugin-system)
- [Infrastructure](#infrastructure)
- [Examples](#examples)
- [Testing](#testing)
  - [Run Tests](#run-tests)
  - [Test Coverage](#test-coverage)
  - [Benchmarks](#benchmarks)
  - [Test Logging](#test-logging)
- [Deployment](#deployment)
  - [Docker](#docker)
  - [Kubernetes](#kubernetes)
- [CI/CD](#cicd)
- [Performance Summary](#performance-summary)
- [License](#license)

---

## Quick Start

### Single Protocol

```go
package main

import (
    "log"
    "github.com/X1aSheng/shark-socket/internal/protocol/tcp"
    "github.com/X1aSheng/shark-socket/internal/types"
)

func main() {
    handler := func(sess types.RawSession, msg types.RawMessage) error {
        return sess.Send(msg.Payload)
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
        return sess.Send(msg.Payload) // echo
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

---

## Features

- **Multi-protocol**: TCP, TLS, UDP, HTTP, WebSocket, CoAP with unified handler interface
- **Generic sessions**: `Session[M]` with type-safe `SendTyped` and universal `Send([]byte)`
- **Sharded SessionManager**: 32-shard locking with LRU eviction, up to 1M sessions
- **Plugin system**: Priority-ordered chain with `ErrSkip`/`ErrDrop`/`ErrBlock` flow control
- **6-level BufferPool**: Micro(512B)/Tiny(2KB)/Small(4KB)/Medium(32KB)/Large(256KB)/Huge — zero-GC for pooled allocations
- **Built-in plugins**: Blacklist, RateLimit, Heartbeat, AutoBan, Persistence, Cluster
- **Gateway**: Multi-protocol orchestration with shared SessionManager and 6-stage graceful shutdown
- **Defense**: Overload protection, backpressure, log sampling
- **Observability**: Prometheus metrics, structured logging (slog), health/ready endpoints
- **Production-ready**: Docker, docker-compose, Kubernetes manifests (HPA, PDB, NetworkPolicy, Ingress)

---

## Architecture

```
shark-socket/
├── api/                    Public API — type aliases, factory functions
├── internal/
│   ├── gateway/            Multi-protocol orchestration (6-stage shutdown)
│   ├── protocol/           TCP, UDP, HTTP, WebSocket, CoAP implementations
│   ├── session/            BaseSession, sharded Manager, LRU eviction
│   ├── plugin/             Chain, Blacklist, RateLimit, Heartbeat, AutoBan, Persistence, Cluster
│   ├── infra/
│   │   ├── bufferpool/     6-level sync.Pool-based buffer pool
│   │   ├── cache/          In-memory TTL cache
│   │   ├── circuitbreaker/ Circuit breaker (Closed/Open/HalfOpen)
│   │   ├── logger/         Structured logger (slog backend)
│   │   ├── metrics/        Prometheus integration
│   │   ├── pubsub/         Channel-based pub/sub
│   │   ├── store/          Key-value store with prefix queries
│   │   └── tracing/        Minimal tracing interface
│   ├── defense/            Overload protector, backpressure, log sampler
│   ├── types/              Enums, Message[T], Session[M], Plugin interface
│   ├── errs/               Error taxonomy with classification helpers
│   └── utils/              ShardedMap[K,V], AtomicBool, IP parsing
├── examples/               Working code examples
├── tests/
│   ├── unit/               Cross-package unit tests
│   ├── integration/        Multi-protocol system tests
│   └── benchmark/          Performance benchmarks
├── k8s/                    Kubernetes deployment manifests
├── scripts/                Build, test, and utility scripts
└── docs/                   Architecture documentation
```

---

## Protocols

| Protocol | Default Port | Features |
|----------|-------------|----------|
| TCP | 18000 | 4 framer types, WorkerPool (4 policies), writeQueue, drain, TLS |
| UDP | 18200 | Pseudo-sessions by address, sweep TTL |
| HTTP | 18400 | Mode A (thin wrapper) + Mode B (session + plugins) |
| WebSocket | 18600 | Ping/Pong keepalive, origin check, gorilla/websocket |
| CoAP | 18800 | CON retransmit, ACK, MessageID dedup (RFC 7252) |

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

## Plugin System

Plugins intercept session lifecycle events with priority-ordered execution. Lower priority numbers run first.

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

### Example: Plugin Chain

```go
import "github.com/X1aSheng/shark-socket/api"

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

## Infrastructure

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

Pool vs direct allocation: ~10x faster for Micro, ~20x faster for Small.

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

### Store, Cache, PubSub

```go
store := api.NewMemoryStore()
store.Save(ctx, "key", []byte("value"))
result, _ := store.Load(ctx, "key")

cache := api.NewMemoryCache()
cache.Set(ctx, "session:123", data, 5*time.Minute)

ps := api.NewChannelPubSub()
ch := ps.Subscribe(ctx, "events")
ps.Publish(ctx, "events", []byte("hello"))
ps.Close()
```

### Prometheus Metrics

Built-in metrics exposed at `/metrics`:

```
shark_connections_total         shark_connection_errors_total
shark_messages_total            shark_errors_total
shark_worker_panics_total       sh shark_session_lru_evictions_total
shark_rejected_connections_total  shark_write_queue_full_total
shark_autoban_total             shark_bufferpool_hits_total
shark_connections_active        shark_worker_queue_depth
shark_message_bytes             shark_message_duration_seconds
shark_plugin_duration_seconds
```

---

## Examples

| Example | Description | Run |
|---------|-------------|-----|
| basic_tcp | TCP echo server | `go run examples/basic_tcp/main.go` |
| basic_udp | UDP echo server | `go run examples/basic_udp/main.go` |
| basic_http | HTTP server with routes | `go run examples/basic_http/main.go` |
| basic_websocket | WebSocket echo server | `go run examples/basic_websocket/main.go` |
| basic_coap | CoAP echo server | `go run examples/basic_coap/main.go` |
| multi_protocol | Gateway with all 5 protocols | `go run examples/multi_protocol/main.go` |
| session_plugins | TCP with plugin chain | `go run examples/session_plugins/main.go` |
| graceful_shutdown | Graceful shutdown demo | `go run examples/graceful_shutdown/main.go` |

### Testing with Clients

```bash
# TCP
nc localhost 18000

# UDP
nc -u localhost 18200

# HTTP
curl http://localhost:18400/hello

# WebSocket
websocat ws://localhost:18600/ws

# Metrics
curl http://localhost:9091/metrics

# Health
curl http://localhost:9091/healthz
```

---

## Testing

### Run Tests

```bash
# All tests (600+ test cases across 25 packages, all pass)
go test ./... -v

# With coverage
go test ./... -cover

# Benchmarks (49 benchmarks across 5 packages)
go test -bench=. -benchmem -run=^$ ./...

# Race detector
go test -race ./...

# Specific package
go test ./internal/protocol/tcp/... -v
```

### Test Coverage

22 internal packages + 3 test packages, 600+ test cases total:

| Package | Coverage |
|---------|----------|
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

Test methodology: Server readiness is verified via polling (`waitForTCPServer` / `waitForUDPServer`) instead of fixed sleeps, ensuring stability across CI and local environments.

### Benchmarks

Benchmark results on AMD Ryzen 7 8845HS (Windows 11, Go 1.26.1):

#### TCP Protocol

| Benchmark | ns/op | B/op | allocs/op | Throughput |
|-----------|-------|------|-----------|------------|
| TCPEcho | 31,703 | 4,937 | 14 | ~31.5K msg/s |
| TCPEcho_SmallMessage | 40,374 | 4,850 | 14 | ~24.8K msg/s |
| TCPEcho_LargeMessage | 58,582 | 72,531 | 14 | 139.8 MB/s |
| TCPEcho_Parallel | 6,030 | 4,930 | 14 | ~166K msg/s |

#### UDP Protocol

| Benchmark | ns/op | B/op | allocs/op |
|-----------|-------|------|-----------|
| UDPEcho | 14,651 | 160 | 8 |

#### CoAP Protocol

| Benchmark | ns/op | B/op | allocs/op |
|-----------|-------|------|-----------|
| CoAP_ParseMessage | 119.5 | 106 | 3 |
| CoAP_Serialize | 47.8 | 32 | 1 |

#### Session Manager

| Benchmark | ns/op | B/op | allocs/op |
|-----------|-------|------|-----------|
| SessionRegister | 144.5 | 400 | 8 |
| ManagerGet | 7.7 | 0 | 0 |
| ManagerNextID | 1.6 | 0 | 0 |
| ManagerNextIDParallel | 9.9 | 0 | 0 |
| ManagerCount | 0.4 | 0 | 0 |

#### BufferPool

| Benchmark | ns/op | B/op | allocs/op |
|-----------|-------|------|-----------|
| Pool/Micro_64 | 12.5 | 0 | 0 |
| Pool/Tiny_1024 | 16.1 | 0 | 0 |
| Pool/Small_4096 | 44.0 | 0 | 0 |
| Pool/Medium_16384 | 129.9 | 0 | 0 |
| Pool/Large_131072 | 909.2 | 0 | 0 |
| Parallel/Micro_64 | 11.5 | 0 | 0 |
| Parallel/Small_4096 | 11.0 | 0 | 0 |
| Parallel/Large_131072 | 126.9 | 0 | 0 |
| GetLevel | 0.3 | 0 | 0 |
| DefaultSingleton | 20.1 | 0 | 0 |
| DirectAlloc/Micro_64 | 24.0 | 64 | 1 |
| DirectAlloc/Large_131072 | 13,817 | 131,072 | 1 |

#### Plugin Chain

| Benchmark | ns/op | B/op | allocs/op |
|-----------|-------|------|-----------|
| Chain_Empty | 1.5 | 0 | 0 |
| Chain_5Plugins | 9.6 | 0 | 0 |
| Chain_10Plugins | 17.2 | 0 | 0 |
| Chain_OnAccept_5 | 8.1 | 0 | 0 |
| Chain_OnClose_5 | 7.0 | 0 | 0 |
| Chain_Parallel | 1.3 | 0 | 0 |

### Test Logging

All test runs are automatically logged with structured output:

```bash
# Run with automatic log recording (JSON + readable text)
bash scripts/run_tests.sh              # all tests
bash scripts/run_tests.sh --unit       # unit tests only
bash scripts/run_tests.sh --integration # integration tests only
bash scripts/run_tests.sh --benchmark  # benchmarks only
```

Logs are saved to `logs/` with timestamped files:
```
logs/
├── 2026-0426_121931_unit.json          # Raw JSON (go test -json)
├── 2026-0426_121931_unit.log           # Readable report
├── 2026-0426_121931_integration.json
├── 2026-0426_121931_integration.log
├── 2026-0426_121931_benchmark.json
└── 2026-0426_121931_benchmark.log
```

---

## Deployment

### Docker

```bash
# Build
docker build -t shark-socket .

# Run with docker-compose (includes Prometheus)
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
| 18800 | UDP | CoAP |
| 9091 | TCP | Metrics / Health |

### Kubernetes

Complete Kubernetes manifests are provided in `k8s/` for production deployment.

```bash
# Deploy everything (one command)
kubectl apply -k k8s/

# Or apply individually
kubectl apply -f k8s/namespace.yaml
kubectl apply -f k8s/configmap.yaml
kubectl apply -f k8s/deployment.yaml
kubectl apply -f k8s/service.yaml
kubectl apply -f k8s/hpa.yaml
kubectl apply -f k8s/networkpolicy.yaml
kubectl apply -f k8s/ingress.yaml
```

#### K8s Resources

| Resource | File | Description |
|----------|------|-------------|
| Namespace | `namespace.yaml` | Dedicated `shark-socket` namespace |
| Deployment | `deployment.yaml` | 2 replicas, anti-affinity, health checks, preStop hook |
| Services | `service.yaml` | ClusterIP for each protocol + metrics; NodePort template (commented) |
| HPA | `hpa.yaml` | CPU 50% / Memory 70%, scale 2–10 replicas |
| PDB | `hpa.yaml` | minAvailable: 1 (in same file) |
| NetworkPolicy | `networkpolicy.yaml` | Intra-namespace, ingress-nginx, monitoring whitelist + DNS egress |
| Prometheus | `prometheus/` | Deployment + Service with ConfigMap-based scrape config |
| Ingress | `ingress.yaml` | HTTP + WebSocket routes via nginx ingress |
| ConfigMap | `configmap.yaml` | Prometheus scrape configuration |

#### Health Checks

The deployment uses the built-in health endpoints:

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

## CI/CD

GitHub Actions CI pipeline (`.github/workflows/ci.yml`):

| Step | Description |
|------|-------------|
| Verify | `go mod verify` |
| Build | `go build ./...` |
| Vet | `go vet ./...` |
| Test | `go test ./... -count=1 -timeout 120s` |
| Race | `go test -race ./... -count=1 -timeout 180s` |
| Bench | `go test -bench=. -benchmem -run=^$ ./...` |
| Lint | golangci-lint (latest) |

---

## Performance Summary

| Metric | Result |
|--------|--------|
| TCP parallel throughput | ~166K msg/s |
| TCP single-conn throughput | ~31.5K msg/s |
| TCP large message throughput | 139.8 MB/s |
| UDP echo throughput | ~68K msg/s |
| CoAP parse | 119.5 ns/op |
| Plugin chain overhead | ~1.9 ns/hop (5 plugins) |
| BufferPool Get+Put (Micro) | 12.5 ns/op, 0 alloc |
| BufferPool vs DirectAlloc | 10–15x faster (pooled) |
| Session register | 144.5 ns/op |
| Session get | 7.7 ns/op |
| Session next ID (parallel) | 9.9 ns/op |
| Concurrent connections | tested up to 100K |

---

## License

MIT
