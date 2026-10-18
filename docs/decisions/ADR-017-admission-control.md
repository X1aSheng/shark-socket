# ADR-017:在监听层统一连接准入, 并引入单来源配额与握手预算

状态:已采纳

## 背景

对比报告 (`COMPARISON-261018-OSS-PEERS.md`) 第 4.1 节的 A1-A5 是同一类问题:
**每一个传输层都自己实现了一部分准入, 而没有任何一层实现完整**. 逐个看:

| 项 | 现象 |
|----|------|
| A1 | UDP/CoAP 每个新源地址就 `newSession` + `Register`, 无配额; options 里没有 `MaxSessions` |
| A2 | TLS 握手走 `tls.NewListener`, 推迟到首次 `Read`, 只受 `ReadTimeout` (默认 5m) 约束; WS 的 `websocket.Upgrader` 没有 `HandshakeTimeout` (gorilla 默认 0 = 永不超时) |
| A3 | HTTP 传输的 options 只有 `Addr/Handler/Mux/MaxBodyBytes/3 个 timeout/CORS`, 没有 `MaxConnections` / `AcceptRate`, 而 TCP/WS/QUIC/gRPC-Web 都有 |
| A4 | TCP 的 accept 循环对任何错误固定 `time.Sleep(100ms)`, 不分类、不计数、不退避 |
| A5 | 只有全局 `MaxConnections`; `RateLimit` 插件是**每 IP 的消息**限流, 建连阶段不触发 |

它们共同的后果不是"某个传输有洞", 而是"攻击者总有最软的一扇门": 同一个网关里
WS 与 gRPC-Web 有闸门, 纯 HTTP 没有; TCP 有总量上限, UDP 没有; 有总量上限的地方
又都可以被一个 IP 合法占满.

真正需要修的是**准入这件事没有单一实现点**. 只要每个传输各自补一块, 下一次新增
传输时同样的洞会再出现一次 (A3 本身就是这个规律的结果).

## 决策

1. **一个共享配额原语 `shared.PeerQuota`(总量 + 单来源).**
   两个维度在同一个结构里, 同一把分片锁下用 CAS 领取全局名额, 因此"当前打开了
   多少"与"这个来源占了多少"是同一份计数的两种读法, 不会互相漂移, 也不存在
   "归还了总量却漏了单来源"的路径.
2. **单来源的身份是 IP, 不是 `IP:port`.** 归一化在 `shared.PeerIP` 里完成:
   IPv4-mapped IPv6 用 `Unmap` 折叠成 IPv4, zone 保留. 无法归属的空地址只计入
   总量, 不进入任何单来源桶 -- 否则所有不可归属的连接会共用 `maxPeer` 这一份配额,
   互相把对方挤掉.
3. **`shared.Acceptor` 保留三维: 总量 + 单来源 + 速率**, 并把拒绝原因区分为
   `capacity` / `peer` / `rate` 三档返回给调用方. 速率拒绝时归还刚拿到的名额,
   否则每次限速都会泄漏一个配额.
4. **TCP 系 (TCP/WS/QUIC/gRPC-Web/HTTP) 在 `Accept` 之后判定**; HTTP 通过
   `shared.NewGateListener` 包装 listener, 在被拒时回一个真的 `503`.
5. **UDP/CoAP 只有配额, 没有速率.** 数据报没有"建连"这个可计数时机, 对每个
   数据报计数就是对流量限速, 那是插件的职责. 配额归还绑在 `session.Close` 上
   (`closeOnce.Do` 内), 因此主动关闭、TTL 淘汰、`LoadOrStore` 竞态丢弃三条路径
   都恰好归还一次.
6. **accept 错误分三类**: `closed` (`net.ErrClosed`, 直接退出) /
   `resource` (`EMFILE`/`ENFILE`/`ENOMEM`, 额外计数) / `temporary` (其余).
   三类都退避, 日志按连续失败段折叠成一行.
7. **TLS 握手有独立预算** (`TLSHandshakeTimeout`), 0 值回落 `ReadTimeout`;
   握手后 `SetDeadline(time.Time{})` 清零; 失败计入 `tls_handshake_failed_total`.
8. **WS 暴露 `HandshakeTimeout` / `ReadTimeout` / `ReadBufferSize` / `WriteBufferSize`.**

## 原因

- **为什么配额带分片锁, 而不是"带 TTL 的 map + 定期清理".** 报告建议加 TTL 清理,
  但条目在计数归零时立即删除 (归零即消失), 没有常驻条目需要清理: 表里只存在
  真正持有连接的来源, 规模天然被 `maxTotal` 界定. TTL 清理器会引入第二个时间源,
  并在"刚删完又建连"时短暂低估用量, 而收益是零.
- **为什么单来源键要 `Unmap`.** `::ffff:1.2.3.4` 与 `1.2.3.4` 是同一个对端,
  内核在不同 socket 类型上给出的形式不同. 不归一化, 同一台机器可以用 IPv4 与
  IPv4-mapped 各拿一份配额.
- **为什么 HTTP 需要 `GateListener` 而不是 `ConnState`.** 报告建议用
  `http.Server.ConnState` 维护在连数. `ConnState` 是**事后通知**: 回调触发时会话
  已经被接受, 能做的只有立刻关掉, 客户端看到的是连接被重置而不是一个状态码.
  在 listener 上判定才能在被拒时写出 `503`, 让客户端知道"稍后重试"而不是
  "服务器坏了". 顺序必须是 PROXY -> gate -> TLS(ADR-016): gate 在 TLS 之下,
  因为 `net/http` 对 `*tls.Conn` 做类型断言; 又在 PROXY 之上, 因为配额要按真实
  客户端算.
