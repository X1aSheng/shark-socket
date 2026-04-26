# Shark-Socket 测试覆盖文档

## 测试架构

```
shark-socket/
├── tests/
│   ├── unit/                 # 跨模块单元测试
│   ├── integration/          # 端到端集成测试
│   └── benchmark/            # 性能基准测试
└── internal/
    ├── defense/*_test.go     # 防御模块测试
    ├── errs/*_test.go        # 错误定义测试
    ├── gateway/*_test.go     # 网关模块测试
    ├── infra/*_test.go       # 基础设施测试
    ├── plugin/*_test.go      # 插件系统测试
    ├── protocol/
    │   ├── tcp/*_test.go     # TCP 协议测试
    │   ├── udp/*_test.go     # UDP 协议测试
    │   ├── http/*_test.go    # HTTP 协议测试
    │   ├── websocket/*_test.go # WebSocket 协议测试
    │   └── coap/*_test.go    # CoAP 协议测试
    ├── session/*_test.go     # 会话管理测试
    ├── types/*_test.go       # 类型系统测试
    └── utils/*_test.go       # 工具库测试
```

## 测试统计

| 层级 | 测试文件数 | Test 函数 | Benchmark 函数 |
|------|-----------|-----------|----------------|
| tests/unit | 3 | 24 | 0 |
| tests/integration | 2 | 16 | 0 |
| tests/benchmark | 2 | 0 | 24 |
| internal/defense | 3 | 14 | 0 |
| internal/errs | 1 | 7 | 0 |
| internal/gateway | 2 | 7 | 0 |
| internal/infra | 8 | 52 | 7 |
| internal/plugin | 6 | 28 | 6 |
| internal/protocol/tcp | 9 | 58 | 4 |
| internal/protocol/udp | 4 | 14 | 0 |
| internal/protocol/http | 4 | 19 | 0 |
| internal/protocol/websocket | 4 | 20 | 0 |
| internal/protocol/coap | 5 | 27 | 0 |
| internal/session | 4 | 30 | 5 |
| internal/types | 4 | 7 | 0 |
| internal/utils | 3 | 18 | 0 |
| **合计** | **60** | **335** | **46** |

---

## 1. 跨模块单元测试 (tests/unit)

### infra_test.go — 基础设施与类型校验

| 函数 | 说明 |
|------|------|
| `TestProtocolTypes_All` | 验证所有协议类型的 String() 表示非空 |
| `TestSessionStates_All` | 验证所有会话状态的 String() 表示非空 |
| `TestMessageTypes_All` | 验证所有消息类型的 String() 表示非空 |
| `TestNewRawMessage` | 测试 RawMessage 创建包含正确的会话ID/协议/负载 |
| `TestDefaultPorts` | 验证所有协议服务器的默认端口 |
| `TestOptions_Addr` | 测试不同协议的选项配置 |
| `TestPluginInterface` | 验证基础插件接口实现 |
| `TestWebSocketOptions_All` | WebSocket 服务器配置选项全面测试 |
| `TestCoAPOptions_All` | CoAP 服务器配置选项全面测试 |
| `TestTCPOptions_All` | TCP 服务器配置选项全面测试 |
| `TestUDPOptions_All` | UDP 服务器配置选项全面测试 |
| `TestHTTPOptions_All` | HTTP 服务器配置选项全面测试 |

### session_test.go — 会话管理单元测试

| 函数 | 说明 |
|------|------|
| `TestSessionManager_RegisterGet` | 基本会话注册与检索 |
| `TestSessionManager_Unregister` | 会话移除 |
| `TestSessionManager_Count` | 会话计数准确性 |
| `TestSessionManager_All` | 遍历所有会话 |
| `TestSessionManager_Broadcast` | 消息广播到所有会话 |
| `TestSessionManager_LRUEviction` | 超过最大会话数时的 LRU 淘汰 |
| `TestSessionManager_Range` | 会话范围迭代 |
| `TestSessionManager_Range_EarlyStop` | 范围迭代提前终止 |
| `TestSessionManager_Close` | 关闭所有会话 |
| `TestSessionManager_RegisterAfterClose` | 关闭后注册返回错误 |
| `TestSessionManager_NextID_Monotonic` | ID 生成单调递增 |
| `TestSessionManager_MultiProtocol` | 跨协议会话管理 |

---

## 2. 端到端集成测试 (tests/integration)

### data_comm_test.go — 数据通信完整性测试

| 函数 | 说明 |
|------|------|
| `TestTCP_EchoIntegrity` | TCP 回声完整性：8种负载模式（空、单字节、全零、全FF、递增、递减、交替、32KB大包） |
| `TestTCP_MultiClientConcurrent` | TCP 多客户端并发：20 客户端 × 50 消息无数据混淆 |
| `TestTCP_PingPongIntegrity` | TCP 请求-响应序列完整性：200 条消息严格顺序验证 |
| `TestTCP_LargePayloadRoundTrip` | TCP 大包传输：1KB/8KB/64KB/128KB 逐字节验证 |
| `TestUDP_EchoIntegrity` | UDP 回声完整性：4种负载模式验证 |
| `TestUDP_MultiClientConcurrent` | UDP 多客户端并发：10 客户端 × 30 消息 |
| `TestWebSocket_EchoIntegrity` | WebSocket 回声完整性：文本+二进制消息，256B 到 16KB |
| `TestWebSocket_MultiClientConcurrent` | WebSocket 多客户端并发：10 客户端 × 30 消息 |
| `TestHTTP_RequestResponse` | HTTP 请求-响应：查询参数回声 + 二进制 body 回声 |
| `TestHTTP_ConcurrentRequests` | HTTP 并发请求：20 客户端 × 50 请求带计数验证 |
| `TestCoAP_MessageRoundTrip` | CoAP 消息序列化/反序列化往返 |
| `TestGateway_CrossProtocolCommunication` | 网关跨协议通信：TCP+WS+HTTP+UDP 同时运行 |
| `TestGateway_CrossProtocolStress` | 网关跨协议压力：5 客户端 × 4 协议同时并发 |

