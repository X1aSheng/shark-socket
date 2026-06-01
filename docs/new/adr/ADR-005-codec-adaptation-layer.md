# ADR-005：类型化消息通过 Codec 适配层实现

状态：已采纳

## 背景

业务需要类型安全，但核心运行时需要跨协议统一。直接让核心 Message 泛型化会扩大 API 面并增加插件复杂度。

## 决策

在 core 定义 `Codec[M]` 和 `TypedHandler[M]`，通过 `AdaptTyped[M]` 把业务类型适配到原始字节 Handler。

## 原因

- 保持核心 Message 简单。
- 业务可以选择 JSON、Protobuf、MessagePack 等编码。
- 类型化能力不影响插件和 Gateway。

## 后果

业务处理链多一次 encode/decode 边界。性能敏感场景可以直接使用原始 `[]byte` Handler。

## 重新评估条件

若 Codec 适配成为明确性能瓶颈，可通过 benchmark 评估零拷贝 codec 或协议专属 adapter。

## 关联文档

- [CONTRACTS.md](../CONTRACTS.md)
- [PERFORMANCE.md](../PERFORMANCE.md)
