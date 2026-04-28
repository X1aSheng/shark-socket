# Shark-Socket 专家审查报告（第三轮）

> 2026-04-28 完整架构级审查，覆盖全部 174 个 Go 源文件

---

## 审查概况

| 级别 | 数量 | 状态 |
|------|------|------|
| Critical (Phase 1) | 3 | ✅ 已完成 |
| High (Phase 2) | 8 | ✅ 已完成 |
| Medium (Phase 3) | 9 | ✅ 已完成 |
| Long-term (Phase 4) | 5 | 2/5 已完成 |
| 总计 | 25 | 22/25 已完成 |

---

## Phase 1：Critical — 功能不工作的致命缺陷 ✅ (已完成)

### 1.1 QUIC Session WriteQueue 永不被排空 ✅

**文件**：`internal/protocol/quic/session.go:33-42`

**问题**：`Send()` 将数据放入 `writeQueue` channel，但没有任何 goroutine 从 channel 读取数据并写入 QUIC 连接。所有通过 Send() 发送的消息被静默丢弃。

**对比**：TCP Session 有独立的 `WriteLoop()` goroutine 排空队列，QUIC Session 完全缺失。

**方案**：添加 `WriteLoop()` goroutine（或在 handleStream 中直接写入 stream，移除 writeQueue 中间层）。

**实施**：
- 添加 `startWriteLoop()` goroutine，从 writeQueue 读取数据，为每条消息打开 QUIC unidirectional stream 并写入
- `Close()` 使用 `sync.Once` 保护，先 close(draining) 触发排空，再等待 drained channel
- `BaseSession` 改为指针嵌入 `*session.BaseSession`（与其他协议一致）
- 移除 `SetDeadline`/`SetReadDeadline`/`SetWriteDeadline` 方法（quic-go 不支持，原实现错误调用 CloseWithError）
- 移除冗余的 `RemoteAddr()`/`LocalAddr()` 覆盖（BaseSession 已存储正确地址）
- Server.handleConn 调用 `sess.startWriteLoop()` 并等待 `sess.Context().Done()`

---

### 1.2 QUIC Session SetDeadline 系列方法错误关闭连接 ✅

**文件**：`internal/protocol/quic/session.go:87-98`

**问题**：
```go
func (s *Session) SetDeadline(t time.Time) error      { return s.conn.CloseWithError(0, "") }
func (s *Session) SetReadDeadline(t time.Time) error   { return s.conn.CloseWithError(0, "") }
func (s *Session) SetWriteDeadline(t time.Time) error  { return s.conn.CloseWithError(0, "") }
```
这三个方法应设置 I/O 超时，实际却直接关闭连接。quic-go 的 Conn 不支持 SetDeadline，应直接移除这些方法或返回 `ENOSYS`。

**实施**：已在 Phase 1.1 的 session.go 重写中移除这三个方法。

---

### 1.3 Gateway sharedManager 未注入到各协议 Server ✅

**文件**：`internal/gateway/gateway.go:66-78` + 各 `protocol/*/server.go`

**问题**：Gateway 在 `Start()` 创建 `sharedManager`，但从未传递给已注册的 Server。每个 Server 在各自的 `Start()` 中独立创建 Manager。Gateway 的 `/healthz` 接口读取 `sharedManager.Count()` 永远为 0。

**方案**：
- 在各 Server 添加 `SetManager(*session.Manager)` 方法
- Gateway.Start() 中将 sharedManager 注入所有 Server
- 修改 Server.Start() 检查是否已有外部注入的 Manager

**实施**：
- 为 TCP/WebSocket/QUIC/gRPC-Web Server 添加 `SetManager(*session.Manager)` 方法
- 各 Server.Start() 中仅在 `s.manager == nil` 时创建新 Manager
- QUIC Server 的 Manager 创建从 NewServer 移至 Start()
- Gateway.Start() 注入 sharedManager 到所有支持 SetManager 的 Server
- 修复 ID 冲突：WebSocket/gRPC-Web/QUIC 改用 `s.manager.NextID()` 替代独立的 `s.idGen`
- 移除各 Server 不再使用的 `idGen` 字段