### multi_protocol_test.go — 多协议网关集成测试

| 函数 | 说明 |
|------|------|
| `TestMultiProtocol_AllProtocols` | TCP + HTTP + WebSocket 通过网关联合运行验证 |
| `TestMultiProtocol_UDPAndCoAP` | UDP + CoAP 数据报交换验证 |
| `TestGateway_GracefulShutdown` | 网关优雅关闭：超时 + 端口释放验证 |

---

## 3. 性能基准测试 (tests/benchmark)

### data_comm_bench_test.go — 数据通信性能测试

| 函数 | 说明 |
|------|------|
| `BenchmarkTCPEcho_MultiSize` | TCP 多尺寸负载：64B → 1KB → 4KB → 16KB → 64KB |
| `BenchmarkTCPEcho_BinaryIntegrity` | TCP 二进制完整性：全零/全FF/递增/递减/随机模式 |
| `BenchmarkTCPEcho_ParallelMultiClient` | TCP 多客户端并行吞吐量 |
| `BenchmarkTCPEcho_Burst` | TCP 突发流量：50 消息连续发送 |
| `BenchmarkTCPEcho_PingPong` | TCP 请求-响应延迟基准 |
| `BenchmarkUDPEcho_MultiSize` | UDP 多尺寸负载：64B → 256B → 1024B → 1400B |
| `BenchmarkWSEcho` | WebSocket 基础回声性能 |
| `BenchmarkWSEcho_MultiSize` | WebSocket 多尺寸：64B → 64KB |
| `BenchmarkWSEcho_Parallel` | WebSocket 多客户端并行 |
| `BenchmarkHTTPEcho` | HTTP GET 回声性能 |
| `BenchmarkHTTP_Throughput` | HTTP POST 吞吐量 |
| `BenchmarkHTTP_Concurrent` | HTTP 并发请求基准 |
| `BenchmarkGateway_MultiProtocol` | 网关跨协议联合吞吐量 (TCP+WS+HTTP) |

### protocol_bench_test.go — 协议层基准测试

| 函数 | 说明 |
|------|------|
| `BenchmarkSessionManager_NextID` | 会话 ID 生成吞吐量 |
| `BenchmarkSessionManager_NextID_Parallel` | 并行 ID 生成竞争测试 |
| `BenchmarkShardedMap_Set` | 分片 Map 写入性能 |
| `BenchmarkShardedMap_Get` | 分片 Map 读取性能 |
| `BenchmarkShardedMap_SetGet_Parallel` | 分片 Map 并发读写 |
| `BenchmarkBufferPool_AllLevels` | 缓冲池全级别（64B ~ 128KB）Get/Put |
| `BenchmarkBufferPool_Parallel` | 缓冲池并发访问 |
| `BenchmarkTCPEcho` | TCP 基础回声延迟 |
| `BenchmarkUDPEcho` | UDP 基础回声延迟 |
| `BenchmarkCoAP_ParseMessage` | CoAP 消息解析性能 |
| `BenchmarkCoAP_Serialize` | CoAP 消息序列化性能 |

---

## 4. 防御模块 (internal/defense)

### backpressure_test.go — 背压控制

| 函数 | 说明 |
|------|------|
| `TestCheckQueueLevel_OK` | 正常队列级别返回 OK |
| `TestCheckQueueLevel_Warn` | 高队列使用率返回 Warn |
| `TestCheckQueueLevel_Close` | 队列满时返回 Close |
| `TestCheckQueueLevel_ZeroCapacity` | 零容量边界处理 |
| `TestBroadcastLimiter_Allow` | 广播限流器正常放行 |
| `TestBroadcastLimiter_DenyOversized` | 拒绝超限广播 |
| `TestBroadcastLimiter_EnforcesMinInterval` | 最小间隔强制执行 |
| `TestBroadcastLimiter_DifferentTopics` | 不同主题独立限流 |

### overload_test.go — 过载保护

| 函数 | 说明 |
|------|------|
| `TestNewOverloadProtector` | 过载保护器创建 |
| `TestIsOverloaded_InitiallyFalse` | 初始状态非过载 |
| `TestHighWater_TriggersOverload` | 高水位触发过载 |
| `TestLowWater_RecoversFromOverload` | 低水位恢复正常 |
| `TestStop_StopsCheckLoop` | 停止检测循环 |

### sampler_test.go — 日志采样器

| 函数 | 说明 |
|------|------|
| `TestShouldLog_FirstTime` | 首次日志允许通过 |
| `TestShouldLog_SameKeyWithinWindow` | 窗口内重复日志抑制 |
| `TestShouldLog_AfterWindow_ReturnsTrueWithSummary` | 窗口过后恢复并附带摘要 |
| `TestShouldLog_DifferentKeys` | 不同 Key 独立采样 |
| `TestClean_RemovesStaleEntries` | 清理过期条目 |

---

## 5. 错误定义 (internal/errs)

| 函数 | 说明 |
|------|------|
| `TestAllErrorsNonNil` | 所有预定义错误非 nil |
| `TestAllErrorsHaveSharkPrefix` | 所有错误含 "shark:" 前缀 |
| `TestIsRetryable` | 可重试错误识别 |
| `TestIsFatal` | 致命错误识别 |
| `TestIsRecoverable` | 可恢复错误识别 |
| `TestIsSecurityRejection` | 安全拒绝错误识别 |
| `TestIsPluginControl` | 插件控制错误识别 |

