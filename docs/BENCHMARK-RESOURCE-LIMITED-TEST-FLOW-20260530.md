# Shark-Socket-New Resource-Limited Benchmark Flow

This flow adapts the original Shark-Socket cloud benchmark approach for
`shark-socket-new`.

## Purpose

Validate benchmark smoke and light paths on small cloud servers without running
the full benchmark suite at once. It is designed for constrained hosts such as
2 vCPU / 2 GB RAM / no swap Ubuntu servers.

## Script

Run from the project root:

```powershell
go run scripts/run_benchmarks.go -profile local -stage smoke
go run scripts/run_benchmarks.go -profile local -stage light
```

On a Linux cloud host:

```bash
go run scripts/run_benchmarks.go -profile cloud -stage smoke
go run scripts/run_benchmarks.go -profile cloud -stage light
go run scripts/run_benchmarks.go -profile cloud -stage medium
```

The cloud profile checks `/proc/meminfo` and `/proc/loadavg` before each group.
Smoke/light groups require at least 768 MB `MemAvailable` and load1 at or below
2.5. Medium groups require at least 1024 MB `MemAvailable` and load1 at or
below 2.0.

## Stages

| Stage | Groups | Benchtime | Cloud Use |
| --- | --- | --- | --- |
| `smoke` | core/session/plugin, CoAP message parse/marshal | 100 ms | Yes |
| `light` | smoke plus TCP/UDP echo and HTTP/WebSocket echo | 100-300 ms | Yes |
| `medium` | light plus selected 1 s core microbenchmarks | 1 s selected | Only if resource gates pass |

## Groups

| Group | Regex | Packages |
| --- | --- | --- |
| `core-smoke` | `BenchmarkSessionManager|BenchmarkPluginChain` | `./tests/benchmark` |
| `coap-smoke` | `BenchmarkMessageParse|BenchmarkMessageMarshal` | `./internal/transport/coap` |
| `tcp-udp-light` | `BenchmarkTCPEcho$|BenchmarkUDPEcho$` | `./tests/benchmark` |
| `http-ws-light` | `BenchmarkHTTPEcho$|BenchmarkWSEcho$` | `./tests/benchmark` |
| `core-medium` | selected session/plugin/CoAP microbenchmarks | `./tests/benchmark`, `./internal/transport/coap` |

## Cloud Procedure

1. Confirm SSH is responsive.
2. Confirm the working tree is a clean checkout or an uploaded archive of the
   commit under test.
3. Confirm Go is installed with the version expected by `go.mod`.
4. Run smoke first.
5. Run light only if smoke passes.
6. Run medium only if light passes and resource gates remain healthy.
7. Stop on any failure, timeout, process kill, or resource gate rejection.

## Result Recording

Create a review file such as:

```text
docs/BENCHMARK-RESULT-YYMMDD-HHMMSS.md
```

Record:

- Commit hash.
- Host CPU/RAM and Go version.
- Stage and group.
- Command and log file.
- Pass/fail.
- Resource state before and after each cloud group.
- Representative `ns/op`, `B/op`, and `allocs/op`.
- Stop or skip reason.

## Current Limit

The repository now has repeatable cloud-safe benchmark commands, but actual
cloud execution still requires SSH access or a preconfigured remote runner.
