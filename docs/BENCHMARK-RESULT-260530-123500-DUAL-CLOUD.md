# Shark-Socket-New Dual Cloud Benchmark Result

Date: 2026-05-30 12:35 Asia/Shanghai

## Summary

Dual-cloud validation completed with commit `a3d283f`.

Result:

- Server2 `47.96.129.59`: full Go tests, vet, scripted all-mode validation,
  medium benchmark, binary build, and multi-protocol service run passed.
- Server1 `120.76.44.233`: full Go tests and resource-gated medium benchmark
  passed.
- Cross-host traffic from server1 to server2 passed for health/readiness, TCP,
  UDP, HTTP, WebSocket, CoAP/LwM2M, and gRPC-Web direct.

## Environment

| Role | Host | CPU/RAM | Go | Working Directory |
| --- | --- | --- | --- | --- |
| Server | `47.96.129.59` | 8 cores, 16 GB class | `go1.26.3 linux/amd64` | `/tmp/shark-socket-dual-server-a3d283f` |
| Client | `120.76.44.233` | 2 cores, 2 GB class | `go1.26.3 linux/amd64` | `/tmp/shark-socket-dual-client-a3d283f` |

Network/build settings:

- `GOPROXY=https://goproxy.cn,direct`
- `GOSUMDB=sum.golang.google.cn`

Initial resource state:

| Host | MemAvailable | Load |
| --- | --- | --- |
| `47.96.129.59` | 14,493,636 KB | `0.11 0.10 0.13` |
| `120.76.44.233` | 1,249,164 KB | `0.09 0.11 0.09` |

## Server2 Validation

Commands:

```bash
go test ./... -count=1
go vet ./...
go run scripts/run_tests.go -mode all -timeout 5m
go run scripts/run_benchmarks.go -profile cloud -stage medium -logdir logs/dual-server-bench
go build -o /tmp/shark-socket-dual-server-a3d283f/shark-socket ./cmd/shark-socket
```

Results:

- Full tests passed.
- Vet passed.
- Scripted all-mode validation passed.
- Unit report: 113 passed, 0 failed, 0 skipped.
- Integration report: 6 passed, 0 failed, 0 skipped.
- Benchmark report passed.
- Binary build passed.

Representative scripted benchmark results on server2:

