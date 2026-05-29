# Shark-Socket-New Project Plan

Updated: 2026-05-29T12:15:52

## Timestamp Format

- General planning and design timestamps use `YYYY-MM-DDTHH:mm:ss`.
- Test and validation logs that need precision use `YYYY-MM-DDTHH:mm:ss.xxx`.
- Timezone suffixes are intentionally omitted from project documents.

## Plan Basis

This plan is based on the current repository state, not aspirational scope.

- Module: `github.com/X1aSheng/shark-socket-new`, Go `1.26.1`.
- Current branch: `shark-socket-new-main`.
- Current design reference: `docs/Architecture.md`, including Step 1 through Step 17 validation records.
- Current verified commands:
  - `go test ./... -count=1`
  - `go vet ./...`
  - `$env:PATH='D:\Programs\w64devkit\bin;D:\Programs\LLVM\bin;' + $env:PATH; $env:CGO_ENABLED='1'; go test -race ./... -count=1`
- Current implemented subsystems:
  - `api`: public facade for runtime, transports, plugins, and protocol helpers.
  - `internal/core`: protocol/session/server/plugin/runtime contracts.
  - `internal/runtime`: Gateway, plugin chain, shared SessionManager, lifecycle orchestration.
  - `internal/transport`: TCP, UDP, HTTP, WebSocket, CoAP, QUIC, gRPC-Web direct.
  - `internal/protocol/lwm2m`: in-memory LwM2M lifecycle model.
  - `internal/plugin`: blacklist, rate limit, heartbeat, persistence, autoban.
  - `internal/infra`: cache, store, pubsub, circuit breaker, in-memory observability.
  - `deploy`: Docker, docker-compose, K8s, Helm baseline.

## Current Status Summary

| Area | Status | Evidence |
| --- | --- | --- |
| Runtime architecture | Done | Gateway runtime injection, duplicate protocol checks, rollback, staged stop, readiness/health tests |
| TCP | Done | Framer variants, client, worker pool, echo and shutdown tests |
| UDP | Done | Pseudo-session registry, TTL sweep, plugin transform tests |
| HTTP | Done | Mode A router, Mode B runtime path, body limit tests |
| WebSocket | Done | Runtime integration, origin check, ping loop, shutdown cleanup tests |
| CoAP | Done | Message parse/marshal, CON ACK, TTL cleanup tests |
| LwM2M | Partial | Lifecycle/resource model exists; network binding to CoAP is not yet implemented |
| QUIC | Done | TLS requirement, stream echo, plugin transform, shutdown cleanup tests |
| gRPC-Web | Partial | Direct HTTP mode exists; WebSocket mode and protobuf framing depth are not yet implemented |
| Plugins | Partial | Core plugins exist; cluster and slow-query style plugins are not yet implemented |
| Infra | Partial | In-memory primitives exist; external adapters/exporters are not yet implemented |
| Deploy | Baseline | Static Docker/K8s/Helm manifest tests pass |
| Release validation | Partial | Unit/integration/race/vet pass; benchmark/fuzz/deploy CLI validation still pending |

## Completed Milestones

| Step | Milestone | Status | Completed At | Commit | Validation |
| --- | --- | --- | --- | --- | --- |
| 1 | Core/runtime foundation | Done | 2026-05-29T06:53:30 | `079b1fc` | Runtime tests, full test sweep |
| 2 | TCP transport slice | Done | 2026-05-29T06:53:40 | `542f168` | TCP focused tests, full test sweep |
| 3 | UDP/HTTP/WebSocket transports | Done | 2026-05-29T06:53:54 | `a5e2752` | Transport focused tests, full test sweep |
| 4 | Public API, plugins, docs | Done | 2026-05-29T06:54:06 | `d54b76b` | API build via full test sweep |
| 5 | CoAP transport | Done | 2026-05-29T09:30:28 | `8ff0ca5` | CoAP focused tests, full test sweep |
| 6 | LwM2M lifecycle model | Done | 2026-05-29T09:33:16 | `20816a3` | LwM2M focused tests, full test sweep |
| 7 | QUIC transport | Done | 2026-05-29T09:36:33 | `b6774f3` | QUIC focused tests, full test sweep |
| 8 | gRPC-Web direct transport | Done | 2026-05-29T09:38:56 | `9b92f40` | gRPC-Web focused tests, full test sweep |
| 9 | Core infra primitives | Done | 2026-05-29T09:40:36 | `ffb022e` | Infra focused tests, full test sweep |
| 10 | Deployment baseline | Done | 2026-05-29T09:41:47 | `3086e6e` | Static deploy tests, full test sweep |
| 11 | Release validation record | Done | 2026-05-29T09:42:41 | `cbc9946` | Test/vet record |
| 12 | Successful race validation | Done | 2026-05-29T09:58:16 | `cd5cba6` | Full race test |
| 13 | Expanded plugin ecosystem | Done | 2026-05-29T10:17:00 | `57d22b2` | Plugin focused tests, full test sweep |
| 14 | Observability test primitives | Done | 2026-05-29T10:18:00 | `2ba964d` | Observability focused tests, full test sweep |
| 15 | Infra/cache/breaker/heartbeat hardening | Done | 2026-05-29T10:32:25 | `8d613f5` | Focused tests, full test sweep, race test |

