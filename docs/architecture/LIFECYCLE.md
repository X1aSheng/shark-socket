# LIFECYCLE.md

> Shark-Socket 生命周期与状态机完整定义  
> 版本:v0.2.x-alpha  
> 最后更新:2026-06-01

---

## 目录

1. [概述](#1-概述)
2. [Session 生命周期](#2-session-生命周期)
3. [Gateway 生命周期](#3-gateway-生命周期)
4. [协议 Server 生命周期](#4-协议-server-生命周期)
5. [插件生命周期](#5-插件生命周期)
6. [连接完整数据流](#6-连接完整数据流)
7. [并发 Goroutine 管理](#7-并发-goroutine-管理)

---

## 1. 概述

本文档描述 Shark-Socket 所有关键组件的**生命周期状态机**与**状态转换规则**,确保:

- **可解释性**:每个状态转换有明确触发条件
- **可测试性**:每个状态可独立验证
- **可恢复性**:异常状态有明确恢复或终止路径

**核心组件生命周期关系:**

```
Gateway 生命周期
    +-- 启动阶段 -> 注入 Runtime -> Plugins().StartAll() -> 逐 Server Start
    +-- 运行阶段 -> 管理所有 Server + SessionManager + PluginChain
    +-- 停止阶段 -> 3 阶段关闭(StopAccept -> Drain -> CloseSessions)
                    -> Sessions().CloseAll -> Plugins().StopAll()

Server 生命周期(每个协议独立)
    +-- 启动阶段 -> 监听端口 + 启动 WorkerPool
    +-- 运行阶段 -> Accept 连接 -> 创建 Session
    +-- 停止阶段 -> 停止 Accept -> Drain goroutine -> 关闭 Session

Session 生命周期(每个连接独立)
    +-- Connecting -> 连接建立中(插件链 OnAccept)
    +-- Active     -> 正常通信(readLoop + writeLoop)
    +-- Draining   -> 排空写队列(Close 触发)
    +-- Closed     -> 已关闭(清理资源)

Plugin 生命周期(跨 Session 共享)
    +-- 注册阶段 -> PluginChain.Append(装配期一次性完成)
    +-- 启动阶段 -> StartAll -> Start(仅 LifecyclePlugin,优先于 Server)
    +-- 执行阶段 -> OnAccept / OnMessage(每个 Session 触发)
    +-- 关闭阶段 -> OnClose(每个 Session 触发,逆序执行)
    +-- 停止阶段 -> StopAll -> Stop(仅 LifecyclePlugin,晚于全部会话关闭)
```

---

## 2. Session 生命周期

### 2.1 状态机定义

```go
type SessionState uint8

const (
    Connecting SessionState = 0 // 连接建立中
    Active     SessionState = 1 // 正常通信
    Draining   SessionState = 2 // 排空写队列中
    Closed     SessionState = 3 // 已关闭
)
```

### 2.2 状态转换图

```
+--------------+
|  Connecting  |  连接建立后,插件链 OnAccept 执行中
+------+-------+
       | OnAccept 成功
       v
+--------------+
|    Active    |  正常通信,readLoop + writeLoop 运行
+------+-------+
       | Close() 调用 或 错误/超时
       v
+--------------+
|   Draining   |  排空写队列,等待 writeLoop 完成
+------+-------+
       | 写队列清空 或 DrainTimeout
       v
+--------------+
|    Closed    |  连接关闭,资源释放
+--------------+

异常路径(任意状态):
  Fatal 错误(ErrFrameTooLarge / panic)-> 直接跳转到 Closed
```

### 2.3 状态转换规则

| 当前状态 | 触发事件 | 目标状态 | 前置条件 | 后置条件 |
|---------|---------|---------|---------|---------|
| Connecting | `OnAccept` 成功 | Active | 插件链返回 nil | 启动 readLoop + writeLoop |
| Connecting | `OnAccept` 返回 `ErrPluginBlock` | Closed | - | 连接立即关闭,不进入 Active |
| Active | `Close()` 调用 | Draining | `sync.Once` 保证单次执行 | `close(draining)` 信号触发排空 |
| Active | 读取错误(EOF / 超时) | Draining | readLoop 检测到错误 | 调用 `Close()` |
| Active | 连续错误超限 | Draining | `consecutiveErrors > MaxConsecutiveErrors` | 调用 `Close()` |
| Draining | 写队列排空 | Closed | `writeQueue` 全部发送完成 | `conn.Close()` |
| Draining | 写队列排空等待超时 | Closed | `Close(ctx)` 的 ctx 到期 | 强制 `conn.Close()`,返回 `ctx.Err()` |
| Closed | 任意操作 | Closed | - | 返回 `ErrSessionClosed` |

### 2.4 状态转换实现(CAS 保证并发安全)

```go
// BaseSession 内部
type BaseSession struct {
    state atomic.Int32 // SessionState
    // ...
}

// SetState 使用 CAS 保证唯一转换
func (s *BaseSession) SetState(newState SessionState) bool {
    for {
        current := SessionState(s.state.Load())
        
        // 状态转换合法性检查
        if !isValidTransition(current, newState) {
            return false
        }
        
        // CAS 尝试转换
        if s.state.CompareAndSwap(int32(current), int32(newState)) {
            return true
        }
        // CAS 失败,重试
    }
}

// 合法转换矩阵
func isValidTransition(from, to SessionState) bool {
    switch from {
    case Connecting:
        return to == Active || to == Closed
    case Active:
        return to == Draining || to == Closed
    case Draining:
        return to == Closed
    case Closed:
        return to == Closed // 幂等
    default:
        return false
    }
}
```

### 2.5 Close 六步状态机(长连接协议)

**适用协议:** TCP,WebSocket,QUIC(有写队列的协议)

```go
func (s *TCPSession) Close(ctx context.Context) error {
    // 步骤1:CAS Active -> Draining(幂等保护)
    if !s.SetState(Draining) {
        // 已在关闭或已关闭,直接返回
        return nil
    }

    // 步骤2:若 writeLoop 已启动,触发排空信号
    if s.writerStarted.Load() {
        close(s.draining) // 信号:停止接收新消息
    } else {
        // writeLoop 未启动(连接在 Accept 后立即 Close),直接释放 writeQueue 中的 Buffer
        for buf := range s.writeQueue {
            bufferpool.Put(buf)
        }
    }

    // 步骤3:等待 writeQueue 排空(DrainTimeout 控制)
    if s.writerStarted.Load() {
        select {
        case <-s.drained: // writeLoop 发送完成信号
            // 排空成功
        case <-ctx.Done(): // DrainTimeout
            s.logger.Warn("drain timeout",
                "session_id", s.ID(),
                "queue_depth", len(s.writeQueue))
            // 强制继续
        }
    }

    // 步骤4:CAS Draining -> Closed
    s.SetState(Closed)

    // 步骤5:CancelContext() -> 通知所有 <-ctx.Done() 的 goroutine 退出
    s.cancel()

    // 步骤6:conn.Close()
    return s.conn.Close()
}
```

**步骤3 的两种路径:**

| 场景 | 路径 | 说明 |
|------|------|------|
| writeLoop 已启动 | 等待 `<-s.drained` 或 `<-ctx.Done()` | 正常连接关闭路径 |
| writeLoop 未启动 | 直接释放 `writeQueue` 中的 Buffer | 连接在 Accept 后立即 Close(如 OnAccept 返回 ErrPluginBlock) |

### 2.6 Send 并发安全

```go
func (s *TCPSession) Send(data []byte) error {
    // 检查状态(Draining/Closed 状态拒绝新消息)
    if !s.IsAlive() { // IsAlive() = State() == Active
        return ErrSessionClosed
    }

    // 复制调用方 data,防止 writeQueue 中的 buffer 被调用方复用覆盖
    buf := make([]byte, len(data))
    copy(buf, data)

    // 非阻塞写入
    select {
    case s.writeQueue <- buf:
        return nil
    default:
        return ErrWriteQueueFull // 队列满,立即返回
    }
}
```

**关键约束:**

| 约束 | 实现 | 原因 |
|------|------|------|
| 非阻塞 | `select { case...: default: }` | 避免 Send 阻塞 readLoop |
| 状态检查 | `IsAlive()` 在队列写入前检查 | 拒绝向 Draining/Closed 状态发送 |
| 数据拷贝 | `copy(buf, data)` | 防止调用方复用切片污染 writeQueue |

---

## 3. Gateway 生命周期

### 3.1 状态定义

```go
// Gateway 内部状态(无独立状态机,用 atomic 字段 + startMu 表达)
type Gateway struct {
    startMu   sync.Mutex                   // 串行化 Start / Stop / Register
    mu        sync.RWMutex                 // 保护 servers / order
    servers   map[core.Protocol]core.Server
    order     []core.Protocol
    rt        *Runtime
    timeouts  core.StageTimeouts
    startedAt atomic.Pointer[time.Time]    // nil 表示未启动
    started   atomic.Bool
}
```

只有一个布尔状态位 `started`:启动中与运行中都为 true,关停一开始就置回 false.没有
"starting" / "stopping" 这样的中间态,因为这两种阶段的边界由 `startMu` 的持有者界定,
暴露成状态反而会引入不一致窗口.

### 3.2 启动流程

```go
func (g *Gateway) Start(ctx context.Context) error {
    g.startMu.Lock()
    defer g.startMu.Unlock()

    if g.started.Load() {
        return fmt.Errorf("gateway: already started")
    }
    servers := g.snapshot()
    if len(servers) == 0 {
        return core.ErrNoServers
    }

    // 1. 注入 Runtime:必须在任何 Start 之前完成.
    for _, srv := range servers {
        if configurable, ok := srv.(core.RuntimeConfigurable); ok {
            configurable.UseRuntime(g.rt)
        }
    }

    // 2. 启动生命周期插件:失败时还没有 Server 启动过,StartAll 自行回滚.
    if err := g.rt.Plugins().StartAll(); err != nil {
        return err
    }

    // 3. 按注册顺序启动 Server.
    started := make([]core.Server, 0, len(servers))
    for _, srv := range servers {
        if err := srv.Start(ctx); err != nil {
            g.rt.Logger().Error("server start failed", "protocol", srv.Protocol(), "error", err)
            // 回滚:以 Finalize 预算构造上下文,逆序停止已启动的 Server.
            rollbackCtx, cancel := context.WithTimeout(context.Background(), g.timeouts.Finalize)
            for i := len(started) - 1; i >= 0; i-- {
                if stopErr := started[i].Stop(rollbackCtx); stopErr != nil {
                    g.rt.Logger().Error("rollback stop failed", "protocol", started[i].Protocol(), "error", stopErr)
                }
            }
            cancel()
            if stopErr := g.rt.Plugins().StopAll(); stopErr != nil {
                g.rt.Logger().Error("plugin stop during rollback failed", "error", stopErr)
            }
            return err
        }
        started = append(started, srv)
    }

    // 4. 标记启动.
    now := time.Now()
    g.startedAt.Store(&now)
    g.started.Store(true)
    return nil
}
```

**启动不变量:**

- `RuntimeConfigurable` 注入必须在任何 `srv.Start` 之前完成,Server 之后才能持共享资源.
- 插件先于 Server 启动:`StartAll` 失败即返回,此时没有任何 Server 启动过,不需要回滚 Server.
- Server 启动失败时按逆序停止已启动的 Server,随后 `Plugins().StopAll()` 停掉刚启动的插件,
  最后返回原始错误.不留半启动状态.
- `startMu` 串行化 `Start` / `Stop` / `Register`,三者不会交错.
- 启动成功后 `startedAt` 与 `started` 同时就位,`Ready()` 开始返回 true.

### 3.3 停止流程(3 阶段 + 收尾)

```go
func (g *Gateway) Stop(ctx context.Context) error {
    g.startMu.Lock()
    defer g.startMu.Unlock()

    // 先置为未启动,让整个关停窗口内 Ready() 为 false,Register() 被拒绝.
    g.started.Store(false)

    servers := g.snapshot()
    var firstErr error

    // 阶段1:StopAccept -- 停止接受新连接.
    for _, srv := range servers {
        if staged, ok := srv.(core.StagedServer); ok {
            if err := runStage(ctx, g.timeouts.StopAccept, staged.StopAccept); err != nil && firstErr == nil {
                firstErr = err
            }
        }
    }

    // 阶段2:Drain -- 等待在途请求处理完.
    for _, srv := range servers {
        if staged, ok := srv.(core.StagedServer); ok {
            if err := runStage(ctx, g.timeouts.Drain, staged.Drain); err != nil && firstErr == nil {
                firstErr = err
            }
        }
    }

    // 阶段3:CloseSessions -- 关闭剩余会话.
    for _, srv := range servers {
        if staged, ok := srv.(core.StagedServer); ok {
            if err := runStage(ctx, g.timeouts.CloseSessions, staged.CloseSessions); err != nil && firstErr == nil {
                firstErr = err
            }
            continue
        }
        // 未实现 StagedServer 的 Server 在同一个 CloseSessions 预算内整体 Stop.
        if err := runStage(ctx, g.timeouts.CloseSessions, func(stageCtx context.Context) error {
            return srv.Stop(stageCtx)
        }); err != nil && firstErr == nil {
            firstErr = err
        }
    }
    if err := runStage(ctx, g.timeouts.CloseSessions, func(stageCtx context.Context) error {
        return g.rt.Sessions().CloseAll(stageCtx)
    }); err != nil && firstErr == nil {
        firstErr = err
    }

    // 收尾:全部会话关闭后才停插件,避免插件后台循环与传输层拆除竞争.
    if err := g.rt.Plugins().StopAll(); err != nil {
        g.rt.Logger().Error("plugin stop failed", "error", err)
        if firstErr == nil {
            firstErr = err
        }
    }

    g.startedAt.Store(nil)
    return firstErr
}

func runStage(parent context.Context, timeout time.Duration, fn func(context.Context) error) error {
    ctx := parent
    cancel := func() {}
    if timeout > 0 {
        ctx, cancel = context.WithTimeout(parent, timeout)
    }
    defer cancel()
    return fn(ctx)
}
```

**停止不变量:**

- 三个阶段串行执行,每个阶段内部对全部 Server 依次调用(超时逐 Server 独立计时).
- 每阶段使用 `core.StageTimeouts` 中自己的预算(默认 `StopAccept=5s`、`Drain=5s`、
  `CloseSessions=10s`),某一阶段超时不缩短后续阶段的预算.
- 首个错误被保留为返回值,但**不中断**后续阶段:关停必须走完,否则会留下未释放的连接与插件.
- `CloseAll` 在全部 `CloseSessions` 之后执行,防止新会话逃脱清理.
- `Plugins().StopAll()` 的失败也计入返回值.
- 重复调用 `Stop` 安全:第二阶段开始 `started` 已为 false,而所有阶段操作对已停止的
  Server 都是幂等的空操作.

### 3.4 CloseAll 并发安全保证

```
阶段顺序保证了 CloseAll 的安全性:

  阶段1 StopAccept 完成后:
    -> 所有新连接入口已关闭,不会产生新 session

  阶段2 Drain 完成后:
    -> 所有 readLoop goroutine 已退出,不会注册新 session

  阶段3 CloseSessions 完成后:
    -> 协议层 session 已关闭

  阶段5 CloseAll:
    -> 清理 SessionManager 中可能遗留的 session(应为空或极少数)
    -> 若遗留,说明协议层未正确清理,记录 Warn 日志
```

### 3.5 Ready 状态查询

```go
func (g *Gateway) Ready() bool { return g.started.Load() }

func (g *Gateway) Health() map[string]any {
    started := g.started.Load()
    resp := map[string]any{
        "started":  started,
        "sessions": g.rt.Sessions().Count(),
    }
    if started {
        if startedAt := g.startedAt.Load(); startedAt != nil {
            resp["started_at"] = startedAt.Format(time.RFC3339Nano)
            resp["uptime"] = time.Since(*startedAt).String()
        }
    }
    resp["protocols"] = g.Protocols()
    return resp
}
```

`Health()` 把启动时间与 uptime 放在 `started == true` 分支内,因此关停窗口内不会出现
`started=false` 却带一个活的 uptime 的自相矛盾响应.`healthz` 恒返回 200,`readyz` 映射
`Ready()`:未就绪时返回 503,负载均衡器据此摘除实例.

`Health()` 不检查外部依赖(Cache / Store / PubSub).把外部依赖塞进就绪判断会让一次下游抖动
摘掉整个实例,覆盖面远超实际故障;这些依赖的健康度应各自暴露为独立端点.

---

## 4. 协议 Server 生命周期

### 4.1 TCP Server 生命周期

```
Start() 阶段:
  1. net.Listen(addr) -> listener
  2. WorkerPool.Start() -> 启动核心 Worker goroutine
  3. go acceptLoop() -> 独立 goroutine 等待连接

acceptLoop() 运行阶段:
  for {
      conn, err = listener.Accept()
      if err != nil {
          if errors.Is(err, net.ErrClosed) {
              return // listener 已关闭,正常退出
          }
          指数退避(5ms -> 1s -> 10s)
          consecutiveErrors++
          if consecutiveErrors > MaxConsecutiveErrors {
              srv.Stop() // 自动停止
              return
          }
          continue
      }
      consecutiveErrors = 0
      go handleConn(conn)
  }

handleConn() 处理阶段:
  sess = newTCPSession(conn)
  manager.Register(sess) -> 超容:LRU Evict 或拒绝连接
  pluginChain.OnAccept(sess) -> ErrPluginBlock:Close(sess) + return
  go sess.readLoop()
  go sess.writeLoop()

readLoop() 读循环:
  for {
      payload, err = Framer.ReadFrame(conn)
      if err != nil {
          sess.Close() // 触发 Close 六步状态机
          return
      }
      sess.TouchActive()
      WorkerPool.Submit(sess, payload)
  }

writeLoop() 写循环:
  for {
      select {
      case data := <-writeQueue:
          conn.Write(data)
          bufferpool.Put(data)
      case <-draining:
          // 排空剩余消息
          for data := range writeQueue {
              conn.Write(data)
              bufferpool.Put(data)
          }
          close(drained) // 通知 Close 步骤3
          return
      case <-ctx.Done():
          return
      }
  }

Stop() 阶段(StagedServer 三阶段):
  StopAccept:
    listener.Close() -> acceptLoop 退出
    WorkerPool.Stop() -> 等待所有 Worker 完成当前任务

  Drain:
    WaitGroup.Wait() -> 等待所有 readLoop / writeLoop 退出

  CloseSessions:
    遍历所有 TCPSession -> Close(ctx)
```

### 4.2 UDP Server 生命周期

```
Start() 阶段:
  1. net.ListenUDP(addr) -> conn
  2. go readLoop() -> 单 goroutine 读取所有 datagram
  3. go sweepLoop() -> 定期清理过期伪会话

readLoop() 运行阶段:
  for {
      n, remoteAddr, err = conn.ReadFromUDP(buf)
      if err != nil {
          if errors.Is(err, net.ErrClosed) {
              return // conn 已关闭,正常退出
          }
          continue
      }

      // 查找或创建伪会话
      sess, exists = sessions.Load(remoteAddr.String())
      if !exists {
          tempSess = newUDPSession(remoteAddr)
          if err = pluginChain.OnAccept(tempSess); err != nil {
              if errors.Is(err, ErrPluginBlock) {
                  continue // 不注册伪会话,丢弃 datagram
              }
          }
          sess = tempSess
          sessions.Store(remoteAddr.String(), sess)
          manager.Register(sess)
      }

      sess.TouchActive()
      pluginChain.OnMessage(sess, buf[:n]) -> handler
  }

sweepLoop() 清理阶段:
  ticker := time.NewTicker(sweepInterval)
  for range ticker.C {
      sessions.Range(func(key, val) bool {
          sess := val.(*UDPSession)
          if time.Since(sess.LastActiveAt()) > sessionTTL {
              sess.Close()
              sessions.Delete(key)
              manager.Unregister(sess.ID())
          }
          return true
      })
  }

Stop() 阶段(非 StagedServer):
  conn.Close() -> readLoop 退出
  sweepLoop ticker.Stop()
  清理所有伪会话
```

### 4.3 WebSocket Server 生命周期

```
Start() 阶段:
  1. http.Server 启动
  2. 注册 Upgrade handler 到指定 Path
  3. go pingLoop() -> 为每个 WSSession 启动心跳

Upgrade 处理阶段:
  http.HandleFunc(path, func(w, r)) {
      // Origin 检查(AllowedOrigins 白名单)
      if !isOriginAllowed(r.Header.Get("Origin")) {
          http.Error(w, "Forbidden", 403)
          return
      }

      conn, err := upgrader.Upgrade(w, r, nil)
      if err != nil {
          return
      }

      sess = newWSSession(conn)
      manager.Register(sess)
      pluginChain.OnAccept(sess) -> ErrPluginBlock:Close(sess) + return
      go handleSession(sess)
  }

handleSession() 处理阶段:
  conn.SetReadLimit(MaxMessageSize)
  conn.SetPongHandler(func(string) error {
      sess.TouchActive()
      conn.SetReadDeadline(time.Now().Add(PingInterval + PongTimeout))
      return nil
  })

  go pingLoop(sess)
  for {
      msgType, data, err = conn.ReadMessage()
      if err != nil {
          sess.Close()
          return
      }
      pluginChain.OnMessage(sess, data) -> handler
  }

pingLoop() 心跳阶段:
  ticker := time.NewTicker(PingInterval)
  for {
      select {
      case <-ticker.C:
          sess.sendPing() -> WriteMessage(PingMessage, nil)
      case <-sess.Context().Done():
          ticker.Stop()
          return
      }
  }

OnClose 单次执行保证:
  closeOnce.Do(func() {
      pluginChain.OnClose(sess)  <- 在 Once 内部,天然保证单次执行
      manager.Unregister(sess.ID())
      cancel()
      conn.Close()
  })

Stop() 阶段(StagedServer 三阶段):
  StopAccept:
    停止 HTTP Upgrade Mux(拒绝新 Upgrade 请求)

  Drain:
    等待所有 pingLoop / readLoop 退出

  CloseSessions:
    遍历所有 WSSession -> 发送 Close 帧 -> Close(ctx)
```

---

## 5. 插件生命周期

插件链的执行器是 `internal/runtime/plugin_chain.go` 中的 `PluginChain`,实现
`core.PluginRunner`.本节只描述生命周期相关的行为,执行规则的完整说明见
`PLUGIN.md sec.2` 与 `GATEWAY.md sec.4`.

### 5.1 注册阶段(Append)

注册发生在装配期,不在运行期:

```go
func (c *PluginChain) Append(plugins ...core.Plugin) {
    c.mu.Lock()
    defer c.mu.Unlock()
    next := append([]core.Plugin(nil), c.plugins...)
    for _, p := range plugins {
        if p == nil {
            continue
        }
        next = append(next, p)
    }
    slices.SortStableFunc(next, func(a, b core.Plugin) int {
        return a.Priority() - b.Priority()
    })
    c.plugins = next
}
```

- 没有 `Register`,也没有 `nameIndex`:不再按名称去重覆盖.需要唯一性时由装配方保证.
- copy-on-write:构造新切片后整体换入,因此 `snapshot()` 可以直接交出切片引用,热路径零分配,
  且插件在回调内调用 `SetLogger` / `Append` 不会死锁.
- `nil` 插件被静默跳过,方便调用方用条件表达式拼装列表.
- 稳定排序:同优先级插件按 `Append` 顺序执行,跨运行、跨平台可复现.

### 5.2 执行阶段

**OnAccept(按 Priority 升序):**

```go
func (c *PluginChain) OnAccept(sess core.Session) error {
    plugins, logger := c.snapshot()
    for i, p := range plugins {
        if err := c.safeAccept(p, logger, sess); err != nil {
            // 回滚:已 accept 的插件按逆序收到 OnClose.
            for j := i - 1; j >= 0; j-- {
                c.safeClose(plugins[j], logger, sess)
            }
            return err
        }
    }
    return nil
}
```

链不调用 `sess.Close()`:它只表达"拒绝",关闭动作由传输层执行.

**OnMessage(按 Priority 升序,支持 payload 改写):**

```go
func (c *PluginChain) OnMessage(sess core.Session, data []byte) ([]byte, error) {
    plugins, logger := c.snapshot()
    var err error
    for _, p := range plugins {
        data, err = c.safeMessage(p, logger, sess, data)
        if err != nil {
            return nil, err
        }
    }
    return data, nil
}
```

非 nil 返回即下一个插件的输入;任何错误立即终止链并返回 `nil` payload.

**OnClose(按 Priority 逆序,不可中断):**

```go
func (c *PluginChain) OnClose(sess core.Session) {
    plugins, logger := c.snapshot()
    for i := len(plugins) - 1; i >= 0; i-- {
        c.safeClose(plugins[i], logger, sess)
    }
}
```

逆序执行保证依赖方先释放;某个插件 panic 后,序号更靠前的插件仍会收到 `OnClose`.

### 5.3 panic 隔离

五个钩子各有一个包装,panic 一律在包装内被转为 `core.ErrPluginPanic`:

```go
func (c *PluginChain) safeAccept(p core.Plugin, logger core.Logger, sess core.Session) (err error) {
    defer func() {
        if r := recover(); r != nil {
            logger.Error("plugin accept panic", "plugin", p.Name(), "panic", r)
            err = core.ErrPluginPanic
        }
    }()
    return p.OnAccept(sess)
}
```

| 包装 | 钩子 | panic 后 |
|------|------|----------|
| `safeAccept` | `OnAccept` | 返回 `ErrPluginPanic`,链逆序回滚 |
| `safeMessage` | `OnMessage` | 返回 `ErrPluginPanic`,链终止 |
| `safeClose` | `OnClose` | 记录日志,继续下一个插件 |
| `safeStart` | `Start` | 返回 `ErrPluginPanic`,`StartAll` 回滚 |
| `safeStop` | `Stop` | 返回 `ErrPluginPanic`,`StopAll` 继续 |

注意 `safeClose` 不返回错误:`OnClose` 的契约是 void,且关闭阶段必须走完所有插件.
链内不做 metrics 埋点,插件耗时与错误计数由插件自身与传输层上报.

### 5.4 StartAll 与 StopAll

```go
func (c *PluginChain) StartAll() error {
    plugins, logger := c.snapshot()
    var started []core.Plugin
    for _, p := range plugins {
        if _, ok := p.(core.LifecyclePlugin); !ok {
            continue        // 被动插件直接跳过
        }
        if err := c.safeStart(p, logger); err != nil {
            var rollbackErr error
            for i := len(started) - 1; i >= 0; i-- {
                rollbackErr = errors.Join(rollbackErr, c.safeStop(started[i], logger))
            }
            return errors.Join(fmt.Errorf("plugin %q start: %w", p.Name(), err), rollbackErr)
        }
        started = append(started, p)
    }
    return nil
}
```

`StopAll` 逆序遍历,对每个实现 `LifecyclePlugin` 的插件调用 `safeStop`,`errors.Join`
汇总失败.空链、未启动、重复调用都是安全 no-op.

### 5.5 插件生命周期与网关阶段的对应关系

```
Gateway.Start:
  1. 对每个 Server 注入 Runtime
  2. Plugins().StartAll()          <- 插件后台循环在此启动
  3. 逐个 srv.Start(ctx)           <- 此后才有流量
     失败 -> 逆序 Stop 已启动 Server + Plugins().StopAll()

Gateway.Stop:
  1. started = false
  2. StopAccept  (默认 5s)
  3. Drain       (默认 5s)
  4. CloseSessions (默认 10s) + Sessions().CloseAll(ctx)
  5. Plugins().StopAll()           <- 全部会话关闭之后才停插件
```

启动顺序与关停顺序相反,理由是不对称的:

- 启动时插件必须先就绪,否则第一条消息到达时自动封禁的过期清理、心跳扫荡、集群消费循环
  都还没在跑,前几秒的行为与稳态不一致.
- 关停时插件必须在最后停止,否则插件的后台循环会与传输层拆除竞争同一批会话.

`StopAll` 的失败会计入 `Gateway.Stop` 的返回值,关停故障不会被静默丢弃.

## 6. 连接完整数据流

### 6.1 TCP 连接完整生命周期

```
网络 -> listener.Accept()
  v
id   := rt.Sessions().NextID()
sess := newSession(id, conn, framer, writeQueue, writeTimeout, highWater)
  v
register / accept 阶段:
  defer 中记录 accepted 标志:只有真正完成 OnAccept 的会话才会收到 OnClose
  rt.Sessions().Register(sess)  -> 失败(ErrSessionCapacity / ErrDuplicateSession):
                                    sess.Close + return,不发 OnClose
  rt.Plugins().OnAccept(sess)   -> 失败:链已逆序回滚自身,传输层 sess.Close + return
  accepted = true
  v
go sess.writeLoop()
sess.readLoop(...)     <- readLoop 内部同时是插件链的驱动者
  Framer.ReadFrame(conn) -> payload
  sess.TouchActive()
  rt.Plugins().OnMessage(sess, payload) -> data
    -> ErrPluginDrop :丢弃该消息,连接继续
    -> 其它错误      :sess.Close + return
  pool.Submit(sess, core.ProtocolTCP, data)
    -> ErrWriteQueueFull :计数 tcp_task_queue_full_total(按 FullPolicy 决定是否关会话)

Worker goroutine(池内):
  Handler(sess, Message{Payload: data})
    -> sess.Send(response)

writeLoop:
  for data := range writeQueue:
    conn.Write(data)
    bufferpool.Put(data)

[断开]
readLoop 检测到错误(EOF / net.ErrClosed / 超时)
  v
sess.Close(ctx):
  步骤1:CAS Active -> Draining
  步骤2:close(draining) -> 触发 writeLoop 排空
  步骤3:等待 <-drained 或 DrainTimeout
  步骤4:CAS Draining -> Closed
  步骤5:cancel() -> 所有 <-ctx.Done() 退出
  步骤6:conn.Close()
  v
defer 收尾:
  sessions.Delete(id)
  rt.Sessions().Unregister(id)
  accepted == true -> rt.Plugins().OnClose(sess)(逆序执行)
```

**插件链在 readLoop 上,不在 WorkerPool 里:** `OnMessage` 与 Handler 分处两个阶段.链上运行的
是黑名单匹配、令牌桶等轻量判断,作用是尽快丢弃不该处理的消息;真正的业务 Handler 才交给池.
这样做的代价是:如果某个插件在 `OnMessage` 里做重活,它的耗时会计入 readLoop,造成队首阻塞
(见 `PROJECT-REVIEW-261015-172344.md` 的记录项 D1).插件应当保持 O(1) 判断,重活放到 Handler.

**`accepted` 标志是必要的:** 当 `Register` 或 `OnAccept` 失败时,链已经对部分插件执行过逆序
回滚(触发了它们的 `OnClose`),传输层若再无条件下发一次 `OnClose` 就会重复通知,导致插件对同
一个会话做两次资源释放.

### 6.2 UDP 伪会话生命周期

```
网络 -> conn.ReadFromUDP(buf)
  v
查找伪会话:sessions.Load(remoteAddr.String())
  -> 已存在:复用
  -> 不存在:创建新伪会话
      v
  newUDPSession(remoteAddr)
      v
  pluginChain.OnAccept(tempSess)
    -> ErrPluginBlock:丢弃 datagram,不注册伪会话
    -> nil:sessions.Store + manager.Register
  v
sess.TouchActive()
  v
pluginChain.OnMessage(sess, buf) -> handler

[清理]
sweepLoop 定期扫描:
  time.Since(sess.LastActiveAt()) > sessionTTL
    v
  sess.Close()
  sessions.Delete(key)
  manager.Unregister(sess.ID())
```

---

## 7. 并发 Goroutine 管理

### 7.1 Goroutine 分类

| 类型 | 数量 | 生命周期 | 退出信号 |
|------|------|---------|---------|
| **固定系统 Goroutine** | ~10-20 | 与 Gateway 同生命周期 | Gateway.Stop() -> ctx.Done() |
| **per-Protocol Goroutine** | 每协议 1-3 个 | 与 Server 同生命周期 | Server.Stop() -> listener.Close() 或 ctx.Done() |
| **per-Session Goroutine** | 每连接 2-3 个 | 与 Session 同生命周期 | Session.Close() -> ctx.Done() |
| **WorkerPool Goroutine** | 固定数量(`WorkerCount`) | 与 WorkerPool 同生命周期 | WorkerPool.Stop() -> close(done) |

### 7.2 固定系统 Goroutine

```
Gateway 级别(全局唯一):
  1. HeartbeatPlugin 扫描(P0:ticker;P2:时间轮单 goroutine)
  2. RateLimitPlugin cleanupLoop(清理空闲 IP 桶)
  3. BlacklistPlugin cleanupLoop(清理过期 IP)
  4. PersistencePlugin batchWriter(批量刷盘)
  5. ClusterPlugin 订阅 goroutine(接收跨节点消息)
  6. ClusterPlugin 节点心跳 goroutine
  7. MemoryCache cleanupLoop(清理过期条目)
  8. Metrics HTTP Server(独立 http.Server)
  9. Health HTTP Server(独立 http.Server)
  10. slogLogger 异步写入 goroutine
```

### 7.3 per-Protocol Goroutine

```
TCP Server:
  acceptLoop(1 个)

UDP Server:
  readLoop(1 个)
  sweepLoop(1 个)

CoAP Server(基于 UDP):
  readLoop(1 个)
  sweepLoop(1 个)
  retransmitLoop(1 个,CON 重传)

WebSocket Server:
  无独立 goroutine(复用 http.Server 的 goroutine 池)

HTTP Server:
  无独立 goroutine(复用 http.Server 的 goroutine 池)
```

### 7.4 per-Session Goroutine

```
TCP Session:
  readLoop(1 个)
  writeLoop(1 个)

UDP Session:
  无(复用 Server readLoop)

CoAP Session:
  无(复用 Server readLoop)

WebSocket Session:
  handleSession(1 个,包含 readLoop)
  pingLoop(1 个)
  writeLoop(隐式,由 handleSession 调用 Send 时触发)

HTTP Session(per-request):
  无(http.Server 内部 goroutine)
```

### 7.5 Goroutine 泄漏防护

**退出路径保证(每个 goroutine 至少 3 个退出路径):**

```go
// 示例:readLoop
func (s *TCPSession) readLoop() {
    defer func() {
        s.Close(context.Background()) // 路径1:defer 保证关闭
    }()

    for {
        select {
        case <-s.ctx.Done(): // 路径2:context cancel
            return
        default:
        }

        payload, err := s.framer.ReadFrame(s.conn)
        if err != nil {
            if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
                return // 路径3:连接关闭
            }
            s.logger.Error("read frame error", "error", err)
            return
        }

        // 处理消息...
    }
}
```

**WorkerPool 的 handler 隔离:**

```go
func (p *WorkerPool) handle(t PoolTask) {
    if p.handler == nil || t.Session == nil {
        return
    }
    msg := core.Message{SessionID: t.Session.ID(), Protocol: t.Protocol, Payload: t.Payload}
    if err := p.callHandler(t.Session, msg); err != nil {
        _ = t.Session.Close(context.Background())
    }
}

// CallHandler 捕获 handler panic,记录日志并返回 core.ErrHandlerPanic.
func (p *WorkerPool) callHandler(sess core.Session, msg core.Message) error {
    return CallHandler(func() error { return p.handler(sess, msg) }, p.logger)
}
```

handler panic 被转为 `core.ErrHandlerPanic`,随后关闭该会话.worker goroutine 继续消费队列,
一条连接的故障不会影响其它连接,也不会带走进程(V9 修复项:此前 panic 会直接终止进程,
且没有任何诊断输出).

池**不提供** handler 执行超时:worker 不额外起 goroutine 包住 handler,也不做超时后放弃的
select.原因是一次"放弃等待"必然泄漏那个仍在运行的 handler goroutine,而且会让同一条会话的
后续消息乱序执行.需要超时语义时由 handler 自身观察 `sess.Context()` 实现,超时归还在 handler
内部完成,池不引入第二种并发语义.
