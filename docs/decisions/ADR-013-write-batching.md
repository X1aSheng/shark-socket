# ADR-013:写路径批处理可选, 合并写按平台生效

状态:已采纳

## 背景

写路径原先每帧两次 `Write`: `LengthPrefixFramer` 先写 4 字节长度头, 再写 payload;
`LineFramer` 先写 payload, 再写分隔符. 在 `TCP_NODELAY` 的默认设置下这就是两次
系统调用, 也可能是两个 TCP 段.

对比报告 (`COMPARISON-261018-OSS-PEERS.md` B2) 把这一项列为数据面第二项: 读路径
已经用缓冲把每帧 2 次 Read 降到 0.27 次 (ADR-012), 写路径仍是每帧 2 次.

## 决策

1. **合并写按平台生效**: 新增 `framedWrite(w, a, b)`, unix 构建用
   `net.Buffers` (writev) 一次写出相邻的两段, 非 unix 构建保持两次 `Write`.
2. **批处理默认关闭**: `tcp.Options.WriteBufferBytes` 默认 0, 即直写 conn.
   设为 n 后会话持有 `bufio.Writer`, `writeLoop` 先把队列里已排队的帧全部写入
   缓冲, 最后 `Flush` 一次.
3. 写超时从"每帧一次"变为"每批一次", 语义仍是"对端最多占用写路径多久".

## 原因

- 合并写在没有 writev 的平台上会多一次分配却仍是两次写, 因此用构建标签分开,
  而不是无条件调用 `net.Buffers.WriteTo` (它对不支持 writev 的 writer 会退化).
- 批处理与读缓冲不同: 读缓冲的收益与消息速率无关 (每帧都要读), 而写缓冲只对
  **突发**有用. 队列里没有后继帧时 `Flush` 立即发生, 延迟不变; 连接数受限的
  场景下从不突发的连接也要常驻 n 字节, 所以默认关闭, 由调用方按负载决定.
- `bufio.Writer` 在缓冲装不下时会自动冲刷, 因此生产者快于 socket 时不会饿死,
  批处理不会把数据滞留在缓冲里.

## 后果

- 开启后每连接常驻堆增加 `WriteBufferBytes` 字节, 与读缓冲同量级.
- 写超时的粒度从帧变批: 一批的总耗时受同一个 deadline 约束, 而不是每帧各自计时.
- 实测 (开发机 Windows, 3 帧排队): 底层 `Write` 次数 6 -> 1; 数据不丢不乱序
  (有测试校验解码出的每一帧).
- Windows 回环的流水线 echo (`BenchmarkTCPEchoWriteBuffer`, 16 帧突发) 两组无
  可分辨差异 (677-936 us 对 724-855 us, 调度主导), 且 writev 在 Windows 上不生效.
  因此本项的收益**尚未被端到端数字证明**, 需要在 Linux 云主机上用 `strace -c`
  或阶梯压测复核后才能写进 README.

## 重新评估条件

若流水线场景的云主机数据表明批处理没有收益, 应删除 `WriteBufferBytes` 而不是
保留一个无人启用的开关; 若 writev 在 unix 上被证明有效但合并写仍无明显收益,
也应退回两次写.

## 关联文档

- [ADR-012](ADR-012-session-read-buffer.md) 会话持有读缓冲
- [TRANSPORT.md](../architecture/TRANSPORT.md) 第 2.7 / 3.6 节
- [COMPARISON-261018-OSS-PEERS.md](../reports/COMPARISON-261018-OSS-PEERS.md) B2
