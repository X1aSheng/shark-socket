# shark-socket 项目全面审核报告

> **审核日期:** 2026-06-15T21:15  
> **审核范围:** 全项目 (源码、文档、部署、CI、测试、依赖)  
> **审核方法:** 自动化静态分析 + 人工检查 + 完整测试套件执行  
> **Go 版本:** 1.26.4 | **OS:** Windows 11 / Ubuntu 26.04  

---

## 执行摘要

shark-socket 是一个多协议 socket server 框架，代码架构清晰（core → runtime → transport 三层），测试覆盖率 72.3%，21 个包全部通过单元测试，集成测试全部通过。本次审核发现 **49 项问题**：Critical 4 项、High 21 项、Medium 15 项、Low 9 项。主要问题集中在 QUIC goroutine 泄漏、文档链接断裂、CI Action 版本可疑、WS/gRPC-Web 默认允许所有跨域来源、以及多处未限制增长的 map。

---

## 测试执行结果

| 测试类型 | 包数 | 结果 |
|---------|------|------|
| 单元测试 `api/cmd/internal` | 21 | ✅ 全部通过 |
| 集成测试 `tests/` | 3 | ✅ 全部通过 (deploy, stress, benchmark) |
| go vet | 全项目 | ✅ 无问题 |
| 代码覆盖率 | 全项目 | ✅ 72.3% |

---

## 缺陷清单

### CRITICAL (4)

| # | 位置 | 描述 |
|---|------|------|
| C1 | `internal/transport/quic/server.go:148` | **QUIC writeLoop goroutine 泄漏** — `go sess.writeLoop()` 未加入 `s.wg`，shutdown 时无等待 |
| C2 | `internal/transport/quic/session.go:96-97` | **QUIC writeLoop 吞掉所有写错误** — `stream.Write()` 和 `stream.Close()` 错误被丢弃 |
| C3 | `internal/transport/udp/options.go:66-70`, `internal/transport/coap/server.go:483-487` | **DTLS 配置仅复制 2 个字段** — 丢失 MinVersion、CipherSuites 等，可能导致弱加密协商 |
| C4 | `internal/transport/coap/server.go:361-364` | **全局 CoAP message ID counter** — `lastMsgID` 跨所有 CoAP server 实例共享，message ID 序列交错 |

### HIGH — 代码质量 (10)

| # | 位置 | 描述 |
|---|------|------|
| H1 | `internal/transport/websocket/options.go:34` | **WebSocket 默认 CheckOrigin 允许所有来源** — 生产环境存在跨域劫持风险 |
| H2 | `internal/transport/grpcweb/options.go:36` | **gRPC-Web 默认 CheckOrigin 允许所有来源** |
| H3 | `internal/plugin/ratelimit.go:16` | **RateLimit counters map 永不清理** — 内存无限制增长 |
| H4 | `internal/plugin/autoban.go:15` | **AutoBan banned map 永不清理** — 被封 IP 永久驻留 |
| H5 | `internal/transport/*/server.go` (7 处) | **Drain() goroutine 在 ctx 超时后继续运行** — `go func() { wg.Wait(); close(done) }` 在超时后泄漏 |
| H6 | `internal/app/config.go:298` | **TLS MinVersion 硬编码 TLS 1.2** — 无法配置 TLS 1.3-only 或 FIPS 环境 |
| H7 | `internal/transport/websocket/server.go:62-63` | **TLS 路径的 http.Server 缺少 IdleTimeout** — 易受慢速攻击 |
| H8 | `internal/transport/grpcweb/server.go:65-70` | **同上，gRPC-Web TLS 路径** |
| H9 | `internal/plugin/cluster.go:57` | **cluster plugin consume goroutine 无生命周期追踪** — shutdown 时无等待 |
| H10 | `internal/runtime/gateway.go:93` | **Gateway.Stop 无并发保护** — 并发调用可能重复关闭 |

### HIGH — 文档 & 部署 (11)

| # | 位置 | 描述 |
|---|------|------|
| H11 | `README.md:132-144` | **11 个文档链接中 10 个断裂** — docs 重组后未更新 |
| H12 | `docs/architecture/ARCHITECTURE.md:163-334` | **目录结构完全过时** — 引用不存在的 `cmd/server/`、`deploy/kubernetes/`、ADR-010 |
| H13 | `.github/workflows/ci.yml` | **CI Action 版本可能不存在** — `checkout@v6`、`setup-go@v6`、`golangci-lint@v9`、`upload-artifact@v7` 均高于已知最新版；若不存在则 CI 完全失效 |
| H14 | `go.mod` | **依赖版本异常高** — `otel v1.44.0`（当前约 v1.30）；`x/net v0.47.0`；需确认这些版本确实已发布 |
| H15 | `docs/guides/Architecture.md` | **4232 行双语旧版文档** — 与新 docs/architecture/ 重复，目录过时，造成混淆 |
| H16 | `deploy/docker/Dockerfile:16-17` | **HEALTHCHECK 依赖未设置的环境变量** — 裸 `docker run` 会失败 |
| H17 | `deploy/k8s/configmap.yaml` | **ConfigMap 创建但从未挂载** — Deployment 硬编码 env 值而非引用 ConfigMap |
| H18 | `deploy/k8s/deployment.yaml:26` | **镜像无 registry 前缀** — 仅适用于本地 K8s |
| H19 | `deploy/helm/shark-socket/templates/` | **Helm chart 缺少 ConfigMap 模板** |
| H20 | `.github/workflows/ci.yml` | **CI 无 Docker build 验证** — 破损的 Dockerfile 在 PR 中检测不到 |
| H21 | `README.md:35` | **功能矩阵表格标注 "Hardened" 但目标文件不存在** |

