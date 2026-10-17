# docs/architecture/ 索引

## meta-info

- 用途: 本目录导航。目录下 12 份专题文档此前互不引用也无索引, 只能靠文件名猜入口
- 最近整理: 2026-10-17
- 上一级: [`docs/README.md`](../README.md)
- 文档规范: 全文仅使用基本 ASCII 字符与中文

## 1. 文件清单

| 文件 | 职责 | 行数 |
| --- | --- | --- |
| [`ARCHITECTURE.md`](ARCHITECTURE.md) | **总入口**: 项目定位, 设计哲学, 非目标, 分层图, 依赖矩阵, 目录树 | 362 |
| [`CONTRACTS.md`](CONTRACTS.md) | `internal/core` 接口, 类型, 不变量 | 746 |
| [`GATEWAY.md`](GATEWAY.md) | Gateway, Runtime, SessionManager, PluginChain, WorkerPool | 1044 |
| [`LIFECYCLE.md`](LIFECYCLE.md) | 连接生命周期, Gateway 启停, StagedServer 三阶段, 状态机汇总 | 1065 |
| [`TRANSPORT.md`](TRANSPORT.md) | TCP, UDP, HTTP, WebSocket, CoAP, QUIC, gRPC-Web 的实现细节 | 4206 |
| [`PROTOCOL.md`](PROTOCOL.md) | LwM2M 等应用协议层 | 1440 |
| [`PLUGIN.md`](PLUGIN.md) | 插件链执行规则, 内置插件, 自定义插件 | 1508 |
| [`ERRORS.md`](ERRORS.md) | 失败信号哨兵列表, 判定方式, 使用规范 | 365 |
| [`OBSERVABILITY.md`](OBSERVABILITY.md) | 日志, 指标, 追踪, 健康检查 | 311 |
| [`SECURITY.md`](SECURITY.md) | TLS/mTLS, 六层防御体系, 攻击面缓解 | 484 |
| [`DEPLOYMENT.md`](DEPLOYMENT.md) | Docker, Kubernetes, Helm, 集群拓扑, 资源估算 | 374 |
| [`TROUBLESHOOTING.md`](TROUBLESHOOTING.md) | 诊断工具, 常见故障排查, 错误码速查 | 417 |
| [`OUTPUT-PLAN.md`](OUTPUT-PLAN.md) | 已归档 stub, 原文档在 [`../archive/OUTPUT-PLAN.md`](../archive/OUTPUT-PLAN.md) | - |

行数随编辑会变, 这里只是量级参考。

## 2. 建议阅读顺序

1. `ARCHITECTURE.md` - 先建立分层与依赖的整体印象。
2. `CONTRACTS.md` - 再看核心接口的形状, 后续文档里的类型名都出自这里。
3. `GATEWAY.md` + `LIFECYCLE.md` - 运行时的组装与启停路径。
4. `TRANSPORT.md` / `PROTOCOL.md` - 按需查具体协议, 不必通读。
5. `PLUGIN.md`, `ERRORS.md` - 扩展点与失败信号。
6. `OBSERVABILITY.md`, `SECURITY.md` - 上线前必读。
7. `DEPLOYMENT.md`, `TROUBLESHOOTING.md` - 部署与排障时查。

`TRANSPORT.md` 标题带 "Part 1/5" 且已 4206 行, 是当前最大的一份; 找某一协议时直接用
标题锚点跳转, 不要顺序翻。

## 3. 容易找错位置的内容

| 想找什么 | 在哪 |
| --- | --- |
| Prometheus 指标清单 | [`OBSERVABILITY.md`](OBSERVABILITY.md) 3.3 节 (不是 `guides/Architecture.md`, 那份已归档) |
| 配置字段 | [`../guides/CONFIGURATION-20260530.md`](../guides/CONFIGURATION-20260530.md) |
| 回归清单与验收命令 | [`../guides/TEST-STRATEGY-20260529.md`](../guides/TEST-STRATEGY-20260529.md) |
| 某次验证跑出来什么 | [`../reports/README.md`](../reports/README.md) |
| 为什么这么设计 | [`../decisions/README.md`](../decisions/README.md) |
