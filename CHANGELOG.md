# Changelog

All notable changes for `shark-socket` are recorded here.

This project uses semantic versioning. Pre-release tags use the form
`vMAJOR.MINOR.PATCH-rc.N`.

## Unreleased

### 配置热重载子集 (2026-10-18)

对比报告 C6: 配置一次加载, 只有证书能热更. 改限流阈值、黑名单或日志级别都要重启,
而重启会掐断全部在线会话 -- 事故中要收紧限流时, 手边唯一的工具是最贵的那一个.
根因不是"没有热重载", 而是**从来没有声明过哪些字段可以不重启生效**, 于是每个字段
都只能按最保守的方式处理. 见 ADR-019.

**热更子集是封闭的**: `log_level` / `blacklist` / `rate_limit` / `shutdown_timeout`.
判据一条: 新值能否在不重建 socket、goroutine 或 handler 的前提下生效. `SIGHUP`
重读文件与环境变量, 应用该子集.

**监听类变更整份拒绝.** 文档里只要含 `health_addr` / `metrics_addr` / `protocols`
(地址、路径、模式、TLS、CORS) 的变更, 整次重载返回
`ErrListenChangeRequiresRestart`, 热字段一个也不应用. 运维很少一次只改一个字段,
"改端口 + 顺手加一条黑名单"是最常见的组合; 部分应用会把进程留在一种没人能推理的
状态. 整份拒绝的代价是一条错误日志, 换来的是"要么全生效要么全不生效".

**先验证后应用.** 校验 (含日志级别解析、黑名单严格解析) 全部通过后才写状态, 写阶段
只有原子 store, 因此不存在"应用到一半失败". 三条失败路径 (解析/校验/监听变更) 下
运行配置、黑名单、限流参数、日志级别全部保持原值, 只多一条

```text
config reload rejected, keeping the running configuration
```

**改的是活对象, 不是换对象**:

- `plugin.Blacklist` 改为持有 `atomic.Pointer[blacklistSet]`, 新增
  `Replace(entries []string) error` 与 `ValidateBlacklist`. 精确地址表与 CIDR 表
  封装在同一个不可变结构里, 重载不会出现"新精确表 + 旧 CIDR 表"的中间态. 构造器
  仍是宽松的(跳过非法条目), `Replace` 是严格的 -- 运维从配置文件来的输入被静默
  丢弃, 看起来会和"拉黑没生效"一模一样
- `plugin.RateLimit` 的 `rate`/`window` 改为 `atomic.Int64`, 新增 `SetLimit`/`Limit`.
  **计数不重置**: 收紧立即生效, 放宽随旧时间戳老化逐步放开; 清零等于给客户端一个
  "等到重载就能翻倍"的漏洞
- 新增 `core.LeveledLogger`(内部 `slog.LevelVar`)与 `core.ParseLevel`. 组件持有的是
  `core.Logger` 接口值, 换 handler 就要替换被八处各存一份的接口值; `LevelVar` 是
  slog 为此提供的原语, 每条 record 现读, 日志热路径不加锁

**补上一个既有缺口**: `app.New` 之前不注入 Logger, 网关拿到的是 `NopLogger` --
运行时的 `server failed` / `cert reload failed` 无人可见. 现在默认注入
`info` 级 stderr logger, "热更日志级别"才有作用对象.

**入口与生命周期**: `App.Reload(cfg)` / `App.ReloadFile(path)`. 不重走插件生命周期,
因为 `Gateway.Start` 已经调过 `Plugins().StartAll()`, 再走一遍会让限流的清扫 goroutine
有两条. 主程序的 SIGHUP 走独立 channel, 重载失败不会被误当成停机请求;
停机宽限期改为向 App 要当前值, 因此 `shutdown_timeout` 的热更真的会在下一次停机生效.

**验收** (`internal/app/reload_test.go`, `internal/plugin/reload_test.go`):

- 热更后新连接立即生效: 同一操作重载前能通、重载后被立即关闭 (elapsed < 1s 区分
  "关闭"与"静默丢弃"), 且重载前有同操作的阳性对照
- 存量连接不受影响: 重载前建立的连接继续 echo
- 失败保留旧配置: 非法黑名单条目、未知日志级别、截断的配置文件三条路径, 断言配置与
  日志级别都是旧值
- 监听变更整份拒绝: 断言 `ErrListenChangeRequiresRestart`, 且同一文档里的黑名单
  没有泄漏进来 (连接仍被服务)
- 限流: 2/s 下第 3 条被丢弃, 重载到 1000/s 后下一条通过 (计数未重置)
- 配置里没有限流段时重载"开启限流"被拒 (插件链构成在启动时固定)

**文档**: ADR-019; `DEPLOYMENT.md` 新增 sec.6.2 (热重载矩阵与三条性质),
`CONFIGURATION-20260530.md` 新增 Hot Reload 一节与四个环境变量.

### CoAP 分块下发 (2026-10-18)

对比报告 C3: 全仓没有 Block1/Block2, 于是任何超过单数据报的响应都只能靠 IP 分片,
而 Leshan 这类标准客户端对大 payload 默认走 block-wise -- 互联会直接失败.

**新增 `internal/transport/coap/blockwise.go`**:

- `BlockOption` 是 RFC 7959 的 NUM/M/SZX 三元组, 编解码用**最短字节数**
  (NUM 的高位零必须省掉, 全零是空值). 值超过 3 字节或 SZX=7(保留值)一律判非法
- `blockFor` 切块, `negotiateBlock2` 做协商. 协商的关键不是"取小的"而是
  **偏移换算**: 对端要 1024 而服务端上限 512 时, 对端的第 2 块是字节 2048,
  在 512 的块大小下是第 4 块. 直接沿用 NUM 会重复发送偏移 1024 的数据
- NUM 越界回 `4.02 Bad Option`, 而不是回一个空载荷的成功响应

**`WithMaxBlockSize(size)` 默认关闭.** 打开后超过块大小的响应按块下发; 关闭时
逐字节与之前一致 (有测试锁住"关闭时仍是单数据报且不带 Block2"). 默认关闭是因为
Block2 是 **critical** 选项: 不实现 RFC 7959 的客户端收到带它的响应必须整条拒绝,
默认打开会让"读 2 KiB 资源"对老客户端从成功变成失败. 合法块大小 16..1024,
其它值在配置时向下取整到 2 的幂 (线上只有 3 位指数, 表达不了 700).

**按请求重建响应体, 不缓存.** 缓存意味着在两次块请求之间持有一块大小由业务决定、
数量由并发读决定、生命周期由对端决定的缓冲 -- 与 ADR-017 刚清掉的资源形态同类.
代价是 `Responder` 每块被调用一次, 已写进 ADR-018 的后果与重新评估条件.

**Block1(分块上传)明确拒绝**: 带 Block1 的请求回 `4.13 Request Entity Too Large`
并附 Block1 提示. 忽略该选项的后果是每个分片被当成完整请求体执行一次 handler,
对 LwM2M 的 write/execute 是一次真实副作用; 测试断言 handler 未被调用.

**观察通知不分块**: 后续块由客户端请求驱动, 而通知是服务端主动推送的, 没有可依附的
请求. 需要观察大资源时应在注册的 GET 上走分块.

**验收** (自建客户端, 非 Leshan):

- 4 KiB body / 1 KiB 块: 4 块, M 位依次 1,1,1,0, 重组后逐字节相等
- 对端请求 256 字节: 4 块, 每块 256, 响应回显的 SZX 是 4 而不是服务端的 6
- NUM 越界: `4.02 Bad Option`
- 关闭时: 单数据报, 无 Block2 选项
- 重传同一 MessageID 的块请求: 走 dedup 缓存返回同一块, 不重新切
- 基准: `blockFor` 13.70 ns/op / 8 B / 1 alloc, `parseBlockOption` 2.23 ns/op / 0 alloc

**顺带补齐 `api` 门面**: A 类的准入选项当时只在传输包里, 公开 API 拿不到 --
补上 `WithTCPMaxConnectionsPerIP`, `WithTCPTLSHandshakeTimeout`,
`WithUDPMaxSessions(PerPeer)`, `WithCoAPMaxSessions(PerPeer)`,
`WithCoAPMaxBlockSize`, `WithHTTPMaxConnections(PerIP)`/`WithHTTPAcceptRate`,
`WithWebSocketMaxConnectionsPerIP`/`WithWebSocketHandshakeTimeout`,
`WithQUICMaxConnectionsPerIP`, `WithGRPCWebMaxConnectionsPerIP`/`WithGRPCWebHandshakeTimeout`.

**文档**: ADR-018; `TRANSPORT.md` CoAP 部分新增 sec.4.11 与 options 条目.

### 连接准入与握手预算 (2026-10-18)

对比报告 A 类 (P0 安全与稳定性) 五项一次落地, 见 ADR-017. 五项的共同根因不是
"某个传输有洞", 而是**准入没有单一实现点**: TCP 有 `MaxConnections`, HTTP 一个都没有;
WS/QUIC/gRPC-Web 有闸门, UDP/CoAP 没有; 有总量上限的地方都能被一个 IP 合法占满.

**新增共享原语 `internal/transport/shared/quota.go`**:

- `PeerQuota` 同时持有"总量"与"单来源"两个维度. 全局名额在**分片锁内 CAS 领取**,
  单来源计数在同一把锁下增减, 于是"打开了多少"与"这个来源占了多少"是同一份计数的
  两种读法, 不存在"归还了总量却漏了单来源"的路径
- 单来源的键是 **IP 而不是 `IP:port`**: 换源端口就换一份配额, 限制等于没有.
  `PeerIP` 用 `Unmap` 把 IPv4-mapped IPv6 折叠成 IPv4, 否则 `::ffff:1.2.3.4` 与
  `1.2.3.4` 是两个来源
- **没有 TTL 清理器**. 条目在计数归零时立即删除, 表里只留下真正持有连接的来源,
  规模天然被 `maxTotal` 界定. TTL 清理会引入第二个时间源, 并在"刚删完又建连"时
  短暂低估用量, 而收益是零
- 无法归属的空地址只计入总量: 否则所有不可归属的连接会共用 `maxPeer` 那一份配额,
  互相把对方挤掉

**`shared.acceptor.go` 从二维扩到三维** (总量 + 单来源 + 速率), 拒绝原因分为
`capacity` / `peer` / `rate` 返回给调用方, 由调用方归到自己的指标上. 速率拒绝时
归还刚拿到的名额 -- 否则每次限速都泄漏一个配额. `Acceptor` 在 nil 上安全
(`TryAccept` 放行, `Done` 空操作), 因为延迟清理会在从未领取名额的情况下调用它.

**五路 TCP 系传输同构接入**:

- TCP: `MaxConnectionsPerIP`; 握手从 `tls.NewListener` 的懒式首读前移到
  `handleConn` 开头的 `handshakeTLS`, 拥有独立预算 `TLSHandshakeTimeout` 与失败计数
  `tls_handshake_failed_total`; 成功后 `SetDeadline(time.Time{})` 清零
- WebSocket: `MaxConnectionsPerIP`, `HandshakeTimeout`, `ReadTimeout`,
  `ReadBufferSize`/`WriteBufferSize`(`WithUpgradeBuffers`)
- gRPC-Web / QUIC: `MaxConnectionsPerIP`, `HandshakeTimeout`. gRPC-Web 的 WS 模式
  把来源挂在会话上, 因为释放发生在 `closeWebSocketSession`, 那里看不到 request
- HTTP: 第一次有准入 (`MaxConnections` / `MaxConnectionsPerIP` / `AcceptRate`).
  实现**不是** `http.Server.ConnState`(报告的建议), 而是 `shared.NewGateListener`
  包装 listener: `ConnState` 是事后通知, 回调触发时会话已被接受, 能做的只有立刻关掉,
  客户端看到连接重置; 在 listener 层判定才能写出真的 503. 顺序 PROXY -> gate -> TLS:
  gate 在 TLS 之下因为 `net/http` 对 `*tls.Conn` 做类型断言, 在 PROXY 之上因为配额
  要按真实客户端算

**UDP / CoAP 伪会话准入**: `MaxSessions` / `MaxSessionsPerPeer`, 超限丢包并计
`sessions_rejected_total`. 归还绑在 `session.Close` 的 `closeOnce.Do` 内, 于是主动
关闭, TTL 淘汰, `LoadOrStore` 竞态丢弃三条路径都恰好归还一次. 写在清扫循环里会漏掉
"先被主动关闭, 再被清扫"的那次. 没有速率维度: 数据报没有"建连"这个可计数时机,
对每个数据报计数就是对流量限速, 那是 `RateLimit` 插件的职责.

**accept 错误分三类 + 退避**: 新增 `shared/accept_error.go`
(`closed` / `resource` / `temporary`) 与 `shared/backoff.go` 复用. `net.ErrClosed`
直接终止; `EMFILE`/`ENFILE`/`ENOMEM` 额外计 `accept_resource_errors_total`; 其余退避
后继续. 日志按连续失败段折叠成一行 -- fd 耗尽会持续到有 fd 被释放, 每次重试打一行
会把一次事故变成日志风暴. 取代了原来对任何错误固定 `time.Sleep(100ms)` 的写法.