---

## Phase 2：High — 数据并发安全 ✅ (已完成)

### 2.1 LRUList 互斥锁声明但从不加锁 ✅

**文件**：`internal/session/lru.go:17`

```go
type LRUList struct {
    mu sync.Mutex  // 所有方法(Touch/Evict/Remove)都不 Lock/Unlock
}
```

`Touch`、`Evict`、`Remove` 全部无锁访问链表和 map。Manager 的 `evictGlobal` 在释放本地分片锁后遍历其他分片，此时 LRU 可被并发修改。`Stop()` 是唯一加锁的方法。

**方案**：在 Touch/Evict/Remove 内部加锁，或移除 mutex 并在 Manager shard 锁文档中明确要求调用方持锁。

**实施**：移除 `mu sync.Mutex` 字段。所有 LRUList 方法实际调用路径均已在 Manager 中持有 shard 锁，无需双重加锁。在 LRUList 类型文档中明确标注"调用方必须持有 shard 锁"。同时从 `Stop()` 移除内部 Lock/Unlock。

---

### 2.2 CoAP/UDPServer getOrCreateSession 双重 OnAccept ✅

**文件**：`internal/protocol/coap/server.go:182-200`, `internal/protocol/udp/server.go:142-160`

```go
val, ok := s.sessions.Load(key)      // ← 未找到
sess := NewCoAPSession(id, conn, addr) // ← 两个 goroutine 都创建了
s.chain.OnAccept(sess)               // ← 两次 OnAccept
actual, _ := s.sessions.LoadOrStore(key, sess) // ← 败者的 session 被丢弃
```

竞态窗口导致：双重 OnAccept 调用 + 败者 session 对象泄漏。

**方案**：使用 `LoadOrStore` 作为原子操作，仅当 LoadOrStore 返回 `loaded=false` 时才调用 OnAccept。使用惰性初始化模式创建 session。

**实施**：在 CoAP 和 UDP 的 `getOrCreateSession` 中，先创建 session 再 `LoadOrStore`。若 `loaded=true`（败者），丢弃创建的 session 并返回已存储的。仅在 `loaded=false`（胜者）时调用 `OnAccept`。若 `OnAccept` 失败，从 `sync.Map` 中删除该 session。

---

### 2.3 PubSub.unsubscribe 解锁后重入锁的 TOCTOU ✅

**文件**：`internal/infra/pubsub/pubsub.go:122-134`

```go
ps.mu.Unlock()   // 解锁
sub.stop()       // ← Close() 可在此获取锁并清空 subscribers
ps.mu.Lock()     // 重新加锁，subscribers 可能已被清空
```

在 `sub.stop()` 执行期间，另一个 goroutine 可获取锁并修改 `ps.subscribers`。

**方案**：在 unlock 前保存需要的数据，stop 在锁外执行后不再重新加锁。

**实施**：移除 `defer ps.mu.Unlock()`，在修改 `ps.subscribers` 后显式 Unlock，再调用 `sub.stop()`。不再重新加锁，因 `stop()` 后无需访问受保护状态。

---

### 2.4 TokenBucket 非原子 refill ✅

**文件**：`internal/plugin/ratelimit.go:33-59`

两个 goroutine 可同时计算 elapsed 并 replenish 同样的 token 数，导致 tokens 超过 maxBurst。

**方案**：使用 CAS 循环保护整个 replenish + consume 操作，或使用互斥锁。

**实施**：添加 `sync.Mutex` 保护 refill 操作（`lastFill` 和 `tokens`）。将 `lastFill` 从 `atomic.Int64` 改为 `time.Time`（受 mutex 保护）。Token 消耗仍使用 CAS 无锁循环，refill 使用 mutex 确保互斥。

---

### 2.5 TCP Client Send() 死锁风险 ✅

**文件**：`internal/protocol/tcp/client.go:78-95`

