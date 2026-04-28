# Shark-Socket Expert Skill

## Overview
Shark-Socket is a high-performance multi-protocol socket server/gateway supporting **TCP, TLS, UDP, HTTP, WebSocket, CoAP, QUIC, and gRPC-Web** with a unified plugin chain architecture.

**Module**: `github.com/X1aSheng/shark-socket`
**Go Version**: 1.26

## Architecture
Layered architecture with clear separation of concerns:

```
api/                         → Unified public facade (Gateway + Options)
internal/
  gateway/                   → Multi-protocol server orchestration, lifecycle
  session/                   → Sharded session manager (32 shards), LRU eviction
  plugin/                    → Plugin chain: Blacklist, RateLimit, AutoBan,
                               Heartbeat, Cluster, Persistence, SlowQuery
  protocol/                  → Protocol implementations:
    tcp/                     → TCP + TLS with worker pool
    udp/                     → UDP with session tracking
    http/                    → HTTP/1.1 + H2C with Mode A/B handlers
    websocket/               → WebSocket upgrade + frame handling
    coap/                    → CoAP (RFC 7252) with CON/ACK tracking, dedup
    quic/                    → QUIC transport
    grpcgw/                  → gRPC-Web gateway
  infra/                     → Infrastructure:
    cache/                   → TTL cache
    store/                   → Pluggable storage backends
    pubsub/                  → Channel-based PubSub with fan-out
    circuitbreaker/          → Circuit breaker pattern
    bufferpool/              → 6-tier sync.Pool buffer pool
    tracing/                 → OpenTelemetry with W3C propagation
    logger/                  → slog-based structured logging
    metrics/                 → Prometheus metrics collectors
    config/                  → YAML/ENV configuration
    ratelimit/               → Connection rate limiter
  defense/                   → Backpressure, overload protection, sampler
  types/                     → Core interfaces (Session, Plugin, Message, Handler)
  errs/                      → Sentinel error definitions
  utils/                     → Atomic, sync, and network utilities
cmd/shark-socket/            → CLI entry point
tests/
  unit/                      → Unit test suites
  integration/               → Integration tests (data comm, multi-protocol, deploy, k8s, helm)
  benchmark/                 → Protocol and data communication benchmarks
scripts/                     → Cross-platform test runner (run_tests.go)
examples/                    → Example applications per protocol
deploy/
  docker/                    → Dockerfile + docker-compose.yml (with Prometheus)
  k8s/                       → Kubernetes manifests + Helm chart
```

## Key Design Principles
- **P1**: Plugin chain architecture with priority-based ordering
- **P2**: Sharded session manager for concurrent session access (32 shards, LRU eviction)
- **P3**: Protocol abstraction via `types.RawSession` interface
- **P4**: 6-stage graceful shutdown (StopAccept→Drain→SessionClose→ManagerClose→MetricsClose→Finalize)
- **P5**: Observability first (OpenTelemetry tracing, Prometheus metrics, slog logging)

## Protocol Implementations

### TCP (`internal/protocol/tcp/`)
- Worker pool for connection handling
- TLS reloader with hot certificate rotation
- Custom framer for message boundary detection
- Echo server for testing

### UDP (`internal/protocol/udp/`)
- Session tracking via remote address
- Per-session read buffers
- Connectionless message dispatch

### HTTP (`internal/protocol/http/`)
- Mode A: Request-response handler
- Mode B: Session-aware handler with upgrade support
- H2C (HTTP/2 cleartext) support
- Access logging middleware

### WebSocket (`internal/protocol/websocket/`)
- Upgrade from HTTP with protocol negotiation
- Text and binary frame support
- Per-session message routing

### CoAP (`internal/protocol/coap/`)
- RFC 7252 compliant
- CON/ACK reliable messaging with retry
- Message deduplication via atomic CheckAndRecord
- Options encoding/decoding

### QUIC (`internal/protocol/quic/`)
- QUIC transport protocol support
- Session management with stream multiplexing