**三个实现期被数据纠正的判断**:

- `handshakeTLS` 最初用带超时的 `context` 限制 `HandshakeContext`. crypto/tls 在 ctx
  取消时通过 `context.AfterFunc` 从**另一个 goroutine** 关掉底层连接, 对端可以早于
  计数与日志看到断开; 20 次重复里 6 次 `tls_handshake_failed_total` 停在 0.
  改为只用 `SetDeadline` 后稳定: 读同步失败, "计数 -> 日志 -> Close"顺序确定
- HTTP 拒绝路径最初直接写 503 再关连接, 测试拿到的是 `ECONNRESET` 而不是 503.
  带未读数据关闭 socket 会换来 RST (RFC 1122), RST 会丢掉刚写出的响应.
  改为先在 5ms 内排空最多 8x512 字节再写 503 并 `CloseWrite()`. 排空必须**有界**
- 指标自增原本写在 `Reject` 之后, 客户端可能在计数落地之前就观测到拒绝.
  `gateListener.Accept` 改为先 `OnReject` 后 `Reject`

**`TLSHandshakeTimeout` 的 0 值回落 `ReadTimeout`**, 不是"无上限": 旧实现里握手本来
就被 `ReadTimeout` 覆盖, 默认成无上限是一次藏在特性里的回归.

**验证** (`go test ./internal/transport/... -count=1`, 8 个包全绿; `-race` 全绿;
`golangci-lint` 0 issues):

- A1: `TestUDPMaxSessionsHoldsUnderManySourceAddresses` -- 200 个来源, 上限 64,
  最终 `SessionCount() == 64`, `sessions_rejected_total == 136`. 未按报告用 10 万
  伪造地址: 测试进程无法伪造源地址(需要 raw socket), 而被检验的性质是"表的大小由
  `MaxSessions` 决定, 不由来源数量决定", 200 与 10 万走同一段代码
- A2: 静默 TLS 对端在 **150.6ms** 被断开(原实现要等 5 分钟), `ReadTimeout=5m` 不变;
  握手成功后清 deadline 的一侧有 `TestTCPTLSHandshakeSucceedsThenServes` 锁住
- A3: `TestHTTPMaxConnectionsAnswers503` / `...PerIPAnswers503` / `...UnlimitedKeepsServing`,
  客户端拿到真 503 (`-count=5` 稳定)
- A4: `TestTCPAcceptLoopSurvivesResourceErrors`(注入两次 EMFILE, 断言退避后自动恢复
  且两个计数器各为 2) 与 `TestTCPAcceptLoopStopsOnClosedListener`(`net.ErrClosed`
  直接终止). 未用 `ulimit -n`: 本机是 Windows, 压 fd 会先打垮测试进程自身
- A5: TCP / WS / gRPC-Web / QUIC / HTTP 各一个测试, 全部用 `127.0.0.2` 与
  `127.0.0.3` 作为两个真实对端. 每个测试断言三件事: 同址第二份被拒, **另一地址
  仍被服务**, 释放后同一地址重新可用(否则每次拒绝都泄漏一个名额)

**默认行为不变**: 所有新字段零值即"不限制", 既有部署的行为与计数不变.

**文档**: 新增 ADR-017; `TRANSPORT.md` 更新 TCP sec.4.3/4.4/4.5 与
sec.6.1/6.2, UDP sec.2.2/4, WebSocket sec.4, 汇总 sec.4.2/4.3;
`OBSERVABILITY.md` 3.3 的内置指标表从 1 项补到 10 项.

### Benchmark Regression Gate (2026-10-18)

批 3 的 E2: 基准有了 CI 门禁. 没有门禁的基准, 半年后就是过期数字.

**新增 `scripts/benchgate/` 包**:

- 用 `git archive` 把 base commit 导出到临时目录. 不用 `git worktree`: 它会往
  `.git` 里写 ref 与锁文件, 在开发机上会被 IDE 的 git 集成抢占, 然后以一个脚本
  从未申请的锁报 "Permission denied" -- 本地第一次跑就是卡在这个锁上
- 只用 **CPU 型** 微基准 (`SessionManager`, `PluginChain`, CoAP 编解码,
  framer round trip, peer key, 广播分派, 拷贝, 插件限流). socket 型基准
  (`BenchmarkTCPEcho` 等) 在共享 runner 上两轮之间就能差几十个百分点,
  拿它们做门禁只会得到噪声或一个再也没人看的阈值
- **两侧交替采样** (base, head, base, head ...), 并且每轮**交换先后手**. 先跑完
  一侧再跑另一侧, 在 16 线程开发机上把两个只差一个未使用的 HTTP handler 的 commit
  判出了 6 项 20-45% 的"回归", 同时另有十几项"提升" 20-46% -- 那测的是机器的漂移.
  交换先后手是因为先跑的一侧离上一个包的收尾更近, 要吃机器回落的那一段
- 采样前先 `warmBuild` 两侧的测试二进制. 否则每个包的第一轮把编译也算进样本, 而
  编译器只会落在某一侧的某一个样本里
- 判定用**单侧 95% 置信下界**, 不是裸比值: 只有当下界仍然高于 `-threshold`
  (默认 20%) 才失败. 判定规则一共试了三版, 前两版被同一组对照实验否掉:
  - 比值比中位数: 同一 commit 的两个构建判出 6 项 20-48% 假回归
  - best-of-N (取两侧各字段最小值), 理由是干扰只能让样本变慢. 被数据否掉: 一侧
    三次采样读到 76930 / 22875 / 30185 ns/op, 最小值本身就是从 3.4 倍区间里抽出的
    一个随机数, 不是成本下界. 仍剩 6 项假回归
  - 当前规则: 配对比值 (head_i / base_i) 的均值减去 t 倍标准误. 用 t 表而不是固定
    系数, 因为小样本时标准误本身就不准, 固定系数恰好在门禁最弱的地方最自信
- 少于 3 次采样的基准不判定 (verdict 写 `n=N, not judged`), 报告里如实计数, 不假装
  通过. 这也是 `-count 1/2` 只能当冒烟跑的原因
- 分配数用**中位数**且不参与统计: 整数不是时长, 0 -> 1 不可能是噪声, 而"每条消息
  多一次分配"正是本轮工作要消灭的东西. 只有一半迭代多分配也算回归, 时间变快也不能
  抵掉
- 不解析 benchstat 的渲染表格 (列会随 `-count` 与可用指标变化). 两轮原始输出都落
  到 `-out`, 想人工看置信区间时 `benchstat base.txt head.txt` 仍然可用
- 报告写入 `-out/report.md`: 两侧中位数, delta, 95% 下界, 分配数与 verdict. 下界是
  否决依据, 所以一行 `+14.3% / -17.5%` 自己就说明了"这台机器判不了它"
- 解析与判定逻辑拆到 `parse.go` (无构建标签), 因此有单测: 解析 (含多出来的 MB/s 列
  不能把后面的列挤位), 配对统计, 小样本保护, t 表单调性, 阈值, 分配数回归, 豁免,
  只在单侧出现的基准, 以及未判定的标注. `go run 单文件` 看不到同包其他文件, 所以
  整个门禁是一个包目录 `scripts/benchgate/`, 不再是 `//go:build ignore` 的单文件脚本

**验证**: 两个对照实验, 缺一不可 -- 只证明"不误报"的门禁, 可能永远不报.

- 负对照: `-base HEAD`, 工作树相对 HEAD 只多了门禁脚本本身,`-count 6
  -benchtime 100ms`, 52 项基准 **0 失败**
- 正对照: 在 `shared.NewPeerKey` 里临时插一行 `addr.String()`, `-count 4
  -benchtime 50ms`, 门禁报出 `BenchmarkUDPSessionLookup: +210.2% slower, 95%
  bound +191.5%` 与 `allocs/op 0 -> 3`, 并连带报出复用同一条路径的 4 个 plugin
  chain 基准的分配数回归. 扰动已回退, `git diff` 干净

**CI**: 新增 `bench-gate` job (`fetch-depth: 0`, 因为要构建 base),
`-count 6 -threshold 1.20`, 报告作为 artifact 上传.

### Optional Admin Listener (2026-10-18)

批 3 的 C7: 新增可选的管理/调试监听, 默认关闭. 说明见
[`GATEWAY.md`](docs/architecture/GATEWAY.md) 第 2.8 节.
不改 `core` 契约, 故未新增 ADR.

**问题**: 全仓无 `pprof` / `expvar`, `Gateway.Health()` 只返回一个 map. 线上出问题
时既没有 CPU/heap profile 可抓, 也没有一个不依赖指标后端的"当前有多少会话"入口.

**改动**:

- 新增 `runtime.WithAdmin(AdminConfig{Addr, Token})` 与 `Gateway.AdminAddr()`
  (`Addr` 用 `:0` 时取实际端口)
- 端点: `GET /healthz` (与 `Health()` 同体), `GET /sessions`
  (`total` / `by_protocol` / `capacity`), `GET /plugins` (按执行顺序),
  `GET /debug/pprof/*`
- 只读; 不暴露会话 ID 与对端地址, 只给聚合值
- 新增 `SessionManager.Capacity()` 与 `PluginChain.PluginNames()`;
  `Runtime.PluginNames()` 在 runner 不支持枚举时返回 nil
- `Gateway` 保留 `rawSessions` (装饰前的 manager): `metricSessionManager` 内嵌的是
  `core.SessionManager` **接口**, 具体类型上的 `Capacity()` 透过它取不到
- 非 loopback 地址且无 token 时 `Start` **失败**, 不是警告: pprof 能读进程内存与
  全部 goroutine 栈
- pprof 挂独立 mux 而非 `DefaultServeMux`; 只设 `ReadHeaderTimeout`, 不设
  `WriteTimeout` (profile/trace 会跑满请求时长, 写超时会截断结果)
- 顺手把 `Start` 里内联的回滚逻辑提成 `rollbackStart`, admin 启动失败复用同一条路径

**验证**: 新增 7 个测试 -- 默认不监听, `/sessions` 与 `Runtime.Sessions().Count()`
一致且按协议分组, `/plugins` 按优先级而非追加顺序, `/healthz` 与 pprof 可达,
写方法返回 405, 无 token 401 / 带 token 200, 非 loopback 无 token 拒绝启动且
回滚已启动的 server. `go test -race` 通过, `golangci-lint` 0 issues.

### PROXY Protocol Listener (2026-10-18)

批 3 的 C4: 在监听层支持 PROXY protocol v1/v2, 让部署在 L4 负载均衡器后面的服务
能拿到真实客户端地址. 决策记录为
[`ADR-016`](docs/decisions/ADR-016-proxy-protocol.md), 说明见
[`TRANSPORT.md`](docs/architecture/TRANSPORT.md) TCP 部分第 6.4 节.

**问题**: `RemoteAddr` 直接取 `conn.RemoteAddr()`, LB 后恒为 LB 地址. 黑名单,
per-IP 限流, 审计会同时失效, 而且不报错. 这是"上生产才暴露"的一类.

**改动**:

- 新增 `shared.NewProxyListener` / `ProxyConfig` / `ProxyProtocolMode`
  (`Off` / `V1` / `V2` / `Auto`), 头部解析在 `Accept` 返回之前完成,
  `Framer` 与 `Session` 一行未改
- v1 用 `ReadSlice` 而非 `ReadString` (后者对不换行的对端会无限分配);
  v2 载荷上限 2048 字节 (规范允许 65535, 按对端声明分配等于白送放大攻击),
  TLV 一并吞掉而不是留给 framer
- `proxyConn` 覆盖 `Read` / `RemoteAddr` / `CloseWrite`; 少了最后一项,
  半关闭会退化成整条 `Close` 并截断对端在途响应
- 包装顺序固定为 `shared.Listen` -> `NewProxyListener` -> `tls.NewListener`:
  头在 TLS 之下, 均衡器写的是明文
- 缺少或错误的头一律拒绝 (关闭 + `proxy_header_rejected_total` 计数), 不向上
  返回 Accept 错误 -- `net/http.Server.Serve` 会因非 `ErrClosed` 的 Accept 错误
  停掉整个服务. 拒绝在 `Accept` 内部循环处理
- 没有"头可选"模式: 若既有头又允许无头, 直连客户端就能自己决定身份归属
- TCP / HTTP / WebSocket / gRPC-Web 新增 `Options.Proxy` 与
  `WithProxyProtocol(mode)` / `WithProxyHeaderTimeout(d)`

**语义变化 (仅开启时)**: `RemoteAddr()` 返回头里声明的客户端地址,
`LocalAddr()` 仍是本端 socket 地址, 两者不再来自同一个 socket. 未开启时行为不变
(`TestServerWithoutProxyProtocolKeepsSocketPeer` 锁住这条).

**未做**: 来源 CIDR 白名单. 信任模型是网络拓扑 (端口只对均衡器可达), 理由与
重新评估条件写在 ADR-016. UDP / CoAP / QUIC 不在本轮范围.

