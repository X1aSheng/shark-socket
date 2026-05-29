# Shark-Socket New Architecture

## Design Goals

The redesign focuses on runtime correctness before feature breadth.

1. Keep public APIs small and stable.
2. Make ownership explicit.
3. Let Gateway compose shared runtime dependencies.
4. Keep protocol implementations replaceable.
5. Put typed messages above raw transport sessions through codecs.
6. Make staged shutdown a real protocol contract, not a comment.

## Timestamp Format

- General design timestamps use `YYYY-MM-DDTHH:mm:ss`.
- Test and validation logs that need precision use `YYYY-MM-DDTHH:mm:ss.xxx`.
- Timezone suffixes are intentionally omitted from project documents.

## Layers

```text
api/
  Public facade and aliases.

internal/core/
  Stable contracts: Session, SessionManager, Server, Plugin, Runtime, Codec.

internal/runtime/
  Runtime composition: Gateway, session index, plugin chain.

internal/transport/*
  Protocol implementations. Transports receive Runtime from Gateway.

cmd/
  Runnable applications.
```

## Key Decisions

### Raw Session Core

`core.Session` only sends and receives `[]byte`. Typed messages are adapted by
`core.Codec[M]` and `core.AdaptTyped`. This avoids promising compile-time typed
sessions in places where the gateway must store mixed protocols in one manager.

### Runtime Injection

Transports implement:

```go
type RuntimeConfigurable interface {
    UseRuntime(Runtime)
}
```

Gateway calls this before `Start`. This is how global plugins and shared session
management become real execution behavior, not just configuration fields.

### Manager Ownership

Gateway owns the shared `SessionManager`. Transports register and unregister
sessions, but do not close the global manager. This prevents one protocol server
from shutting down the whole gateway session index.

### Staged Shutdown

Transports may implement:

```go
type StagedServer interface {
    StopAccept(context.Context) error
    Drain(context.Context) error
    CloseSessions(context.Context) error
}
```

Gateway calls those stages in order. A transport that does not support staged
shutdown can still implement only `Server`.

### Plugins

Plugins are resolved once into a `PluginRunner` owned by Runtime. Protocols call
the runner on accept, message, and close. Panic isolation lives in the runner,
not inside every transport.

## Adding A Protocol

1. Create `internal/transport/<protocol>`.
2. Implement `core.Server`.
3. Implement `core.RuntimeConfigurable` if the protocol participates in Gateway.
4. Implement `core.StagedServer` if the protocol can stop accepting separately.
5. Keep protocol-specific parsing inside the transport package.
6. Export constructors through `api`.

## Next Protocol Targets

1. UDP pseudo-session registry.
2. WebSocket full-duplex session.
3. HTTP mode A/mode B split with explicit handler registration.
4. QUIC stream lifecycle.
5. CoAP/LwM2M as an application protocol package over UDP primitives.

## Stepwise Validation

### Step 1: TCP Runtime Slice

Scope:

- Gateway runtime injection.
- Global plugin execution on TCP messages.
- TCP echo through length-prefixed frames.
- Staged shutdown with session cleanup.

Command:

```bash
go test ./internal/transport/tcp -run TestGatewayTCPGlobalPluginEchoAndShutdown -count=1 -v
```

Result:

- Passed.
- Verified response payload `global:hello`.
- Verified Gateway session count returns to `0` after shutdown.

### Step 2: Runtime/Core Hardening

Scope:

- Core errors, logger, metrics, tracing, and stage timeout contracts.
- SessionManager capacity, snapshot, broadcast, and close semantics.
- Gateway duplicate protocol rejection, start rollback, staged stop, readiness, and health snapshot.

Commands:

```bash
go test ./internal/runtime -count=1 -v
go test ./... -count=1
```

Result:

- Passed.
- Verified duplicate protocol rejection.
- Verified failed start rolls back previously started servers.
- Verified staged stop calls StopAccept, Drain, and CloseSessions in order.
- Verified session capacity and broadcast behavior.

### Step 3: TCP Production Foundations

Scope:

