# ADR-019:配置热重载限定在不动 listener 的子集

状态:已采纳

## 背景

对比报告 (`COMPARISON-261018-OSS-PEERS.md`) 第 4.3 节的 C6 指出: 配置一次加载,
只有证书走 `infra/tlsutil/watcher.go` 热重载. 改限流阈值、黑名单或日志级别都要
重启进程. 这与"确定性生命周期"的运维叙事不匹配 -- 一个在事故中需要立刻收紧限流
的运维, 手边唯一的工具是重启, 而重启会掐断全部在线会话.

问题不在于"没有热重载", 而在于**没有任何地方声明过哪些字段可以不重启生效**, 于是
每个字段都只能按最保守的方式处理: 重启.

## 决策

1. **热更子集显式且封闭**: `log_level`、`blacklist`、`rate_limit`、`shutdown_timeout`.
   判据只有一条: 新值能否在不重建 socket、goroutine 或 handler 的前提下生效.
2. **监听类字段一律不支持**: `health_addr`、`metrics_addr`、`protocols`(地址/路径/
   模式/TLS/CORS). 一个文档里只要含这类变更, **整个重载被拒绝**
   (`ErrListenChangeRequiresRestart`), 热字段一个也不应用.
3. **先验证后应用**: 校验(`Config.Validate` + 日志级别解析 + 黑名单严格解析)全部
   通过之后才写任何状态. 写阶段只有原子 store, 因此不存在"应用到一半失败".
4. **应用的是活对象, 不是新对象**: `Blacklist.Replace` 原子换一个不可变集合
   (`atomic.Pointer[blacklistSet]`), `RateLimit.SetLimit` 写两个 `atomic.Int64`,
   日志级别写 `slog.LevelVar`. 插件实例、插件链、会话表都不重建.
5. **插件链的构成在启动时固定**. 只在配置里写了 `rate_limit` 才装限流插件; 重载
   时"从没有到有"或"从有到没有"同样返回 `ErrListenChangeRequiresRestart`.
   黑名单插件**始终**在链上(空集合 = 空操作), 因为它是最可能需要在事故中开启的
   那一项, 而"需要重启才能开启"等于没有.
6. **重载入口是 `App.Reload(cfg)` / `App.ReloadFile(path)`**, 不触碰插件生命周期:
   `Gateway.Start` 已经调过 `Plugins().StartAll()`, 重载路径若再走一遍入口就会
   double-start (限流的清扫 goroutine 会有两条).
7. **信号是 SIGHUP**, 与 SIGINT/SIGTERM 分开成独立 channel: 重载失败只记日志,
   进程继续用旧配置服务, 不会被误当成停机请求.
8. **日志口子先补齐**: `app.New` 之前不注入 Logger, 网关拿的是 `NopLogger`.
   "热更日志级别"在没有级别概念的 logger 上是空操作, 所以先引入
   `core.LeveledLogger`(内部 `slog.LevelVar`)并在 `app.New` 注入.

## 原因

- **为什么拒绝是"整份拒绝"而不是"应用能应用的, 警告其余".** 运维改配置文件时
  很少一次只改一个字段; "改端口 + 顺手加一条黑名单"是最常见的组合. 部分应用会让
  进程处于一种没人能推理的状态: 端口没变但黑名单变了, 而运维手里只有一条"重启
  后生效"的心理预期. 整份拒绝的代价是一次明确的错误日志, 收益是"配置要么全生效
  要么全不生效"这条可以依赖的性质.
- **为什么 `shutdown_timeout` 算热更.** 它不持有任何资源, 只在停机那一刻读一次;
  把它列进"需要重启"会逼着运维为了改一个停机宽限期去做一次真正的停机. 主程序因此
  在停机时向 `App` 要当前值 (`ShutdownDuration()`), 而不是用启动时捕获的那份.
- **为什么限流参数改了不重置计数.** 计数在窗口内保留: 收紧立即生效(下一条消息就
  被拦), 放宽则随旧时间戳老化逐步放开. 反过来(重载时清零)等于给客户端一个"等到
  重载就能翻倍"的漏洞, 与运维改限流的意图相反.
