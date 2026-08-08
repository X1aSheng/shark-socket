# shark-socket 项目深度审查报告 (V7)

- 日期: 2026-08-08 22:02
- 范围: 全部 Go 源文件 + 脚本 + 部署清单 + CI（不含 C 嵌入式附加目录）
- 方法: 4 个并行审查代理深度审查 + 本人逐项复核关键发现
- 基线: 本地 Windows 实测（见 §1）
- 前置: V6 审查 (2026-08-06) 后 V6 修复已全部落地（见 §2 验证清单）

## 1. 测试基线（本地 Windows 实测）

| 检查项 | 命令 | 结果 |
| --- | --- | --- |
| go build ./... | `go build ./...` | ✅ PASS |
| go vet ./... | `go vet ./...` | ✅ PASS |
| 生产包单元测试 | `go test ./api ./internal/... -count=1` | ✅ 20 包全过 |

> 与 V6 审查基线一致（build/vet 通过、单元测试通过）。集成/压力测试未在本次会话运行（V6 已知 Windows 端口耗尽偶发失败已通过 `-p 1` 串行化 + SO_LINGER(0) 修复）。

## 2. V6 修复验证（已确认正确，不再重复报告）

以下 V6 修复经本次代理逐文件复核 + 本人抽样确认，均已正确落地：

- **插件生命周期** (`internal/plugin/lifecycle.go`)：`begin/done/shutdown` 互斥 + 每周期全新 WaitGroup，Start-during-shutdown 为 no-op，WaitGroup 不跨周期复用。正确。
- **CoAP option 扩展编码**：nibble=14 用 `v-269`，encoder/decoder 一致。正确。
- **TCP/QUIC session Close 不再关闭 writeCh**；Send/writeLoop 均 select ctx.Done()。正确。
- **TCP worker pool 从不关闭 queue**；`done` channel 终止，stop 时排空残留任务。正确。
- **UDP/CoAP 错误路径 closeSession**（而非仅 sess.Close）。正确。
- **CoAP 空 payload GET 可达 handler**。正确。
- **gRPC-Web 原始请求透传**。正确。
- **零值 framer 默认 1MiB 安全上限**。正确。
- **AutoBan Record 接线 OnMessage + sweep staleness**。正确。
- **MessageLog.Replay 短 key 防护**。正确。
- **mqtt clientFactory 逐实例 + Start 双检**。正确。
- **BoltStore 读锁跨 closed 检查 + DB 访问**（TOCTOU 关闭）。正确。
- **SetLogger 加锁**（Persistence/Cluster/PluginChain）。正确。
- **PluginChain.OnAccept 逆序回滚 + 传输层 accepted 标志防重复 OnClose**。正确。
- **Gateway.Stop started 前置清位 / startedAt 收尾**。正确。
- **-mode deploy 只跑 ./tests/deploy；-mode cover 排除 ./tests；集成 -p 1 串行**。正确。
- **CI race job 用脚本 runner 覆盖 ./tests 与 ./tests/stress**。正确。
- **Helm service 显式 protocol: TCP**。正确。
- **golangci-lint v2.12.2 + action v7 / govulncheck@v1.1.4 固定**；本地 lint 0 问题。正确。

## 3. 缺陷清单（按严重程度, 去重, 已验证）

### P1 - 功能不可用 / 部署静默失败

| # | 缺陷 | 位置 | 验证 |
| --- | --- | --- | --- |
| P1-1 | **接受速率 < 1 时全部拒连**: `TryAccept` 将 allowance 封顶为 `rate`，`rate<1`（如 0.5 = 每 2 秒 1 连接）时 allowance 永远达不到 `>=1`，`TryAccept` 恒 false → TCP/WebSocket/QUIC/gRPC-Web 一个连接也收不到。修复：封顶改为 `max(rate,1)`（桶至少存 1 个 token） | internal/transport/shared/acceptor.go:37-41 | **代码确认** |
| P1-2 | **NetworkPolicy 出口规则与注释意图矛盾**: `egress: - to: - namespaceSelector: {}` 只匹配集群内 Pod（空选择器=所有命名空间 Pod），外部 IP 默认拒绝。注释声称"Egress 不限制"，但外部 MQTT broker/上游服务出站连接会被 CNI（Calico/Cilium）丢弃，且 DNS 仍可解析（kube-dns 是 Pod），表现为静默连不上。修复：`egress: - {}` 或显式 ipBlock 0.0.0.0/0 + DNS 规则。部署测试仅字符串检查，抓不到 | deploy/k8s/networkpolicy.yaml:28-30 | **代码确认** |

### P2 - 功能缺陷 / 资源泄漏 / 竞态