- Length-prefix, line, fixed-size, and raw framers.
- TCP client for integration and benchmark scenarios.
- Worker pool with block, drop, and close policies.
- Handler-error session close behavior.

Commands:

```bash
go test ./internal/transport/tcp -count=1 -v
go test ./... -count=1
```

Result:

- Passed.
- Verified all built-in framers round-trip payloads.
- Verified oversized length-prefixed frames are rejected.
- Verified TCP client echo through Gateway.
- Verified queue full drop policy and handler-error close behavior.

### Step 4: UDP Runtime Slice

Scope:

- UDP server with pseudo-session registry keyed by remote address.
- Gateway runtime and shared SessionManager injection.
- Global plugin execution on datagram payloads.
- TTL sweep and shutdown cleanup.

Commands:

```bash
go test ./internal/transport/udp -count=1 -v
go test ./... -count=1
```

Result:

- Passed.
- Verified UDP echo with global plugin transform.
- Verified shutdown clears server and runtime sessions.
- Verified inactive pseudo-sessions expire through TTL sweep.

### Step 5: HTTP Runtime Slice

Scope:

- HTTP Mode A plain router.
- HTTP Mode B session/plugin/handler message flow.
- Body size limit.
- Per-request session cleanup.

Commands:

```bash
go test ./internal/transport/http -count=1 -v
go test ./... -count=1
```

Result:

- Passed.
- Verified plain HTTP handler response.
- Verified global plugin transform in Mode B.
- Verified oversized request body returns 413.
- Verified request sessions are unregistered after handler completion.

### Step 6: WebSocket Runtime Slice

Scope:

- WebSocket server over the shared Gateway runtime.
- Global plugin execution on binary messages.
- Serialized session writes and ping loop.
- Origin check rejection.
- Shutdown cleanup through CloseSessions.

Commands:

```bash
go test ./internal/transport/websocket -count=1 -v
go test ./... -count=1
```

Result:

- Passed.
- Verified WebSocket echo with global plugin transform.
- Verified origin rejection path.
- Verified Gateway shutdown clears WebSocket runtime sessions.

### Step 7: Basic Plugin Ecosystem

Scope:

- Blacklist plugin with exact IP and CIDR support.
- RateLimit plugin with per-remote fixed-window message limiting.
- Public API constructors for both plugins.

Commands:

```bash
go test ./internal/plugin -count=1 -v
go test ./... -count=1
```

Result:

- Passed.
- Verified blacklist blocks exact IP matches.
- Verified rate limit drops messages over the configured window quota.

### Step 8: CoAP Runtime Slice

Scope:

- Minimal RFC 7252 message header parse/marshal.
- UDP-backed CoAP pseudo-sessions through Gateway runtime.
- CON request ACK generation.
- Global plugin execution on CoAP payloads.
- TTL sweep and shutdown cleanup.

Commands:

```bash
go test ./internal/transport/coap -count=1 -v
go test ./... -count=1
```

Result:

- Passed.
- Verified CoAP message round-trip and invalid version rejection.
- Verified CON POST receives ACK Created with the same Message ID.
- Verified inactive CoAP pseudo-sessions expire through TTL sweep.

### Step 9: LwM2M Lifecycle Model

Scope:

- LwM2M object path parsing.
- In-memory registration, update, deregistration, and lifetime expiry.
- Resource read/write model.
- Public API constructors for LwM2M server/client.

Commands:

```bash
go test ./internal/protocol/lwm2m -count=1 -v
go test ./... -count=1
```

Result:

- Passed.
- Verified registration lifecycle and resource read/write.
- Verified expired registrations are swept.

### Step 10: QUIC Runtime Slice

Scope:

- QUIC Gateway transport using quic-go.
- TLS configuration requirement.
- Bidirectional stream request and response flow.
- Global plugin execution on stream payloads.
- Shutdown cleanup through Gateway runtime.

Commands:

```bash
go test ./internal/transport/quic -count=1 -v
go test ./... -count=1
```

Result:

