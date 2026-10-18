# ADR-016:在监听层选择性支持 PROXY protocol v1/v2

状态:已采纳

## 背景

对比报告 (`COMPARISON-261018-OSS-PEERS.md`) 的 C4 项:

> 全仓无相关解析; `RemoteAddr` 直接取 `conn.RemoteAddr()`.
> 部署在 L4 LB/NLB 后, `RemoteAddr` 恒为 LB 地址 -> 黑名单, RateLimit (按 IP),
> 审计全部失效. 这是**上生产才暴露**的问题.

具体地说, 本仓库里所有按对端地址做判断的东西都会在那一刻同时失效: 插件
`Blacklist` 与 `AutoBan` 的封禁目标, `RateLimit` 的 `remoteKey`
(`plugin/ratelimit.go`), 审计日志里的来源, 以及任何以 `sess.RemoteAddr()` 做
去重或统计的调用方. 它们不会报错, 只会安静地把所有流量算到同一个地址上.

这是传输层**外部形态**的问题: 单机监听时协议本身没有错, 一旦前面有 L4 负载
均衡器, 连接携带的就不再是客户端的身份. 而 TCP 协议没有任何字段可以补救这件事
-- 所以行业做法是由均衡器在 payload 之前插入一小段明文头, 即 PROXY protocol.

## 决策

1. **在 listener 层解决, 不碰 `Framer` 与 `Session`.** 新增
   `shared.NewProxyListener(ln, cfg)`, 在 `Accept` 返回之前读掉头部, 并把
   `RemoteAddr` 换成头里声明的地址. `Framer` 看到的是干净的 payload 流,
   `Session` 一行不改.
2. **头在 TLS 之下.** 包装顺序固定为 `shared.Listen` -> `NewProxyListener` ->
   `tls.NewListener`. 均衡器写的是明文, 加密记录无法先于握手.
3. **四种模式, 零值即关闭.** `Off` (默认) / `V1` / `V2` / `Auto` (按前缀识别).
   `Auto` 只表示版本二选一, **不表示头可选**.
4. **缺少或错误的头一律拒绝连接**, 而不是放行. 拒绝的连接被关闭并计数
   (`proxy_header_rejected_total`), `Accept` 内部继续接受下一个.
5. **头部等待有上限**, 默认 `shared.DefaultProxyHeaderTimeout` (5s), 由
   `WithProxyHeaderTimeout` 覆盖; 没有"永久等待"这一档.

## 原因

- **为什么不在 `handleConn` 里解析.** 报告的建议是"在 `handleConn` 首帧前解析",
  那只覆盖 TCP. HTTP, WebSocket, gRPC-Web 的 accept 循环在 `net/http` 手里,
  同样的逻辑要在三处重新实现一遍, 且都很容易在读缓冲与 `Serve` 之间丢字节.
  listener 包装只有一份实现, 四路共用, 而 `net/http` 恰好也是从 `Accept` 拿连接.
- **为什么不把头做成可选.** "有头就认, 没头就用 socket 地址"看起来更兼容, 但它
  把身份归属的选择权交给了客户端: 任何人直连该端口并省略头部, 就可以用真实
  地址(或伪造地址)顶替. 一个安全相关的开关不能有"由对端决定"的中间态.
- **为什么拒绝时不向 `Accept` 返回错误.** `net/http.Server.Serve` 收到非
  `net.ErrClosed` 的 Accept 错误会**停止整个服务**; TCP 的 acceptLoop 会走退避.
  两种都不是我们想要的 -- 一个坏客户端不该影响其他连接. 因此 `Accept` 内部
  循环丢弃, 错误通过 `OnReject` 回调交给调用方记录.
- **为什么限制 v2 的 65535 字节长度.** v2 规范允许头部带 TLV 且长度字段是 16 位.
  按对端声明分配 64 KiB 是白送的放大攻击. 上限取 2048, 实际均衡器的头部
  远小于此 (UNIX family 的定长部分才 216 字节). 超过上限直接关闭.
- **为什么 v1 用 `ReadSlice` 而不是 `ReadString`.** `ReadString` 会一直追加直到
  遇到分隔符, 对端只要不换行就能让分配无限增长. `ReadSlice` 受缓冲区限制,
  返回 `ErrBufferFull`, 天然带上限.
- **为什么 `proxyConn` 要实现 `CloseWrite`.** 半关闭(发 FIN 但继续读)是
  请求-响应型协议的常见收尾方式. 若包装类型不转发 `CloseWrite`, 调用方的类型
  断言失败后会退化成整条 `Close`, 把对端还在路上的响应截断. 这类问题不会在
  功能测试里出现, 只在第一次遇到慢对端时出现.

## 后果

- **默认行为完全不变.** `ProxyConfig` 零值下 `NewProxyListener` 原样返回 listener,
  `RemoteAddr` 仍是 socket 对端; 有一个测试
  (`TestServerWithoutProxyProtocolKeepsSocketPeer`) 锁住这一点.
- **开启后每连接多一次 `bufio` 包装.** 头部读完时该缓冲里通常还剩 payload 的前
  若干字节, 之后读取从缓冲走, 排空后直连底层 conn, 不再有额外拷贝. 该缓冲固定
  256 字节, 与连接数同阶.
- **信任模型是网络拓扑, 不是配置.** 本 ADR 不提供来源 CIDR 白名单. 正确用法是让
  该端口只对均衡器可达 (常见做法: 单独端口 + 私网地址 + 安全组). 报告把这一项
  列为"上生产才暴露", 而 CIDR 白名单本身是部署手段, 不是协议问题.
- **`RemoteAddr` 的语义变了, 文档必须跟着改.** 开启后 `sess.RemoteAddr()` 返回
  头里声明的地址, `sess.LocalAddr()` 仍是本端 socket 地址; 两者不再来自同一个
  socket. 依赖"`RemoteAddr` 一定可达"的代码(例如主动反连)在 LB 后不成立.
- **v2 的 `LOCAL` 与 v1 的 `UNKNOWN` 不提供地址.** 它们表示"这条连接是均衡器
  自己发起的"(健康检查), 此时保留 socket 对端, 不伪造一个地址.
- **未覆盖的传输**: UDP 与 CoAP 无连接, PROXY protocol 的用途不同 (每个数据报
  独立), 本轮不做. QUIC 也不做 -- 它有自己的连接 ID, 且 L4 均衡器对 QUIC 的
  处理方式与 TCP 不同.

## 重新评估条件

- 若实际部署中出现"同一个端口既要给均衡器用又要给直连客户端用", 那么"头必须
  存在"这条会变成障碍. 届时应引入**按来源地址的白名单**(只对白名单内的对端要求
  头部), 而不是允许头可选.
- 若均衡器开始发送带 TLV 的 v2 头部且超过 2048 字节, 调整上限即可, 不需要改结构.

## 关联文档

- 报告: `docs/reports/COMPARISON-261018-OSS-PEERS.md` 第 4.3 节 C4
- 架构: `docs/architecture/TRANSPORT.md` TCP 部分 sec.6.4
- 相邻决策: ADR-012 (会话读缓冲), ADR-013 (写批处理) -- 都对 conn 做了包装,
  包装顺序与字节所有权是同一类问题
