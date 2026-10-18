# ADR-020:指标面用真直方图,并把协议作为 label

状态:已采纳

## 背景

对比报告 (`COMPARISON-261018-OSS-PEERS.md`) 第 4.4 节的三项是同一件事的三个面:

| 项 | 现象 |
|----|------|
| D1 | `core.Metrics` 定义了 `ObserveHistogram`, 全仓生产代码调用点 **0 个** |
| D2 | 导出器只写 `_count`/`_sum`, `# TYPE` 却声明 `summary` |
| D3 | 所有指标都没有 label, 五个传输混在一个计数器里 |

合起来看: 项目对外宣称"关键路径内置 metrics", 但关键路径上一次延迟也没记;
记了也导不出分位数 (`summary` 无 quantile 时连单实例 P99 都算不出, 更不能跨实例
聚合); 而且就算导出了, 也分不出是哪个协议变慢.

README 里的 P99 全部来自压测工具, 线上无法自证. 这不是"少一个指标", 是**指标面
不能回答运维唯一会问的问题**: 现在慢的是谁, 慢了多少.

## 决策

1. **`handler_duration_seconds` 在 handler 边界记一次, 而不是每个传输各记一次.**
   边界是 `shared.CallHandlerObserved`: 计时包住**无 panic 隔离之后**的用户 handler
   调用, 因此数值含 handler 自己的 `Send`, 不含运行时开销, 跨协议可比.
2. **`messages_total` 在同一处计一次**, 与直方图同源同 label. 它度量的是"投递给
   handler 的流量", 不是"成功处理的流量" -- 失败与 panic 也计入, 否则丢包会表现为
   流量下降, 正好掩盖事故.
3. **协议是 label, 不是指标名前缀.** 取值为 `core.Protocol` 的封闭枚举 (8 个),
   基数上界确定, 不构成基数爆炸; 而"把协议编进名字"会让跨协议汇总退化成一次正则.
   报告 D3 的建议与此一致.
4. **导出器输出真 `histogram`**: 每个 bucket 一个 `_bucket{le="..."}`, 累计值,
   `+Inf` 桶等于 `_count`. 这是 `histogram_quantile()` 唯一能读的形态.
5. **bucket 梯度是秒, 1µs 到 10s, 22 档**; 其它单位必须 `RegisterHistogram` 显式
   声明. 默认梯度不是 Prometheus 的 `DefBuckets` (从 5ms 起): 本项目的 echo 路径
   在微秒级, 用 5ms 起步的梯度会让所有样本落进第一个桶, 等于没测.
6. **序列键用结构体, 不用拼接字符串.** `name + "{" + join(labels) + "}"` 每调用
   一次分配一个字符串再加一份 label 拷贝; 数据面每消息调两次, 10 万 msg/s 就是
   每秒 20 万次纯记账分配. 结构体键 (name, label0, value0, rest) 可比较, 用调用方
   已有的字符串构造, 零分配. 超过一对 label 时退回拼接形式, 是"慢但正确", 不是
   "快但串号".
7. **指标不是无代价的, 所以代价要有门禁.** `BenchmarkCallHandlerObserved*` 与
   `BenchmarkObserveHistogram/IncCounter` 进 `scripts/benchgate` 的固定套件.
8. **准入/回收计数也带 `protocol` label**, 与直方图同一个 label 名与同一套取值.
   它们是"多于一路传输会写"的指标, 判据是"需要一个不按协议拆分的视图", 不是
   "只有一路在调用". 只有一路能写的指标 (`tcp_task_queue_full_total`,
   `tls_handshake_failed_total`) 保留协议在名字里 -- 把它们也加 label 只会得到
   一个恒定值的 `protocol`, 却让看板多一次 join.
9. **跨传输计数器只经 `shared.IncProtocolCounter` 递增.** 该函数把 nil 检查与
   `core.ProtocolLabel` 一起封装. 手写 `IncCounter(name)` 少一个 label 时没有任何
   编译错误, 结果是一条静默缺失的序列 -- 未计数的拒绝与没有拒绝在抓取结果里长得
   一样, 这正是 D3 要消灭的形态.

## 原因

- **为什么计时点选在 handler 边界, 而不是 `readLoop` 或 `Submit`.** 那两处测的是
  队列与调度, 会被 worker 数、队列深度、负载策略主导, 换一个 pool 配置就不可比;
  而"用户回调有多慢"是所有传输共有、且唯一能跨协议对齐的口径. 队列等待本身是另一个
  问题 (那是 `tcp_task_queue_full_total` 与池的伸缩曲线), 混进来会让两个故障源
  给出一个数.
- **为什么不引入 Prometheus client_golang.** 该库的正确用法是调用方持有预创建的
  `Counter`/`Histogram` 句柄 (每消息只剩一次原子加). 引入它意味着 `core.Metrics`
  从"名字 + label"接口改成"句柄"接口, 八个传输、插件、运行时全要改持有方式;
  而本项目的指标集合是**固定枚举**, 用结构体键 + 一把锁已经做到零分配, 每消息
  98 ns (见下). 收益不足以支付接口重构.
- **为什么保留全局锁而不分片.** 观测路径的并行基准是 87 ns/op (16 线程), 单点
  串行上限约 1150 万次/秒, 而每条消息只观测两次 -- 相当于 570 万 msg/s 的网关
  才可能撞到这堵墙. 分片会把导出路径从"遍历一张表"变成"遍历 N 张表再归并",
  在撞墙之前是纯复杂度.
- **为什么 `+Inf` 桶必须写 `_count` 而不是累加梯度.** 超出最后一档的样本只记在
  `count`/`sum` 里. 用梯度累加值写 `+Inf` 会让 `bucket{le="+Inf"} != _count`,
  Prometheus 判定该直方图不自洽, `histogram_quantile()` 直接返回 NaN -- 实现时
  正是这里错了一次, 被 `TestPrometheusHistogramImplicitInfBucket` 抓住.
