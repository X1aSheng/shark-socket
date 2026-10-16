# SECURITY.md

> Shark-Socket 安全与防御体系  
> 版本:v0.2.x-alpha

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

Shark-Socket 采用**分层防御**策略,安全控制分布在六个层级:

| 层级 | 防御机制 | 作用 |
|------|---------|------|
| L1 插件层 | Blacklist / AutoBan / RateLimit | 身份识别,访问控制,速率限制 |
| L2 传输层 | Framer MaxFrameBytes / MaxMessageSize / Origin Check | 输入大小限制,跨域控制 |
| L3 TLS 层 | TLS 1.2+ / mTLS / 证书校验 | 传输加密,客户端身份验证 |
| L4 配置层 | Validate() / QUIC 强制 TLS | 启动前安全检查 |
| L5 运行时层 | Plugin panic 隔离 / Session 容量限制 | 故障隔离,资源保护 |
| L6 容器层 | non-root / read-only FS / drop ALL / seccomp | 最小权限运行 |

---

## 2. TLS 与 mTLS

### 2.1 支持协议

TLS 在 **TCP** 与 **QUIC** 上支持(QUIC 强制要求),DTLS 在 **UDP** 与 **CoAP** 上支持.

| 协议 | TLS/DTLS | mTLS | 说明 |
|------|----------|------|------|
| TCP | 可选 | 可选 | 通过 `tls_cert_file` / `tls_key_file` 启用,证书与客户端 CA 均热加载 |
| QUIC | **必须** | 可选 | 启动时校验,缺少证书直接拒绝启动 |
| WebSocket | - | - | 建议前置 TLS 反向代理 |
| HTTP | - | - | 建议前置 TLS 反向代理 |
| UDP | 可选(DTLS) | 可选 | 通过 `tls_cert_file` / `tls_key_file` 启用 |
| CoAP | 可选(DTLS) | 可选 | 通过 `tls_cert_file` / `tls_key_file` 启用 |

只有 `tcp` / `udp` / `coap` / `quic` 接受 TLS 相关字段,其它协议在配置校验阶段即被拒绝.
证书与客户端 CA 的热加载机制见 2.5.

**UDP/CoAP DTLS 与 mTLS 注意**:应用配置路径(JSON / `SHARK_*` 环境变量)的证书经
`GetCertificate` 桥接进 pion/dtls(配置 `Certificates` 恒为空),映射层负责在
Accept 后按 `tls_client_auth` 策略强制校验客户端证书(缺失/链不可信即关闭连接).
该强制不依赖 pion/dtls 自身的客户端认证流程--历史版本(含 v3.1.2)在部分路径上
不会可靠执行链校验,客户端认证握手也可能停滞,因此接入真实 DTLS 客户端栈
(libcoap,openssl 等)时仍需做互操作验证.当前锁定版本为 v3.1.12.

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
| `none` / `no_client_cert` | 不验证客户端证书(默认) |
| `request` / `request_client_cert` | 请求但不强制 |
| `require_any` / `require_any_client_cert` | 要求有效证书,不验证 CA |
| `verify_if_given` / `verify_client_cert_if_given` | 有证书时验证 |
| `require_and_verify` / `require_and_verify_client_cert` | 强制验证(推荐生产使用) |

### 2.3 启动校验

配置校验规则(`config.go Validate()`):

- `tls_cert_file` 和 `tls_key_file` 必须成对提供
- `tls_client_ca_file` 必须配合 cert/key 使用
- `tls_client_auth` 必须配合 cert/key 使用
- QUIC 协议必须提供 cert/key(否则启动失败)
- 非 TCP/UDP/CoAP/QUIC 协议不支持 TLS 字段(否则启动失败)

### 2.4 环境变量

```bash
SHARK_TCP_CERT_FILE=/certs/server.crt
SHARK_TCP_KEY_FILE=/certs/server.key
SHARK_TCP_CLIENT_CA_FILE=/certs/ca.crt
SHARK_TCP_CLIENT_AUTH=require_and_verify
```

### 2.5 证书与客户端 CA 的热加载

服务端证书与 mTLS 客户端 CA **都不需要重启进程即可轮换**:

```go
// internal/infra/tlsutil/cert_cache.go
type CertCache struct {
    mu           sync.RWMutex
    cert         *tls.Certificate
    certFile     string
    keyFile      string
    clientCAPool *x509.CertPool
    clientCAFile string
}

func NewCertCache(certFile, keyFile string) *CertCache
func (c *CertCache) SetClientCA(caFile string)
func (c *CertCache) Load() error                             // 原子替换
func (c *CertCache) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error)
func (c *CertCache) GetClientCAPool() *x509.CertPool
func (c *CertCache) Files() []string
```

