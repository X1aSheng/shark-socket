# Project Review 260530-094810

Reviewed at: 2026-05-30T09:48:10

## Scope

- Performed full cloud validation with two Ubuntu 26.04 servers in China network conditions.
- Server 1 `120.76.44.233` was used as the remote client test node.
- Server 2 `47.96.129.59` was used as the server/build/deploy test node.
- Configured China-friendly Go and Docker network paths.
- Verified Go tests, scripted reports, binary run, cross-host protocol traffic, Docker, docker-compose, Kubernetes, and Helm.

Credentials were used only for SSH execution and are intentionally not recorded in this document.

## Environment

| Role | Host | OS | Toolchain |
| --- | --- | --- | --- |
| Client node | `120.76.44.233` | Ubuntu 26.04 | Go 1.26.3, Docker 29.5.0, Docker Compose v5.1.3, Kubectl v1.35.0, Helm v3.21.0 |
| Server node | `47.96.129.59` | Ubuntu 26.04 | Go 1.26.3, Docker 29.1.3, Docker Compose 2.40.3, Kubectl v1.35.0, Helm v3.21.0, kind v0.31.0 |

Network optimization applied:

- Go: `GOPROXY=https://goproxy.cn,direct`, `GOSUMDB=sum.golang.google.cn`.
- Docker daemon registry mirrors: `docker.1ms.run`, `docker.xuanyuan.me`, `registry.cn-hangzhou.aliyuncs.com`.
- Dockerfile build arg `GOPROXY` used default `https://goproxy.cn,direct`.

## Server-Side Validation

| Command | Result |
| --- | --- |
| `go test ./... -count=1` | Passed |
| `go vet ./...` | Passed |
| `go run scripts/run_tests.go -mode all -timeout 5m` | Passed |
| `go build -o /tmp/shark-socket-new ./cmd/shark-socket-new` | Passed |
| `docker build -t shark-socket-new:cloud -f deploy/docker/Dockerfile .` | Passed with legacy builder compatible flags |
| `docker compose -f deploy/docker/docker-compose.yml config` | Rendered successfully |
| `kubectl kustomize deploy/k8s` | Rendered successfully |
| `helm template shark-socket-new deploy/helm/shark-socket-new` | Rendered successfully |

Scripted test report:

- Unit report: 96 passed, 0 failed, 0 skipped.
- Integration report: 6 passed, 0 failed, 0 skipped.
- Benchmark report generated for TCP framers and CoAP message parse/marshal.

## Binary Multi-Protocol Run

The server node ran `/tmp/shark-socket-new` with a cloud config binding to `0.0.0.0`:

| Protocol | Address |
| --- | --- |
| TCP | `0.0.0.0:18000` |
| UDP | `0.0.0.0:18002` |
| HTTP | `0.0.0.0:18003` |
| WebSocket | `0.0.0.0:18004/ws` |
| CoAP/LwM2M | `0.0.0.0:18005` |
| gRPC-Web direct | `0.0.0.0:18006/grpc` |
| Metrics | `0.0.0.0:18080` |
| Health/readiness | `0.0.0.0:18081` |

Local server checks:

- `GET /healthz`: `ok`
- `GET /readyz`: `ready`

## Cross-Host Protocol Validation

The client node sent traffic to server node `47.96.129.59`.

| Check | Result |
| --- | --- |
| `GET http://47.96.129.59:18081/healthz` | Passed |
| `GET http://47.96.129.59:18081/readyz` | Passed |
| `GET http://47.96.129.59:18080/metrics` | Passed |
| TCP length-prefix echo on `:18000` | Passed |
| UDP datagram echo on `:18002` | Passed |
| HTTP POST echo on `:18003` | Passed |
| WebSocket binary echo on `:18004/ws` | Passed |
| CoAP LwM2M register command on `:18005` | Passed |
| gRPC-Web direct echo on `:18006/grpc` | Passed |

Note: an initial gRPC-Web probe against `/` returned `404`; the correct direct path is `/grpc`.

## Docker Compose Validation

After stopping the direct binary server, Docker Compose was started on the server node:

```bash
docker compose -f deploy/docker/docker-compose.yml up -d --build
```

Results:

- Container `docker-shark-socket-new-1` started.
- Ports exposed: `18000`, `18080`, `18081`.
- `GET /healthz`: `ok`.
- `GET /readyz`: `ready`.
- Client node TCP echo to `47.96.129.59:18000` returned `compose-tcp`.

## Kubernetes Validation

Because the server node had no preconfigured cluster, a local kind cluster was created:

```bash
kind create cluster --name shark-test --wait 180s
kind load docker-image shark-socket-new:latest --name shark-test
kubectl apply -k deploy/k8s
kubectl rollout status deployment/shark-socket-new --timeout=180s
```

Results:

- kind cluster `shark-test` created successfully.
- Deployment rolled out successfully.
- Two pods were `Running` and `Ready`.
- Service `shark-socket-new` was created.
- Port-forwarded health and readiness checks returned `ok` and `ready`.

## Helm Validation

After deleting the Kustomize deployment, Helm installation was verified:

```bash
helm upgrade --install shark-socket-new deploy/helm/shark-socket-new \
  --set image.repository=shark-socket-new \
  --set image.tag=latest \
  --set image.pullPolicy=IfNotPresent
kubectl rollout status deployment/shark-socket-new --timeout=180s
```

Results:

- Helm release `shark-socket-new` installed in namespace `default`.
- Deployment rolled out successfully.
- Two pods were `Running` and `Ready`.
- Port-forwarded health and readiness checks returned `ok` and `ready`.

## Findings

| ID | Severity | Finding | Resolution |
| --- | --- | --- | --- |
| C-001 | P2 | Direct Go download from `go.dev` was too slow in the China cloud environment. | Installed Go from the Aliyun Golang mirror and configured `GOPROXY=https://goproxy.cn,direct`. |
| C-002 | P2 | Server node Docker legacy builder did not support `docker build --progress=plain`. | Used compatible `docker build` flags; project Dockerfile itself required no change. |
| C-003 | P2 | Server node initially had no Kubectl/Helm. | Copied verified Kubectl/Helm binaries from the client node and validated versions. |
| C-004 | P2 | No preconfigured Kubernetes cluster existed on the server node. | Created a local kind cluster and completed Kustomize and Helm live deployment validation. |

## Status

Cloud validation is complete for:

- Go build and tests.
- Scripted unit/integration/benchmark reports.
- Multi-protocol binary server.
- Cross-host client/server data exchange.
- Docker image build.
- Docker Compose runtime.
- Kubernetes Kustomize deployment on kind.
- Helm deployment on kind.

