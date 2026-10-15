# shark-socket 项目审查与改进计划 (V9)

## 0. 元信息

| 项 | 值 |
| --- | --- |
| 报告版本 | V9 |
| 上一版报告 | docs/reports/PROJECT-REVIEW-260809-091049.md (V8) |
| 审查基线 | 0f5c54237 (docs/security: calibrate SECURITY/PLUGIN docs) |
| 本次改动 | 16 commits, 67 files, +3122 / -455 |
| 代码规模 | 38 个包, 其中 27 个包含测试, 105 个测试文件 |
| 构建环境 | Windows 11, Go 1.26.5 windows/amd64 |
| 竞态环境 | CGO_ENABLED=1 + D:/Programs/w64devkit + D:/Programs/LLVM |
| 目标环境 | Alibaba Cloud, ubuntu 26.04, 4 core / 8GB |

审查方法: 对每个候选缺陷先复现或写出失败用例, 确认缺陷真实存在后再修复, 修复后
重跑该包与整个测试树, 最后逐项提交. 未能在本地确认的项一律归入第 4 节 (未修复),
不做无证据的修改.

## 1. 结论摘要

V9 一共确认并修复 18 项缺陷, 其中包含 3 项会造成进程级后果的高危缺陷:

1. UDP / CoAP 的 accept 与 read 循环在非关闭错误上无退避, 会 100% 占满一个核心.
   实测 UDP 在 250ms 内调用 Accept 10298963 次 (约 4100 万次/秒), CoAP 9541655 次.
2. QUIC 的 Drain 与 Gateway.Stop 在有活跃连接时死锁, 直到超时.
3. TLS/DTLS 客户端证书热更新不生效 (ClientCAs 是启动快照), mTLS 轮换必须重启进程.

同时确认 CI 处于红灯状态: golangci-lint 报 4 个 staticcheck 问题, 其中 3 个在
本次审查之前就已存在. 现已全部清零.

所有修复均通过了本地等价门禁: build / vet / unit / integration / deploy / lint /
cover(80.4%) 全部为 0 退出码. race 门禁见 2.7.

## 2. 验证记录

以下命令在仓库根目录执行, 结果为本机实测.

| 门禁 | 命令 | 结果 |
| --- | --- | --- |
| 构建 | go build ./... | EXIT 0 |
| 静态检查 | go vet ./... | EXIT 0, 无输出 |
| 格式 | gofmt -l . | 无输出 |
| 单元测试 | go test ./... | EXIT 0, 27 个含测试包全部 ok |
| 单元(门禁) | go run scripts/run_tests.go -mode unit -timeout 5m | EXIT 0 |
| 集成测试 | go run scripts/run_tests.go -mode integration -timeout 5m | EXIT 0, 含 5 组压力测试 |
| 部署校验 | go run scripts/run_tests.go -mode deploy | EXIT 0 |
| 质量门禁 | golangci-lint run --max-same-issues 0 --max-issues-per-linter 0 | 0 issues |
| 覆盖率 | go run scripts/run_tests.go -mode cover | EXIT 0, 总覆盖率 80.4% (门限 70%) |
| 压力测试 | go run scripts/run_stress.go -mode all | EXIT 0, 三模式全部 PASSED |

### 2.1 集成模式明细

| 用例 | 结果 |
| --- | --- |
| TestCrossProtocolPlugin, TestCrossProtocolOnAcceptBlock | PASS |
| TestExamplesCompile (11 个示例) | PASS |
| TestStressTCPConnections / TestStressTCPBurst / TestStressTCPReconnect | PASS |
| TestStressUDPConnections / TestStressWSChurn / TestStressHTTPRequests | PASS |
| TestK8sManifestSemantics / TestHelmChartSemantics / TestDockerfileEntrypoint | PASS |

### 2.2 覆盖率

总覆盖率 80.4%. 覆盖模式会动态枚举生产包 (排除 ./tests/...), 再以 70% 为硬门限,
低于门限时脚本返回非 0.

### 2.7 竞态门禁

命令: go run scripts/run_tests.go -mode race -timeout 5m (CGO_ENABLED=1, 全量 ./...).

结果: EXIT 0, 日志中没有任何 WARNING: DATA RACE, 没有任何 FAIL. 含 ./tests/stress 的
压力包也在竞态检测下通过 (55.9s).