```go
c.mu.Lock()
defer c.mu.Unlock()
// ...
c.mu.Unlock()          // 绕过 defer
reconnErr := c.Connect() // Connect 内部 c.mu.Lock()/Unlock()
c.mu.Lock()             // 重新加锁，defer 将再次 Unlock
```

手动绕过 defer 的 Lock/Unlock 极易在未来维护中引入死锁。

**方案**：将重连逻辑提取到不持锁的方法中，或使用内部辅助方法避免锁重入。

**实施**：将 `Send()` 重构为两个方法：`Send()` 仅持锁读取 `c.conn`，然后调用不持锁的 `sendWithReconnect(conn, data)`。重连逻辑中不持有 mutex，避免绕过 defer 的 Lock/Unlock 模式。

---

### 2.6 RateLimitPlugin getPerIPBucket 的 tokenBucket 泄漏 ✅

**文件**：`internal/plugin/ratelimit.go:130-138`

```go
val, ok := p.perIPBuckets.Load(key)
if ok { return val.(*tokenBucket) }
tb := newTokenBucket(...)             // 分配新对象
actual, _ := p.perIPBuckets.LoadOrStore(key, tb) // 败者的 tb 被丢弃
```

两个并发请求同时 Load 返回 nil，各自创建 tokenBucket，LoadOrStore 的败者对象被丢弃。

**方案**：只创建一次，使用 `LoadOrStore` + 惰性初始化（在 LoadOrStore loaded=false 时初始化）。

**实施**：明确检查 `loaded` 标志，在 `loaded=true`（败者）时返回 winner 的 bucket 并让 loser 被 GC。由于 `sync.Map` 不支持原子 get-or-compute，竞态下仍有一次浪费的分配，但正确处理了 loaded 标志。

---

### 2.7 MemoryCache.Get 写锁阻止并发读取 ✅

**文件**：`internal/infra/cache/cache.go:79-93`

```go
func (c *MemoryCache) Get(_ context.Context, key string) ([]byte, error) {
    c.mu.Lock()  // 写锁阻止所有并发 Get
```

仅因需删除过期条目而使用写锁。过期删除应推迟到 cleanup goroutine。

**方案**：Get 使用 RLock；若发现过期条目，标记后由 cleanup 删除，或 lock upgrade 仅在过期时获取写锁。

**实施**：`Get()` 默认使用 `RLock()` 读取。仅在条目过期时才释放读锁、获取写锁进行删除（双重检查后）。正常路径（非过期）不再阻塞并发 Get。

---

### 2.8 QUIC Session 嵌入 BaseSession 为值类型导致不一致 ✅

**文件**：`internal/protocol/quic/session.go:16`

```go
type Session struct {
    session.BaseSession       // ← 值嵌入（其他协议都用 *session.BaseSession 指针嵌入）
    writeQueue chan []byte
    conn       *quic.Conn
}
```

值嵌入导致 `Context()` 被覆盖返回 `s.conn.Context()` 而非 BaseSession 的 context。`handleConn` 中 `<-conn.Context().Done()` 等待的是同一个 context，生命周期混乱。

**方案**：统一为指针嵌入 `*session.BaseSession`，与其他协议一致。

**实施**：已在 Phase 1.1 的 QUIC Session 重写中改为 `*session.BaseSession` 指针嵌入。

---

## Phase 3：Medium — 设计质量

### 3.1 泛型体系完全未使用 ✅

**实施**：移除完全未使用的 `TypedServer[M]` 泛型接口。保留 `Session[M]`、`MessageHandler[T]`、`MessageConstraint` 作为 `RawSession`/`RawHandler`/`RawMessage` 别名的基础类型（这些别名在项目中 100% 使用，且通过 `api` 包对外暴露）。

**文件**：`internal/types/message.go:11-13`, `handler.go:5-9`

`MessageConstraint`（含 `any` 导致约束无意义）、`Session[M]`、`MessageHandler[T]`、`TypedServer[M]`——项目中 100% 代码使用 `RawSession`/`RawHandler`/`RawMessage`。

**方案**：评估是否保留；如短期内无泛型使用场景则简化。