**验证**: 新增 12 个测试 -- v1 十一行表 (含 UNKNOWN, 非 CRLF, 家族与字面量不符,
超长行), v2 八行表 (含 TLV, LOCAL, UNSPEC, 版本 nibble, 超限载荷, 截断载荷),
版本不匹配, listener 重写 `RemoteAddr` 且 payload 不丢, 坏头被跳过而服务继续,
静默客户端超时; 传输级 4 个 (TCP 三个 + WebSocket 握手一个, 后者证明在
`net/http` 下也成立). `go test -race` 通过, linux/darwin 交叉编译通过.

### Listener Socket Options (2026-10-18)

批 3 的 C5: 把 SO_REUSEPORT 与 TCP keepalive 节奏变成 listener 级配置, 四路 TCP 系
传输 (TCP / HTTP / WebSocket / gRPC-Web) 共用一份实现. 说明见
[`TRANSPORT.md`](docs/architecture/TRANSPORT.md) TCP 部分第 6.3 节.
不改 `core` 契约, 零值时与 `net.Listen` 行为逐字节一致, 故未新增 ADR.

**为什么需要自己建 socket**: `net.Listen` 返回时 socket 已在 listen 状态, 而
`SO_REUSEPORT` 必须在 `bind` 之前由 `Control` 回调设置; keepalive 的节奏也超出
`SetKeepAlive` 这一档开关能表达的范围.

**改动**:

- 新增 `shared.ListenOptions` / `shared.Listen` (`internal/transport/shared/listen.go`)
- `listen_unix.go` (`//go:build unix && !solaris`) 用 `golang.org/x/sys/unix` 设
  `SO_REUSEPORT`; `listen_other.go` 返回 `shared.ErrReusePortUnsupported`
- 零值 `ListenOptions` 不装 `Control`, 不调任何 `Set`, 与 `net.Listen` 等价
- TCP / HTTP / WebSocket / gRPC-Web 新增 `Options.Listen` 字段与
  `WithReusePort()` / `WithKeepAlive(idle, interval, count)`
- WebSocket 与 gRPC-Web 的 TLS 分支由 `tls.Listen` 改为
  `shared.Listen` + `tls.NewListener`: 前者自己建 socket 会丢掉 `ListenOptions`;
  顺带把重复的 `&http.Server{...}` 构造提到分支外
- `golang.org/x/sys` 由间接依赖升为直接依赖 (`go mod tidy`)

**行为变化 (有意)**: 平台不支持 `SO_REUSEPORT` 时返回错误而非静默忽略. 该开关影响
部署拓扑 (多进程共端口), 静默失效会让滚动发布时的端口冲突出现在离配置很远的地方.

**验证**: 新增 6 个测试 -- 零值绑定, keepalive 映射 (5 个子例), keepalive 对
listener 生效, reuseport 在 linux/darwin 上成功 / 其它平台断言显式错误,
server 级两个 (`TestServerServesWithListenOptions`, `TestServerReusePort`).
Linux `SO_REUSEPORT` 正例未在本机执行 (开发机为 Windows, 容器不可用), 由
`GOOS=linux go build` + `go vet` 保证编译期正确, 测试在 linux/darwin CI 上覆盖.

### CoAP Reliability State Machine (2026-10-18)

批 3 的前两项: 重传调度改为 RFC 7252 第 4.2 节规定的指数退避 + 随机抖动,
去重改为重发缓存的响应. 说明见
[`TRANSPORT.md`](docs/architecture/TRANSPORT.md) 第 4.10 节.
两项都不改 `core` 契约, 故未新增 ADR.

**C1 重传调度**:

- 新增 `coap/retransmit.go`: 参数常量 (ACK_TIMEOUT/ACK_RANDOM_FACTOR/
  MAX_RETRANSMIT) + 纯函数 `decide`/`advance`
- `pendingNotify` 增加 `timeout` 与 `deadline`, 每条消息按自己的截止时刻触发
- 首次超时从 `[2s, 3s)` 随机抽取 (原来固定 2s, 无抖动), 每次翻倍
- `deadline` 从上一个 deadline 推出, 调度器 tick 延迟不累积; 迟到的 tick 不补发
- `retransmitTick` 250ms 是调度器分辨率而非重传间隔

时序 (RFC 7252 第 4.2 节表格): 重传 4 次, 每间隔 `[2,3) [4,6) [8,12) [16,24)`,
累计 `[2,3) [6,9) [14,21) [30,45)`, 放弃在 `[62,93)` (MAX_TRANSMIT_WAIT).

**修正报告自身的验收标准**: `COMPARISON-261018-OSS-PEERS.md` C1 写的是
"第 n 次重传落在 `[2^n*2, 2^n*3]`" -- 既把每间隔量当成累计量, 指数又错一位.
按 RFC 表格修正为每间隔 `[2^(n-1)*2, 2^(n-1)*3)`, 并在报告里记录了这次更正.

**C2 去重**:

- `seen` 的值从 `struct{}{}` 改为 `*seenEntry`, 保存首次响应的确切字节
- 重复 CON 重发同样字节, 不重跑 handler (RFC 7252 第 4.5 节); 此前回的是
  `2.03 Valid`, 相当于声称一个并不存在的响应码
- 首次响应尚不存在时回空 ACK 0.00 (separate response), 而非猜测一个码

**修正报告自身的取证**: C2 原文写"无 duplicate 检测", 但 `seen` 表早已存在且
重复 CON 不会重跑 handler. 真实缺口只是"没有缓存响应", 已在报告里更正.

**验证**: 新增 6 个测试 (RFC 表格逐行断言 + 极值抽样, 抖动, 调度器粒度不累积,
ACK_TIMEOUT 之前绝不触发, 迟到 tick 不补发, 放弃前等待最后一个超时);
`TestCoAPDuplicateCONDoesNotRerunHandler` 改用 `Responder` 返回可区分负载,
断言重复请求拿到 `response-1` 而不是 `response-2`.

### Shared Hot Path Overhead (2026-10-18)

第四批: 消除共享结构本身的开销 -- 对端键, 插件链读路径, 会话表争用. 决策记录为
[`ADR-015`](docs/decisions/ADR-015-remove-shared-hot-path-overhead.md).

**改动**:

- 新增 `shared.PeerKey` (`[16]byte` 地址 + `uint16` 端口 + zone) 代替 `addr.String()`
  作 UDP / CoAP 的会话表键, 并在会话上缓存供错误路径复用. 地址统一存 16 字节形式,
  使 IPv4 与 IPv4-mapped 落到同一个键
- `runtime.PluginChain` 改为 `atomic.Pointer[pluginView]` 发布不可变视图, 读路径
  不再加锁; `OnMessage` / `OnAccept` 改为链级单次 recover (用下标定位插件),
  `OnClose` / `StartAll` / `StopAll` 保留逐插件 recover
- `runtime.SessionManager` 按会话 ID 分 64 片 (每片独立 `RWMutex` + map, 按 64 字节
  对齐); 全局容量改在共享计数器上 CAS 认领, 判重仍先于判容量
- 清理 `scripts/protocolgen` 两处死代码 (`grpcWSHandler`, `readStream`),
  `golangci-lint` 由 2 issues 降为 0

**数据** (开发机 Windows / Ryzen 7 8845HS, 16 线程):

| 项 | 改前 | 改后 |
| --- | --- | --- |
| UDP 会话解析 | 99-113 ns/包, 32 B, 3 allocs | 46-47 ns/包, 0 B, 0 allocs |
| 插件链 5 插件 (16 并发) | 23.4-24.5 ns/op | 4.5-5.3 ns/op |
| 插件链 + 并发重配置 (16 并发) | 83-97 ns/op | 7-17 ns/op |
| 会话注册/查询/注销 (16 并发) | 366-382 ns/op | 149-163 ns/op |

三项并发收益随核数放大, 分别为 4.6 倍, 6 倍, 2.4 倍. 单线程会话管理一项在噪声内
持平 (184-194 -> 165-203 ns/op), 未回退.

**未做**: 数据报 payload 拷贝仍在 (每包 4 次分配降到 1 次), 要归零需要缓冲池并改
`Send` 的所有权; 传输内表与全局表的双表保留 -- 两者键与用途不同, 合并需要全局表
支持按地址查找.

**验证**: 新增 16 个测试, 覆盖 PeerKey 的四种区分维度与 v4-mapped 归一, 链级 recover
的三条语义 (消息 panic 转错误, accept panic 回滚, close panic 继续), 零值链可用,
以及分片后并发容量恰好等于上限, 重复注册归还名额, 重复注销不下溢, ID 均匀分片.

### TCP Read Path Buffering (2026-10-18)

第一批优化落地: 读路径按会话缓冲, 消除 header 逃逸. 决策记录为
[`ADR-012`](docs/decisions/ADR-012-session-read-buffer.md), 规则与实测更新在
[`TRANSPORT.md`](docs/architecture/TRANSPORT.md) 第 2.1 / 2.6 / 3.6 节.

**改动**:

- `tcp.Options.ReadBufferBytes` + `WithReadBufferBytes`, 默认 1024, 0 表示直读 conn
- 会话持有 `bufio.Reader` 并在整个连接生命周期内复用 (`session.go`)
- `LengthPrefixFramer` 走 `Peek(4)` + `Discard(4)`: header 不再逃逸, 每帧 1 次分配
- `LineFramer` 走 `ReadSlice`: 每字节一次内存扫描, 结果显式复制一次
  (`ReadSlice` 的切片不能跨调用持有)
- `RawFramer` 例外不缓冲: 其语义依赖"一次 Read 返回多少", 由
  `framerAcceptsBufferedRead` 统一判定

**数据** (开发机 Windows / Ryzen 7 8845HS, `BenchmarkTCPFramerReadCost`):

| 帧大小 | reads/frame 无缓冲 | reads/frame 1 KiB | allocs/frame |
| --- | --- | --- | --- |
| 64 B | 2.000 | 0.078 | 2 -> 1 |
| 256 B | 2.000 | 0.266 | 2 -> 1 |
| 1 KiB | 2.000 | 1.016 | 2 -> 1 |

代价是每连接正好 `ReadBufferBytes` 字节常驻堆: 1,000 空闲连接 6,647 B -> 7,888 B
(`BenchmarkTCPIdleConnectionFootprint`, Windows 对照). 回环 echo
(`BenchmarkTCPEchoReadBuffer`) 在 Windows 上无显著差异 (46-49 us, 调度主导),
因此本项不能靠回环延迟证明.

**验证**: 新增 6 个测试 (覆盖 EOF 语义, ReadSlice 别名, 长行跨缓冲, RawFramer 例外,
会话级读次数 7 -> 2) 与 1 个 fuzz 目标 (`FuzzBufferedFramerRead`, 30 万次执行无失败);
`go test ./...`, `go test -race ./internal/transport/tcp/`, `go vet` 全绿.

**未闭合**: 云端 Linux 资源阶梯未重采, README 的 6,219 B/连接 仍是改动前数值,
已就地标注差额; 写路径批处理 (B2), 广播共享帧 (B4), 客户端 `Receive` 的读缓冲
未开始.

### Broadcast Shared Frame (2026-10-18)

第三批优化: 广播不再每个收件人复制一份 payload. 决策记录为
[`ADR-014`](docs/decisions/ADR-014-shared-broadcast-frame.md), 契约见
[`CONTRACTS.md`](docs/architecture/CONTRACTS.md) 第 5.4.1 节.

**改动**:

- `core.SharedFrame`: 不可变 payload + 引用计数, `NewSharedFrame` 只复制一次
- `core.SharedSender`: **可选**接口 (UDP/WebSocket 的 `Send` 本就不复制, 不需要它)
- `SessionManager.Broadcast` 按能力分派: 能共享的共用一个帧, 其余走普通 `Send`;
  只有一个共享者时不建帧
- TCP 写队列元素 `[]byte` -> `pendingWrite{payload, frame}`; 会话在写出后, 关闭时释放引用; `SendShared` 失败则不转移引用, 由广播侧释放

**实测** (`BenchmarkBroadcastFanOut`, 1 KiB payload, 开发机 Windows):

| 收件人 | 共享帧 | 逐个复制 |
| --- | --- | --- |
| 100 | 7,264 B/op, 9 allocs, ~4-6 us | 215,895 B/op, 208 allocs, ~50-56 us |
| 1,000 | 52,576 B/op, 12 allocs, ~33-35 us | 2,132,297 B/op, 2,011 allocs, ~1.0-1.1 ms |

1,000 收件人: 2.13 MB -> 52.6 KB (40 倍), 2,011 -> 12 次分配, 1.05 ms -> 34 us.
剩余的 O(N) 是 `Snapshot` 与分派切片的扩容 (每收件人 8 字节级), 不是 payload 复制.

**代价**: 写队列每槽多 8 字节 (默认 128 槽约 +1 KiB/连接), 云阶梯需复核.

**验证**: 8 个新测试 (共享同一对象/同一底层数组, 失败路径释放, 混合会话回退,
调用方可立即复用缓冲, 会话关闭释放队列中帧, 背压不绕过, 关闭后返回同一哨兵),
`go test ./...`, `-race` (core/runtime/tcp), `go vet`, `gofmt` 全绿.

### TCP Write Path Batching (2026-10-18)