| # | 缺陷 | 位置 | 验证 |
| --- | --- | --- | --- |
| P2-1 | **`sessions_closed_total` 双计/虚计**: `metricSessionManager.Unregister` 无条件 `IncCounter`，不检查会话是否真实存在。TCP handleConn defer 在 Register 失败（容量/重复）时也调 Unregister；优雅停机 `CloseAll` 对每个会话再调一次 Unregister → 优雅停机后 closed ≈ 2× accepted，仪表盘告警失真。修复：`Unregister` 返回是否真实移除，仅在其为 true 时计数 | internal/runtime/session_metrics.go:28-32 | **代码确认** |
| P2-2 | **`sessions_active` gauge 竞态**: Register/Unregister 在底层临界区释放后才读 `Count()` 写 gauge，并发下 gauge 可过期（见 agent 场景）。修复：在锁内取数或由 Register/Unregister 返回操作后计数 | internal/runtime/session_metrics.go:24-25,31 | 代理复核 |
| P2-3 | **PluginChain 可重入死锁**: OnAccept/OnMessage/OnClose 持 `RLock` 期间调用插件代码，而 `SetLogger`/`Append` 需要 `Lock`——插件在回调内调用二者即死锁（RWMutex 不可重入）。"serving 期间安全调用"注释有误导。修复：快照切片后在锁外执行回调（同 SessionManager.Broadcast），或文档明确约束 | internal/runtime/plugin_chain.go:24-31,47-82 | **代码确认** |
| P2-4 | **WorkerPool 停池与提交竞态丢任务**: `submit` 通过 `closed` 检查后、在 drain 循环看到空队列退出**之后**才入队 → 任务永久滞留队列丢失（PolicyBlock 下与 done 竞争、PolicyDrop 下非阻塞入队）。修复：stop() 在 wg.Wait 后再自行排空队列，或最终再查一次 | internal/transport/tcp/worker_pool.go:129-148 | **代码确认** |
| P2-5 | **Gateway.Stop 非 StagedServer 与 CloseAll 绕过阶段超时**: `srv.Stop(ctx)`/`CloseAll(ctx)` 直接用原始 ctx，无 deadline 时卡死会无限挂起并持有 startMu 阻塞 Register。修复：二者均包 runStage(ctx, g.timeouts.CloseSessions) | internal/runtime/gateway.go:145-150 | **代码确认** |
| P2-6 | **传输层无 panic 恢复（进程崩溃）**: 用户 `core.Handler`/`Responder` 在 tcp/udp/coap/quic/websocket/grpcweb 的 worker/read goroutine 中裸调用，无 recover（`internal/transport` 全目录 grep `recover()` 零匹配）。插件链有恢复，裸 handler 没有 → 单连接 handler panic 击穿整个进程，违背"失败隔离"设计原则 | tcp/worker_pool.go:124, udp/server.go:225, coap/server.go:308/311, quic/server.go:202, websocket/server.go:211, grpcweb/server.go:286 | **grep 确认** |
| P2-7 | **UDP/CoAP DTLS 会话双重 OnClose**: handleDTLSConn defer 直接调 `OnClose`（仅本地 accepted 标志），与 `CloseSessions`→closeSession 并发时二次 OnClose，插件计数器/资源双释放 | udp/server.go:184-191, coap/server.go:199-206 | 代理复核 |
| P2-8 | **DTLS 会话无空闲超时/清扫（慢速耗尽）**: sweepLoop 只在明文 UDP 分支启动，handleDTLSConn 不设读 deadline；静默客户端占住 goroutine+会话+注册永不释放。TCP 有 anti-slowloris，DTLS 没有 | udp/server.go:159-230, coap/server.go:173-232 | 代理复核 |
| P2-9 | **WebSocket PongTimeout 是死配置，无对端失活检测**: 无 SetPongHandler/SetReadDeadline，对端断网不发 RST 时会话/goroutine 挂到内核 TCP 超时（数分钟） | websocket/options.go:18,34, server.go:194-233 | 代理复核 |
| P2-10 | **gRPC-Web WebSocket 模式无保活/读超时**: readWebSocketLoop 仅 SetReadLimit，无 ping/pong 无读 deadline，静默客户端无限泄漏 | grpcweb/server.go:269-291 | 代理复核 |
| P2-11 | **CoAP 去重表污染非 CON 消息**: `seen.LoadOrStore` 在 RST/ACK 类型判断**之前**，NON/ACK/RST 均入库；随后同 msgID 的合法 CON 被误判为重复直接回 ACK、handler 不执行（RFC 7252 去重只适用于 CON）。修复：仅对 CON 存储/检查 | coap/server.go:261-267 | **代码确认** |
| P2-12 | **UDP/CoAP 会话 ID 数据竞态**: `sessions.LoadOrStore` 先以 `id=0` 发布，再非原子写 `sess.id=NextID()`；并发 sweep/Close 读到 0 去 Unregister，留下 map 已删但 SessionManager 仍注册的残留会话，且 sess.id 写入本身是数据竞态。修复：入 map 前先分配 ID | udp/server.go:294-317, coap/server.go:445-473 | **代码确认** |
| P2-13 | **TLV 整数 1-7 字节按无符号解码**: 4 字节 `0xFFFFFFFF`（int32 -1）解为 4294967295；负数需按高位符号扩展（8 字节分支正确）。当前 codec 无生产调用点，影响仅限内部 round-trip，但修复不完整 | internal/protocol/lwm2m/codec_tlv.go:121-131 | 代理复核 |
| P2-14 | **熔断器 Execute 遇 panic 永久卡死**: half-open 中 `Allow` 置 `halfOpenActive=true`，`fn()` panic 时 Success/Failure 均不执行 → 熔断器永远停在拒绝态；closed 态 panic 也不计失败。修复：Execute 内 defer recover 调 Failure | internal/infra/circuitbreaker/circuitbreaker.go:103-113 | **代码确认** |
| P2-15 | **Helm serviceAccount 名称不匹配**: Deployment 用 `include "shark-socket.fullname"`（`helm install prod` → `prod-shark-socket`），ServiceAccount 用 `serviceAccountName` helper（values 硬编码 `shark-socket`）→ Pod 卡 ContainerCreating "serviceaccount not found"。默认 release 名 `shark-socket` 时恰好重合。修复：Deployment 改用 `include "shark-socket.serviceAccountName" .`；同步修正 tests/deploy/deploy_test.go:120 | deploy/helm/shark-socket/templates/deployment.yaml:17 | **代码确认** |
| P2-16 | **裸镜像 EXPOSE 端口不可达**: 运行时镜像未设 env，应用默认绑定 `127.0.0.1`（internal/app/config.go:40-43）；`docker run -p 18000:18000` DNAT 到容器 bridge IP 但监听在 loopback → 外部拒连。Compose/K8s 显式 0.0.0.0 所以正常。修复：运行时阶段加 `ENV SHARK_*_ADDR=0.0.0.0:*` 或文档说明 | deploy/docker/Dockerfile:16-19 | 代理复核 |