本轮修复中与竞态直接相关的是 F9 的 Cluster topic 竞争与 F13 的 tcp.Client 无同步.
两者都在修复前被 -race 实际捕获过 (见 3.3 与 3.5), 修复后不再出现.

## 3. 已修复缺陷

严重度定义: P0 = 崩溃或不可用, P1 = 资源耗尽或功能错误, P2 = 边界错误或可观测性缺失,
P3 = 工程卫生.

### 3.1 传输层忙等 (P1, F10)

现象: UDP / CoAP 的 DTLS accept 循环与 UDP read 循环在错误分支上直接 continue.
只要错误不是关闭导致的 (EMFILE, listener 被外部破坏, 对端不断发送畸形握手), 下一次
Accept / Read 会立即再次失败, 循环变成纯 CPU 自旋.

确认证据 (移除修复后实测):

| 位置 | 250ms 内重试次数 | 折算 |
| --- | --- | --- |
| internal/transport/udp 的 dtlsAcceptLoop | 10298963 | 约 4100 万次/秒 |
| internal/transport/coap 的 dtlsAcceptLoop | 9541655 | 约 3800 万次/秒 |

修复:

- 新增 internal/transport/shared/backoff.go. 采用与 net/http Server.Serve 相同的
  策略: 首次等待 5ms, 之后每次失败翻倍, 上限 1s, 成功后 Reset. 该结构不持有任何
  共享状态, 每个循环各自持有一个实例, 因此不引入跨实例耦合.
- udp/server.go 与 coap/server.go 的 4 个循环接入该退避.
- 同时把 net.ErrClosed 视为终止条件. 此前 socket 被外部关闭时, s.closed 仍为 false,
  循环会永久重试, 导致 Drain 永远等不到 goroutine 退出.

验证: internal/transport/{udp,coap}/accept_backoff_test.go 用计数型假 listener 断言
250ms 内重试次数小于 20 (修复前分别为 10298963 / 9541655, 必然失败); 另用
"外部关闭 socket 后 read 循环必须退出" 断言 net.ErrClosed 分支. 4 个用例修复前全部
失败, 修复后全部通过.

提交: 308a01361

### 3.2 DTLS 连接泄漏 (P1, F12)

现象: handleDTLSConn 在 read 出错 (对端断开, 读超时, 传输错误) 后直接 return,
deferred 清理只做 map 删除, 从未关闭连接.

根因: deferred 里先执行 dtlsConns.Delete(id), 这会让 StopAccept 的遍历也看不到该连接,
于是没有任何路径会关闭它, 只能等待 net 的 finalizer. 每个断开的 DTLS 对端泄漏一个
socket, 高并发下会耗尽文件描述符.

修复: 在 udp/server.go 与 coap/server.go 的 deferred 清理中先调用 sess.Close. 该方法
经 closeOnce 保证幂等, 因此与 CloseSessions / closeSession 并发执行安全.

验证: internal/transport/{udp,coap}/dtls_conn_release_test.go 直接以 Read 立即失败的
net.Conn 调用 handleDTLSConn, 断言连接被关闭且会话计数归零. 修复前两个用例均失败
(报 "socket leaked"), 修复后通过.

提交: 30cb29584

### 3.3 TCP Client 并发不安全与连接泄漏 (P1, F13)

现象: internal/transport/tcp/client.go 的 Connect / Send / Receive / Close 都直接
读写 c.conn, 没有任何同步; 且 Connect 直接覆盖 c.conn, 前一个连接不会被关闭.

确认证据 (回退到修复前版本):

- -race 报 WARNING: DATA RACE, 指向 client.go 中 Connect 的写入与 Send 的读取.
- 重复 Connect 后前一个连接仍可写入, 用例报 "re-Connect leaked the previous
  connection: it is still writable".

影响面: 该类型通过 api.NewTCPClient 对外导出, 是公开 API; scripts/run_stress.go 的
reconnect 模式也是重连场景.

修复: 增加 RWMutex, 只在指针交换时持锁, 不跨 I/O 持锁. Connect 在锁外拨号, 安装新连接
后再关闭旧连接; Close 把字段置 nil, 因此重复 Close 是空操作, Close 之后的 Send 返回
core.ErrClosed 而不是底层网络错误.

验证: internal/transport/tcp/client_concurrency_test.go. 修复前泄漏用例与竞态用例均
失败; 修复后通过, 且整个 tcp 包在 -race 下干净.

