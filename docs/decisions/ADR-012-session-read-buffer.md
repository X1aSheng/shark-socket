# ADR-012:会话持有读缓冲, framer 保留无状态快路径

状态:已采纳

## 背景

TCP 读路径原先在裸 `net.Conn` 上按帧边界读取:

- `LengthPrefixFramer` 每帧两次 `Read`: 4 字节长度头一次, payload 一次
- `LineFramer` 每字节一次 `Read`, 100 字节的行就是 101 次系统调用
- 4 字节长度头通过 `io.ReadFull` 传入, 逃逸到堆, 每帧因此有两次分配

`docs/architecture/TRANSPORT.md` 第 2.1 节当时明确写着"禁止使用 bufio.Reader 跨调用",
理由是预读缓冲跨调用保持状态会导致帧错位. 该规则把"reader 被换掉"与"存在预读"
混为一谈, 代价是把每帧的读次数固定在 2 次以上.

横向对比 (`docs/reports/COMPARISON-261018-OSS-PEERS.md` B1) 显示 gnet 的 ring buffer,
netpoll 的 LinkBuffer, Netty 的池化 ByteBuf 都在这一层做复用, 只有本项目按帧边界直读.

## 决策

1. 缓冲由**会话**持有, 不由 framer 持有. 会话创建时按 `ReadBufferBytes` 建一个
   `bufio.Reader`, `readLoop` 在整个连接生命周期内只用它.
2. `Framer` 接口不变. 内置实现在收到 `*bufio.Reader` 时走快路径
   (`Peek`/`Discard`/`ReadSlice`), 收到其他 `io.Reader` 时按原逻辑工作.
3. 默认 `ReadBufferBytes = 1024`; 传 0 表示直读 conn, 恢复旧行为.
4. `RawFramer` 永不缓冲: 它的语义是"一次 Read 返回多少就给多少", 缓冲会缩小它
   交付的帧. 该例外由 `framerAcceptsBufferedRead` 统一判定, 不散落在调用点.

## 原因

- 帧错位的真实条件是"同一连接的帧之间换了 reader". 会话独占 reader 消除了这个
  条件, 预读本身不再是风险.
- framer 保持无状态意味着它仍然可以被并发用于不同连接, 不引入共享的可变状态.
- 快路径把长度头从未逃逸的缓冲切片里取出, 每帧分配从 2 次降到 1 次; 这是
  零拷贝工作之前能拿到的, 不需要改接口的全部收益.
- 读缓冲的代价可以用一个数字说清: 每连接正好 `ReadBufferBytes` 字节, 与消息速率
  无关. 这让"内存换系统调用"成为调用方可计算的取舍, 而不是隐式行为.

## 后果

- 每连接常驻堆增加 `ReadBufferBytes` 字节 (默认 1 KiB). 1,000 空闲连接的实测对照:
  6,647 B/连接 (关闭) 对 7,888 B/连接 (1 KiB), 差 1,241 B. 该值为 Windows 对照值,
  云端 Linux 基线需重采后才能替换 README 中的 6,219 B/连接.
- `LineFramer` 的快路径必须复制一次: `ReadSlice` 返回的切片只在下一次读之前有效.
  有专门测试 (`TestLineFramerBufferedPayloadDoesNotAliasBuffer`) 守护该复制.
- 错误语义按 `Buffered() > 0` 还原: 帧边界上的 EOF 仍是 `io.EOF`, 半帧仍是
  `io.ErrUnexpectedEOF`, 依赖 `errors.Is` 的调用方不受影响.
- `TRANSPORT.md` 第 2.1 节的禁令被本 ADR 取代, 第 2.6 节记录新规则与实测数据.

## 重新评估条件

若某条传输路径改成"framer 返回池化帧/引用计数帧"(即零拷贝, 见对比报告 B3(b)),
则本 ADR 中"每帧仍有一次 payload 分配"的前提失效, 缓冲策略需与帧所有权一并重新设计.

## 关联文档

- [TRANSPORT.md](../architecture/TRANSPORT.md) 第 2.1 / 2.6 / 3.6 节
- [COMPARISON-261018-OSS-PEERS.md](../reports/COMPARISON-261018-OSS-PEERS.md) B1 / B3
- [ADR-001](ADR-001-session-raw-bytes.md) 核心 Session 保持原始字节
