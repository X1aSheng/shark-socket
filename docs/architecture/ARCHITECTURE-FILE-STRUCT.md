# 架构文档文件结构

> 本文记录 `docs/` 下文档的拆分结果与职责边界。文件清单已与磁盘目录对齐。

---

## 1. 文件列表

```text
docs/
+-- architecture/
|   +-- ARCHITECTURE.md                 # 总入口:定位,哲学,分层,依赖矩阵,目录树
|   +-- ARCHITECTURE-FILE-STRUCT.md     # 本文档
|   +-- CONTRACTS.md                    # internal/core 接口,类型,不变量
|   +-- GATEWAY.md                      # Gateway,Runtime,SessionManager,PluginChain,WorkerPool
|   +-- LIFECYCLE.md                    # 连接与进程生命周期,StagedServer 三阶段
|   +-- PLUGIN.md                       # 插件链执行规则,内置插件,自定义插件
|   +-- TRANSPORT.md                    # TCP,UDP,HTTP,WebSocket,CoAP,QUIC,gRPC-Web
|   +-- PROTOCOL.md                     # LwM2M 应用协议层
|   +-- ERRORS.md                       # 失败信号哨兵与判定方式
|   +-- OBSERVABILITY.md                # 日志,指标,追踪,健康检查
|   +-- SECURITY.md                     # TLS/mTLS,防御体系,攻击面缓解
|   +-- DEPLOYMENT.md                   # Docker,K8s,Helm,集群拓扑
|   +-- TROUBLESHOOTING.md              # 诊断工具,常见故障排查
|   +-- OUTPUT-PLAN.md                  # 文档输出计划(历史归档)
+-- guides/
|   +-- Architecture.md                 # 分层架构详解(与 architecture/ 互为补充)
|   +-- CONFIGURATION-20260530.md       # 配置字段参考
|   +-- TEST-STRATEGY-20260529.md       # 测试策略
|   +-- PROTOCOL-TEST-GUIDE-20260530.md # 协议测试指南
|   +-- EXAMPLES.md                     # 示例索引
|   +-- MQTT-INTEGRATION.md             # MQTT 外部 broker 互通
+-- decisions/
|   +-- README.md                       # ADR 索引
|   +-- ADR-001-session-raw-bytes.md
|   +-- ADR-002-gateway-owns-runtime.md
|   +-- ADR-003-staged-shutdown.md
|   +-- ADR-004-plugin-panic-isolation.md
|   +-- ADR-005-codec-adaptation-layer.md
|   +-- ADR-006-benchmark-driven-optimization.md
|   +-- ADR-007-build-proxy-configurable.md
|   +-- ADR-008-mqtt-external-broker.md
|   +-- ADR-009-protocol-integration-boundary.md
|   +-- ADR-011-mqtt-adapter.md
+-- planning/
|   +-- PROJECT-PLAN-20260529.md
|   +-- IMPLEMENTATION-GOALS-20260530.md
+-- reports/
    +-- PROJECT-REVIEW-YYMMDD-HHMMSS.md    # 审查报告
    +-- DEPLOYMENT-VALIDATION-YYMMDD-*.md  # 云端部署验证记录
    +-- BENCHMARK-YYMMDD-*.md              # 基准测试记录
    +-- ARCHITECTURE-ANALYSIS-*.md         # 架构分析
```

---

## 2. 职责边界

| 文件 | 职责 |
| --- | --- |
| `ARCHITECTURE.md` | 总入口:项目定位,设计哲学,非目标,分层图,依赖矩阵,目录结构 |
| `CONTRACTS.md` | `internal/core` 接口,类型,不变量 |
| `GATEWAY.md` | Gateway,Runtime,SessionManager,PluginChain,WorkerPool |
| `LIFECYCLE.md` | 连接生命周期,Gateway 启停,状态机汇总 |
| `ERRORS.md` | 失败信号哨兵列表,判定方式,使用规范 |
| `TRANSPORT.md` | 各传输协议的实现细节 |
| `PROTOCOL.md` | LwM2M 等应用协议层 |
| `PLUGIN.md` | 插件执行顺序,控制错误,内置插件,自定义插件 |
| `OBSERVABILITY.md` | 日志,指标,追踪,健康检查 |
| `SECURITY.md` | 安全配置,防御体系,敏感配置保护,部署安全 |
| `DEPLOYMENT.md` | Docker,Kubernetes,Helm,集群拓扑,资源估算 |
| `TROUBLESHOOTING.md` | 诊断工具,常见故障排查,错误码速查 |
| `guides/Architecture.md` | 分层架构与各层实现的详尽展开 |
| `guides/CONFIGURATION-*.md` | 配置字段,默认值,环境变量,验证规则 |
| `guides/TEST-STRATEGY-*.md` | 测试层次,回归清单,fuzz,benchmark,验收命令 |
| `guides/MQTT-INTEGRATION.md` | MQTT 外部 broker 互通契约 |
| `decisions/` | 长期架构决策记录(ADR) |
| `reports/` | 审查报告,部署验证记录,基准测试记录 |

---

## 3. 内容归属规则

- 总览,入口和导航写入 `architecture/ARCHITECTURE.md`.
- 依赖是否合法只看 `architecture/ARCHITECTURE.md` 与 `guides/Architecture.md`.
- 核心接口先更新 `architecture/CONTRACTS.md`,再改代码.
- Gateway 生命周期问题写入 `architecture/GATEWAY.md` 和 `architecture/LIFECYCLE.md`.
- TCP/UDP/HTTP/WebSocket/CoAP/QUIC/gRPC-Web 写入 `architecture/TRANSPORT.md`.
- LwM2M 和未来应用协议写入 `architecture/PROTOCOL.md`.
- 配置字段只写入 `guides/CONFIGURATION-*.md`.
- 测试命令和回归清单写入 `guides/TEST-STRATEGY-*.md`.
- 具体验证流水账写入 `reports/PROJECT-REVIEW-*.md` 或 `reports/BENCHMARK-*.md`,不写入主架构文档.
- 影响长期边界的选择必须新增或更新 `decisions/ADR-*.md`.

---

## 4. 完成度检查

每份文件完成后检查:

- 职责是否单一.
- 是否与其他专题文件大段重复.
- 是否引用正确的上游或下游文档.
- 是否包含必要的验收或检查清单.
- 文中出现的类型名,函数名,文件路径是否与磁盘上的代码一致.

---

> **状态:历史归档** - 本文档早期版本记录过一次文档拆分规划;该规划已执行完毕,上表为对齐
> 磁盘目录后的实际文件清单.