## Active Improvement Plan

| Priority | Workstream | Status | Target | Plan Basis |
| --- | --- | --- | --- | --- |
| P0 | Documentation accuracy | In Progress | Keep this plan, `Architecture.md`, and README aligned with implemented capability | README still describes only the initial TCP vertical slice |
| P0 | Release validation automation | Planned | Add scripted validation command that runs test/vet/race with local toolchain PATH | Race validation now works but requires manual env setup |
| P1 | LwM2M over CoAP binding | Planned | Connect LwM2M lifecycle operations to CoAP request/response handlers | Current LwM2M is an in-memory model only |
| P1 | gRPC-Web WebSocket mode | Planned | Add WebSocket transport mode for gRPC-Web gateway | Current gRPC-Web supports direct HTTP only |
| P1 | External observability adapters | Planned | Add Prometheus/OpenTelemetry adapters behind existing core interfaces | Core interfaces and memory adapters exist |
| P1 | Deploy validation depth | Planned | Add Docker/Helm/K8s render checks when tools are available | Current deploy tests only assert manifest presence and Dockerfile entrypoint |
| P2 | Benchmarks and fuzzing | Planned | Add protocol benchmarks and fuzz tests for CoAP/TCP framing | Current validation is unit/integration/race/vet focused |
| P2 | Plugin completeness | Planned | Add cluster and slow-query style plugins if production use requires them | Current plugin set covers common local safety, not clustering |

## Next Execution Steps

1. Documentation alignment
   - Update `README.md` from “architecture spike” to current multi-protocol status.
   - Add a concise feature matrix matching implemented packages.
   - Validation: `go test ./... -count=1`.

2. Validation script
   - Add `scripts/validate.ps1` for Windows.
   - Include `go test ./... -count=1`, `go vet ./...`, and optional race mode with `w64devkit`/`LLVM` PATH setup.
   - Validation: run the script in normal mode and race mode.

3. LwM2M over CoAP binding
   - Add a handler adapter that maps simple CoAP payload operations to `Register`, `Update`, `Deregister`, `Read`, and `Write`.
   - Keep wire shape minimal and documented before expanding.
   - Validation: CoAP + LwM2M integration test.

4. gRPC-Web WebSocket mode
   - Add a WebSocket mode adapter using the existing WebSocket transport patterns.
   - Preserve direct HTTP mode behavior.
   - Validation: direct mode tests continue passing; new WebSocket mode echo/max-size tests pass.

5. Release hardening
   - Add benchmark baselines.
   - Add fuzz smoke for CoAP parse/marshal and TCP framers.
   - Add deploy CLI validation gated by tool availability.

## Acceptance Criteria

The project is considered replacement-ready when all of the following are true:

- All existing protocol packages pass focused tests and `go test ./... -count=1`.
- `go vet ./...` passes.
- `go test -race ./... -count=1` passes on a machine with C toolchain installed.
- README and docs accurately describe implemented capability and known limitations.
- Deploy artifacts can be statically validated and rendered with Docker/Helm/K8s tools where available.
- LwM2M has a network-facing CoAP binding, not only an in-memory lifecycle model.
- gRPC-Web supports the intended production modes or clearly documents direct-only scope.

## Update Rules

- Every implementation step must update this file or `Architecture.md` with status, validation commands, and completion time.
- Each step should be committed independently with a focused message.
- A step is `Done` only after focused tests and full `go test ./... -count=1` pass.
- Race/vet/deploy validation should be recorded when run, including toolchain blockers if any.