### P3 - 健壮性 / 质量 / 脚本

| # | 缺陷 | 位置 |
| --- | --- | --- |
| P3-1 | acceptor maxConns 在互斥锁外检查，并发升级时可超上限 | shared/acceptor.go:29,50 |
| P3-2 | TCP writeQueueHighWater 死配置（仅注释，无行为） | tcp/session.go:77-79 |
| P3-3 | CoAP decodeOptionHeader 空转包装 + `offset+1>len` 死守卫；截断 option 头静默错配 | coap/message.go:88,123-133 |
| P3-4 | CoAP observe 仅 CON 走 addObserveSeq（NON GET 注册后首值 0），CON 通知无重传 | coap/server.go:316-322,366-387 |
| P3-5 | gRPC-Web SendTrailers 不设 content-type，仅 trailer 的响应被 net/http 标为 text/plain | grpcweb/session.go:60-66 |
| P3-6 | RawFramer 写无上限（读限 32KiB），零值写帧可大于可读帧 | tcp/framer.go:151-157 |
| P3-7 | SessionManager.CloseAll 快照后新注册会话漏关/漏注销（TOCTOU） | runtime/session_manager.go:114-124 |
| P3-8 | Broadcast/CloseAll 无 per-session recover，会话 Send/Close panic 击穿 Stop/Health | runtime/session_manager.go:104-124 |
| P3-9 | PluginChain.OnMessage panic 时 safeMessage 的 `out=data` 死赋值（返回 nil,err） | runtime/plugin_chain.go:94-103 |
| P3-10 | 死代码 core.ConfigSnapshot（全仓库无引用） | core/observability.go:76-87 |
| P3-11 | AutoBan 封禁不关已建立会话（仅拦新连接），攻击者长连持续占用并反复触发 | plugin/autoban.go:92-97 |
| P3-12 | Cache 无后台 TTL 清扫器，长 TTL 过期项驻留 map | infra/cache/cache.go:29-40,70-81 |
| P3-13 | MemoryMetrics.ObserveHistogram / MemoryLogger 无界增长（生产长期运行内存泄漏） | infra/observability/metrics.go:39-44, logger.go:35-39 |
| P3-14 | RateLimit stamps 记录全部请求（含超限），每 key 内存随洪峰消息量增长 | plugin/ratelimit.go:93-101 |
| P3-15 | Cluster PubSub 容量 16 非阻塞，消费跟不上静默丢消息；慢会话 Send 会卡单消费者 | plugin/cluster.go:104-116, infra/pubsub/pubsub.go:39-54 |
| P3-16 | MessageLog.Len 不跳过短 key（与 Replay/Prune 不一致）；Replay 全程持锁，回调内 Append 自死锁 | infra/store/message_log.go:117-123 |
| P3-17 | PubSub 主题键在最后一个订阅取消后永不删除（慢 map 泄漏） | infra/pubsub/pubsub.go:24-37 |
| P3-18 | MQTT Start 忽略传入 ctx（已取消也不中止拨号） | infra/mqtt/adapter.go:38-85 |
| P3-19 | TLV codec 记录布局非 OMA 标准（type 字节未打包 type/ID/len 位），仅内部自洽 | protocol/lwm2m/codec_tlv.go:17-39 |
| P3-20 | 插件 Start 签名不一致（部分返回 error 部分不返回） | plugin/{autoban,ratelimit,heartbeat,cluster}.go |
| P3-21 | run_stress.go "资源门控"仅打印不 gate（与 run_benchmarks 的 resourceGate 不一致） | scripts/run_stress.go:56-58 |
| P3-22 | run_stress.go runTCPConcurrent 无总超时，半死连接卡 Receive 整个压测挂起 | scripts/run_stress.go:189-215,297-322 |
| P3-23 | run_stress.go runBurst 多 goroutine 共享单连接 Send/Receive，结果不可归因（仅因 payload 相同而"通过"） | scripts/run_stress.go:232-250 |
| P3-24 | helm 与 k8s ConfigMap 是死配置（Deployment 未 envFrom 引用，编辑无效） | helm/templates/configmap.yaml, k8s/configmap.yaml |
| P3-25 | run_tests.go raceEnv 追加 CGO_ENABLED=1 不删既有 CGO_ENABLED=0（Windows 导出时静默无 race 检测） | scripts/run_tests.go:156-173 |