提交: d56ade6b5

### 3.4 插件生命周期 panic 未隔离 (P1, F8)

现象: PluginChain.StartAll 在某个插件 Start panic 时会让整个进程崩溃 (Go 无法跨
goroutine recover), StopAll 同理; 且 StopAll 不返回错误, 关停期的 Stop 失败被完全丢弃.

修复:

- core.PluginRunner.StopAll 改为返回 error.
- runtime/plugin_chain.go 增加 safeStart / safeStop, 逐个包装插件, 用 recover 转为
  core.ErrPluginPanic 并记录日志, 多个错误用 errors.Join 汇总.
- StartAll 在中途失败时回滚已启动的插件.
- runtime/gateway.go 的两处调用点 (启动回滚路径与关停路径) 检查并记录错误, 关停路径
  将其并入 firstErr.

验证: internal/runtime/plugin_lifecycle_regression_test.go 三个用例 (StartAll panic
隔离, StopAll panic 隔离, StopAll 上报插件错误). 全部通过, 且 ./internal/runtime 与
./internal/plugin 在 -race 下干净.

提交: 384681d62

### 3.5 插件状态类缺陷 (P2, F9)

| 缺陷 | 位置 | 影响 | 修复 |
| --- | --- | --- | --- |
| 封禁地址未归一化 | plugin/autoban.go | 以 "1.2.3.4" 记录封禁时, 调用方传 "1.2.3.4:5678" 查不到, 封禁静默失效 | 经 normalizeBanKey 走 SplitHostPort 取 host |
| 心跳清扫被单个会话拖死 | plugin/heartbeat.go | 一个卡死的会话会让整轮清扫阻塞, 其余会话得不到回收 | 新增 closeTimeout 与 WithSweepCloseTimeout, 用 goroutine + timer 给 sess.Close 设上限 |
| Cluster topic 数据竞争 | plugin/cluster.go | Start 写入 topic 的同时 OnMessage 读取, -race 报 DATA RACE | 新增 topicName() 在 RLock 下读取 |
| Persistence 构造失败被吞 | plugin/persistence.go | store 构造失败后仍以 nil store 运行 | 新增 initErr / Err() / Start() / Stop() 上报 |
| SlowHandler nil next 崩溃 | plugin/slow_handler.go | 越过阈值后每条消息都 panic | 将 nil next 替换为 no-op |

验证: internal/plugin/plugin_defect_regression_test.go 与 slow_handler_nil_test.go.
其中 Cluster 用例在 -race 下被验证为能捕获该竞争 (临时回退修复即 FAIL).

提交: 384681d62

### 3.6 QUIC Drain 死锁 (P1, F2)

现象: 有活跃连接时 QUIC 的 Drain 在 3s 后报 context deadline exceeded, Gateway.Stop 在
5s 后同样超时.

根因: quic-go 的 Listener.Close 不会关闭已接受的连接. handleConn 阻塞在
AcceptStream 上, 而 Drain 先于 CloseSessions 执行, 因此 Drain 永远等不到 goroutine 退出.

修复: server.go 增加 server 级 stopCtx / stopCancel, Start 时创建并传给 acceptLoop,
StopAccept 触发取消; handleConn 使用 conn.AcceptStream(ctx). 另外 session.go 的 Send
增加 ctx 预检查, 关闭后返回 core.ErrSessionClosed.

附带修复: quic 的 stopGateway 测试辅助函数此前用 `_ = gateway.Stop(ctx)` 吞掉错误,
掩盖了该缺陷, 改为 tb.Fatalf.

验证: internal/transport/quic/lifecycle_regression_test.go.

提交: 325742d99

### 3.7 CoAP RFC 7252 / 7641 一致性 (P1, F1)

确认 3 项缺陷:

1. Empty CON (Code 0.00, 即 ping) 被回复 2.05, 规范要求回 RST (RFC 7252 4.3).
2. 规范的 RST (Empty, TKL=0, 无 token) 无法取消 observe 关系, 因为代码按 token 关联,
   而规范 RST 根本不带 token, 必须按 Message ID 关联.
3. Observe 选项长度为 0 (即 Observe=0 注册) 时不会注册.

