# Changelog

All notable changes for `shark-socket` are recorded here.

This project uses semantic versioning. Pre-release tags use the form
`vMAJOR.MINOR.PATCH-rc.N`.

## Unreleased

### Security (Phase 1)
- Added TLS certificate hot-reload via file watcher and `GetCertificate` callback.
- Added DTLS support for UDP transport using pion/dtls v3.
- Wired CoAP DTLS and UDP DTLS from JSON config and environment overrides.
- Fixed TCP sentinel error to use `core.ErrWriteQueueFull` instead of raw `errors.New`.

### Resilience (Phase 2)
- Added configurable write deadlines on TCP (30s default), QUIC (30s default), and WebSocket (30s default).
- Added token-bucket accept rate limiter with atomic max-connections counter (TCP, QUIC, WebSocket).
- Changed TCP worker pool default full-policy from `PolicyBlock` to `PolicyDrop`.
- Added write buffer high-water-mark threshold configuration on TCP.

### IoT Protocol Depth (Phase 3)
- Expanded LwM2M object model with `ResourceType`, `OperationMask`, `ObjectDefinition`, `ResourceDefinition`, and `DeviceInfo`.
- Added OMA LwM2M TLV binary codec (`[type][id(2B)][length(2B)][value]`) with encode/decode/round-trip support.
- Added LwM2M object registry with operation validation in `Write()`.
- Added `discover` command to LwM2M CoAP responder.
- Added CoAP option delta encoding/decoding per RFC 7252.
- Added CoAP Observe (RFC 7641) — `ObserverRegistry` with Register/Remove/Notify/RemoveBySession, wired to LwM2M `OnWrite` callback.

### Durable Persistence (Phase 4)
- Added `StoreV2` interface with error-returning `SaveV2`/`LoadV2`/`DeleteV2`/`List`/`Close`.
- Added BoltDB-backed `BoltStore` implementing `StoreV2`.
- Added `MessageLog` — durable append-only message log with auto-incrementing sequence numbers, replay, and prune.
- Added `PersistenceV2` plugin using `StoreV2` with `OnMessage` hook appending to `MessageLog`.
- Added `SessionStore` — JSON session snapshot save/load/list/delete for restart recovery.

### Defect Fixes (2026-06-02 Comprehensive Review)

#### Critical Fixes
- Fixed data race on `allowance` field in shared Acceptor (added mutex for rate limiting).
- Fixed DTLS goroutine leaks in UDP and CoAP transports (track and close connections on shutdown).
- Fixed unbounded memory leak in CoAP dedup map (periodic cleanup goroutine).
- Fixed QUIC double-invoke of OnClose/Unregister (use LoadAndDelete for idempotency).
- Fixed data race on `clientCAFile` in tlsutil CertCache (added mutex to SetClientCA).
- Added nil guards on exported `Gateway.Register` and `SessionManager.Register`.

#### High Priority Fixes
- Fixed CoAP Observe sequence encoding inconsistency (variable-length big-endian encoding).
- Added BoltDB closed-state guard with mutex (operations return ErrClosed after close).
- Added BulkDeleter interface and BoltDB batch delete for MessageLog bulk operations.
- Fixed TCP accept loop spin on persistent errors (added 100ms backoff).
- Added nil guard and validation in PluginChain.Append.
- Panicking plugins now return ErrPluginPanic instead of silently succeeding.

#### Deployment Hardening
- Added ca-certificates, wget, and HEALTHCHECK to Dockerfile.
- Fixed Docker UID to 1000 for K8s compatibility.
- Added .dockerignore to reduce build context.
- Fixed docker-compose YAML ambiguity and added tmpfs mounts.
- Added K8s namespace, ServiceAccount, ConfigMap, NetworkPolicy, PDB, HPA manifests.
- Added Helm _helpers.tpl and NOTES.txt templates.
- Added golangci-lint and govulncheck CI jobs.

#### Test & Coverage
- Added WSS/TLS tests for WebSocket transport.
- Added TLS tests for gRPC-Web transport.
- Added CoAP Observe E2E tests (4 test functions).
- Fixed scripts: `./api/...` to `./api`, removed duplicate test from validate.ps1.
- Added comprehensive project review document.

## v0.1.0 - 2026-05-30

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