---

## 6. 网关模块 (internal/gateway)

### gateway_test.go

| 函数 | 说明 |
|------|------|
| `TestNew` | 网关创建 |
| `TestRegister` | 服务器注册 + 重复协议拒绝 |
| `TestStart_NoServers` | 无服务器时启动报错 |
| `TestStartStopLifecycle` | 完整启停生命周期 |
| `TestProtocol` | 网关返回 Custom 协议类型 |
| `TestManager_BeforeStart` | Start 前 Manager 为 nil |

### integration_test.go

| 函数 | 说明 |
|------|------|
| `TestIntegration_Gateway_MultiProtocol` | TCP + HTTP 多协议网关联合验证 |
| `TestIntegration_Gateway_StopIdempotent` | 重复 Stop 安全性 |

---

## 7. 基础设施 (internal/infra)

### bufferpool/ — 缓冲池

**pool_test.go**

| 函数 | 说明 |
|------|------|
| `TestGetLevel` | 大小到级别的映射 |
| `TestGet_ReturnsBufferAtLeastRequestedSize` | Get 返回满足大小的缓冲区 |
| `TestPut_ReturnsBufferToPool` | Put 回收到池 |
| `TestPut_ZeroesData` | Put 清零敏感数据 |
| `TestPut_DiscardsHugeBuffers` | 丢弃超大缓冲区 |
| `TestGet_PutsHugeAllocCounter` | 超大分配计数 |
| `TestBufferPool_ConcurrentGetPut` | 并发 Get/Put |
| `TestBuffer_Cap` | 缓冲区容量 |
| `TestBuffer_Len` | 缓冲区长度 |
| `TestDefaultPool` | 全局单例池 |

**bench_test.go**

| 函数 | 说明 |
|------|------|
| `BenchmarkPoolGetPutByLevel` | 各级别 Get+Put 性能 |
| `BenchmarkBufferPoolDirectAlloc` | 直接分配对比基准 |
| `BenchmarkBufferPoolParallel` | 并发池访问 |
| `BenchmarkBufferPoolGetLevel` | 级别映射性能 |
| `BenchmarkBufferPoolDefaultSingleton` | 全局单例池性能 |
| `BenchmarkBufferPoolHugeAlloc` | 超大分配性能 |
| `BenchmarkBufferPoolGetPut` | 基础 Get/Put 基准 |

### cache/ — 内存缓存

| 函数 | 说明 |
|------|------|
| `TestMemoryCache_SetGetDel` | 基本 Set/Get/Del |
| `TestMemoryCache_GetMissingKey` | 缓存未命中 |
| `TestMemoryCache_Exists` | Key 存在性检查 |
| `TestMemoryCache_TTLExpiry` | TTL 过期淘汰 |
| `TestMemoryCache_TTLMethod` | TTL 查询方法 |
| `TestMemoryCache_Exists_Expired` | 过期条目存在性检查 |
| `TestMemoryCache_MGet` | 批量 Get |
| `TestMemoryCache_MGet_EmptyKeys` | 空 Key 列表处理 |
| `TestMemoryCache_ConcurrentReadWrite` | 并发读写 |
| `TestMemoryCache_ConcurrentExistsAndTTL` | 并发 Exists + TTL |
| `TestMemoryCache_Close_StopsCleanup` | 关闭停止清理 |
| `TestMemoryCache_CleanupRemovesExpired` | 主动清理过期条目 |

### circuitbreaker/ — 熔断器

| 函数 | 说明 |
|------|------|
| `TestNew_CircuitStartsClosed` | 初始状态为 Closed |
| `TestThreshold_ReachesOpen` | 失败达阈值后打开 |
| `TestDo_ReturnsErrCircuitOpen_WhenOpen` | Open 状态返回熔断错误 |
| `TestHalfOpen_TransitionAfterTimeout` | 超时后进入 Half-Open |
| `TestHalfOpen_SuccessTransitionsToClosed` | Half-Open 成功 → Closed |
| `TestHalfOpen_FailureTransitionsBackToOpen` | Half-Open 失败 → Open |
| `TestReset_ForcesBackToClosed` | 强制重置为 Closed |

### logger/ — 日志

| 函数 | 说明 |
|------|------|
| `TestNewSlogLogger_Creation` | slog 日志器创建 |
| `TestNopLogger_MethodsDontPanic` | 空日志器方法安全 |
| `TestWith_CreatesChildLogger` | 子日志器带属性 |
| `TestWithContext_ExtractsFields` | 上下文字段提取 |

### metrics/ — 指标

| 函数 | 说明 |
|------|------|
| `TestNewPrometheusMetrics_Creation` | Prometheus 指标创建 |
| `TestCounter_BasicOperations` | Counter 自增/累加 |
| `TestCounter_CachedOnSecondCall` | Counter 缓存 |
| `TestGauge_BasicOperations` | Gauge 设置/增减 |
| `TestHistogram_BasicOperations` | Histogram 观测 |
| `TestTimer_BasicOperations` | Timer 计时观测 |
| `TestNopMetrics_NoPanic` | 空指标器安全性 |

### pubsub/ — 发布订阅