- Passed.
- Verified QUIC refuses to start without TLS config.
- Verified QUIC echo with global plugin transform.
- Verified Gateway shutdown clears QUIC runtime sessions.

### Step 11: gRPC-Web Direct Runtime Slice

Scope:

- Direct HTTP gRPC-Web transport through Gateway runtime.
- Max message size boundary.
- Global plugin execution on request payloads.
- Per-request session cleanup.

Commands:

```bash
go test ./internal/transport/grpcweb -count=1 -v
go test ./... -count=1
```

Result:

- Passed.
- Verified direct gRPC-Web echo with global plugin transform.
- Verified oversized request returns 413.
- Verified runtime sessions are cleaned after request completion.

### Step 12: Core Infra Primitives

Scope:

- In-memory cache with TTL.
- In-memory store.
- In-process pubsub.
- Circuit breaker state transitions.

Commands:

```bash
go test ./internal/infra/... -count=1 -v
go test ./... -count=1
```

Result:

- Passed.
- Verified cache set/get/expiry.
- Verified store save/load/delete.
- Verified pubsub delivery.
- Verified circuit breaker closed/open/half-open/closed flow.

### Step 13: Deployment Baseline

Scope:

- Docker multi-stage build.
- docker-compose service.
- Kubernetes Deployment and Service manifests.
- Helm chart baseline.
- Static deploy tests.

Commands:

```bash
go test ./tests/deploy -count=1 -v
go test ./... -count=1
```

Result:

- Passed.
- Verified Dockerfile builds `cmd/shark-socket-new`.
- Verified Dockerfile has an entrypoint.
- Verified K8s and Helm baseline manifests exist.

### Step 14: Release Validation Pass

Scope:

- Full package test sweep.
- Static vet check.
- Race-test attempt with environment diagnostics.

Commands:

```bash
go test ./... -count=1
go vet ./...
$env:PATH='D:\Programs\w64devkit\bin;D:\Programs\LLVM\bin;' + $env:PATH
$env:CGO_ENABLED='1'
go test -race ./... -count=1
```

Result:

- `go test ./... -count=1`: passed.
- `go vet ./...`: passed.
- `go test -race ./... -count=1`: passed with `w64devkit`/`LLVM` on `%PATH%`.

### Step 15: Expanded Plugin Ecosystem

Scope:

- Heartbeat plugin for sweeping idle sessions.
- Persistence plugin for lifecycle event storage.
- AutoBan plugin for threshold-based connection blocking.
- Public API constructors for expanded plugins.

Commands:

```bash
go test ./internal/plugin -count=1 -v
go test ./... -count=1
```

Result:

- Passed.
- Verified heartbeat closes and unregisters idle sessions.
- Verified persistence writes lifecycle events.
- Verified AutoBan blocks remotes after threshold is reached.

### Step 16: Observability Test Primitives

Scope:

- In-memory metrics implementation for tests and local diagnostics.
- In-memory logger implementation for runtime assertions.

Commands:

```bash
go test ./internal/infra/observability -count=1 -v
go test ./... -count=1
```

Result:

- Passed.
- Verified counter, gauge, and histogram storage.
- Verified structured log capture.

### Step 17: Infra And Heartbeat Hardening

Scope:

- Circuit breaker Execute wrapper, half-open single-probe gate, and state snapshots.
- Cache maintenance APIs: Has, Len, Sweep, and Clear.
- Heartbeat idempotent Start/Stop and automatic idle-session sweep.

Commands:

```bash
go test ./internal/infra/circuitbreaker ./internal/infra/cache ./internal/plugin -count=1 -v
go test ./... -count=1
$env:PATH='D:\Programs\w64devkit\bin;D:\Programs\LLVM\bin;' + $env:PATH
$env:CGO_ENABLED='1'
go test -race ./... -count=1
```

Result:

- Passed.
- Verified circuit breaker execute/open/half-open/snapshot behavior.
- Verified cache maintenance and expiry cleanup.
- Verified heartbeat loop sweeps idle sessions and Stop is idempotent.

### Step 18: Documentation And Validation Script

