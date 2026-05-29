# shark-socket-new

`shark-socket-new` is a redesigned multi-protocol runtime gateway for
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
| TCP | Implemented | Length-prefix, line, fixed-size, raw framers, client, worker pool |
| UDP | Implemented | Pseudo-sessions, TTL sweep, plugin path |
| HTTP | Implemented | Mode A router and Mode B session/plugin/handler flow |
| WebSocket | Implemented | Binary message path, origin check, ping loop |
| CoAP | Implemented | Message parse/marshal, CON ACK, pseudo-sessions |
| LwM2M | Implemented | In-memory lifecycle/resource model with CoAP text-command binding |
| QUIC | Implemented | TLS-required stream transport using quic-go |
| gRPC-Web | Implemented | Direct HTTP mode and WebSocket mode |
| Plugins | Partial | Blacklist, RateLimit, Heartbeat, Persistence, AutoBan |
| Infra | Partial | In-memory cache/store/pubsub/circuitbreaker/observability |
| Deploy | Baseline | Docker, docker-compose, K8s, Helm manifests |

## Run

```bash
go run ./cmd/shark-socket-new
```

The example starts a TCP echo server on `127.0.0.1:18000`.

## Validate

Fast validation:

```powershell
.\scripts\validate.ps1
```

Race validation with the local Windows toolchain:

```powershell
.\scripts\validate.ps1 -Race
```

The race mode expects these compiler toolchains to be available:

- `D:\Programs\w64devkit\bin`
- `D:\Programs\LLVM\bin`

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
.\scripts\validate_deploy.ps1
```

Raw JSON and readable reports are written to `logs/`.

## Documentation

- Architecture: [docs/Architecture.md](docs/Architecture.md)
- Project plan: [docs/PROJECT-PLAN-20260529.md](docs/PROJECT-PLAN-20260529.md)
- Test strategy: [docs/TEST-STRATEGY-20260529.md](docs/TEST-STRATEGY-20260529.md)
