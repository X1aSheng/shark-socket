# Shark-Socket

High-performance, extensible multi-protocol networking framework in Go (>= 1.26).

Supports **TCP, TLS, UDP, HTTP, WebSocket, CoAP** with a unified API, shared session management, and a plugin system.

## Quick Start

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

## Features

- **Multi-protocol**: TCP, TLS, UDP, HTTP, WebSocket, CoAP
- **Generic sessions**: `Session[M]` with type-safe `SendTyped` and universal `Send([]byte)`
- **Sharded SessionManager**: 32-shard locking with LRU eviction
- **Plugin system**: Priority-ordered chain with ErrSkip/ErrDrop/ErrBlock control
- **6-level BufferPool**: Micro(128B)/Tiny(512B)/Small(4KB)/Medium(32KB)/Large(256KB)/Huge
- **Built-in plugins**: Blacklist, RateLimit, Heartbeat, AutoBan, Persistence, Cluster
- **Gateway**: Multi-protocol orchestration with shared SessionManager
- **Defense**: Overload protection, backpressure, log sampling
- **Observability**: Prometheus metrics, structured logging, health checks

## Architecture

```
api/          Public API — type aliases, factory functions
gateway/      Multi-protocol orchestration
protocol/     TCP, UDP, HTTP, WebSocket, CoAP implementations
session/      BaseSession, sharded Manager, LRU eviction
plugin/       Chain, Blacklist, RateLimit, Heartbeat, AutoBan, Persistence, Cluster
infra/        BufferPool, Logger, Metrics, Cache, Store, PubSub, CircuitBreaker
types/        Enums, Message[T], Session[M], Plugin interface
errs/         Error taxonomy with classification helpers
defense/      Overload protector, backpressure, log sampler
utils/        IP parsing, atomic helpers
```

## Protocols

| Protocol | Port | Features |
|----------|------|----------|
| TCP | 18000 | Framer (4 types), WorkerPool, writeQueue, drain |
| UDP | 18200 | Pseudo-sessions, sweep TTL |
| HTTP | 18400 | Mode A (thin wrapper) + Mode B (session + plugins) |
| WebSocket | 18600 | Ping/Pong, origin check, gorilla/websocket |
| CoAP | 18800 | CON retransmit, ACK, MessageID dedup |

## Plugins

| Plugin | Priority | Purpose |
|--------|----------|---------|
| BlacklistPlugin | 0 | IP/CIDR blocking with TTL |
| RateLimitPlugin | 10 | Dual-layer token bucket |
| AutoBanPlugin | 20 | Auto-ban on threshold violations |
| HeartbeatPlugin | 30 | TimeWheel idle timeout |
| ClusterPlugin | 40 | Cross-node routing via PubSub |
| PersistencePlugin | 50 | Async batch Store writes |

## Running Examples

```bash
# TCP echo server
go run examples/basic_tcp/main.go

# Multi-protocol gateway
go run examples/multi_protocol/main.go

# TCP with plugins
go run examples/session_plugins/main.go

# Graceful shutdown demo
go run examples/graceful_shutdown/main.go
```

## Testing

```bash
# All tests
go test ./... -v

# Benchmarks
go test ./... -bench=. -benchmem

# Specific package
go test ./internal/protocol/tcp/... -v
```

## Docker

```bash
docker build -t shark-socket .
docker-compose up
```

## Performance Targets

| Metric | Target |
|--------|--------|
| TCP throughput | >= 100K msg/s (single core) |
| Connection latency P99 | <= 1ms |
| Concurrent connections | >= 100K |
| Plugin overhead | <= 200ns/hop |
| BufferPool Get+Put | < 10ns/op, 0 alloc |

## License

MIT