第二批优化: 写路径合并写 + 可选批处理. 决策记录为
[`ADR-013`](docs/decisions/ADR-013-write-batching.md), 规则在
[`TRANSPORT.md`](docs/architecture/TRANSPORT.md) 第 2.7 / 3.6 节.

**改动**:

- `framedWrite(w, a, b)`: unix 构建走 `net.Buffers` (writev) 一次写出长度头 +
  payload (或 payload + 分隔符); 非 unix 保持两次 `Write`. 分开实现而不是无条件
  调用 `WriteTo` 是因为它在没有 writev 的平台上会退化成两次写, 却仍要分配那 4 字节
- `tcp.Options.WriteBufferBytes` + `WithWriteBufferBytes`, **默认 0 (关闭)**:
  开启后会话持有 `bufio.Writer`, `writeLoop` 把队列中已排队的帧全部写入后 `Flush`
  一次; 队列无后继帧时立即冲刷, 低负载延迟不变
- 写超时从每帧一次变为每批一次 (语义仍为"对端最多占用写路径多久")
- `newSession` 改为接收 `sessionConfig`: 位置参数已排到 7 个, 其中两个是每连接
  内存旋钮, 顺序写错编译器查不出来

**验证**: 3 个新测试 (3 帧排队时底层 `Write` 6 -> 1, 且解码校验每帧完整;
单帧立即冲刷; 批级超时), `go test ./...`, `-race`, `go vet`, `gofmt` 全绿,
`GOOS=linux/darwin/windows` 三个构建均通过 (writev 分支只有 linux/darwin 编译).

**未证明的部分**: Windows 回环的 16 帧流水线 echo 两组无可分辨差异
(677-936 us 对 724-855 us, 调度主导), 且 writev 在 Windows 不生效. 本项收益需在
Linux 云主机上用 `strace -c` 或阶梯压测复核后才能写进 README; 若复核无收益,
按 ADR-013 的重新评估条件应删除该开关而不是留着.

### Peer Comparison and Improvement Backlog (2026-10-18)

新增 [`docs/reports/COMPARISON-261018-OSS-PEERS.md`](docs/reports/COMPARISON-261018-OSS-PEERS.md):
把对比对象从 smart-socket 单一目标扩展到 gnet v2 / netpoll / Netty / t-io,
只比机制不比吞吐 (沿用 261017 的口径), 产出 5 类共 29 项带 file:line 取证的差距
与三批实施顺序.

**取证的三个主要结论**:

- 读路径在裸 conn 上按帧边界读: `LengthPrefixFramer` 每帧 2 次 Read,
  `LineFramer` 每字节 1 次 Read; 写路径每帧 2 次 Write; 全仓 `sync.Pool` /
  `bufio` / `net.Buffers` 各 0 命中
- 可观测只到接口层: `ObserveHistogram` 生产代码 0 调用点, Prometheus 直方图导出为
  无分位数的 summary, 生产代码只有 5 个指标名且 0 个 label, `Tracer` 除生命周期外无埋点
- CoAP 重传为固定 2s + 4 次, 不符 RFC 7252 第 4.2 节的随机初始超时与指数退避;
  无 duplicate 检测, 无 Block1/Block2

**同时修正**: `docs/reports/README.md` 2.1 节把资源基线写成了 Windows 值
(6,647 B / 164.5 MB), 与 261017 报告的云端 Linux 结论 (6,219 B / 40.3 MB) 不一致;
现按 Linux 值更正并保留 Windows 对照说明.

### Documentation Reorganization (2026-10-17)

全仓 67 份说明文档做了一次结构整理.新增 [`docs/README.md`](docs/README.md) 作为文档
总入口,规定各目录职责与两套命名规则;新增 `docs/architecture/README.md` 给此前互不
引用、无索引的 12 份专题文档建导航.详见这两份索引.

**归档与归位**(原路径均留跳转 stub,历史引用不断链):

- `docs/guides/Architecture.md` -> `docs/archive/Architecture.md`:与 `docs/architecture/`
  下的分专题文档重复(V9 审查 H15 / L9)
- `docs/design/REARCHITECTURE-261016.md`(v1)-> 归档,已由 `-v2.md` 取代
- `docs/design/WORKER-ELASTICITY-261016.md` -> 归档,内容已并入 v2 第 11 节
- `docs/architecture/OUTPUT-PLAN.md` -> 归档,输出计划已执行完毕
- `docs/architecture/ARCHITECTURE-FILE-STRUCT.md` -> 改写为 `docs/README.md`

**命名**:`NETWORK-VALIDATION-DIRECT-261016-092259.md` ->
`NETWORK-VALIDATION-261016-092259-DIRECT.md`,标签回到日期之后.

**链接**:修复 18 处真断链(11 个 ADR 仍指向迁移前的 `../CONTRACTS.md` 一类旧路径,
其中 3 处指向从未落地的 `LAYERING.md` / `PERFORMANCE.md` / `TESTING.md`),并把
`PROTOCOL.md` / `TRANSPORT.md` 表格里 26 处会被渲染成超链接的 OK / X 标注改回纯文本
(本意是"OK,由 retransmitLoop 实现"这类补充说明,不是链接).复检后断链 0,
字符违规 0,无孤儿文档.

### V10 Review - Multi-Host Validation Batch (2026-10-16)

在两台 8C/16G 云主机(srv2 / srv3, Ubuntu 26.04)上复验 V10 批次.同一 commit
在两台机器上结果不一致,由此定位并修复 2 项缺陷.详见
`docs/reports/CLOUD-VALIDATION-261016-SRV23.md`.

**部署测试的可用性判定不成立(1 个根因,3 个失败步骤)**:

- `TestDeployToolRenderingWhenAvailable` 用 `exec.LookPath` 判定工具可用性,问的是
  "PATH 上有没有叫 docker 的文件",而不是"`docker compose` 能不能跑".Ubuntu 的
  `docker.io` 包不带 compose 插件,于是该主机的判定通过、子命令以 125 退出
  (`unknown command`),测试报失败.表现为 `integration` / `deploy` / `race` 三项
  同时转红,而每一项日志里失败的断言都只有 `deploy_test.go:149` 这一处;同一 commit
  在装有 compose v5.5.0 的另一台机器上三项皆绿
- 改为探测而非推断:新增 `toolRuns(name, args...)`,对测试真正依赖的那次调用做一次
  无副作用试探.工具包方式不同的机器正确地跳过,而确实具备该能力的机器仍然渲染
  清单、仍然能抓到真正的清单错误.`kubectl` / `helm` 同样按此处理

**后台清扫器的默认间隔分支从未被覆盖**:

- 覆盖率在同一 commit 上不可复现:两台主机分别报 79.7% 与 79.6%.逐函数比对
  `go tool cover -func` 后定位到 3 个函数,其中只有这一处是系统性缺口 --
  `Memory.StartSweeper` 的 `interval <= 0` 分支,调用方无一传非正值,时序驱动的
  清扫测试传的是 10ms,该函数因而无论怎么跑都停在 88.9%
- 这个分支值得单独立测:它是调用方传 0 与 `time.NewTicker(0)` 之间唯一的一道
  防护,而后者对非正周期会 panic;panic 发生在清扫器 goroutine 上,回归因此会直接
  中止整个测试二进制,而不是让某个测试失败.补测后 88.9% -> 100.0%,两台主机各 5 次
  独立单跑均为 100.0%
- 修完这一处后重跑,总覆盖率变为 79.8% / 79.7%,差异仍在,但受影响的函数**完全换了
  一批**(第二轮是 `plugin/cluster.go Start`、`tcp/session.go readLoop`、
  `websocket/server.go pingLoop`).两轮集合没有交集,说明这不是若干固定薄弱点,而是
  同一类竞态在不同函数上随机显现:所有受影响函数的共同属性是定时器驱动的 goroutine
  循环,其退出与测试收尾之间没有同步点.因此无法靠补测消除 -- 要做到确定性只能改
  生产 API 暴露完成信号,或加入概率性 sleep,后者本项目在池的丢包测试上已明确否决过
  一次.记为**测量边界**:覆盖率可复现区间为 79.6-79.8%(三轮观测:79.7/79.6、
  79.8/79.7、79.7/79.7),今后应报区间,不应声明与本地逐位一致.对 CI 无实际影响,
  门限为 70%

**云端验证**(srv2 47.120.44.128 / srv3 8.130.190.141,各 8 vCPU / 15.3GB,
Ubuntu 26.04, Go 1.26.9, gcc 15.2.0):

修正后两台主机均为 10 步全绿 -- 编译 2-3s / vet 0s / unit 6s / integration
62-65s / deploy 7-9s / cover 6-7s / race 63-65s / govulncheck 8-14s / 产物 0-1s,
覆盖率 79.6-79.8%,`govulncheck` 0 个可达漏洞,产物 sha256 在两台机器上完全相同
(`61d52e8b...`,13954176 字节).6 组压力用例 `fail=0`.

跨主机压测(srv2 服务端 / srv3 施压,handler 1ms,队列 512,丢包策略)表明弹性池
在同等负载下的价值:供给提到 12000/s 时固定 8 worker 丢包 43.88%、投递 67007 条,
弹性池(2-16)丢包 13.55%、投递 103222 条 -- 同等负载下多投递 54.0%,把固定上限造成的
拒绝挽回了约七成.同时如实记录一处不利结论:6000/s 供给下弹性池丢包 4.06%,高于固定
8 worker 的 0.46%,原因是弹性池从 4 worker 起步、扩容爬坡落在 10s 测量窗口内.详见
`docs/reports/CLOUD-VALIDATION-261016-SRV23.md`.

**验证**:7 种模式全部退出码 0,`golangci-lint` 0 issues,`govulncheck` 0 漏洞,
本地 `gofmt` 干净.


### V10 Review - CI Restore Batch (2026-10-16)

V10 全量审查(逐项复现 - 修复 - 验证 - 提交)后的修复批次.详见
`docs/reports/PROJECT-REVIEW-261016-163106.md`.审查结论:8 个 CI job 中 6 个为绿
(validate-ubuntu / validate-windows / race / coverage / docker-build / 以及
ubuntu 侧集成),红灯只来自两个静态检查 job;另有一个时序竞态测试会让 race job
随机转红.本轮修复 4 项.

**CI 阻断 - 静态检查**:

- **lint job 全红**:`golangci-lint` 5 处未通过(注意默认 `max-same-issues=3` 会
  掩盖同类项,该批实际为 9 处) -- `ScalablePool.shouldScale` 是死函数(判定逻辑
  已完整内联进 `maybeScale`,只剩注释提到它);7 处 QF1008 冗余内嵌字段限定符
  (`p.poolCore.x` -> `p.x`;`WorkerPool.Handler` / `Enabled` 因自身同名而保留
  限定符,否则会递归);1 处 SA2001 空临界区.删除死函数并把其"为何只用队列占用率"
  的设计说明并入 `maybeScale`;测试中的空 `Lock`/`Unlock` 对是断言本身,改为用
  atomic 显式记录持锁事实并在 select 之后断言
- **security job 全红**:`govulncheck` 命中 11 个可达漏洞.`go` 指令 1.26.5 ->
  1.26.9(9 个,net/http / crypto/tls / net/textproto / encoding/asn1),
  `golang.org/x/net` v0.47.0 -> v0.61.0(4 个),`pion/dtls/v3` v3.1.2 ->
  v3.1.12(GO-2026-6165,解析畸形 ECDHE_PSK ServerKeyExchange 时 panic,可达路径
  `udp.session.Send -> dtls.Conn.Write -> handshake.Handshake.Unmarshal`).两个
  模块直接取最新已发布版本而非"刚好够"的下界,避免下个补丁版再改一次.修复后
  `govulncheck ./...` 报告 `No vulnerabilities found`
- **`scripts/workerpool_baseline.go` 未过 gofmt**:var 块错位一空格 + 文件末尾缺
  换行.该文件带 `//go:build ignore`,不属于任何 package,因此 lint 的 formatter
  扫不到它 -- 这正是它漂移的原因,只能靠手工保持

**CI 可靠性**:

- **`TestWorkerPoolDropPolicyRejectsWhenFull` 时序竞态**:池配置为 1 worker +
  深度 1 队列,handler 睡 100ms.测试连发两次 Submit 并期望第二次撞满队列,但唯一
  worker 可能在第二次 Submit 之前就把首任务取走,队列因而为空,Submit 成功返回.
  "队列已满"是概率而非事实,`-race` 的插桩改变了调度时序,把这个潜在竞态暴露成
  race job 的间歇性红灯.改为由 handler 信号确认它已接手首任务(此时队列必为空),
  再断言下一次 Submit 被拒;信号用非阻塞带缓冲发送,因为 `Stop` 之后还会 drain
  队列并再次调用 handler

**文档与产物说明**:

- **README 的静态链接声明缺少前提**:开篇写 "statically linked",对 Docker 镜像
  成立(Dockerfile 显式 `CGO_ENABLED=0`),但对装有 C 工具链的主机上的裸
  `go build` 不成立 -- 该场景下产物动态链接 `libc.so.6`,无法直接在 Alpine/musl
  上运行.云端实测两种产物对照后,已在两版 README 补上构建命令与前提说明

