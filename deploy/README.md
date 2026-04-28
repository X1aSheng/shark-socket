# shark-socket 部署指南

## 快速开始

### Docker Compose

```bash
# 生产模式
docker compose -f deploy/docker/docker-compose.yml up -d

# 开发模式（详细日志，无重启）
docker compose -f deploy/docker/docker-compose.yml --profile dev up

# 带 Prometheus 监控
docker compose -f deploy/docker/docker-compose.yml --profile prod up -d
```

### Docker

```bash
# 构建镜像
docker build -f deploy/docker/Dockerfile -t shark-socket:latest .

# 运行容器
docker run -d \
  -p 18000:18000 \
  -p 18200:18200/udp \
  -p 18400:18400 \
  -p 18600:18600 \
  -p 18800:18800/udp \
  -p 18900:18900/udp \
  -p 18650:18650 \
  -p 9091:9091 \
  -e SHARK_LOG_LEVEL=info \
  shark-socket:latest
```

---

## 端口参考

| 端口 | 协议 | 传输 | 说明 |
|------|------|------|------|
| 18000 | TCP | TCP | TCP 回声服务 |
| 18200 | UDP | UDP | UDP 回声服务 |
| 18400 | HTTP | TCP | HTTP API（/hello, /health） |
| 18600 | WebSocket | TCP | WebSocket（/ws） |
| 18800 | CoAP | UDP | CoAP 服务 |
| 18900 | QUIC | UDP | QUIC 传输（需 TLS，默认禁用） |
| 18650 | gRPC-Web | TCP | gRPC-Web 网关（默认禁用） |
| 9091 | Metrics | TCP | Prometheus /metrics, /healthz, /readyz |

---

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `SHARK_LOG_LEVEL` | `info` | 日志级别: debug, info, warn, error |
| `SHARK_LOG_FORMAT` | `json` | 日志格式: json, text |
| `SHARK_METRICS_ADDR` | `:9091` | Metrics 监听地址 |
| `SHARK_METRICS_ENABLED` | `true` | 启用 Prometheus 指标 |
| `SHARK_SHUTDOWN_TIMEOUT` | `30s` | 优雅关闭总超时 |
| `SHARK_TCP_ENABLED` | `true` | 启用 TCP 服务 |
| `SHARK_TCP_HOST` | `0.0.0.0` | TCP 监听地址 |
| `SHARK_TCP_PORT` | `18000` | TCP 监听端口 |
| `SHARK_UDP_ENABLED` | `true` | 启用 UDP 服务 |
| `SHARK_UDP_HOST` | `0.0.0.0` | UDP 监听地址 |
| `SHARK_UDP_PORT` | `18200` | UDP 监听端口 |
| `SHARK_HTTP_ENABLED` | `true` | 启用 HTTP 服务 |
| `SHARK_HTTP_HOST` | `0.0.0.0` | HTTP 监听地址 |
| `SHARK_HTTP_PORT` | `18400` | HTTP 监听端口 |
| `SHARK_WS_ENABLED` | `true` | 启用 WebSocket 服务 |
| `SHARK_WS_HOST` | `0.0.0.0` | WebSocket 监听地址 |
| `SHARK_WS_PORT` | `18600` | WebSocket 监听端口 |
| `SHARK_WS_PATH` | `/ws` | WebSocket 路径 |
| `SHARK_COAP_ENABLED` | `true` | 启用 CoAP 服务 |
| `SHARK_COAP_HOST` | `0.0.0.0` | CoAP 监听地址 |
| `SHARK_COAP_PORT` | `18800` | CoAP 监听端口 |
| `SHARK_QUIC_ENABLED` | `false` | 启用 QUIC（需 TLS 证书） |
| `SHARK_QUIC_HOST` | `0.0.0.0` | QUIC 监听地址 |
| `SHARK_QUIC_PORT` | `18900` | QUIC 监听端口 |
| `SHARK_GRPCWEB_ENABLED` | `false` | 启用 gRPC-Web |
| `SHARK_GRPCWEB_HOST` | `0.0.0.0` | gRPC-Web 监听地址 |
| `SHARK_GRPCWEB_PORT` | `18650` | gRPC-Web 监听端口 |

---

## Kubernetes 部署

### 使用 kubectl + Kustomize

```bash
kubectl apply -k deploy/k8s/app/
```

### 使用 Helm

```bash
# 默认配置
helm install shark-socket deploy/k8s/helm/shark-socket/ \
  --namespace shark-socket --create-namespace

# 生产配置
helm install shark-socket deploy/k8s/helm/shark-socket/ \
  --namespace shark-socket --create-namespace \
  --values deploy/k8s/helm/shark-socket/values.yaml \
  --values deploy/k8s/helm/shark-socket/values-prod.yaml

# 升级
helm upgrade shark-socket deploy/k8s/helm/shark-socket/ \
  --namespace shark-socket
```

---

## 监控

### Prometheus 指标

指标地址: `http://<host>:9091/metrics`

主要指标:
- `shark_connections_total` — 连接总数
- `shark_sessions_total` — 会话总数
- `shark_messages_total` — 消息总数
- `shark_errors_total` — 错误总数
- `shark_sessions_active` — 活跃会话数
- `shark_message_bytes` — 消息字节分布（直方图）
- `shark_message_duration_seconds` — 消息处理延迟（直方图）

### 健康检查

- `/healthz` — 存活检查（返回 200 表示服务运行中）
- `/readyz` — 就绪检查（返回 200 表示可接收请求）

---

## 生产检查清单

- [ ] 配置 TLS 证书（如启用 QUIC 或 HTTPS）
- [ ] 设置资源限制（CPU/Memory）
- [ ] 配置 HPA 自动扩缩
- [ ] 启用 NetworkPolicy 网络隔离
- [ ] 配置 PodDisruptionBudget
- [ ] 启用 Prometheus 监控
- [ ] 配置日志聚合（JSON 格式输出到 stdout）
- [ ] 设置 terminationGracePeriodSeconds >= 60
- [ ] 配置 Ingress（用于 HTTP/WebSocket 外部访问）
- [ ] 使用非 root 用户运行容器

---

## 故障排查

### 端口冲突

QUIC 和 WebSocket 默认端口已分离（18900 vs 18600）。检查是否所有端口可用。

### TLS 证书

QUIC 必须配置 TLS 证书。在 K8s 中可通过 cert-manager 自动管理证书。

### 优雅关闭

默认关闭超时 30 秒。在 K8s 中设为 60 秒需同步调整 `terminationGracePeriodSeconds`。