---

### 3.2 SlowQueryPlugin 不测量任何东西 ✅

**实施**：
- `OnAccept` 记录会话开始时间到 session metadata
- `OnClose` 计算总会话时长，超过阈值时通过 `LogSlow` 记录
- `NewSlowQueryPlugin` 将其阈值同步到包级变量 `SlowQueryThreshold`，使 Chain 的每条消息慢查询检测与插件配置一致
- `SetThreshold` 同时更新插件和 Chain 的阈值

**文件**：`internal/plugin/slow_query.go:50-57`

`OnMessage` 是空操作；实际计时在 `Chain.OnMessage()` 中使用包级变量 `SlowQueryThreshold`。

**方案**：让 SlowQueryPlugin 在 OnMessage 中记录开始时间并在 OnClose 中计算总耗时，或移除。

---

### 3.3 GatewayConfig 热重载基于副作用而非真正 diff ✅

**实施**：
- `loadJSONFile` 使用 `reflect.New` 创建新实例并反序列化，不再原地修改传入的指针
- `Reload()` 使用 `reflect.DeepEqual` 比较新旧配置，仅在内容实际变化时触发 `onReload` 回调

**文件**：`internal/infra/config/config.go:55-86`

`loadJSONFile` 每次对同一个 struct 指针做 `json.Unmarshal`（原地修改）。`r.config` 永远指向同一个地址。"变化检测"依赖于 JSON 覆盖后字段值的副作用。

**方案**：每次 reload 创建新 struct 实例，通过 `reflect.DeepEqual` 或字段比较进行真正的 diff。

---

### 3.4 PersistencePlugin.flush 逐条 Marshal 无批量优化 ✅

**实施**：使用共享的 `bytes.Buffer` + `json.Encoder` 替代每条记录独立 `json.Marshal`，减少内存分配。Buffer 在每次编码前 Reset 并复制数据到新切片传递给 goroutine。

**文件**：`internal/plugin/persistence.go:130-137`

```go
for _, entry := range batch {
    data, _ := json.Marshal(entry)  // N 次独立 Marshal
    _ = p.store.Save(...)            // N 次独立 Save
}
```

虽然有分批概念，但每条记录仍独立 Marshal + Save。

**方案**：使用 `json.Encoder` 写入 `bytes.Buffer` 一次性序列化整个 batch。

---

### 3.5 UUID 生成每条消息调用 crypto/rand ✅

**实施**：使用 `crypto/rand` 在 `init()` 中生成一次性随机前缀（8 字节 hex），运行时使用 `atomic.Uint64` 单调递增计数器作为后缀。计数器值以大端序写入 8 字节数组并 hex 编码，与前缀拼接。完全消除每条消息对 OS 熵源的系统调用。

**文件**：`internal/types/message.go:42-46`

```go
func generateID() string {
    b := make([]byte, 16)
    _, _ = rand.Read(b)  // crypto/rand 每次读取 OS 熵源
```

高吞吐场景下系统调用开销显著。

**方案**：使用 `crypto/rand` 初始化 seed + `math/rand/v2` 本地 PRNG，或使用 `atomic.Uint64` 单调递增 ID。

---

### 3.6 WebSocket 默认允许所有 Origin ✅

**实施**：默认改为 deny-all（`len(allowedOrigins)==0` 返回 `false`）。添加 `"*"` 通配符支持，允许显式 `WithAllowedOrigins("*")` 恢复旧行为。所有 WebSocket 测试已更新为显式设置允许的 origin。

**文件**：`internal/protocol/websocket/server.go:56-61`

```go
if len(allowedOrigins) == 0 { return true }  // ← 允许一切来源
```

**方案**：默认为 deny-all，要求显式配置白名单。

---

### 3.7 Framer 无防护的 OOM 攻击面 ✅

**文件**：`internal/protocol/tcp/framer.go:28-52`

`maxSize=0` 时允许任意大小 frame。攻击者可发送 4GB length prefix 导致 OOM。

