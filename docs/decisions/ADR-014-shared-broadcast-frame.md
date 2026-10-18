# ADR-014:广播使用引用计数的共享帧

状态:已采纳

## 背景

`Session.Send` 按契约复制 payload (ADR-001 的后续约束): 调用方在 `Send` 返回后
即可释放自己的缓冲. 对单播这是必要的, 对广播则是 N 次复制同一份字节 --
1 KiB payload 广播到 1,000 个会话就是 2 MB 分配和 2,000 次分配调用.

对比报告 (`COMPARISON-261018-OSS-PEERS.md` B4) 把这一项列为数据面第三项.
Netty 用 `ByteBuf` 引用计数天然支持一对多; 本项目此前没有等价机制.

## 决策

1. `core` 增加 `SharedFrame`: 不可变 payload + 引用计数,
   `NewSharedFrame(data, refs)` 只复制一次.
2. `core` 增加**可选**接口 `SharedSender`. 可选是因为并非所有传输都需要:
   UDP 与 WebSocket 的 `Send` 是同步写, 本来就不复制; 只有排队型传输 (TCP)
   为所有权付了复制的代价.
3. `SessionManager.Broadcast` 按能力分派: 能共享的会话共用一个帧,
   其余走普通 `Send`. 只有一个共享者时不建帧 (不值得引入引用计数).
4. **引用所有权**: `SendShared` 成功转移一份引用, 失败不转移, 由调用方释放.

## 原因

- 不改动 `Send` 契约 (ADR-001 未受影响): 单播调用方的所有权保证不变,
  只有广播这条已知"同一份字节发给多人"的路径换一种做法.
- 可选接口让 8 种传输不必一起改造: 同步写的传输天然没有这个问题, 强行实现
  只是增加一个空壳方法.
- 失败不转移引用, 是为了让"部分会话拒绝"这一常见情形不产生泄漏: 广播侧在错误
  分支释放, 会话侧不再重复释放, 责任唯一.

## 后果

- TCP 的写队列元素从 `[]byte` 变为 `pendingWrite{payload, frame}`, 每个队列槽
  多 8 字节 (128 槽约 +1 KiB/连接). 这是共享帧的代价, 需要在云阶梯上复核.
- 三条释放路径都必须存在, 缺一条就会把 payload 钉住到进程结束: 写出后释放,
  关闭时释放队列中未写的帧, `SendShared` 失败时由广播侧释放. 三种都有测试.
- 实测 (`BenchmarkBroadcastFanOut`, 1 KiB payload, 开发机 Windows):

| 收件人 | 共享帧 | 逐个复制 |
| --- | --- | --- |
| 100 | 7,264 B/op, 9 allocs, ~4-6 us | 215,895 B/op, 208 allocs, ~50-56 us |
| 1,000 | 52,576 B/op, 12 allocs, ~33-35 us | 2,132,297 B/op, 2,011 allocs, ~1.0-1.1 ms |

  共享帧的剩余 O(N) 部分是 `Snapshot` 与分派切片的扩容, 每收件人 8 字节级别,
  不是 payload 复制. 若要进一步下降, 需要复用这两个切片, 那是另一个改动.

## 重新评估条件

若 `Snapshot` 与分派切片被改为复用的 (广播不再分配 O(N) 的指针), 或若写队列
改为池化帧, 本节的所有权规则需要重新表述.

## 关联文档

- [ADR-001](ADR-001-session-raw-bytes.md) 核心 Session 保持原始字节
- [CONTRACTS.md](../architecture/CONTRACTS.md) 第 5.4.1 节
- [COMPARISON-261018-OSS-PEERS.md](../reports/COMPARISON-261018-OSS-PEERS.md) B4