| 函数 | 说明 |
|------|------|
| `TestPublishSubscribe_BasicCommunication` | 基本发布/订阅 |
| `TestPublishSubscribe_MultipleSubscribersFanOut` | 多订阅者扇出 |
| `TestUnsubscribe_StopsDelivery` | 取消订阅停止投递 |
| `TestClose_RejectsFurtherOperations` | 关闭拒绝后续操作 |
| `TestPublish_NoSubscribers_NoError` | 无订阅者发布不报错 |
| `TestPublishSubscribe_ConcurrentStress` | 并发压力测试 |

### store/ — KV 存储

| 函数 | 说明 |
|------|------|
| `TestMemoryStore_SaveLoadDelete` | 基本 Save/Load/Delete |
| `TestMemoryStore_LoadNonexistent` | 不存在 Key 返回 nil |
| `TestMemoryStore_DeleteNonexistent` | 删除不存在 Key 安全 |
| `TestMemoryStore_SaveOverwrite` | 覆盖已有 Key |
| `TestMemoryStore_QueryPrefixMatch` | 前缀匹配查询 |
| `TestMemoryStore_QueryNoMatch` | 无匹配查询 |
| `TestMemoryStore_QueryEmptyPrefix` | 空前缀查询 |
| `TestMemoryStore_QueryReturnsCopy` | 查询返回数据副本 |
| `TestMemoryStore_ConcurrentAccess` | 并发读写 |

### tracing/ — 链路追踪

| 函数 | 说明 |
|------|------|
| `TestNopTracer_StartSpan` | 空追踪器 Span 创建 |
| `TestNopTracer_WithAttributes` | 属性附加 |
| `TestNopSpan_MultipleEnd` | 多次 End 安全 |
| `TestNopSpan_Context` | Span 上下文处理 |
| `TestWithSpanContext_RoundTrip` | Span 上下文往返 |
| `TestSpanFromContext_Missing` | 空上下文处理 |
| `TestSpanFromContext_WrongType` | 类型错误处理 |
| `TestWithSpanContext_Overwrite` | 上下文覆盖 |
| `TestWithAttribute` | 单属性设置 |
| `TestWithAttribute_Multiple` | 多属性设置 |

---

## 8. 插件系统 (internal/plugin)

### chain_test.go — 插件链

| 函数 | 说明 |
|------|------|
| `TestNewChain_SortsByPriority` | 按优先级排序 |
| `TestChain_OnAccept_ErrBlock_Interrupts` | ErrBlock 中断 OnAccept |
| `TestChain_OnMessage_ErrSkip_ReturnsOriginalData` | ErrSkip 返回原始数据 |
| `TestChain_OnMessage_ErrDrop_ReturnsNilData` | ErrDrop 返回 nil |
| `TestChain_OnMessage_DataPassesThrough` | 数据逐层传递变换 |
| `TestChain_OnClose_ReverseOrder` | OnClose 反序调用 |
| `TestChain_EmptyChain_PassesThrough` | 空链直接通过 |

### chain_bench_test.go — 插件链性能

| 函数 | 说明 |
|------|------|
| `BenchmarkPluginChain_Empty` | 空链开销 |
| `BenchmarkPluginChain_5Plugins` | 5 插件吞吐量 |
| `BenchmarkPluginChain_10Plugins` | 10 插件吞吐量 |
| `BenchmarkPluginChain_OnAccept_5Plugins` | OnAccept 路径 5 插件 |
| `BenchmarkPluginChain_OnClose_5Plugins` | OnClose 路径 5 插件 |
| `BenchmarkPluginChain_NewChain` | 链创建开销 |
| `BenchmarkPluginChain_Parallel` | 并发 OnMessage |

### ratelimit_test.go — 速率限制

| 函数 | 说明 |
|------|------|
| `TestRateLimitPlugin_UnderLimitPasses` | 限额内放行 |
| `TestRateLimitPlugin_OverGlobalLimit_ReturnsErrBlock` | 超全局限额拒绝 |
| `TestRateLimitPlugin_OverPerIPMessageLimit_ReturnsErrDrop` | 超单 IP 限额丢弃 |
| `TestRateLimitPlugin_NamePriority` | 插件名称/优先级 |
| `TestRateLimitPlugin_ViolationsRecorded` | 违规记录 |
| `TestRateLimitPlugin_OnCloseNoop` | OnClose 空操作 |

### blacklist_test.go — IP 黑名单

| 函数 | 说明 |
|------|------|
| `TestBlacklistPlugin_ExactIPBlocking` | 精确 IP 封禁 |
| `TestBlacklistPlugin_CIDRRangeBlocking` | CIDR 段封禁 |
| `TestBlacklistPlugin_NonBlockedIPPassthrough` | 非封禁 IP 放行 |
| `TestBlacklistPlugin_AddRemove` | 动态添加/移除 |
| `TestBlacklistPlugin_AddCIDR` | 动态添加 CIDR |
| `TestBlacklistPlugin_Reload` | 配置热重载 |
| `TestBlacklistPlugin_OnMessagePassthrough` | 消息直接通过 |
| `TestBlacklistPlugin_NamePriority` | 插件名称/优先级 |
| `TestBlacklistPlugin_IPv6Blocking` | IPv6 地址封禁 |
| `TestBlacklistPlugin_TTLExpiry` | TTL 过期自动解封 |

### autoban_test.go — 自动封禁

| 函数 | 说明 |
|------|------|
| `TestAutoBanPlugin_NoBanUnderThreshold` | 阈值下不封禁 |
| `TestAutoBanPlugin_RateLimitViolationsTriggerBan` | 速率违规触发封禁 |
| `TestAutoBanPlugin_EmptyConnThreshold` | 空连接阈值触发 |
| `TestAutoBanPlugin_ProtocolErrorThreshold` | 协议错误阈值触发 |
| `TestAutoBanPlugin_NamePriority` | 插件名称/优先级 |
| `TestAutoBanPlugin_OnMessagePassthrough` | 消息透传 |

