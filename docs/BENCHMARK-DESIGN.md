# Benchmark & Stress Test Design

## Overview

This document describes the benchmark and stress test infrastructure for
shark-socket. The system consists of three layers:

1. **Micro-benchmarks** (`tests/benchmark/`) — Go `testing.B` benchmarks
2. **Stress tests** (`tests/stress/`) — Sustained load testing
3. **Runners** (`scripts/`) — Orchestrated execution with resource gating

---

## File Layout

```
tests/benchmark/
  protocol_bench_test.go    # Original 11 benchmarks (SessionManager, PluginChain, 6x transport echo)
  bench_payload_test.go     # Payload-size variants: 64B, 1KB, 16KB, 64KB across all transports
  bench_concurrent_test.go  # Concurrent connection variants: 1, 10, 100, 500 conns
  bench_plugins_test.go     # Real-plugin benchmarks: Blacklist, RateLimit, combined, full chain

tests/stress/
  stress_test.go            # Stress test suite (TCP sustained, burst, reconnect)

scripts/
  run_benchmarks.go         # Benchmark runner (profiles: local/cloud, stages: smoke/light/medium)
  run_stress.go             # Stress test runner (modes: tcp/burst/reconnect/all)
```

---

## Benchmarks

### Protocol Echo Benchmarks (`protocol_bench_test.go`)

| Benchmark | Transport | Measures |
|---|---|---|
| `BenchmarkSessionManager_NextID` | — | Atomic ID generation (serial) |
| `BenchmarkSessionManager_NextID_Parallel` | — | Atomic ID generation (parallel) |
| `BenchmarkSessionManager_RegisterGetUnregister` | — | Session lifecycle |
| `BenchmarkPluginChain_Empty` | — | Zero-plugin overhead |
| `BenchmarkPluginChain_5Plugins` | — | 5-plugin chain overhead |
| `BenchmarkTCPEcho` | TCP | Full round-trip echo latency |
| `BenchmarkUDPEcho` | UDP | Full round-trip echo latency |
| `BenchmarkWSEcho` | WebSocket | Full round-trip echo latency |
| `BenchmarkHTTPEcho` | HTTP | Full round-trip echo latency |
| `BenchmarkGRPCWebEcho` | gRPC-Web | Full round-trip echo latency |
| `BenchmarkQUICEcho` | QUIC | Full round-trip echo (TLS) |

### Payload-Size Benchmarks (`bench_payload_test.go`)

Each transport benchmark is run with 4 payload sizes as sub-benchmarks:

| Sub-benchmark | Size |
|---|---|
| `64B` | 64 bytes |
| `1KB` | 1,024 bytes |
| `16KB` | 16,384 bytes |
| `64KB` | 65,536 bytes |

**Functions**: `BenchmarkTCPEcho_PayloadSize`, `BenchmarkUDPEcho_PayloadSize`,
`BenchmarkWSEcho_PayloadSize`, `BenchmarkHTTPEcho_PayloadSize`,
`BenchmarkGRPCWebEcho_PayloadSize`, `BenchmarkQUICEcho_PayloadSize`

### Concurrent-Connection Benchmarks (`bench_concurrent_test.go`)

Each transport benchmark is run with 4 connection counts as sub-benchmarks,
using `b.RunParallel` for concurrent client execution:

| Sub-benchmark | Connections |
|---|---|
| `1conn` | 1 |
| `10conns` | 10 |
| `100conns` | 100 |
| `500conns` | 500 |

**Functions**: `BenchmarkTCPEcho_Concurrent`, `BenchmarkUDPEcho_Concurrent`,
`BenchmarkWSEcho_Concurrent`, `BenchmarkHTTPEcho_Concurrent`,
`BenchmarkGRPCWebEcho_Concurrent`

### Plugin Benchmarks (`bench_plugins_test.go`)

Real plugin instances tested through TCP echo:

| Benchmark | Plugins | What It Tests |
|---|---|---|
| `BenchmarkPluginChain_Blacklist` | `NewBlacklist("192.168.0.1", "10.0.0.0/8")` | OnAccept + OnMessage pass-through |
| `BenchmarkPluginChain_RateLimit` | `NewRateLimit(1e6, 1s)` | High-limit rate check on every message |
| `BenchmarkPluginChain_BlacklistRateLimit` | Blacklist + RateLimit | Combined plugin overhead |
| `BenchmarkPluginChain_FullChain` | Blacklist + AutoBan + RateLimit + Persistence | Full defense chain |

---

## Stress Tests (`tests/stress/stress_test.go`)

Three stress scenarios run via standard `go test`:

