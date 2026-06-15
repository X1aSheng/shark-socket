# shark-socket

`shark-socket` is a redesigned multi-protocol runtime gateway for
Shark-Socket. It keeps the useful ideas from the original project while making
runtime ownership, plugin execution, and graceful shutdown explicit.

## Design Principles

- Gateway owns global runtime composition.
- Transports receive runtime dependencies and do not close shared managers.
- Global plugins are applied through one plugin runner.
- Graceful shutdown is staged through optional transport capabilities.
- Typed messages are layered through codecs while transport sessions stay raw.

## Feature Matrix

| Area | Status | Notes |
| --- | --- | --- |
| Runtime/Gateway | Implemented | Runtime injection, shared SessionManager, plugin chain, staged stop |
| TCP | Implemented | Length-prefix, line, fixed-size, raw framers, TLS server/client, worker pool, accept rate limiting, write deadlines |
| UDP | Implemented | Pseudo-sessions, TTL sweep, DTLS support, plugin path |
| HTTP | Implemented | Mode A router and Mode B session/plugin/handler flow |
| WebSocket | Implemented | Binary message path, origin check, ping loop, write deadlines, accept rate limiting |
| CoAP | Implemented | Message parse/marshal, CON ACK, pseudo-sessions, DTLS, option encoding (RFC 7252), Observe (RFC 7641) |
| LwM2M | Implemented | Object/resource model with operation masks, TLV binary codec, discover/register/update/deregister/write/read, Observer notifications |
| QUIC | Implemented | TLS-required stream transport using quic-go, write deadlines, accept rate limiting |
| gRPC-Web | Implemented | Direct HTTP mode, binary framing/trailers, and WebSocket mode |
| Plugins | Implemented | Blacklist, RateLimit, Heartbeat, Persistence V1+V2, AutoBan, SlowHandler, Cluster |
| Security | Implemented | TLS cert hot-reload via file watcher, mTLS client auth, DTLS for UDP/CoAP |
| Persistence | Implemented | StoreV2 interface, BoltDB backend, durable message log with sequence numbers, session snapshots |
| Infra | Implemented | In-memory cache/store/pubsub/circuitbreaker/observability, Prometheus metrics exporter, OpenTelemetry tracer adapter, TLS cert cache |
| MQTT | Integrated | External broker adapter (paho client), docker-compose mosquitto for E2E tests |
| Fuzz Testing | 11 tests | TCP framers, CoAP message parse, LwM2M TLV codec — all passing |
| Benchmark | 6 protocols | TCP, UDP, HTTP, WebSocket, gRPC-Web, QUIC — all benchmarked |
| Deploy | Hardened | Docker (HEALTHCHECK, non-root), K8s (HPA, PDB, NetworkPolicy, ConfigMap), Helm _helpers.tpl |

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
| Unit tests (25 suites) | `go test ./...` | ✅ |
| Race detection | `go test -race ./...` | ✅ |
| Coverage (50% threshold) | `go run scripts/run_tests.go -mode cover` | ✅ 72.1% |
| Lint (golangci-lint) | `golangci-lint run` | ✅ |
| Security (govulncheck) | `govulncheck ./...` | ✅ |
| Deploy manifests | `go run scripts/run_tests.go -mode deploy` | ✅ |

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

- Architecture: [docs/Architecture.md](docs/Architecture.md)
- Project plan: [docs/PROJECT-PLAN-20260529.md](docs/PROJECT-PLAN-20260529.md)
- Implementation goals: [docs/IMPLEMENTATION-GOALS-20260530.md](docs/IMPLEMENTATION-GOALS-20260530.md)
- Configuration: [docs/CONFIGURATION-20260530.md](docs/CONFIGURATION-20260530.md)
- Test strategy: [docs/TEST-STRATEGY-20260529.md](docs/TEST-STRATEGY-20260529.md)
- Protocol test guide: [docs/PROTOCOL-TEST-GUIDE-20260530.md](docs/PROTOCOL-TEST-GUIDE-20260530.md)
- Resource-limited benchmark flow: [docs/BENCHMARK-RESOURCE-LIMITED-TEST-FLOW-20260530.md](docs/BENCHMARK-RESOURCE-LIMITED-TEST-FLOW-20260530.md)
- Latest cloud benchmark: [docs/BENCHMARK-RESULT-260530-123500-DUAL-CLOUD.md](docs/BENCHMARK-RESULT-260530-123500-DUAL-CLOUD.md)
- Examples: [docs/EXAMPLES.md](docs/EXAMPLES.md)
- Latest review: [docs/PROJECT-REVIEW-260602-213050.md](docs/PROJECT-REVIEW-260602-213050.md)
- Changelog: [CHANGELOG.md](CHANGELOG.md)
