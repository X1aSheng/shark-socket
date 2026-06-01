# Shark-Socket-New Project Plan

Updated: 2026-05-30T11:35:00

## Timestamp Format

- General planning and design timestamps use `YYYY-MM-DDTHH:mm:ss`.
- Test and validation logs that need precision use `YYYY-MM-DDTHH:mm:ss.xxx`.
- Timezone suffixes are intentionally omitted from project documents.

## Plan Basis

This plan is based on the current repository state, not aspirational scope.

- Module: `github.com/X1aSheng/shark-socket`, Go `1.26.1`.
- Current branch: `shark-socket-main`.
- Current design reference: `docs/Architecture.md`, including Step 1 through Step 28 validation records.
- Forward implementation guide: `docs/IMPLEMENTATION-GOALS-20260530.md`.
- Current test reference: `docs/TEST-STRATEGY-20260529.md`.
- Current verified commands:
  - `go test ./... -count=1`
  - `go vet ./...`
  - `$env:PATH='D:\Programs\w64devkit\bin;D:\Programs\LLVM\bin;' + $env:PATH; $env:CGO_ENABLED='1'; go test -race ./... -count=1`
- Current implemented subsystems:
  - `api`: public facade for runtime, transports, plugins, and protocol helpers.
  - `internal/core`: protocol/session/server/plugin/runtime contracts.
  - `internal/runtime`: Gateway, plugin chain, shared SessionManager, lifecycle orchestration.
  - `internal/transport`: TCP, UDP, HTTP, WebSocket, CoAP, QUIC, gRPC-Web direct and WebSocket modes.
  - `internal/protocol/lwm2m`: in-memory LwM2M lifecycle model with CoAP binding.
  - `internal/plugin`: blacklist, rate limit, heartbeat, persistence, autoban, slow handler logging adapter, cluster pub/sub.
  - `internal/infra`: cache, store, pubsub, circuit breaker, in-memory observability, Prometheus metrics exporter, OpenTelemetry tracer adapter.
  - `deploy`: Docker, docker-compose, K8s, Helm production-oriented baseline.
  - `examples`: compile-checked multi-protocol runtime example.

## Current Status Summary

