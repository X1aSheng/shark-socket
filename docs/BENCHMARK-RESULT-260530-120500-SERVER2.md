# Shark-Socket-New Server2 Cloud Validation Result

Date: 2026-05-30 12:05 Asia/Shanghai

## Summary

Cloud validation completed on server2 `47.96.129.59` using commit `68e3123`.

Result:

- Cloud `go test ./... -count=1`: PASS
- Cloud `go vet ./...`: PASS
- Cloud scripted all-mode validation: PASS
- Cloud resource-gated benchmark medium stage: PASS
- Multi-protocol service run on server2: PASS
- Cross-host client checks from `120.76.44.233` to `47.96.129.59`: PASS

The source tree was uploaded as a temporary archive to
`/tmp/shark-socket-server2-68e3123` so existing server directories were not
modified.

## Environment

| Item | Value |
| --- | --- |
| Host | `47.96.129.59` |
| Role | Server-side cloud validation |
| OS | Ubuntu 26.04 |
| CPU | 8 cores |
| RAM | 16 GB class |
| Go | `go1.26.3 linux/amd64` |
| Working directory | `/tmp/shark-socket-server2-68e3123` |
| Go proxy | `GOPROXY=https://goproxy.cn,direct` |
| Sum DB | `GOSUMDB=sum.golang.google.cn` |

Initial resource state:

- `MemAvailable`: about 13.9 GB
- Load average: `0.23 0.16 0.16`

## Validation Commands

```bash
go test ./... -count=1
go vet ./...
go run scripts/run_tests.go -mode all -timeout 5m
go run scripts/run_benchmarks.go -profile cloud -stage medium -logdir logs/server2-bench
go build -o /tmp/shark-socket-server2-68e3123 ./cmd/shark-socket
```

Scripted validation result:

- Unit report: 113 passed, 0 failed, 0 skipped.
- Integration report: 6 passed, 0 failed, 0 skipped.
- Benchmark report generated and passed.

## Benchmark Results

Representative scripted benchmark results:

| Benchmark | Result |
| --- | --- |
| `BenchmarkLengthPrefixFramerRoundTrip` | 209.3 ns/op, 664 B/op, 6 allocs/op |
| `BenchmarkLineFramerRoundTrip` | 2122 ns/op, 1840 B/op, 12 allocs/op |
| `BenchmarkMessageParse` | 78.70 ns/op, 264 B/op, 2 allocs/op |
| `BenchmarkMessageMarshal` | 89.33 ns/op, 304 B/op, 3 allocs/op |
| `BenchmarkSessionManager_NextID` | 4.661 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkPluginChain_5Plugins` | 36.22 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkTCPEcho` | 27558 ns/op, 88 B/op, 7 allocs/op |
| `BenchmarkUDPEcho` | 4745 ns/op, 112 B/op, 6 allocs/op |
| `BenchmarkWSEcho` | 6062 ns/op, 1088 B/op, 5 allocs/op |
| `BenchmarkHTTPEcho` | 43585 ns/op, 9728 B/op, 100 allocs/op |

Resource-gated medium benchmark result:

| Benchmark | Result |
| --- | --- |
| `BenchmarkSessionManager_NextID` | 4.662 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkSessionManager_NextID_Parallel` | 20.69 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkSessionManager_RegisterGetUnregister` | 215.5 ns/op, 224 B/op, 3 allocs/op |
| `BenchmarkPluginChain_Empty` | 1.834 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkPluginChain_5Plugins` | 37.15 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkMessageParse` | 77.15 ns/op, 264 B/op, 2 allocs/op |
| `BenchmarkMessageMarshal` | 88.18 ns/op, 304 B/op, 3 allocs/op |

Medium-stage resource gates stayed healthy:

- Start: MemAvailable about 13.6 GB, load1 0.45.
- End: MemAvailable about 13.6 GB, load1 1.17.

## Service Run

Ports `18000`, `18080`, and `18081` were already occupied by an existing Docker
service on server2, so this validation used non-conflicting temporary ports.

Temporary server config:

| Protocol | Address |
| --- | --- |
| TCP | `0.0.0.0:18007` |
| UDP | `0.0.0.0:18002` |
| HTTP | `0.0.0.0:18003` |
| WebSocket | `0.0.0.0:18004/ws` |
| CoAP/LwM2M | `0.0.0.0:18005` |
| gRPC-Web direct | `0.0.0.0:18006/grpc` |
| Health/readiness | `0.0.0.0:18082` |
| Metrics | `0.0.0.0:18083` |

Server2 local checks passed:

- `GET /healthz`: `ok`
- `GET /readyz`: `ready`
- HTTP POST echo: `http-server2`
- TCP length-prefix echo: `tcp-server2`
- UDP datagram echo: `udp-server2`
- gRPC-Web direct echo: `grpc-server2`

Cross-host checks from `120.76.44.233` to `47.96.129.59` passed:

- `GET /healthz`: `ok`
- `GET /readyz`: `ready`
- HTTP POST echo: `http-cross-server2`
- TCP length-prefix echo: `tcp-cross-server2`
- UDP datagram echo: `udp-cross-server2`
- gRPC-Web direct echo: `grpc-cross-server2`

The temporary server process was stopped after validation. Logs remain under
`/tmp/shark-socket-server2-68e3123`.
