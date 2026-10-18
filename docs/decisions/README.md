# Shark-Socket ADR 索引

> ADR 是 Architecture Decision Record,用于记录长期有效的架构决策.

---

## 编写规范

每个 ADR 使用固定结构:

```text
# ADR-NNN:标题

状态:已采纳 / 已废弃 / 替代 ADR-XXX

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
| [ADR-001](ADR-001-session-raw-bytes.md) | 核心 Session 保持原始字节 | 已采纳 | `Session.Send` 只接收 `[]byte` |
| [ADR-002](ADR-002-gateway-owns-runtime.md) | Gateway 拥有共享 Runtime | 已采纳 | Gateway 创建或接收 Runtime 并注入协议 |
| [ADR-003](ADR-003-staged-shutdown.md) | 分阶段优雅关闭 | 已采纳 | StopAccept,Drain,CloseSessions,CloseAll |
| [ADR-004](ADR-004-plugin-panic-isolation.md) | 插件 panic 隔离 | 已采纳 | PluginRunner 统一 recover |
| [ADR-005](ADR-005-codec-adaptation-layer.md) | Codec 适配层 | 已采纳 | 类型化消息通过 Codec 和 AdaptTyped 实现 |
| [ADR-006](ADR-006-benchmark-driven-optimization.md) | benchmark 驱动优化 | 已采纳 | BufferPool,分片锁,时间轮由证据驱动 |
| [ADR-007](ADR-007-build-proxy-configurable.md) | 构建代理可配置 | 已采纳 | Docker 构建通过 `GOPROXY` build arg 配置 |
| [ADR-008](ADR-008-mqtt-external-broker.md) | MQTT 外部 Broker | 已采纳 | MQTT 由 shark-MQTT 独立提供 |
| [ADR-009](ADR-009-protocol-integration-boundary.md) | 协议整合边界 | 已采纳 | 统一业务运行时,不替代通用代理 |
| [ADR-011](ADR-011-mqtt-adapter.md) | MQTT 外部适配器 | 已采纳 | 通过 paho 客户端连接外部 MQTT Broker |
| [ADR-012](ADR-012-session-read-buffer.md) | 会话持有读缓冲 | 已采纳 | 缓冲归会话, framer 保留无状态快路径, RawFramer 不缓冲 |
| [ADR-013](ADR-013-write-batching.md) | 写路径批处理可选 | 已采纳 | 写缓冲默认关闭, 合并写按平台生效 (unix writev) |
| [ADR-014](ADR-014-shared-broadcast-frame.md) | 广播使用共享帧 | 已采纳 | 引用计数的 SharedFrame + 可选 SharedSender 接口 |
| [ADR-015](ADR-015-remove-shared-hot-path-overhead.md) | 消除共享热路径开销 | 已采纳 | 对端键零分配, 插件链无锁读 + 链级 recover, 会话表 64 分片 |
| [ADR-016](ADR-016-proxy-protocol.md) | 监听层支持 PROXY protocol | 已采纳 | listener 包装在 TLS 之下读头, 缺头拒绝而非放行 |
| [ADR-017](ADR-017-admission-control.md) | 统一准入与单来源配额 | 已采纳 | `shared.PeerQuota` 一个计数两维读法, HTTP 走 GateListener 回真 503, 握手独立预算 |
| [ADR-018](ADR-018-coap-blockwise.md) | CoAP 分块下发 | 已采纳 | Block2 按请求重建响应体 (不缓存), 块大小只向下协商, Block1 回 4.13 |
| [ADR-019](ADR-019-config-hot-reload.md) | 配置热重载子集 | 已采纳 | SIGHUP 只换日志级别/黑名单/限流参数, 含监听变更则整份拒绝 |
| [ADR-020](ADR-020-metric-truthfulness.md) | 指标面真实化 | 已采纳 | handler 边界记直方图 + protocol label, 结构体序列键零分配, 真 histogram 导出 |

编号从 001 直接到 011, 缺 010。仓库中从未存在 `ADR-010` 文件 (查过全部提交历史),
V9 审查 H12 曾记录某处引用了这个不存在的编号, 现已无残留引用。新 ADR 顺延 012 起编。

---

## 使用方式

- 修改核心接口前先查 ADR-001,ADR-005.
- 修改 Gateway 或协议生命周期前先查 ADR-002,ADR-003.
- 修改插件执行语义前先查 ADR-004.
- 引入性能复杂度前先查 ADR-006.
- 修改部署构建策略前先查 ADR-007.
- 调整 MQTT 或协议整合边界前先查 ADR-008,ADR-009,ADR-011.
- 修改 TCP 读路径或帧解码的内存策略前先查 ADR-012.
- 修改 TCP 写路径或写批处理策略前先查 ADR-013.
- 修改广播或会话发送的所有权语义前先查 ADR-001,ADR-014.
- 修改会话表, 插件链读取或对端键的构造方式前先查 ADR-015.
- 修改监听层包装, PROXY protocol 或 `RemoteAddr` 语义前先查 ADR-016.
- 修改连接准入, 单来源配额, accept 错误处理或握手超时前先查 ADR-017.
- 修改 CoAP 分块传输, 或决定是否实现 Block1 前先查 ADR-018.
- 修改配置热重载范围, 或调整 SIGHUP 语义前先查 ADR-019.
- 修改指标名/label/bucket 梯度, 或在热路径上加观测前先查 ADR-020.
