# shark-socket 云部署验证报告

> **日期:** 2026-06-26T09:30 CST | **执行者:** Claude  
> **目标服务器:** 120.76.44.233 (Alibaba Cloud ECS, Ubuntu 26.04)

---

## 环境信息

| 项目 | 详情 |
|------|------|
| OS | Linux 7.0.0-15-generic x86_64 (Ubuntu 26.04) |
| Go | go1.26.4 linux/amd64 |
| Docker | 29.5.0 |
| Git | 2.53.0 |
| CPU/RAM | 2 core / 2 GB |

---

## 测试结果

| 测试 | 结果 | 详情 |
|------|------|------|
| 单元测试 (21 包) | ✅ PASS | api/app/core/infra/plugin/protocol/runtime/transport 全部通过 |
| 集成测试 (3 包) | ✅ PASS | deploy (22s) / stress (20s) / benchmark |
| go vet | ✅ 干净 | 无警告 |
| 覆盖率 | ✅ **75.1%** | 超过 70% 阈值 |

---

## Docker 构建与运行

| 步骤 | 结果 |
|------|------|
| 镜像构建 | ✅ 成功 (shark-socket:latest) |
| 容器启动 | ✅ TCP echo = 0.0.0.0:18000, health = 0.0.0.0:18081 |
| 端口映射 | ✅ 18000/tcp, 18081/tcp |

### Dockerfile 修正

构建过程中发现 `golang:1.26-alpine` 使用 Go 1.26.3，而 go.mod 要求 >= 1.26.4。  
在 Dockerfile 中添加 `ENV GOTOOLCHAIN=auto` 启用自动工具链下载，配合 `GOPROXY=https://goproxy.cn,direct` 在中国网络环境下正常构建。

---

## 已知问题

- **GitHub 直连:** 服务器无法直连 GitHub（TLS 终止），通过 `scp` 上传源码包
- **Docker GOPROXY:** 需显式设置 `--build-arg GOPROXY=https://goproxy.cn,direct`
- **GOTOOLCHAIN:** 需在 Dockerfile 中设置 `GOTOOLCHAIN=auto`

---

## 服务器状态

| 服务器 | IP | 状态 | 备注 |
|--------|-----|------|------|
| Client | 120.76.44.233 | ✅ valid | 已通过全部测试 |
| Server | 47.110.238.85 | ❌ invalid | 未使用 (标记为 invalid) |
