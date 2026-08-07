# shark-socket

[English](README.md) | **简体中文**

`shark-socket` 是为 Shark-Socket 重新设计的多协议运行时网关。
它保留了原项目的有用思想，同时将运行时归属、插件执行和优雅关闭显式化。

## 设计原则

- 网关拥有全局运行时组合。
- 传输层接收运行时依赖，不关闭共享管理器。
- 全局插件通过统一的插件运行器应用。
- 优雅关闭通过可选的传输层能力分阶段执行。
- 类型化消息通过编解码器分层，传输会话保持原始。

## 功能矩阵

| 领域 | 状态 | 说明 |
| --- | --- | --- |
| 运行时/网关 | 已实现 | 运行时注入、共享 SessionManager、插件链、分阶段停止 |
| TCP | 已实现 | 长度前缀、行、定长、原始帧，TLS 服务端/客户端，工作池，接受速率限制，写超时 |
| UDP | 已实现 | 伪会话、TTL 清扫、DTLS 支持、插件路径 |
| HTTP | 已实现 | Mode A 路由器与 Mode B 会话/插件/处理器流程 |
| WebSocket | 已实现 | 二进制消息路径、Origin 校验、心跳循环、写超时、接受速率限制 |
| CoAP | 已实现 | 消息解析/编码、CON ACK、伪会话、DTLS、选项编码（RFC 7252）、Observe（RFC 7641） |
| LwM2M | 已实现 | 带操作掩码的对象/资源模型、TLV 二进制编解码、discover/register/update/deregister/write/read、Observer 通知 |
| QUIC | 已实现 | 基于 quic-go 的 TLS 必需流传输，写超时、接受速率限制 |
| gRPC-Web | 已实现 | 直连 HTTP 模式、二进制帧/trailer、WebSocket 模式 |
| 插件 | 已实现 | Blacklist、RateLimit（滑动窗口）、Heartbeat、Persistence（Store+MessageLog）、AutoBan（按 IP 过期）、SlowHandler、Cluster |
| 安全 | 已实现 | 文件监听 TLS 证书热加载、mTLS 客户端认证、UDP/CoAP 的 DTLS |
| 持久化 | 已实现 | Store 接口（返回错误）、BoltDB 后端、带序列号的持久消息日志、会话快照 |
| 基础设施 | 已实现 | 内存缓存/存储/发布订阅/熔断器/可观测性、Prometheus 指标导出器、OpenTelemetry tracer 适配、TLS 证书缓存 |
| MQTT | 已集成 | 外部 broker 适配（paho 客户端）、docker-compose mosquitto 用于 E2E 测试 |
| 模糊测试 | 11 个测试 | TCP 帧、CoAP 消息解析、LwM2M TLV 编解码 —— 全部通过 |
| 基准测试 | 6 种协议 | TCP、UDP、HTTP、WebSocket、gRPC-Web、QUIC —— 全部已基准化 |
| 部署 | 已加固 | Docker（HEALTHCHECK、非 root）、K8s（HPA、PDB、NetworkPolicy、ConfigMap）、Helm _helpers.tpl |

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
| 覆盖率（70% 门槛） | `go run scripts/run_tests.go -mode cover` | ✅ 75.9% |
| 静态检查（golangci-lint） | `golangci-lint run` | ✅ |
| 安全扫描（govulncheck） | `govulncheck ./...` | ✅ |
| 部署清单 | `go run scripts/run_tests.go -mode deploy` | ✅ |

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
- [示例](docs/guides/EXAMPLES.md)
- [架构分析](docs/reports/ARCHITECTURE-ANALYSIS-260626.md)
- [架构方法论](docs/reports/ARCHITECTURE-METHODOLOGY-260626.md)
- [最新项目审查 (V6)](docs/reports/PROJECT-REVIEW-260806-230955.md)
- [最新部署验证 (V6)](docs/reports/DEPLOYMENT-VALIDATION-260807-010639.md)
- [更新日志](CHANGELOG.md)
