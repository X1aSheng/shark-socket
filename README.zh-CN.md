# shark-socket

[English](README.md) | **简体中文**

## 项目概述

`shark-socket` 是使用 Go 语言新设计的多协议运行时网关，面向 **IoT、实时通信与边缘计算** 场景。它保留了原项目的有用思想，同时将运行时归属、插件执行和优雅关闭显式化。

**核心价值**

- **统一运行时**：单进程内同时承载 TCP、UDP、CoAP、LwM2M、WebSocket、QUIC、gRPC-Web、HTTP 等协议，共享 SessionManager、PluginRunner、Metrics 与 Logger。
- **跨协议会话管理**：统一 Session 抽象，支持跨协议查询、广播与路由。
- **可扩展插件系统**：黑名单、限流、心跳、持久化、集群、自动封禁、慢处理等内置插件，全局链式执行、panic 隔离。
- **分阶段优雅关闭**：StopAccept → Drain → CloseSessions，连接不丢失。
- **IoT 语义完整**：CoAP（RFC 7252 / RFC 7641）与 LwM2M 原生支持，含 TLV 编解码、Observe 通知与注册生命周期。

**目标场景**

- **IoT 平台**：设备经 CoAP/LwM2M 接入，Web 端经 WebSocket，管理 API 使用 HTTP。
- **实时通信网关**：WebSocket 长连接 + TCP 自定义协议 + UDP 广播。
- **边缘计算节点**：资源受限设备经 CoAP 接入，跨协议统一路由。

**边界（非目标）**

