# 云服务器部署验证报告 (V6)

- 日期: 2026-08-07 01:06
- 服务器: 120.76.44.233 (Alibaba Cloud ECS, Ubuntu 26.04, 2C/2GB)
- 提交: 5bdbd4b (V6 全部修复已推送并克隆到服务器)
- 方法: 真实云服务器编译 + docker 构建/部署 + k8s/helm 清单验证

## 0. 服务器清理 (任务前置)

| 清理项 | 结果 |
| --- | --- |
| /opt 下 shark-mqtt/shark-mqtt-bad/shark-mqtt-bad-old/cloud.tar.gz 等残留 | 已删除 |
| docker 镜像/容器/卷/网络/构建缓存 (912.7MB) | 已回收 |
| dockerd/containerd 守护进程 | 已停止, 部署验证前重启 |

## 1. 代码获取与编译

- GitHub push: 10 个 V6 修复提交已推送 `origin/main`
- 服务器: `git clone --depth 1` 成功 (GitHub 可达)
- `go build ./...` → **PASS**
- `go test ./api ./internal/...` → **全部 PASS** (quic/tcp/udp/websocket 等 21 包)
- `go test ./tests/...` → **全部 PASS** (cross_protocol, benchmark, deploy, stress)

## 2. Docker 验证

### 2.1 镜像源问题与解决
服务器到 Docker Hub / 国内镜像的大层下载极慢 (~1.4MB/min), golang:1.26-alpine (~350MB) 无法在合理时间拉取。已配置国内镜像 (daocloud/baidubce/163/1panel), 但大层仍慢。
**解决**: 用服务器本地 Go 工具链 (snap, 静态链接) 构建本地 `golang:1.26-alpine` 基础镜像 (364MB), 使真实 Dockerfile 免拉取构建。

### 2.2 真实 Dockerfile 构建
```
docker build -f deploy/docker/Dockerfile -t shark-socket:v6 .
```
- 多阶段构建 (golang build -> alpine:3.22 runtime)
- `CGO_ENABLED=0` 静态编译
- 非 root (adduser -u 1000), HEALTHCHECK 探测 :18081/healthz
- **构建成功**, 镜像 40.1MB

### 2.3 Docker Compose 部署
```
docker compose -f deploy/docker/docker-compose.yml up -d --build
```
| 容器 | 状态 |
| --- | --- |
| docker-shark-socket-1 | **healthy** |
| docker-mosquitto-1 | **healthy** |

### 2.4 冒烟测试
| 检查项 | 结果 |
| --- | --- |
| `GET /healthz` | `ok` |
| `GET /readyz` | `ready` |
| TCP Echo (LengthPrefix, 容器内端口 18000) | `V6-FULL-IMAGE-ECHO` 正确往返 |
| MQTT 集成测试 (真实 mosquitto broker) | `go test ./internal/infra/mqtt` → **PASS** (Connect/PublishSubscribe 等全部通过) |

## 3. Kubernetes / Helm 验证

| 检查项 | 结果 |
| --- | --- |
| `kubectl kustomize deploy/k8s/` | **OK** - 渲染 9 个资源 (Namespace/SA/ConfigMap/Service/Deployment/PDB/HPA/NetworkPolicy) |
| `helm template deploy/helm/shark-socket/` | **OK** - 渲染 4 个资源 |
| `helm lint deploy/helm/shark-socket/` | **0 失败** |
| `kubectl apply --dry-run=client` | 需集群连接 (本机无集群, API server 不可达), 属环境限制非清单问题 |

## 4. 观察项

1. **metrics 端点响应但内容为空**: `GET :18080/metrics` 返回 200 且 content-type 正确, 但正文为空。根因: 生产代码几乎不调用 `core.Metrics()` (仅 tcp worker pool 队列满时), 网关未在连接/消息路径上发出指标, Prometheus 导出器无数据可导出。建议后续为各传输层补充基础指标 (连接数/消息数/延迟)。
2. **服务器到容器镜像源带宽受限**: 大层下载 ~1.4MB/min, 需本地化 base 镜像或更高带宽。
3. **无 k8s 集群**: 服务器仅有 kubectl 客户端, 无法做真实 pod 部署, 清单通过渲染/lint 验证。

## 5. 结论

- 服务端程序在云服务器 (Linux) 编译通过, 单元 + 集成测试全部通过。
- Docker 真实 Dockerfile 构建成功, compose 栈 (shark-socket + mosquitto) 部署 healthy, healthz/readyz/TCP echo/MQTT 集成全部验证通过。
- K8s/Helm 清单通过 kustomize 渲染、helm template、helm lint 验证。
- 部署流程可用, 镜像源带宽为环境限制。