## 4. 结论

- **无 P0（崩溃/数据损坏）**：build/vet/单元测试全过，V6 全部修复正确落地。
- **2 个 P1**：accept 速率 < 1 全拒连（配置值范围缺陷，生产配置偶发但致命）；NetworkPolicy 出口与注释意图相反（外部 broker 静默连不上）。
- **16 个 P2**：指标双计/竞态（P2-1/P2-2）、PluginChain 可重入死锁（P2-3）、WorkerPool 丢任务（P2-4）、传输层无 panic 恢复（P2-6）、DTLS 双重 OnClose/无空闲超时（P2-7/P2-8）、CoAP 去重污染（P2-11）、会话 ID 竞态（P2-12）、熔断器卡死（P2-14）、Helm SA 不匹配（P2-15）、裸镜像端口不可达（P2-16）等。
- 高价值修复顺序：**P1-1（acceptor 封顶）→ P1-2（egress 规则）→ P2-1/P2-2（session 指标）→ P2-3（PluginChain 锁）→ P2-6（handler panic 恢复）**。

## 5. 改进计划（优先级排序）

### 阶段 A: P1
1. **P1-1** acceptor 封顶 `max(rate,1)`，补 `AcceptRate<1` 回归测试
2. **P1-2** networkpolicy egress 改 `- {}` 或 ipBlock 0.0.0.0/0 + DNS 规则，注释与行为一致

### 阶段 B: P2 高价值
3. **P2-1/P2-2** Unregister 返回 bool 按实际移除计数；gauge 在锁内取数
4. **P2-3** PluginChain 回调移出锁（快照切片）或文档明确约束
5. **P2-6** 各传输层 handler 调用包 recover+log（复用 PluginChain.safeMessage 模式）
6. **P2-14** CircuitBreaker.Execute defer recover → Failure
7. **P2-15** Helm Deployment 用 serviceAccountName helper，修 deploy_test 断言
8. **P2-16** Dockerfile 运行时 ENV 0.0.0.0 或文档说明

### 阶段 C: P2 传输/运行时
9. **P2-4** WorkerPool stop 自行排空
10. **P2-5** Gateway.Stop 非 staged + CloseAll 包 runStage 超时
11. **P2-7/P2-8** DTLS defer 走 closeSession 去重 + 空闲超时/清扫
12. **P2-9/P2-10** WS/gRPC-Web 补 pong deadline/保活
13. **P2-11** CoAP 去重仅 CON
14. **P2-12** UDP/CoAP 会话 ID 先分配后发布

### 阶段 D: P2/P3 其余
15. P2-13 TLV 符号扩展; P3-1..P3-25 逐一处理
