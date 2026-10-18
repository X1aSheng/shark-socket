# shark-socket

[简体中文](README.zh-CN.md) | **English**

## Project Overview

`shark-socket` is a Go multi-protocol gateway runtime for IoT, real-time
messaging, and edge computing. In a single process it hosts connection-
oriented (TCP, WebSocket, QUIC), datagram (UDP, CoAP), and application-layer
(LwM2M, gRPC-Web, HTTP) transports, unified by one session index, one plugin
chain, and one observability plane.

**Essential properties**

- **Single runtime ownership** - the Gateway creates and injects the shared
  Runtime (SessionManager / PluginRunner / Logger / Metrics / Tracer);
  transports consume it and hold no shared state.
- **One session model across protocols** - a single `Session` abstraction
  enables cross-protocol query, broadcast, and routing.
- **Deterministic lifecycle** - staged shutdown (StopAccept -> Drain ->
  CloseSessions) drops no connection; every connection carries explicit idle
  and write timeouts, so dead peers are reclaimed in bounded time and the
  reclamation is observable.
- **Decoupled extension** - plugins execute as one priority-ordered chain
  with panic isolation; `Codec[M]` layers typed business messages above raw
  transport sessions.

**Target scenarios**

- IoT platform aggregation - devices over CoAP/LwM2M, web clients over
  WebSocket, administration over HTTP.
- Real-time messaging gateways - WebSocket long connections, custom TCP
  protocols, UDP broadcast.
- Edge computing nodes - constrained devices over CoAP, cross-protocol
  routing within one process.

**Boundaries**

- HTTP is a lightweight option only, not a reverse proxy (no Nginx/Envoy
  competition).
