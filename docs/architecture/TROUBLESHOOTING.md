# TROUBLESHOOTING.md

> Shark-Socket 故障排查指南  
> 版本：v0.2.x-alpha

---

## 目录

1. [诊断工具](#1-诊断工具)
2. [启动失败](#2-启动失败)
3. [连接问题](#3-连接问题)
4. [性能问题](#4-性能问题)
5. [插件问题](#5-插件问题)
6. [关闭问题](#6-关闭问题)
7. [Docker / K8s 问题](#7-docker--k8s-问题)
8. [常见错误码速查](#8-常见错误码速查)
9. [日志分析](#9-日志分析)

---

## 1. 诊断工具

### 1.1 健康端点

```bash
# 存活探针
curl http://localhost:18081/healthz
# → ok

# 就绪探针
curl http://localhost:18081/readyz
# → ready（Gateway 已启动）
# → 503 not ready（Gateway 未就绪）
```

### 1.2 指标端点

```bash
# Prometheus 格式指标
curl http://localhost:18080/metrics
```

### 1.3 Gateway 状态（程序化）

```go
health := gateway.Health()
// health["started"]    → bool
// health["sessions"]   → int64
// health["protocols"]  → []Protocol
// health["uptime"]     → string
```

### 1.4 测试脚本

```bash
# 全量测试
go run scripts/run_tests.go -mode all -timeout 5m

# 竞态检测
go run scripts/run_tests.go -mode race -timeout 5m

# 基准测试
go run scripts/run_tests.go -mode benchmark -timeout 5m
```

---

## 2. 启动失败

### 2.1 端口占用

**症状：** `bind: address already in use`

**诊断：**

```bash
# Linux/macOS
lsof -i :18000
netstat -tlnp | grep 18000

# Windows
netstat -ano | findstr :18000
```

**解决：** 更换端口或终止占用进程。

### 2.2 配置校验失败

**症状：** 启动时立即报错退出，日志含 `Validate()` 相关错误。

**常见原因：**

| 错误信息 | 原因 | 解决 |
|---------|------|------|
| `at least one protocol must be enabled` | 所有协议 `enabled: false` | 至少启用一个协议 |
| `duplicate protocol "tcp"` | 同一协议注册两次 | 检查 JSON 和环境变量是否重复 |
| `protocol "quic" tls_cert_file and tls_key_file are required` | QUIC 未配置 TLS | 提供证书和密钥文件 |
| `tls_cert_file and tls_key_file must be supplied together` | 证书/密钥不配对 | 两个字段同时配置 |
| `protocol "http" does not support tls_cert_file` | HTTP 配置了 TLS | HTTP 不支持 TLS，使用反向代理 |

### 2.3 证书加载失败

**症状：** `load tls certificate: ...` 或 `read tls client ca file: ...`

**排查：**

```bash
# 验证证书格式
openssl x509 -in server.crt -text -noout
openssl rsa -in server.key -check

# 验证 CA 证书
openssl x509 -in ca.crt -text -noout
```

**常见原因：**
- 证书文件路径错误（相对路径相对于进程工作目录）
- 证书格式不是 PEM
- 私钥密码保护（需先解密）
- 证书与密钥不匹配

### 2.4 配置文件格式错误

**症状：** `parse config ...: invalid character ...`

**解决：** 使用 JSON 校验工具检查配置文件格式。

---

## 3. 连接问题

### 3.1 客户端无法连接

**排查步骤：**

1. **确认端口监听**：`netstat -tlnp | grep 18000`
2. **确认防火墙**：检查 OS 防火墙和云安全组
3. **确认地址**：配置文件中 `addr` 是否为 `0.0.0.0:18000`（而非 `127.0.0.1`）
4. **检查日志**：是否有 `accept failed` 错误

### 3.2 TLS 握手失败

**症状：** `tls: handshake failure` 或 `connection reset`

**排查：**

```bash
# 测试 TLS 连接
openssl s_client -connect localhost:18000 -servername localhost

# 检查协议版本
openssl s_client -connect localhost:18000 -tls1_2
```

**常见原因：**
- 客户端未启用 TLS，服务端要求 TLS
- 证书 CN/SAN 与客户端验证的主机名不匹配
- mTLS 模式下客户端未提供证书

### 3.3 WebSocket 连接被拒

**症状：** HTTP 403 或 Origin 检查失败

**排查：**
- 检查 `allowed_origins` 配置是否包含客户端 Origin
- Origin 头是否精确匹配（包含协议、域名、端口）
- 开发环境可使用 `["*"]` 允许所有来源

### 3.4 QUIC 连接失败

**症状：** `connection refused` 或 TLS 错误

**排查：**
- QUIC 必须配置 TLS 证书
- 客户端需使用支持 QUIC 的库（如 `quic-go`）
- UDP 端口是否正确映射

---

## 4. 性能问题

### 4.1 高延迟

**排查步骤：**

1. **检查 WorkerPool**：TCP WorkerPool 队列是否满（`tcp_task_queue_full_total` 指标）
2. **检查 SlowHandler**：是否触发慢处理日志，定位耗时 Handler
3. **检查会话数**：`gateway.Health()["sessions"]` 是否接近容量上限
4. **检查系统负载**：`top` / `htop` 查看 CPU 和内存

### 4.2 高内存

**排查步骤：**

1. **会话数**：大量空闲会话未清理，检查 HeartbeatPlugin 配置
2. **写队列**：TCP 写队列积压，检查 `FullPolicy` 配置
3. **goroutine 泄漏**：使用 `pprof` 检查

```bash
# 开启 pprof（需在代码中导入 _ "net/http/pprof"）
go tool pprof http://localhost:6060/debug/pprof/goroutine
```

### 4.3 goroutine 泄漏

**症状：** goroutine 数持续增长不下降

**常见原因：**
- TCP 写队列满时 session 未正确关闭
- HeartbeatPlugin 未正确 `Stop()`，ticker goroutine 泄漏
- ClusterPlugin 未调用 `Stop()`，consume goroutine 泄漏

**诊断：**

```bash
curl http://localhost:6060/debug/pprof/goroutine?debug=1
```

### 4.4 基准测试基线

运行基准测试建立性能基线：

```bash
# 完整基准
go test -bench=. -benchmem ./tests/benchmark/ -timeout 5m

# 单协议基准
go test -bench=BenchmarkTCPEcho -benchmem ./tests/benchmark/ -timeout 2m
```

参考基线（2vCPU/2GB 云服务器）：

| 指标 | 参考值 |
|------|--------|
| SessionManager_NextID | ~8 ns/op |
| TCPEcho | ~56 μs/op |
| PluginChain（空） | ~200 ns/op |

---

## 5. 插件问题

### 5.1 插件 panic

**症状：** 日志出现 `plugin panic: ...`，该插件后续调用被跳过

**说明：** PluginChain 的 `safeRun` 捕获 panic，不影响其他插件和协议层。连接继续正常处理。

**排查：** 检查 panic 堆栈，修复插件实现中的 nil 解引用或数组越界。

### 5.2 黑名单误封

**症状：** 正常 IP 被拒绝连接

**排查：**
- 检查 BlacklistPlugin 初始化时的 IP/CIDR 列表
- AutoBanPlugin 是否误触发（检查违规阈值配置）
- 确认 IP 是否命中 CIDR 网段

### 5.3 限流过于严格

**症状：** 正常消息被丢弃（`ErrPluginDrop`）

**排查：**
- 检查 RateLimitPlugin 的 `rate` 和 `window` 配置
- 监控丢弃指标，调整阈值

### 5.4 插件顺序错误

**症状：** 预期的插件行为未生效

**说明：** 插件按 `Priority()` 数值升序执行（数值越小越先）。OnClose 按逆序执行。

**检查优先级：**

| 插件 | 优先级 |
|------|--------|
| Blacklist | 0 |
| AutoBan | 5 |
| RateLimit | 10 |
| Heartbeat | 50 |
| Persistence | 90 |
| Cluster | 95 |

---

## 6. 关闭问题

### 6.1 优雅关闭超时

**症状：** 日志出现 `drain timeout` 或关闭过程超过预期

**说明：** Gateway 三阶段关闭各有独立超时：

| 阶段 | 默认超时 | 含义 |
|------|---------|------|
| StopAccept | 5s | 停止接受新连接 |
| Drain | 5s | 等待 in-flight 请求完成 |
| CloseSessions | 10s | 关闭所有会话 |
| Finalize | 2s | 回滚/清理 |

**解决：** 增大 `shutdown_timeout` 配置值。

### 6.2 会话未关闭

**症状：** 关闭后仍有残留连接

**排查：**
- 检查 `SessionManager.Count()` 在关闭后是否归零
- 检查是否有协程持有 Session 引用未释放
- UDP 伪会话依赖 TTL 清扫，可能需要等待 sweep 周期

---

## 7. Docker / K8s 问题

### 7.1 容器立即退出

**排查：**

```bash
docker logs <container_id>
```

**常见原因：**
- 配置文件未挂载或路径错误
- 环境变量覆盖导致配置校验失败
- 端口映射错误

### 7.2 K8s Pod CrashLoopBackOff

**排查：**

```bash
kubectl describe pod <pod_name>
kubectl logs <pod_name>
```

**常见原因：**
- Liveness 探针失败（Health 地址配置为 `127.0.0.1` 而非 `0.0.0.0`）
- 资源 limits 过小导致 OOM
- 安全上下文限制文件写入（read-only rootfs）

### 7.3 K8s 探针失败

**排查：**
- `health_addr` 必须绑定 `0.0.0.0`（K8s 从 Pod 外部探测）
- `initialDelaySeconds` 是否足够（建议 ≥3 秒）
- readiness 探针检查 `/readyz`，需等待 Gateway 启动完成

### 7.4 Docker 镜像构建失败

```bash
# 检查 GOPROXY 配置（国内环境）
docker build --build-arg GOPROXY=https://goproxy.cn,direct \
  -f deploy/docker/Dockerfile .
```

---

## 8. 常见错误码速查

| 错误 | 含义 | 处理 |
|------|------|------|
| `closed` | 操作对象已关闭 | 检查生命周期 |
| `duplicate protocol` | 重复注册同一协议 | 检查配置 |
| `session manager at capacity` | 会话容量达到上限 | 增加容量或清理空闲会话 |
| `session closed` | 会话已关闭，无法发送 | 停止发送，清理引用 |
| `server closed` | 服务已关闭 | 检查 Gateway 生命周期 |
| `write queue full` | TCP 写队列满 | 增大队列或调整 FullPolicy |
| `frame too large` | 帧超过 MaxFrameBytes | 检查客户端消息大小 |
| `plugin drop message` | 插件丢弃消息（正常控制流） | 检查 RateLimit 配置 |
| `plugin block session` | 插件拒绝连接 | 检查 Blacklist/AutoBan 配置 |
| `listen failed` | 端口监听失败 | 检查端口占用和权限 |

---

## 9. 日志分析

### 9.1 日志级别使用

| 级别 | 场景 |
|------|------|
| Debug | 插件控制流（消息丢弃）、会话详细操作 |
| Info | Gateway 启动/关闭、协议注册 |
| Warn | Accept 失败、插件 panic、Drain 超时、慢处理 |
| Error | 协议 Start 失败、业务 Handler 错误 |

### 9.2 关键日志关键词

| 关键词 | 含义 | 对应问题 |
|--------|------|---------|
| `accept failed` | TCP Accept 失败 | 端口/网络问题 |
| `plugin panic` | 插件 panic 被捕获 | 插件实现 bug |
| `server start failed` | 协议 Start 失败 | 配置或端口问题 |
| `write queue full` | 写队列满 | 高负载 |
| `drain timeout` | Drain 超时 | 关闭超时配置不足 |
| `heartbeat timeout` | 心跳超时 | 客户端异常断开 |

### 9.3 日志结构化

生产环境推荐使用 JSON 格式日志：

```go
logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
    Level: slog.LevelInfo,
}))
```

日志包含结构化字段：`session_id`、`protocol`、`remote_addr`、`duration_ms`、`payload_bytes`，便于过滤和聚合。

---

**文档职责边界：** 本文档描述故障排查方法。错误分类和语义详见 ERRORS.md，部署配置详见 DEPLOYMENT.md，插件行为详见 PLUGIN.md。