### heartbeat_test.go — 心跳检测

| 函数 | 说明 |
|------|------|
| `TestTimeWheel_AddAndTimeout` | 时间轮添加与超时 |
| `TestTimeWheel_Remove` | 时间轮移除 |
| `TestTimeWheel_ResetDelaysTimeout` | 重置延迟超时 |
| `TestTimeWheel_Stop` | 时间轮停止 |
| `TestHeartbeatPlugin_OnAcceptAddsToTimeWheel` | 接受连接加入时间轮 |
| `TestHeartbeatPlugin_OnMessageResetsTimeout` | 收消息重置超时 |
| `TestHeartbeatPlugin_OnCloseRemovesFromTimeWheel` | 关闭连接移出时间轮 |
| `TestHeartbeatPlugin_NamePriority` | 插件名称/优先级 |

---

## 9. 协议层测试 (internal/protocol)

### TCP 协议

**server_test.go — 服务器核心**

| 函数 | 说明 |
|------|------|
| `TestNewServer_Creation` | 服务器创建 |
| `TestServer_ProtocolReturnsTCP` | 返回 TCP 协议类型 |
| `TestServer_StartStopLifecycle` | 启停生命周期 |
| `TestServer_StopIsIdempotent` | 重复 Stop 安全 |
| `TestServer_StopWithoutStart` | 未启动直接 Stop |
| `TestServer_StartOnUsedPort` | 端口占用报错 |
| `TestServer_EchoTest` | 回声功能测试 |
| `TestServer_MultipleEchoMessages` | 多消息有序回声 |
| `TestServer_MultipleClients` | 多客户端顺序测试 |
| `TestServer_ClientDisconnect` | 客户端断连处理 |
| `TestServer_ManagerNotNilAfterStart` | Start 后 Manager 非 nil |
| `TestAcceptBackoff` | 连接错误指数退避 |
| `TestServer_LineFramerEcho` | 行帧器回声测试 |
| `TestServer_StopWithContextCancel` | 取消上下文停止 |

**client_test.go — 客户端**

| 函数 | 说明 |
|------|------|
| `TestNewClient_DefaultOptions` | 默认客户端选项 |
| `TestClient_WithClientTimeout` | 超时选项 |
| `TestClient_WithClientReconnect` | 重连选项 |
| `TestClient_WithClientFramer` | 帧器选项 |
| `TestClient_ConnectAndIsConnected` | 连接状态 |
| `TestClient_RemoteAddr` | 远端地址 |
| `TestClient_SendReceive_RoundTrip` | 发送/接收往返 |
| `TestClient_SendReceive_Multiple` | 多消息往返 |
| `TestClient_SendReceive_Combined` | 组合发送接收 |
| `TestClient_Close_IsSafe` | 关闭安全性 |
| `TestClient_Close_BeforeConnect` | 连接前关闭 |
| `TestClient_Send_AfterClose_ReturnsError` | 关闭后发送报错 |
| `TestClient_Receive_AfterClose_ReturnsError` | 关闭后接收报错 |
| `TestClient_Connect_UnreachableHost` | 不可达主机 |
| `TestClient_Connect_UnreachableHost_WithReconnect` | 重连不可达主机 |
| `TestClient_Send_AfterServerDisconnect_WithReconnect` | 服务器断连后重连 |
| `TestClient_Concurrent_MultipleClients` | 多客户端并发 |
| `TestClient_WithClientTLS_Nil` | TLS 为 nil 处理 |

**framer_test.go — 帧编解码**

| 函数 | 说明 |
|------|------|
| `TestLengthPrefixFramer_WriteThenRead` | 长度前缀帧写/读 |
| `TestLengthPrefixFramer_EmptyPayload` | 空负载帧 |
| `TestLengthPrefixFramer_LargePayload` | 最大尺寸帧 |
| `TestLengthPrefixFramer_FrameTooLarge` | 超大帧拒绝 |
| `TestLengthPrefixFramer_TruncatedFrame` | 截断帧检测 |
| `TestLengthPrefixFramer_ReadEmpty` | 空缓冲 EOF |
| `TestLengthPrefixFramer_MultipleFrames` | 多帧连续读取 |
| `TestLengthPrefixFramer_MaxSizeZero` | 无大小限制 |
| `TestLineFramer_WriteThenRead` | 行帧写/读 |
| `TestLineFramer_MultipleLines` | 多行读取 |
| `TestLineFramer_ReadWithoutNewline` | 无换行符处理 |
| `TestFixedSizeFramer_WriteThenRead` | 定长帧写/读 |
| `TestFixedSizeFramer_Truncated` | 定长帧截断检测 |
| `TestFixedSizeFramer_MultipleFrames` | 多定长帧读取 |
| `TestRawFramer_WriteThenRead` | 原始帧写/读 |
| `TestRawFramer_DefaultBufSize` | 默认缓冲大小 |
| `TestRawFramer_NegativeBufSize` | 负缓冲大小默认值 |
| `TestRawFramer_ReadEmpty` | 空读 EOF |
| `TestFramerInterface` | 所有帧器实现 Framer 接口 |

**session_test.go — 会话**

