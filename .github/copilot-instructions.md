# Shark-Socket Development Skill Guide

## Project Overview
Shark-Socket is a high-performance multi-protocol socket server/gateway written in Go. It supports TCP, TLS, UDP, HTTP, WebSocket, CoAP, QUIC, and gRPC-Web protocols with a unified plugin chain, sharded session management, and distributed tracing.

## Architecture Layers
The project follows strict separation of concerns:

| Layer | Package | Responsibility |
|-------|---------|----------------|
| **API** | `api/` | Unified public interface — all exports in one package |
| **Gateway** | `internal/gateway/` | Multi-protocol server orchestration, lifecycle management |
| **Session** | `internal/session/` | Sharded session manager (32 shards), LRU eviction, session lifecycle |
| **Plugin** | `internal/plugin/` | Plugin chain with priority ordering (Blacklist→AutoBan→RateLimit→Heartbeat→Cluster→Persistence) |
| **Protocol** | `internal/protocol/` | Protocol implementations: TCP, TLS, UDP, HTTP, WebSocket, CoAP, QUIC, gRPC-Web |
| **Config** | `internal/infra/config/` | YAML/ENV-based configuration loading |
| **Infra** | `internal/infra/` | Cross-cutting: Cache, Store, PubSub, CircuitBreaker, BufferPool, Tracing, Logger, Metrics |
| **Defense** | `internal/defense/` | Backpressure, overload protection, adaptive sampling |
| **Types** | `internal/types/` | Core interfaces: Session, Message, Plugin, Handler |
| **Errors** | `internal/errs/` | Centralized sentinel error definitions |
| **Utils** | `internal/utils/` | Atomic operations, sync primitives, network helpers |

## Key Design Principles

### P1 — Plugin Chain Architecture
- Plugins execute in priority order: Blacklist(0) → AutoBan(20) → RateLimit(10) → Heartbeat(30) → Cluster(40) → Persistence(50)
- Each plugin implements `OnAccept`, `OnMessage`, `OnClose` hooks
- `OnAccept` can reject connections via sentinel errors (`ErrBlock`)
- `OnMessage` can transform or drop messages via returned data/error

### P2 — Sharded Session Manager
- 32-shard design with per-shard `sync.RWMutex` for minimal contention
- LRU eviction per shard with configurable max capacity
- Atomic ID generation with shard-aware routing
- `All()` returns `iter.Seq` for Go 1.23+ range-over-func iteration

### P3 — Protocol Abstraction
- All protocols implement `types.RawSession` interface
- Protocol-specific servers register with the Gateway
- Unified connection lifecycle: Accept → Read/Write → Close
- CoAP: CON/ACK tracking with dedup cache, message ID atomic check-and-record

### P4 — Graceful Shutdown
- 6-stage lifecycle: StopAccept → Drain → SessionClose → ManagerClose → MetricsClose → Finalize
- Configurable shutdown timeout per stage
- Circuit breaker protects downstream calls during degradation

### P5 — Observability First
- OpenTelemetry distributed tracing with W3C propagation
- Prometheus metrics with structured collectors
- Structured logging via `log/slog`
- Health check endpoint (`/healthz`) and metrics endpoint (`/metrics`)

## Key Interfaces

### Session (`internal/types/session.go`)
```go
type RawSession interface {
    ID() uint64
    Protocol() Protocol
    RemoteAddr() net.Addr
    LocalAddr() net.Addr
    IsAlive() bool
    Send(data []byte) error
    Close() error
}
```

### Plugin (`internal/types/plugin.go`)
```go
type Plugin interface {
    Name() string
    Priority() int
    OnAccept(sess RawSession) error
    OnMessage(sess RawSession, data []byte) ([]byte, error)
    OnClose(sess RawSession)
}
```

### SessionManager (`internal/types/session.go`)
```go
type SessionManager interface {
    Register(RawSession) error
    Unregister(uint64)
    Get(uint64) (RawSession, bool)
    Count() int64
    All() iter.Seq[RawSession]
    Range(func(RawSession) bool)
    Broadcast([]byte)
    Close() error
}
```

### Tracer (`internal/infra/tracing/tracing.go`)
```go
type Tracer interface {
    StartSpan(ctx context.Context, name string, opts ...SpanOption) (Span, context.Context)
}
```

### CircuitBreaker (`internal/infra/circuitbreaker/circuitbreaker.go`)
```go
type CircuitBreaker interface {
    Do(fn func() error) error
    State() State  // Closed, Open, HalfOpen
}
```

## Coding Standards
- Go 1.23+, standard library conventions
- Functional options pattern for configuration: `WithXxx()`
- Table-driven tests for unit tests
- Sentinel errors in `internal/errs/`
- Context propagation for tracing and cancellation
- No global state — inject all dependencies
- `sync.Pool` for hot-path buffer allocation (BufferPool with 6 size tiers)
- Atomic operations for counters and flags; mutexes for compound state

## Implementation Guidelines
When extending or modifying:
1. **New protocol**: Implement `types.RawSession`, create server in `internal/protocol/<name>/`, register with Gateway
2. **New plugin**: Implement `types.Plugin`, set priority, add to chain in `NewPluginChain()`
3. **New store backend**: Implement `store.Store` interface in `internal/infra/store/`
4. **Session changes**: Keep shard logic in `internal/session/`, use atomic operations for ID generation
5. **Error handling**: Add new errors to `internal/errs/`, use `errors.Is()` for checking

## Protocol Port Map
| Protocol | Default Port | Transport |
|----------|-------------|-----------|
| TCP | 18000 | TCP |
| UDP | 18200 | UDP |
| HTTP | 18400 | TCP |
| WebSocket | 18600 | TCP |
| gRPC-Web | 18650 | TCP |
| CoAP | 18800 | UDP |
| QUIC | 18900 | UDP |
| Metrics | 9091 | TCP |