修复: handleCoAPMessage 调整分支顺序 (ACK 清 pending, RST 按 Message ID 取回 token 后
RemoveByToken, CodeEmpty 且 TypeCON 回 RST); 新增 takePendingNotify(remote, msgID);
handleObserve 区分 len(obsVal)==0 与非 0 两种情况.

注意: 既有用例 TestCoAPRSTCancelsObserver 固化了错误假设 (RST 带 token), 掩盖了缺陷 2,
已同步修正.

验证: internal/transport/coap/protocol_conformance_test.go 三个用例.

提交: 7d9e84481

### 3.8 gRPC-Web 分帧与写超时 (P2, F3)

- Send 无条件设置 content-type, 对 passthrough 响应是错的; 改为仅在 s.framed 时设置.
- framed 的判定从"解析请求"改为"按声明" (grpcWeb || strictFraming), 避免请求体形态
  影响响应分帧.
- grpc-message trailer 未按 gRPC-Web 规范百分号编码, 含 CR/LF 或非 ASCII 字节时会破坏
  trailer 块 (可注入 header). 新增 encodeGRPCMessage, 只放行 %x20-%x24 与 %x26-%x7E.
- WebSocket 会话缺少写超时, 慢速对端会永久占住 goroutine. 新增 WithWriteTimeout 与
  setWriteDeadline, 作用于 Send / ping / Close.

提交: 325742d99

### 3.9 mTLS 客户端 CA 热更新 (P2, F4)

现象: tls.Config.ClientCAs 在握手时被读取的是启动时的快照, 轮换 CA 必须重启进程.

修复: internal/app/config.go 在已加载 CA 池时安装 GetConfigForClient, 每次握手 Clone
配置并刷新 ClientCAs.

验证: internal/app/mtls_hot_reload_test.go. 早期版本用真实 TLS 握手, 在 Windows 上会
偶发 wsarecv 中断, 已改为确定性的 X.509 链校验, 直接校验 TLS 栈实际取到的配置.

提交: 3c5423ac9

### 3.10 infra 层缺陷 (P2, F5 / F6 / F7)

| 缺陷 | 位置 | 影响 | 修复 |
| --- | --- | --- | --- |
| MQTT 重连后订阅丢失 | infra/mqtt/options.go | SetAutoReconnect 默认不恢复订阅, 重连后静默失联 | SetResumeSubs(true) |
| MQTT 入站 payload 被复用 | infra/mqtt/adapter.go | paho 在回调返回后回收 paylaod 缓冲, 下游持有即读到脏数据 | 入站做一次拷贝; 并校验 nil handler / 空 topic |
| waitToken 无超时兜底 | infra/mqtt/adapter.go | 超时 <= 0 时永久阻塞 | time.NewTimer + defer Stop, 超时 <= 0 取默认值 |
| Bolt 打开无超时 | infra/store/bolt.go | 库文件被其他进程持有时永久挂起 | 默认 3s 超时, 映射 bolt.ErrTimeout |
| Open 状态下仍记成功 | infra/circuitbreaker/circuitbreaker.go | 熔断器被"成功"事件提前闭合 | state == Open 时 Success 直接返回 |
| ttl < 0 语义不明 | infra/cache/cache.go | 负数 TTL 被当成"永不过期"或立即过期, 行为依赖实现 | 显式删除该键 |
| pubsub 广播共享同一 payload | infra/pubsub/pubsub.go | 一个订阅者修改 payload 会影响其他订阅者 | 每个订阅者构造独立的 msg |

提交: 3c5423ac9

### 3.11 Acceptor 令牌与容量记账 (P2, F11)

缺陷: TryAccept 先扣令牌再判容量, 容量拒绝时不会归还令牌; 令牌桶拒绝时也不会归还容量
槽位, 导致 active 计数被永久抬高, 后续合法连接被无谓限流. 另有两点: 时钟回拨 (NTP 校正)
会让 elapsed 为负, 令牌桶被抽干而长时间停止接纳; 速率低于 1/s 时桶容量不足 1, 会导致
所有连接被拒.

修复: 容量用 CAS 先判再加 (并发调用不会再超出 maxConns); 令牌桶拒绝时归还容量槽位;
elapsed 为负时钳到 0; 桶容量下限为 1.

提交: 308a01361

### 3.12 共享 WorkerPool 与 handler panic 日志 (P2, F14)