| Benchmark | Result |
| --- | --- |
| `BenchmarkLengthPrefixFramerRoundTrip` | 209.2 ns/op, 664 B/op, 6 allocs/op |
| `BenchmarkLineFramerRoundTrip` | 2117 ns/op, 1840 B/op, 12 allocs/op |
| `BenchmarkMessageParse` | 83.18 ns/op, 264 B/op, 2 allocs/op |
| `BenchmarkMessageMarshal` | 90.72 ns/op, 304 B/op, 3 allocs/op |
| `BenchmarkSessionManager_NextID` | 4.659 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkSessionManager_NextID_Parallel` | 23.98 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkSessionManager_RegisterGetUnregister` | 214.8 ns/op, 224 B/op, 3 allocs/op |
| `BenchmarkPluginChain_5Plugins` | 35.86 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkTCPEcho` | 27262 ns/op, 88 B/op, 7 allocs/op |
| `BenchmarkUDPEcho` | 4676 ns/op, 112 B/op, 6 allocs/op |
| `BenchmarkWSEcho` | 5944 ns/op, 1088 B/op, 5 allocs/op |
| `BenchmarkHTTPEcho` | 43236 ns/op, 9787 B/op, 100 allocs/op |

Server2 resource-gated medium benchmark highlights:

| Benchmark | Result |
| --- | --- |
| `BenchmarkSessionManager_NextID` | 4.657 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkSessionManager_NextID_Parallel` | 20.36 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkSessionManager_RegisterGetUnregister` | 214.8 ns/op, 224 B/op, 3 allocs/op |
| `BenchmarkPluginChain_Empty` | 2.011 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkPluginChain_5Plugins` | 35.13 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkMessageParse` | 78.14 ns/op, 264 B/op, 2 allocs/op |
| `BenchmarkMessageMarshal` | 89.11 ns/op, 304 B/op, 3 allocs/op |

Server2 benchmark resource gates:

- Medium start: MemAvailable about 14,176 MB, load1 1.33.
- Medium end: MemAvailable about 14,175 MB, load1 1.78.

## Server1 Validation

Commands:

```bash
go test ./... -count=1
go run scripts/run_benchmarks.go -profile cloud -stage medium -logdir logs/dual-client-bench
```

Results:

- Full tests passed.
- Resource-gated medium benchmark passed.

Server1 resource-gated medium benchmark highlights:

| Benchmark | Result |
| --- | --- |
| `BenchmarkSessionManager_NextID` | 7.804 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkSessionManager_NextID_Parallel` | 6.830 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkSessionManager_RegisterGetUnregister` | 365.7 ns/op, 224 B/op, 3 allocs/op |
| `BenchmarkPluginChain_Empty` | 3.660 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkPluginChain_5Plugins` | 72.33 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkMessageParse` | 144.4 ns/op, 264 B/op, 2 allocs/op |
| `BenchmarkMessageMarshal` | 166.3 ns/op, 304 B/op, 3 allocs/op |
| `BenchmarkTCPEcho` | 56432 ns/op, 88 B/op, 7 allocs/op |
| `BenchmarkUDPEcho` | 16686 ns/op, 112 B/op, 6 allocs/op |
| `BenchmarkWSEcho` | 21134 ns/op, 1088 B/op, 5 allocs/op |
| `BenchmarkHTTPEcho` | 77520 ns/op, 9326 B/op, 100 allocs/op |

Server1 benchmark resource gates:

- Medium start: MemAvailable about 1,205 MB, load1 0.82.
- Medium end: MemAvailable about 1,212 MB, load1 1.08.

## Cross-Host Protocol Test

Server2 ran the binary with this temporary configuration:

| Protocol | Address |
| --- | --- |
| TCP | `0.0.0.0:18000` |
| UDP | `0.0.0.0:18002` |
| HTTP | `0.0.0.0:18003` |
| WebSocket | `0.0.0.0:18004/ws` |
| CoAP/LwM2M | `0.0.0.0:18005` |
| gRPC-Web direct | `0.0.0.0:18006/grpc` |
| Metrics | `0.0.0.0:18080` |
| Health/readiness | `0.0.0.0:18081` |

Cross-host checks from server1 to server2:

| Check | Result |
| --- | --- |
| `GET /healthz` | `ok` |
| `GET /readyz` | `ready` |
| HTTP POST echo | `http-dual-cloud` |
| TCP length-prefix echo | `tcp-dual-cloud` |
| UDP datagram echo | `udp-dual-cloud` |
| WebSocket binary echo | `ws-dual-cloud` |
| gRPC-Web direct echo | `grpc-dual-cloud` |
| CoAP/LwM2M lifecycle | register, write, read, deregister passed |

The metrics endpoint returned HTTP 200 with an empty body in this run. The
temporary server process was stopped after cross-host validation.

## Logs And Data

Server2 logs:

- Directory: `/tmp/shark-socket-dual-server-a3d283f/logs`
- Scripted unit JSON: `106279` bytes
- Scripted unit report: `9730` bytes
- Scripted integration JSON: `6112` bytes
- Scripted integration report: `878` bytes
- Scripted benchmark JSON: `15624` bytes
- Scripted benchmark report: `2751` bytes
- Resource-gated benchmark logs: five files under `logs/dual-server-bench`
- Metrics snapshot after cross-host run: `logs/dual-server-metrics-after-cross.log`, `0` bytes

Server1 logs:

- Directory: `/tmp/shark-socket-dual-client-a3d283f/logs`
- Cross-host client log: `logs/dual-cross-client.log`, `192` bytes
- Resource-gated benchmark logs: five files under `logs/dual-client-bench`

Snapshot tar files were removed after upload. The working directories and logs
remain on both cloud hosts for inspection.

## Disk State After Run

| Host | Root FS Used | Root FS Available | `/tmp` Used |
| --- | --- | --- | --- |
| `47.96.129.59` | 5.0 GB | 33 GB | 87 MB |
| `120.76.44.233` | 9.4 GB | 28 GB | 4.5 MB |
