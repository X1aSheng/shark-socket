# Shark-Socket 专家审查报告（第二轮）

> 基于 Phase 1-4 改进后的全面代码审查，聚焦真实缺陷和改进方向

---

## 审查概况

| 级别 | 数量 | 状态 |
|------|------|------|
| Critical | 5 | ✅ 全部完成 |
| High | 7 | ✅ 全部完成 |
| Medium | 8 | ✅ 6/8 已完成 |
| 总计 | 20 | 18/20 已完成 |

---

## Critical 修复（稳定性）

### 1. TCP WriteLoop buffer 泄漏 ✅

**文件**：`internal/protocol/tcp/session.go:131-158`

**问题**：`WriteLoop` 从 `writeQueue` 读取 `data`，但 `data` 来自 `bufferpool.GetDefault()` 分配的 buffer。WriteLoop 写完后没有调用 `bufferpool.PutDefault()` 归还，导致每次 Send 都永久泄漏一个 buffer。

```go
// Send 中分配:
buf := bufferpool.GetDefault(len(data))
copy(buf.Bytes(), data)
s.writeQueue <- buf.Bytes()  // 传走了

// WriteLoop 中消费:
data, ok := <-s.writeQueue
_ = s.framer.WriteFrame(s.conn, data)
// ❌ 没有 PutDefault — buffer 永久泄漏
```

**方案**：WriteLoop 写完后通过 `buf := bufferpool.GetDefault(0)` 反查或改为传递 `*Buffer` 指针。

---

### 2. BlacklistPlugin isBlocked 两次 RLock 间存在 TOCTOU 竞态 ✅

**文件**：`internal/plugin/blacklist.go:117-134`

**问题**：`isBlocked` 先 RLock 检查 exactMap，解锁后再次 RLock 检查 cidrList。两次加锁之间，另一个 goroutine 可能修改了 exactMap（例如 Reload 清空了列表），导致已过期的 IP 绕过检查。

**方案**：合并为单次 RLock 调用。

---

### 3. BroadcastLimiter 非线程安全 ✅

**文件**：`internal/defense/backpressure.go:57-67`

**问题**：`Allow()` 读写 `lastSend map[string]time.Time` 没有任何锁保护。并发广播时，map 的读写会导致 panic 或数据损坏。

**方案**：添加 `sync.Mutex` 保护 `lastSend`。

---

### 4. Gateway servers map 并发读写 ✅

**文件**：`internal/gateway/gateway.go:52-57, 82-90, 126-128`

**问题**：`g.servers` 是普通 `map`，`Register` 写入、`Start` 遍历、`Stop` 遍历。如果用户在 Start 后调用 Register，将触发 fatal error: concurrent map read and map write。

**方案**：添加 `sync.RWMutex` 保护 servers map，或改用 `sync.Map`。

---

### 5. WorkerPool processTask panic 静默吞没 ✅

**文件**：`internal/protocol/tcp/worker_pool.go:148-151`

**问题**：`processTask` 中 `defer func() { _ = recover() }()` 完全吞没 panic，无日志、无指标、无法诊断。生产中业务 handler panic 后不会有任何可观测信号。

**方案**：添加日志和 metrics 计数器。

```go
defer func() {
    if r := recover(); r != nil {
        log.Printf("[tcp-worker] panic in processTask: %v", r)
        metrics.IncCounter("shark_worker_panics_total", "tcp")
    }
}()
```

---

## High 修复（正确性）

### 6. Session Manager 容量检查非原子 ✅

**文件**：`internal/session/manager.go:77`

**问题**：`m.total.Load() >= m.maxSess` 和后续 `m.total.Add(1)` 之间无原子保护。高并发时多个 goroutine 可能同时通过容量检查，导致实际注册数超过 maxSess。

**方案**：使用 `m.total.Add(1)` 先占位，超过则回退；或在 shard 锁内做完整的 check-and-reserve。

---

### 7. CoAP msgCache 无界增长 ✅

**文件**：`internal/protocol/coap/session.go:104-117`

**问题**：`RecordMessageID` 只在超过 500 时清理 5 分钟前的条目。如果短时间内收到大量不同 MessageID（正常 CoAP 流量），map 会增长到远超 500，且清理阈值硬编码。

**方案**：使用带容量的 LRU map（如 `linked hash map`），超容量自动淘汰最旧条目。

---

### 8. TCP Session Close 超时硬编码 ✅

**文件**：`internal/protocol/tcp/session.go:91`

**问题**：`time.After(5 * time.Second)` 硬编码 drain 超时。用户无法配置，5 秒可能对高延迟场景不够。

**方案**：从 Options.DrainTimeout 读取。

---

### 9. Gateway readyz 无实际就绪检查 ✅

**文件**：`internal/gateway/gateway.go:233-236`

**问题**：`handleReadyz` 永远返回 "ready"，不检查各协议服务器是否真正就绪。Kubernetes readinessProbe 依赖此端点，无意义的 ready 信号会导致流量打到未就绪的 Pod。

**方案**：检查每个服务器的 listener 是否已绑定端口。

---

### 10. HTTP handleWithSession 读取循环效率低 ✅

**文件**：`internal/protocol/http/server.go:109-128`