| Test | Description | Metrics |
|---|---|---|
| `TestStressTCPConnections` | N concurrent persistent TCP connections, sustained traffic | Throughput (msg/s), P50/P90/P99 latency, error rate |
| `TestStressTCPBurst` | Single connection, M concurrent requests (burst) | Burst throughput, concurrent request handling |
| `TestStressTCPReconnect` | N goroutines doing rapid connect/send/receive/close cycles | Connection churn tolerance |

### Usage

```bash
# Default: 50 conns, 10s duration, 256B payload
go test ./tests/stress/ -v -count=1 -timeout 120s

# Quick TCP sustained test
go test ./tests/stress/ -v -run TestStressTCPConnections -count=1
```

### Configuration

Default values are hardcoded in `stress_test.go`:
- `conns = 50` concurrent connections
- `duration = 10s` per test
- `payloadSize = 256` bytes

---

## Script Runners

### `scripts/run_benchmarks.go`

```bash
go run scripts/run_benchmarks.go -profile local -stage smoke
go run scripts/run_benchmarks.go -profile cloud -stage light
go run scripts/run_benchmarks.go -profile cloud -stage medium -logdir logs/cloud
```

| Profile | Behavior |
|---|---|
| `local` | Runs all groups without resource checks |
| `cloud` | Skips groups not marked `cloud: true`; checks MemAvailable and Load1 |

| Stage | Groups |
|---|---|
| `smoke` | Micro-benchmarks + CoAP |
| `light` | Smoke + transport echo + payload-size + concurrent + plugins |
| `medium` | All groups including QUIC, gRPC-web concurrent, full plugin chain |

### `scripts/run_stress.go`

```bash
go run scripts/run_stress.go -mode tcp -conns 100 -duration 30s
go run scripts/run_stress.go -mode burst -conns 200 -size 1024
go run scripts/run_stress.go -mode reconnect -conns 50 -duration 10s
go run scripts/run_stress.go -mode all -profile cloud

# Remote connection
go run scripts/run_stress.go -host tcp://47.110.42.28:18000 -conns 500 -duration 60s
```

| Flag | Default | Description |
|---|---|---|
| `-mode` | `tcp` | `tcp`, `burst`, `reconnect`, `all` |
| `-conns` | `100` | Concurrent connections |
| `-duration` | `30s` | Test duration |
| `-size` | `256` | Payload bytes |
| `-host` | `""` | Remote addr (empty = start local server) |
| `-profile` | `local` | `local` or `cloud` |
| `-logdir` | `logs` | Log output directory |

---

## Cloud Execution Plan

| Server | Role | Run |
|---|---|---|
| `47.110.42.28` (8c/30GB) | Server | `shark-socket` listening on `:18000` |
| `120.76.44.233` (2c/2GB) | Client | `go run scripts/run_stress.go -host tcp://47.110.42.28:18000 -conns 500 -duration 60s` |
| Local Windows | Dev | Compile + unit tests + benchmarks |

---

## Verification

```bash
# 1. Full regression
go test ./... -count=1 -timeout 300s

# 2. Benchmarks
go test ./tests/benchmark/ -bench=. -benchmem -count=1 -timeout 120s

# 3. Stress tests
go test ./tests/stress/ -v -count=1 -timeout 120s

# 4. Runner scripts
go run scripts/run_benchmarks.go -profile local -stage smoke
go run scripts/run_stress.go -mode tcp -conns 10 -duration 5s
```

## Results

### Local Machine (Ryzen 7 8845HS, Windows 11)

| Test | Throughput | P50/P99 | Errors |
|---|---|---|---|
| TCP sustained (50 conns, 256B, 10s) | **219,720 msg/s** | ~500µs / 1.0ms | 0 |
| TCP sustained (200 conns, 256B, 30s) | — | — | — |

### Cloud Server 2 (8c/30GB, Ubuntu 26.04)

| Test | Throughput | Errors | Notes |
|---|---|---|---|
| TCP sustained (50 conns, 256B, 10s) | **316,375 msg/s** | 0 | 3.16M msgs in 10s |
| TCP burst (500 concurrent, 1 conn) | **12,331 msg/s** | 425 recv err | Single-conn burst — sequential Send/Receive model limits concurrency |
| TCP reconnect (50 loops, 10s) | **85,922 msg/s** | 0 | 859K rapid connect/send/close cycles |

> Note: The `run_stress.go` runner script connects successfully when using `-host`
> but the `tcp.Client.Receive()` times out when connecting to a remote container.
> Use `go test ./tests/stress/` for reliable in-situ stress testing. The runner
> script works correctly for local (non-remote) scenarios.