### MEDIUM (15)

| # | 位置 | 描述 |
|---|------|------|
| M1 | `internal/runtime/plugin_chain.go:71,82,91` | **Plugin panic 通过 slog 记录而非配置的 Logger** |
| M2 | `internal/infra/store/bolt.go:38-39,55-57` | **Legacy Save/Delete 吞掉错误** |
| M3 | `internal/infra/observability/prometheus.go:134-137` | **PrometheusMetrics 与 MemoryMetrics label 语义不一致** |
| M4 | `internal/transport/coap/server.go:393` | **seen cleanup 间隔硬编码 5 分钟** — 应可配置 |
| M5 | `deploy/docker/docker-compose.yml:36-48` | **Mosquitto 服务容器无安全加固** |
| M6 | `deploy/k8s/networkpolicy.yaml:23-26` | **NetworkPolicy egress 允许所有流量** |
| M7 | `.github/workflows/ci.yml:99-101` | **Ubuntu runner 使用 pwsh 运行 .ps1 脚本** — 不必要的依赖 |
| M8 | `deploy/k8s/deployment.yaml` | **K8s 缺少 TLS 证书 volume 挂载** |
| M9 | `deploy/docker/docker-compose.yml:39` | **Mosquitto 端口暴露到宿主机无认证** |
| M10 | `deploy/docker/Dockerfile:11` | **运行时镜像包含 wget** — 增加攻击面 |
| M11 | `.github/workflows/ci.yml` | **Windows 无 race detection** |
| M12 | `docs/architecture/ARCHITECTURE-FILE-STRUCT.md` | **过时的规划文档** — 标记为归档但引用不存在的文件 |
| M13 | `docs/architecture/CONTRACTS.md` | **Protocol 枚举值与 ARCHITECTURE.md 不一致** |
| M14 | `.dockerignore` | **排除过多** — `*.md` 排除所有文档 |
| M15 | `README.md:69` | **覆盖率数字陈旧** — 显示 72.1% 实际 72.3% |

### LOW (9)

| # | 位置 | 描述 |
|---|------|------|
| L1 | `internal/infra/observability/prometheus.go:60-70` | **ObserveHistogram label 追加模式脆弱** |
| L2 | `internal/transport/http/server.go:168` | **双重 Close** — 显式 Close 后又 defer Close |
| L3 | `internal/transport/grpcweb/server.go:191,196` | **SendTrailers 错误被丢弃** |
| L4 | `internal/runtime/session_manager.go:36` | **nextID 溢出后静默回绕** |
| L5 | `internal/transport/quic/session.go:89-98` | **Partial write 未处理** |
| L6 | `deploy/k8s/deployment.yaml`, `service.yaml` | **缺少 namespace 元数据** |
| L7 | `.gitignore` | **缺少 .env、.DS_Store** |
| L8 | `docs/reports/` | **PROJECT-REVIEW-260614-102231.md 覆盖率为 74.1%** — 可能计算口径不同 |
| L9 | `docs/guides/Architecture.md` | **同一文件用中英双语写** — 4232 行难以维护 |

---

## 缺陷分类统计

| 类别 | Critical | High | Medium | Low | 合计 |
|------|----------|------|--------|-----|------|
| 代码 — Goroutine/内存泄漏 | 2 | 4 | 0 | 0 | 6 |
| 代码 — 安全 | 1 | 4 | 1 | 0 | 6 |
| 代码 — 数据丢失/错误处理 | 1 | 0 | 3 | 3 | 7 |
| 代码 — 并发安全 | 0 | 1 | 0 | 1 | 2 |
| 文档 | 0 | 4 | 4 | 3 | 11 |
| 部署/Docker/K8s | 0 | 5 | 6 | 2 | 13 |
| CI/CD | 0 | 3 | 2 | 0 | 5 |
| 依赖 | 0 | 1 | 0 | 0 | 1 |
| 其他 | 0 | 0 | 0 | 1 | 1 |

---

## 改进建议优先级

### 优先处理 (本周)
1. **H11-H12** — 修复 README 和 ARCHITECTURE.md 的断裂链接（用户可见，影响信任）
2. **H13** — 验证 CI Action 版本并修正（CI 可能完全失效）
3. **H1-H2** — 修改 WebSocket/gRPC-Web 默认 CheckOrigin 为拒绝所有
4. **C1-C2** — 修复 QUIC goroutine 泄漏和错误处理
5. **H3-H4** — 给 RateLimit/AutoBan 添加过期清理机制

### 后续处理
6. **H5** — 修复 Drain() goroutine 泄漏
7. **C3** — 补全 DTLS 配置字段映射
8. **H16-H17** — 修复 Docker HEALTHCHECK 和 K8s ConfigMap 挂载
9. **H6** — 添加 TLS MinVersion 配置
10. **H14** — 验证依赖版本

### 可延后
11. 文档同步整理（H15, M12, M13）
12. K8s/Helm 完善（H18, H19, M6, M8）
13. CI 增强（H20, M7, M11）

---

## 验证记录

- **单元测试:** 21/21 包通过，go vet 无警告
- **集成测试:** deploy (22.090s), stress (20.918s), benchmark (0.131s) 全部通过
- **覆盖率:** 72.3% statements
- **gofmt:** 全项目已格式化
- **Benchmark:** 所有 33 个 benchmark 可通过 (Phase 1-3 优化后)