**方案**：强制 maxSize > 0；对超限 frame 使用 `io.LimitReader` 块读取而非一次性分配。

**实施**：`NewLengthPrefixFramer` 和 `NewLineFramer` 在 `maxSize <= 0` 时默认使用 1 MB 限制。添加 `HardMaxFrameSize` (100 MB) 作为绝对上限。移除 `ReadFrame` 中 `maxSize > 0` 的条件保护（maxSize 始终 > 0）。

---

### 3.8 HTTP Body 慢速读取攻击面 ✅

**文件**：`internal/protocol/http/server.go:218-221`

`io.LimitReader` 限制总字节但不限速率。

**方案**：使用 `http.MaxBytesReader` 设置连接超时。

**实施**：将 `io.LimitReader(r.Body, maxBodySize+1)` 替换为 `http.MaxBytesReader(w, r.Body, maxBodySize)`，利用 `http.Server.ReadTimeout` 防止慢速读取。检测 `*http.MaxBytesError` 返回 413 而非通用 400。

---

### 3.9 所有 Plugin 的 Close() 不等待 goroutine 退出 ✅

**实施**：为以下所有组件添加 `sync.WaitGroup` 支持，确保 `Close()`/`Stop()` 等待后台 goroutine 确认退出：
- `RateLimitPlugin` — cleanupLoop 添加 wg.Done()，Close() 添加 wg.Wait()
- `BlacklistPlugin` — cleanupLoop 添加 wg.Done()，Close() 添加 wg.Wait()
- `AutoBanPlugin` — resetLoop 添加 wg.Done()，Close() 添加 wg.Wait()
- `ClusterPlugin` — heartbeatLoop + routeSubscribe 添加 wg.Done()，Close() 添加 wg.Wait()
- `PersistencePlugin` — batchWriter 添加 wg.Done()，Close() 添加 wg.Wait()
- `TimeWheel` (HeartbeatPlugin) — tickLoop 添加 wg.Done()，Stop() 添加 wg.Wait()
- `OverloadProtector` — checkLoop 添加 wg.Done()，Stop() 添加 wg.Wait()
- `ConnectionLimiter` — cleanupLoop 改为 select on stopCh/ticker，添加 Close() 方法

涉及 7 个 plugin + `ConnectionLimiter`。

**方案**：使用 `sync.WaitGroup` 确保 Close() 等待所有后台 goroutine 确认退出。

---

## Phase 4：Long-term — 测试和性能

### 4.1 添加 Framer Fuzz Testing

对所有 Framer 实现（LengthPrefixFramer、LineFramer、RawFramer、CoAP ParseMessage）添加 fuzz test。

### 4.2 添加 Gateway 多协议集成测试

测试 TCP+WS+HTTP 同时运行的完整生命周期。

### 4.3 Framer bufio.Reader 池化 ✅

**实施**：在 `framer.go` 中添加 `sync.Pool` 用于 `bufio.Reader` 复用。`LengthPrefixFramer.ReadFrame` 和 `LineFramer.ReadFrame` 从池中获取 reader，使用 `Reset(r)` 绑定新的底层 reader，defer 归还。避免每帧创建新的 bufio.Reader。

避免每帧都创建新的 `bufio.NewReader`。

### 4.4 BufferPool 清零使用 clear() 内置函数 ✅

**实施**：`BufferPool.Put()` 中的 `for i := range buf.data { buf.data[i] = 0 }` 替换为 `clear(buf.data)`（Go 1.21+ 内置函数）。

`for i := range buf.data { buf.data[i] = 0 }` → `clear(buf.data)`（Go 1.21+）。

### 4.5 添加完整的 grace period 关停集成测试

测试有活跃连接时的完整关停流程。

---

## 实施顺序

```
Phase 1 (功能修复) → Phase 2 (并发安全) → Phase 3 (设计质量) → Phase 4 (测试增强)
     ~1 天               ~2 天                ~2 天               ~2 天
```

每阶段完成后运行：

```bash
go test ./... -count=1 -timeout 120s
go vet ./...
go test -race ./...
```
