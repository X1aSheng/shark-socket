# ADR-008：MQTT 由外部 Broker 提供

状态：已采纳

## 背景

MQTT 3.1.1/5.0 是完整应用协议，包含 QoS、retain、will、session expiry、subscription、ACL 等复杂语义。Shark-Socket 的目标是多协议业务运行时，不是完整 MQTT Broker。

## 决策

MQTT 由 shark-MQTT 作为独立 Broker 提供。Shark-Socket 通过数据契约与其互通。

## 原因

- 避免在一个仓库中维护两个复杂产品边界。
- MQTT 合规测试归属更清晰。
- 两个系统可独立部署、扩容和演进。

## 后果

Shark-Socket 不解析 MQTT packet，不维护 MQTT ClientID 会话，不实现 QoS 状态机。

## 重新评估条件

只有在明确选择内建 MQTT Broker，并接受协议合规、存储和运维复杂度后，才重新评估。

## 关联文档

- [MQTT-INTEGRATION.md](../../guides/MQTT-INTEGRATION.md)
- [ADR-009-protocol-integration-boundary.md](ADR-009-protocol-integration-boundary.md)
