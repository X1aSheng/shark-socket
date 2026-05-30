# Project Review 2026-05-30 11:55

## Scope

- Added a `tests/benchmark` package with benchmark coverage aligned to the
  original Shark-Socket smoke/light benchmark categories.
- Covered runtime session ID generation, session register/get/unregister,
  plugin-chain message flow, and TCP/UDP/WebSocket/HTTP echo paths.
- Updated the scripted benchmark runner so `go run scripts/run_tests.go -mode benchmark`
  includes the new benchmark package alongside existing TCP framer and CoAP
  message microbenchmarks.

## Benchmark Alignment

The new suite provides local and cloud-comparable signals for:

- `BenchmarkSessionManager_NextID`
- `BenchmarkSessionManager_NextID_Parallel`
- `BenchmarkSessionManager_RegisterGetUnregister`
- `BenchmarkPluginChain_Empty`
- `BenchmarkPluginChain_5Plugins`
- `BenchmarkTCPEcho`
- `BenchmarkUDPEcho`
- `BenchmarkWSEcho`
- `BenchmarkHTTPEcho`

These benchmarks intentionally start with smoke/light paths before adding
larger payloads, parallel clients, or soak/load escalation.

## Validation

Commands run locally:

```powershell
go test ./tests/benchmark -run=^$ -bench='Benchmark(SessionManager_NextID|PluginChain_Empty|TCPEcho|UDPEcho|WSEcho|HTTPEcho)$' -benchmem -benchtime=100ms -count=1
go test ./... -count=1
go vet ./...
go run scripts/run_tests.go -mode benchmark -timeout 3m
```

Results:

- Focused benchmark smoke passed.
- Full `go test ./... -count=1` passed.
- Full `go vet ./...` passed.
- Scripted benchmark mode passed and wrote reports under `logs/`.

Representative scripted benchmark results:

| Benchmark | Result |
| --- | --- |
| `BenchmarkSessionManager_NextID` | 1.599 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkSessionManager_NextID_Parallel` | 9.200 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkSessionManager_RegisterGetUnregister` | 178.8 ns/op, 224 B/op, 3 allocs/op |
| `BenchmarkPluginChain_Empty` | 1.625 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkPluginChain_5Plugins` | 48.64 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkTCPEcho` | 56838 ns/op, 88 B/op, 7 allocs/op |
| `BenchmarkUDPEcho` | 14921 ns/op, 112 B/op, 6 allocs/op |
| `BenchmarkWSEcho` | 18399 ns/op, 1088 B/op, 5 allocs/op |
| `BenchmarkHTTPEcho` | 89648 ns/op, 10040 B/op, 101 allocs/op |

## Notes

- Cloud benchmark execution is still pending available SSH credentials or an
  already configured remote target.
- Next benchmark step should add guarded smoke/light scripts for cloud hosts
  modeled after the original project's resource-limited benchmark flow.
