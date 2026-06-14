# 项目功能缺口分析报告

> **生成日期:** 2026-06-14
> **依据:** 代码审查、覆盖分析、文档审计、版本规划

---

## 一、概述

当前项目状态：**v0.2.x-alpha** 阶段，核心运行时和 8 种传输协议已完成。
此报告列出所有未完成、待优化或待补充的工作项，按优先级分组。

**最近完成（06-14 本轮推进）:**
- ✅ CI 覆盖率阈值 50% → **70%**
- ✅ MQTT CI 集成 — workflow 中添加 mosquitto 服务
- ✅ 7 个传输层 session 0% 方法覆盖测试（TCP/UDP/HTTP/WS/QUIC/gRPC-Web）
- ✅ API TLS/Handler Option 构造函数测试（13 个函数）
- ✅ BoltDB Legacy 方法测试（Save/Load/Delete/DeleteBatch）
- ✅ examples 编译检查（11 个 examples 全部通过）
- ✅ 总覆盖率 **73.2% → 74.1%**（+0.9%）

---

## 二、P0 — 阻塞 / 必须修复

当前无 P0 阻塞项。所有严重缺陷已于 06-02 审计中修复。

---

## 三、P1 — 高优先级（建议下一迭代完成）

### 1. 12 项中型遗留缺陷（06-02 深度审计）

来自 v0.1.0-rc 审计的 37 项 P2 缺陷中，约 **12 项仍未修复**
（原审计文件已整合删除，需重新审计确认具体清单）。
这些缺陷涉及传输层健壮性、超时配置边界、日志一致性等。
建议在下一迭代中进行全面代码审查以重新识别。

### 2. MQTT 集成测试需 Broker

```
internal/infra/mqtt/mqtt_test.go:20   t.Skip("SHARK_MQTT_BROKER not set")
internal/infra/mqtt/mqtt_test.go:48   t.Skip("SHARK_MQTT_BROKER not set")
```

每次测试都因缺少 MQTT Broker 被跳过 → 无法在 CI 中验证。
- **建议:** 在 CI 中启动 docker-compose mosquitto 服务，设置 `SHARK_MQTT_BROKER` 环境变量

### 3. CI 覆盖率阈值未强制

覆盖率从 72.1% 提升到 73.3%，但目前 CI 中覆盖率检查通过标准缺失。
- **文件:** `.github/workflows/ci.yml` / `scripts/run_tests.go:134`
- **建议:** 设置 `minCoverage` 为 70%，强制作业失败阈值

### 4. 测试跳过项中缺少 kubectl/helm

项目对 K8s/Helm 部署有完整 manifest 和语义测试，但依赖本地安装的工具：
- `tests/deploy/deploy_test.go` 中使用 `kubectl kustomize`、`helm template`
- 在未安装工具的 CI runner 上静默跳过
- **建议:** 在 CI 中安装 kubectl + helm 或使用 `--validate` 模式

---

## 四、P2 — 中优先级

### 5. 会话方法零覆盖率（所有传输层）

每个传输层的 session 实现都有 7-9 个 0% 覆盖率的接口方法：

| 传输层 | 0% 方法 |
|---|---|
| **TCP** | Protocol, RemoteAddr, LocalAddr, CreatedAt, LastActiveAt, SetMeta, GetMeta, DelMeta |
| **UDP** | 同上（8 个方法） |
| **HTTP** | 同上（8 个方法） + Network, String |
| **WebSocket** | 同上（8 个方法）+ ping |
| **gRPC-Web** | 同上（8 个方法）+ Network, String (×2 session types) |
| **QUIC** | 同上（8 个方法） |

这些方法大部分是一行 getter/setter，风险低但反映了测试覆盖不足。
- **建议:** 使用 `benchSession` 模式添加简易单元测试覆盖

### 6. API 层 TLS/Handler Option 零覆盖

| 函数 | 文件:行 | 覆盖率 |
|---|---|---|
| `WithTCPTLS` | `api/api.go:132` | 0% |
| `WithUDPDTLS` | `api/api.go:152` | 0% |
| `WithHTTPHandler` | `api/api.go:164` | 0% |
| `WithWebSocketHandler` | `api/api.go:184` | 0% |
| `WithWebSocketCheckOrigin` | `api/api.go:188` | 0% |
| `WithCoAPDTLS` | `api/api.go:204` | 0% |
| `WithQUICAddr/TLS/Handler` | `api/api.go:232-240` | 0% |
| `WithGRPCWebHandler` | `api/api.go:252` | 0% |
| `WithGRPCWebCheckOrigin` | `api/api.go:264` | 0% |
| `NewOpenTelemetryTracer` | `api/api.go:324` | 0% |
| `AdaptTyped` | `api/api.go:328` | 0% |

- **建议:** `api_test.go` 中添加构造函数调用测试

### 7. OpenTelemetry 集成未测试

`internal/infra/observability/otel.go` 中的 OTel 适配器有 0% 测试覆盖，
且从未在集成场景中验证。
- **建议:** 添加 OTel 导出器测试（可用内存导出器验证 span 创建）

### 8. BoltDB Legacy 方法未测试

`internal/infra/store/bolt.go` 中有 4 个方法覆盖率为 0%：
- `Save`, `Load`, `Delete`（Legacy Store 接口）
- `DeleteBatch`（BulkDeleter 接口）

这些是 StoreV2 之外的遗留方法。
- **建议:** 添加简易调用测试

### 9. NopLogger / NopMetrics 分支未覆盖

