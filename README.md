# shark-socket

[简体中文](README.zh-CN.md) | **English**

## Project Overview

`shark-socket` is a Go multi-protocol gateway runtime for IoT, real-time
messaging, and edge computing. In a single process it hosts connection-
oriented (TCP, WebSocket, QUIC), datagram (UDP, CoAP), and application-layer
(LwM2M, gRPC-Web, HTTP) transports, unified by one session index, one plugin
chain, and one observability plane.

**Essential properties**

- **Single runtime ownership** — the Gateway creates and injects the shared
  Runtime (SessionManager / PluginRunner / Logger / Metrics / Tracer);
  transports consume it and hold no shared state.
- **One session model across protocols** — a single `Session` abstraction
  enables cross-protocol query, broadcast, and routing.
- **Deterministic lifecycle** — staged shutdown (StopAccept → Drain →
  CloseSessions) drops no connection; every connection carries explicit idle
  and write timeouts, so dead peers are reclaimed in bounded time and the
  reclamation is observable.
- **Decoupled extension** — plugins execute as one priority-ordered chain
  with panic isolation; `Codec[M]` layers typed business messages above raw
  transport sessions.

**Target scenarios**

- IoT platform aggregation — devices over CoAP/LwM2M, web clients over
  WebSocket, administration over HTTP.
- Real-time messaging gateways — WebSocket long connections, custom TCP
  protocols, UDP broadcast.
- Edge computing nodes — constrained devices over CoAP, cross-protocol
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
  panic isolation — a plugin failure never leaks into protocol layers.
- **Staged graceful shutdown**: StagedServer defines StopAccept → Drain →
  CloseSessions with clear, rollback-safe semantics — no connection is dropped.
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
- **Observability first**: metrics, traces, and logs on critical paths;
  Prometheus metrics use a fixed key set (no cardinality explosion);
  `/healthz` and `/readyz` endpoints plus `sessions_active` /
  `sessions_reclaimed_total` session metrics.
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
| TCP | Implemented | Length-prefix, line, fixed-size, raw framers, TLS server/client, worker pool, accept rate limiting, connection caps, write deadlines, idle read timeout |
| UDP | Implemented | Pseudo-sessions, TTL sweep, DTLS support (configurable read buffer), plugin path |
| HTTP | Implemented | Mode A router and Mode B session/plugin/handler flow |
| WebSocket | Implemented | Binary message path, origin check, ping loop, write deadlines, accept rate limiting, connection caps |
| CoAP | Implemented | Message parse/marshal, CON ACK, pseudo-sessions, DTLS (configurable read buffer), option encoding (RFC 7252), Observe (RFC 7641) |
| LwM2M | Implemented | Object/resource model with operation masks, TLV binary codec, discover/register/update/deregister/write/read, Observer notifications |
| QUIC | Implemented | TLS-required stream transport using quic-go, write deadlines, accept rate limiting, connection caps, configurable idle timeout |
| gRPC-Web | Implemented | Direct HTTP mode, binary framing/trailers, WebSocket mode, connection caps |
| Plugins | Implemented | Blacklist (exact + CIDR), RateLimit (32-shard sliding window), Heartbeat, Persistence (Store+MessageLog), AutoBan, SlowHandler, Cluster |
| Security | Implemented | TLS cert hot-reload via file watcher, mTLS client auth, DTLS for UDP/CoAP |
| Persistence | Implemented | Store interface (error-returning), BoltDB backend, durable message log with sequence numbers, session snapshots |
| Infra | Implemented | In-memory cache/store/pubsub/circuitbreaker/observability, Prometheus metrics exporter, OpenTelemetry tracer adapter, TLS cert cache |
| MQTT | Integrated | External broker adapter (paho client), docker-compose mosquitto for E2E tests |
| Zombie reclaim | Implemented | Bounded idle timeouts on every transport, counted via `sessions_reclaimed_total` |
| Fuzz Testing | 8 tests | TCP framers, CoAP message parse, LwM2M TLV codec — all passing |
| Stress Testing | 6 suites | TCP sustained/burst/reconnect + UDP/WebSocket/HTTP with leak detection |
| Benchmark | 6 protocols | TCP, UDP, HTTP, WebSocket, gRPC-Web, QUIC — all benchmarked |
| Deploy | Hardened | Docker (HEALTHCHECK, non-root), K8s (HPA, PDB, NetworkPolicy, ConfigMap), Helm _helpers.tpl |

## Resource Requirements

`shark-socket` is statically linked with no external runtime dependency, so it
deploys on resource-constrained edge nodes.

**Measured footprint** (idle, local dev machine)

- Idle process: ~46 MB private / ~10 MB resident
- Per idle TCP connection: ~24.8 KB (fully released on close — verified with
  2,000 connections)
- Per DTLS peer: 16 KiB read buffer by default (was 64 KiB; configurable via
  `WithDTLSReadBufferBytes`) — 10,000 DTLS peers previously held ~640 MB of
  read buffers alone

**Artifacts**

| Artifact | Size | Notes |
| --- | --- | --- |
| Docker image | ~40 MB | Multi-stage build, `alpine:3.22` runtime, `CGO_ENABLED=0` static binary |
| Executable | single file | No runtime dependencies, deployable standalone |

**Kubernetes defaults (bundled manifests)**

| Item | Value |
| --- | --- |
| requests | 50m CPU / 64Mi memory |
| limits | 500m CPU / 256Mi memory |
| replicas | 2 (HPA 2–10, target 50% average CPU utilization) |

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

- TCP throughput: ~316k msg/s (50 connections, 256B payload), P50 ~144µs, P99 ~401µs
- TCP / UDP / HTTP echo latency: ~19µs / 5µs / 31µs
- Plugin-chain overhead: 4 real plugins add only ~1.7% latency (<5%)
- Connection churn: 50-way concurrency, 859k connect/disconnect cycles in 10s, 0 errors

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
| Unit tests (26 suites) | `go test ./...` | ✅ |
| Race detection | `go test -race ./...` | ✅ |
| Coverage (70% threshold) | `go run scripts/run_tests.go -mode cover` | ✅ 78.6% |
| Lint (golangci-lint) | `golangci-lint run` | ✅ |
| Security (govulncheck) | `govulncheck ./...` | ✅ |
| Deploy manifests | `go run scripts/run_tests.go -mode deploy` | ✅ |
| Stress (6 suites incl. leak detection) | `go test ./tests/stress/ -count=1 -p 1` | ✅ |

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
go run scripts/run_tests.go -mode deploy
```

Docker builds support a configurable module proxy:

```powershell
$env:GOPROXY='https://goproxy.cn,direct'
docker compose -f deploy/docker/docker-compose.yml up -d --build
```

Raw JSON and readable reports are written to `logs/`.

## Documentation

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
- [Latest Project Review (V8)](docs/reports/PROJECT-REVIEW-260809-091049.md)
- [Project Review (V7)](docs/reports/PROJECT-REVIEW-260808-220224.md)
- [Project Review (V6)](docs/reports/PROJECT-REVIEW-260806-230955.md)
- [Latest Deployment Validation (V7)](docs/reports/DEPLOYMENT-VALIDATION-260809-085443.md)
- [Deployment Validation (V6)](docs/reports/DEPLOYMENT-VALIDATION-260807-010639.md)
- [Changelog](CHANGELOG.md)
