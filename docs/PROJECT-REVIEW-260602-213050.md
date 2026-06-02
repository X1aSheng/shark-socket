# Project Review 2026-06-02 21:30

## Overview

Full repository review and validation pass for `shark-socket` on 2026-06-02.
The review read all 216 tracked files and grouped them into source, tests,
scripts, deploy/CI, examples, and documentation.

| Category | Files | Lines |
|---|---:|---:|
| source | 76 | 8,535 |
| tests | 51 | 8,231 |
| scripts | 6 | 908 |
| deploy-ci | 20 | 650 |
| examples | 12 | 626 |
| docs | 51 | 18,500 |

## Fixed Defects

| ID | Severity | Issue | Fix Commit |
|---|---|---|---|
| R-001 | High | `FuzzRawFramerRoundTrip/seed#1` failed because empty raw payload was treated as a readable frame. | `511eb33` |
| R-002 | High | LwM2M TLV fuzz test used stale private field names, causing `go vet ./...` to fail. | `19481f9` |
| R-003 | Medium | TLV values larger than uint16 could be encoded with truncated length. | `19481f9` |
| R-004 | High | PowerShell validation scripts reported PASS for failing native commands. | `8a7aadd` |
| R-005 | Medium | `BenchmarkQUICEcho` read from the request stream instead of accepting the server response stream. | `0d40027` |

## Local Validation

| Check | Result |
|---|---|
| `go test ./... -count=1` | PASS |
| `go vet ./...` | PASS |
| `go run scripts/run_tests.go -mode all -timeout 5m` | PASS: 333 passed, 0 failed, 2 skipped |
| `go run scripts/run_tests.go -mode race -timeout 5m` | PASS: 341 passed, 0 failed, 2 skipped |
| `go run scripts/run_tests.go -mode cover -timeout 5m` | PASS: total coverage 72.1% |
| `./scripts/validate.ps1` | PASS |
| `./scripts/validate_deploy.ps1` | PASS for static tests; local docker/kubectl/helm not installed, render checks skipped |

Logs are under `logs/2026-06-02T21-29-*` and `logs/2026-06-02T21-30-*`.

## CI and Deployment Validation

- GitHub Actions workflow semantics passed in `tests/deploy`.
- Action versions were checked against official GitHub release pages for:
  - `actions/checkout`
  - `actions/setup-go`
  - `actions/upload-artifact`
- Cloud server `120.76.44.233` validation:
  - Go version: `go1.26.3 linux/amd64`.
  - `go build ./cmd/shark-socket`: PASS.
  - `go test ./... -count=1`: PASS.
  - `go vet ./...`: PASS.
  - `go run scripts/run_tests.go -mode all`: PASS.
  - `go run scripts/run_tests.go -mode race`: PASS.
  - `go run scripts/run_tests.go -mode cover`: PASS, coverage 72.1%.
  - `go test ./tests/deploy -count=1 -v`: PASS with docker/kubectl/helm available.
  - `docker compose config`, `kubectl kustomize`, and `helm template`: PASS.
  - Docker Compose build and startup: PASS; health `/healthz` returned `ok`, readiness `/readyz` returned `ready`.
  - Cloud-side TCP client test: PASS.
  - Local client to cloud Docker TCP echo: PASS, payload `local-to-cloud-echo`.
  - Local HTTP health to cloud Docker: PASS, HTTP 200.

## Open Environment Item

Kubernetes actual deployment was attempted using a temporary `kind` cluster on
the 2-core/2GB Alibaba Cloud server. The cluster creation timed out waiting for
the control plane to become Ready, and subsequent SSH access to the server
stopped responding while ICMP and Docker-published health endpoints remained
reachable. This appears to be a resource exhaustion/environment issue, not a
manifest render issue.

Required cleanup after SSH recovers or from the cloud console:

```bash
kind delete cluster --name shark-socket-review || true
cd /tmp/shark-socket-review-20260602-213131 && docker compose -f deploy/docker/docker-compose.yml down --remove-orphans || true
rm -rf /tmp/shark-socket-review-20260602-213131 /tmp/shark-socket-20260602-213131.tar
```

Security note: the previously shared root password should be treated as exposed.
Continue using SSH key access and rotate the root password in the cloud console.

## Status

Code, tests, CI scripts, Docker, Helm rendering, Kustomize rendering, and local
client to cloud Docker interaction are validated. The only incomplete item is
actual Kubernetes rollout on this small cloud host due to the temporary kind
cluster exhausting server responsiveness.
