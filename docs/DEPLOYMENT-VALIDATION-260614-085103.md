# Deployment Validation 2026-06-14 08:51

## Server 1 — Client Role (`120.76.44.233`)

| Item | Value |
|---|---|
| Host | `120.76.44.233` (Alibaba Cloud ECS) |
| OS | Ubuntu 26.04, kernel 7.0.0-15-generic |
| Go | go1.26.4 linux/amd64 |
| Docker | 29.5.0 |
| kubectl | Not installed |

### Compilation & Test Results

| Check | Result |
|---|---|
| `git clone && git checkout shark-socket-new-main` | PASS |
| `go test ./... -count=1 -timeout 300s` | PASS — all 20 packages |

### Docker Build & Run

| Step | Result |
|---|---|
| `docker build -f deploy/docker/Dockerfile -t shark-socket:latest` | PASS |
| Image SHA | `sha256:8648b5ac0d99281f8033c69b4d501962024d825e660f08fa722440e5b4d7f73a` |
| Container status | `running healthy` |
| Health endpoint `/healthz` | Responded `ok` |
| Readiness endpoint `/readyz` | Responded `ready` |

---

## Server 2 — Server Role (`47.110.42.28`)

| Item | Value |
|---|---|
| Host | `47.110.42.28` (Alibaba Cloud ECS, new) |
| OS | Ubuntu 26.04, kernel 7.0.0-15-generic |
| CPU / RAM | 8 core / 30 GiB |
| Disk | 40 GB (35 GB free) |
| Go | go1.26.4 (installed via Alibaba mirror) |
| Docker | 29.5.3 (installed via Alibaba mirror) |
| Go Proxy | `GOPROXY=https://goproxy.cn,direct` |
| Docker Mirror | Alibaba Cloud / DaoCloud |

### Environment Setup

| Step | Result |
|---|---|
| Install Go 1.26.4 | `mirrors.aliyun.com/golang/go1.26.4.linux-amd64.tar.gz` |
| Install Docker CE | `mirrors.aliyun.com/docker-ce/linux/ubuntu` stable |
| Configure Go proxy | `goproxy.cn` for module download |
| Configure Docker mirror | Alibaba + DaoCloud registry mirrors |
| Transfer repo | SCP from Server 1 (GitHub direct too slow in China) |

### Compilation & Test Results

| Check | Result |
|---|---|
| `go build ./...` | PASS |
| `go test ./... -count=1 -timeout 300s` | PASS — all 20 packages |

### Docker Build & Run

| Step | Result |
|---|---|
| `docker build -f deploy/docker/Dockerfile -t shark-socket:latest` | PASS |
| Image SHA | `sha256:1679675d2130c7d7336fbd0ea582e7445b1bf99d36a5f45ec2a20413e297cb3a` |
| Container status | `running healthy` |
| Health endpoint `/healthz` (docker exec) | Responded `ok` |

---

## Summary

Both cloud servers validated successfully. Server 2 required Chinese mirror
configuration for Go modules (`goproxy.cn`) and Docker images (Alibaba Cloud
registry mirrors) to work around network restrictions. The server binary
compiles natively on Ubuntu 26.04, Docker images build with multi-stage
optimization (distroless Alpine base), and containers start with proper
HEALTHCHECK reporting healthy on both nodes.