**云端验证**(120.76.44.233, Ubuntu 26.04, 2 vCPU, 1.6GB, Go 1.26.9, gcc 15.2.0):

9 步全部通过 -- 编译 59s / vet 23s / unit 37s / integration 81s / deploy 15s /
cover 44s (79.6%) / race 178s / 产物构建 2s / govulncheck 0 漏洞.6 组压力用例
`fail=0`.详见 `docs/reports/CLOUD-VALIDATION-261016-V10.md`.

**验证**:7 种模式(unit / vet / integration / deploy / cover / race / benchmark)
全部退出码 0,覆盖率 79.6%(门限 70%),`golangci-lint` 0 issues,
`govulncheck` 0 漏洞.修复后 `go.mod` 的 `go` 指令为 1.26.9.


### V9 Review - Correctness and CI Batch (2026-10-15)

V9 全量审查(逐项复现 - 写失败用例确认 - 修复 - 验证 - 提交)后的修复批次.详见
`docs/reports/PROJECT-REVIEW-261015-172344.md`.18 项缺陷已修复,8 项记入该报告的
第 4 节作为待办.

**高危(资源耗尽 / 不可用)**:

- **传输层忙等**:UDP/CoAP 的 DTLS accept 循环与 UDP read 循环在非关闭错误上直接
  `continue`,一个卡在错误态的 listener(EMFILE,被外部破坏)会把一个核心跑满 -- 实测
  UDP 在 250ms 内调用 `Accept` 10,298,963 次(约 4100 万次/秒),CoAP 9,541,655 次.
  新增 `shared.Backoff`(5ms 起,每次失败翻倍,上限 1s,成功后 Reset,与 `net/http`
  `Server.Serve` 同策略)并接入 4 个循环.同时把 `net.ErrClosed` 视为终止条件:此前
  socket 被外部关闭时 `s.closed` 仍为 false,循环永久重试,`Drain` 永远等不到退出
- **QUIC Drain 死锁**:quic-go 的 `Listener.Close` 不关闭已接受连接,`handleConn` 阻塞
  在 `AcceptStream`,而 `Drain` 先于 `CloseSessions` 执行,于是有活跃连接时 `Drain` 与
  `Gateway.Stop` 必然超时.改为 server 级 stop context
- **mTLS 客户端 CA 热更新不生效**:`tls.Config.ClientCAs` 在握手中读到的是启动快照,
  轮换 CA 必须重启进程.改为 `GetConfigForClient` 每次握手 Clone 并刷新
- **DTLS 连接泄漏**:`handleDTLSConn` 读错误后只做 map 删除,而 `dtlsConns.Delete` 又让
  `StopAccept` 看不到该连接,于是无人关闭它 -- 每个断开的 DTLS 对端泄漏一个 socket.
  改为在清理中先 `sess.Close`(`closeOnce` 保证幂等)
- **`tcp.Client` 并发不安全且泄漏连接**:`Connect/Send/Receive/Close` 直接读写 `conn`,
  无任何同步(`-race` 实报 DATA RACE),且重复 `Connect` 覆盖字段而不关闭旧连接.该类型
  经 `api.NewTCPClient` 对外导出.改为 RWMutex 仅保护指针交换,锁外拨号,装新后关旧
- **插件生命周期 panic 未隔离**:一个插件 `Start`/`Stop` panic 会打崩整个进程,且
  `StopAll` 不返回错误.新增 `safeStart`/`safeStop` + `errors.Join`,`StopAll` 改为返回
  error,Gateway 两处调用点检查并记录

**正确性与资源**:

- **CoAP RFC 7252/7641 一致性**:Empty CON(ping)此前回 2.05,规范要求回 RST;规范的
  RST(TKL=0,无 token)无法取消 observe 关系,因为代码按 token 关联而必须按 Message ID;
  Observe 选项长度为 0(Observe=0 注册)时不注册.既有用例固化了错误假设,已同步修正
- **Acceptor 记账**:容量限制先扣令牌再判容量,令牌桶拒绝时不归还容量槽位,导致 active
  计数被永久抬高;时钟回拨(NTP 校正)会把令牌桶抽干;速率低于 1/s 时桶容量不足 1 会拒绝
  所有连接.容量改为 CAS 先判再加,拒绝时归还槽位,负 elapsed 钳到 0,桶容量下限 1
- **gRPC-Web**:`content-type` 仅在分帧响应上设置;`framed` 按声明判定而非解析请求体;
  `grpc-message` trailer 按规范百分号编码(此前含 CR/LF 会破坏 trailer 块,可注入
  header);WebSocket 会话新增写超时 `WithWriteTimeout`(慢对端不再永久占住 goroutine)
- **MQTT**:`SetResumeSubs(true)`(默认不恢复订阅,重连后静默失联);入站 payload 做一次
  拷贝(paho 在回调返回后回收缓冲);`waitToken` 增加超时兜底
- **infra**:Bolt 打开默认 3s 超时(库文件被占用时不再永久挂起);熔断器 `Open` 态不再被
  迟到的 `Success` 提前闭合;cache `ttl < 0` 显式删除;pubsub 每个订阅者构造独立 msg
- **插件状态**:AutoBan 封禁键按 `SplitHostPort` 归一化(此前 `1.2.3.4` 记录,`1.2.3.4:5678`
  查询命中不了,封禁静默失效);Heartbeat 清扫给 `sess.Close` 加超时上限(单个卡死会话不再
  阻塞整轮清扫);Cluster topic 改在 RLock 下读取(`-race` 实报 DATA RACE);Persistence
  构造失败经 `Err()`/`Start()` 上报;SlowHandler 容忍 nil next

**工程与工具**:

- **worker pool 移入 shared**:TCP 的 pool 把 handler panic 转成 `ErrHandlerPanic` 却不记
  日志,运维只能看到会话消失.泛化到 `core.Session` 并移入 `internal/transport/shared`,
  委托 `shared.CallHandler`(recover + 日志 + 同一 sentinel);`tcp` 用类型别名保留
  `FullPolicy`/`WithWorkers`/`Policy*` 等公开名字
- **CI lint 红灯**:`golangci-lint` 报 4 个 staticcheck 问题(1 个 QF1001,3 个 QF1006),
  其中 3 个在本次审查之前就已存在,即 main 上 lint 一直是红的.已全部修复
- **CI 权限收敛**:顶层原本给所有 job 授予 `actions: write`,而 lint/security/docker-build
  都不需要.改为顶层 `contents: read`,仅 4 个上传日志的 job 单独声明 `actions: write`
- **`run_stress` 退出码**:此前只有初始化失败才返回非 0,运行结果完全不影响退出码
  (`-host <unreachable>` 输出 `sent=0 recv=0` 却退出 0).现在未发出消息,未收到回复或
  错误率超过 5% 时逐条输出原因并退出 1;顺手修掉失败场景的 `throughput=%!s(<nil>)`
- **`examples/tls_server`**:内嵌 PEM 是手工拼凑的(base64 中混入空格),启动即
  `x509: malformed certificate`,该示例从未可用过.改为运行时真正生成 ECDSA P-256 自签
  证书(SAN 覆盖 localhost 与 127.0.0.1),并补测试
- **文档字符集**:49 个 markdown 文件共 14420 处字符不符合"仅 ASCII + 中文"约定
  (全角标点,制表符画线,箭头,破折号,勾选符号,表情符号),已逐类映射归一化,复查为 0.
  代码块内的改动全部落在中文注释,未触碰任何 Go 语句或字面量
- **`.gitignore`**:忽略本地工具目录 `.workbuddy/`

### Security Hardening - Audit Batch (2026-09-02)

全量代码审计(传输/插件/运行时/LwM2M/CoAP/部署四路并行复核)后的修复批次:

- **插件生命周期接入 Gateway**:`AutoBan/RateLimit/Heartbeat/Cluster` 的 `Start/Stop`
  此前无人调用(Heartbeat/Cluster 挂载后完全失效,AutoBan/RateLimit 清理永不运行,计数随远端
  IP 无界增长).新增 `core.LifecyclePlugin` + `PluginRunner.StartAll/StopAll`:
  Gateway.Start 在服务器 accept 之前按优先级启动(失败回滚并拒绝启动),Gateway.Stop 在
  会话全部关闭后逆序停止;插件启动失败也会回滚已启动服务器
- **UDP/CoAP closed 会话重建**:插件/错误路径 `Close` 的明文 UDP 会话此前被同源后续报文
  静默复用(`touch()` 续命,Send 全失败,槽位永久占用);现在按状态即时回收并重建新会话
  (重新执行 OnAccept 门控)
- **CoAP observe(RFC 7641)**:同 (resource, remote) 重复注册替换旧关系(token 轮换不再
  累积),RST 按 token 注销观察者,CON 通知重传耗尽自动注销死订阅,每源 64/全局 4096
  订阅上限,空闲清扫豁免含观察者的会话(低频率通知不再静默断订阅),DTLS 会话拆除清理
  观察者
- **LwM2M 注册表治理**:默认 65536 注册上限(`WithMaxRegistrations`)+ 满员时节流过期清扫
  (`ErrRegistrationLimit`,此前 `SweepExpired` 仅测试调用);lifetime 钳制 30 天并拒绝
  溢出值(此前 `seconds*time.Second` 可 int64 溢出,注册永不过期);`Client.Register` 透传错误
- **DTLS 配置映射**:应用配置路径(`GetCertificate` 热载,`Certificates` 恒空)此前经映射后
  DTLS 服务端无证书,握手必然失败--现桥接 `GetCertificate`;`tls_client_auth` 此前被静默
  丢弃--现数值映射并在 UDP/CoAP Accept 后按 crypto/tls 策略强制校验(存在性 + 链验证,
  不依赖 pion 内部路径,v3.1.2 客户端认证流程不可靠的互操作限制见 SECURITY.md)
- **32 位崩溃**:LengthPrefixFramer 对 >=2^3^1 长度前缀先转 int 再比较,32 位平台负数绕过上限
  并在 `make` 崩溃--改为 uint32 域比较(单帧远程崩溃,IoT 类 32 位部署)
- **黑名单 IPv4-mapped 绕过**:精确条目与对端地址双向 IP 归一化(`::ffff:a.b.c.d`)
- **错误信息收敛**:HTTP/gRPC-Web 不再把内部/插件错误文本回显给客户端(通用状态文本 +
  服务端日志;gRPC-Web 业务 handler 错误仍经 grpc-message 透传,契约不变)
- **infra**:证书热载原子化(CA 失败不再留半更新状态),watcher 轮询间隔防 panic,
  熔断器 Open 态不再被迟到 Failure 无限延长窗口,PubSub dropped 计数随最后订阅者清理
- **MQTT**:Start 覆盖"未连接旧 client"前先 Disconnect(旧自动重连 goroutine 不再泄漏并与
  新连接互踢),Stop 与拨号窗口竞态(generation 校验,Stop 后不再被迟到拨号复活),
  Publish 防御性拷贝
- **文档校准**:SECURITY.md(DTLS/mTLS 支持与限制,插件生命周期,WS Origin 默认拒绝,
  黑名单动态添加不实声明移除,攻击面矩阵补订阅/注册治理行),PLUGIN.md(生命周期插件
  章节),docker-compose 不再向宿主机发布无鉴权 metrics/health 端口

### Zombie-Connection Reclaim (2026-08-31)

假链接/僵尸连接回收的可见性与回归保护:

- **新增 `sessions_reclaimed_total` 计数器**:TCP 读超时,UDP/CoAP 伪会话 TTL sweep,UDP/CoAP DTLS 读超时,WebSocket/gRPC-Web 读超时(PongTimeout)五个回收点全部埋点;`shared.IsTimeout` 统一判定 deadline 过期;sweep 仅在真正移除会话时计数(防与 DTLS 读超时双计);QUIC 由 quic-go 内部 idle 处理,不经过该计数(文档注明)
- **回收回归测试**:TCP 静默对端读超时回收(含计数断言+服务器存活验证),gRPC-Web WS 模式死对端 PongTimeout 回收(含计数断言)
- **QUIC idle 可配置**:新增 `WithQUICMaxIdleTimeout`(api 门面同步暴露),`TestQUICIdleReclaimsSilentPeer` 验证静默连接被 200ms idle 超时回收

### Test Completeness - Batch 4 (2026-08-31)

压力测试收尾(C10):

- **非 TCP 压力测试**:新增 UDP 持续吞吐(96.8k msg/s,0 失败 + 伪会话 TTL 回收断言),WebSocket churn(5k 循环/s,0 失败 + 会话零泄漏断言,linger(0) 拨号防 Windows 临时端口耗尽),HTTP 并发请求(115.7k req/s,0 失败 + 按请求会话不累积断言)
- 最终总覆盖率 **78.6%**(专项补测前 75.0%)

### Test Completeness - Batch 3 (2026-08-31)

接线与广度补强(C8/C9/B6):