- **为什么 `SetLimit` 里非正值回落到 1/1s 而不是"关闭限流".** "关掉限流"需要把
  插件从链上摘掉, 那是插件链重建; 用一个魔法值表达"关闭"会让
  `rate_limit: {per_second: 0}` 在配置里看起来像"不限", 实际是"每秒 1 条".
  真要关闭, 删掉 `rate_limit` 段并重启, 语义清楚.
- **为什么 `LeveledLogger` 用 `slog.LevelVar` 而不是换 handler.** 组件持有的是
  `core.Logger` 接口值, 换 handler 意味着替换接口值本身, 而接口值被八个传输、
  运行时、插件链各自存了一份 -- 要么给每处加锁, 要么让日志热路径读一个可变字段.
  `LevelVar` 是 slog 为此提供的唯一正确原语: 每个 record 现读, 无锁.

## 后果

- **新连接立即生效**: 黑名单在 `OnAccept` 读集合, 重载后的下一次 accept 用新集合.
  有测试 (`TestReloadBlacklistAppliesToNewConnectionsOnly`) 用同一个操作先成功
  后失败来证明: 重载前建立连接能通, 重载后同样的连接被立即关闭.
- **存量连接不受影响**: `OnAccept` 不会对已接受的会话重跑, 所以重载不会把在线
  会话踢掉. **一处例外要讲清**: 限流是消息级策略, 新参数对存量连接的下一条消息
  生效 -- 否则"改限流"这件事本身就没有意义. 证书热更同样是"新连接生效".
- **失败保留旧配置**: 解析失败、校验失败、监听变更三种失败路径下, 运行配置、
  黑名单、限流参数、日志级别全部保持原值, 只多一条 error 日志
  (`config reload rejected, keeping the running configuration`).
- **黑名单在 `Register` 之后判定**: 传输层的顺序是
  `Sessions().Register` -> `Plugins().OnAccept`, 所以被拉黑的来源会短暂占用一个
  会话名额, 随后在 deferred 清理里归还. 不是 DoS 面(拒绝在整个 accept 周期内完成),
  但审计"被拒连接是否占用过容量"时要知道这一点.
- **`app.New` 现在总会注入日志**: 之前网关默认 `NopLogger`, 运行时的错误
  (server failed / cert reload failed) 无人可见. 默认级别 `info`, 输出到 stderr.
- **新增配置字段**: `log_level` / `blacklist` / `rate_limit{per_second,window}`,
  对应环境变量 `SHARK_LOG_LEVEL` / `SHARK_BLACKLIST` / `SHARK_RATE_LIMIT_PER_SECOND`
  / `SHARK_RATE_LIMIT_WINDOW`. 四项都有零值默认, 既有配置文件不需要改.

## 重新评估条件

- 若出现"必须不重启换监听地址"的部署 (例如多租户按需开端口), 正确做法是
  引入**可增删的子监听**(每个协议实例可独立 Start/Stop), 而不是放宽 `Reload`;
  当前 `Gateway.Register` 明确禁止 `Start` 之后注册, 这条约束正是"确定性生命周期"
  的实现.
- 若黑名单条目数涨到十万级, `Replace` 每次重载会重建整张表(当前线性). 届时应改成
  分片或增量 diff, 并考虑把重载做成后台任务, 避免 SIGHUP 处理阻塞在 O(n) 解析上.
- 若需要"重载后主动断开已匹配黑名单的存量连接", 那是另一个开关(当前设计明确不做):
  它会与"存量连接不受影响"直接冲突, 必须先有运维语义上的共识.

## 关联文档

- 报告: `docs/reports/COMPARISON-261018-OSS-PEERS.md` 第 4.3 节 C6
- 架构: `docs/architecture/TRANSPORT.md`(准入与插件链), `docs/DEPLOYMENT.md`(配置项)
- 相邻决策: ADR-017 (准入控制, 限流与黑名单在同一链路), ADR-003 (分阶段优雅关闭,
  `shutdown_timeout` 的消费者), ADR-004 (插件 panic 隔离, 重载不改变链的 recover 语义)