`Load()` 的原子性是关键:**先读盘并解析全部输入,全部成功后才提交**.一个损坏的客户端 CA
文件不会让正在服务的证书池处于半更新状态 -- 失败时旧证书与旧 CA 池完整保留.

`internal/app` 在装配时收集所有需要 TLS 的协议的 `CertCache`,并为每个 cache 启动一个
30 秒轮询的观察者:

```go
cancel := tlsutil.WatchFilesWithWG(a.appCtx, 30*time.Second, func() {
    if err := c.Load(); err != nil {
        logger.Error("cert reload failed", "error", err)
    } else {
        logger.Info("cert reload successful")
    }
}, &a.certWG, c.Files()...)
```

**客户端 CA 必须走 `GetConfigForClient`,不能直接赋给 `ClientCAs`:**

```go
// internal/app/config.go
if cache.GetClientCAPool() != nil {
    // tls.Config.ClientCAs 在握手时读取一次捕获到的值,直接赋值会把启动时
    // 加载的 CA 钉死,此后轮换 CA 文件(观察者调用 cache.Load())直到进程
    // 重启都不会生效.
    cfg.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) {
        next := cfg.Clone()
        next.GetConfigForClient = nil // 克隆不得重入本钩子
        next.ClientCAs = cache.GetClientCAPool()
        return next, nil
    }
}
```

| 目标 | 机制 | 生效时机 |
|------|------|----------|
| 服务端证书轮换 | `tls.Config.GetCertificate` -> `CertCache.GetCertificate` | 每次握手 |
| mTLS 客户端 CA 轮换 | `tls.Config.GetConfigForClient` -> `CertCache.GetClientCAPool` | 每次握手 |
| 磁盘变更检测 | `tlsutil.WatchFilesWithWG`,30s 轮询 mtime | 变更后 <= 30s |
| 轮换失败 | `Load()` 返回错误,旧值保留 | 立即(记录 Error 日志) |

**验证:** `internal/app/mtls_hot_reload_test.go` 的
`TestClientCAHotReloadTakesEffect` 用真实 CA 做端到端确认 -- 轮换前只有 ca-one 签发的
客户端证书能通过校验,轮换后只有 ca-two 的能通过,且 ca-one 立即失效.
若把 CA 池直接赋给 `ClientCAs`(去掉 `GetConfigForClient`),该测试会失败并明确指出
"CA 池被钉在启动值上".

---

## 3. 插件层防御

插件按优先级(数值越小越先执行)构成纵深防御链:

### 3.1 IP 黑名单(BlacklistPlugin,优先级 0)

```go
blacklist := plugin.NewBlacklist("10.0.0.1", "192.168.0.0/16")
```

- **精确匹配**:IP 地址直接查哈希表,O(1);条目与对端地址均做 IP 归一化
  (IPv4-mapped `::ffff:a.b.c.d` 与纯 IPv4 等价,不会绕过精确条目)
- **CIDR 匹配**:遍历网段列表,`net.IPNet.Contains()` 判断
- 触发时返回 `ErrPluginBlock`,立即拒绝连接

### 3.2 自动封禁(AutoBanPlugin,优先级 5)

```go
autoBan := plugin.NewAutoBan(3) // 违规 3 次封禁
```

- 追踪每个 IP 的违规次数
- 达到阈值后自动加入封禁列表
- 后续连接在 `OnAccept` 阶段被拒绝

### 3.3 速率限制(RateLimitPlugin,优先级 10)

```go
rateLimit := plugin.NewRateLimit(100, time.Second) // 每秒 100 条消息
```

- 滑动窗口算法,per-IP 独立计数
- 超限时 `OnMessage` 返回 `ErrPluginDrop`,丢弃消息但不关闭连接
- 窗口到期自动重置

### 3.4 心跳检测(HeartbeatPlugin,优先级 50)

- 定期扫描所有会话的 `LastActiveAt`
- 超过空闲阈值的会话自动关闭
- 防止僵尸连接消耗资源

### 3.5 插件生命周期(重要)

插件分为两类:

- **被动钩子插件**(Blacklist,Persistence,业务插件):只实现
  `OnAccept/OnMessage/OnClose`,无需额外管理.
