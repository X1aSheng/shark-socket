# shark-socket 项目深度审查报告 (V5)

- 日期: 2026-08-06 03:46
- 范围: 全部 155 个 Go 源文件 + 部署清单 + CI
- 方法: 5 个并行审查代理深度审查 + 本人逐文件复核 + 复现测试实证
- 基线: build OK, vet OK, 单元测试 21 包全过, 集成测试过, race 干净, 覆盖率 74.4%

## 1. 测试基线

| 检查项 | 结果 |
| --- | --- |
| go build ./... | PASS |
| go vet ./... | PASS |
| 单元测试 (./api ./cmd/... ./internal/...) | 21 包全部 PASS |
| 集成测试 (./tests/...) | PASS |
| race 检测 (CGO=1) | PASS, 0 data race |
| 覆盖率 (./...) | 74.4% (门槛 70%) |
| 脚本测试器 (scripts/run_tests.go -mode all) | PASS |

日志: logs/2026-08-06T03-16-01_{unit,integration,benchmark}.log

## 2. 缺陷清单 (按严重程度, 已去重)

### P0 - 进程崩溃

| # | 缺陷 | 位置 |
| --- | --- | --- |
| P0-1 | workerPool.submit 阻塞发送与 stop() close(p.queue) 竞争, "send on closed channel" panic | internal/transport/tcp/worker_pool.go:56-91 |
| P0-2 | TCP/QUIC session.Send 与 Close 并发, 对已关闭 writeCh 发送 panic | internal/transport/tcp/session.go:69-96, quic/session.go |

### P1 - 功能损坏 / 永久 DoS

| # | 缺陷 | 位置 |
| --- | --- | --- |
| P1-1 | UDP/CoAP 会话楔死: handler/插件错误路径只 Close 不从 sessions 移除, 对端永久卡死 | udp/server.go:243-254, coap/server.go:268-292 |
| P1-2 | CoAP 空 payload (标准 GET) 永不进入 Handler/Responder | coap/server.go:280-292 |
| P1-3 | gRPC-Web 原始请求体误判为帧, 原始客户端收到帧化响应; trailer 内容丢弃 | grpcweb/framing.go:39-69, server.go:165-171 |
| P1-4 | Gateway.Start/Stop 无公共锁, 重复/并发调用状态脱节, Stop 后健康服务器仍监听但 started=false | runtime/gateway.go:68-135 |
| P1-5 | SessionManager.Broadcast/Range 持 RLock 执行用户 Send, 可死锁或全局阻塞 | runtime/session_manager.go:89-107 |
| P1-6 | TCP 无读超时 + 默认连接数无限 → slowloris 资源耗尽 | tcp/session.go:98-108, options.go |
| P1-7 | QUIC stream ReadAll 无 deadline + 连接/流数无限 → goroutine 耗尽 | quic/server.go:170-190, options.go |

### P2 - 边界 / 泄漏 / 协议不符

