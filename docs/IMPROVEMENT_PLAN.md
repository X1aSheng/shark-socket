# Shark-Socket 改进计划

> 基于 Go 专家级代码审查，按优先级分 4 个阶段实施

---

## 审查概况

| 级别 | 数量 | 说明 |
|------|------|------|
| Critical | 6 | 生产稳定性风险：竞态条件、panic 未恢复、资源泄漏 |
| High | 9 | 正确性缺陷：状态转换错误、阻塞操作、虚假健康检查 |
| Medium | 9 | 质量问题：编码规范、边界校验、性能优化 |
| 总计 | 24 | |

---

## 第一阶段：Critical 修复（稳定性）

### 1.1 Gateway 启动同步 — 替换 time.Sleep

**问题**：`internal/gateway/gateway.go:97` 使用 `time.Sleep(100ms)` 等待服务器启动，存在竞态：100ms 内服务器可能未就绪，也可能已失败。

**方案**：使用 `errgroup` 管理并发启动，每个服务器在就绪后发送信号。

```go
// 修改前
time.Sleep(100 * time.Millisecond)

// 修改后
g, ctx := errgroup.WithContext(context.Background())
ready := make(chan struct{}, len(g.servers))

for proto, srv := range g.servers {
    p, s := proto, srv
    g.Go(func() error {
        if err := s.Start(); err != nil {
            return fmt.Errorf("server %s failed: %w", p, err)
        }
        ready <- struct{}{}
        return nil
    })
}

if err := g.Wait(); err != nil {
    // rollback
    startErr = err
}
```

**涉及文件**：
- `internal/gateway/gateway.go` — Start() 方法重写

---

### 1.2 Plugin Chain panic 保护

**问题**：`internal/plugin/chain.go` 中任何插件 panic 会崩溃整个链路，拖垮服务。

**方案**：每个插件调用外包 `defer recover()`，panic 时记录日志并继续执行后续插件。

```go
func (c *Chain) OnAccept(sess types.RawSession) error {
    for _, p := range c.plugins {
        func() {
            defer func() {
                if r := recover(); r != nil {
                    log.Printf("[shark-socket] plugin %s OnAccept panic: %v", p.Name(), r)
                }
            }()
            if err := p.OnAccept(sess); err != nil {
                // ... existing error handling ...
            }
        }()
    }
    return nil
}
```

**涉及文件**：
- `internal/plugin/chain.go` — OnAccept/OnMessage/OnClose 三个方法

---

### 1.3 TCP 连接生命周期管理

**问题**：`internal/protocol/tcp/server.go:82-108` shutdown 窗口内接受的连接可能产生无限运行的 goroutine，缺少 context 传播。

**方案**：为每个连接创建从 server ctx 派生的 context，handleConn 中检查 ctx.Done()。

```go
func (s *Server) acceptLoop() {
    defer s.wg.Done()
    for {
        conn, err := s.listener.Accept()
        if err != nil { return }
        s.wg.Add(1)
        go func(c net.Conn) {
            defer s.wg.Done()
            ctx, cancel := context.WithCancel(s.ctx)
            defer cancel()
            s.handleConn(ctx, c)
        }(conn)
    }
}
```

**涉及文件**：
- `internal/protocol/tcp/server.go` — acceptLoop、handleConn 签名
- `internal/protocol/tcp/session.go` — readLoop/writeLoop 传入 ctx

---

### 1.4 UDP Session 原子创建

**问题**：`internal/protocol/udp/server.go:131-149` 多个 goroutine 可对同一地址创建重复 session。

**方案**：使用 `sync.Map.LoadOrStore` 确保同一地址只创建一个 pseudo-session。

```go
func (s *Server) getOrCreateSession(addr string, raddr *net.UDPAddr) *UDPSession {
    newSess := newUDPSession(s.nextID(), raddr, s)
    if actual, loaded := s.sessions.LoadOrStore(addr, newSess); loaded {
        return actual.(*UDPSession)
    }
    return newSess
}
```

**涉及文件**：
- `internal/protocol/udp/server.go` — getOrCreateSession 方法

---

### 1.5 CoAP pendingACKs 加锁

**问题**：`internal/protocol/coap/server.go:151-162` 重传定时器与主循环并发访问 pendingACKs map，无锁保护导致 data race。

