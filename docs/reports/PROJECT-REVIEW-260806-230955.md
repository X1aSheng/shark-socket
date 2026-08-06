# shark-socket 项目深度审查报告 (V6)

- 日期: 2026-08-06 23:09
- 范围: 全部 158 个 Go 源文件 + 脚本 + 部署清单 + CI + C 嵌入式附加目录
- 方法: 4 个并行审查代理深度审查 + 本人逐文件复核 + 复现测试实证
- 基线: build OK, vet OK, 单元测试 21 包全过, 集成测试偶发失败 (见 S-01/S-02), race 待跑

## 1. 测试基线

| 检查项 | 结果 |
| --- | --- |
| go build ./... | PASS |
| go vet ./... | PASS |
| 单元测试 (./api ./cmd/... ./internal/...) | 21 包全部 PASS |
| 集成测试 (./tests/...) | 偶发 FAIL (TestCrossProtocolPlugin/WebSocket 端口耗尽) |
| 覆盖率脚本 (go run scripts/run_tests.go -mode cover) | 偶发 FAIL (并行端口耗尽 + tests/stress 无语句) |
| 部署模式 (go run scripts/run_tests.go -mode deploy) | 偶发 FAIL (同上) |
| 日志 | logs/2026-08-06T23-01-20_unit.log 等 |

## 2. 缺陷清单 (按严重程度, 已去重, 已验证)

### P0 - 数据损坏 / 崩溃

| # | 缺陷 | 位置 | 验证 |
| --- | --- | --- | --- |
| P0-1 | **CoAP option 扩展编码 base 错误**: nibble=14 时编码器写 `v-13`, 解码器期望 `v-269`, option 号或 option 值长度 >= 269 的报文编解码错误 (数据损坏) | internal/transport/coap/message.go:214-243 | **测试确认**: delta=269->解码525, 300字节 option 值损坏, option 300->556 |
| P0-2 | **插件 Start/Stop sync.Once 重置 data race**: autoban/ratelimit/heartbeat/cluster 用 `p.stopOnce = sync.Once{}` 重启, 与 Stop() 的 `stopOnce.Do` 并发时竞态, 可能向已关闭 channel close 二次或泄漏 goroutine | internal/plugin/{autoban,ratelimit,heartbeat,cluster}.go | 代码确认 (4 处同模式) |
| P0-3 | **Cluster Start/Stop 无锁并发**: `p.stop/p.cancel/p.stopOnce` 无保护读写 | internal/plugin/cluster.go:54-83 | 代码确认 |

### P1 - 功能损坏 / 资源泄漏

| # | 缺陷 | 位置 | 验证 |
| --- | --- | --- | --- |
| P1-1 | **AutoBan 完全失效**: `Record()` 无生产调用点 + `sweep()` 无条件删除所有非封禁计数 (无 staleness 检查), 慢速攻击永不被封禁 | internal/plugin/autoban.go:84-88,107 | 代码确认 |
| P1-2 | **TCP worker pool PolicyBlock 阻塞泄漏**: 队列满时 submit 只 select `p.done`, 不监听 `sess.ctx`, 对端断开后 handleConn goroutine 阻塞直到 pool stop | internal/transport/tcp/worker_pool.go:64-72 | 代码确认 |
| P1-3 | **QUIC 部分写入静默丢数据**: `stream.Write` 返回 n<len 时直接 return 不重试 (依赖 io.Writer 契约兜底, 仍应防御) | internal/transport/quic/session.go:102-106 | 代码确认 |
| P1-4 | **MessageLog.Replay 短 key panic**: 直接 `[]byte(key)[:8]` 无长度检查, store 中存在 <8 字节 key 时 panic; 同文件 Prune/NewMessageLog 均有检查 | internal/infra/store/message_log.go:69 | 代码确认 |
| P1-5 | **MQTT Start TOCTOU**: 锁内检查后释放锁再 connect, 并发 Start 双连接泄漏 | internal/infra/mqtt/adapter.go:43-80 | 代码确认 |
| P1-6 | **RateLimit 解析失败回退带端口 key**: SplitHostPort 失败时用含端口地址作 key, 退化为按连接限流 | internal/plugin/ratelimit.go:89-93 | 代码确认 |
| P1-7 | **测试基础设施端口耗尽**: `go test ./...` 并行跑 stress/reconnect/cross_protocol 连接洪峰, Windows 上 TIME_WAIT 端口耗尽, 集成/覆盖率偶发失败 | tests/cross_protocol_test.go, tests/stress/stress_test.go | **测试复现** |

### P2 - 健壮性 / 可重入性

