# MQTT Integration

> Shark-Socket ↔ External MQTT Broker 数据契约  
> 版本：v0.1.0

---

## 概述

遵循 [ADR-008](docs/new/adr/ADR-008-mqtt-external-broker.md) 决策，Shark-Socket **不实现完整 MQTT Broker**，而是通过 `internal/infra/mqtt` 适配器连接外部 MQTT Broker（如 Mosquitto、EMQX、VerneMQ）。

## 架构

```
┌──────────────┐      MQTT 3.1.1/5.0      ┌─────────────────┐
│  Shark-Socket │ ◄──────────────────────► │  External Broker  │
│  (MQTT Client) │     publish/subscribe    │  (Mosquitto/EMQX) │
└──────────────┘                           └─────────────────┘
        │                                           │
  Gateway Runtime                              Other MQTT
  Session/Plugin Chain                       Clients/Devices
```

## 适配器能力

| 能力 | 支持 | 说明 |
|------|------|------|
| 连接外部 Broker | ✓ | TCP/TLS/WebSocket 协议 |
| 自动重连 | ✓ | paho 客户端内置 |
| 订阅 Topic | ✓ | 运行时动态订阅 |
| 发布 Topic | ✓ | 同步等待确认 |
| QoS 0/1/2 | ✓ | 由调用方指定 |
| 用户名密码认证 | ✓ | Options 配置 |
| TLS 加密 | ✓ | 通过 `*tls.Config` 配置 |
| Clean Session | ✓ | 默认启用 |

## Topic 命名约定

建议使用分层 topic 结构：

```
shark/{protocol}/{session-id}/{action}
```

| 层级 | 示例 | 说明 |
|------|------|------|
| `shark` | — | 固定前缀，避免冲突 |
| `{protocol}` | `tcp`, `coap`, `ws` | 来源协议 |
| `{session-id}` | `42` | Gateway 分配的 Session ID |
| `{action}` | `incoming`, `outgoing`, `event` | 消息方向或事件类型 |

### 网关消息映射

```
MQTT Topic                        Direction         Gateway Path
────────────────────────────────────────────────────────────────
shark/tcp/+/incoming              Broker → Gateway   Handler(msg)
shark/coap/+/incoming             Broker → Gateway   Handler(msg)
shark/{protocol}/42/outgoing      Gateway → Broker   sess.Send(data)
shark/event/+/connected           Gateway → Broker   Plugin OnAccept
shark/event/+/disconnected        Gateway → Broker   Plugin OnClose
```

## 使用方式

### Go API

```go
import "github.com/X1aSheng/shark-socket/internal/infra/mqtt"

adapter, err := mqtt.NewAdapter(
    mqtt.WithBrokerURL("tcp://localhost:1883"),
    mqtt.WithClientID("shark-gateway"),
    mqtt.WithTopic("shark/+/incoming"),
    mqtt.WithQoS(0),
    mqtt.WithMessageHandler(func(topic string, payload []byte) {
        log.Printf("received: %s => %s", topic, string(payload))
    }),
)
```

### 环境变量

| 变量 | 用途 |
|------|------|
| `SHARK_MQTT_BROKER` | Broker URL（`tcp://host:1883` 或 `ssl://host:8883`） |

### 完整示例

参见 `examples/mqtt-bridge/main.go`。

## 与 shark-MQTT 的关系

[shark-MQTT](https://github.com/X1aSheng/shark-MQTT) 是完整 MQTT Broker 实现。本适配器的定位是：

1. **连接 shark-MQTT**：将 shark-MQTT 作为 Gateway 的外部 MQTT Broker
2. **桥接协议**：通过 MQTT topic 在其他系统与 Gateway Session 之间传递数据
3. **不替代 shark-MQTT**：本适配器不实现 QoS 状态机、Retain、Will、ACL 等 Broker 语义

## Topic 安全建议

- 生产环境使用 TLS 加密（`ssl://` 协议）
- 使用独立 MQTT 用户并限制权限
- 通过 ACL 限制 topic 读写范围
- 敏感数据使用端到端加密（应用层处理，MQTT 层不解析 payload）