**方案**：为 pendingACKs map 添加 `sync.Mutex`，或改用 `sync.Map`。

```go
type Session struct {
    mu         sync.Mutex
    pendingACKs map[uint16]*pendingAck
    // ...
}

func (s *Session) addPending(msgID uint16, data []byte) {
    s.mu.Lock()
    s.pendingACKs[msgID] = &pendingAck{data: data, sentAt: time.Now(), retries: 0}
    s.mu.Unlock()
}
```

**涉及文件**：
- `internal/protocol/coap/server.go` — retransmitLoop、readLoop
- `internal/protocol/coap/session.go` — 添加 mu 字段

---

### 1.6 PubSub 异步分发

**问题**：`internal/infra/pubsub/pubsub.go:64` handler 同步调用，一个慢消费者阻塞整个 topic 的消息分发。

**方案**：每个订阅者使用带缓冲 channel，Publish 写入 channel 而非直接调用 handler。慢消费者超过缓冲时丢弃消息。

```go
type subscription struct {
    handler func([]byte)
    ch      chan []byte
    done    chan struct{}
}

func newSubscription(handler func([]byte), bufSize int) *subscription {
    s := &subscription{
        handler: handler,
        ch:      make(chan []byte, bufSize),
        done:    make(chan struct{}),
    }
    go s.process()
    return s
}

func (s *subscription) process() {
    defer close(s.done)
    for data := range s.ch {
        s.handler(data)
    }
}
```

**涉及文件**：
- `internal/infra/pubsub/pubsub.go` — 重写订阅者模型

---

## 第二阶段：High 修复（正确性）

### 2.1 Session Manager Broadcast 并发化

**问题**：`session/manager.go:153` 遍历所有 session 逐个 Send，一个失败影响后续所有。

**方案**：使用 errgroup 并发 Send，收集失败 session，不中断整体广播。

**涉及文件**：`internal/session/manager.go` — Broadcast 方法

---

### 2.2 Session Close CAS 保护

**问题**：`session/session.go:113-116` DoClose 未做 CAS 状态保护，并发 Close 可多次调用 cancel()。

**方案**：使用 `atomic.CompareAndSwap` 仅在 Active/Connecting → Closed 时执行关闭逻辑。

**涉及文件**：`internal/session/session.go` — DoClose 方法

---

### 2.3 Session meta 清理

**问题**：`session/session.go:28` Close 后 meta sync.Map 不释放，可能长期持有引用。

**方案**：DoClose 中遍历 meta 删除所有条目。

**涉及文件**：`internal/session/session.go` — DoClose 方法

---

### 2.4 WorkerPool tempCount 原子操作

**问题**：`protocol/tcp/worker_pool.go:88-94` PolicySpawnTemp 下 tempCount 非原子操作。

**方案**：使用 `atomic.Int32` 替代 int 字段。

**涉及文件**：`internal/protocol/tcp/worker_pool.go`

---

### 2.5 WebSocket pingLoop 生命周期

**问题**：`protocol/websocket/server.go:171-188` session 关闭后 ping goroutine 可能继续运行。

**方案**：从 session ctx 派生 ping context，Close() 取消 context。

**涉及文件**：`internal/protocol/websocket/server.go`、`internal/protocol/websocket/session.go`

---

### 2.6 CircuitBreaker CAS 状态转换

**问题**：`infra/circuitbreaker/circuitbreaker.go:54` Open→HalfOpen 转换可被多 goroutine 同时触发。

**方案**：使用 `CompareAndSwap` 保护状态转换，失败时重试。

**涉及文件**：`internal/infra/circuitbreaker/circuitbreaker.go`

---

### 2.7 Gateway 健康检查

**问题**：`gateway/gateway.go:188-192` healthz 只返回固定 "healthy"，未检测各协议真实状态。

**方案**：遍历所有已注册服务器检查状态，session manager 未初始化时返回 503。

**涉及文件**：`internal/gateway/gateway.go` — handleHealthz

---

### 2.8 HTTP Mode B 会话唯一性

**问题**：`protocol/http/server.go:93-135` 多个请求可能产生相同 session ID。

**方案**：确保 session ID 使用 Manager.NextID() 生成。

**涉及文件**：`internal/protocol/http/server.go`