- 不参与 Nginx/Envoy 的 HTTP 反向代理竞争（HTTP 仅作轻量支持）。
- 不内置完整 MQTT Broker，由外部 [shark-MQTT](https://gitee.com/X1aSheng/shark-mqtt) 通过数据契约互通；网关以 MQTT 客户端身份接入（paho 适配器）。
- 不替代 `google.golang.org/grpc`（gRPC-Web 仅支持 Unary 与 Server Streaming）。

## 设计特点

- **网关拥有运行时**：Gateway 显式创建并注入 Runtime（SessionManager / PluginRunner / Logger / Metrics / Tracer）；传输层接收运行时依赖，不自行创建、也不关闭共享管理器。
- **统一插件执行**：全局插件通过单个 PluginRunner 链式执行，panic 在链内隔离，不传播到协议层。
- **分阶段优雅关闭**：StagedServer 定义 StopAccept → Drain → CloseSessions 三阶段，语义清晰、可回滚，连接不丢失。
- **类型化消息分层**：Codec[M] 承载类型化消息，传输会话保持原始字节，业务类型不污染运行时层。
- **接口契约优先**：所有模块通过 interface 交互、依赖倒置；`core/` 只定义契约，不依赖具体实现，层间依赖单向。
- **零值可用**：Functional Options 模式，全部配置项有合理默认值，开箱即用。
- **失败隔离**：单连接 / 单协程 panic 不影响整体；插件 panic 由 PluginRunner 捕获并返回控制错误。
- **僵尸连接回收、可观测**：每个空闲/死对端回收路径都有超时边界（TCP 读超时、UDP/CoAP TTL 清扫、DTLS 读超时、WebSocket/gRPC-Web PongTimeout、可配置的 QUIC idle 超时），并经 `sessions_reclaimed_total` 计数——幽灵连接不可能永远存活，回收可监控。
- **可观测优先**：关键路径内置 metrics / trace / log；Prometheus 指标采用固定键集合（无基数爆炸）；提供 `/healthz`、`/readyz` 端点及 `sessions_active`、`sessions_reclaimed_total` 等会话指标。
- **benchmark 驱动**：性能优化以基准测试与 pprof 热点证据为前提，禁止凭直觉改结构。
- **编译期验证**：`var _ Interface = (*Impl)(nil)` 接口满足检查，关键约束用类型系统表达。
- **安全内建**：TLS 证书热加载、mTLS、DTLS、接受速率限制、连接上限、写超时、非 root 容器、只读根文件系统、Drop ALL capabilities。

## 功能矩阵

| 领域 | 状态 | 说明 |
| --- | --- | --- |
| 运行时/网关 | 已实现 | 运行时注入、共享 SessionManager、插件链、分阶段停止 |
| TCP | 已实现 | 长度前缀、行、定长、原始帧，TLS 服务端/客户端，工作池，接受速率限制，连接上限，写超时，空闲读超时 |
| UDP | 已实现 | 伪会话、TTL 清扫、DTLS 支持（读缓冲可配置）、插件路径 |
| HTTP | 已实现 | Mode A 路由器与 Mode B 会话/插件/处理器流程 |
| WebSocket | 已实现 | 二进制消息路径、Origin 校验、心跳循环、写超时、接受速率限制、连接上限 |
| CoAP | 已实现 | 消息解析/编码、CON ACK、伪会话、DTLS（读缓冲可配置）、选项编码（RFC 7252）、Observe（RFC 7641） |
| LwM2M | 已实现 | 带操作掩码的对象/资源模型、TLV 二进制编解码、discover/register/update/deregister/write/read、Observer 通知 |
| QUIC | 已实现 | 基于 quic-go 的 TLS 必需流传输，写超时、接受速率限制、连接上限、idle 超时可配置 |
| gRPC-Web | 已实现 | 直连 HTTP 模式、二进制帧/trailer、WebSocket 模式、连接上限 |
| 插件 | 已实现 | Blacklist（精确 + CIDR）、RateLimit（32 分片滑动窗口）、Heartbeat、Persistence（Store+MessageLog）、AutoBan、SlowHandler、Cluster |
| 安全 | 已实现 | 文件监听 TLS 证书热加载、mTLS 客户端认证、UDP/CoAP 的 DTLS |
| 持久化 | 已实现 | Store 接口（返回错误）、BoltDB 后端、带序列号的持久消息日志、会话快照 |
| 基础设施 | 已实现 | 内存缓存/存储/发布订阅/熔断器/可观测性、Prometheus 指标导出器、OpenTelemetry tracer 适配、TLS 证书缓存 |
| MQTT | 已集成 | 外部 broker 适配（paho 客户端）、docker-compose mosquitto 用于 E2E 测试 |
| 僵尸回收 | 已实现 | 各传输皆有界空闲超时，经 `sessions_reclaimed_total` 计数 |
| 模糊测试 | 8 个测试 | TCP 帧、CoAP 消息解析、LwM2M TLV 编解码 —— 全部通过 |
| 压力测试 | 6 套件 | TCP 持续/突发/重连 + UDP/WebSocket/HTTP 含泄漏检测 |
| 基准测试 | 6 种协议 | TCP、UDP、HTTP、WebSocket、gRPC-Web、QUIC —— 全部已基准化 |
| 部署 | 已加固 | Docker（HEALTHCHECK、非 root）、K8s（HPA、PDB、NetworkPolicy、ConfigMap）、Helm _helpers.tpl |

## 资源需求

`shark-socket` 静态链接、无外部运行时依赖，可部署于资源受限的边缘节点。

**实测占用**（空闲，本地开发机）

- 空闲进程：私有 ~46 MB / 常驻 ~10 MB
- 每空闲 TCP 连接：~24.8 KB（关闭后完全释放——已用 2000 连接实测验证）
- 每 DTLS 对端：默认 16 KiB 读缓冲（原 64 KiB，`WithDTLSReadBufferBytes` 可配）——1 万 DTLS 对端此前仅读缓冲即占 ~640 MB

**制品大小**

| 制品 | 大小 | 说明 |
| --- | --- | --- |
| Docker 镜像 | ~40 MB | 多阶段构建，`alpine:3.22` 运行时，`CGO_ENABLED=0` 静态编译 |
| 可执行文件 | 单个二进制 | 无运行时依赖，可独立部署 |

**Kubernetes 默认资源规格（随附清单）**

| 项 | 值 |
| --- | --- |
| requests | CPU 50m / 内存 64Mi |
| limits | CPU 500m / 内存 256Mi |
| 副本 | 2（HPA 2–10，CPU 平均利用率 50% 触发扩容） |

**容量规划**

| 规格 | CPU | 内存 | 适用场景 |
| --- | --- | --- | --- |
| 最小 | 50m / 256m | 64Mi / 256Mi | 测试 / 开发 |
| 推荐 | 200m / 1000m | 128Mi / 512Mi | 生产单节点 |
| 高吞吐 | 500m / 2000m | 256Mi / 1Gi | 万级连接 |

**端口规划**

| 端口 | 协议 | 用途 |
| --- | --- | --- |
| 18000 | TCP | 业务连接（默认） |
| 18080 | HTTP | Prometheus 指标 |
| 18081 | HTTP | 健康 / 就绪探针 |
| 18443 | UDP | QUIC（TLS 必需） |
| 18500 | UDP | CoAP / LwM2M |
| 18700 | HTTP/WS | WebSocket |
| 18900 | HTTP | gRPC-Web |

**基准容量（Linux 8 核 Xeon）**

- TCP 吞吐：约 31.6 万 msg/s（50 连接，256B payload），P50 ~144µs、P99 ~401µs
- TCP / UDP / HTTP 回显延迟：约 19µs / 5µs / 31µs
- 插件链开销：4 个真实插件仅增加 ~1.7% 延迟（<5%）
- 连接抖动：50 路并发，10s 内 85.9 万次连接/断开循环零错误

## 运行

```bash
go run ./cmd/shark-socket
```

该示例在 `127.0.0.1:18000` 启动一个 TCP echo 服务器。

使用配置文件运行：

```powershell
go run ./cmd/shark-socket -config .\examples\config\multi-protocol.json
```

配置 `health_addr` 后可用的健康与就绪端点：

- `GET /healthz`
- `GET /readyz`

### MQTT 集成测试

```bash
# 启动 mosquitto broker + 运行 E2E 测试（需要 Docker）
docker compose -f deploy/docker/docker-compose.yml --profile test run mqtt-test
```

## 验证

| 检查项 | 命令 | 状态 |
|-------|---------|--------|
| 单元测试（26 套件） | `go test ./...` | ✅ |
| 竞态检测 | `go test -race ./...` | ✅ |
| 覆盖率（70% 门槛） | `go run scripts/run_tests.go -mode cover` | ✅ 78.6% |
| 静态检查（golangci-lint） | `golangci-lint run` | ✅ |
| 安全扫描（govulncheck） | `govulncheck ./...` | ✅ |
| 部署清单 | `go run scripts/run_tests.go -mode deploy` | ✅ |
| 压力测试（6 套件含泄漏检测） | `go test ./tests/stress/ -count=1 -p 1` | ✅ |

快速验证：

```bash
go run scripts/run_tests.go -mode vet
```

竞态验证：

```bash
go run scripts/run_tests.go -mode race
```

竞态模式需要以下编译器工具链：

- `D:\Programs\w64devkit\bin`
- `D:\Programs\LLVM\bin`

在 Linux runner 上，竞态验证直接使用 runner 的 C 工具链。

等价的手动命令：

```powershell
go test ./... -count=1
go vet ./...
$env:PATH='D:\Programs\w64devkit\bin;D:\Programs\LLVM\bin;' + $env:PATH
$env:CGO_ENABLED='1'
go test -race ./... -count=1
```

发布加固命令：

```powershell
go test ./internal/transport/tcp -run='^$' -fuzz=FuzzLengthPrefixFramer -fuzztime=2s
go test ./internal/transport/tcp -run='^$' -fuzz=FuzzLineFramerRead -fuzztime=2s
go test ./internal/transport/coap -run='^$' -fuzz=FuzzParseMessage -fuzztime=2s
go test './internal/transport/tcp' './internal/transport/coap' '-run=^$' '-bench=.' '-benchmem'
```

脚本化测试报告：

```powershell
go run scripts/run_tests.go -mode all
go run scripts/run_tests.go -mode unit
go run scripts/run_tests.go -mode integration
go run scripts/run_tests.go -mode benchmark
go run scripts/run_benchmarks.go -profile local -stage light
go run scripts/run_tests.go -mode deploy
```

Docker 构建支持可配置的模块代理：

```powershell
$env:GOPROXY='https://goproxy.cn,direct'
docker compose -f deploy/docker/docker-compose.yml up -d --build
```

原始 JSON 与可读报告写入 `logs/` 目录。

## 文档

- [架构](docs/architecture/ARCHITECTURE.md)
- [契约与接口](docs/architecture/CONTRACTS.md)
- [网关与运行时](docs/architecture/GATEWAY.md)
- [部署](docs/architecture/DEPLOYMENT.md)
- [配置指南](docs/guides/CONFIGURATION-20260530.md)
- [测试策略](docs/guides/TEST-STRATEGY-20260529.md)
- [协议测试指南](docs/guides/PROTOCOL-TEST-GUIDE-20260530.md)
- [MQTT 集成](docs/guides/MQTT-INTEGRATION.md)
- [示例](docs/guides/EXAMPLES.md)
- [架构分析](docs/reports/ARCHITECTURE-ANALYSIS-260626.md)
- [架构方法论](docs/reports/ARCHITECTURE-METHODOLOGY-260626.md)
- [最新项目审查 (V8)](docs/reports/PROJECT-REVIEW-260809-091049.md)
- [项目审查 (V7)](docs/reports/PROJECT-REVIEW-260808-220224.md)
- [项目审查 (V6)](docs/reports/PROJECT-REVIEW-260806-230955.md)
- [最新部署验证 (V7)](docs/reports/DEPLOYMENT-VALIDATION-260809-085443.md)
- [部署验证 (V6)](docs/reports/DEPLOYMENT-VALIDATION-260807-010639.md)
- [更新日志](CHANGELOG.md)