`internal/core/observability.go` 中的 NopLogger/NopMetrics/NopTracer 实现
（Debug, Info, Warn, Error, IncCounter, SetGauge, ObserveHistogram, RecordError 等）
覆盖率为 0%。
- **建议:** 无需单独测试，被 Gateway 默认配置覆盖即可

---

## 五、P3 — 低优先级 / 功能增强

### 10. RateLimit 缺少自定义 KeyFunc

`internal/plugin/ratelimit.go` 的限流器仅支持基于 IP 的键。
- **计划:** 添加 `WithKeyFunc(func(Session) string)` 选项
- **状态:** backlog

### 11. `sortByteKeys` O(n²) 排序

`internal/infra/store/message_log.go` 中的 `sortByteKeys` 使用冒泡排序。
- **影响:** 仅在 <1 万键时可忽略
- **建议:** 替换为 `sort.Slice`

### 12. `examples/` 无自动测试

12 个 examples 包全部标记 `[no test files]`。
- **建议:** 添加编译检查或简单冒烟测试确保 examples 不退化
- **风险:** 低 — 变更 `cmd/` 或 `api/` 可能导致 examples 编译失败而不被察觉

### 13. cmd/shark-socket/main.go 无测试

主入口文件覆盖率为 0%。
- **建议:** 添加 `TestMain` 级别的集成测试（启动/停止）

### 14. `serveHTTP` 错误处理路径未覆盖

`internal/app/app.go:257` 中的 `serveHTTP` 方法（处理 Health/Metrics HTTP 服务器错误）覆盖率为 0%。
- **建议:** 添加端口冲突测试验证错误传播

### 15. CoAP Observe 部分功能未覆盖

| 函数 | 覆盖率 |
|---|---|
| `handleObserve` | 18.2% |
| `addObserveSeq` | 12.5% |
| `NotifyObservers` | 0% |
| `findSessionByRemote` | 0% |
| `nextMessageID` | 0% |

CoAP Observe (RFC 7641) 是较新的功能，需要更多测试。

### 16. LwM2M `handleUpdate` 和 `handleDiscover` 未测试

`internal/protocol/lwm2m/coap.go` 中的 `handleUpdate` 和 `handleDiscover` 覆盖率为 0%。
- **建议:** 在 `lwm2m_test.go` 或 `coap_integration_test.go` 中添加测试

---

## 六、版本规划缺口

来自 `docs/planning/IMPLEMENTATION-GOALS-20260530.md` 的未实现规划：

### v0.3.x — IoT 协议深度（未开始）

| 规划项 | 当前状态 |
|---|---|
| MQTT 3.1.1/5.0 协议支持 | ⚠️ 适配器存在，E2E 测试依赖外部 Broker |
| 设备标识 / 注册 / 心跳 | ❌ 未实现 |
| 离线检测与会话元数据 | ❌ 未实现 |
| 协议安全建议文档 | ❌ 未更新 |
| 协议一致性边缘测试 | ⚠️ 部分存在（Fuzz） |

### v0.4.x — 可靠性、集群与持久化（部分开始）

| 规划项 | 当前状态 |
|---|---|
| 集群拓扑定义 | ❌ 未实现 |
| 外部消息总线集成 | ⚠️ Cluster 插件存在，未验证 |
| 会话/设备持久化 | ✅ PersistenceV2 + BoltDB 已实现 |
| 背压与过载策略 | ⚠️ TCP Worker Pool 有 PolicyDrop 等 |
| 负载与浸泡测试脚本 | ✅ Benchmark + Stress 已实现 |

### v1.0.0 — 稳定网关契约（未开始）

| 规划项 | 当前状态 |
|---|---|
| 冻结公共 API 兼容性规则 | ❌ 未开始 |
| 文档化支持/不支持协议功能 | ❌ 未完成 |
| 生产部署参考架构 | ⚠️ 部分存在 |
| 迁移指南 | ❌ 未开始 |

---

## 七、已完成的 DevOps/文档改进

| 项目 | 状态 |
|---|---|
| Docker 多阶段构建（40.5MB） | ✅ |
| docker-compose + Mosquitto | ✅ |
| K8s manifests（ConfigMap/PDB/NetworkPolicy/HPA） | ✅ |
| Helm Chart + _helpers.tpl | ✅ |
| CI: golangci-lint + govulncheck | ✅ |
| CI: 跨平台矩阵（Windows + Ubuntu） | ✅ |
| Fuzz 测试（11 个 fuzz） | ✅ |
| Benchmark 套件 + Stress 套件 | ✅ |
| 文档: 9 份 ADR + 架构/安全/部署/排错 | ✅ |

---

## 八、推荐行动路线

### 迭代 A（修复 + 加固）— 预计 2-3 天
1. 审阅并修复 12 项 P2 遗留缺陷
2. CI 中添加 MQTT Broker 服务 + 设置 `SHARK_MQTT_BROKER`
3. CI 中设置覆盖率阈值（70%）
4. 添加 session 方法零覆盖率测试（7 个传输层 × 8 方法）

### 迭代 B（测试覆盖提升）— 预计 2-3 天
5. API 层 Option 构造函数覆盖率提升
6. OTel 适配器基础测试
7. BoltDB Legacy 方法测试
8. CoAP Observe / LwM2M 补充测试

### 迭代 C（功能增强）— 预计 3-5 天
9. RateLimit WithKeyFunc
10. `sortByteKeys` 优化
11. examples 编译检查
12. cmd/main.go 集成测试

### 迭代 D（版本规划）— 预计 5-10 天
13. MQTT 协议深度支持
14. 设备标识/注册/心跳
15. 集群拓扑定义与多节点验证