- **生命周期插件**(AutoBan,RateLimit,Heartbeat,Cluster):内部有后台
  清理/扫荡 goroutine(例如过期封禁与限速计数回收,心跳扫荡,集群消费),
  必须随 Gateway 启停.这些插件实现 `core.LifecyclePlugin`(`Start()/Stop()`);
  经 `api.WithPlugins` 挂载后,**Gateway.Start 会自动按优先级启动它们,
  Gateway.Stop 会在所有会话关闭后自动逆序停止它们**,无需手工调用.
  若脱离 Gateway 直接使用插件链,需自行调用 `Start()`/`Stop()`(实现幂等,
  可安全重复调用与 Stop-without-Start).

> 历史缺陷(已修复):插件 `Start/Stop` 曾完全未被 Gateway 调用,导致
> Heartbeat/Cluster 挂载后不工作,AutoBan/RateLimit 的清理永不运行,计数
> 表随远端 IP 无界增长.

### 3.6 插件组合建议

**生产环境推荐配置(优先级顺序):**

```
Blacklist(0) -> AutoBan(5) -> RateLimit(10) -> [业务插件] -> Heartbeat(50) -> Persistence(90)
```

说明:
- AutoBan 是**按消息数**计数的每 IP 限幅器(不是违规检测器):阈值即该 IP
  允许发送的消息总数,默认 3 偏保守,需按业务量调大,否则会误封合法客户端;
- 封禁/计数表在进程内存中,**进程重启即清零**,且清理周期由自动生命周期
  保证;需要持久封禁时请接入外部封禁列表;
- 反向代理/网关拓扑下所有客户端共享代理 IP,黑名单/封禁/限流退化为单键,
  请按部署拓扑配置.

---

## 4. 传输层防御

### 4.1 输入大小限制

| 传输层 | 配置项 | 默认值 | 作用 |
|--------|--------|--------|------|
| TCP | `MaxFrameBytes` | 1 MB | 单帧最大长度(LengthPrefixFramer) |
| WebSocket | `MaxMessageSize` | 1 MB | 单消息最大大小 |
| gRPC-Web | `MaxMessageBytes` | 4 MB | 单消息最大大小 |
| HTTP | `MaxBodyBytes` | 8 MB | 请求体最大大小 |
| CoAP | `MaxDatagram` | 64 KB | 单个 UDP 报文最大大小 |
| UDP | `MaxDatagram` | 64 KB | 单个 UDP 报文最大大小 |

超过限制时:
- TCP/WS/gRPC-Web:关闭连接,返回 `core.ErrFrameTooLarge`(没有单独的「消息过大」哨兵)
- HTTP:返回 413 Payload Too Large
- CoAP/UDP:丢弃报文

### 4.2 WebSocket Origin 检查

```go
api.NewWebSocketServer(
    api.WithWebSocketCheckOrigin(func(r *http.Request) bool {
        origin := r.Header.Get("Origin")
        return allowedOrigins[origin]
    }),
)
```

- `AllowedOrigins` 配置白名单,精确匹配 Origin 头
- 包含 `*` 表示允许所有来源(仅限开发环境)
- **不设置时默认拒绝所有来源**(库默认 `CheckOrigin` 返回 false,与
  gorilla/websocket 的宽松默认不同--禁止跨站 WebSocket 劫持;同源 Web
  应用需显式配置自己的 Origin)

### 4.3 CORS 配置(HTTP)

```json
{
  "name": "http",
  "addr": "0.0.0.0:18080",
  "allowed_origins": ["https://app.example.com"]
}
```

### 4.4 CoAP CON 去重

- 维护已处理 MessageID 缓存
- 重复 CON 报文直接返回缓存 ACK,不重新执行 Handler
- 防止网络重传导致重复处理

---

## 5. 配置层防御

### 5.1 启动时校验

`Config.Validate()` 在启动前执行完整检查:

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

**校验失败时进程拒绝启动,不会进入运行状态.**

### 5.2 环境变量覆盖安全

- 环境变量仅覆盖已知字段,不接受任意 JSON 注入
- `SHARK_*` 前缀隔离,不与其他服务冲突
- 仅在变量实际存在时覆盖(`os.LookupEnv`)

---

## 6. 运行时防御

### 6.1 Session 容量限制

`SessionManager` 默认容量上限 **100 万**(P0 阶段),超过时:

- `Register()` 返回 `ErrSessionCapacity`
- 新连接被拒绝
- 不影响已有连接