- **为什么拒绝时先 drain 再写 503.** 带着未读数据关闭 socket 会换来一个 RST
  (RFC 1122), 而 RST 会丢掉刚写出的 503. 测试里第一次就是这样拿到
  `ECONNRESET` 而不是 `503`. 于是 `rejectWithServiceUnavailable` 先在 5ms 读
  截止时间内排空最多 8x512 字节, 再写响应, 最后 `CloseWrite()` 半关闭.
  排空必须**有界**: 一个可以无限发送的客户端不该能拖住拒绝路径.
- **为什么 `OnReject` 在 `Reject` 之前调用.** 反过来写的话, 指标在 503 写出之后
  才自增, 客户端可能在计数落地之前就观测到拒绝, 断言"拒绝已被统计"的测试会间歇
  失败 -- 这正是实现时遇到的那次失败.
- **为什么握手用 `SetDeadline` 而不是带超时的 `context`.** `HandshakeContext` 在
  ctx 取消时通过 `context.AfterFunc` 从**另一个 goroutine** 关闭底层连接, 于是
  对端可以早于"计数 -> 日志"看到断开, 账目落在调度上. 实测 20 次里有 6 次
  `tls_handshake_failed_total` 停留在 0. 改用 deadline 后读同步失败, 顺序确定.
  该 deadline 同时是"整个握手"的绝对预算, 而不是空闲超时 -- 逐字节慢速喂养也
  无法延长它, 这正是握手预算该有的语义.
- **为什么 `TLSHandshakeTimeout` 的 0 值不表示"无上限".** 旧实现里握手本来就被
  `ReadTimeout` 覆盖; 新字段默认成无上限是一次藏在特性里的回归. 0 表示"沿用
  旧上界", 想放宽就显式给一个大值.
- **为什么 UDP/CoAP 的归还写在 `Close` 里而不是清扫循环里.** 清扫循环只能看到
  "过期", 看不到"已主动关闭"; 在清扫里归还, 主动关闭的那一份就永远不还.
  写入 `closeOnce.Do` 之后, 三条销毁路径共用一次归还.

## 后果

- **默认行为完全不变.** 所有新字段零值即"不限制", 因此既有部署的行为与计数不变.
  `Acceptor` 在 nil 上也是安全的 (`TryAccept` 视作放行, `Done` 空操作), 因为延迟
  清理路径会在从未领取名额的情况下调用它.
- **内存与来源数同阶, 不与会话数同阶.** 每个持有连接的来源占一个 map 条目,
  上界是 `min(maxTotal, 活跃来源数)`. 没有空闲条目的累积.
- **NAT 后面的客户端共享一份配额.** 这是单来源限流的固有代价: `MaxConnectionsPerIP`
  要按最大的共享出口设定, 而不是按单机设定. 需要更细粒度时应做 IP 段分组配额,
  见"重新评估条件".
- **HTTP 传输第一次有了准入, 也因此第一次有 `sessions_rejected_total`.** 被拒的
  请求拿到 `503` 以及一个已经排空的连接, 而不是被接受后再丢弃.
- **UDP/CoAP 的会话表有了上界**, 因此 "10 万伪造源地址" 只能把表填满到
  `MaxSessions`, 不能把 `SessionManager` 的 100 万兜底顶穿 -- 后者会让正常设备
  一律进不来, 从"内存耗尽"退化成更难恢复的"容量占满".
- **新增指标**: `accept_errors_total`, `accept_resource_errors_total`,
  `tls_handshake_failed_total`, `sessions_rejected_total`(HTTP 与 UDP/CoAP 均为
  该名字). 命名沿用既有惯例, 不按传输加前缀, 便于跨协议汇总.
- **`MaxConnections` 与 `MaxSessions` 是同一件事的两个名字.** TCP 系在 `Accept`
  之后有真实连接, 限制的是并发连接数; UDP/CoAP 没有连接, 限制的是会话条目数.
  语义一致, 名字跟随各自的模型, 见 `TRANSPORT.md` 汇总部分 sec.4.2.
- **WS 的 `HandshakeTimeout` 不是 slowloris 防御.** gorilla 只给 101 响应写设
  截止时间, 升级请求头由 `ReadTimeout` 约束. 这一点写在字段注释里, 并有测试
  (`TestWebSocketHeaderReadBoundedByReadTimeout`) 锁住, 因为把两者混为一谈会
  让人以为设了握手超时就安全了.

## 重新评估条件

- 若出现"同一 NAT 后大量合法设备"的部署, 单 IP 配额会成为障碍. 届时应引入
  **IP 段分组**(按 CIDR 前缀聚合计数), 而不是取消配额: 取消会让 A5 回到原点.
- 若 QUIC 的连接迁移 (connection migration) 被启用, 对端地址会在连接生命周期内
  变化, 届时配额需要在迁移时转移而不是重新领取, 否则迁移会被判成新连接.
- 若网关开始同时承载十个以上传输, 应把准入提到 `Gateway` 层统一执行, 而不是
  继续在传输层的 `Accept` 之后. 现在的放置反映的是"传输层是唯一知道
  `Accept` 时机的一层".

## 关联文档

- 报告: `docs/reports/COMPARISON-261018-OSS-PEERS.md` 第 4.1 节 A1-A5
- 架构: `docs/architecture/TRANSPORT.md` TCP sec.4.3/4.4/4.5, UDP sec.2.2/4,
  WebSocket sec.4, 汇总部分 sec.4.2/4.3
- 相邻决策: ADR-016 (监听层包装顺序: PROXY -> gate -> TLS), ADR-012 (会话读缓冲)
  -- 都对 `net.Conn`/`net.Listener` 做包装, 包装顺序与类型断言是同一类问题