Scope:

- README updated from initial architecture spike wording to current multi-protocol implementation status.
- Project plan updated to make completed and remaining work explicit.
- Windows validation script added for normal and race validation.

Commands:

```bash
powershell -ExecutionPolicy Bypass -File .\scripts\validate.ps1
powershell -ExecutionPolicy Bypass -File .\scripts\validate.ps1 -Race
```

Result:

- Passed at 2026-05-29T12:21:22.812.
- Verified `go test ./... -count=1`.
- Verified `go vet ./...`.
- Verified `go test -race ./... -count=1` with `w64devkit`/`LLVM` on `%PATH%`.

### Step 19: LwM2M Over CoAP Binding

Scope:

- CoAP responder option for request/response payload flows.
- LwM2M CoAP payload adapter for `register`, `update`, `deregister`, `write`, and `read`.
- Public API helpers for CoAP responder wiring.

Wire Shape:

```text
register <endpoint> <lifetime-seconds> [object-path...]
update <endpoint> <lifetime-seconds>
deregister <endpoint>
write <endpoint> <resource-path> <value>
read <endpoint> <resource-path>
```

Commands:

```bash
go test ./internal/protocol/lwm2m ./internal/transport/coap -count=1 -v
powershell -ExecutionPolicy Bypass -File .\scripts\validate.ps1
```

Result:

- Passed at 2026-05-29T12:24:31.075.
- Verified CoAP CON ACK response payloads for LwM2M register/write/read/deregister.

### Step 20: gRPC-Web WebSocket Mode

Scope:

- Optional WebSocket mode for the gRPC-Web transport.
- WebSocket sessions report protocol `grpc-web` and use the same runtime plugin chain as direct HTTP mode.
- Max message size and origin checks apply to WebSocket mode.

Commands:

```bash
go test ./internal/transport/grpcweb -count=1 -v
powershell -ExecutionPolicy Bypass -File .\scripts\validate.ps1
```

Result:

- Passed at 2026-05-29T12:27:15.243.
- Verified direct HTTP mode still passes.
- Verified WebSocket echo, max-size close behavior, origin rejection, and session cleanup.

### Step 21: TCP And CoAP Fuzz/Benchmark Baseline

Scope:

- TCP length-prefix and line framer fuzz smoke tests.
- CoAP message parse/marshal fuzz smoke test.
- Benchmark baseline for TCP framers and CoAP parse/marshal.

Commands:

```bash
go test ./internal/transport/tcp ./internal/transport/coap -count=1 -v
go test ./internal/transport/tcp -run=^$ -fuzz=FuzzLengthPrefixFramer -fuzztime=2s
go test ./internal/transport/tcp -run=^$ -fuzz=FuzzLineFramerRead -fuzztime=2s
go test ./internal/transport/coap -run=^$ -fuzz=FuzzParseMessage -fuzztime=2s
go test './internal/transport/tcp' './internal/transport/coap' '-run=^$' '-bench=.' '-benchmem'
powershell -ExecutionPolicy Bypass -File .\scripts\validate.ps1
```

Benchmark Baseline:

```text
BenchmarkLengthPrefixFramerRoundTrip-16    249.4 ns/op    664 B/op    6 allocs/op
BenchmarkLineFramerRoundTrip-16           2496 ns/op     1840 B/op   12 allocs/op
BenchmarkMessageParse-16                    88.33 ns/op   264 B/op    2 allocs/op
BenchmarkMessageMarshal-16                 101.2 ns/op    304 B/op    3 allocs/op
```

Result:

- Passed at 2026-05-29T12:30:30.148.
- Verified fuzz smoke for TCP length-prefix, TCP line framing, and CoAP parse/marshal.
- Verified full validation script after adding fuzz and benchmark tests.

### Step 22: shark-socket-Style Test Logging

Scope:

- Test strategy documented from the mature `shark-socket` test approach.
- Scripted runner added for unit, integration, benchmark, race, cover, and all modes.
- Raw `go test -json` logs and readable parsed reports are written to `logs/`.
- `validate.ps1` now records a transcript log with millisecond timestamps.

