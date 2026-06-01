# ADR-003：采用分阶段优雅关闭

状态：已采纳

## 背景

直接 `Stop` 容易混淆 listener、读写 goroutine、Session 和后台组件的释放顺序，导致漏连接、重复 OnClose 或关闭超时难以定位。

## 决策

长连接协议实现 `StagedServer`，Gateway 按 StopAccept、Drain、CloseSessions、Stop、CloseAll 阶段关闭。

## 原因

- 每个阶段语义明确。
- 超时和错误可定位。
- 对 TCP、WebSocket、QUIC 等长连接协议更安全。
- 支持 Gateway Start → Stop → Start 回归测试。

## 后果

协议实现需要维护更清晰的生命周期状态。短连接协议可以只实现基础 `Stop`。

## 重新评估条件

若某协议无法拆分阶段，必须在 TRANSPORT 文档中说明原因和替代关闭保证。

## 关联文档

- [GATEWAY.md](../GATEWAY.md)
- [TRANSPORT.md](../TRANSPORT.md)