- **连接上限/接受速率接线(C8)**:`MaxConnections`/`AcceptRate` 此前存在于 TCP/WebSocket/gRPC-Web/QUIC 选项并已接线 Acceptor,但**无任何 setter 与 api 门面**(配置不可达)--补 4 组 `WithMaxConnections`/`WithAcceptRate` + 8 个 api 包装;端到端测试验证 TCP 超限即断,TCP 速率突发拒绝,WS/gRPC-Web 503,QUIC 连接被服务端关闭
- **跨协议矩阵扩展(C9)**:插件链 OnMessage 矩阵从 3 协议扩到 **7 协议**(新增 CoAP/HTTP/gRPC-Web/QUIC);新增 OnAccept 拦截矩阵--黑名单插件在 7 协议上行为一致(TCP/QUIC 断连,UDP/CoAP 无响应,HTTP/gRPC-Web 403,WS 升级后关闭)
- **低覆盖项(B6)**:`cache.StartSweeper`/`Delete`,Blacklist CIDR 分支,`Persistence.SetLogger` 错误日志路径全部补测
- **非 staged Stop(B6)**:5 个传输绕过网关直接 Start/Stop 的路径(此前 0%)全部补测,Stop 后监听器不再接受连接

### Test Completeness - Batch 2 (2026-08-31)

历史 V7/V8 修复无回归测试 + Cluster 消息路径的专项补齐:

- **HTTP OnAccept 失败双 OnClose(V8 P2-4)**:拒绝插件 -> 403,已接受插件仅获一次 OnClose(链回滚),会话不泄漏
- **CloseAll 停机中途注册 + ctx 取消(V8 P2-1)**:Close 注册后继会话的 drain 循环测试;取消上下文立即中止且不关闭会话
- **gRPC-Web 错误路径 trailer 后停写(V8 P3-7)**:handler 错误响应仅含帧序列(grpc-status 13 trailer),严格帧解析器保证 trailer 后无多余字节
- **UDP getOrCreateSession OnAccept 失败分支**:拒绝插件 -> 无响应,无会话,OnClose 零调用
- **Cluster 消息路由**:`handleClusterMessage` 31.2% -> **100%**(畸形消息告警丢弃,wrong-topic/空载荷静默丢弃,Broadcast 错误告警)

### Test Completeness (2026-08-31)

针对协议连接管理与插件路径的测试缺口专项补齐(详见 `docs/reports/PROJECT-REVIEW-260809-091049.md` 后的逐项审计):

- **UDP DTLS 应用数据路径首次被测试驱动**:`handleDTLSConn` 覆盖率 0% -> 75.6%.旧 idle-timeout 测试空转通过(pion/dtls v3 惰性握手--客户端不写数据就永远不会有会话),已修复为"先写后断言";新增完整 DTLS echo + 断开回收测试;测试证书从 RSA 换为 ECDSA P-256(DTLS 1.3 拒绝 RSA 证书)
- **handler panic 回归测试**(V8 P1-2 修复此前无测试):UDP/CoAP/WebSocket 各新增 panic 隔离集成测试(对齐 TCP 已有测试)--首次调用 panic,后续请求正常服务证明进程存活
- **LwM2M update/discover 命令**:`handleUpdate` 0% -> 88.9%,`handleDiscover` 0% -> 100%;E2E 生命周期测试补 update 交换
- **插件后台清扫**:`RateLimit.sweep` 0% -> 84.6%,`AutoBan.sweep` 0% -> 100%(过期计数/封禁清理行为测试)

### Performance & Resource Improvements (2026-08-26)

- **RateLimit 分片锁**: 单一全局 Mutex 改为 32 分片(maphash 进程随机种子选择分片,防攻击者构造同分片碰撞放大锁竞争);8 核并行微基准 243ns -> 75.8ns(3.2x 扩展),单插件 TCP echo 延迟 53.5us -> 51.5us,每消息分配 10 -> 7
- **插件 per-message 键分配消除**: RateLimit/AutoBan 的 remote-IP 键首次计算后缓存到 session meta,每消息的 SplitHostPort 字符串分配降为每会话一次
- **DTLS 读缓冲从 64 KiB 降到 16 KiB(可配置)**: 每个 DTLS 连接持有独立读缓冲,旧默认下 1 万 DTLS 对端仅读缓冲即 ~640MB;新增 `WithUDPDTLSReadBufferBytes` / `WithCoAPDTLSReadBufferBytes`(api 门面同步暴露),明文 UDP 共享缓冲仍为 64 KiB
- **基准缺陷修复**: FullChain 系列基准的 `NewAutoBan(100)` 在第 100 条消息即断开连接导致 EOF 失败(并因 WS 基准无读超时而挂死整套基准)--阈值改为 `math.MaxInt` 以测量纯计数开销;WS 基准补 `SetReadDeadline`,失败快速报错
- 新增 `internal/plugin/benchmark_test.go`(RateLimit 单对端/多对端并行微基准)与分片隔离,键缓存单元测试

### CI Hardening (2026-08-09)

- **docker-build**: builds with the default Go module proxy
  (`--build-arg GOPROXY=https://proxy.golang.org,direct`) instead of the
  Dockerfile's China-oriented goproxy.cn, and now smoke-tests the image
  (runs the container and checks `/healthz`), validating the 0.0.0.0 bind.
- **permissions**: adds `actions: write` (required for `actions/upload-artifact`;
  with only `contents: read` every other scope, including actions, is denied
  and the log uploads 403).
- **lint fixes**: `worker_pool.stop` holds the submit lock across the drain
  (no empty critical section, SA2001); `MemoryMetrics.ObserveHistogram` /
  `MemoryLogger.append` append to the same slice (appendAssign);
  `Cluster.Close` returns `p.Stop()` (errcheck).
- **action upgrades**: `actions/checkout@v5`, `actions/setup-go@v6`,
  `actions/upload-artifact@v6`, `golangci/golangci-lint-action@v9` - all
  Node 24 runtimes (clears the Node 20 deprecation warnings).
- CI is green end-to-end: 7/7 jobs pass with no Node 20 annotations.

### V8 Audit Fixes (2026-08-09)

Post-V7 verification review (`docs/reports/PROJECT-REVIEW-260809-091049.md`).
All V7 fixes confirmed correct; 21 new findings fixed (1 P0 / 2 P1 / 7 P2 / 11 P3).

- **P0** - PubSub dropped counter is written under the exclusive lock (it was
  under `RLock`, causing `concurrent map read and map write` / process crash
  under drops).
- **P1** - `metricSessionManager.CloseAll` (the default gateway shutdown path)
  and the UDP plain readLoop now isolate session/handler panics like every
  other path.
- **P2** - `CloseAll` aborts on context cancellation; gRPC-Web `Stop` no longer
  hangs (Drain is a no-op, sessions are closed before waiting on goroutines);
  TCP high-water threshold clamped to >=1 for small queues; HTTP gates plugin
  `OnClose` on an `accepted` flag; `run_stress` reconnect mode and
  `TestStressTCPBurst` get the per-connection fixes; AutoBan semantics
  documented as a strict message-count limiter.
- **P3** - TLV 32-bit length overflow guard; worker-pool nil-session guard;
  `WithReadTimeout(0)` disables the timeout; CoAP retransmission keyed by
  `(remote, msgID)` with sends outside the lock and track-after-send;
  gRPC-Web error path stops writing after the trailer; `Cluster.WithTopic`
  locked; heartbeat sweep panic-isolated; PluginChain copy-on-write snapshot
  (zero per-message allocation); Helm NOTES port-forward fixed; benchmark
  resource gate fails open.

### V7 Audit Fixes (2026-08-09)

Full audit (V7, `docs/reports/PROJECT-REVIEW-260808-220224.md`) - all
43 findings fixed (2 P1 / 16 P2 / 25 P3).

#### P1
- **Acceptor sub-1 rate**: `AcceptRate < 1` (e.g. 0.5) previously rejected every
  connection because the token bucket was capped below one token; it now caps at
  `max(rate, 1)`.
- **NetworkPolicy egress**: `egress` now allows all destinations (empty peer
  rule); the previous `namespaceSelector: {}` only matched in-cluster pods and
  silently blocked outbound connections to external endpoints.

#### Reliability & metrics
- **Session metrics**: `SessionManager.Unregister` now reports whether a session
  was actually removed, so `sessions_closed_total` is no longer double-counted
  on graceful shutdown or inflated by no-op unregisters.
- **PluginChain**: callbacks run off the read lock against a snapshot, so a
  plugin calling `SetLogger`/`Append` from inside a hook no longer deadlocks.
- **Worker pool**: `submit` is serialized against `stop()` so no task is lost at
  shutdown.
- **Gateway.Stop**: non-staged servers and the final `CloseAll` get the
  configured `CloseSessions` deadline instead of hanging on a context without a
  deadline.
- **Handler panic isolation**: a new `shared.CallHandler` recovers user
  handler/responder panics across TCP/UDP/CoAP/QUIC/WebSocket/gRPC-Web, and
  `SessionManager.Broadcast`/`CloseAll` recover session panics - a panicking
  handler can no longer crash the process.
- **TCP write queue**: `WriteQueueHighWater` now enforces early backpressure
  (default 0.8) instead of being a no-op.

#### Transports
- **DTLS (UDP/CoAP)**: sessions get a `SessionTTL` read deadline so silent peers
  are reclaimed, and `OnClose` fires exactly once via a guarded cleanup even
  when `CloseSessions` races the connection handler.
- **CoAP**: dedup map records CON messages only (NON/ACK/RST no longer
  misclassify later CON requests); a NON observe registration receives the
  current value as an initial notification; CON observe notifications are
  retransmitted until ACKed/RSTed (RFC 7641/7252).
- **WebSocket / gRPC-Web**: pong-handler + per-read deadline implement the
  previously dead `PongTimeout` (dead-peer detection); gRPC-Web WebSocket mode
  gains `PingInterval`/`PongTimeout` options and a ping loop.
- **UDP/CoAP session IDs**: allocated before publication, removing a data race.
- **RawFramer**: zero-value writes default to the same 32 KiB cap as reads.
- **gRPC-Web trailers**: `SendTrailers` sets `content-type` so a trailer-only
  response is not labelled `text/plain`.

#### Plugins / infra
- **AutoBan**: a session that trips the ban threshold is closed immediately.
- **RateLimit**: only accepted requests are recorded, bounding per-key memory
  under flood.
- **PubSub**: drops are counted per topic (`Dropped`) instead of silent, and the
  topic key is removed when the last subscriber cancels.
- **MessageLog**: `Len` skips short keys; `Replay` runs callbacks outside the
  lock so a re-entrant `Append` cannot deadlock.
- **MemoryCache**: expiry is checked under the lock; new `StartSweeper` bounds a
  long-lived cache.
- **MemoryMetrics/MemoryLogger**: retained window is capped.
- **MQTT**: `Start`/`Subscribe` honor the caller context (abort on cancellation)
  with a hard timeout.

#### Plugin lifecycle API
- `NewHeartbeat(manager, timeout, interval)` and
  `NewCluster(nodeID, bus, manager, buffer)` take their parameters at
  construction; `Start() error` / `Stop() error` are now uniform across all
  lifecycle plugins (Heartbeat, Cluster, AutoBan, RateLimit).

#### Protocol codecs
- **LwM2M TLV is now OMA-compliant**: `EncodeTLV` emits proper "Resource with
  Value" records (type byte packs TT/identifier-width/length-width flags,
  variable-width 8/16/32-bit identifiers and 8/16/24/32-bit lengths), so the
  output is parseable by real LwM2M devices. `DecodeTLV` returns raw values;
  `DecodeTLVTyped(data, resolver)` resolves data types from the object model,
  matching how devices interpret TLV (types are not carried on the wire).

#### Scripts / deploy
- **run_stress**: cloud profile gets a real resource gate (skips under memory /
  load pressure); TCP clients get a per-receive read timeout so a half-dead peer
  cannot hang a run; burst mode uses one dedicated client per goroutine.
- **run_tests**: race mode strips a pre-existing `CGO_ENABLED` so race detection
  cannot silently no-op.
- **K8s/Helm**: deployments wire `SHARK_*` env via `envFrom` the ConfigMaps
  (previously dead config); the K8s ConfigMap keys are now valid env var names.
- **Docker**: the runtime image binds `0.0.0.0` so EXPOSE'd ports are reachable.
- **Helm**: the Deployment references the ServiceAccount via the
  `serviceAccountName` helper, fixing `helm install <release>` with a custom
  release name.

### V6.1 CI Hardening (2026-08-07)

- **Go toolchain 1.26.5**: Bump `go.mod` from 1.26.4 to 1.26.5 to fix
  GO-2026-5856 / CVE-2026-42505 (crypto/tls ECH privacy leak, fixed in
  go1.26.5). `govulncheck` now reports 0 called vulnerabilities.
- **golangci-lint v2.12.2**: The pinned v1.64.2 was built with Go 1.24 and
  could not analyze a Go 1.26 module; bump to v2.12.2 (0 issues locally).
- **Observability**: NewGateway installs a metrics decorator over the session
  manager, so every transport now emits `sessions_active` (gauge) and
  `sessions_accepted_total` / `sessions_closed_total` counters through the
  configured metrics backend. The Prometheus `/metrics` endpoint is no longer
  empty during normal operation.

### V6 Audit Fixes (2026-08-06)