现象: TCP 的 worker pool 把 handler panic 转成 core.ErrHandlerPanic 却不记日志, 运维只能
看到会话消失而无任何线索; 与此同时其它传输层都通过 shared.CallHandler 记了日志.

修复: 把 worker pool 泛化到 core.Session 并移入 internal/transport/shared, 命名为
WorkerPool, 其 callHandler 改为委托 shared.CallHandler (recover + 日志 + 同一 sentinel).
行为与原来的 TCP pool 等价 (相同的 submit/stop 串行化, 相同的队列满策略, 相同的关停排空),
但不再重复实现. tcp 包用类型别名保留 FullPolicy / WithWorkers / Policy* 等公开名字,
对 api 层无影响.

验证: shared/worker_pool_test.go 新增 panic 用例, 断言 panic 被记录且会话被关闭; 该用例
对旧的静默实现失败 (报 "handler panic was not logged").

提交: d79bd88b2

### 3.13 run_stress 退出码 (P2, F15)

现象: 脚本只在初始化失败 (register/start 失败, 未知 mode) 时返回非 0, 运行结果完全不影响
退出码. 实测 `-host 127.0.0.1:1` 时输出 "sent=0 recv=0" 但退出码为 0, 脚本与 CI 无法据此
判定失败.

修复: 打印汇总后按模式判定: 未发出任何消息, 未收到任何回复, 或发送/接收错误率超过 5%,
则逐条输出原因并以状态 1 退出. 结果 map 无序, 因此排序后再输出, 保证输出稳定.

附带修复: throughput 仅在收到回复时才存在, 失败场景下 printSummary 打印出
"throughput=%!s(<nil>)", 现回退为 n/a.

验证: 不可达主机场景退出码 1 且给出明确原因; 本地 all 模式退出码 0 且三模式 PASSED.

提交: 2706afca8

### 3.14 tls_server 示例无法启动 (P2, F16)

现象: examples/tls_server 内嵌的证书/私钥 PEM 是手工拼凑的, base64 中间混入空格
("BwM CMAkGA1UdEw"), 启动即报 "x509: malformed certificate" 并以 1 退出. 该示例从来
没有可用过. 且函数名叫 generateSelfSignedCert 却只是解析内嵌字节.

修复: 真正在启动时生成证书 (ECDSA P-256 自签, SAN 覆盖 localhost 与 127.0.0.1). 这同时
避免把私钥写进仓库.

验证: examples/tls_server/main_test.go 断言证书可解析, 对两个 SAN 均有效, 并以自身为根
完成一次真实 TLS 握手; 示例可正常监听 18005.

提交: 4b4237f04

### 3.15 CI 相关 (P1, F17 / F18)

| 缺陷 | 证据 | 修复 |
| --- | --- | --- |
| lint 门禁红灯 | golangci-lint 报 4 个问题: 1 个 QF1001 (grpcweb/framing.go 的 De Morgan) 与 3 个 QF1006 (轮询循环可提到循环条件). 其中 3 个在基线 0f5c54237 就存在, 说明 main 上 lint 一直是红的 | framing.go 抽出正向谓词 grpcWebSafeByte; 4 处轮询循环改为把条件放进循环头. 现在 0 issues |
| 权限过宽 | 顶层给所有 job 授予 actions: write, 而 lint / security / docker-build 都不需要; golangci-lint-action 与 govulncheck 安装都是第三方代码 | 顶层改为 contents: read, 仅 4 个上传日志的 job 单独声明 actions: write |

提交: 1a0a08eea, 55fd15201

## 4. 未修复缺陷与改进计划

以下各项未在本次修复, 原因是它们属于设计决策或需要额外环境, 需要明确取舍后再动手.

### D1 UDP / CoAP 的 handler 在 read 循环内同步执行 (P1)

现状: udp/server.go 的 readLoop 与 coap/server.go 的 readLoop 直接同步调用 handler.
一个慢 handler 会阻塞唯一的 read goroutine, 于是所有对端的数据报在内核缓冲里排队直到
溢出被丢弃. TCP 已经有 worker pool, UDP / CoAP 没有. 这是典型的队头阻塞.