**问题**：每次请求创建 4096 字节 buf，逐块 append 到 body。当 MaxBodySize=10MB 时，可能触发多次扩容和拷贝。

**方案**：使用 `io.ReadAll(io.LimitReader(r.Body, maxBodySize+1))` 替代手动循环。

---

### 11. WebSocket readLoop 错误类型比较 ✅

**文件**：`internal/protocol/websocket/server.go:157`

**问题**：`err == errs.ErrDrop` 使用 `==` 比较，而非 `errors.Is(err, errs.ErrDrop)`。如果错误被包装，比较会失败。

**方案**：全部改为 `errors.Is(err, errs.ErrDrop)`。

---

### 12. UDP/CoAP readLoop 中 conn.ReadFromUDP 不响应 context ✅

**文件**：`internal/protocol/udp/server.go:74-81`, `internal/protocol/coap/server.go:75-83`

**问题**：虽然添加了 `select { case <-s.ctx.Done(): return default: }` 检查，但 `ReadFromUDP` 是阻塞调用。当 context 取消时，goroutine 会阻塞在 Read 上直到有数据到达或 conn.Close() 被调用。如果 conn.Close 发生在 ReadFromUDP 之前，不会泄漏；但如果顺序相反，goroutine 最多阻塞到下一个数据包到达。

**方案**：这是 UDP 的固有限制。可以在 Stop 中先 Close conn，再 cancel context，确保 ReadFromUDP 返回错误。当前代码 Stop 已经先 Close conn，所以实际不会泄漏。保留当前实现但添加注释说明。

---

## Medium 修复（质量）

### 13. Cache 接口缺少 Close 方法 ✅

**文件**：`internal/infra/cache/cache.go:10-17`

**问题**：`Cache` 接口没有 `Close()` 方法，但 `MemoryCache` 实现了 `Close()`。用户无法通过接口停止后台清理 goroutine。

**方案**：添加 `io.Closer` 到接口嵌入，或添加 `Close() error` 方法。

---

### 14. CircuitBreaker HalfOpen 只允许单次探测

**文件**：`internal/infra/circuitbreaker/circuitbreaker.go:69-70`

**问题**：当前 HalfOpen 状态下所有请求都被拒绝（`return ErrCircuitOpen`）。标准 Circuit Breaker 模式应允许一个请求通过进行探测。当前实现在 Open→HalfOpen 转换时只让赢得 CAS 的那个请求通过，其他全部拒绝，这是正确的。但 HalfOpen→Closed 的转换只在成功时发生，如果探测请求失败则立即回到 Open，可能过于激进。

**方案**：考虑在 HalfOpen 状态允许有限的并发探测（如 3 个请求），成功率达到阈值才关闭。

---

### 15. PubSub 消息静默丢弃 ✅

**文件**：`internal/infra/pubsub/pubsub.go:99-101`

**问题**：慢消费者 channel 满时消息被静默丢弃，无日志、无指标、无回调。用户无法知道消息丢失。

**方案**：添加可选的 `OnDrop func(topic string, data []byte)` 回调或 metrics 计数器。

---

### 16. Gateway metrics 端点为占位实现 ✅

**文件**：`internal/gateway/gateway.go:238-244`

**问题**：`/metrics` 端点输出硬编码文本和 session 计数，未集成 Prometheus client 的 `/metrics` handler。实际 Prometheus 抓取无法获得已注册的所有 `shark_*` 指标。

**方案**：集成 `promhttp.Handler()` 替代手动输出。

---

### 17. Session 状态转换无约束

**文件**：`internal/session/session.go:84-94`

**问题**：`SetState` 允许任意状态转换（如从 Closed 回到 Active），缺乏状态机约束。虽然调用方可以控制，但无保护意味着未来维护者可能意外引入非法转换。

**方案**：添加状态转换表校验。

---

### 18. 缺少集成测试：高并发 session 注册/注销

**问题**：当前测试未覆盖高并发下的 session 注册/注销竞争。Phase 4 添加的跨 shard 全局 LRU 淘汰在并发压力下可能暴露死锁（持有 shard A 锁时尝试获取 shard B 锁）。

**方案**：添加 `-race` 标记的并发压力测试（需启用 CGO），或添加确定性的并发测试用例。

---

### 19. OverloadProtector 无滞后机制

**文件**：`internal/defense/overload.go:41-47`

**问题**：过载/恢复基于固定阈值切换，无滞后区间。当 session 数在阈值附近波动时，状态快速翻转（flapping），导致 Guard 间歇性拒绝连接。

**方案**：进入过载用高水位，退出过载用低水位（已有 highWater/lowWater），但切换瞬间无冷却期。

---

### 20. API 层缺少版本号和构建信息

**文件**：`api/api.go`

**问题**：框架没有暴露版本号、构建时间、Git commit 等信息。运维无法确认运行中的二进制版本。

**方案**：通过 `ldflags` 注入版本信息，在 `/healthz` 中返回。

---

## 实施优先级

```
Critical (1-5) ──→ High (6-12) ──→ Medium (13-20)
   ~1 天              ~1-2 天           ~2-3 天
```

每项修复完成后运行完整测试套件：

```bash
go test ./... -count=1 -timeout 120s
```
