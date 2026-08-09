# 云服务器部署验证报告 (V7)

- 日期: 2026-08-09 08:54
- 服务器: 120.76.44.233 (Alibaba Cloud ECS, Ubuntu, 2C / 1.6GB, Go 1.26.5, Docker 29.5)
- 提交: `32bd53d` (V7 全部修复 + OMA TLV 已推送并拉取)
- 方法: 真实云服务器编译 + 测试 + Docker 构建/部署 + 健康检查 + K8s/Helm 清单验证

## 1. 代码获取

- 服务器 `/opt/shark-socket` 原在 V6 提交 `5bdbd4b`，`git pull --ff-only` 拉取到 `32bd53d`
- 覆盖 V7 全部修复（43 项）+ P3-19 OMA TLV

## 2. 编译与测试

| 检查项 | 命令 | 结果 |
| --- | --- | --- |
| go build ./... | `go build ./...` | ✅ PASS |
| go vet ./... | `go vet ./...` | ✅ PASS |
| 生产包单元测试 | `go test ./api ./internal/... -count=1` | ✅ 21 包全过 |
| 集成/压力/部署 | `go test ./tests/... -p 1 -count=1` | ✅ tests / deploy / stress 全过 |

## 3. Docker 验证

- 多阶段构建（golang:1.26-alpine → alpine:3.22，`CGO_ENABLED=0` 静态编译，非 root）
- **构建成功**: `shark-socket:v7` = **40.2MB**
- `docker compose up -d --build`:
  - `docker-shark-socket-1` → **healthy**
  - `docker-mosquitto-1` → **healthy**

## 4. 运行验证

| 检查项 | 结果 |
| --- | --- |
| `GET /healthz` | `ok` |
| `GET /readyz` | `ready` |
| TCP Echo (LengthPrefix, :18000) | `V7-CLOUD-ECHO` 正确往返 |
| MQTT 集成测试 (真实 mosquitto) | `go test ./internal/infra/mqtt` → **PASS** (Connect/PublishSubscribe 等) |

## 5. 会话指标验证（关键）

连接一个 TCP 会话后查询 `/metrics`:

```
sessions_accepted_total 2
sessions_closed_total   2
sessions_active         0
```

- **`closed == accepted` 证明 P2-1 双计数修复生效**（旧实现优雅关闭/重复注销会导致 closed ≈ 2× accepted）
- V6.1 指标装饰器在会话事件发生时正常输出

## 6. Kubernetes / Helm 验证

| 检查项 | 结果 |
| --- | --- |
| `kubectl kustomize deploy/k8s/` | ✅ 渲染 8 资源 (Namespace/SA/ConfigMap/Service/Deployment/PDB/HPA/NetworkPolicy) |
| `helm template deploy/helm/shark-socket/` | ✅ 渲染 4 资源 |
| `helm lint deploy/helm/shark-socket/` | ✅ 0 失败 |

## 7. 观察项

1. `/metrics` 在无会话事件时为空（指标惰性注册）；有连接后正常输出——与 V6 报告的观察一致，属初始化行为而非缺陷。
2. 服务器内存 1.6GB 紧张（可用约 1.2GB），测试均以 `-p 1` 串行运行避免 OOM。

## 8. 结论

- **V7 全部修复在真实 Linux 云服务器上验证通过**：编译、单元、集成、压力、部署测试全过。
- Docker 镜像构建/部署 healthy，healthz/readyz/TCP echo/MQTT 集成全部验证通过。
- **P2-1 会话指标双计数修复经真实运行确认**（closed == accepted）。
- K8s/Helm 清单渲染 + lint 通过。