| 函数 | 说明 |
|------|------|
| `TestNewTCPSession_StartsActive` | 新会话 Active 状态 |
| `TestTCPSession_SendEnqueues` | Send 入队 |
| `TestTCPSession_SendOnClosedSession` | 关闭后 Send 报错 |
| `TestTCPSession_CloseDrainsWriteQueue` | Close 排空写队列 |
| `TestTCPSession_CloseIsIdempotent` | 重复 Close 安全 |
| `TestTCPSession_SendWriteQueueFull` | 写队列满报错 |
| `TestTCPSession_ContextCancelledAfterClose` | Close 后 Context 取消 |
| `TestTCPSession_MetaData` | 元数据操作 |
| `TestTCPSession_RemoteAddr` | 远端地址 |
| `TestTCPSession_SendTyped` | SendTyped 无编码器 |
| `TestTCPSession_SendTypedWithEncoder` | SendTyped 有编码器 |
| `TestTCPSession_SendTypedEncoderFailure` | 编码器失败 |
| `TestTCPSession_CloseSetsState` | Close 设置状态 |
| `TestTCPSession_ReadLoop_SubmitsToPool` | 读循环提交工作池 |

**worker_pool_test.go — 工作池**

| 函数 | 说明 |
|------|------|
| `TestWorkerPool_SubmitProcessesTask` | 任务提交处理 |
| `TestWorkerPool_MultipleTasks` | 多任务并发处理 |
| `TestWorkerPool_PolicyDrop_DiscardsWhenFull` | Drop 策略丢弃超额 |
| `TestWorkerPool_PolicyBlock_BlocksWhenFull` | Block 策略等待 |
| `TestWorkerPool_StopWaitsForWorkers` | Stop 等待工作线程 |
| `TestWorkerPool_StopIsIdempotent` | 重复 Stop 安全 |
| `TestWorkerPool_DefaultValues` | 默认工作线程数 |
| `TestWorkerPool_SafeRunRecoversPanic` | Panic 恢复 |
| `TestWorkerPool_NilHandler` | nil Handler 安全 |
| `TestWorkerPool_SpawnTempPolicy` | 临时工作线程策略 |
| `TestWorkerPool_ClosePolicy` | 关闭策略 |

**tls_reloader_test.go — TLS 证书热加载**

| 函数 | 说明 |
|------|------|
| `TestTLSReloader_InitialLoad` | 初始证书加载 |
| `TestTLSReloader_ReloadSwapsCert` | 证书热替换 |
| `TestTLSReloader_GetConfigForClient` | 客户端 TLS 配置 |
| `TestTLSReloader_ReloadFailureKeepsOldCert` | 加载失败保留旧证书 |
| `TestTLSReloader_CloseStopsWatcher` | 关闭停止文件监控 |
| `TestTLSReloader_TLSConfig` | 获取 TLS 配置 |
| `TestTLSReloader_ReloadChan` | 通过 Channel 触发重载 |

**integration_test.go — TCP 集成测试**

| 函数 | 说明 |
|------|------|
| `TestIntegration_TCP_Echo` | TCP 帧化消息回声 |
| `TestIntegration_TCP_MultipleClients` | 多客户端并发回声 |

**echo_bench_test.go — TCP 性能基准**

| 函数 | 说明 |
|------|------|
| `BenchmarkTCPEcho` | 单连接回声往返延迟 |
| `BenchmarkTCPEcho_LargeMessage` | 8KB 负载回声 |
| `BenchmarkTCPEcho_Parallel` | 多连接并发回声 |
| `BenchmarkTCPEcho_SmallMessage` | 小消息开销基准 |

### UDP 协议

**server_test.go**

| 函数 | 说明 |
|------|------|
| `TestNewUDPServer` | UDP 服务器创建 |
| `TestUDPServer_StartStop` | 启停生命周期 |
| `TestUDPServer_StopIdempotent` | 重复 Stop 安全 |
| `TestUDPServer_StopWithoutStart` | 未启动直接 Stop |
| `TestUDPServer_SessionCount` | 会话计数 |

**session_test.go**

| 函数 | 说明 |
|------|------|
| `TestNewUDPSession` | UDP 会话创建 |
| `TestUDPSession_Send` | 发送数据报 |
| `TestUDPSession_SendAfterClose` | 关闭后发送报错 |
| `TestUDPSession_CloseTransitionsState` | 关闭状态转换 |
| `TestUDPSession_CloseIdempotent` | 重复 Close 安全 |
| `TestUDPSession_SendConcurrent` | 并发发送 |
| `TestUDPSession_SendTyped` | SendTyped 方法 |
| `TestUDPSession_Metadata` | 元数据操作 |

### HTTP 协议

**server_test.go**

| 函数 | 说明 |
|------|------|
| `TestNewServer` | HTTP 服务器创建 |
| `TestNewServerWithOptions` | 带选项创建 |
| `TestHandleFunc` | 路由注册 |
| `TestHandleFunc_NotFound` | 未注册路由 404 |
| `TestServer_SetHandler_ModeB` | Mode B 处理器注册 |
| `TestServer_SetHandler_ModeB_EmptyBody` | 空 body Mode B |
| `TestServer_StartStop` | 启停生命周期 |
| `TestServer_StopWithoutStart` | 未启动直接 Stop |
| `TestServer_StopIdempotent` | 重复 Stop 安全 |
| `TestServer_Protocol` | 返回 HTTP 协议类型 |

**session_test.go**

| 函数 | 说明 |
|------|------|
| `TestNewHTTPSession` | HTTP 会话创建 |
| `TestHTTPSession_Send` | 发送到 ResponseWriter |
| `TestHTTPSession_SendTyped` | SendTyped 方法 |
| `TestHTTPSession_SendClosed` | 关闭后发送报错 |
| `TestHTTPSession_Close` | 会话关闭 |
| `TestHTTPSession_CloseIdempotent` | 重复 Close 安全 |
| `TestHTTPSession_Request` | 获取原始 Request |
| `TestHTTPSession_ResponseWriter` | 获取原始 ResponseWriter |
| `TestHTTPSession_RemoteAddr` | 远端地址解析 |

