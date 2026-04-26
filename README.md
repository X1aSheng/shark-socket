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
- **6-level BufferPool**: Micro(512B)/Tiny(2KB)/Small(4KB)/Medium(32KB)/Large(256KB)/Huge
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
utils/        ShardedMap, AtomicBool, IP parsing
```

## Protocols

| Protocol | Default Port | Features |
|----------|-------------|----------|
| TCP | 18000 | Framer (4 types), WorkerPool, writeQueue, drain, TLS reload |
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

### Run Tests

```bash
# All tests (487 test cases across 22 packages)
go test ./... -v

# With coverage
go test ./... -cover

# Benchmarks
go test ./... -bench=. -benchmem

# Race detector
go test -race ./...

# Specific package
go test ./internal/protocol/tcp/... -v
```

### Test Coverage

| Package | Coverage |
|---------|----------|
| api | 92.6% |
| defense | 100.0% |
| errs | 100.0% |
| infra/cache | 100.0% |
| infra/store | 100.0% |
| infra/tracing | 100.0% |
| infra/pubsub | 93.9% |
| infra/bufferpool | 87.5% |
| infra/circuitbreaker | 82.8% |
| infra/logger | 73.9% |
| infra/metrics | 60.4% |
| session | 92.2% |
| types | 92.3% |
| utils | 89.0% |
| protocol/tcp | 87.0% |
| protocol/websocket | 83.4% |
| protocol/http | 83.0% |
| protocol/udp | 81.4% |
| protocol/coap | 77.8% |
| plugin | 62.7% |
| gateway | 48.5% |

Test methodology: Server readiness is verified via polling (`waitForTCPServer` / `waitForUDPServer`) instead of fixed sleeps, ensuring stability across CI and local environments.

### Benchmarks

Benchmark results on AMD Ryzen 7 8845HS (Windows 11, Go 1.26):

#### TCP Protocol

| Benchmark | ns/op | B/op | allocs/op | Throughput |
|-----------|-------|------|-----------|------------|
| TCPEcho | 28,493 | 4,937 | 14 | ~35K msg/s |
| TCPEcho_SmallMessage | 29,479 | 4,850 | 14 | ~34K msg/s |
| TCPEcho_LargeMessage | 51,601 | 72,532 | 14 | 158.76 MB/s |
| TCPEcho_Parallel | 5,390 | 4,930 | 14 | ~186K msg/s |

#### Session Manager

| Benchmark | ns/op | B/op | allocs/op |
|-----------|-------|------|-----------|
| SessionRegister | 147.1 | 400 | 8 |
| ManagerGet | 7.4 | 0 | 0 |
| ManagerNextID | 1.6 | 0 | 0 |
| ManagerNextIDParallel | 9.9 | 0 | 0 |
| ManagerCount | 0.4 | 0 | 0 |

#### BufferPool

| Benchmark | ns/op | B/op | allocs/op |
|-----------|-------|------|-----------|
| PoolGetPut/Micro_64 | 12.6 | 0 | 0 |
| PoolGetPut/Tiny_1024 | 16.2 | 0 | 0 |
| PoolGetPut/Small_4096 | 41.4 | 0 | 0 |
| PoolGetPut/Medium_16384 | 125.2 | 0 | 0 |
| PoolGetPut/Large_131072 | 885.1 | 0 | 0 |
| Parallel/Micro_64 | 11.6 | 0 | 0 |
| Parallel/Small_4096 | 11.1 | 0 | 0 |
| Parallel/Large_131072 | 130.0 | 0 | 0 |
| GetLevel | 0.2 | 0 | 0 |
| DefaultSingleton | 16.4 | 0 | 0 |

#### Plugin Chain

| Benchmark | ns/op | B/op | allocs/op |
|-----------|-------|------|-----------|
| Chain_Empty | 1.6 | 0 | 0 |
| Chain_5Plugins | 9.1 | 0 | 0 |
| Chain_10Plugins | 16.4 | 0 | 0 |
| Chain_OnAccept_5 | 7.8 | 0 | 0 |
| Chain_OnClose_5 | 6.7 | 0 | 0 |
| Chain_Parallel | 1.3 | 0 | 0 |

## Docker

```bash
docker build -t shark-socket .
docker-compose up
```

## Performance Summary

| Metric | Result |
|--------|--------|
| TCP parallel throughput | ~186K msg/s |
| TCP single-conn throughput | ~35K msg/s |
| TCP large msg throughput | 158.76 MB/s |
| Plugin chain overhead | ~1.8 ns/hop (5 plugins) |
| BufferPool Get+Put (Micro) | 12.6 ns/op, 0 alloc |
| Session register | 147 ns/op |
| Session get | 7.4 ns/op |
| Concurrent connections | tested up to 100K |

## License

MIT
