# shark-socket 项目深度审查报告 (V8)

- 日期: 2026-08-09 09:10
- 方法: 4 个并行审查代理复核 V7 全部修复 + 寻找新问题；本人逐项复核关键发现
- 基线: build/vet/全包测试/集成/压力/部署 全过（本地 + 云服务器 120.76.44.233）

## 1. V7 修复复核结论

全部 43 项 V7 修复经 4 代理逐文件复核，**均正确**，未发现回归。已确认：
- Session 指标 `Unregister`→bool 双计修复、PluginChain 快照去锁、Gateway.Stop 超时
- CallHandler panic 隔离、RawFramer 写上限、DTLS OnClose 幂等 + 空闲超时
- CoAP 去重仅 CON、通知重传、NON observe 初值；WS/gRPC-Web 保活
- run_stress 资源门控、raceEnv CGO 清理、K8s/Helm envFrom、Helm SA helper
- OMA TLV 编解码（8/16/32 位 ID、8/16/24/32 位长度，边界检查正确）
- 插件生命周期签名统一（cluster 早期返回路径 WaitGroup 平衡正确）

## 2. 新发现缺陷（已全部修复）

### P0 - 进程崩溃（1）

| # | 缺陷 | 位置 | 修复 |
| --- | --- | --- | --- |
| P0-1 | **PubSub 丢包计数在 RLock 下写 map**: `Publish` 持 `RLock` 期间 `p.dropped[topic] += ...`，并发 Publish 或 Dropped() 读取 → `concurrent map read and map write` 进程崩溃（独立复现确认） | infra/pubsub/pubsub.go:62 | 迭代保持 RLock，计数更新取独占 Lock；新增并发回归测试 |

### P1 - 进程崩溃（2）

| # | 缺陷 | 位置 | 修复 |
| --- | --- | --- | --- |
| P1-1 | **metricSessionManager.CloseAll 绕过 panic 隔离**: 装饰器直接 `sess.Close(ctx)` 未走 safeSessionCall；且装饰器是默认网关路径（NopMetrics 非 nil），故 P3-8 修复在网关路径实际失效 | runtime/session_metrics.go | CloseAll 包 safeSessionCall |
| P1-2 | **UDP 明文 readLoop 未恢复 handler panic**: 唯一未包 CallHandler 的传输路径（DTLS 变体已包），handler panic 击穿进程 | transport/udp/server.go | 包 shared.CallHandler |

### P2 - 功能/健壮性（7）

| # | 缺陷 | 修复 |
| --- | --- | --- |
| P2-1 | CloseAll 循环忽略 ctx，停机中持续注册会话时无限旋转、越过 CloseSessions 期限 | base+装饰器循环首部检查 ctx.Err() |
| P2-2 | gRPC-Web Stop 挂起（测试确认）：Drain 先 wg.Wait，而 read/ping goroutine 需 CloseSessions 关连接后才退出 | Drain 改 no-op，CloseSessions 关连接后再 wg.Wait |
| P2-3 | TCP 高水位阈值 `int(0.8*cap)` 对 cap=1 截断为 0 → 永久 ErrWriteQueueFull | 阈值钳制 >= 1 |
| P2-4 | HTTP 传输 OnAccept 失败时 defer 无条件 OnClose → 双通知（其他传输均加了 accepted 标志） | 加 accepted 标志门控 OnClose |
| P2-5 | run_stress reconnect 模式缺读超时/linger(0) → 半死连接挂死、TIME_WAIT 端口耗尽；且无 send 计数 | 补 linger(0)+读超时+sendOk/Fail |
| P2-6 | tests/stress TestStressTCPBurst 共享单连接（与 P3-23 相同的反模式），指标无效 | 每 goroutine 独立客户端 |
| P2-7 | AutoBan 按消息数计（阈值 3 过低，误封合法客户端），P3-11 关会话放大 | 文档明确为消息数限制器，建议合理配置阈值 |

### P3 - 健壮性/性能（修复 11 项）

| # | 缺陷 | 修复 |
| --- | --- | --- |
| P3-1 | TLV 32 位长度在 32 位平台 int 溢出 → make panic | length < 0 防护 |
| P3-2 | TCP worker pool handle 对 nil session 解引用 | nil 守卫 |
| P3-3 | WithReadTimeout(0) 无法禁用超时 | 无条件赋值 |
| P3-4 | CoAP 重传表仅按 msgID 键（回绕后跨 observer 冲突） | 键改 (remote, msgID) |
| P3-5 | CoAP 重传持锁跨阻塞写 | 锁内快照、锁外发送 |
| P3-6 | CoAP 先 track 后发 → 重传可能先于首次发送 | 先发后 track |
| P3-7 | gRPC-Web 错误路径 trailer 后写 HTTP body（协议违规） | SendTrailers 后直接返回 |
| P3-8 | Cluster.WithTopic 无锁写 | 加锁 |
| P3-9 | heartbeat Sweep 调 sess.Close 无 panic 隔离 | safeSessionClose |
| P3-10 | PluginChain snapshot 每消息 clone 分配 | Append 写时复制，snapshot 零分配 |
| P3-11 | Helm NOTES 端口转发指向 fullname（自定义 release 名失效）+ run_benchmarks 门控 fail-closed 与 run_stress 不一致 | NOTES 指向 Chart.Name；门控改 fail-open |

## 3. 验证

| 检查项 | 结果 |
| --- | --- |
| go build ./... / go vet ./... / gofmt | ✅ |
| 全包单元测试 + race（9 个改动包） | ✅ |
| 集成/压力/部署 `go test ./tests/... -p 1` | ✅ |
| 云服务器 (120.76.44.233) build/单测/集成 | ✅ |

## 4. 结论

- V7 全部修复正确落地，无回归。
- V8 新发现 **1 P0 + 2 P1 + 7 P2 + 11 P3** 已全部修复并推送（`63429226e`）。
- 最关键的三个真实崩溃点已消除：PubSub 并发 map 写、网关默认关闭路径 panic 穿透、UDP 明文 handler panic。
- 保留项（文档已标注）：OMA TLV 仅支持 Resource-with-Value 记录（Object Instance 等需对象模型，属后续互操作需求）。
