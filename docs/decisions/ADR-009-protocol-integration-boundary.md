# ADR-009：协议整合边界是统一业务运行时

状态：已采纳

## 背景

市面上已有 nginx、Envoy、Caddy 等成熟通用代理，也有 Californium、Leshan 等 CoAP/LwM2M 实现。Shark-Socket 需要明确自身价值，避免成为重复建设的代理软件。

## 决策

Shark-Socket 的协议整合边界是统一业务服务端运行时，不替代通用反向代理或服务网格。

## 原因

- 统一 Session、Plugin、Metrics、Logger、Tracer 是业务框架价值。
- 通用代理的负载均衡、路由、生态不是本项目优势。
- CoAP/LwM2M 等协议接入应服务于统一业务处理模型。

## 后果

部署时可以与 nginx、Envoy、云 LB 配合使用。Shark-Socket 聚焦应用层连接生命周期、插件和业务处理。

## 重新评估条件

若未来明确要做独立代理产品，需要另起产品目标、配置模型和兼容性测试矩阵。

## 关联文档

- [ARCHITECTURE.md](../ARCHITECTURE.md)
- [TRANSPORT.md](../TRANSPORT.md)
- [MQTT-INTEGRATION.md](../../guides/MQTT-INTEGRATION.md)