| Area | Status | Evidence |
| --- | --- | --- |
| Runtime architecture | Done | Gateway runtime injection, duplicate protocol checks, rollback, staged stop, readiness/health tests |
| TCP | Done | Framer variants, client, worker pool, echo and shutdown tests |
| UDP | Done | Pseudo-session registry, TTL sweep, plugin transform tests |
| HTTP | Done | Mode A router, Mode B runtime path, body limit tests |
| WebSocket | Done | Runtime integration, origin check, ping loop, shutdown cleanup tests |
| CoAP | Done | Message parse/marshal, CON ACK, TTL cleanup tests |
| LwM2M | Done | Lifecycle/resource model and CoAP text-command binding exist |
| QUIC | Done | TLS requirement, stream echo, plugin transform, shutdown cleanup tests |
| gRPC-Web | Done | Direct HTTP mode, binary frame parsing, grpc-status trailers, and WebSocket mode exist |
| Plugins | Done | Core plugins, slow-handler logging adapter, and cluster pub/sub plugin exist |
| Infra | Done | In-memory primitives, Prometheus metrics exporter, and OpenTelemetry tracer adapter exist |
| Deploy | Hardened | Security contexts, resource requests/limits, probes, static semantic tests, and optional Docker/Kubectl/Helm rendering exist |
| Examples | Done | Multi-protocol example and examples guide cover TCP, WebSocket, CoAP/LwM2M, metrics, and tracing |
| Release validation | Done | Unit/integration/race/vet/fuzz/benchmark/deploy validation pass; latest normal validation passed |
| Release notes | Done | `CHANGELOG.md` defines `v0.1.0` release candidate scope, validation, and known scope |

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
| 16 | Documentation alignment and validation script | Done | 2026-05-29T12:21:22 | `4682c78` | README status matrix, `scripts/validate.ps1`, normal validation, race validation |
| 17 | LwM2M over CoAP binding | Done | 2026-05-29T12:24:31 | `c624830` | CoAP + LwM2M integration test, full validation |
| 18 | gRPC-Web WebSocket mode | Done | 2026-05-29T12:27:15 | `f5a3dd9` | gRPC-Web focused tests, full validation |
| 19 | TCP/CoAP fuzz and benchmark baseline | Done | 2026-05-29T12:30:30 | `2863470` | TCP/CoAP focused tests, fuzz smoke, benchmark baseline, full validation |
| 20 | shark-socket-style test logging | Done | 2026-05-29T12:49:05 | `7ec9e62` | Test strategy doc, JSON/log parser, scripted runner, validation transcript |
| 21 | Deploy validation depth | Done | 2026-05-29T12:58:24 | `ee577ea` | Static manifest semantics, optional Docker/Kubectl/Helm render validation, deploy transcript |
| 22 | Final race refresh after deploy hardening | Done | 2026-05-29T12:58:54 | `ee577ea` | `validate.ps1 -Race`, full race sweep |
| 23 | Slow handler logging adapter | Done | 2026-05-29T13:01:11 | `a4ad2b8` | Plugin focused tests, full validation |
| 24 | Prometheus metrics exporter | Done | 2026-05-29T13:50:35 | `e2de2ba` | Observability focused tests, full validation |
| 25 | Cluster pub/sub plugin | Done | 2026-05-29T13:53:26 | `d2cf09e` | Plugin focused tests, full validation |
| 26 | Final scripted release sweep | Done | 2026-05-29T13:54:39 | `7bd3c4d` | `run_tests.go -mode all`, `validate.ps1 -Race` |
| 27 | Release candidate notes | Done | 2026-05-30T00:13:42 | `9f58140` | `CHANGELOG.md`, README changelog link, normal validation |
| 28 | Production enhancement pass | Done | 2026-05-30T00:33:34 | `c2b6464` | gRPC-Web focused tests, observability/API focused tests, deploy validation, examples compile, full validation, race validation |
| 29 | Gateway restart lifecycle fix | Done | 2026-05-30T00:42:00 | `35b8428` | Restart regression test, full test sweep, vet |
| 30 | GitHub Actions validation workflow | Done | 2026-05-30T00:43:15 | `c106bbf` | Deploy workflow semantics test, deploy validation, full test sweep, vet |
| 31 | Protocol test guide | Done | 2026-05-30T00:50:00 | `777f6ae` | Independent protocol testing guide and README link |
| 32 | Protocol edge coverage expansion | Done | 2026-05-30T00:56:21 | `b00d46a` | Focused protocol tests, full test sweep, vet, scripted all-mode validation, race validation |
| 33 | Configurable runtime entrypoint | Done | 2026-05-30T01:12:39 | Pending | App config tests, deploy tests, full test sweep, vet, build, deploy validation, race validation |
| 34 | Review hardening and cloud Docker validation | Done | 2026-05-30T08:51:09 | `6025e5a`, `7a47db6`, `f9c26c6`, `8edc9eb` | Local tests/race/coverage/deploy checks; cloud Go tests; Docker build/compose; K8s and Helm render; local-to-cloud TCP echo |
| 35 | QUIC configuration with TLS material | Done | 2026-05-30T10:23:37 | Pending | QUIC config regression tests, `go test ./...`, `go vet ./...`, `go run scripts/run_tests.go -mode all -timeout 5m` |
| 36 | TCP TLS server configuration | Done | 2026-05-30T10:32:00 | Pending | TCP TLS handshake regression test, app TLS config tests, focused tests |
| 37 | WebSocket and gRPC-Web Origin allowlist config | Done | 2026-05-30T10:39:00 | Pending | App origin helper tests, env override tests, full validation pending |
| 38 | HTTP CORS allowlist config | Done | 2026-05-30T10:45:00 | Pending | HTTP CORS integration test, app env override test, full validation pending |
| 39 | TCP/QUIC mTLS configuration | Done | 2026-05-30T11:20:00 | `f49b126` | App config rejection/env tests, TCP mTLS integration test, full test sweep, vet |
| 40 | CI validation hardening | Done | 2026-05-30T11:35:00 | This commit | Workflow semantic test, scripted all-mode validation, deploy validation, full test sweep, vet |

## Active Improvement Plan