#### Protocol Correctness
- **CoAP option encoding:** Extended option delta/length (nibble 14, value >= 269)
  is now encoded as `value-269` to match the decoder, fixing corrupted option
  numbers/values in that range. Reserved nibble 15 is rejected as malformed and
  option-number overflow is guarded.
- **CoAP observe:** `SendObserveNotification` encodes the Observe option with the
  same variable-length `encodeObserveSeq` used by the server notification path.

#### Concurrency / Lifecycle
- **Plugins (AutoBan/RateLimit/Heartbeat/Cluster):** Replaced the unsafe
  "reassign `sync.Once{}`" restart pattern with a `lifecycle` that owns a fresh
  WaitGroup per Start cycle. Concurrent Start/Stop no longer races on the Once
  struct and the WaitGroup is never reused across cycles (previously panicked
  "WaitGroup is reused before previous Wait"). Cluster Start/Stop fields are
  mutually excluded.
- **OnClose semantics:** Transports no longer call plugin `OnClose` for sessions
  that were never accepted (Register/OnAccept failure), eliminating double
  notifications after the plugin chain's rollback.
- **TCP worker pool:** Blocking submit also selects on the session context, so a
  peer disconnect unblocks the submitting goroutine instead of leaking it until
  the pool stops.
- **SetLogger:** PluginChain and Persistence SetLogger take a lock; Persistence
  reads go through a `loggerRef()` helper.

#### Functional Fixes
- **AutoBan:** `Record` is now called from `OnMessage` (it was dead code), so
  AutoBan can actually ban; `sweep` only removes counters idle for
  `banDuration` instead of resetting every non-banned IP each cycle.
- **QUIC:** Short `stream.Write` results are retried instead of silently
  dropping the remainder of the payload.
- **MQTT:** The package-global `clientFactory` is now a per-adapter field
  (reentrancy); `Start` double-checks under the lock so concurrent Starts
  discard a duplicate connection, and connect-failure paths disconnect.
- **MessageLog.Replay:** Skips keys shorter than the 8-byte sequence prefix
  instead of panicking.
- **HTTP:** `responseRecorder` no longer buffers the response body into memory.
- **SlowHandler:** Threshold <= 0 passes requests through instead of logging
  every request as slow.
- **BoltStore:** Operations run with the closed check under the read lock,
  closing the TOCTOU window that surfaced `bolt.ErrDatabaseNotOpen`.
- **Gateway:** Removed a redundant `started` reset in `Stop`; `Health` reports
  uptime only while started.

#### Test Infrastructure & CI
- `run_tests.go`: `-mode deploy` now validates only `./tests/deploy`;
  `-mode cover` covers production packages only; integration serializes with
  `-p 1`; JSON arg splicing cleaned up.
- Stress tests use SO_LINGER(0) clients and retry transient connects, fixing
  Windows ephemeral-port exhaustion that made integration/coverage flaky.
- CI race job uses the scripted runner so it covers `./tests` and
  `./tests/stress` like local validation.
- Deployment artifacts: Helm service declares `protocol: TCP`; `.gitignore`
  anchors `/shark-socket` so it no longer hides the Helm chart directory.

### V5 Audit Fixes (2026-08-06)

#### Crash / Concurrency
- **TCP/QUIC session:** `Close()` no longer closes `writeCh`; `Send()` and `writeLoop` select on `ctx.Done()`, eliminating the "send on closed channel" panic when `Send` races with `Close`.
- **TCP worker pool:** Never closes the task queue; workers terminate via a `done` channel and drain remaining tasks on `stop()`, so a blocking `submit` can no longer race `stop()` into a panic.
- **Gateway:** `Start`/`Stop`/`Register` are serialized by a shared mutex; `Start` rejects double start; `Stop` marks not-started up front (readyz reports not-ready during shutdown) and clears stale uptime.
- **SessionManager.Broadcast:** Sends to a snapshot instead of under the read lock, so a blocking or re-entrant `Send` cannot deadlock the manager.
- **PluginChain.OnAccept:** On failure, already-accepted plugins receive `OnClose` (reverse order) to release their resources.

#### Transport
- **UDP/CoAP session wedge:** Error paths now call `closeSession` (removing the session and unregistering it) instead of only `sess.Close()`, so a peer is not permanently wedged to a closed session.
- **CoAP:** Empty-payload requests (standard GET) now reach the Handler/Responder instead of being dropped by a `len(payload) > 0` gate.
- **gRPC-Web:** Raw request bodies without gRPC-Web headers are passed through untouched; only declared gRPC-Web requests are parsed as frames.
- **TCP/QUIC read timeouts:** New `ReadTimeout` option (default 5m) closes idle connections/streams, mitigating slowloris-style resource exhaustion.
- **Framers:** Zero-value `LengthPrefixFramer`/`LineFramer` fall back to a 1 MiB safe default instead of allowing up-to-4 GiB allocations or unbounded line growth.

#### LwM2M / Protocol
- **LwM2M Server.Write:** Invokes `OnWrite` after releasing the lock (re-entrancy deadlock fix) and no longer performs callback I/O under the global mutex.
- **LwM2M TLV:** 5-7 byte integers decode from all bytes (no truncation); 4-byte float32 values decode correctly instead of 0.0.

#### CI & Test
- All regression tests added pass under `go test -race`.

### V4 Audit Fixes (2026-06-26)

#### Plugin Lifecycle
- **Cluster:** Fixed double-close panic - `Stop()` now uses `sync.Once` for channel closure. `Start()` resets `stopOnce` and recreates `stop` channel on restart, matching RateLimit/AutoBan/Heartbeat pattern.

#### Transport Lifecycle
- **HTTP/WS/gRPC-Web/CoAP/UDP:** Reset `started` flag on listen failure (port conflict, permission denied, address resolution). Previously `started` remained true, permanently blocking recovery.
- **CoAP/UDP:** `session.Close()` now captures and returns close errors from the underlying DTLS/UDP connection.
- **HTTP:** Added `sync.Once` to `session.Close()` for double-close safety (matching all other session types).

#### Performance
- **SessionManager.Range():** Inlined map iteration under RLock - avoids full `[]Session` slice allocation on every `Range()` call. `CloseAll` and `Heartbeat.Sweep` use explicit `Snapshot()` for mutating operations.
- **CoAP dedup:** Replaced `fmt.Sprintf` per-message key with struct key `{remote, msgID}` - zero allocation on hot path.

#### Configuration
- **Env vars:** Added `SHARK_TCP_TLS_MIN_VERSION` and `SHARK_QUIC_TLS_MIN_VERSION` environment variable support.

#### CI & Observability
- **golangci-lint:** Pinned to v1.64.2 (was `latest`, producing non-deterministic builds).
- **Mosquitto health check:** Changed from `nc -z` to `mosquitto_sub` in CI service containers.
- **App:** Health/metrics HTTP `ListenAndServe` goroutines now tracked via `serveWG sync.WaitGroup`.

#### Documentation
- **V4 Review:** Added `docs/reports/PROJECT-REVIEW-260626-163000.md` - comprehensive 4-agent parallel audit (26 findings: 7H/12M/7L, 0 Critical).

### Comprehensive Quality Hardening (2026-06-26)

#### V1 Interface Cleanup (Breaking)
- Removed legacy `Store` interface (error-discarding `Save`/`Load`/`Delete`).
- Renamed `StoreV2` -> `Store` (all methods return errors + `List`/`Close`).
- Removed legacy `Persistence` plugin (V1). Renamed `PersistenceV2` -> `Persistence`.
- Removed `BoltStore` V1 wrapper methods (`Save`->`SaveV2` internally, etc.).
- Removed `api.PersistenceV2Plugin` and `api.StoreV2` type aliases.
- Updated all tests, benchmarks, and docs to use unified interfaces.

#### Concurrency & Safety Fixes
- **PubSub (Critical):** Fixed send-on-closed-channel panic - `Publish` now holds `RLock` during iteration+send, preventing concurrent `cancel()` from closing subscriber channels.
- **PrometheusMetrics:** Fixed label slice data race - `IncCounter`/`ObserveHistogram` now copy labels instead of reusing backing arrays.
- **Cache:** Fixed `Get()` TOCTOU race - expired items are no longer deleted inside `Get`; `Sweep` handles cleanup.
- **MessageLog:** `Replay()` and `Prune()` now hold mutex for concurrent `Append` safety.

#### Plugin Improvements
- **RateLimit:** Replaced fixed-window counter with true sliding window (`[]time.Time` timestamp slice), eliminating 2x burst vulnerability at window boundaries.
- **AutoBan:** Replaced global sweep (clear all every 30min) with per-IP ban expiry (`map[string]time.Time`). Expired bans auto-removed on `OnAccept` check. Sweep interval reduced to 5min.
- **Heartbeat:** Removed `sync.Once` from `Start()` - now supports restart after `Stop()` via `running` flag + channel recreation.
- **RateLimit/AutoBan:** `Stop()` now uses `sync.Once` to prevent double-close panic. `Start()` recreates stop channel if previously closed. Added `sync.WaitGroup` for goroutine tracking.
- **Cluster:** Consume goroutine now tracked in `sync.WaitGroup`. Added broadcast amplification warning docs.
- **Persistence/Cluster:** Replaced `log.Printf` with `core.Logger` field + `SetLogger()`.

#### Transport Layer Fixes
- **HTTP/WebSocket/gRPC-Web:** Added `s.closed.Store(false)` in `Start()` to enable proper restart after `Stop()`.
- **HTTP/WebSocket/gRPC-Web:** `Serve()` goroutines now tracked in `sync.WaitGroup`.
- **HTTP:** Added `Drain()` implementation (was no-op).
- **WebSocket/gRPC-Web:** Added `ReadTimeout`/`WriteTimeout`/`IdleTimeout` to `http.Server`.
- **TCP:** Unified `Stop()` order to `StopAccept->Drain->CloseSessions` (consistent with all other transports).
- **TCP:** `writeLoop` goroutine now tracked in `connWG`.
- **CoAP:** ACK send errors now logged via `rt.Logger().Warn`.
- **QUIC:** Handler errors now logged.
- **DTLS:** Extracted shared `DTLSConfig()` to `transport/shared/`, eliminating 22-line duplication between UDP and CoAP.

#### Gateway & Runtime
- **Gateway.Stop():** Added `stopMu` mutex for concurrent `Stop()` call protection.
- **Gateway rollback:** `Start()` failure rollback now logs individual `Stop()` errors.
- **PluginChain:** Panic recovery now uses configured `core.Logger` instead of `slog` directly.
- **PluginChain:** `safeAccept/safeMessage/safeClose` converted to methods with logger access.

#### Security Hardening
- **TLS MinVersion:** Now configurable via `TLSMinVersion` field (was hardcoded TLS 1.2).
- **TLS:** `parseTLSMinVersion` rejects versions below 1.2 (1.0/1.1 are insecure).
- **Health/Metrics HTTP:** Added `ReadTimeout`/`WriteTimeout`/`IdleTimeout` to internal servers.
- **allowedOriginChecker:** Added docs warning that `"*"` wildcard is dev-only.

#### Deploy & CI
- **CI Actions:** Fixed versions - `checkout@v4`, `setup-go@v5`, `golangci-lint@v6`, `upload-artifact@v4`.
- **CI:** Added `docker-build` job for image validation.
- **Dockerfile:** Added `ENV GOTOOLCHAIN=auto` for Go toolchain compatibility.
- **docker-compose:** Added Mosquitto config mount with security notes.
- **Helm:** Added `configmap.yaml` and `serviceaccount.yaml` templates.
- **K8s:** Added `namespace: shark-socket` to Deployment and Service for consistency.
- **.dockerignore:** Added `.env`, `.env.*`, `*.pem`, `*.key` exclusions.
- **.gitignore:** Added `.claude/` exclusion.

#### Documentation
- Updated `ARCHITECTURE.md` directory tree (application/->app/, infrastructure/->infra/, core file list).
- Updated `CONTRACTS.md` Protocol type, PluginRunner methods, file references.
- Updated `PLUGIN.md` sec.9 from `PersistenceV2Plugin` to `Persistence Plugin`.
- Updated `README.md` coverage number, feature matrix, plugin list.
- Added `ARCHITECTURE-ANALYSIS-260626.md` - comprehensive architecture analysis.
- Added `ARCHITECTURE-METHODOLOGY-260626.md` - design decisions and methodology.
- Updated `PROJECT-REVIEW-260626-V3.md` - V3 audit with 22 findings.
- Updated `PROJECT-REVIEW-260626-230000.md` - V2 audit with 31 findings.

### Protocol Test Coverage (2026-06-15)

#### Port Exhaustion Fixes (Windows)
- Added `WithClientLinger(0)` option to TCP client - sends RST on close, avoiding TIME_WAIT.
- Added `lingerTransport()` helper for HTTP/gRPC-Web benchmarks - `SetLinger(0)` via `DialContext`.
- Added `portCooldown()` to `run_benchmarks.go` - 3s wait between groups on Windows.
- Added `integration_helpers_test.go` to http/grpcweb/websocket packages - `init()` replaces `http.DefaultTransport` and websocket `DefaultDialer` with Linger(0) dialer.
- Fixed `TestGatewayTCPRestartKeepsSessionManagerUsable` - relaxed port reuse check for fast recycling.
- Fixed `parse_test_log_test.go` - updated timestamp assertion for millisecond-free format.
- All 27 network benchmarks now pass on Windows with zero port exhaustion failures.

