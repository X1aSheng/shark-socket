# Shark-Socket-New Cloud Benchmark Result

Date: 2026-05-30 12:00 Asia/Shanghai

## Summary

Resource-limited benchmark validation completed on a cloud Ubuntu host using
commit `d64a9db`.

Result:

- Cloud smoke benchmark: PASS
- Cloud light benchmark: PASS
- Cloud selected medium benchmark: PASS

The run used the new `scripts/run_benchmarks.go` cloud profile. The source tree
was uploaded to `/tmp/shark-socket-new-bench-d64a9db` as a temporary archive so
existing server working directories were not modified.

## Cloud Environment

| Item | Value |
| --- | --- |
| Host | `120.76.44.233` |
| OS | Ubuntu server |
| CPU | 2 vCPU class, `Intel(R) Xeon(R) Platinum` |
| Go | `go1.26.3 linux/amd64` |
| Working directory | `/tmp/shark-socket-new-bench-d64a9db` |
| Go proxy | `GOPROXY=https://goproxy.cn,direct` |
| Sum DB | `GOSUMDB=sum.golang.google.cn` |

## Commands

```bash
go run scripts/run_benchmarks.go -profile cloud -stage smoke -logdir logs/cloud-bench
go run scripts/run_benchmarks.go -profile cloud -stage light -logdir logs/cloud-bench
go run scripts/run_benchmarks.go -profile cloud -stage medium -logdir logs/cloud-bench
```

## Resource Gates

Observed resource range during the benchmark run:

| Stage | MemAvailable | Load1 |
| --- | --- | --- |
| Smoke start | 1201 MB | 0.23 |
| Light start | 1204 MB | 0.29 |
| Medium start | 1210 MB | 0.35 |
| Medium after selected medium | 1212 MB | 0.50 |

All smoke/light gates passed the 768 MB and load1 <= 2.5 thresholds. The medium
gate passed the 1024 MB and load1 <= 2.0 thresholds.

## Smoke Results

| Benchmark | Result |
| --- | --- |
| `BenchmarkSessionManager_NextID` | 7.894 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkSessionManager_NextID_Parallel` | 6.378 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkSessionManager_RegisterGetUnregister` | 379.9 ns/op, 224 B/op, 3 allocs/op |
| `BenchmarkPluginChain_Empty` | 3.589 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkPluginChain_5Plugins` | 75.05 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkMessageParse` | 146.3 ns/op, 264 B/op, 2 allocs/op |
| `BenchmarkMessageMarshal` | 166.5 ns/op, 304 B/op, 3 allocs/op |

## Light Results

| Benchmark | Result |
| --- | --- |
| `BenchmarkTCPEcho` | 56241 ns/op, 89 B/op, 7 allocs/op |
| `BenchmarkUDPEcho` | 16660 ns/op, 112 B/op, 6 allocs/op |
| `BenchmarkWSEcho` | 21078 ns/op, 1088 B/op, 5 allocs/op |
| `BenchmarkHTTPEcho` | 76299 ns/op, 9322 B/op, 100 allocs/op |

## Selected Medium Results

| Benchmark | Result |
| --- | --- |
| `BenchmarkSessionManager_NextID` | 7.858 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkSessionManager_NextID_Parallel` | 6.382 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkSessionManager_RegisterGetUnregister` | 367.2 ns/op, 224 B/op, 3 allocs/op |
| `BenchmarkPluginChain_Empty` | 3.604 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkPluginChain_5Plugins` | 73.00 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkMessageParse` | 146.4 ns/op, 264 B/op, 2 allocs/op |
| `BenchmarkMessageMarshal` | 166.3 ns/op, 304 B/op, 3 allocs/op |

## Notes

- This is a constrained smoke/light/selected-medium signal, not a maximum
  capacity test.
- Server `47.96.129.59` was not used in this run because SSH host key
  verification failed from the current local environment.
- Temporary benchmark logs remain on the cloud host under
  `/tmp/shark-socket-new-bench-d64a9db/logs/cloud-bench`.
