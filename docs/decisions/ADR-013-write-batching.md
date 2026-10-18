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
- **机制已端到端证明** (`internal/transport/tcp/writebuf_e2e_test.go`): 之前
  的验证是会话级 -- 帧在 `writeLoop` 启动前就排好队, 批处理"必然"生效, 证明不了
  真实负载下队列里是否真的会有第二帧. 现在由一个真实客户端经真实 socket 连发 64 帧,
  handler 收齐后一口气回发, 再由注入的 `Framer` 数"每次进入时缓冲是否为空"
  (= 批次边界): **64 帧 -> 1 批**, 数据逐帧校验无丢失/截断. 同一用例的无缓冲对照臂
  看到的是裸 conn, 每帧各自一条写路径 -- 这也说明若批处理哪天不再触发, 该用例会失败.
- **墙钟收益已在 Linux 上测出** (阿里云 2C/4G, Ubuntu 26.04, go1.26.9, 两台机器
  各跑一遍, `scripts/probe-write-batching.sh`, 2000 轮 x 16 帧流水线 echo):

  | WriteBufferBytes | writev/帧 | write/帧 | ns/op | 吞吐 |
  | --- | --- | --- | --- | --- |
  | 0 (默认) | 2.001 | 0.000 | 83.8k-89.9k | 45.5-48.9 MB/s |
  | 4096 | 1.000 | 0.407 / 0.552 | 66.9k-68.5k | 59.8-61.2 MB/s |

  吞吐 **1.24x (srv2) / 1.28x (srv3)**, 两侧各 3 轮区间无重叠. `writev/帧` 是双向
  计数 (客户端+服务端各一次), 所以 2.001 -> 1.000 表示服务端真的从"逐帧"改成了
  "逐批"; `write/帧` 0.41-0.55 即平均 1.8-2.4 帧一批 -- echo handler 逐帧回写,
  队列里同时只有两三帧, 不是理论上的 16 (64 帧一次性回发由
  `TestWriteBatchingEngagesUnderRealIO` 覆盖: 64 帧 -> 1 批).
- **Windows 上测不出差异的原因已查清**: 那里 `framedWrite` 回退成两次 `Write`,
  未开缓冲的路径本身就是逐帧的, 而批处理的收益只在 writev 把单帧压成一次调用之后
  才出现 -- 此前比较的是两条同样的"逐帧"路径, 属于测错对象.
- **验收口径因此要改**: 报告 B2 原本写"写 syscall 从 2/帧 降到 1/帧", 那是非 unix
  的形态. unix 的真实口径是"1 次 syscall/帧 -> 1 次/批", 实测落到 0.41-0.55 次/帧.
- **默认仍关闭, 但有了明确推荐值**: 收益只对突发成立; 请求-响应型负载里队列始终只有
  一帧, `Flush` 立即发生, 开关只多占 4 KiB/连接. 广播/批量下发类负载应显式设
  `WriteBufferBytes: 4096`.

## 重新评估条件

若流水线场景的云主机数据表明批处理没有收益, 应删除 `WriteBufferBytes` 而不是
保留一个无人启用的开关; 若 writev 在 unix 上被证明有效但合并写仍无明显收益,
也应退回两次写. 判定数据由 `scripts/probe-write-batching.sh` 产生; 若它在 Linux 上
也显示批处理不触发 (writev/帧 没有从 2 降到 1, 或 write/帧 未降到约 1/16),
应当直接删掉开关.

另: 若将来把 `bufio.Writer` 换成自管的切片缓冲, "批次边界"的观测手段需要同步换 --
现在的度量依赖 `bufio.Writer.Buffered()`.

## 关联文档

- [ADR-012](ADR-012-session-read-buffer.md) 会话持有读缓冲
- [TRANSPORT.md](../architecture/TRANSPORT.md) 第 2.7 / 3.6 节
- [COMPARISON-261018-OSS-PEERS.md](../reports/COMPARISON-261018-OSS-PEERS.md) B2
