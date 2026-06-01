# Shark-Socket ADR 索引

> ADR 是 Architecture Decision Record，用于记录长期有效的架构决策。

---

## 编写规范

每个 ADR 使用固定结构：

```text
# ADR-NNN：标题

状态：已采纳 / 已废弃 / 替代 ADR-XXX

## 背景
## 决策
## 原因
## 后果
## 重新评估条件
```

---

## 决策列表

| 编号 | 标题 | 状态 | 摘要 |
| --- | --- | --- | --- |
| ADR-001 | 核心 Session 保持原始字节 | 已采纳 | `Session.Send` 只接收 `[]byte` |
| ADR-002 | Gateway 拥有共享 Runtime | 已采纳 | Gateway 创建或接收 Runtime 并注入协议 |
| ADR-003 | 分阶段优雅关闭 | 已采纳 | StopAccept、Drain、CloseSessions、CloseAll |
| ADR-004 | 插件 panic 隔离 | 已采纳 | PluginRunner 统一 recover |
| ADR-005 | Codec 适配层 | 已采纳 | 类型化消息通过 Codec 和 AdaptTyped 实现 |
| ADR-006 | benchmark 驱动优化 | 已采纳 | BufferPool、分片锁、时间轮由证据驱动 |
| ADR-007 | 构建代理可配置 | 已采纳 | Docker 构建通过 `GOPROXY` build arg 配置 |
| ADR-008 | MQTT 外部 Broker | 已采纳 | MQTT 由 shark-MQTT 独立提供 |
| ADR-009 | 协议整合边界 | 已采纳 | 统一业务运行时，不替代通用代理 |
| ADR-011 | MQTT 外部适配器 | 已采纳 | 通过 paho 客户端连接外部 MQTT Broker |

---

## 使用方式

- 修改核心接口前先查 ADR-001、ADR-005。
- 修改 Gateway 或协议生命周期前先查 ADR-002、ADR-003。
- 修改插件执行语义前先查 ADR-004。
- 引入性能复杂度前先查 ADR-006。
- 修改部署构建策略前先查 ADR-007。
- 调整 MQTT 或协议整合边界前先查 ADR-008、ADR-009、ADR-011。
