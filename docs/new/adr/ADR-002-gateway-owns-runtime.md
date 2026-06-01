# ADR-002：Gateway 拥有共享 Runtime

状态：已采纳

## 背景

多协议框架需要共享 SessionManager、PluginRunner、Logger、Metrics、Tracer。若各协议自行创建，会导致统计不一致、关闭顺序混乱和测试困难。

## 决策

Gateway 创建或接收 Runtime，并通过 `UseRuntime` 注入协议服务。

## 原因

- 运行时所有权显式。
- 跨协议统计和广播一致。
- 测试可以替换 Runtime 组件。
- 协议代码专注网络生命周期。

## 后果

协议实现必须依赖 `core.Runtime` 接口，不能导入 `runtime` 具体包。

## 重新评估条件

若出现必须独立运行且不需要 Gateway 编排的协议模式，可评估独立 Runtime 工厂，但不得破坏 Gateway 注入路径。

## 关联文档

- [GATEWAY.md](../GATEWAY.md)
- [LAYERING.md](../LAYERING.md)
