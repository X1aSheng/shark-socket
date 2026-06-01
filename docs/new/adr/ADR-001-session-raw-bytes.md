# ADR-001：核心 Session 保持原始字节

状态：已采纳

## 背景

Gateway 需要统一管理不同协议的会话。若核心 Session 引入泛型，运行时层会被业务消息类型污染，插件和跨协议管理也会复杂化。

## 决策

核心 `Session.Send` 只接收 `[]byte`。业务类型安全通过 `Codec[M]` 和 `AdaptTyped[M]` 在 Handler 层实现。

## 原因

- 混合协议 SessionManager 更简单。
- 插件天然处理原始 payload。
- 类型化需求不进入运行时所有权边界。

## 后果

业务需要显式配置 Codec。核心 API 更稳定，Gateway 和插件无需感知业务类型。

## 重新评估条件

若至少两个真实业务协议证明 `[]byte` 核心 Session 无法满足类型安全和可维护性需求，在 v0.1.0 API 稳定前重新评估。

## 关联文档

- [CONTRACTS.md](../CONTRACTS.md)
- [ADR-005-codec-adaptation-layer.md](ADR-005-codec-adaptation-layer.md)
