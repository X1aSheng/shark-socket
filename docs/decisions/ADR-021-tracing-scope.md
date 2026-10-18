# ADR-021:不产生 span,把 Tracer 定位为嵌入方的扩展点

状态:已采纳

## 背景

对比报告 (`COMPARISON-261018-OSS-PEERS.md`) 的 D4: README 声明
"metrics, traces, and logs on critical paths", 而全仓生产代码里 `Tracer().Start`
的调用点是 **0 个**. 现状比"少一个埋点"更彻底:

- `core.Tracer` / `core.Span` 接口、`core.NopTracer`、`observability.NewOpenTelemetryTracer`
  适配器、`runtime.WithTracer` 选项、`Runtime.Tracer()` 访问器**全都在**
- `internal/app` 从不配置 tracer, 8 路传输也从不调用它
- 于是 `WithTracer` 是唯一入口, 而它只对"自己组装 Gateway"的嵌入方可见

也就是说: 一个带 OpenTelemetry 依赖、有独立文档章节、有测试的子系统, 在默认使用
路径上永远返回 nop, 且运行时自己不产生任何 span. 这不是"未完成", 是**没想清楚
span 要回答什么问题**就先把接口建好了.

## 决策

1. **运行时不在消息路径上产生 span.** `handler_duration_seconds{protocol}` 已经
   回答"有多慢", 而且是聚合的、零分配的; 每消息一个 span 用约 1000 倍的代价回答
   同一个问题, 还带基数风险.
2. **运行时也不在会话生命周期上产生 span.** 会话可以存活数天, 一个到会话结束才
   `End()` 的 span 会在导出器里占内存数天; 而"连接建立有多慢"这类问题用一条指标
   就能答, 不需要 span 的一次性传参成本.
3. **`Tracer` 保留为嵌入方的扩展点**, 而不是框架的埋点设施. 定位写进接口注释与
   文档: 你的 handler/插件想给下游调用加 span, 从 `Runtime.Tracer()` 取一个.
4. **README 与架构文档按事实改写**: 不再声称"critical paths 上有 trace"; 明确
   写出运行时不发 span (本 ADR), 以及指标覆盖了哪些临界路径.
5. **把扩展点的唯一保证变成测试**: `TestGatewayInjectsTheConfiguredTracer` 断言
   `WithTracer` 的值确实经 `UseRuntime` 到达传输, 且 `Start` 会分派到它;
   `TestGatewayDefaultsToNopTracer` 断言默认值非 nil.

## 原因

- **单进程网关里, 单个函数的 span 是一条更慢的指标.** trace 的价值在跨边界: 把一次
  请求在多个服务、多个线程、多个 goroutine 之间的耗时串起来. 本项目没有出站调用,
  唯一能"跨"的是 `Session -> 读循环 -> worker -> handler`, 而这段的时间边界已经由
  直方图和 `tcp_task_queue_full_total` 各自负责. 再包一层 span 只得到一个和指标
  同值不同价的数.
- **接口里没有 context, 所以无法传播.** `core.Session`、handler
  (`func(Session, Message) error`)、`core.PluginRunner` 都不带 `context.Context`.
  即使运行时在边界起了 span, 也没有通道把它传给用户 handler 或插件, 于是所有 span
  都是无父的根 span -- 那不是 trace, 是一组带时间戳的日志. 要让 trace 真正成立,
  得给这三个接口加 ctx, 那是破坏性的 API 变更, 收益要先被证明.
- **会话级 span 在长连接上是反模式.** 导出器按 span 缓冲, 一个三天不收尾的 span
  就是三天的常驻内存, 且无法中途观察. 会话级的目标(连接建立耗时、插件链耗时)
  本来就有界, 用指标表达更贴.
- **为什么不直接删掉 Tracer.** 它已经是一个公开的扩展点, 且对嵌入方有真实用途:
  用户 handler 里调数据库/下游服务时, 需要一个与进程内其他 span 同源的 tracer.
  删掉是把可用的东西拿走; 留着一个"运行时不用、嵌入方能用"的能力, 并把这件事写清楚,
  才是诚实的最小集.
- **为什么必须补测试.** 因为没有任何调用点, `WithTracer` 的接线断了也不会有人发现
  -- 没有测试的扩展点等于没有扩展点.

## 后果

- **README 的宣称变小了, 但可验证.** 之前是"关键路径有 trace"(不成立), 现在是
  "消息路径有指标、有结构化日志, tracer 作为扩展点供嵌入方使用, 运行时不发 span".
- **不引入每消息开销.** 消息路径的成本保持 ADR-020 记录的水平
  (`CallHandlerObserved` + Prometheus 101 ns / 0 alloc); D4 没有改动它.
- **代价是"开箱即用的 trace 视图"不存在.** 想在 Jaeger 里看到 shark-socket 的
  请求链路, 需要自己埋点. 这是有意的取舍, 写在 `OBSERVABILITY.md` 第 4 章.
- **未来的埋点位置已经想好**: 若真要做, 先给 `Session`/handler/`PluginRunner` 加
  ctx, 再做**按连接的** span (`connection.accept` 带 `protocol`/`peer`), 不做按消息的.
  判据是"这条 span 的父子关系有没有第二个节点", 没有就不要建.

## 重新评估条件

- 若网关获得出站能力 (转发到上游、跨节点路由、集群内代理), trace 就有了真正的跨边界
  节点, 应按连接或按请求建 span, 并先把 ctx 通到 handler 与插件.
- 若用户反馈"无法把 handler 里的下游调用串进一条 trace", 说明扩展点的 ctx 缺失已经
  成为实际障碍, 此时做 ctx 穿通 (破坏性变更) 比继续保留无 ctx 的 Tracer 更合理.
- 若 OpenTelemetry 的 `Span` 支持在尾部采样下低开销地丢弃 (例如 sampler 在
  `Start` 之前拒绝), 可以让消息级 span 的成本降到可接受, 届时重新比较它与直方图.
- 若有人真的需要"连接建立耗时"的分布, 应当先加 `accept_duration_seconds{protocol}`
  直方图 (复用 ADR-020 的机制), 而不是加 span.

## 关联文档

- 报告: `docs/reports/COMPARISON-261018-OSS-PEERS.md` 第 4.4 节 D4
- 架构: `docs/architecture/OBSERVABILITY.md` 第 4 章 (Tracer 接口与实现)
- 相邻决策: ADR-020 (指标面的真实性 -- 本文的"span 不如直方图"与它同源),
  ADR-017 (一处计数两维读法)
