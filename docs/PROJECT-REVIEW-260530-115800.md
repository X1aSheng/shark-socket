# Project Review 2026-05-30 11:58

## Scope

- Added `scripts/run_benchmarks.go`, a resource-aware benchmark matrix runner
  for local and cloud smoke/light/medium stages.
- Added `docs/BENCHMARK-RESOURCE-LIMITED-TEST-FLOW-20260530.md` to document
  constrained cloud benchmark usage.
- Linked the resource-limited benchmark flow from the README and current
  implementation goals.

## Validation

Commands run locally:

```powershell
go run scripts/run_benchmarks.go -profile local -stage smoke -logdir logs/bench-matrix
go run scripts/run_benchmarks.go -profile local -stage light -logdir logs/bench-matrix
go test ./scripts -count=1
go test ./... -count=1
go vet ./...
```

Results:

- Local smoke benchmark matrix passed.
- Local light benchmark matrix passed.
- Script package tests passed.
- Full `go test ./... -count=1` passed.
- Full `go vet ./...` passed.

Representative local light results:

| Benchmark | Result |
| --- | --- |
| `BenchmarkSessionManager_NextID` | 1.585 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkSessionManager_NextID_Parallel` | 10.36 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkPluginChain_5Plugins` | 46.90 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkMessageParse` | 73.98 ns/op, 264 B/op, 2 allocs/op |
| `BenchmarkMessageMarshal` | 94.73 ns/op, 304 B/op, 3 allocs/op |
| `BenchmarkTCPEcho` | 38785 ns/op, 88 B/op, 7 allocs/op |
| `BenchmarkUDPEcho` | 14446 ns/op, 112 B/op, 6 allocs/op |
| `BenchmarkWSEcho` | 17002 ns/op, 1088 B/op, 5 allocs/op |
| `BenchmarkHTTPEcho` | 62098 ns/op, 9937 B/op, 101 allocs/op |

## Notes

- The cloud profile checks `/proc/meminfo` and `/proc/loadavg` on Linux before
  each group.
- Actual cloud execution is not complete in this step because no SSH target or
  remote runner was configured in the local repository context.
