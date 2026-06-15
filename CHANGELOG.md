# Changelog

All notable changes for `shark-socket` are recorded here.

This project uses semantic versioning. Pre-release tags use the form
`vMAJOR.MINOR.PATCH-rc.N`.

## Unreleased

### Benchmark & Test Hardening (2026-06-15)

#### Port Exhaustion Fixes (Windows)
- Added `WithClientLinger(0)` option to TCP client — sends RST on close, avoiding TIME_WAIT.
- Added `lingerTransport()` helper for HTTP/gRPC-Web benchmarks — `SetLinger(0)` via `DialContext`.
- Added `portCooldown()` to `run_benchmarks.go` — 3s wait between groups on Windows.
- Added `integration_helpers_test.go` to http/grpcweb/websocket packages — `init()` replaces `http.DefaultTransport` and websocket `DefaultDialer` with Linger(0) dialer.
- Fixed `TestGatewayTCPRestartKeepsSessionManagerUsable` — relaxed port reuse check for fast recycling.
- Fixed `parse_test_log_test.go` — updated timestamp assertion for millisecond-free format.
- All 27 network benchmarks now pass on Windows with zero port exhaustion failures.

#### Benchmark Structural Improvements
- Added `concurrentClientsForOS()` — platform-aware concurrency caps (Windows: 50, Linux: 500).
- Added `BENCH_MAX_CONNS` env var to override concurrency levels.
- Added read deadlines to UDP and WebSocket concurrent benchmarks.
- Unified HTTP client timeout to 5s across all single-connection benchmarks.
- Fixed gRPC-Web error handling — `io.ReadAll` and `Body.Close` errors now checked.
- Fixed `BenchmarkTCPEcho_Concurrent` — each goroutine now creates its own dedicated client (was shared, causing data corruption).
- Fixed `BenchmarkWSEcho_Concurrent` — each goroutine creates its own WebSocket connection.

#### Benchmark Architecture
- Extracted shared `echoHarness`, `echoHandler`, `newEchoHarness`, `getAddr` helpers.
- Added `newEchoHarnessWithPlugins` for plugin benchmarks.
- Added `skipIfShort()` for fast smoke-test mode.
- Refactored PayloadSize (6) and Concurrent (5) benchmarks — server created once, shared across sub-benchmarks (44→11 server creations).
- Refactored single-echo (6) and plugin (4) benchmarks to use `echoHarness`.
- Added PluginChain UDP/WS benchmarks (6 new: Blacklist/RateLimit/FullChain × UDP + WS).
- Added `BenchmarkQUICEcho_Concurrent` (documented with Skip for QUIC stream limitations).
- Fixed `payloadSizes` max: `65536→65507` (safe UDP datagram limit).

#### Orchestration
- `run_benchmarks.go`: added `-list` flag (17 groups), `-bench <name>` filter, extracted `allBenchmarkGroups()`.
- Replaced `validate.ps1` and `validate_deploy.ps1` with `run_tests.go -mode vet` and `-mode deploy`.
- Removed millisecond precision from all timestamp formats.

#### Documentation
- Updated README coverage to 74.9%.
- Updated CI workflow to use Go runner instead of PowerShell scripts.
- Updated all active documentation references to use `run_tests.go` commands.

### Review Fixes (2026-06-02 Evening)
- Fixed TCP RawFramer fuzz behavior for empty raw payload reads.
- Fixed LwM2M TLV fuzz tests after field rename and added value length validation.
- Fixed PowerShell validation scripts so native command failures fail CI correctly.
- Fixed QUIC benchmark response handling to read the server-initiated stream.
- Revalidated local Go tests, race, coverage, deploy static tests, and cloud Docker smoke tests.

### Comprehensive Review Fixes (2026-06-02)

#### Critical (5 fixes)
- Fixed data race on `allowance` in shared Acceptor (mutex for rate limiting).
- Fixed DTLS goroutine leaks in UDP and CoAP transports (track + close connections).
- Fixed unbounded memory leak in CoAP dedup map (periodic cleanup).
- Fixed QUIC double-invoke of OnClose/Unregister (LoadAndDelete guard).
- Fixed data race on `clientCAFile` in tlsutil CertCache.

#### High (7 fixes)
- Fixed CoAP Observe sequence encoding inconsistency (variable-length BE).
- Added BoltDB closed-state guard with sync.RWMutex.
- Added BulkDeleter interface + BoltDB batch delete for MessageLog.
- Fixed TCP accept loop spin on persistent errors (100ms backoff).
- Added nil guard to PluginChain.Append (filter nil plugins).
- Panicking plugins return ErrPluginPanic instead of silently succeeding.
- Added nil guards to Gateway.Register and SessionManager.Register.

#### Medium (8 fixes)
- Added sync.RWMutex to PluginChain for thread safety.
- Fixed TOCTOU in SessionManager.Register capacity check.
- Added double-start guard to TCP Server (atomic.Bool).
- WebSocket pingLoop closes session on failure (prevent zombie).
- Added Acceptor rate limiting to gRPC-Web direct + WebSocket modes.
- Added sync.Once to gRPC-Web session.Close().
- Fixed cert watchers to use app lifecycle context instead of Background.
- Fixed parseUint64 to return error instead of silently returning 0.

#### Deployment Hardening
- Docker: ca-certificates, wget, HEALTHCHECK, UID 1000, .dockerignore.
- K8s: namespace, ServiceAccount, ConfigMap, NetworkPolicy, PDB, HPA.
- Helm: _helpers.tpl, NOTES.txt, fsGroup, serviceAccountName.
- CI: golangci-lint + govulncheck jobs, .golangci.yml config.

#### Test Coverage
- Added WebSocket TLS (WSS) integration test.
- Added gRPC-Web TLS integration test.
- Added CoAP Observe E2E tests (4 test functions).
- Fixed data race in CoAP duplicate CON test handler.

#### Cloud Validation
- ✅ Server 1 (120.76.44.233): Go 1.26.3 build, test, race, Docker deploy, client test.
- ✅ Server 2 (47.110.238.85): Go 1.26.3 build, test, race, coverage, Docker deploy, concurrent 64KB.

### Coverage Improvements (2026-06-02)
- Core package: 0% → 100% (17 tests)
- API package: 0% → 77.4% (44 tests)
- Runtime package: 64.5% → 88.2% (20 tests)
- UDP transport: 51.1% → 71.5% (20 tests)
- MQTT adapter: 0% → 59.1% (13 tests)
- Plugin package: 71.2% → 79.3% (8 tests)
- LwM2M protocol: 67% → 73.9% (5 tests)
- App package: 67.9% → 73.4% (4 tests)
- CoAP transport: 67.6% → 69.3% (4 tests)

### Latest Fixes & Enhancements
- MQTT integration: mosquitto broker in docker-compose, E2E tests pass on dual cloud servers.
- Fuzz testing: TCP framers, LwM2M TLV codec (11 fuzz tests total).
- Benchmark: gRPC-Web + QUIC benchmarks added (6 protocols covered).
- CoAP: message edge cases, option encoding, extended deltas (coverage 69% → 76%).
- UDP/CoAP: session ID allocation fix (defer NextID until confirmed new session).
- Health/metrics: error propagation via App.ServeErrors().
- K8s: explicit ClusterIP type, protocol fields on service ports.
- CI: PR branch filter, cross-platform path separators, missing strconv import fix.
- Docs: ARCHITECTURE test matrix, SECURITY Docker hardening updated.

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