| # | 缺陷 | 位置 | 验证 |
| --- | --- | --- | --- |
| P2-1 | **mqtt.clientFactory 包级可变全局** (破坏可重入性, 多实例不可安全共享) | internal/infra/mqtt/adapter.go:22-24 | 代码确认 |
| P2-2 | **PluginChain.SetLogger 无锁写** (与 safeAccept/Message/Close 的 RLock 读竞态) | internal/runtime/plugin_chain.go:23-27 | 代码确认 |
| P2-3 | **Persistence.SetLogger 无锁写** | internal/plugin/persistence.go:37-41 | 代码确认 |
| P2-4 | **CoAP nibble=15 保留值无防御**: RFC 7252 禁止, 当前静默当作 delta=0 解析 (已证不 hang, 但需拒绝) | internal/transport/coap/message.go:91-145 | 测试确认不 hang |
| P2-5 | **CoAP optionNum uint16 截断**: delta 累积超 65535 静默回绕 | internal/transport/coap/message.go:100 | 代码确认 |
| P2-6 | **HTTP responseRecorder.body 只写不读**: 每请求整份缓存进内存 | internal/transport/http/session.go:79-101 | 代码确认 |
| P2-7 | **Cluster.handleClusterMessage 静默丢弃 JSON 错误** (无日志/metrics) | internal/plugin/cluster.go:117-121 | 代码确认 |
| P2-8 | **SlowHandler threshold<=0 clamp 为 0**: elapsed>=0 恒真, 每请求记慢日志 | internal/plugin/slow_handler.go:24-25 | 代码确认 |
| P2-9 | **BoltStore isClosed TOCTOU**: isClosed 检查与 DB 操作间 Close() 可执行, 返回 bolt.ErrDatabaseNotOpen 而非 core.ErrClosed | internal/infra/store/bolt.go:38-48 | 代码确认 |

### P3 - 代码质量 / 文档

| # | 缺陷 | 位置 |
| --- | --- | --- |
| P3-1 | Gateway.Stop 二次 started.Store(false) (冗余) | runtime/gateway.go:112,145 |
| P3-2 | Gateway.Health started/startedAt 短暂不一致 (窗口期) | runtime/gateway.go:150-161 |
| P3-3 | run_stress.go init() 死代码: exit channel 永不触发, Stop 不执行 | scripts/run_stress.go:354-366 |
| P3-4 | run_stress.go readResourceState 读 /proc 后丢弃结果, cloud 资源门控形同虚设 | scripts/run_stress.go:376-384 |
| P3-5 | run_stress.go Burst 单连接并发 Send/Receive, 45% 发送失败掩盖问题 | scripts/run_stress.go:217-258 |
| P3-6 | MemoryMetrics/MemoryLogger 无界增长 (生产长期运行内存泄漏) | infra/observability/{metrics,logger}.go |
| P3-7 | CoAP SendObserveNotification 固定 4 字节 vs encodeObserveSeq 变长, 两套不一致 (前者无调用点) | transport/coap/observe.go:104 |
| P3-8 | LwM2M handleWrite 用 strings.Fields 后 Join, 多空格语义丢失 | protocol/lwm2m/coap.go:103 |

### S - 脚本 / CI / 部署

| # | 缺陷 | 位置 | 验证 |
| --- | --- | --- | --- |
| S-1 | **run_tests.go `-mode deploy` 与 `-mode integration` 完全相同**: 都跑 `./tests/...` (含 stress 洪峰测试), 部署清单验证应只跑 `./tests/deploy` | scripts/run_tests.go:44-45 | **测试复现 FAIL** |
| S-2 | **run_tests.go `-mode cover` 跑 `./...`**: 并行全包 + tests/ 无语句包, 偶发端口耗尽 FAIL; 覆盖率应只统计生产包 | scripts/run_tests.go:165-168 | **测试复现 FAIL** |
| S-3 | **TestStressTCPBurst Connect 失败直接 t.Fatal**: 端口耗尽时整个套件 FAIL, 应重试/报告 | tests/stress/stress_test.go:158-161 | **测试复现 FAIL** |
| S-4 | **TestCrossProtocolPlugin/WebSocket 端口耗尽偶发失败** | tests/cross_protocol_test.go:110-112 | **测试复现 FAIL** |
| S-5 | run_tests.go JSON 模式切片插入 `-json` 脆弱 (args[:1] 拼接) | scripts/run_tests.go:68-71 | 代码确认 |
| S-6 | **CI race job 缺 `./tests` 与 `./tests/stress/...`**: cross_protocol 与 stress 不在 race 范围; 且与脚本 `-mode race` (./...) 不一致 | .github/workflows/ci.yml:186 | 代码确认 |
| S-7 | run_benchmarks.go stage 用硬编码索引切片, 组顺序变更即失效 | scripts/run_benchmarks.go:265-271 | 代码确认 |
| S-8 | K8s NetworkPolicy egress 全开放 (namespaceSelector: {}) | deploy/k8s/networkpolicy.yaml:23-25 | 代码确认 |
| S-9 | Helm service 缺 protocol/namespace 显式声明 | deploy/helm/shark-socket/templates/service.yaml | 代码确认 |

