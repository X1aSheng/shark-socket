# Project Review 260530-085109

Reviewed at: 2026-05-30T08:51:09

## Scope

- Read repository source, tests, scripts, docs, deployment manifests, workflow config, and current git history.
- Ran local package tests, scripted tests, validation scripts, deploy validation, race validation, coverage smoke, and formatting checks.
- Verified cloud Ubuntu build/test execution, Docker image build, docker-compose deployment, K8s/Helm rendering, and local-to-cloud TCP data exchange.

## Validation Run

| Command | Result |
| --- | --- |
| `go version` | Local `go1.26.1 windows/amd64`; cloud `go1.26.3 linux/amd64` |
| `go test ./... -count=1` | Passed locally and on cloud |
| `go vet ./...` | Passed through `scripts/validate.ps1` |
| `go run scripts/run_tests.go -mode all -timeout 5m` | Passed locally and on cloud; latest cloud report: 96 unit tests, 6 integration tests, benchmark report generated |
| `go run scripts/run_tests.go -mode race -timeout 5m` | Passed locally |
| `go run scripts/run_tests.go -mode cover -timeout 5m` | Passed locally; package coverage smoke generated |
| `powershell -ExecutionPolicy Bypass -File .\scripts\validate.ps1` | Passed locally |
| `powershell -ExecutionPolicy Bypass -File .\scripts\validate_deploy.ps1` | Passed static deploy tests locally; Docker/Kubectl/Helm optional checks skipped locally because tools are not installed |
| Cloud `docker build -t shark-socket:latest -f deploy/docker/Dockerfile .` | Initially failed on `proxy.golang.org` timeout; passed after configurable `GOPROXY` fix |
| Cloud `docker compose -f deploy/docker/docker-compose.yml up -d --build` | Passed after stopping an older container already bound to port 18000 |
| Cloud health/readiness | `GET /healthz` returned `ok`; `GET /readyz` returned `ready` |
| Cloud `kubectl kustomize deploy/k8s` | Rendered 82 lines successfully |
| Cloud `helm template shark-socket deploy/helm/shark-socket` | Rendered successfully |
| Cloud `kubectl cluster-info` | Failed because no reachable Kubernetes cluster context is configured on the server |
| Local TCP client to cloud | Sent length-prefix payload `codex-cloud-echo` to `120.76.44.233:18000`; received `codex-cloud-echo` |

## Findings And Fix Plan

| ID | Severity | Finding | Confirmation | Fix | Status |
| --- | --- | --- | --- | --- | --- |
| R-005 | P1 | WebSocket shutdown could invoke plugin `OnClose` twice for the same session when gateway shutdown raced with the read loop cleanup. The same close pattern existed in gRPC-Web WebSocket mode and CoAP pseudo-sessions. | Added `TestWebSocketOnCloseRunsOnceDuringShutdown`; it failed with `OnClose calls = 2`. | Changed transport session cleanup to use `LoadAndDelete` before unregistering and calling plugins; kept regression coverage. | Fixed in `6025e5a` |
| R-006 | P1 | `max_message_bytes` accepted negative values and invalid `SHARK_GRPCWEB_MAX_MESSAGE_BYTES` values were silently ignored. | Added focused config tests; both failed before the fix. | Made environment application return parse errors and made `Validate` reject negative max message sizes. | Fixed in `7a47db6` |
| R-007 | P1 | CI validated only Windows, while the target deployment server is Ubuntu. | Workflow semantics test showed no Linux matrix. | Added Windows/Ubuntu matrix and OS-specific validation log artifact names; updated deploy workflow assertions. | Fixed in `f9c26c6` |
| R-008 | P1 | Docker builds on the cloud server timed out while downloading modules from `proxy.golang.org` inside the build container. | Cloud `docker build` failed at `go mod download` with an I/O timeout. | Added configurable Docker build `GOPROXY`, defaulting compose builds to `https://goproxy.cn,direct`. | Fixed in `8edc9eb` |
| R-009 | P2 | Local workstation lacks Docker, Kubectl, and Helm, so deploy render checks are skipped locally. | `validate_deploy.ps1` logged explicit skips. | Cloud server render/build checks cover Docker, Kustomize, and Helm; local skips remain documented. | Verified externally |
| R-010 | P2 | Kubernetes apply cannot be completed on the provided cloud server because `kubectl` has no reachable cluster context. | `kubectl cluster-info` failed on the server. | K8s and Helm manifests render successfully; live apply remains blocked until a cluster context is configured. | Blocked by environment |

## Improvement Results

- Commit `6025e5a`: made transport session close callbacks idempotent.
- Commit `7a47db6`: validated gRPC-Web max message size config.
- Commit `f9c26c6`: added Windows and Ubuntu CI matrix coverage.
- Commit `8edc9eb`: made Docker builds proxy configurable for cloud builds.

## Remaining External Work

1. Configure a Kubernetes cluster context on the cloud server, or provide the target kubeconfig/namespace.
2. Re-run `kubectl apply -k deploy/k8s` or `helm upgrade --install` after the cluster context is available.
3. If port 18000 is used by another long-running service, choose a non-conflicting published port before compose deployment.