### 6.2 插件 Panic 隔离

五个插件钩子(以及 Start/Stop)各有一个包装,panic 一律转为 `core.ErrPluginPanic`:

```go
func (c *PluginChain) safeAccept(p core.Plugin, logger core.Logger, sess core.Session) (err error) {
    defer func() {
        if r := recover(); r != nil {
            logger.Error("plugin accept panic", "plugin", p.Name(), "panic", r)
            err = core.ErrPluginPanic
        }
    }()
    return p.OnAccept(sess)
}
```

| 包装 | 钩子 | panic 后 |
|------|------|----------|
| `safeAccept` | `OnAccept` | 返回 `ErrPluginPanic`,链逆序回滚 |
| `safeMessage` | `OnMessage` | 返回 `ErrPluginPanic`,链终止 |
| `safeClose` | `OnClose` | 记录日志,继续下一个插件 |
| `safeStart` | `Start` | 返回 `ErrPluginPanic`,`StartAll` 回滚 |
| `safeStop` | `Stop` | 返回 `ErrPluginPanic`,`StopAll` 继续 |

**单个插件 panic 不影响其他插件和协议层.** `Start` / `Stop` 也在包装内(V9 修复项):
此前它们完全没有 recover,插件后台循环的启动 panic 会绕过网关的错误路径直接终止进程.

用户 handler 走同一套思路:`shared.CallHandler` 捕获 panic,记录日志并返回
`core.ErrHandlerPanic`,随后关闭该会话.`SessionManager.Broadcast` / `CloseAll` 中会话实现的
panic 由 `safeSessionCall` 隔离.

### 6.3 写队列满保护

TCP 会话写队列满时:

- 返回 `ErrWriteQueueFull`
- 记录 `tcp_task_queue_full_total` 指标
- 按 WorkerPool 的 `FullPolicy` 决定后续行为

| 策略 | 行为 | 风险 |
|------|------|------|
| `PolicyDrop`(默认) | 返回 `ErrWriteQueueFull`,消息丢弃 | 消息丢失 |
| `PolicyBlock` | 提交方阻塞,同时监听池停止与会话 Context | 对端断连时可立即返回,不会泄漏 goroutine |
| `PolicyClose` | 返回 `ErrWriteQueueFull` 并关闭会话 | 连接中断,客户端可感知 |

`PolicyBlock` 的阻塞发送同时 select 三个出口(队列、`done`、`sess.Context().Done()`),
因此队列满且对端已断连时提交方立刻返回 `ErrSessionClosed`,不会为每条卡住的连接留下一个
永久阻塞的 goroutine.这是拒绝服务角度最容易出问题的一处:只 select 队列的实现会在
对端消失后把整个 readLoop 卡死.

### 6.4 Gateway 启动回滚

某个协议 Start 失败时,Gateway 自动回滚已启动的协议:

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

Dockerfile 安全措施:
- `adduser -u 1000` 确定 UID,匹配 K8s `runAsUser`
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
| Alpine 基础镜像 | 最小化攻击面(约 5MB) |
| non-root 用户 | `adduser -D -H shark`,进程以普通用户运行 |
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
| 单点故障扩散 | L5 运行时 | Plugin panic 隔离,Gateway 启动回滚 |
| 容器逃逸 | L6 容器 | non-root + read-only + drop ALL + seccomp |
| 协议层重复处理 | L2 传输 | CoAP CON MessageID 去重 |
| 僵尸连接资源耗尽 | L1 + L5 | 传输读超时/TTL 清扫 + HeartbeatPlugin + SessionManager 容量;含观察者会话豁免空闲清扫,订阅经 RST / 重传耗尽 / 会话拆除结束 |
| 无界订阅/注册状态增长 | L2 + 应用层 | CoAP observe:每源 64 / 全局 4096 上限,同 (resource,remote) 重复注册替换,RST 注销,CON 通知重传耗尽自动注销;LwM2M 注册表:容量上限(默认 65536,可配)与节流过期清扫,lifetime 上限 30 天 |
| 明文 UDP 伪造源填充 | L2 + 部署 | UDP/CoAP 会话按源地址记账,空闲 TTL 回收,closed 会话即时重建;生产建议 DTLS + mTLS 或可信网络边界 |

---

**文档职责边界:** 本文档描述安全防御机制和配置.插件具体实现细节见 PLUGIN.md,TLS 配置字段完整参考见 DEPLOYMENT.md 和配置文档,错误分类见 ERRORS.md.