**integration_test.go**

| 函数 | 说明 |
|------|------|
| `TestIntegration_HTTP_ModeA` | Mode A httptest 路由测试 |
| `TestIntegration_HTTP_ModeA_RealServer` | Mode A 真实服务器 |
| `TestIntegration_HTTP_ModeB_SessionHandler` | Mode B 原始处理器 |

### WebSocket 协议

**server_test.go**

| 函数 | 说明 |
|------|------|
| `TestNewWSServer` | WebSocket 服务器创建 |
| `TestNewWSServer_WithOptions` | 带选项创建 |
| `TestWSServer_StartStop` | 启停生命周期 |
| `TestWSServer_StopIdempotent` | 重复 Stop 安全 |
| `TestWSServer_StopWithoutStart` | 未启动直接 Stop |
| `TestWSServer_OriginCheck` | Origin 头验证 |
| `TestWSServer_OriginCheck_Empty` | 空 Origin 放行 |
| `TestWSServer_Manager_BeforeStart` | Start 前 Manager 为 nil |

**session_test.go**

| 函数 | 说明 |
|------|------|
| `TestNewWSSession` | WebSocket 会话创建 |
| `TestWSSession_SendBinary` | 发送二进制消息 |
| `TestWSSession_SendText` | 发送文本消息 |
| `TestWSSession_SendPing` | 发送 Ping 帧 |
| `TestWSSession_Close` | 会话关闭 |
| `TestWSSession_CloseIdempotent` | 重复 Close 安全 |
| `TestWSSession_SendAfterClose` | 关闭后发送报错 |
| `TestWSSession_SendTyped` | SendTyped 方法 |
| `TestWSSession_Metadata` | 元数据操作 |
| `TestWSSession_AddrInfo` | 地址信息 |
| `TestWSSession_Conn` | 底层连接访问 |
| `TestWSSession_RemoteAddrUsesLoopback` | 回环地址验证 |

**integration_test.go**

| 函数 | 说明 |
|------|------|
| `TestIntegration_WS_Echo` | 二进制消息回声 |
| `TestIntegration_WS_TextEcho` | 文本消息回声 |
| `TestIntegration_WS_MultipleMessages` | 多消息连续回声 |

### CoAP 协议

**message_test.go — 消息编解码**

| 函数 | 说明 |
|------|------|
| `TestParseMessage_MinimalValid` | 最小有效帧解析 |
| `TestParseMessage_WithToken` | 带 Token 帧解析 |
| `TestParseMessage_WithPayload` | 带负载帧解析 |
| `TestParseMessage_WithOptions` | 带选项帧解析 |
| `TestParseMessage_AllTypes` | 所有消息类型解析 |
| `TestParseMessage_TooShort` | 短帧拒绝 |
| `TestParseMessage_BadVersion` | 无效版本拒绝 |
| `TestParseMessage_BadTKL` | 无效 TKL 拒绝 |
| `TestParseMessage_TokenExceedsMessage` | Token 溢出检测 |
| `TestParseMessage_OptionValueExceedsMessage` | 选项溢出检测 |
| `TestNewACK` | ACK 消息创建 |
| `TestNewACK_NilPayload` | 无负载 ACK |
| `TestNewRST` | RST 消息创建 |
| `TestSerialize_RoundTrip_Minimal` | 最小帧序列化/解析往返 |
| `TestSerialize_RoundTrip_WithToken` | 带 Token 往返 |
| `TestSerialize_RoundTrip_WithOptionsAndPayload` | 带选项+负载往返 |
| `TestSerialize_RoundTrip_ACK` | ACK 往返 |
| `TestSerialize_RoundTrip_MultipleOptions` | 多选项往返 |
| `TestSerialize_KnownBytes` | 已知字节序列验证 |
| `TestSerialize_WithPayloadMarker` | 负载标记验证 |

**server_test.go**

| 函数 | 说明 |
|------|------|
| `TestNewCoAPServer` | CoAP 服务器创建 |
| `TestNewCoAPServer_WithOptions` | 带选项创建 |
| `TestCoAPServer_StartStop` | 启停生命周期 |
| `TestCoAPServer_StopIdempotent` | 重复 Stop 安全 |
| `TestCoAPServer_StopWithoutStart` | 未启动直接 Stop |

**integration_test.go**

| 函数 | 说明 |
|------|------|
| `TestIntegration_CoAP_Echo` | NON 消息回声 |
| `TestIntegration_CoAP_CON_ACK` | CON-ACK 交换 |
| `TestIntegration_CoAP_Dedup` | 消息去重 |

---

## 10. 会话管理 (internal/session)

### manager_test.go — 会话管理器

| 函数 | 说明 |
|------|------|
| `TestManager_NextID_MonotonicallyIncreasing` | ID 单调递增 |
| `TestManager_RegisterAndGet` | 注册并检索 |
| `TestManager_Unregister` | 注销会话 |
| `TestManager_Count` | 计数准确 |
| `TestManager_Get_Nonexistent` | 不存在返回 false |
| `TestManager_All` | 遍历所有会话 |
| `TestManager_Range` | 范围迭代 |
| `TestManager_Range_EarlyStop` | 提前终止迭代 |
| `TestManager_Broadcast` | 广播发送 |
| `TestManager_Close` | 关闭所有会话 |
| `TestManager_Register_AfterClose` | 关闭后注册报错 |
| `TestManager_Close_Idempotent` | 重复 Close 安全 |
| `TestManager_ConcurrentRegisterUnregister` | 并发注册/注销压力 |
| `TestManager_ConcurrentRegisterWithCapacityLimit` | 容量限制并发注册 |