建议: 复用本轮已经建好的 shared.WorkerPool. 但必须先确定语义: 单个 worker 可保持全局
FIFO 顺序, 多 worker 会让同一会话的消息并发且可能乱序, 这对 CoAP 的 dedup / Observe
逻辑有影响. 建议第一阶段 WorkerCount 默认 1 (等价于当前顺序, 但 read 循环不再被阻塞,
丢包由队列背压取代), 把并发度做成显式配置并在文档中说明顺序代价. 预计改动: udp 与
coap 各新增选项 + 约 20 行接线 + 回归测试.

### D2 部署校验停留在子串匹配 (P2)

现状: tests/deploy/deploy_test.go 用 assertContains 校验 YAML 文本. 一个结构已损坏但
关键字齐全的清单仍会通过. 且不做跨文件一致性校验, 例如 Dockerfile 的 EXPOSE 与
Service 端口, probe 端口与 containerPort, ConfigMap 键与应用实际读取的键.

建议: 引入 YAML 解析后做语义断言. 注意当前 go.mod 未直接依赖 yaml.v3 (仅出现在
go.sum 的间接依赖中), 需要先在云端或本地确认可离线获取该模块再落地.

### D3 覆盖率缺少 -coverpkg (P2)

现状: scripts/run_tests.go 的 cover 模式只对生产包跑 go test, 不计入 ./tests/... 的
集成与压力测试对生产代码的覆盖. 一个没有任何包内测试的生产包会显示 0%, 而门限仍可能
通过, 指标偏保守且具误导性.

建议: 增加 -coverpkg=./api/...,./internal/...,./cmd/... 并把 ./tests/... 纳入运行集合,
或对两套口径分别报告.

### D4 指标与健康端点在 k8s 中可达且无鉴权 (P2)

现状: /healthz, /readyz 与 Prometheus 指标端点都没有鉴权. deploy/docker/
docker-compose.yml 明确不发布 18080 / 18081 (注释说明它们无鉴权, 保持容器内), 但
deploy/k8s/service.yaml 把两个端口都发布为 ClusterIP 端口, 且 networkpolicy.yaml 的
ingress 允许这两个端口且未限定 from, 即集群内任意 Pod 都能读取. 两套部署的安全姿态
不一致.

建议: 明确取舍并统一. 若保留 Prometheus 抓取, 建议用 NetworkPolicy 的 from 限定到
监控命名空间; 若不需要对外, 从 Service 中摘除这两个端口.

### D5 GitHub Actions 使用浮动主版本标签 (P3)

现状: actions/checkout@v5, actions/setup-go@v6, actions/upload-artifact@v6,
golangci/golangci-lint-action@v9 均为可变标签.

建议: 固定到 commit SHA. 本轮未做是因为需要联网解析每个主版本对应的真实 SHA, 凭猜测
写入会让 CI 直接失效, 风险高于收益.

### D6 errcheck 在部分目录被豁免 (P3)

现状: .golangci.yml 对 internal/transport/ 与 scripts/ 关闭 errcheck, 因此传输层未检查
的返回错误不会被报告.

建议: 逐目录收敛豁免范围, 优先恢复 internal/transport/ 的 errcheck, 这些 defer 里的
Close 错误实际上有观测价值.

### D7 Windows CI 不跑集成模式 (P3)

现状: validate-windows 只跑 unit / vet / deploy, 因此 ./tests/... 的 Windows 侧回归只会在
Linux 上暴露. 判断为有意为之 (压力测试会耗尽 Windows 临时端口, 集成模式用 -p 1 串行化).

建议: 在 Windows 上单独加一个仅跑非压力子集的 job, 或明确在文档中记录该缺口.

### D8 云端编译与容器编排验证 (待执行)

任务要求第 6 项 (云服务器编译验证, Docker 与 K8s 部署测试并记录) 尚未完成. 本地 docker
与 kubectl / helm 均不可用 (deploy 测试中相应分支被跳过), 因此:

- Dockerfile / compose 的实际构建与运行
- kustomize 与 helm template 的实际渲染
- 容器内 read_only 根文件系统下应用是否可正常写数据目录
- k8s 探针是否真的通过

以上均需在 8.130.190.141 上验证, 结果应单独记录到 docs/reports/DEPLOYMENT-VALIDATION-*.md,
不混入本报告.

## 5. CI 现状

修复后各 job 的本地等价验证结果:

