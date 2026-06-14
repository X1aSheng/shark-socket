# ADR-004：插件 panic 在 PluginRunner 隔离

状态：已采纳

## 背景

插件是扩展点，质量不可完全由框架控制。若 panic 分散在协议层处理，会造成语义不一致和重复代码。

## 决策

插件 panic 由 PluginRunner 统一 recover、记录日志和指标，并根据阶段决定后续行为。

## 原因

- 插件失败策略一致。
- 协议实现保持简洁。
- OnClose 可以尽量执行完整条链。
- 指标 `plugin_panics_total` 可统一统计。

## 后果

PluginRunner 是插件执行的唯一入口。协议不得绕过 PluginRunner 直接调用插件链。

## 重新评估条件

若某插件需要进程级 fail-fast，必须作为显式配置加入 PluginRunner 策略，而不是在插件内部直接终止进程。

## 关联文档

- [PLUGIN.md](../PLUGIN.md)
- [GATEWAY.md](../GATEWAY.md)
