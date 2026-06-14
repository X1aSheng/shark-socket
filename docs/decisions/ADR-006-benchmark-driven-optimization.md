# ADR-006：复杂优化由 benchmark 驱动

状态：已采纳

## 背景

BufferPool、时间轮、分片锁、LRU 等结构能提升性能，也会增加实现复杂度和并发风险。

## 决策

复杂性能优化必须先建立 benchmark 基线和 pprof 证据，再按热点引入。

## 原因

- 避免过早优化。
- 保持 P0 架构正确性。
- 优化收益可量化。
- 并发结构引入后必须能被测试证明安全。

## 后果

P0 允许使用更简单实现。P2 根据实测引入六级 BufferPool、分片 SessionManager、时间轮和日志采样器。

## 重新评估条件

若生产压测或 benchmark 显示目标无法达成，按 PERFORMANCE 文档更新优化优先级。

## 关联文档

- [PERFORMANCE.md](../PERFORMANCE.md)
- [TESTING.md](../TESTING.md)