- No embedded MQTT broker: external
  [shark-MQTT](https://gitee.com/X1aSheng/shark-mqtt) interoperates through
  a data contract; the gateway connects as an MQTT client (paho adapter).
- gRPC-Web supports Unary and Server Streaming only; not a replacement for
  `google.golang.org/grpc`.

## Design Features

- **Gateway owns the runtime**: the Gateway explicitly creates and injects
  Runtime (SessionManager / PluginRunner / Logger / Metrics / Tracer); transports
  receive runtime dependencies and never create or close shared managers.
- **One plugin runner**: global plugins execute through a single chain with
  panic isolation - a plugin failure never leaks into protocol layers.
- **Staged graceful shutdown**: StagedServer defines StopAccept -> Drain ->
  CloseSessions with clear, rollback-safe semantics - no connection is dropped.
- **Typed messages, raw sessions**: Codec[M] layers typed messages while
  transport sessions stay raw bytes, keeping business types out of the runtime
  layer.
- **Contracts first**: every module talks through interfaces with dependency
  inversion; `core/` defines contracts only, and layers depend one-directionally.
- **Zero-value usability**: Functional Options with sensible defaults everywhere.
- **Failure isolation**: a panic in one connection/goroutine never takes down
  the process; PluginRunner captures plugin panics and returns control errors.
- **Zombie-connection reclaim, observable**: every idle/dead-peer reclaim path
  is bounded by a timeout (TCP read deadline, UDP/CoAP TTL sweep, DTLS read
  deadline, WebSocket/gRPC-Web PongTimeout, configurable QUIC idle timeout) and
  counted in `sessions_reclaimed_total`, so ghost connections can never live
  forever and their reclamation is monitorable.
- **One admission path, three dimensions**: every transport refuses excess
  connections (or pseudo-sessions) through the same `shared.PeerQuota` /
  `shared.Acceptor` - a server-wide ceiling, a per-source-IP ceiling and an
  accept rate. Capacity and per-peer counts are two reads of one counter, so
  they cannot drift apart (ADR-017).
- **Optional block-wise CoAP**: `WithMaxBlockSize` serves responses larger than
  one datagram as RFC 7959 blocks, regenerating the body per request instead of
  caching it (a cache would be an unbounded buffer sized by the peer's request),
  and refusing block-wise uploads with 4.13 rather than executing a fragment as
  a whole body (ADR-018).
- **Bounded handshakes**: the TLS handshake runs under its own budget
  (`TLSHandshakeTimeout`, falling back to `ReadTimeout`) with failure counted in
  `tls_handshake_failed_total`, instead of riding the 5-minute session read
  timeout; WebSocket upgrades carry an explicit `HandshakeTimeout` and buffer
  sizes. A peer that sends half a ClientHello cannot hold a connection slot.
- **Classified accept errors**: `net.ErrClosed` stops the loop, resource
  exhaustion (`EMFILE`/`ENFILE`/`ENOMEM`) is counted separately and backed off,
  and a failure streak collapses into one log line instead of a flood.
- **Hot-reloadable operations, bounded on purpose**: `SIGHUP` swaps the log
  level, the block list, the rate-limit parameters and the shutdown grace period
  without touching a live session; a document that also moves a listener is
  rejected whole rather than half-applied, so the running configuration is
  always one the operator can reason about (ADR-019).
- **Observability first**: metrics and structured logs on the message path;
  a `Tracer` is exposed for embedders who want spans in their own handlers,
  but the runtime itself emits none - `handler_duration_seconds{protocol}`
  already answers "how slow" for a fraction of the cost of a span per message
  (ADR-021). Prometheus metrics use a fixed key set (no cardinality explosion);
  `/healthz` and `/readyz` endpoints plus `sessions_active` /
  `sessions_reclaimed_total` session metrics. Handler latency is a real
  histogram labelled by protocol (`handler_duration_seconds{protocol}`), so
  `histogram_quantile(0.99, ...)` answers on the live process -- and it costs
  zero allocations per message. Rejections and reclamations are labelled the
  same way, so `sum(rate(sessions_rejected_total[5m])) by (protocol)` locates
  the transport doing the refusing instead of a single blended number (ADR-020).
- **Benchmark-driven**: optimizations require benchmark and pprof evidence.
- **Compile-time verification**: `var _ Interface = (*Impl)(nil)` checks, with
  key invariants expressed in the type system.
- **Security built in**: TLS cert hot-reload, mTLS, DTLS, accept rate limiting,
  connection caps, write deadlines, non-root containers, read-only root FS,
  drop-ALL capabilities.

## Feature Matrix

| Area | Status | Notes |
| --- | --- | --- |
| Runtime/Gateway | Implemented | Runtime injection, shared SessionManager, plugin chain, staged stop |
| TCP | Implemented | Length-prefix, line, fixed-size, raw framers, TLS server/client, worker pool, per-connection read buffer (1 KiB default), optional write batching, accept rate limiting, connection caps (global + per-IP), TLS handshake timeout, write deadlines, idle read timeout |
| UDP | Implemented | Pseudo-sessions with session caps (global + per-peer), TTL sweep, DTLS support (configurable read buffer), plugin path |
| HTTP | Implemented | Mode A router and Mode B session/plugin/handler flow, accept rate limiting, connection caps (global + per-IP) answered with a real 503 |
| WebSocket | Implemented | Binary message path, origin check, ping loop, write deadlines, accept rate limiting, connection caps (global + per-IP), upgrade handshake timeout and buffer sizes |
| CoAP | Implemented | Message parse/marshal, CON ACK with RFC 7252 retransmission, dedup with response replay, pseudo-sessions with session caps, DTLS (configurable read buffer), option encoding (RFC 7252), Observe (RFC 7641), block-wise responses (RFC 7959 Block2, opt-in), Block1 refused with 4.13 |
| LwM2M | Implemented | Object/resource model with operation masks, TLV binary codec, discover/register/update/deregister/write/read, Observer notifications |
| QUIC | Implemented | TLS-required stream transport using quic-go, write deadlines, accept rate limiting, connection caps (global + per-IP), configurable idle timeout |
| gRPC-Web | Implemented | Direct HTTP mode, binary framing/trailers, WebSocket mode, connection caps (global + per-IP) |
| Plugins | Implemented | Blacklist (exact + CIDR), RateLimit (32-shard sliding window), Heartbeat, Persistence (Store+MessageLog), AutoBan, SlowHandler, Cluster |
| Security | Implemented | TLS cert hot-reload via file watcher, mTLS client auth, DTLS for UDP/CoAP, per-source-IP quotas on every transport |
| Ops | Implemented | `SIGHUP` reloads log level / blacklist / rate limit / shutdown timeout; listener changes require a restart |
| Persistence | Implemented | Store interface (error-returning), BoltDB backend, durable message log with sequence numbers, session snapshots |
| Infra | Implemented | In-memory cache/store/pubsub/circuitbreaker/observability, Prometheus metrics exporter, OpenTelemetry tracer adapter, TLS cert cache |
| MQTT | Integrated | External broker adapter (paho client), docker-compose mosquitto for E2E tests |
| Zombie reclaim | Implemented | Bounded idle timeouts on every transport, counted via `sessions_reclaimed_total` |
| Fuzz Testing | 8 tests | TCP framers, CoAP message parse, LwM2M TLV codec - all passing |
| Stress Testing | 6 suites | TCP sustained/burst/reconnect + UDP/WebSocket/HTTP with leak detection |
| Benchmark | 6 protocols | TCP, UDP, HTTP, WebSocket, gRPC-Web, QUIC - all benchmarked |
| Deploy | Hardened | Docker (HEALTHCHECK, non-root), K8s (HPA, PDB, NetworkPolicy, ConfigMap), Helm _helpers.tpl |

## Resource Requirements

`shark-socket` has no runtime dependency beyond libc, and builds to a single
static binary, so it deploys on resource-constrained edge nodes.

**Build this way to get the static binary**

```bash
CGO_ENABLED=0 go build -o shark-socket ./cmd/shark-socket
```

The qualifier matters. Nothing in the tree requires cgo, but on a host with a C
toolchain the default `CGO_ENABLED=1` turns on the cgo-enabled DNS resolver and
the result is dynamically linked against `libc.so.6` and
`/lib64/ld-linux-x86-64.so.2`:

```
$ go build -o shark-socket ./cmd/shark-socket && file shark-socket
shark-socket: ELF 64-bit LSB executable, x86-64, dynamically linked, interpreter /lib64/ld-linux-x86-64.so.2

$ CGO_ENABLED=0 go build -o shark-socket ./cmd/shark-socket && file shark-socket
shark-socket: ELF 64-bit LSB executable, x86-64, statically linked
```

Both work on a glibc host, but only the `CGO_ENABLED=0` build runs on a musl
image such as Alpine without glibc compatibility packages. The bundled
Dockerfile already sets it (`alpine:3.22` runtime); a bare `go build` does not.
`CGO_ENABLED=1` is still required for `-race` validation, which is a separate
concern from the shipped artifact.

**Measured footprint** (idle, local dev machine)

- Idle process: ~46 MB private / ~10 MB resident
- Per idle TCP connection: ~24.8 KB (fully released on close - verified with
  2,000 connections)
- Per DTLS peer: 16 KiB read buffer by default (was 64 KiB; configurable via
  `WithDTLSReadBufferBytes`) - 10,000 DTLS peers previously held ~640 MB of
  read buffers alone

**Artifacts**

| Artifact | Size | Notes |
| --- | --- | --- |
| Docker image | ~40 MB | Multi-stage build, `alpine:3.22` runtime, `CGO_ENABLED=0` static binary |
| Executable | single file | Build with `CGO_ENABLED=0` for a static binary; the default build is dynamically linked against libc |

**Kubernetes defaults (bundled manifests)**

| Item | Value |
| --- | --- |
| requests | 50m CPU / 64Mi memory |
| limits | 500m CPU / 256Mi memory |
| replicas | 2 (HPA 2-10, target 50% average CPU utilization) |

**Capacity planning**

| Tier | CPU | Memory | Use case |
| --- | --- | --- | --- |
| Minimum | 50m / 256m | 64Mi / 256Mi | Test / development |
| Recommended | 200m / 1000m | 128Mi / 512Mi | Single-node production |
| High throughput | 500m / 2000m | 256Mi / 1Gi | Tens of thousands of connections |

**Ports**

| Port | Protocol | Purpose |
| --- | --- | --- |
| 18000 | TCP | Business traffic (default) |
| 18080 | HTTP | Prometheus metrics |
| 18081 | HTTP | Health / readiness probes |
| 18443 | UDP | QUIC (TLS required) |
| 18500 | UDP | CoAP / LwM2M |
| 18700 | HTTP/WS | WebSocket |
| 18900 | HTTP | gRPC-Web |

**Benchmark capacity (Linux 8-core Xeon)**

- TCP throughput: ~316k msg/s (50 connections, 256B payload), P50 ~144us, P99 ~401us
- TCP / UDP / HTTP echo latency: ~19us / 5us / 31us
- Plugin-chain overhead: 4 real plugins add only ~1.7% latency (<5%)
- Datagram session lookup (UDP/CoAP): **0 allocations** per packet, ~46 ns
  (was 3 allocations / 32 B, ~99-113 ns). Development-machine measurement, not
  part of the cloud baseline above; see ADR-015
- Connection churn: 50-way concurrency, 859k connect/disconnect cycles in 10s, 0 errors

**Resource footprint per idle TCP connection**

Measured on two Aliyun Linux hosts (2 vCPU / 3622 MB each, Xeon 6982P-C,
Ubuntu 26.04.1, go1.26.9), `ReadTimeout=0`, 6 independent runs with < 0.3% spread.
`HeapInuse` excludes kernel socket buffers, so process RSS is the number to size
deployments against; both are given.

- Resident heap: **6,219 B/connection** at 10k connections (linear: 6,266 B at 1k)
- Goroutines: **2 per connection** (`readLoop` + `writeLoop`), 20,008 at 10k
- Goroutine stacks: **40.3 MB at 10k** (2,112 B per goroutine), i.e. **0.66x the heap**
- Process RSS: **~87 MB at 10k** (peak during connect is 175-184 MB; read RSS at steady state)
- Frame decode: **1 alloc/frame** with the read buffer on (payload only; the header is
  peeked out of the buffer), 2 allocs/frame with `WithReadBufferBytes(0)`
- Guard: a benchmark fails if resident heap exceeds 64 KiB per connection

Since ADR-012 the TCP read path keeps a read buffer per connection. The figures
above were collected before that change, so they understate the current heap by
exactly `ReadBufferBytes`: at 1,000 connections the 1 KiB default moves 6,647 B
to 7,888 B per connection (Windows control run, not the cloud baseline). Add
~1 KiB per connection to the extrapolation below -- 100k connections become
~1.13 GB -- until the Linux ladder is re-measured. `WithReadBufferBytes(0)`
recovers the unbuffered footprint and the old 2 allocs/frame.

At the pre-buffer footprint, 100k connections need ~1.03 GB and 1M connections
~10.3 GB, of which the heap is 60%. Stack size is platform-dependent and this figure is
Linux-only: Windows reserves 8 KB per goroutine against Linux's 2 KB, a 3.89x
difference, so stack figures collected on Windows do not transfer.

Full data, method and comparison against smart-socket:
[`docs/reports/COMPARISON-261017-SMARTBOOT.md`](docs/reports/COMPARISON-261017-SMARTBOOT.md).

## Run

```bash
go run ./cmd/shark-socket
```

The example starts a TCP echo server on `127.0.0.1:18000`.

Run with a configuration file:

```powershell
go run ./cmd/shark-socket -config .\examples\config\multi-protocol.json
```

Health and readiness endpoints are available when `health_addr` is configured:

- `GET /healthz`
- `GET /readyz`

### MQTT Integration Test

```bash
# Start mosquitto broker + run E2E tests (requires Docker)
docker compose -f deploy/docker/docker-compose.yml --profile test run mqtt-test
```

## Validate

| Check | Command | Status |
|-------|---------|--------|
| Unit tests (27 suites) | `go test ./...` | [OK] |
| Race detection | `go test -race ./...` | [OK] |
| Coverage (70% threshold) | `go run scripts/run_tests.go -mode cover` | [OK] 79.6-79.8% |
| Lint (golangci-lint) | `golangci-lint run` | [OK] |
| Security (govulncheck) | `govulncheck ./...` | [OK] |
| Deploy manifests | `go run scripts/run_tests.go -mode deploy` | [OK] |
| Stress (6 suites incl. leak detection) | `go test ./tests/stress/ -count=1 -p 1` | [OK] |
| Benchmark regression | `go run ./scripts/benchgate -base origin/main` | [OK] |

Coverage is reproducible as a range rather than to the digit: the timer-driven
goroutine loops (`Start`, `readLoop`, `pingLoop`) can exit before or after test
teardown, so which of them registers as fully covered varies by about 0.1
percentage point between runs of the same commit.

The benchmark gate compares the merge base against the working tree and fails
when a benchmark gets slower, or starts allocating more per operation. It
measures CPU-only microbenchmarks: socket echo benchmarks on a shared CI runner
move by tens of percent between runs, so gating on them produces noise rather
than signal.

The slowdown rule is a one-sided confidence bound, not a bare ratio. A
benchmark fails only when the pessimistic end of its 95% interval is still above
the 20% limit, which means a benchmark the machine cannot resolve passes and
shows a wide bound instead of inventing a verdict. The 20% allowance is
deliberately loose - it catches the step changes (an extra copy, a lost buffer,
a new lock) that this kind of work actually introduces, and leaves single-digit
noise alone. Allocation counts need no statistics: they are exact integers, so
0 -> 1 per op fails on its own.

Fast validation:

```bash
go run scripts/run_tests.go -mode vet
```

Race validation:

```bash
go run scripts/run_tests.go -mode race
```

The race mode expects these compiler toolchains to be available:

- `D:\Programs\w64devkit\bin`
- `D:\Programs\LLVM\bin`

On Linux runners, race validation uses the runner C toolchain directly.

Equivalent manual commands:

```powershell
go test ./... -count=1
go vet ./...
$env:PATH='D:\Programs\w64devkit\bin;D:\Programs\LLVM\bin;' + $env:PATH
$env:CGO_ENABLED='1'
go test -race ./... -count=1
```

Release hardening commands:

```powershell
go test ./internal/transport/tcp -run='^$' -fuzz=FuzzLengthPrefixFramer -fuzztime=2s
go test ./internal/transport/tcp -run='^$' -fuzz=FuzzLineFramerRead -fuzztime=2s
go test ./internal/transport/coap -run='^$' -fuzz=FuzzParseMessage -fuzztime=2s
go test './internal/transport/tcp' './internal/transport/coap' '-run=^$' '-bench=.' '-benchmem'
```

Scripted test reports:

```powershell
go run scripts/run_tests.go -mode all
go run scripts/run_tests.go -mode unit
go run scripts/run_tests.go -mode integration
go run scripts/run_tests.go -mode benchmark
go run scripts/run_benchmarks.go -profile local -stage light
go run ./scripts/benchgate -base HEAD~1
go run scripts/run_tests.go -mode deploy
```

Docker builds support a configurable module proxy:

```powershell
$env:GOPROXY='https://goproxy.cn,direct'
docker compose -f deploy/docker/docker-compose.yml up -d --build
```

Raw JSON and readable reports are written to `logs/`.

## Documentation

- [Documentation Index](docs/README.md)
- [Architecture](docs/architecture/ARCHITECTURE.md)
- [Contracts & Interfaces](docs/architecture/CONTRACTS.md)
- [Gateway & Runtime](docs/architecture/GATEWAY.md)
- [Deployment](docs/architecture/DEPLOYMENT.md)
- [Configuration Guide](docs/guides/CONFIGURATION-20260530.md)
- [Test Strategy](docs/guides/TEST-STRATEGY-20260529.md)
- [Protocol Test Guide](docs/guides/PROTOCOL-TEST-GUIDE-20260530.md)
- [MQTT Integration](docs/guides/MQTT-INTEGRATION.md)
- [Examples](docs/guides/EXAMPLES.md)
- [Architecture Analysis](docs/reports/ARCHITECTURE-ANALYSIS-260626.md)
- [Architecture Methodology](docs/reports/ARCHITECTURE-METHODOLOGY-260626.md)
- [Latest Project Review (V10)](docs/reports/PROJECT-REVIEW-261016-163106.md)
- [Project Review (V9)](docs/reports/PROJECT-REVIEW-261015-172344.md)
- [Latest Deployment Validation](docs/reports/DEPLOYMENT-VALIDATION-261016-011918.md)
- [Latest Cloud Validation](docs/reports/CLOUD-VALIDATION-261016-SRV23.md)
- [Latest Network Validation (direct)](docs/reports/NETWORK-VALIDATION-261016-092259-DIRECT.md)
- [Report Index](docs/reports/README.md)
- [Changelog](CHANGELOG.md)
