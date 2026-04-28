# Test Design Skill — Shark-Socket

> This skill encodes the testing strategy and patterns for shark-socket's multi-protocol architecture.

## Testing Philosophy

1. **Protocol Isolation**: Each protocol tested independently before multi-protocol integration
2. **Plugin Chain Isolation**: Plugins tested with mock sessions before chain integration
3. **Layered Testing**: Unit → Integration → Benchmark → Fuzz
4. **Time-Dependent Tests**: Use generous timeouts and atomic flags for async verification
5. **Network Tests**: Bind to `:0` for random port allocation, use `127.0.0.1` only

## Test Categories

### Unit Tests (per package)
- **errs/**: Sentinel error values, `Is()`/`As()` behavior
- **types/**: Interface compliance, enum validation
- **utils/**: Atomic operations, sync primitives, network helpers
- **protocol/*/**: Session lifecycle, message encode/decode, options parsing
- **plugin/**: Time wheel operations, token bucket, blacklist TTL, heartbeat timing
- **infra/**: Cache TTL, store CRUD, circuit breaker state transitions, buffer pool get/put
- **session/**: Shard distribution, LRU eviction, concurrent access, ID uniqueness
- **defense/**: Backpressure threshold, overload detection, sampler distribution

### Protocol-Specific Tests
- **tcp/**: Echo server, framer round-trip, TLS reload, worker pool dispatch
- **udp/**: Session tracking, echo integrity, multi-client concurrency
- **http/**: Mode A/B handlers, H2C, access logging, concurrent requests
- **websocket/**: Upgrade flow, text/binary echo, multi-message
- **coap/**: CON/ACK flow, message dedup (CheckAndRecord), options encode/decode
- **quic/**: Session setup, stream communication
- **grpcgw/**: Server configuration, content-type handling

### Integration Tests (`tests/integration/`)
- `data_comm_test.go`: TCP/UDP/WebSocket/HTTP/CoAP echo integrity and concurrency
- `multi_protocol_test.go`: All protocols running simultaneously, graceful shutdown
- `deploy_test.go`: Dockerfile, docker-compose, and .dockerignore validation
- `k8s_manifest_test.go`: Kubernetes manifest structure and security validation
- `helm_test.go`: Helm chart structure, values, helpers, and notes validation

### Benchmarks
- `tests/benchmark/data_comm_bench_test.go`: End-to-end protocol benchmarks
- `tests/benchmark/protocol_bench_test.go`: Cross-protocol comparison
- `internal/protocol/tcp/echo_bench_test.go`: TCP echo throughput
- `internal/infra/bufferpool/bench_test.go`: Buffer pool allocation
- `internal/session/manager_bench_test.go`: Session manager concurrent operations
- `internal/plugin/chain_bench_test.go`: Plugin chain execution

### Fuzz Tests
- `internal/protocol/tcp/framer_fuzz_test.go`: Framer boundary fuzzing
- `internal/protocol/coap/message_fuzz_test.go`: CoAP message parsing fuzzing

## Test Patterns

### Table-Driven Tests
```go
func TestOptions(t *testing.T) {
    tests := []struct {
        name string
        opt  Option
        want interface{}
    }{
        {"WithTimeout", WithTimeout(5*time.Second), 5 * time.Second},
        {"WithBufferSize", WithBufferSize(4096), 4096},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) { /* apply and verify */ })
    }
}
```

### Mock Session (for plugin tests)
```go
type mockSession struct {
    id       uint64
    addr     net.Addr
    alive    atomic.Bool
    closed   atomic.Bool
}

func newMockSession(id uint64, ip string) *mockSession {
    s := &mockSession{id: id, addr: &net.UDPAddr{IP: net.ParseIP(ip), Port: 12345}}
    s.alive.Store(true)
    return s
}
```

### Integration Test Pattern
```go
func testServer(t *testing.T) net.Listener {
    t.Helper()
    ln, err := net.Listen("tcp", "127.0.0.1:0")
    if err != nil {
        t.Fatalf("listen: %v", err)
    }
    t.Cleanup(func() { ln.Close() })
    return ln
}
```

### Time-Dependent Plugin Test
```go
func TestTimeout(t *testing.T) {
    var fired atomic.Int64
    tw := NewTimeWheel(10*time.Millisecond, 10, func(id uint64) {
        fired.Add(1)
    })
    defer tw.Stop()
    tw.Add(42, 30*time.Millisecond)
    time.Sleep(150 * time.Millisecond)  // generous margin
    if fired.Load() == 0 {
        t.Fatal("expected timeout to fire")
    }
}
```

## Coverage Targets
| Layer | Target |
|-------|--------|
| errs/ | 100% |
| types/ | 100% |
| utils/ | 95%+ |
| plugin/ | 90%+ |
| protocol/*/ | 85%+ |
| infra/ | 85%+ |
| session/ | 90%+ |
| gateway/ | 80%+ |
| defense/ | 90%+ |
| api/ | 80%+ |

## CI Test Matrix
| Job | Scope | Platform |
|-----|-------|----------|
| test-unit | All `./...` with race (Linux/macOS), coverage (Linux) | ubuntu, macos, windows × go 1.26, stable |
| test-integration | `tests/integration/` + protocol integration tests | ubuntu-latest |
| test-plugin | Plugin tests + benchmarks | ubuntu-latest |
| lint | go vet, go fmt, golangci-lint | ubuntu-latest |
| build | Build all, examples, production binary, go mod verify | ubuntu, macos, windows × go 1.26, stable |
| docker | Build image, health check, smoke test | ubuntu-latest |
| deploy-check | Docker compose, Helm lint, Helm template | ubuntu-latest |
