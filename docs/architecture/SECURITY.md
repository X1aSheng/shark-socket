# SECURITY.md

> Shark-Socket 安全与防御体系  
> 版本：v0.2.x-alpha

---

## 目录

1. [安全架构总览](#1-安全架构总览)
2. [TLS 与 mTLS](#2-tls-与-mtls)
3. [插件层防御](#3-插件层防御)
4. [传输层防御](#4-传输层防御)
5. [配置层防御](#5-配置层防御)
6. [运行时防御](#6-运行时防御)
7. [容器安全](#7-容器安全)
8. [攻击面与缓解矩阵](#8-攻击面与缓解矩阵)

---

## 1. 安全架构总览

Shark-Socket 采用**分层防御**策略，安全控制分布在六个层级：

| 层级 | 防御机制 | 作用 |
|------|---------|------|
| L1 插件层 | Blacklist / AutoBan / RateLimit | 身份识别、访问控制、速率限制 |
| L2 传输层 | Framer MaxFrameBytes / MaxMessageSize / Origin Check | 输入大小限制、跨域控制 |
| L3 TLS 层 | TLS 1.2+ / mTLS / 证书校验 | 传输加密、客户端身份验证 |
| L4 配置层 | Validate() / QUIC 强制 TLS | 启动前安全检查 |
| L5 运行时层 | Plugin panic 隔离 / Session 容量限制 | 故障隔离、资源保护 |
| L6 容器层 | non-root / read-only FS / drop ALL / seccomp | 最小权限运行 |

---

## 2. TLS 与 mTLS

### 2.1 支持协议

TLS 仅在 **TCP** 和 **QUIC** 上支持，QUIC 强制要求 TLS。

| 协议 | TLS | mTLS | 说明 |
|------|-----|------|------|
| TCP | 可选 | 可选 | 通过 `tls_cert_file` / `tls_key_file` 启用 |
| QUIC | **必须** | 可选 | 启动时校验，缺少证书直接拒绝启动 |
| WebSocket | — | — | 建议前置 TLS 反向代理 |
| HTTP | — | — | 建议前置 TLS 反向代理 |
| CoAP | — | — | DTLS 需外部代理支持 |

### 2.2 配置方式

#### JSON 配置

```json
{
  "protocols": [
    {
      "name": "tcp",
      "addr": "0.0.0.0:18000",
      "tls_cert_file": "/certs/server.crt",
      "tls_key_file": "/certs/server.key",
      "tls_client_ca_file": "/certs/ca.crt",
      "tls_client_auth": "require_and_verify"
    }
  ]
}
```

#### tls_client_auth 取值

| 值 | 含义 |
|----|------|
| `none` / `no_client_cert` | 不验证客户端证书（默认） |
| `request` / `request_client_cert` | 请求但不强制 |
| `require_any` / `require_any_client_cert` | 要求有效证书，不验证 CA |
| `verify_if_given` / `verify_client_cert_if_given` | 有证书时验证 |
| `require_and_verify` / `require_and_verify_client_cert` | 强制验证（推荐生产使用） |

### 2.3 启动校验

配置校验规则（`config.go Validate()`）：

- `tls_cert_file` 和 `tls_key_file` 必须成对提供
- `tls_client_ca_file` 必须配合 cert/key 使用
- `tls_client_auth` 必须配合 cert/key 使用
- QUIC 协议必须提供 cert/key（否则启动失败）
- 非 TCP/QUIC 协议不支持 TLS 字段（否则启动失败）

### 2.4 环境变量

```bash
SHARK_TCP_CERT_FILE=/certs/server.crt
SHARK_TCP_KEY_FILE=/certs/server.key
SHARK_TCP_CLIENT_CA_FILE=/certs/ca.crt
SHARK_TCP_CLIENT_AUTH=require_and_verify
```

---

## 3. 插件层防御

插件按优先级（数值越小越先执行）构成纵深防御链：

### 3.1 IP 黑名单（BlacklistPlugin，优先级 0）

```go
blacklist := plugin.NewBlacklist("10.0.0.1", "192.168.0.0/16")
```

- **精确匹配**：IP 地址直接查哈希表，O(1)
- **CIDR 匹配**：遍历网段列表，`net.IPNet.Contains()` 判断
- 触发时返回 `ErrPluginBlock`，立即拒绝连接
- 可通过 AutoBan 动态添加

### 3.2 自动封禁（AutoBanPlugin，优先级 5）

```go
autoBan := plugin.NewAutoBan(3) // 违规 3 次封禁
```

- 追踪每个 IP 的违规次数
- 达到阈值后自动加入封禁列表
- 后续连接在 `OnAccept` 阶段被拒绝

### 3.3 速率限制（RateLimitPlugin，优先级 10）

```go
rateLimit := plugin.NewRateLimit(100, time.Second) // 每秒 100 条消息
```

- 滑动窗口算法，per-IP 独立计数
- 超限时 `OnMessage` 返回 `ErrPluginDrop`，丢弃消息但不关闭连接
- 窗口到期自动重置

### 3.4 心跳检测（HeartbeatPlugin，优先级 50）

- 定期扫描所有会话的 `LastActiveAt`
- 超过空闲阈值的会话自动关闭
- 防止僵尸连接消耗资源

### 3.5 插件组合建议

**生产环境推荐配置（优先级顺序）：**

```
Blacklist(0) → AutoBan(5) → RateLimit(10) → [业务插件] → Heartbeat(50) → Persistence(90)
```

---

## 4. 传输层防御

### 4.1 输入大小限制

| 传输层 | 配置项 | 默认值 | 作用 |
|--------|--------|--------|------|
| TCP | `MaxFrameBytes` | 1 MB | 单帧最大长度（LengthPrefixFramer） |
| WebSocket | `MaxMessageSize` | 1 MB | 单消息最大大小 |
| gRPC-Web | `MaxMessageBytes` | 4 MB | 单消息最大大小 |
| HTTP | `MaxBodyBytes` | 8 MB | 请求体最大大小 |
| CoAP | `MaxDatagram` | 64 KB | 单个 UDP 报文最大大小 |
| UDP | `MaxDatagram` | 64 KB | 单个 UDP 报文最大大小 |

超过限制时：
- TCP/WS/gRPC-Web：关闭连接，返回 `ErrFrameTooLarge` 或 `ErrMessageTooLarge`
- HTTP：返回 413 Payload Too Large
- CoAP/UDP：丢弃报文

### 4.2 WebSocket Origin 检查

```go
api.NewWebSocketServer(
    api.WithWebSocketCheckOrigin(func(r *http.Request) bool {
        origin := r.Header.Get("Origin")
        return allowedOrigins[origin]
    }),
)
```

- `AllowedOrigins` 配置白名单，精确匹配 Origin 头
- 包含 `*` 表示允许所有来源（仅限开发环境）
- 不设置时使用 gorilla/websocket 默认策略

### 4.3 CORS 配置（HTTP）

```json
{
  "name": "http",
  "addr": "0.0.0.0:18080",
  "allowed_origins": ["https://app.example.com"]
}
```

### 4.4 CoAP CON 去重

- 维护已处理 MessageID 缓存
- 重复 CON 报文直接返回缓存 ACK，不重新执行 Handler
- 防止网络重传导致重复处理

---

## 5. 配置层防御

### 5.1 启动时校验

`Config.Validate()` 在启动前执行完整检查：

| 校验项 | 错误信息 |
|--------|---------|
| 至少一个协议启用 | `at least one protocol must be enabled` |
| 重复协议名 | `duplicate protocol` |
| 协议 addr 缺失 | `protocol "X" addr is required` |
| MaxMessageBytes 为负 | `max_message_bytes must not be negative` |
| TLS 证书不配对 | `tls_cert_file and tls_key_file must be supplied together` |
| QUIC 缺少 TLS | `quic: tls_cert_file and tls_key_file are required` |
| 非 TCP/QUIC 使用 TLS | `protocol "X" does not support tls_cert_file` |
| mTLS CA 缺少证书 | `tls_client_ca_file requires tls_cert_file and tls_key_file` |
| 无效 tls_client_auth | `invalid tls_client_auth` |

**校验失败时进程拒绝启动，不会进入运行状态。**

### 5.2 环境变量覆盖安全

- 环境变量仅覆盖已知字段，不接受任意 JSON 注入
- `SHARK_*` 前缀隔离，不与其他服务冲突
- 仅在变量实际存在时覆盖（`os.LookupEnv`）

---

## 6. 运行时防御

### 6.1 Session 容量限制

`SessionManager` 默认容量上限 **100 万**（P0 阶段），超过时：

- `Register()` 返回 `ErrSessionCapacity`
- 新连接被拒绝
- 不影响已有连接

### 6.2 插件 Panic 隔离

每个插件调用包裹在 `safeRun` 中：

```go
func (pc *PluginChain) safeRun(name string, fn func() error) (err error) {
    defer func() {
        if r := recover(); r != nil {
            // 记录 panic，返回错误，不传播到协议层
            err = fmt.Errorf("plugin panic: %v", r)
        }
    }()
    return fn()
}
```

**单个插件 panic 不影响其他插件和协议层。**

### 6.3 写队列满保护

TCP Session 写队列满时：
- 返回 `ErrWriteQueueFull`
- 记录 `tcp_task_queue_full_total` 指标
- 根据 WorkerPool 策略（Block/Drop）决定后续行为

### 6.4 Gateway 启动回滚

某个协议 Start 失败时，Gateway 自动回滚已启动的协议：

```go
for i := len(started) - 1; i >= 0; i-- {
    _ = started[i].Stop(rollbackCtx)
}
```

---

## 7. 容器安全

### 7.1 Docker 安全配置

```yaml
# docker-compose.yml
read_only: true                    # 只读根文件系统
tmpfs:
  - /tmp                           # 运行时临时文件
  - /var/log                       # 日志输出
security_opt:
  - "no-new-privileges:true"       # 禁止通过 setuid/setgid 提权
cap_drop:
  - ALL                            # 删除所有 Linux capabilities
```

Dockerfile 安全措施：
- `adduser -u 1000` 确定 UID，匹配 K8s `runAsUser`
- `apk add ca-certificates` TLS 证书验证支持
- HEALTHCHECK 指令监控容器健康状态
- 多阶段构建减小攻击面

### 7.2 Kubernetes 安全配置

```yaml
# Pod 级别
securityContext:
  runAsNonRoot: true
  runAsUser: 1000
  seccompProfile:
    type: RuntimeDefault

# Container 级别
securityContext:
  allowPrivilegeEscalation: false
  readOnlyRootFilesystem: true
  capabilities:
    drop: ["ALL"]
```

### 7.3 镜像安全

| 措施 | 说明 |
|------|------|
| 多阶段构建 | 编译工具不进入运行时镜像 |
| Alpine 基础镜像 | 最小化攻击面（约 5MB） |
| non-root 用户 | `adduser -D -H shark`，进程以普通用户运行 |
| 固定版本标签 | `alpine:3.22` 避免隐式升级 |

---

## 8. 攻击面与缓解矩阵

| 攻击类型 | 缓解层 | 具体机制 |
|---------|--------|---------|
| 未授权连接 | L1 插件 | BlacklistPlugin 精确/CIDR 封禁 |
| DDoS / 洪水攻击 | L1 插件 | RateLimitPlugin 滑动窗口限流 |
| 恶意 IP 持续攻击 | L1 插件 | AutoBanPlugin 违规阈值自动封禁 |
| 超大帧攻击 | L2 传输 | MaxFrameBytes / MaxMessageSize 限制 |
| 僵尸连接资源耗尽 | L1 + L5 | HeartbeatPlugin + SessionManager 容量限制 |
| 中间人攻击 | L3 TLS | TLS 1.2+ 传输加密 |
| 伪造客户端身份 | L3 mTLS | `require_and_verify` 客户端证书验证 |
| 跨站请求伪造 | L2 传输 | WebSocket Origin / HTTP CORS 白名单 |
| 无效配置导致异常 | L4 配置 | Validate() 启动前校验 |
| 单点故障扩散 | L5 运行时 | Plugin panic 隔离、Gateway 启动回滚 |
| 容器逃逸 | L6 容器 | non-root + read-only + drop ALL + seccomp |
| 协议层重复处理 | L2 传输 | CoAP CON MessageID 去重 |

---

**文档职责边界：** 本文档描述安全防御机制和配置。插件具体实现细节见 PLUGIN.md，TLS 配置字段完整参考见 DEPLOYMENT.md 和配置文档，错误分类见 ERRORS.md。