### gRPC-Web (`internal/protocol/grpcgw/`)
- gRPC-Web protocol gateway
- Content-type negotiation

## Plugin System
Priority-ordered plugin chain with three hooks:

| Priority | Plugin | OnAccept | OnMessage | OnClose | Description |
|----------|--------|----------|-----------|---------|-------------|
| 0 | Blacklist | Block banned IPs | - | - | IP/network blacklist with TTL |
| 10 | RateLimit | Token bucket check | Token bucket check | - | Dual-layer rate limiting (global + per-IP) |
| 20 | AutoBan | Track violations | Track violations | - | Auto-ban IPs exceeding thresholds |
| 30 | Heartbeat | Add to time wheel | Reset timeout | Remove | Session heartbeat with time wheel |
| 40 | Cluster | - | - | - | Placeholder for cluster sync |
| 50 | Persistence | Load session | Queue write | Flush | Persistent session storage |

### Time Wheel (`internal/plugin/heartbeat.go`)
- Configurable tick duration and slot count
- O(1) Add, Remove, Reset operations
- Timeout callback on expiry

### Token Bucket (`internal/plugin/ratelimit.go`)
- Lock-free token consumption via CAS loop
- Mutex-protected refill with burst cap
- Per-IP bucket creation via `sync.Map.LoadOrStore`
- TTL-based cleanup goroutine for stale entries

## Infrastructure

### BufferPool (`internal/infra/bufferpool/`)
- 6 size tiers: 64B, 256B, 1KB, 4KB, 16KB, 64KB
- `sync.Pool` per tier for zero-allocation hot paths

### CircuitBreaker (`internal/infra/circuitbreaker/`)
- Three states: Closed → Open → HalfOpen → Closed
- Configurable failure threshold, timeout, half-open max requests
- Used by PersistencePlugin for store calls

### PubSub (`internal/infra/pubsub/`)
- Channel-based publish-subscribe with fan-out goroutines
- Topic subscription with cleanup on unsubscribe
- Non-blocking send with overflow protection

### Tracing (`internal/infra/tracing/`)
- OpenTelemetry SDK with configurable exporters
- W3C TraceContext + Baggage propagation
- `LogExporter` for development/debugging
- Span attribute injection via functional options

## External Dependencies
```
go.opentelemetry.io/otel            → Distributed tracing SDK
github.com/prometheus/client_golang → Prometheus metrics
github.com/quic-go/quic-go         → QUIC protocol implementation
gopkg.in/yaml.v3                   → Config YAML loading
```

## Testing Strategy
- **Unit tests**: Per package (`internal/...`, `api/`, `tests/unit/`)
- **Integration tests**: `tests/integration/` (data_comm, multi_protocol, deploy, k8s_manifest, helm)
- **Protocol integration**: Per-protocol `integration_test.go` files
- **Benchmark tests**: `tests/benchmark/`, per-package `bench_test.go` files
- **Fuzz tests**: `internal/protocol/tcp/framer_fuzz_test.go`, `internal/protocol/coap/message_fuzz_test.go`
- **Cross-platform runner**: `scripts/run_tests.go` (unit, integration, benchmark, cover modes)
- **CI**: GitHub Actions with multi-OS/multi-Go matrix, race detector, coverage, Docker smoke test

## Current Limitations

### Protocol
- QUIC 0-RTT early data not supported
- gRPC-Web server streaming not fully implemented
- CoAP Block-wise transfer not supported
- WebSocket compression extension not implemented

### Advanced Features
- Cluster plugin is placeholder (no multi-node sync)
- No dynamic plugin reloading
- No hot configuration reload
- No admin API for runtime management

## API Usage Pattern

```go
// Standalone gateway
gw := api.NewGateway(
    api.WithTCP(":18000"),
    api.WithHTTP(":18400"),
    api.WithWebSocket(":18600"),
    api.WithCoAP(":18800"),
    api.WithMetrics(":9091"),
)
gw.Start()
```
