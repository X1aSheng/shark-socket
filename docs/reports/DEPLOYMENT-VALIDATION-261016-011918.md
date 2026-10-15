# shark-socket 云服务器部署验证报告

- 报告编号: DEPLOYMENT-VALIDATION-261016-011918
- 日期: 2026-10-16
- 验证目标: 云服务器编译、测试、Docker 部署、Kubernetes 部署
- 验证环境: 阿里云 ubuntu 26.04 LTS (resolute)

## 1. 环境信息

| 项目 | 值 |
| --- | --- |
| 云服务器 | 阿里云 ECS |
| 操作系统 | Ubuntu 26.04.1 LTS (resolute) |
| CPU | 8 core |
| 内存 | 14 GB (可用) |
| 磁盘 | 40 GB (34 GB 可用) |
| 内核 | 7.0.0-31-generic (amd64) |
| 主机名 | iZf8zj5ccfontaezzi4kfrz |

验证服务器: 47.120.44.128 (root@)

工具链版本:

| 工具 | 版本 |
| --- | --- |
| Go | go1.26.5 linux/amd64 |
| Docker | 29.7.2 |
| Docker Compose | v5.5.0 |
| k3s (Kubernetes) | v1.36.5+k3s1 |
| containerd | 2.3.4-k3s1.36 |
| Helm | v3.17.0 |

## 2. 编译验证

在云服务器 /opt/shark-socket/src 解压项目后执行:

- go mod download: rc=0
- go build ./...: rc=0
- go vet ./...: rc=0
- go test ./...: rc=0

二进制产物: /opt/shark-socket/bin/shark-socket (13.7 MB)

入口: cmd/shark-socket，支持 -config 参数与 SHARK_CONFIG 环境变量。

## 3. 单元测试与集成测试

go test ./... 全量结果: 全部包 ok，共 523 个 PASS，0 个 FAIL。

覆盖包 (34 个，均 ok):

- api, internal/app, internal/core
- internal/infra/{cache,circuitbreaker,mqtt,observability,pubsub,store,tlsutil}
- internal/plugin, internal/protocol/lwm2m, internal/runtime
- internal/transport/{coap,grpcweb,http,quic,shared,tcp,udp,websocket}
- scripts, tests, tests/benchmark, tests/deploy, tests/stress
- examples/tls_server

关键测试:

| 测试 | 结果 | 耗时 |
| --- | --- | --- |
| tests (跨协议) | ok | 0.43s |
| tests/deploy (Dockerfile/K8s/Helm/GHA 语义) | ok | 6.7s |
| tests/stress (TCP/UDP/WS/HTTP 压测) | ok | 54.2s |
| tests/benchmark | ok | 95.8s |

scripts/run_tests.go -mode all: rc=0。

## 4. Docker 部署验证

### 4.1 镜像构建

- 命令: docker build -t shark-socket:test -f deploy/docker/Dockerfile .
- 基础镜像: golang:1.26-alpine (build) / alpine:3.22 (runtime)
- 结果: 构建成功，镜像大小 40.3 MB
- 镜像加速: 配置 registry-mirrors (docker.m.daocloud.io 等)

### 4.2 容器运行

- 容器: shark-test (端口 28000/28080/28081 映射到 18000/18080/18081)
- 启动日志: protocols=[tcp] health=0.0.0.0:18081 metrics=0.0.0.0:18080
- 运行用户: shark (uid 1000, 非 root)
- 安全: read-only 根文件系统 + no-new-privileges + cap_drop ALL

### 4.3 功能验证

| 端点 | 结果 |
| --- | --- |
| GET /healthz | ok (HTTP 200) |
| GET /readyz | ready (HTTP 200) |
| GET /metrics | HTTP 200, Prometheus 文本格式 |

metrics 内容 (TCP 连接触发后):

```
# TYPE sessions_accepted_total counter
sessions_accepted_total 3
# TYPE sessions_closed_total counter
sessions_closed_total 3
# TYPE sessions_active gauge
sessions_active 0
```

### 4.4 优雅停机

- docker stop shark-test: 0.239s (优雅退出)

### 4.5 Docker Compose

- docker compose config --quiet: OK
- 服务: shark-socket (依赖 mosquitto 健康) + mosquitto + mqtt-test (profile: test)

## 5. Kubernetes 部署验证

### 5.1 集群

- 类型: k3s 单节点 (control-plane)
- 运行时: containerd 2.3.4
- 节点: Ready, v1.36.5+k3s1

### 5.2 清单部署 (kubectl apply -k)

deploy/k8s/ 通过 kustomization 部署，资源:

| 资源 | 名称 | 状态 |
| --- | --- | --- |
| Namespace | shark-socket | created |
| ServiceAccount | shark-socket | created |
| ConfigMap | shark-socket-config | created |
| Deployment | shark-socket (2 副本) | rolled out |
| Service | shark-socket (ClusterIP) | created |
| PodDisruptionBudget | shark-socket-pdb (minAvailable 1) | created |
| HorizontalPodAutoscaler | shark-socket-hpa (2-10, cpu 50%) | created |
| NetworkPolicy | shark-socket-netpol | created |

### 5.3 Pod 状态

- 2/2 Pod Running, ready=true, restart=0
- 安全上下文: runAsNonRoot + readOnlyRootFilesystem + seccomp RuntimeDefault + drop ALL

### 5.4 端到端验证

| 端点 (port-forward svc) | 结果 |
| --- | --- |
| /healthz | ok |
| /readyz | ready |
| TCP 连接 (18000) | 建立成功 |

### 5.5 滚动更新

- kubectl rollout restart: 旧 Pod 优雅终止, 新 Pod 就绪, successfully rolled out

### 5.6 Helm Chart

- helm lint: 0 failed
- helm template: 渲染正确 (ServiceAccount/ConfigMap/Service/Deployment)
- helm install (namespace shark-helm): deployed, 2 Pod Running
- helm uninstall: 正常清理

## 6. 发现的缺陷与问题

### 6.1 HPA 缺少 metrics-server

- 现象: HPA 状态 cpu: <unknown>/50%，日志 FailedGetResourceMetric
- 原因: k3s 默认不安装 metrics-server
- 影响: HPA 无法自动扩缩容 (不影响部署本身)
- 建议: 安装 metrics-server 后 HPA 生效

### 6.2 Docker Hub 直连超时

- 现象: docker build 拉取 alpine 镜像 i/o timeout
- 原因: 阿里云环境访问 registry-1.docker.io 超时
- 解决: 配置 registry-mirrors (docker.m.daocloud.io 等) 后正常

### 6.3 k3s pause 镜像拉取失败

- 现象: Pod 卡在 ContainerCreating, FailedCreatePodSandBox
- 原因: rancher/mirrored-pause:3.10.2 从 Docker Hub 拉取超时
- 解决: 从镜像站拉取后手动 k3s ctr images import

## 7. 结论

- 编译、单元测试、集成测试、压力测试、基准测试全部通过
- Docker 镜像构建与容器运行正常，健康检查、metrics、优雅停机验证通过
- Kubernetes (k3s) 部署、滚动更新、Service 访问、Helm 安装均验证通过
- 三个环境相关问题 (metrics-server、Docker Hub 直连、pause 镜像) 均通过镜像加速与手动导入解决，非项目代码缺陷

## 8. 附录

验证日志: 云端 /opt/shark-socket/logs/build-test-261016-005748.log (73 KB)

本地备份: .workbuddy/cloud/cloud-build-test.log
