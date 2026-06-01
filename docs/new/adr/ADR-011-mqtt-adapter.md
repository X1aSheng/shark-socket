# ADR-011：MQTT 外部适配器

状态：已采纳

## 背景

Shark-Socket 需要与 MQTT Broker 互通以接入 IoT 设备和管理系统。ADR-008 已决策 MQTT Broker 由外部项目提供，但未定义 Gateway 如何以 MQTT Client 身份连接 Broker。

## 决策

在 `internal/infra/mqtt/` 中创建轻量 MQTT 客户端适配器，通过 `eclipse/paho.mqtt.golang` 连接外部 Broker。

## 设计要点

1. **适配器不是 Broker**：不维护订阅树、不处理 QoS 状态机、不持久化消息
2. **通过 Gateway SessionManager 集成**：MQTT 消息可路由到任意协议 Session
3. **可插拔 Options 模式**：Broker URL、ClientID、TLS 等均可配置
4. **Topic 映射约定**：`shark/{protocol}/{session-id}/{action}` 层级结构

## 原因

- paho 是 Eclipse 维护的成熟 MQTT 客户端库，社区广泛使用
- 适配器模式允许未来切换 MQTT 客户端库而不影响上层
- 与 ADR-008 决策一致（不内建 Broker）

## 后果

- 新增 `github.com/eclipse/paho.mqtt.golang` 依赖
- MQTT 集成测试需要外部 Broker，测试时需设置 `SHARK_MQTT_BROKER`
- Topic 映射为约定而非强制，用户可自定义

## 重新评估条件

在以下情况重新评估是否内建 MQTT Broker：
- 明确需要 Gateway 内建 MQTT endpoint（不只是 MQTT 客户端）
- 接受完整的 MQTT 协议合规测试负担

## 关联文档

- [MQTT-INTEGRATION.md](../../MQTT-INTEGRATION.md)
- [ADR-008-mqtt-external-broker.md](ADR-008-mqtt-external-broker.md)
- [ADR-009-protocol-integration-boundary.md](ADR-009-protocol-integration-boundary.md)