### lru_test.go — LRU 淘汰

| 函数 | 说明 |
|------|------|
| `TestLRUList_Touch_InsertsNew` | 新节点插入 |
| `TestLRUList_Evict_LRLOrder` | LRU 顺序淘汰 |
| `TestLRUList_Touch_ReordersHead` | Touch 移至头部 |
| `TestLRUList_Touch_AlreadyAtHead` | 已在头部无操作 |
| `TestLRUList_Remove` | 移除指定节点 |
| `TestLRUList_Remove_Nonexistent` | 移除不存在节点 |
| `TestLRUList_Len` | 长度计数 |
| `TestLRUList_Stop` | 清空所有节点 |
| `TestLRUList_Evict_Empty` | 空列表淘汰 |
| `TestLRUList_Evict_NonPositive` | 非正数淘汰 |
| `TestLRUList_Evict_MoreThanAvailable` | 超额淘汰 |

### manager_bench_test.go — 管理器性能

| 函数 | 说明 |
|------|------|
| `BenchmarkSessionRegister` | 并行注册/注销 |
| `BenchmarkManagerGet` | 并行 Get 查询 |
| `BenchmarkManagerNextID` | 顺序 ID 生成 |
| `BenchmarkManagerNextIDParallel` | 并行 ID 生成竞争 |
| `BenchmarkManagerCount` | 无锁 Count 操作 |
| `BenchmarkManagerRegisterSequential` | 单线程注册吞吐量 |

### session_test.go — 会话基础

| 函数 | 说明 |
|------|------|
| `TestNewBase_InitialState` | 初始 Connecting 状态 |
| `TestSetState_CAS_Transitions` | CAS 状态转换 |
| `TestSetState_SameState_ReturnsFalse` | 相同状态返回 false |
| `TestSetState_InvalidTransitions` | 无效转换拒绝 |
| `TestIsAlive_OnlyActive` | 仅 Active 为存活 |
| `TestTouchActive_UpdatesLastActiveAt` | 更新最后活跃时间 |
| `TestDoClose_TransitionsAndCancels` | 关闭转换并取消上下文 |
| `TestMetadata_Operations` | 元数据 CRUD |
| `TestMetadata_Concurrent` | 并发元数据访问 |

---

## 11. 类型系统 (internal/types)

| 函数 | 说明 |
|------|------|
| `TestProtocolTypeString` | 协议类型字符串表示 |
| `TestMessageTypeString` | 消息类型字符串表示 |
| `TestSessionStateString` | 会话状态字符串表示 |
| `TestProtocolLabelPooled` | 协议标签字符串池化 |
| `TestNewRawMessage` | 消息创建含负载/元数据 |
| `TestNewRawMessageIDsAreUnique` | 消息 ID 唯一性 |
| `TestMessageGeneric` | 泛型 Message 类型 |
| `TestRawMessageAlias` | RawMessage 别名兼容 |
| `TestBasePluginMethods` | BasePlugin 默认方法 |
| `TestPluginInterfaceSatisfaction` | 接口满足性编译检查 |
| `TestRawSessionAlias` | RawSession = Session[[]byte] 编译验证 |
| `TestRawHandlerAlias` | RawHandler = MessageHandler[[]byte] 编译验证 |

---

## 12. 工具库 (internal/utils)

### atomic_test.go — 原子布尔

| 函数 | 说明 |
|------|------|
| `TestAtomicBool_DefaultFalse` | 默认值为 false |
| `TestAtomicBool_SetGet` | Set/Get 操作 |
| `TestAtomicBool_CompareAndSwap_Success` | CAS 成功 |
| `TestAtomicBool_CompareAndSwap_Failure` | CAS 失败 |
| `TestAtomicBool_Concurrent` | 并发安全 |

### net_test.go — 网络工具

| 函数 | 说明 |
|------|------|
| `TestParseIP` | IP 地址解析（带端口） |
| `TestIPToKey` | IP 转字符串 Key |
| `TestExtractIPFromAddr` | 从地址提取 IP |
| `TestIsPrivateIP` | 私有 IP 检测 |
| `TestNormalizeIP` | IP 归一化 |

### sync_test.go — 分片 Map

| 函数 | 说明 |
|------|------|
| `TestNewShardedMap` | 分片 Map 创建 |
| `TestShardedMap_SingleShard` | 单分片行为 |
| `TestShardedMap_SetGet` | 基本 Set/Get |
| `TestShardedMap_GetMissing` | 缺失 Key 返回 false |
| `TestShardedMap_Delete` | Key 删除 |
| `TestShardedMap_DeleteMissing` | 删除不存在 Key |
| `TestShardedMap_Overwrite` | 覆盖已有 Key |
| `TestShardedMap_IntKeys` | 整数 Key |
| `TestShardedMap_Uint64Keys` | Uint64 Key |
| `TestShardedMap_Len` | 长度跟踪 |
| `TestShardedMap_Concurrent` | 并发操作 |

---

## 运行测试

```bash
# 运行全部测试
go test ./...

# 仅运行单元测试
go test ./tests/unit/...

# 仅运行集成测试
go test ./tests/integration/...

# 仅运行基准测试
go test -bench=. ./tests/benchmark/...

# 运行指定模块
go test ./internal/protocol/tcp/...
go test ./internal/plugin/...

# 带覆盖率
go test -cover ./...

# 基准测试带内存分析
go test -bench=. -benchmem ./tests/benchmark/...
```
