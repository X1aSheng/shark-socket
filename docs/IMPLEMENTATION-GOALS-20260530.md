# Shark-Socket-New Implementation Goals

Updated: 2026-05-30T01:10:00

## Purpose

This document defines the forward implementation goals for
`shark-socket-new`. It is an execution guide for future work, not a record of
completed steps. Use it to decide what to build next, how to split work, and
how to judge whether a milestone is complete.

## Product Direction

`shark-socket-new` should become a modular, observable, and deployable
multi-protocol communication gateway for IoT, edge, and backend systems.

The project should optimize for:

- Unified runtime lifecycle and session ownership.
- Protocol adapters that share Gateway, plugin, observability, and shutdown behavior.
- Production-friendly validation through tests, race checks, benchmarks, CI, Docker, and Kubernetes.
- Clear extension points for MQTT, device management, persistence, clustering, and cloud deployment.

The project should not optimize for:

- Duplicating full broker products before the gateway runtime is stable.
- Hiding protocol differences behind an over-generalized API.
- Adding distributed behavior before single-node correctness and observability are boringly reliable.

## Operating Principles

1. Every protocol feature must pass through the Gateway runtime path unless it is explicitly documented as standalone.
2. Every defect fix must start with a focused regression test.
3. Every milestone must update docs, tests, and validation logs.
4. Each commit should represent one reviewable improvement.
5. Release readiness is decided by validation evidence, not by feature count.

## Version Goals

### v0.1.x: Runtime And Protocol Foundation

Goal:

Make the current multi-protocol Gateway reliable enough for release-candidate
evaluation.

Scope:

- Stabilize Gateway lifecycle, restart behavior, plugin execution, and staged shutdown.
- Keep TCP, UDP, HTTP, WebSocket, CoAP, LwM2M, QUIC, and gRPC-Web behavior covered by focused tests.
- Keep CI, validation scripts, release logs, and protocol testing guide current.
- Complete external Docker/Kubernetes validation when tools and cloud access are available.

Acceptance:

- `go test ./... -count=1` passes.
- `go vet ./...` passes.
- `go test -race ./... -count=1` passes.
- `go run scripts/run_tests.go -mode all -timeout 5m` passes.
- `scripts/validate_deploy.ps1` passes, with missing tools recorded explicitly.
- Cloud deployment validation is recorded when credentials and tools are available.

### v0.2.x: Production Configuration And Deployment

Goal:

Turn the current runnable examples into configurable deployment entrypoints.

Scope:

- Add structured configuration for listeners, protocols, TLS, plugins, and observability.
- Support environment-variable and file-based configuration.
- Add health/readiness HTTP endpoints suitable for Kubernetes probes.
- Build Docker image locally and on a cloud/server runner.
- Validate Docker Compose, Kustomize, and Helm deployment in a real environment.
- Record local-client to deployed-server data exchange.

Acceptance:

- A user can start the gateway without changing Go source code.
- Docker image builds from a clean checkout.
- Kubernetes or Helm deployment is verified against a real cluster.
- At least TCP and WebSocket client interactions are verified against the deployed service.
- Deployment instructions include rollback and log collection steps.

### v0.3.x: IoT Protocol Depth

Goal:

Expand from transport connectivity into practical IoT gateway behavior.

Scope:

- Add MQTT 3.1.1 and MQTT 5.0 protocol support or adapter plan.
- Expand CoAP/LwM2M behavior beyond text-command smoke flows.
- Add device identity, registration, heartbeat, offline detection, and session metadata conventions.
- Add protocol-specific security recommendations for TLS/DTLS/mTLS where applicable.
- Add defect/regression tests for protocol conformance edge cases.

Acceptance:

- MQTT connect/publish/subscribe basics are tested when MQTT enters scope.
- LwM2M lifecycle behavior has documented compatibility limits.
- Device/session metadata is observable through logs or metrics.
- Protocol guide is updated with conformance boundaries and release gates.

### v0.4.x: Reliability, Clustering, And Persistence

Goal:

Make the gateway usable in multi-node and failure-prone deployments.

Scope:

- Define cluster topology and message-routing responsibilities.
- Add external pub/sub or message bus integration options.
- Add persistence strategy for session events, device state, and replayable messages where required.
- Add backpressure, overload, and degradation policies per protocol.
- Add load and soak testing scripts.

Acceptance:

- Multi-node behavior is documented and tested with at least one integration scenario.
- Backpressure behavior is explicit for TCP, UDP, WebSocket, QUIC, and gRPC-Web.
- Persistence semantics are clear: what is durable, best-effort, or in-memory only.
- Benchmarks have stable baselines and comparison notes.

### v1.0.0: Stable Gateway Contract

Goal:

Publish a stable API and operational contract for downstream users.

Scope:

- Freeze public API compatibility rules.
- Document supported protocols, unsupported protocol features, and security posture.
- Provide production deployment reference architecture.
- Provide migration guidance from earlier release candidates.

Acceptance:

- API package has compatibility notes.
- Release checklist is fully automated except for explicitly manual cloud validation.
- Documentation is sufficient for a new operator to run, validate, and troubleshoot the gateway.

## Workstream Backlog

| Priority | Workstream | Target Outcome |
| --- | --- | --- |
| P0 | External deployment validation | Prove Docker/Kubernetes/cloud runtime with real client traffic |
| P0 | Configurable runtime entrypoint | Start multi-protocol gateway from config instead of source edits |
| P0 | Security baseline | TLS/mTLS, origin policy, listener binding, and secret handling guidance |
| P1 | MQTT planning and implementation | Add MQTT 3.1.1/5.0 path with conformance-oriented tests |
| P1 | Protocol conformance depth | Expand edge tests and document compatibility limits |
| P1 | Observability operations | Standard metrics, trace names, log fields, and dashboards/examples |
| P2 | Load and soak testing | Repeatable performance and stability scripts with baselines |
| P2 | Cluster integration | External bus adapter and multi-node data-flow tests |
| P2 | Persistence strategy | Durable state/event design with clear failure semantics |

## Milestone Template

Use this template for each future implementation step:

```md
### Step N: <Milestone Name>

Goal:

- <What user-visible or operator-visible outcome this creates>

Scope:

- <Implementation boundaries>

Tests:

- <Focused tests>
- <Regression tests>
- <Full validation commands>

Docs:

- <Files that must be updated>

Acceptance:

- <Concrete pass/fail criteria>

Commit:

- `<hash>` after completion
```

## Definition Of Done

A milestone is complete only when:

- The implementation is committed independently.
- Focused tests pass.
- `go test ./... -count=1` passes.
- `go vet ./...` passes.
- Race validation passes when concurrency, sessions, transports, plugins, or shared state are touched.
- Docs are updated in the same milestone or an immediately following docs milestone.
- Known blockers are written down with concrete missing inputs.

## Current Next Step

The next recommended milestone is:

### External Deployment Validation

Inputs needed:

- Cloud host and SSH access or a cloud CI runner.
- Docker installed on the validation host.
- Kubectl and Helm installed if Kubernetes validation is required.
- Kubernetes context, namespace, service exposure method, and image registry policy.
- Public endpoint for local-client to remote-server protocol tests.

Expected evidence:

- Build log.
- Docker image or Compose validation log.
- Kubernetes or Helm render/apply log.
- Local client request/response transcript.
- Updated documentation with exact commands and results.