| job | 本地等价命令 | 结果 |
| --- | --- | --- |
| lint | golangci-lint run --max-same-issues 0 --max-issues-per-linter 0 | 0 issues |
| security | govulncheck ./... | 本机未安装, 未验证 |
| docker-build | docker build + healthz 冒烟 | 本机无 docker, 未验证 |
| validate-ubuntu | unit + integration + vet + deploy | 全部 EXIT 0 |
| validate-windows | unit + vet + deploy | 全部 EXIT 0 |
| race | go run scripts/run_tests.go -mode race | 见 2.7 |
| coverage | go run scripts/run_tests.go -mode cover | 80.4%, EXIT 0 |

说明: 本机 golangci-lint 为 2.11.3, CI 固定 v2.12.2. 已用 --max-same-issues 0 打开全部
同类问题, 输出的 QF1001 / QF1006 在两版本上行为一致, 因此判定 CI 也会通过. govulncheck
与 docker 两项需要在云端补验.

## 6. 可重入性评估

结论: 本轮改动没有引入任何包级可变状态, 因此没有降低可重入性; 新增的两个共享组件都
是实例内自持有状态.

逐项核查:

- shared/backoff.go: 只有常量与类型, 无包级变量. Backoff 的 delay 由所属循环独占, 多个
  传输实例互不影响.
- shared/worker_pool.go: 无包级变量. queue / done / wg 都在实例内创建, 每个 Server 拥有
  独立的 pool 实例.
- internal/transport/tcp/policy.go: 仅类型别名与常量.
- examples/tls_server: 每次调用生成独立密钥, 无共享状态.
- scripts/run_stress.go: 新增的判定函数是纯函数 (输入 results, 输出原因列表).

已有的可重入性保障未被削弱:

- Server 的 Start / Stop 仍由 atomic.CompareAndSwap 守卫, 重复调用是安全的空操作.
- NewServer 复制 Options, 配置不共享.
- PluginChain 仍是 copy-on-write.
- tcp.Client 由无同步变为 RWMutex 保护, 是单向增强.

已知边界 (本轮未改变, 建议在文档中明确): Server 实例不支持 Stop 之后再次 Start;
WorkerPool 的 closed 是单向标志, Stop 之后 Start 不会恢复工作. 这与现有的 Server
生命周期约定一致, 但"可重入"若被理解为"同一实例可反复启停", 则当前实现不满足, 建议在
架构文档里把该语义写清楚.

## 7. 建议的后续顺序

1. D8 云端编译与 Docker / K8s 端到端验证 (任务硬要求, 且能暴露 read_only 根文件系统
   之类的真实问题).
2. D1 UDP / CoAP 队头阻塞: 先定顺序语义, 再复用 shared.WorkerPool.
3. D4 指标与健康端点的可达性口径统一 (安全姿态).
4. D3 覆盖率口径, D2 部署校验语义化.
5. D5 / D6 / D7 工程卫生.

## 8. 变更清单

按提交顺序:

| commit | 说明 |
| --- | --- |
| 457d20830 | style: timeout.go 补结尾换行 (gofmt) |
| b63e37b4d | style: 测试文件 gofmt 对齐 |
| 7d9e84481 | fix(coap): RFC 7252/7641 一致性 (ping, RST 关联, Observe=0) |
| 325742d99 | fix(transport): QUIC Drain 死锁, gRPC-Web 分帧与写超时 |
| 43a46122f | chore: 清理一次失败的就地编辑留下的临时文件 |
| 3c5423ac9 | fix(infra): MQTT 重订阅, Bolt 打开超时, CA 热更新, breaker/cache/pubsub |
| 384681d62 | fix(runtime,plugin): 插件生命周期 panic 隔离与插件状态修正 |
| 308a01361 | fix(transport): accept/read 循环退避, acceptor 记账 |
| 058bd6d63 | style: mqtt 测试 gofmt 对齐 |
| 30cb29584 | fix(transport): 对端断开时释放 DTLS 连接 |
| d56ade6b5 | fix(tcp): Client 并发安全与旧连接释放 |
| d79bd88b2 | refactor(transport): worker pool 移入 shared 并记录 handler panic |
| 2706afca8 | fix(scripts): run_stress 通过退出码上报失败 |
| 4b4237f04 | fix(examples): tls_server 改为真正生成证书 |
| 1a0a08eea | fix(lint): 清掉导致 lint 红灯的 staticcheck 问题 |
| 55fd15201 | ci: 把 actions: write 收敛到上传日志的 job |
