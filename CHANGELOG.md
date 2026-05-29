# Changelog

All notable changes for `shark-socket-new` are recorded here.

This project uses semantic versioning. Pre-release tags use the form
`vMAJOR.MINOR.PATCH-rc.N`.

## Unreleased

- Fixed Gateway restart lifecycle after shutdown.
- Added GitHub Actions CI for scripted tests, validation, deploy checks, and log artifacts.
- Added project review report `docs/PROJECT-REVIEW-260530-004244.md`.

## v0.1.0-rc.1 - 2026-05-30

Release candidate for the redesigned Shark-Socket runtime gateway.

### Added

- Core runtime contracts for sessions, servers, codecs, plugins, observability, and staged shutdown.
- Gateway runtime composition with shared session management, global plugin execution, duplicate protocol rejection, readiness, health snapshots, rollback on failed start, and staged stop.
- TCP transport with length-prefix, line, fixed-size, and raw framers, a client helper, worker pool policies, runtime plugin integration, and shutdown cleanup.
- UDP transport with remote-address pseudo-sessions, TTL sweeping, runtime plugin execution, and shutdown cleanup.
- HTTP transport with plain router mode, session/plugin handler mode, request body limits, and per-request cleanup.
- WebSocket transport with binary message handling, origin checks, serialized writes, ping loop, runtime plugin execution, and shutdown cleanup.
- CoAP transport with message parse/marshal, CON ACK responses, responder hooks, pseudo-sessions, TTL sweeping, and runtime plugin execution.
- LwM2M in-memory lifecycle/resource model with registration, update, deregistration, lifetime expiry, resource read/write, and CoAP text-command binding.
- QUIC transport using `quic-go`, TLS-required startup, bidirectional stream request/response flow, runtime plugin execution, and shutdown cleanup.
- gRPC-Web transport with direct HTTP mode, binary frame parsing, framed data responses, grpc-status trailer frames, WebSocket mode, max message size limits, origin checks, runtime plugin execution, and session cleanup.
- Plugin ecosystem covering blacklist, rate limit, heartbeat, persistence, autoban, slow handler logging, and cluster pub/sub broadcast.
- Infrastructure primitives for in-memory cache, store, pub/sub, circuit breaker, in-memory observability, Prometheus metrics export, and OpenTelemetry tracing.
- Deployment baseline for Docker, docker-compose, Kubernetes, and Helm, including security contexts, resource requests/limits, liveness/readiness probes, and configurable Helm ports.
- Compile-checked multi-protocol example and examples documentation for TCP, WebSocket, CoAP/LwM2M, Prometheus metrics, and OpenTelemetry tracing.
- Validation tooling for normal, race, deploy, scripted unit/integration/benchmark/all test runs, JSON logs, parsed reports, fuzz smoke tests, and benchmark baselines.

### Validation

- `powershell -ExecutionPolicy Bypass -File .\scripts\validate.ps1`
- `go run scripts/run_tests.go -mode all -timeout 5m`
- `powershell -ExecutionPolicy Bypass -File .\scripts\validate.ps1 -Race`

### Known Scope

- Docker, Kubernetes, and Helm render checks are run when those tools are installed, and otherwise recorded as explicit skips by `scripts/validate_deploy.ps1`.