| # | 缺陷 | 位置 |
| --- | --- | --- |
| P2-1 | LwM2M Server.Write 持独占锁调用 OnWrite 回调 → 可重入死锁, 且持锁做网络 I/O | protocol/lwm2m/server.go:124-149 |
| P2-2 | LwM2M TLV 整型/浮点静默截断 (数据损坏) | protocol/lwm2m/codec_tlv.go:121-137 |
| P2-3 | PluginChain.OnAccept 中途失败, 已 accept 插件不收 OnClose (泄漏) | runtime/plugin_chain.go:43-52 |
| P2-4 | UDP/CoAP getOrCreateSession 发布 id=0 会话后才赋 ID (data race + 已关闭会话注册) | udp/server.go:280-304, coap/server.go:423-446 |
| P2-5 | Gateway.Health Stop 后仍报过期 uptime; Stop 期间 readyz 恒 true; Register 停机中可成功 | runtime/gateway.go:131-149 |
| P2-6 | App.Start 在 Gateway.Start 失败时不回收 health/metrics HTTP server; 二次 Start 泄漏 | internal/app/app.go:65-79 |
| P2-7 | 证书 watcher 用 context.Background() 启动, 生命周期与 app 脱节, 重启后不重建 | internal/app/app.go:214-228 |
| P2-8 | WebSocket PongTimeout 死配置, 死连接永不回收 | websocket/options.go, server.go:209-224 |
| P2-9 | HTTP responseRecorder 每响应整份缓存进内存且永不消费 | http/session.go:79-103 |
| P2-10 | Framer 零值 = 无长度上限, 恶意长度前缀 OOM; Options.MaxFrameBytes 死配置 | tcp/framer.go:25-75, options.go:22 |
| P2-11 | CoAP observe 用 payload 当资源名而非 URI-Path option; RST 不清理注册 (RFC 7641 不符) | coap/server.go:303-341 |
| P2-12 | UDP/CoAP 单 readLoop 同步执行 handler → 队头阻塞, 全 peer 相互拖垮 | udp/server.go:226-257, coap/server.go:229-247 |
| P2-13 | MQTT Adapter.Start TOCTOU 并发启动双重连接泄漏 | infra/mqtt/adapter.go:43-80 |
| P2-14 | Persistence 消息日志无界增长, 从不 Prune | plugin/persistence.go, store/message_log.go |
| P2-15 | CertCache.Load 热更新非原子, 证书与 CA 池状态不一致 | infra/tlsutil/cert_cache.go:35-56 |
| P2-16 | CircuitBreaker 无超时/panic 保护, half-open 探测阻塞可永久卡死熔断器 | infra/circuitbreaker/circuitbreaker.go |
| P2-17 | AutoBan.Record 无生产调用点, AutoBan 完全不生效 | plugin/autoban.go |
| P2-18 | Cluster.Start/Stop 数据竞争与重复订阅 | plugin/cluster.go:54-83 |
| P2-19 | RateLimit/AutoBan/Heartbeat Start/Stop WaitGroup 误用竞态 | plugin/{ratelimit,autoban,heartbeat}.go |
| P2-20 | gRPC-Web Stop 顺序错误, Drain 必然超时 (websocket 读循环等 CloseSessions) | grpcweb/server.go:115-130 |

### P3 - 健壮性

| # | 缺陷 | 位置 |
| --- | --- | --- |
| P3-1 | Gateway.Start 回滚用 context.Background(), 不回滚当前失败 server | runtime/gateway.go:82-88 |
| P3-2 | 仅设 SHARK_HTTP_ALLOWED_ORIGINS 时 upsert 空 addr 协议导致启动失败 | app/config.go:159-167 |
| P3-3 | PluginChain.SetLogger 无锁写, 潜在 data race | runtime/plugin_chain.go:23-27 |
| P3-4 | CoAP 去重窗口 5 分钟与 16-bit MessageID 回绕冲突; 重复 CON 回空 CodeValid 而非缓存响应 | coap/server.go:255-261, 408-420 |
| P3-5 | HTTP handler 先写响应后报错 → 200 + 错误正文 | http/server.go:191-204 |
| P3-6 | 遗留临时复现测试文件可破坏 CI 构建 (zz_/tmp_*.go) | internal/transport/... |
| P3-7 | 环境无 mosquitto/docker, 集成测试自动跳过 MQTT 依赖用例, 本地验证受限 | - |

## 3. 改进计划 (优先级排序)

1. **P0-1/P0-2** worker pool 与 session 关闭竞态 panic - 用 done channel 代替 close(queue/writeCh), select 化发送/消费
2. **P1-1** UDP/CoAP 会话楔死 - 错误路径调用 closeSession (含 sessions 移除 + Unregister)
3. **P1-2** CoAP 空 payload - 去掉 len(payload)>0 门控
4. **P1-3** gRPC-Web 帧检测 - 以 content-type/x-grpc-web 判定, 原始请求直通
5. **P1-4** Gateway Start/Stop 同步与幂等
6. **P1-5** SessionManager.Broadcast 快照后发送, 不持锁执行回调
7. **P1-6/P1-7** TCP/QUIC 读超时与连接上限
8. **P2-1/P2-2** LwM2M 死锁与 TLV 截断
9. **P2-3** PluginChain OnAccept 失败回调 OnClose
10. **P2-10** Framer 长度上限与 MaxFrameBytes 选项
11. **P2-8** WebSocket Pong 校验
12. **P2-17** AutoBan 接线
13. **其余 P2/P3** 视复杂度逐个处理

每个缺陷的修复均附回归测试, 修复后全量测试 + race 验证, 逐个提交。
