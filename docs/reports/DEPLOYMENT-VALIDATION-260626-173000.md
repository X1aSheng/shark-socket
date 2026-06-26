# shark-socket 云服务器部署验证报告

> **日期:** 2026-06-26  
> **服务器:** Alibaba Cloud ECS (Ubuntu 26.04, 2C/2GB)  
> **IP:** 120.76.44.233  
> **版本:** c32df0e4 (V4 audit + optimizations)  

---

## 部署环境

| 组件 | 版本 |
|------|------|
| OS | Ubuntu 26.04 LTS (kernel 7.0.0-15) |
| Go | 1.26.4 linux/amd64 |
| Docker | 29.5.0 |
| kubectl | v1.35.0 |
| Mosquitto | eclipse-mosquitto:2 (docker) |

---

## 验证结果

### 1. 代码获取
```
Source: GitHub → git clone https://github.com/X1aSheng/shark-socket.git
Status: ✅ 成功
Commit: c32df0e4
```

### 2. 单元测试 (云服务器)
```
go test ./... -count=1
GOPROXY=https://goproxy.cn,direct

Result: ✅ 25/25 suites PASS
Time: ~50s
```

### 3. Docker 构建
```
docker build -f deploy/docker/Dockerfile -t shark-socket:v4 .
Image SHA: fe3f7410825966b8f789ebf61b6b983ad3d56eabefade3e6b56999d69d23de4a

Result: ✅ 构建成功 (68.9s)
```

### 4. Docker Compose 部署
```
cd deploy/docker
SHARK_TCP_ADDR=0.0.0.0:18000 SHARK_HEALTH_ADDR=0.0.0.0:18081 \
  SHARK_METRICS_ADDR=0.0.0.0:18080 docker compose up -d --build

Running containers:
  - docker-shark-socket-1  (0.0.0.0:18000, :18080, :18081)  ✅ healthy
  - docker-mosquitto-1     (0.0.0.0:1883)                   ✅ healthy

Result: ✅ 2 containers healthy
```

### 5. 健康检查
```
curl http://120.76.44.233:18081/healthz → "ok"     ✅
curl http://120.76.44.233:18081/readyz  → "ready"  ✅
```

### 6. 客户端 ↔ 云端 TCP Echo
```
本地 Windows → 120.76.44.233:18000
Protocol: TCP, LengthPrefixFramer
Payload:  "HELLO-CLOUD-SHARK-SOCKET"
Response: "HELLO-CLOUD-SHARK-SOCKET"

Result: ✅ Echo 正确往返
```

### 7. GitHub Actions CI
```
CI workflow: .github/workflows/ci.yml
  - golangci-lint: v1.64.2 (pinned)
  - govulncheck: ✅
  - Docker build: ✅
  - mosquitto health check: mosquitto_sub (proper MQTT check)

Result: ✅ CI 配置正确
```

---

## 部署摘要

| 检查项 | 结果 |
|--------|------|
| 代码推送 (GitHub + Gitee) | ✅ 双仓库同步 |
| 云服务器 clone | ✅ GitHub 公共仓库 |
| `go test ./...` | ✅ 25/25 PASS |
| Docker 构建 | ✅ shark-socket:v4 |
| Docker Compose 部署 | ✅ 2 containers healthy |
| 健康检查 | ✅ healthz/readyz OK |
| TCP Echo (本地→云端) | ✅ HELLO-CLOUD-SHARK-SOCKET 正确往返 |
| Prometheus metrics | ✅ 端点响应 |
| CI 配置 | ✅ lint 版本固定, mosquitto_sub |

---

## 部署命令速查

```bash
# 云端启动
cd /opt/shark-socket/deploy/docker
SHARK_TCP_ADDR=0.0.0.0:18000 \
  SHARK_HEALTH_ADDR=0.0.0.0:18081 \
  SHARK_METRICS_ADDR=0.0.0.0:18080 \
  docker compose up -d --build

# 云端查看状态
docker ps
docker logs docker-shark-socket-1

# 本地测试
curl http://120.76.44.233:18081/healthz

# 停止服务
docker compose down
```