#### Benchmark Structural Improvements
- Added `concurrentClientsForOS()` - platform-aware concurrency caps (Windows: 50, Linux: 500).
- Added `BENCH_MAX_CONNS` env var to override concurrency levels.
- Added read deadlines to UDP and WebSocket concurrent benchmarks.
- Unified HTTP client timeout to 5s across all single-connection benchmarks.
- Fixed gRPC-Web error handling - `io.ReadAll` and `Body.Close` errors now checked.
- Fixed `BenchmarkTCPEcho_Concurrent` - each goroutine now creates its own dedicated client (was shared, causing data corruption).
- Fixed `BenchmarkWSEcho_Concurrent` - each goroutine creates its own WebSocket connection.

#### Benchmark Architecture
- Extracted shared `echoHarness`, `echoHandler`, `newEchoHarness`, `getAddr` helpers.
- Added `newEchoHarnessWithPlugins` for plugin benchmarks.
- Added `skipIfShort()` for fast smoke-test mode.
- Refactored PayloadSize (6) and Concurrent (5) benchmarks - server created once, shared across sub-benchmarks (44->11 server creations).
- Refactored single-echo (6) and plugin (4) benchmarks to use `echoHarness`.
- Added PluginChain UDP/WS benchmarks (6 new: Blacklist/RateLimit/FullChain x UDP + WS).
- Added `BenchmarkQUICEcho_Concurrent` (documented with Skip for QUIC stream limitations).
- Fixed `payloadSizes` max: `65536->65507` (safe UDP datagram limit).

#### Orchestration
- `run_benchmarks.go`: added `-list` flag (17 groups), `-bench <name>` filter, extracted `allBenchmarkGroups()`.
- Replaced `validate.ps1` and `validate_deploy.ps1` with `run_tests.go -mode vet` and `-mode deploy`.
- Removed millisecond precision from all timestamp formats.

#### Documentation
- Updated README coverage to 74.9%.
- Updated CI workflow to use Go runner instead of PowerShell scripts.
- Updated all active documentation references to use `run_tests.go` commands.

### Review Fixes (2026-06-02 Evening)
- Fixed TCP RawFramer fuzz behavior for empty raw payload reads.
- Fixed LwM2M TLV fuzz tests after field rename and added value length validation.
- Fixed PowerShell validation scripts so native command failures fail CI correctly.
- Fixed QUIC benchmark response handling to read the server-initiated stream.
- Revalidated local Go tests, race, coverage, deploy static tests, and cloud Docker smoke tests.

### Comprehensive Review Fixes (2026-06-02)

#### Critical (5 fixes)
- Fixed data race on `allowance` in shared Acceptor (mutex for rate limiting).
- Fixed DTLS goroutine leaks in UDP and CoAP transports (track + close connections).
- Fixed unbounded memory leak in CoAP dedup map (periodic cleanup).
- Fixed QUIC double-invoke of OnClose/Unregister (LoadAndDelete guard).
- Fixed data race on `clientCAFile` in tlsutil CertCache.

#### High (7 fixes)
- Fixed CoAP Observe sequence encoding inconsistency (variable-length BE).
- Added BoltDB closed-state guard with sync.RWMutex.
- Added BulkDeleter interface + BoltDB batch delete for MessageLog.
- Fixed TCP accept loop spin on persistent errors (100ms backoff).
- Added nil guard to PluginChain.Append (filter nil plugins).
- Panicking plugins return ErrPluginPanic instead of silently succeeding.
- Added nil guards to Gateway.Register and SessionManager.Register.

#### Medium (8 fixes)
- Added sync.RWMutex to PluginChain for thread safety.
- Fixed TOCTOU in SessionManager.Register capacity check.
- Added double-start guard to TCP Server (atomic.Bool).
- WebSocket pingLoop closes session on failure (prevent zombie).
- Added Acceptor rate limiting to gRPC-Web direct + WebSocket modes.
- Added sync.Once to gRPC-Web session.Close().
- Fixed cert watchers to use app lifecycle context instead of Background.
- Fixed parseUint64 to return error instead of silently returning 0.

#### Deployment Hardening
- Docker: ca-certificates, wget, HEALTHCHECK, UID 1000, .dockerignore.
- K8s: namespace, ServiceAccount, ConfigMap, NetworkPolicy, PDB, HPA.
- Helm: _helpers.tpl, NOTES.txt, fsGroup, serviceAccountName.
- CI: golangci-lint + govulncheck jobs, .golangci.yml config.

#### Test Coverage
- Added WebSocket TLS (WSS) integration test.
- Added gRPC-Web TLS integration test.
- Added CoAP Observe E2E tests (4 test functions).
- Fixed data race in CoAP duplicate CON test handler.

#### Cloud Validation
- [OK] Server 1 (120.76.44.233): Go 1.26.3 build, test, race, Docker deploy, client test.
- [OK] Server 2 (47.110.238.85): Go 1.26.3 build, test, race, coverage, Docker deploy, concurrent 64KB.

### Coverage Improvements (2026-06-02)
- Core package: 0% -> 100% (17 tests)
- API package: 0% -> 77.4% (44 tests)
- Runtime package: 64.5% -> 88.2% (20 tests)
- UDP transport: 51.1% -> 71.5% (20 tests)
- MQTT adapter: 0% -> 59.1% (13 tests)
- Plugin package: 71.2% -> 79.3% (8 tests)
- LwM2M protocol: 67% -> 73.9% (5 tests)
- App package: 67.9% -> 73.4% (4 tests)
- CoAP transport: 67.6% -> 69.3% (4 tests)

### Latest Fixes & Enhancements
- MQTT integration: mosquitto broker in docker-compose, E2E tests pass on dual cloud servers.
- Fuzz testing: TCP framers, LwM2M TLV codec (11 fuzz tests total).
- Benchmark: gRPC-Web + QUIC benchmarks added (6 protocols covered).
- CoAP: message edge cases, option encoding, extended deltas (coverage 69% -> 76%).
- UDP/CoAP: session ID allocation fix (defer NextID until confirmed new session).
- Health/metrics: error propagation via App.ServeErrors().
- K8s: explicit ClusterIP type, protocol fields on service ports.
- CI: PR branch filter, cross-platform path separators, missing strconv import fix.
- Docs: ARCHITECTURE test matrix, SECURITY Docker hardening updated.

### Security (Phase 1)
- Added TLS certificate hot-reload via file watcher and `GetCertificate` callback.
- Added DTLS support for UDP transport using pion/dtls v3.
- Wired CoAP DTLS and UDP DTLS from JSON config and environment overrides.
- Fixed TCP sentinel error to use `core.ErrWriteQueueFull` instead of raw `errors.New`.

### Resilience (Phase 2)
- Added configurable write deadlines on TCP (30s default), QUIC (30s default), and WebSocket (30s default).
- Added token-bucket accept rate limiter with atomic max-connections counter (TCP, QUIC, WebSocket).
- Changed TCP worker pool default full-policy from `PolicyBlock` to `PolicyDrop`.
- Added write buffer high-water-mark threshold configuration on TCP.

### IoT Protocol Depth (Phase 3)
- Expanded LwM2M object model with `ResourceType`, `OperationMask`, `ObjectDefinition`, `ResourceDefinition`, and `DeviceInfo`.
- Added OMA LwM2M TLV binary codec (`[type][id(2B)][length(2B)][value]`) with encode/decode/round-trip support.
- Added LwM2M object registry with operation validation in `Write()`.
- Added `discover` command to LwM2M CoAP responder.
- Added CoAP option delta encoding/decoding per RFC 7252.
- Added CoAP Observe (RFC 7641) - `ObserverRegistry` with Register/Remove/Notify/RemoveBySession, wired to LwM2M `OnWrite` callback.

### Durable Persistence (Phase 4)
- Added `StoreV2` interface with error-returning `SaveV2`/`LoadV2`/`DeleteV2`/`List`/`Close`.
- Added BoltDB-backed `BoltStore` implementing `StoreV2`.
- Added `MessageLog` - durable append-only message log with auto-incrementing sequence numbers, replay, and prune.
- Added `PersistenceV2` plugin using `StoreV2` with `OnMessage` hook appending to `MessageLog`.
- Added `SessionStore` - JSON session snapshot save/load/list/delete for restart recovery.

### Defect Fixes (2026-06-02 Comprehensive Review)

#### Critical Fixes
- Fixed data race on `allowance` field in shared Acceptor (added mutex for rate limiting).
- Fixed DTLS goroutine leaks in UDP and CoAP transports (track and close connections on shutdown).
- Fixed unbounded memory leak in CoAP dedup map (periodic cleanup goroutine).
- Fixed QUIC double-invoke of OnClose/Unregister (use LoadAndDelete for idempotency).
- Fixed data race on `clientCAFile` in tlsutil CertCache (added mutex to SetClientCA).
- Added nil guards on exported `Gateway.Register` and `SessionManager.Register`.

#### High Priority Fixes
- Fixed CoAP Observe sequence encoding inconsistency (variable-length big-endian encoding).
- Added BoltDB closed-state guard with mutex (operations return ErrClosed after close).
- Added BulkDeleter interface and BoltDB batch delete for MessageLog bulk operations.
- Fixed TCP accept loop spin on persistent errors (added 100ms backoff).
- Added nil guard and validation in PluginChain.Append.
- Panicking plugins now return ErrPluginPanic instead of silently succeeding.

#### Deployment Hardening
- Added ca-certificates, wget, and HEALTHCHECK to Dockerfile.
- Fixed Docker UID to 1000 for K8s compatibility.
- Added .dockerignore to reduce build context.
- Fixed docker-compose YAML ambiguity and added tmpfs mounts.
- Added K8s namespace, ServiceAccount, ConfigMap, NetworkPolicy, PDB, HPA manifests.
- Added Helm _helpers.tpl and NOTES.txt templates.
- Added golangci-lint and govulncheck CI jobs.

#### Test & Coverage
- Added WSS/TLS tests for WebSocket transport.
- Added TLS tests for gRPC-Web transport.
- Added CoAP Observe E2E tests (4 test functions).
- Fixed scripts: `./api/...` to `./api`, removed duplicate test from validate.ps1.
- Added comprehensive project review document.

## v0.1.0 - 2026-05-30

Release candidate for the redesigned Shark-Socket runtime gateway.

### Added

- Core runtime contracts for sessions, servers, codecs, plugins, observability, and staged shutdown.
- Gateway runtime composition with shared session management, global plugin execution, duplicate protocol rejection, readiness, health snapshots, rollback on failed start, and staged stop.
- TCP transport with length-prefix, line, fixed-size, and raw framers, a client helper, worker pool policies, runtime plugin integration, and shutdown cleanup.
- UDP transport with remote-address pseudo-sessions, TTL sweeping, runtime plugin execution, and shutdown cleanup.
- HTTP transport with plain router mode, session/plugin handler mode, request body limits, and per-request cleanup.
- WebSocket transport with binary message handling, origin checks, serialized writes, ping loop, runtime plugin execution, and shutdown cleanup.
- CoAP transport with message parse/marshal, CON ACK responses, responder hooks, pseudo-sessions, TTL sweeping, and runtime plugin execution.
- LwM2M in-memory lifecycle/resource model with registration, update, deregistration, lifetime expiry, resource read/write, and CoAP text-command binding.
- QUIC transport using `quic-go`, TLS-required startup, bidirectional stream request/response flow, runtime plugin execution, and shutdown cleanup.
- gRPC-Web transport with direct HTTP mode, binary frame parsing, framed data responses, grpc-status trailer frames, WebSocket mode, max message size limits, origin checks, runtime plugin execution, and session cleanup.
- Plugin ecosystem covering blacklist, rate limit, heartbeat, persistence, autoban, slow handler logging, and cluster pub/sub broadcast.
- Infrastructure primitives for in-memory cache, store, pub/sub, circuit breaker, in-memory observability, Prometheus metrics export, and OpenTelemetry tracing.
- Deployment baseline for Docker, docker-compose, Kubernetes, and Helm, including security contexts, resource requests/limits, liveness/readiness probes, and configurable Helm ports.
- Compile-checked multi-protocol example and examples documentation for TCP, WebSocket, CoAP/LwM2M, Prometheus metrics, and OpenTelemetry tracing.
- Validation tooling for normal, race, deploy, scripted unit/integration/benchmark/all test runs, JSON logs, parsed reports, fuzz smoke tests, and benchmark baselines.

### Validation

- `powershell -ExecutionPolicy Bypass -File .\scripts\validate.ps1`
- `go run scripts/run_tests.go -mode all -timeout 5m`
- `powershell -ExecutionPolicy Bypass -File .\scripts\validate.ps1 -Race`

### Known Scope

- Docker, Kubernetes, and Helm render checks are run when those tools are installed, and otherwise recorded as explicit skips by `scripts/validate_deploy.ps1`.