| Priority | Workstream | Status | Target | Plan Basis |
| --- | --- | --- | --- | --- |
| P0 | Documentation accuracy | Done | Keep this plan, `Architecture.md`, README, and changelog aligned with implemented capability | README now describes current multi-protocol state and links release notes |
| P0 | Release validation automation | Done | Add scripted validation command that runs test/vet/race with local toolchain PATH | `scripts/validate.ps1` supports normal and race validation |
| P0 | Configurable runtime entrypoint | Done | Start multi-protocol gateway from config instead of source edits | JSON config, env overrides, health/readiness, metrics, container listener env, TCP TLS config, QUIC certificate/key config, TCP/QUIC mTLS policy, HTTP CORS, and WebSocket/gRPC-Web Origin allowlists exist; certificate reload remains a security-baseline workstream |
| P1 | LwM2M over CoAP binding | Done | Connect LwM2M lifecycle operations to CoAP request/response handlers | CoAP responder maps register/update/deregister/read/write payloads to LwM2M server operations |
| P1 | gRPC-Web WebSocket mode | Done | Add WebSocket transport mode for gRPC-Web gateway | Direct HTTP and WebSocket modes now share runtime/plugin/session behavior |
| P1 | External observability adapters | Done | Add Prometheus/OpenTelemetry adapters behind existing core interfaces | Prometheus metrics exporter and OpenTelemetry tracer adapter exist |
| P1 | Deploy validation depth | Done | Add Docker/Helm/K8s render checks when tools are available | Deploy tests assert manifest semantics and production defaults; `validate_deploy.ps1` records optional tool rendering |
| P1 | Test logging workflow | Done | Preserve raw JSON and readable reports for scripted validation | `scripts/run_tests.go`, `scripts/parse_test_log.go`, and `validate.ps1` write logs under `logs/` |
| P2 | Benchmarks and fuzzing | Done | Add protocol benchmarks and fuzz tests for CoAP/TCP framing | TCP framer and CoAP parser fuzz smoke plus benchmark baselines are recorded |
| P2 | Plugin completeness | Done | Add cluster plugin if production use requires it | Cluster pub/sub plugin now covers cross-node local broadcast behavior |
| P2 | Gateway restart lifecycle | Done | Allow stop/start reuse of the same Gateway and shared SessionManager | TCP restart regression covers the bug found in review |
| P2 | GitHub Actions CI | Done | Run scripted validation, race, coverage, deploy checks, and log artifact upload on push/PR | `.github/workflows/ci.yml` runs Windows and Ubuntu matrix jobs, Ubuntu race/coverage jobs, and deploy workflow semantics test exists |
| P2 | Protocol test methodology | Done | Document protocol-specific test strategy and required edge cases | `docs/PROTOCOL-TEST-GUIDE-20260530.md` |
| P2 | Protocol edge regressions | Done | Add targeted edge tests across TCP, UDP, HTTP, WebSocket, CoAP, LwM2M, QUIC, and gRPC-Web | Scripted unit count increased to 88 passed |

## Next Execution Steps

1. Continue the security-baseline configuration workstream.
   - Certificate reload and finer protocol security defaults remain.
2. Complete external production Kubernetes validation when a production cluster context is available.
   - Docker build/compose, kind K8s apply, Helm install, and cross-host protocol traffic have been verified on cloud servers.
   - Remaining input: production Kubernetes cluster context/namespace and service exposure method.
3. Tag release candidate after review.
   - Recommended tag: `v0.1.0`.
   - Recommended command after the release commit is pushed: `git tag -a v0.1.0 -m "shark-socket v0.1.0"` then `git push origin v0.1.0`.

## Acceptance Criteria

The project is considered replacement-ready when all of the following are true:

- All existing protocol packages pass focused tests and `go test ./... -count=1`.
- `go vet ./...` passes.
- `go test -race ./... -count=1` passes on a machine with C toolchain installed.
- README and docs accurately describe implemented capability and known limitations.
- Deploy artifacts can be statically validated and rendered with Docker/Helm/K8s tools where available.

## Update Rules

- Every implementation step must update this file or `Architecture.md` with status, validation commands, and completion time.
- Each step should be committed independently with a focused message.
- A step is `Done` only after focused tests and full `go test ./... -count=1` pass.
- Race/vet/deploy validation should be recorded when run, including toolchain blockers if any.