- **为什么 D3 不是"给指标加个参数"这一行.** 迁移时发现准入计数并不完整:
  WebSocket 升级被拒, QUIC 连接被拒, gRPC-Web 的两个入口 (direct 与 WebSocket
  模式) 被拒, 都直接返回 "server busy" 而不计数. 也就是说**一台拒绝所有连接的
  服务器, 在抓取结果里与空闲服务器完全一样**. label 迁移只是让这些已有的洞更容易
  看见; 洞本身是与 label 无关的缺陷, 一并补上 (新增 4 处计数, 全部 15 个准入/
  回收调用点统一经 `shared.IncProtocolCounter`).
- **为什么用 `ProtocolLabel` 返回预构造的 label 对.** 每次调用 `[]string{"protocol",
  string(p)}` 会在消息路径上分配一次; `incProtocolCounter` 每次拒绝都调用它, 而
  拒绝风暴正是它要度量的事件. 枚举封闭, 8 个切片是常量, 枚举外的值退回现构造
  (冷路径, 正确性优先).
- **为什么 `RegisterHistogram` 在首次观测后拒绝注册.** 换梯度需要把已有样本重新
  分桶, 而导出器为了 O(buckets) 不保留原始样本. 静默换梯度会得到一个"前 1000 条
  按旧梯度, 之后按新梯度"的直方图, 分位数是错的且看不出来.

## 后果

- **实测代价** (Windows / Ryzen 7 8845HS, `benchtime=300000x`, 3 轮稳定):

  | 场景 | ns/op | B/op | allocs/op |
  |------|-------|------|-----------|
  | `CallHandler` (基线) | 4.1-5.5 | 0 | 0 |
  | `CallHandlerObserved` + NopMetrics | 14.8-17.5 | 0 | 0 |
  | `CallHandlerObserved` + Prometheus | 97-98 | 0 | 0 |
  | `ObserveHistogram` (单点) | 46-47 | 0 | 0 |
  | `IncCounter` (单点) | 43-48 | 0 | 0 |
  | `ObserveHistogram` 并行 16 线程 | 87-88 | 0 | 0 |

  对比改造前: `ObserveHistogram` 95-106 ns / 2 allocs, `IncCounter` 105 ns /
  3 allocs, handler 边界合计 235-272 ns / 5 allocs. 结构体键把分配清零, 时间
  降到约 40%.
- **默认配置也要付一点**: 不配导出器的库用户 (NopMetrics) 从 5 ns 涨到 ~15 ns,
  因为要构造 label 并调用两次空实现. 这是"指标缺失"换来的固定成本, 已写进门禁.
- **`sessions_active` 仍是全局 gauge, 没有 `protocol` label.** 按协议拆 gauge 需要
  在每次注册/注销时遍历会话表 (O(n) 每次, 整体 O(n²)), 或者维护第二份计数 -- 后者
  正是 ADR-017 明确否掉的"两处计数互相漂移". 正确做法是在**抓取时**向运行时取值
  (导出器需要一个 collector 回调), 属于后续项, 不在此次偷偷降级.
- **`sessions_rejected_total` / `sessions_reclaimed_total` 已带 `protocol`**
  (D3). 7 路传输共 15 个准入/回收调用点统一走 `shared.IncProtocolCounter`, 每处都有测试断言
  `{protocol=...}` 的那条序列, 因此"少一个 label"会挂测试而不是静默通过.
  跨协议汇总仍是 `sum(...)`; 报告建议的 `udp_sessions_rejected_total` 前缀方案
  未采用 (只能靠正则聚合).
- **`sessions_accepted_total` / `sessions_closed_total` / `sessions_active` 仍是
  label 前的形态**: 它们会改动运维现有的看板与告警表达式, 且 gauge 的按协议读数
  本来就需要抓取期派生, 两件事应一起做, 见"重新评估条件".
- **这是一次破坏性变更**: 老序列 `sessions_rejected_total` (无 label) 不再存在,
  新序列从 0 开始, `rate()`/`increase()` 会抖动一轮. 变更只影响这两条计数, 不影响
  `handler_duration_seconds` / `messages_total` (它们自诞生就带 label).

## 重新评估条件

- 若观测路径并行基准的结构性变化 (例如每消息观测次数从 2 涨到 10) 让全局锁成为
  瓶颈, 应按指标名分片, 并让导出路径先归并再排序.
- 若要做"按协议的会话数", 应当引入抓取期 collector: `PrometheusMetrics` 持有一个
  `func() map[string]float64` 回调, 导出时问运行时要 `by_protocol` 计数, 而不是
  在热路径上维护第二份计数.
- D3 的 label 迁移已启动并完成 `sessions_rejected_total` / `sessions_reclaimed_total`
  两条; 剩余 `sessions_*` 三条要与抓取期 collector 一起做 (它们需要同一份
  "按协议读会话表"的能力), 届时仍需要一次破坏性变更窗口: 老序列消失会让 `rate()`
  从 0 重算, 告警会抖动一轮.
- 若引入 Prometheus client_golang (例如为了 exemplars 或 native histograms),
  本 ADR 的决策 6 可以被句柄替代, 但决策 1-5 不变.

## 关联文档

- 报告: `docs/reports/COMPARISON-261018-OSS-PEERS.md` 第 4.4 节 D1/D2 (D3 部分)
- 架构: `docs/architecture/OBSERVABILITY.md` sec.3.3 (内置指标) 与 sec.5 (导出格式)
- 相邻决策: ADR-006 (benchmark 驱动), ADR-017 (一处计数两维读法 -- 与本文
  "不做第二份计数"同源)