---

### 2.9 MemoryCache 惰性删除

**问题**：`infra/cache/cache.go:78` Get 读到过期项不删除，浪费内存。

**方案**：Get 中发现过期项时立即 delete。

**涉及文件**：`internal/infra/cache/cache.go` — Get 方法

---

## 第三阶段：Medium 修复（质量）

### 3.1 Message ID hex 编码

**问题**：`types/message.go:43-44` 原始随机字节转 string 含不可打印字符。

**方案**：`hex.EncodeToString(randomBytes)` 或 `base64.RawURLEncoding`。

**涉及文件**：`internal/types/message.go`

---

### 3.2 Gateway startTime 原子化

**问题**：`gateway/gateway.go:28` startTime 无锁保护，metrics handler 并发访问可能竞态。

**方案**：使用 `atomic.Value` 或 `sync.Once` 保护写入。

**涉及文件**：`internal/gateway/gateway.go`

---

### 3.3 ShardedMap FNV-1a 哈希

**问题**：`utils/sync.go:66-90` hashAny 对字符串 key 分布不均。

**方案**：使用 FNV-1a 哈希算法替代简单取模。

**涉及文件**：`internal/utils/sync.go`

---

### 3.4 Session Register 重复检测

**问题**：`session/manager.go:82` 重复 ID 静默覆盖已有 session。

**方案**：检测存在时返回 `errs.ErrDuplicateSession`。

**涉及文件**：`internal/session/manager.go`

---

### 3.5 BufferPool 尺寸校验

**问题**：`infra/bufferpool/pool.go:113-132` sync.Pool 返回的旧 buffer 可能小于请求尺寸。

**方案**：Get 时检查 buffer 大小，不足时丢弃重新分配。

**涉及文件**：`internal/infra/bufferpool/pool.go`

---

### 3.6 CoAP option 边界校验

**问题**：`protocol/coap/message.go:177-219` 序列化时无 option delta/length 边界校验。

**方案**：添加 delta 和 length 范围检查（不超过 14-bit）。

**涉及文件**：`internal/protocol/coap/message.go`

---

### 3.7 OverloadProtector 主动拒绝

**问题**：`defense/overload.go:31` 过载时仅打日志，不主动拒绝连接。

**方案**：在 accept 阶段检查 IsOverloaded()，返回拒绝错误。

**涉及文件**：`internal/defense/overload.go`、各协议 accept 逻辑

---

### 3.8 HTTP 请求体大小限制

**问题**：`protocol/http/server.go` 无请求体大小限制，可能导致内存耗尽。

**方案**：添加 MaxBodySize 选项，使用 `io.LimitReader` 包装。

**涉及文件**：`internal/protocol/http/server.go`

---

### 3.9 Framer 边界校验

**问题**：`protocol/tcp/framer.go:30-54` RawFramer 无边界校验，畸形数据可导致 panic。

**方案**：添加读取长度校验。

**涉及文件**：`internal/protocol/tcp/framer.go`

---

## 第四阶段：长期改进

### 4.1 统一错误处理策略

- 定义框架级错误处理规范：何时 log、何时 return、何时 panic
- 所有组件使用一致的错误包装模式（`fmt.Errorf("...: %w", err)`）

### 4.2 全面 Metrics 覆盖

- Session Manager：注册数/活跃数/淘汰数
- Protocol（按协议标签）：连接数/消息数/错误数
- Plugin：执行时间/panic 次数
- BufferPool：命中率/内存占用

### 4.3 配置验证

- 所有 `Option` 函数在应用时验证参数范围
- Start() 前做完整配置校验，fail-fast

### 4.4 全局 LRU 淘汰

- 设计跨 shard 的全局 LRU 索引
- 淘汰时选择全局最久未访问的 session

### 4.5 Context 传播完善

- 所有长期运行 goroutine 接受 context 参数
- 通过 context 取消实现统一生命周期管理

---

## 实施顺序

```
第一阶段 (Critical) ──→ 第二阶段 (High) ──→ 第三阶段 (Medium) ──→ 第四阶段 (长期)
   ~2-3 天                  ~2-3 天              ~1-2 天              持续
```

每阶段完成后运行完整测试套件验证无回归：

```bash
bash scripts/run_tests.sh --all
go test -race ./...
```
