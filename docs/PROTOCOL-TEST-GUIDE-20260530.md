# Protocol Test Guide

Updated: 2026-05-30T01:05:00

## Purpose

This guide defines repeatable testing methods for each communication protocol
implemented by `shark-socket-new`. It complements package tests and scripted
validation logs by naming the behavior that must be protected before release.

## Common Test Model

Every protocol should be tested through five layers:

| Layer | Goal | Examples |
| --- | --- | --- |
| Contract | Verify public options, constructors, and protocol identity | default values, nil-safe options, `Protocol()` |
| Codec/framing | Verify wire parse and marshal rules | frame size, malformed headers, duplicate packets |
| Runtime integration | Verify Gateway injection and shared plugins/sessions | `OnAccept`, `OnMessage`, `OnClose`, session count cleanup |
| Failure behavior | Verify expected errors do not leak sessions or goroutines | body limits, handler errors, plugin drop/block |
| Release gates | Verify platform and deploy confidence | `go test`, race, fuzz, benchmark, deploy render |

Common assertions:

- Use `127.0.0.1:0` for network tests.
- Set client read/write deadlines in integration tests.
- Assert session cleanup after close or request completion.
- Preserve every discovered defect as a focused regression test.
- Prefer protocol-level malformed inputs over implementation-only mocks.
- Keep tests deterministic; avoid sleeps except TTL/sweep tests with bounded deadlines.

## TCP

Core risks:

- Framing correctness and oversized frame rejection.
- Worker queue pressure and handler failure cleanup.
- Gateway restart and session lifecycle correctness.
- Plugin drop/block behavior without closing healthy connections accidentally.

Required tests:

- Length-prefix, line, fixed-size, and raw framer round trips.
- Oversized frame read/write rejection.
- Worker pool block/drop/close policies.
- Echo through Gateway with global plugin transform.
- Restart regression: `Start -> Stop -> Start` keeps sessions usable.
- Plugin drop regression: dropped frames are not delivered to handlers and later frames still work.

Release gates:

```powershell
go test ./internal/transport/tcp -count=1 -v
go test ./internal/transport/tcp -run='^$' -fuzz=FuzzLengthPrefixFramer -fuzztime=2s
go test ./internal/transport/tcp -run='^$' -fuzz=FuzzLineFramerRead -fuzztime=2s
```

## UDP

Core risks:

- Remote-address pseudo-session lifecycle.
- Datagram copy safety.
- TTL sweep cleanup in both server-local and Gateway session managers.
- Plugin drop/block behavior for connectionless traffic.

Required tests:

- Echo through Gateway with global plugin transform.
- Session TTL expiry and runtime cleanup.
- Plugin drop suppresses handler execution and response without tearing down the pseudo-session.
- Max datagram boundary for configured buffer sizes.

Release gate:

```powershell
go test ./internal/transport/udp -count=1 -v
```

## HTTP

Core risks:

- Mode A router and Mode B session handler staying isolated.
- Body size enforcement.
- Per-request session cleanup.
- Error status mapping for plugin and handler failures.

Required tests:

- Plain router response.
- Session/plugin/handler echo and cleanup.
- Body limit returns `413`.
- Unsupported or dropped requests map to deterministic status codes.
- Handler error returns `500` and still cleans the session.

Release gate:

```powershell
go test ./internal/transport/http -count=1 -v
```

## WebSocket

Core risks:

- Origin checking.
- Binary/text message handling policy.
- Serialized writes for handler and ping loop.
- Max message size and close cleanup.

Required tests:

- Binary echo through Gateway with plugin transform.
- Origin rejection.
- Max message size violation closes the connection and cleans sessions.
- Handler error closes the session and removes it from Gateway.

Release gate:

```powershell
go test ./internal/transport/websocket -count=1 -v
```

## CoAP

Core risks:

- RFC 7252 header parse/marshal safety.
- Token length and payload marker handling.
- Confirmable message ACK behavior.
- Duplicate CON handling.
- Pseudo-session TTL cleanup.

Required tests:

- Message round trip and invalid version rejection.
- Token length rejection on marshal and parse.
- CON request receives ACK with same Message ID and token.
- Duplicate CON receives `Valid` ACK without re-running handler.
- Fuzz parse/marshal stability.

Release gates:

```powershell
go test ./internal/transport/coap -count=1 -v
go test ./internal/transport/coap -run='^$' -fuzz=FuzzParseMessage -fuzztime=2s
```

## LwM2M

Core risks:

- Object path validation.
- Registration lifecycle and lifetime expiry.
- Resource read/write isolation.
- CoAP command parsing and error behavior.

Required tests:

- Valid and invalid object paths.
- Register/update/deregister lifecycle.
- Read/write resources by endpoint and path.
- Sweep expired registrations.
- Invalid CoAP commands return errors without mutating registration state.

Release gate:

```powershell
go test ./internal/protocol/lwm2m -count=1 -v
```

## QUIC

Core risks:

- TLS configuration requirement.
- Stream request/response behavior.
- Max message size enforcement.
- Session cleanup after connection close.
- Write queue pressure on response streams.

Required tests:

- Startup fails without TLS.
- Echo through Gateway with plugin transform.
- Oversized stream payload does not invoke handler.
- Gateway shutdown clears runtime sessions.

Release gate:

```powershell
go test ./internal/transport/quic -count=1 -v
```

## gRPC-Web

Core risks:

- Direct unary HTTP mode and WebSocket mode remaining behaviorally aligned.
- Binary frame parsing and trailer generation.
- Strict malformed frame rejection.
- Max message size enforcement.
- Per-request and WebSocket session cleanup.

Required tests:

- Raw direct HTTP echo and cleanup.
- Framed unary request returns data frame plus `grpc-status: 0` trailer.
- Strict malformed frames return `400`.
- Oversized request returns `413`.
- WebSocket echo, origin rejection, max-size close, and cleanup.

Release gate:

```powershell
go test ./internal/transport/grpcweb -count=1 -v
```

## Full Release Gate

Before a release candidate:

```powershell
go run scripts/run_tests.go -mode all -timeout 5m
powershell -ExecutionPolicy Bypass -File .\scripts\validate.ps1 -Race
powershell -ExecutionPolicy Bypass -File .\scripts\validate_deploy.ps1
```

When Docker, Kubectl, and Helm are available, deploy validation must include:

- `docker compose -f deploy/docker/docker-compose.yml config`
- `kubectl kustomize deploy/k8s`
- `helm template shark-socket-new deploy/helm/shark-socket-new`
