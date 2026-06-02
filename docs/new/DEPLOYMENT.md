# DEPLOYMENT.md

> Shark-Socket 部署指南  
> 版本：v0.1.0

---

## 目录

1. [概述](#1-概述)
2. [本地运行](#2-本地运行)
3. [Docker 部署](#3-docker-部署)
4. [Kubernetes 部署](#4-kubernetes-部署)
5. [Helm 部署](#5-helm-部署)
6. [环境变量参考](#6-环境变量参考)
7. [端口规划](#7-端口规划)
8. [CI/CD 流水线](#8-cicd-流水线)
9. [部署校验](#9-部署校验)

---

## 1. 概述

Shark-Socket 提供三种生产部署方式，均为无状态设计（会话仅存在于进程内存）：

| 方式 | 适用场景 | 配置位置 |
|------|---------|---------|
| Docker | 单机或容器编排平台 | `deploy/docker/` |
| K8s 原生清单 | 已有 Kustomize 工作流 | `deploy/k8s/` |
| Helm Chart | 标准化 K8s 部署 | `deploy/helm/shark-socket/` |

**所有部署方式均内置：**
- 非 root 用户运行
- 只读根文件系统
- Drop ALL capabilities
- Seccomp RuntimeDefault 配置
- Liveness / Readiness 探针

---

## 2. 本地运行

### 2.1 直接运行

```bash
# 使用默认配置（TCP :18000, Health :18081, Metrics :18080）
go run ./cmd/shark-socket

# 指定配置文件
go run ./cmd/shark-socket -config examples/config/multi-protocol.json
```

### 2.2 环境变量运行

```bash
SHARK_TCP_ADDR=0.0.0.0:18000 \
SHARK_WS_ADDR=0.0.0.0:18700 \
SHARK_HEALTH_ADDR=0.0.0.0:18081 \
go run ./cmd/shark-socket
```

### 2.3 构建二进制

```bash
CGO_ENABLED=0 go build -o shark-socket ./cmd/shark-socket
```

---

## 3. Docker 部署

### 3.1 镜像构建

```bash
# 默认构建（使用 goproxy.cn 加速）
docker build -f deploy/docker/Dockerfile -t shark-socket:latest .

# 自定义代理
docker build --build-arg GOPROXY=https://goproxy.io,direct \
  -f deploy/docker/Dockerfile -t shark-socket:latest .
```

**Dockerfile 多阶段构建说明：**

| 阶段 | 基础镜像 | 作用 |
|------|---------|------|
| build | `golang:1.26-alpine` | 编译二进制，`CGO_ENABLED=0` |
| runtime | `alpine:3.22` | 最小运行时，non-root 用户 `shark` |

### 3.2 docker-compose 运行

```bash
cd deploy/docker
docker compose up -d
```

`docker-compose.yml` 安全配置：

```yaml
read_only: true              # 只读文件系统
security_opt:
  - no-new-privileges:true   # 禁止提权
cap_drop:
  - ALL                      # 删除所有 capabilities
```

### 3.3 Docker 端口映射

| 容器端口 | 作用 | 映射 |
|---------|------|------|
| 18000 | TCP 业务端口 | 必须映射 |
| 18080 | Prometheus 指标 | 按需映射 |
| 18081 | 健康探针 | K8s 内部使用 |

---

## 4. Kubernetes 部署

### 4.1 使用 Kustomize

```bash
# 预览渲染结果
kubectl kustomize deploy/k8s/

# 应用
kubectl apply -k deploy/k8s/
```

### 4.2 清单说明

#### 额外资源

除了 Deployment 和 Service，Kustomize 还部署：
- **Namespace**: `shark-socket` 命名空间隔离
- **ServiceAccount**: 专用服务账号
- **ConfigMap**: 应用配置（地址、端口）
- **NetworkPolicy**: 网络隔离策略
- **PodDisruptionBudget**: 最小可用 1 副本
- **HorizontalPodAutoscaler**: CPU 50%，2-10 副本

#### Deployment

```yaml
spec:
  replicas: 2
  template:
    spec:
      serviceAccountName: shark-socket
      securityContext:
        runAsNonRoot: true
        runAsUser: 1000
        fsGroup: 1000
        seccompProfile:
          type: RuntimeDefault
      containers:
        - securityContext:
            allowPrivilegeEscalation: false
            readOnlyRootFilesystem: true
            capabilities:
              drop: ["ALL"]
          resources:
            requests: { cpu: 50m, memory: 64Mi }
            limits:   { cpu: 500m, memory: 256Mi }
          readinessProbe:
            httpGet: { path: /readyz, port: health }
            initialDelaySeconds: 3
            periodSeconds: 10
          livenessProbe:
            httpGet: { path: /healthz, port: health }
            initialDelaySeconds: 10
            periodSeconds: 20
```

#### Service

ClusterIP 类型，暴露三个端口：tcp(18000)、metrics(18080)、health(18081)。

### 4.3 资源规划

| 规格 | CPU | Memory | 适用场景 |
|------|-----|--------|---------|
| 最小 | 50m / 256m | 64Mi / 256Mi | 测试/开发 |
| 推荐 | 200m / 1000m | 128Mi / 512Mi | 生产单节点 |
| 高吞吐 | 500m / 2000m | 256Mi / 1Gi | 万级连接 |

---

## 5. Helm 部署

### 5.1 Chart 信息

| 字段 | 值 |
|------|-----|
| Chart 名称 | `shark-socket` |
| Chart 版本 | `0.1.0` |
| appVersion | `0.1.0-rc.1` |
| 类型 | `application` |

### 5.2 安装

```bash
# 预览
helm template shark-socket deploy/helm/shark-socket/

# 安装
helm install shark-socket deploy/helm/shark-socket/

# 自定义值
helm install shark-socket deploy/helm/shark-socket/ \
  --set replicaCount=3 \
  --set image.tag=v0.1.0
```

### 5.3 values.yaml 参考

```yaml
replicaCount: 2

image:
  repository: shark-socket
  tag: latest
  pullPolicy: IfNotPresent

service:
  type: ClusterIP
  port: 18000
  metricsPort: 18080
  healthPort: 18081

config:
  tcpAddr: "0.0.0.0:18000"
  metricsAddr: "0.0.0.0:18080"
  healthAddr: "0.0.0.0:18081"

resources:
  requests: { cpu: 50m, memory: 64Mi }
  limits:   { cpu: 500m, memory: 256Mi }
```

---

## 6. 环境变量参考

环境变量优先级高于 JSON 配置文件，前缀为 `SHARK_`。

### 6.1 全局

| 变量 | 默认值 | 作用 |
|------|--------|------|
| `SHARK_CONFIG` | — | 配置文件路径（`-config` flag 替代） |
| `SHARK_SHUTDOWN_TIMEOUT` | `10s` | 优雅关闭超时 |
| `SHARK_HEALTH_ADDR` | `127.0.0.1:18081` | 健康端点监听地址 |
| `SHARK_METRICS_ADDR` | `127.0.0.1:18080` | 指标端点监听地址 |

### 6.2 协议

| 变量 | 协议 | 示例 |
|------|------|------|
| `SHARK_TCP_ADDR` | TCP | `0.0.0.0:18000` |
| `SHARK_TCP_CERT_FILE` | TCP TLS | `/certs/server.crt` |
| `SHARK_TCP_KEY_FILE` | TCP TLS | `/certs/server.key` |
| `SHARK_TCP_CLIENT_CA_FILE` | TCP mTLS | `/certs/ca.crt` |
| `SHARK_TCP_CLIENT_AUTH` | TCP mTLS | `require_and_verify` |
| `SHARK_HTTP_ADDR` | HTTP | `0.0.0.0:18080` |
| `SHARK_HTTP_ALLOWED_ORIGINS` | HTTP CORS | `https://app.example.com` |
| `SHARK_WS_ADDR` | WebSocket | `0.0.0.0:18700` |
| `SHARK_WS_PATH` | WebSocket | `/ws` |
| `SHARK_WS_ALLOWED_ORIGINS` | WebSocket | `https://app.example.com` |
| `SHARK_GRPCWEB_ADDR` | gRPC-Web | `0.0.0.0:18900` |
| `SHARK_GRPCWEB_PATH` | gRPC-Web | `/grpc` |
| `SHARK_GRPCWEB_MAX_MESSAGE_BYTES` | gRPC-Web | `4194304` |
| `SHARK_GRPCWEB_ALLOWED_ORIGINS` | gRPC-Web | `https://app.example.com` |
| `SHARK_QUIC_ADDR` | QUIC | `0.0.0.0:18443` |
| `SHARK_QUIC_CERT_FILE` | QUIC TLS（必须） | `/certs/server.crt` |
| `SHARK_QUIC_KEY_FILE` | QUIC TLS（必须） | `/certs/server.key` |

---

## 7. 端口规划

| 端口 | 协议 | 用途 | 暴露策略 |
|------|------|------|---------|
| 18000 | TCP | 业务连接（默认） | 对外 / LoadBalancer |
| 18080 | HTTP | Prometheus 指标 | 内部 / ClusterIP |
| 18081 | HTTP | 健康探针 | 内部 / Pod |
| 18443 | UDP | QUIC（TLS 必须） | 对外 |
| 18500 | UDP | CoAP / LwM2M | 对外 |
| 18700 | HTTP/WS | WebSocket | 对外 |
| 18900 | HTTP | gRPC-Web | 对外 |

---

## 8. CI/CD 流水线

CI 配置位于 `.github/workflows/ci.yml`，包含五个 Job。

### 8.1 lint（代码质量检查）

运行于 `ubuntu-latest`：

- 使用 `golangci-lint` 进行静态分析

### 8.2 security（安全扫描）

运行于 `ubuntu-latest`：

- 使用 `govulncheck` 扫描已知漏洞

### 8.3 validate（跨平台测试）

运行于 `windows-latest` + `ubuntu-latest` 矩阵：

1. `go run scripts/run_tests.go -mode all` — 单元 + 集成 + 基准测试
2. `scripts/validate.ps1` — `go vet`
3. `scripts/validate_deploy.ps1` — 部署清单静态校验

### 8.4 race（竞态检测）

运行于 `ubuntu-latest`：

- `go run scripts/run_tests.go -mode race` — 竞态检测

### 8.5 coverage（覆盖率）

运行于 `ubuntu-latest`：

- `go run scripts/run_tests.go -mode cover` — 覆盖率

### 8.6 触发条件

- Push 到 `main`、`shark-socket-main` 或 `shark-socket-new-main` 分支
- Pull Request 到上述分支

---

## 9. 部署校验

### 9.1 静态校验脚本

```bash
# 全量校验（测试 + vet + 部署清单）
pwsh scripts/validate.ps1
pwsh scripts/validate_deploy.ps1
```

### 9.2 部署清单测试

`tests/deploy/deploy_test.go` 静态验证：

| 资源 | 校验内容 |
|------|---------|
| Dockerfile | ENTRYPOINT、GOPROXY ARG、EXPOSE 端口 |
| docker-compose | 服务结构、端口映射、安全配置 |
| K8s Deployment | probe 路径、securityContext、resources |
| K8s Service | 端口名称和映射 |
| Kustomization | resources 列表 |
| Helm Chart | Chart.yaml 字段、values.yaml 模板 |
| CI Workflow | jobs 结构 |

### 9.3 运行时校验

```bash
# 健康检查
curl http://localhost:18081/healthz   # → ok
curl http://localhost:18081/readyz   # → ready

# Prometheus 指标
curl http://localhost:18080/metrics
```

---

**文档职责边界：** 本文档描述部署方式和配置。应用配置字段完整参考见 CONFIGURATION（如有），安全加固详见 SECURITY.md，传输层细节详见 TRANSPORT.md。
