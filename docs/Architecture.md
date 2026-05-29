# Shark-Socket New Architecture

## Design Goals

The redesign focuses on runtime correctness before feature breadth.

1. Keep public APIs small and stable.
2. Make ownership explicit.
3. Let Gateway compose shared runtime dependencies.
4. Keep protocol implementations replaceable.
5. Put typed messages above raw transport sessions through codecs.
6. Make staged shutdown a real protocol contract, not a comment.

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