Commands:

```bash
go test ./scripts -count=1 -v
go run scripts/run_tests.go -mode unit -timeout 5m
powershell -ExecutionPolicy Bypass -File .\scripts\validate.ps1
```

Result:

- Passed at 2026-05-29T12:49:05.843.
- Logging files use `YYYY-MM-DDTHH-mm-ss.xxx_<mode>.json`, `.log`, and `_validate.log`.
- Verified parser tests, unit script mode, integration script mode, benchmark script mode, and full validate transcript.

### Step 23: Deploy Validation Depth

Scope:

- Static deploy tests now assert K8s and Helm manifest semantics, not only file presence.
- Optional deploy validation script records Docker Compose, Kubectl Kustomize, and Helm Template results when tools are installed.
- Missing deploy tools are explicitly logged as `SKIP`.

Commands:

```bash
go test ./tests/deploy -count=1 -v
powershell -ExecutionPolicy Bypass -File .\scripts\validate_deploy.ps1
powershell -ExecutionPolicy Bypass -File .\scripts\validate.ps1
```

Result:

- Passed at 2026-05-29T12:58:24.359.
- Static deploy tests passed.
- `docker`, `kubectl`, and `helm` were not installed on this machine and were recorded as explicit `SKIP` entries in `logs\2026-05-29T12-58-14.009_deploy.log`.
- Full validation script passed after deploy test hardening.

### Step 24: Final Race Refresh After Deploy Hardening

Scope:

- Full validation with race detector after deploy validation changes.
- Confirms deploy test additions do not introduce race-only failures.

Commands:

```bash
powershell -ExecutionPolicy Bypass -File .\scripts\validate.ps1 -Race
```

Result:

- Passed at 2026-05-29T12:58:54.421.
- Verified `go test ./... -count=1`.
- Verified `go vet ./...`.
- Verified `go test -race ./... -count=1` using local `w64devkit` and `LLVM` paths.

### Step 25: Slow Handler Logging Adapter

Scope:

- Slow-query style handler adapter for production diagnostics.
- Logs session ID, protocol, duration in milliseconds, payload size, and handler error when processing exceeds threshold.
- Public API facade exposes `NewSlowHandler`.

Commands:

```bash
go test ./internal/plugin -count=1 -v
powershell -ExecutionPolicy Bypass -File .\scripts\validate.ps1
```

Result:

- Passed at 2026-05-29T13:01:11.672.
- Verified slow handler logs only over-threshold calls and preserves handler errors.
- Full validation script passed after API exposure.

### Step 26: Prometheus Metrics Exporter

Scope:

- Prometheus text-format metrics exporter behind the existing `core.Metrics` interface.
- Supports counters, gauges, and summary-style histogram count/sum output.
- Provides `ServeHTTP` for direct `/metrics` mounting.
- Public API facade exposes `NewPrometheusMetrics`.

Commands:

```bash
go test ./internal/infra/observability -count=1 -v
powershell -ExecutionPolicy Bypass -File .\scripts\validate.ps1
```

Result:

- Passed at 2026-05-29T13:50:35.220.
- Verified counter, gauge, histogram count/sum, label escaping, value-only labels, and HTTP serving.
- Fixed label double-escaping during focused validation.
- Full validation script passed after API exposure.

### Step 27: Cluster Pub/Sub Plugin

Scope:

- Cluster plugin publishes local messages into shared pub/sub with a node envelope.
- Remote nodes subscribe and broadcast payloads to their local SessionManager.
- The plugin ignores messages from its own node to prevent local loopback.
- Public API facade exposes `NewPubSub` and `NewClusterPlugin`.

Commands:

```bash
go test ./internal/plugin -count=1 -v
powershell -ExecutionPolicy Bypass -File .\scripts\validate.ps1
```

Result:

- Passed at 2026-05-29T13:53:26.916.
- Verified remote node broadcast, own-node loopback suppression, nil bus no-op, and plugin conformance.
- Full validation script passed after API exposure.