## 3. C 嵌入式项目缺陷 (附加工作目录, 不在主仓库)

| # | 级别 | 缺陷 | 位置 |
| --- | --- | --- | --- |
| C-1 | **P0** | **FatFs 源文件完全丢失**: MDK .o/.d 依赖证明曾编译 ff_diskio.c/diskio_stm32f407.c 等, 但 g:\c-module\FatFs 目录不存在, 项目不可构建 | g:\cubeide\f407ve-spitf |
| C-2 | **P0** | main.c 在 HAL_Init 前调用 NVIC_BootloaderFix() 内部 printf, 外设未初始化硬故障/挂起 | Core\Src\main.c:107, nvic_bootloader_fix.c |
| C-3 | P0 | HardFault/NMI 等 5 个故障处理器死循环, 无诊断/复位 | Core\Src\stm32f4xx_it.c:93-164 |
| C-4 | P1 | spi_flash RMW 嵌套 acquire_work_buffer 需 2 个缓冲槽, 无动态分配时静默失败 | spi_flash\spi_flash.c:279-298 |
| C-5 | P1 | HAL 时基 TIM6 与 spi_flash_port DWT 两个 tick 源漂移, 性能数据不准 | Core\Src\stm32f4xx_hal_timebase_tim.c, spi_flash_port.c |
| C-6 | P1 | NMI_Handler 处理后可恢复事件仍永久挂起; 看门狗被注释禁用 | Core\Src\stm32f4xx_it.c:98-103, main.c:128 |
| C-7 | P2 | main.c 引用缺失头文件 elog.h/ff_sd_test.h 等 | Core\Src\main.c:34-46 |
| C-8 | P2 | spi_flash 锁超时仅断言后无锁返回, 绕过互斥保护 (USE_FULL_ASSERT 未启用时静默) | spi_flash_port.c:564-568 |
| C-9 | P2 | SPI1 预分频 2 = 42MHz, SD 卡 SPI 模式通常限 25MHz | Core\Src\spi.c:49 |
| C-10 | P3 | main.c 全部测试函数被注释, 构建成功不证明功能 | Core\Src\main.c:161-170 |

**注**: C 项目附加目录的 build.bat 仅对 spi_flash 做 clang 语法检查 (无 STM32 SDK), 不生成日志文件; 完整构建需 Keil MDK-ARM (f407ve-spitf.uvprojx), 因 FatFs/elog 源码丢失当前不可构建。

## 4. 改进计划 (优先级排序)

### 阶段 A: P0 数据正确性
1. **P0-1** CoAP option 扩展编码: 修复 base=269, 附 round-trip 回归测试
2. **P0-2/P0-3** 插件生命周期: 用 mutex + running 状态重构 Start/Stop, 去掉 sync.Once 重置; 附并发 Start/Stop 回归测试 (race 检测)

### 阶段 B: 测试基础设施 (恢复 CI 稳定)
3. **S-1** `-mode deploy` 只跑 `./tests/deploy`
4. **S-2** `-mode cover` 排除 `./tests/...`, 只统计生产包
5. **S-3** TestStressTCPBurst Connect 失败重试
6. **S-4/S-7** 集成模式 `-p 1` 串行化避免端口耗尽; cross_protocol WebSocket 加重试
7. **S-6** CI race job 补齐 ./tests ./tests/stress; 与脚本对齐
8. **S-5** 清理 run_tests.go JSON 拼接

### 阶段 C: P1 功能缺陷
9. **P1-1** AutoBan 接线 OnMessage 调用 Record + sweep 增加 staleness 检查
10. **P1-2** worker pool submit 增加 sess.ctx.Done() 分支
11. **P1-3** QUIC 部分写入防御
12. **P1-4** MessageLog.Replay 长度检查
13. **P1-5** MQTT Start 并发防护 (double-check + 清理)
14. **P1-6** RateLimit key 解析兜底处理

### 阶段 D: P2 可重入性/健壮性
15. **P2-1** clientFactory 改为 Adapter 字段
16. **P2-2/P2-3** SetLogger 加锁
17. **P2-4/P2-5** CoAP nibble=15 拒绝 + optionNum 溢出防护
18. **P2-6** 移除 responseRecorder.body
19. **P2-7** cluster 消息错误日志
20. **P2-8** SlowHandler 参数校验
21. **P2-9** BoltStore 原子 isClosed 检查

### 阶段 E: P3 质量
22. P3-1..P3-8 逐一处理
23. S-8/S-9 K8s/Helm 加固
24. 更新 README/CHANGELOG 文档

### 阶段 F: 云服务器部署验证
25. 清理服务器残留 -> Linux 编译 -> docker 构建/部署 -> k8s/helm 部署 -> 记录验证报告

每个缺陷修复附回归测试, 修复后全量测试 + race 验证, 逐个提交。
