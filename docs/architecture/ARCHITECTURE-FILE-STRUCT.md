# 架构文档文件结构

> 本文记录 `docs/architecture/` 架构文档拆分结果、职责边界和输出顺序。

---

## 1. 文件列表

```text
docs/
├── ARCHITECTURE.md
├── CONTRACTS.md
├── LIFECYCLE.md
├── ERRORS.md
├── GATEWAY.md
├── TRANSPORT.md
├── PROTOCOL.md
├── PLUGIN.md
├── OBSERVABILITY.md
├── SECURITY.md
├── PERFORMANCE.md
├── CONFIGURATION.md
├── TESTING.md
├── DEPLOYMENT.md
├── ROADMAP.md
└── adr/
    ├── README.md
    ├── ADR-001-session-raw-bytes.md
    ├── ADR-002-gateway-owns-runtime.md
    ├── ADR-003-staged-shutdown.md
    ├── ADR-004-plugin-panic-isolation.md
    ├── ADR-005-codec-adaptation-layer.md
    ├── ADR-006-benchmark-driven-optimization.md
    ├── ADR-007-build-proxy-configurable.md
    ├── ADR-008-mqtt-and-protocol-boundary.md
    ├── ADR-009-coap-as-transport-layer.md
    └── ADR-010-udp-pseudo-session-timing.md
```

---

## 2. 职责边界

| 文件 | 职责 |
| --- | --- |
| `ARCHITECTURE.md` | 总入口：项目定位、设计哲学、非目标、文档导航、当前状态摘要 |
| `LAYERING.md` | 分层、目录结构、依赖矩阵、禁止依赖规则 |
| `CONTRACTS.md` | `internal/core` 接口、类型、不变量和错误体系 |
| `GATEWAY.md` | Gateway、Runtime、SessionManager、PluginRunner、WorkerPool |
| `TRANSPORT.md` | TCP、UDP、HTTP、WebSocket、CoAP、QUIC、gRPC-Web 传输层 |
| `PROTOCOL.md` | LwM2M 等应用协议层 |
| `PLUGIN.md` | 插件执行顺序、控制错误、内置插件、自定义插件 |
| `OBSERVABILITY.md` | 日志、指标、追踪、健康检查 |
| `SECURITY.md` | 安全配置、防御体系、敏感配置保护、部署安全 |
| `PERFORMANCE.md` | 性能目标、benchmark 驱动优化、P2 性能路线 |
| `CONFIGURATION.md` | 配置字段、默认值、环境变量、验证规则 |
| `TESTING.md` | 测试层次、回归清单、fuzz、benchmark、验收命令 |
| `DEPLOYMENT.md` | Docker、Kubernetes、Helm、集群拓扑、资源估算 |
| `ROADMAP.md` | P0/P1/P2/P3 实施路线与验收标准 |
| `adr/` | 长期架构决策记录 |

---

## 3. 内容归属规则

- 总览、入口和导航写入 `ARCHITECTURE.md`。
- 依赖是否合法只看 `LAYERING.md`。
- 核心接口先更新 `CONTRACTS.md`，再改代码。
- Gateway 生命周期问题写入 `GATEWAY.md`。
- TCP/UDP/HTTP/WebSocket/CoAP/QUIC/gRPC-Web 写入 `TRANSPORT.md`。
- LwM2M 和未来应用协议写入 `PROTOCOL.md`。
- 配置字段只写入 `CONFIGURATION.md`。
- 测试命令和回归清单写入 `TESTING.md`。
- 具体验证流水账写入 `PROJECT-REVIEW-*.md` 或 `BENCHMARK-*.md`，不写入主架构文档。
- 影响长期边界的选择必须新增或更新 ADR。
- 命名规范变化写入 `NAMING.md`，并同步修正文档示例。
- 修改任何文件后检查本地 Markdown 相对链接。

---

## 4. 输出顺序

后续若逐个输出文件，按以下顺序推进：

1. `ARCHITECTURE.md`
2. `LAYERING.md`
3. `CONTRACTS.md`
4. `GATEWAY.md`
5. `TRANSPORT.md`
6. `PROTOCOL.md`
7. `PLUGIN.md`
8. `OBSERVABILITY.md`
9. `SECURITY.md`
10. `PERFORMANCE.md`
11. `CONFIGURATION.md`
12. `TESTING.md`
13. `DEPLOYMENT.md`
14. `MQTT-INTEGRATION.md`
15. `NAMING.md`
16. `ROADMAP.md`
17. `adr/README.md`
18. `adr/ADR-001-session-raw-bytes.md`
19. `adr/ADR-002-gateway-owns-runtime.md`
20. `adr/ADR-003-staged-shutdown.md`
21. `adr/ADR-004-plugin-panic-isolation.md`
22. `adr/ADR-005-codec-adaptation-layer.md`
23. `adr/ADR-006-benchmark-driven-optimization.md`
24. `adr/ADR-007-build-proxy-configurable.md`
25. `adr/ADR-008-mqtt-external-broker.md`
26. `adr/ADR-009-protocol-integration-boundary.md`

---

## 5. 完成度检查

每份文件完成后检查：

- 职责是否单一。
- 是否与其他专题文件大段重复。
- 是否引用正确的上游或下游文档。
- 是否包含必要的验收或检查清单。
- 示例命名是否符合 `NAMING.md`。

---
> **状态：历史归档** — 本文档是架构文档拆分过程的规划记录，对应的拆分任务已完成。实际文件清单以磁盘目录为准。

---
> **状态：历史归档** — 本文档是架构文档拆分过程的规划记录，对应的拆分任务已完成。实际文件清单以磁盘目录为准。
