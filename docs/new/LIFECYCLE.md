# LIFECYCLE.md

> Shark-Socket 生命周期与状态机完整定义  
> 版本：v0.1.0  
> 最后更新：2026-06-01

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

本文档描述 Shark-Socket 所有关键组件的**生命周期状态机**与**状态转换规则**，确保：

- **可解释性**：每个状态转换有明确触发条件
- **可测试性**：每个状态可独立验证
- **可恢复性**：异常状态有明确恢复或终止路径

**核心组件生命周期关系：**

```
Gateway 生命周期
    ├── 启动阶段 → 向各 Server 注入 Runtime
    ├── 运行阶段 → 管理所有 Server + SessionManager + PluginRunner
    └── 停止阶段 → 5 阶段关闭（StopAccept → Drain → CloseSessions → StopNonStaged → CloseAll）

Server 生命周期（每个协议独立）
    ├── 启动阶段 → 监听端口 + 启动 WorkerPool
    ├── 运行阶段 → Accept 连接 → 创建 Session
    └── 停止阶段 → 停止 Accept → Drain goroutine → 关闭 Session

Session 生命周期（每个连接独立）
    ├── Connecting → 连接建立中（插件链 OnAccept）
    ├── Active     → 正常通信（readLoop + writeLoop）
    ├── Draining   → 排空写队列（Close 触发）
    └── Closed     → 已关闭（清理资源）

Plugin 生命周期（跨 Session 共享）
    ├── 注册阶段 → PluginRunner.Register
    ├── 执行阶段 → OnAccept / OnMessage（每个 Session 触发）
    └── 关闭阶段 → OnClose（每个 Session 触发，逆序执行）
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
┌──────────────┐
│  Connecting  │  连接建立后，插件链 OnAccept 执行中
└──────┬───────┘
       │ OnAccept 成功
       ↓
┌──────────────┐
│    Active    │  正常通信，readLoop + writeLoop 运行
└──────┬───────┘
       │ Close() 调用 或 错误/超时
       ↓
┌──────────────┐
│   Draining   │  排空写队列，等待 writeLoop 完成
└──────┬───────┘
       │ 写队列清空 或 DrainTimeout
       ↓
┌──────────────┐
│    Closed    │  连接关闭，资源释放
└──────────────┘

异常路径（任意状态）：
  Fatal 错误（ErrFrameTooLarge / panic）→ 直接跳转到 Closed
```

### 2.3 状态转换规则

| 当前状态 | 触发事件 | 目标状态 | 前置条件 | 后置条件 |
|---------|---------|---------|---------|---------|
| Connecting | `OnAccept` 成功 | Active | 插件链返回 nil | 启动 readLoop + writeLoop |
| Connecting | `OnAccept` 返回 `ErrPluginBlock` | Closed | - | 连接立即关闭，不进入 Active |
| Active | `Close()` 调用 | Draining | `sync.Once` 保证单次执行 | `close(draining)` 信号触发排空 |
| Active | 读取错误（EOF / 超时） | Draining | readLoop 检测到错误 | 调用 `Close()` |
| Active | 连续错误超限 | Draining | `consecutiveErrors > MaxConsecutiveErrors` | 调用 `Close()` |
| Draining | 写队列排空 | Closed | `writeQueue` 全部发送完成 | `conn.Close()` |
| Draining | `DrainTimeout` 超时 | Closed | 等待超过 `DrainTimeout` | 强制 `conn.Close()`，记录 `ErrDrainTimeout` |
| Closed | 任意操作 | Closed | - | 返回 `ErrSessionClosed` |

### 2.4 状态转换实现（CAS 保证并发安全）

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
        // CAS 失败，重试
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

### 2.5 Close 六步状态机（长连接协议）

**适用协议：** TCP、WebSocket、QUIC（有写队列的协议）

```go
func (s *TCPSession) Close(ctx context.Context) error {
    // 步骤1：CAS Active → Draining（幂等保护）
    if !s.SetState(Draining) {
        // 已在关闭或已关闭，直接返回
        return nil
    }

    // 步骤2：若 writeLoop 已启动，触发排空信号
    if s.writerStarted.Load() {
        close(s.draining) // 信号：停止接收新消息
    } else {
        // writeLoop 未启动（连接在 Accept 后立即 Close），直接释放 writeQueue 中的 Buffer
        for buf := range s.writeQueue {
            bufferpool.Put(buf)
        }
    }

    // 步骤3：等待 writeQueue 排空（DrainTimeout 控制）
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

    // 步骤4：CAS Draining → Closed
    s.SetState(Closed)

    // 步骤5：CancelContext() → 通知所有 <-ctx.Done() 的 goroutine 退出
    s.cancel()

    // 步骤6：conn.Close()
    return s.conn.Close()
}
```

**步骤3 的两种路径：**

| 场景 | 路径 | 说明 |
|------|------|------|
| writeLoop 已启动 | 等待 `<-s.drained` 或 `<-ctx.Done()` | 正常连接关闭路径 |
| writeLoop 未启动 | 直接释放 `writeQueue` 中的 Buffer | 连接在 Accept 后立即 Close（如 OnAccept 返回 ErrPluginBlock） |

### 2.6 Send 并发安全

```go
func (s *TCPSession) Send(data []byte) error {
    // 检查状态（Draining/Closed 状态拒绝新消息）
    if !s.IsAlive() { // IsAlive() = State() == Active
        return ErrSessionClosed
    }

    // 复制调用方 data，防止 writeQueue 中的 buffer 被调用方复用覆盖
    buf := make([]byte, len(data))
    copy(buf, data)

    // 非阻塞写入
    select {
    case s.writeQueue <- buf:
        return nil
    default:
        return ErrWriteQueueFull // 队列满，立即返回
    }
}
```

**关键约束：**

| 约束 | 实现 | 原因 |
|------|------|------|
| 非阻塞 | `select { case...: default: }` | 避免 Send 阻塞 readLoop |
| 状态检查 | `IsAlive()` 在队列写入前检查 | 拒绝向 Draining/Closed 状态发送 |
| 数据拷贝 | `copy(buf, data)` | 防止调用方复用切片污染 writeQueue |

---

## 3. Gateway 生命周期

### 3.1 状态定义

```go
// Gateway 内部状态（无状态机，用 atomic.Bool 控制）
type Gateway struct {
    started atomic.Bool
    ready   atomic.Bool
    // ...
}
```

### 3.2 启动流程

```go
func (g *Gateway) Start(ctx context.Context) error {
    // 1. 检查是否已启动（幂等保护）
    if !g.started.CompareAndSwap(false, true) {
        return ErrGatewayNotStarted
    }

    // 2. 检查 servers 非空
    if len(g.servers) == 0 {
        g.started.Store(false)
        return ErrNoServerRegistered
    }

    // 3. 向 RuntimeConfigurable server 注入 Runtime
    for _, srv := range g.servers {
        if rc, ok := srv.(RuntimeConfigurable); ok {
            rc.UseRuntime(g.runtime)
        }
    }

    // 4. 顺序启动 servers（任一失败时逆序回滚）
    var started []Server
    for _, srv := range g.servers {
        if err := srv.Start(ctx); err != nil {
            // 回滚：逆序停止已启动的 server
            for i := len(started) - 1; i >= 0; i-- {
                stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
                started[i].Stop(stopCtx)
                cancel()
            }
            g.started.Store(false)
            return fmt.Errorf("failed to start %s: %w", srv.Protocol(), err)
        }
        started = append(started, srv)
    }

    // 5. 标记就绪
    g.ready.Store(true)
    g.startedAt = time.Now()
    g.logger.Info("gateway started",
        "protocols", g.protocolNames(),
        "uptime", time.Since(g.startedAt))

    return nil
}
```

**启动不变量：**

- `Start()` 失败必须回滚，不留半启动状态
- `RuntimeConfigurable` 注入必须在 `Start()` 之前完成
- 所有 Server 按注册顺序启动，失败时逆序停止

### 3.3 停止流程（5 阶段）

```go
func (g *Gateway) Stop(ctx context.Context) error {
    // 幂等检查
    if !g.ready.CompareAndSwap(true, false) {
        return nil // 未启动或已停止
    }

    // 阶段1：StopAccept（停止新连接入口）
    g.logger.Info("gateway stopping: phase 1 - stop accept")
    g.runStage(ctx, "StopAccept", func(srv Server) error {
        if ss, ok := srv.(StagedServer); ok {
            return ss.StopAccept(ctx)
        }
        return nil
    })

    // 阶段2：Drain（等待读写 goroutine 收敛）
    g.logger.Info("gateway stopping: phase 2 - drain")
    g.runStage(ctx, "Drain", func(srv Server) error {
        if ss, ok := srv.(StagedServer); ok {
            return ss.Drain(ctx)
        }
        return nil
    })

    // 阶段3：CloseSessions（关闭协议持有的活跃会话）
    g.logger.Info("gateway stopping: phase 3 - close sessions")
    g.runStage(ctx, "CloseSessions", func(srv Server) error {
        if ss, ok := srv.(StagedServer); ok {
            return ss.CloseSessions(ctx)
        }
        return nil
    })

    // 阶段4：StopNonStaged（停止非 StagedServer 协议）
    g.logger.Info("gateway stopping: phase 4 - stop non-staged")
    g.runStage(ctx, "Stop", func(srv Server) error {
        if _, ok := srv.(StagedServer); !ok {
            return srv.Stop(ctx)
        }
        return nil
    })

    // 阶段5：CloseAll（清理 SessionManager 残留会话）
    g.logger.Info("gateway stopping: phase 5 - close all sessions")
    if err := g.runtime.Sessions().CloseAll(ctx); err != nil {
        g.logger.Warn("close all sessions failed", "error", err)
    }

    g.started.Store(false)
    g.logger.Info("gateway stopped", "uptime", time.Since(g.startedAt))
    return nil
}

// runStage 并发执行阶段任务
func (g *Gateway) runStage(ctx context.Context, stage string, fn func(Server) error) {
    var wg sync.WaitGroup
    for _, srv := range g.servers {
        wg.Add(1)
        go func(s Server) {
            defer wg.Done()
            if err := fn(s); err != nil {
                g.logger.Warn("stage failed",
                    "stage", stage,
                    "protocol", s.Protocol(),
                    "error", err)
            }
        }(srv)
    }
    wg.Wait()
}
```

**停止不变量：**

- `Stop()` 必须可重复调用（幂等）
- 各阶段并发执行（所有相同阶段的 server 并发执行，阶段间串行）
- `CloseAll` 执行前必须确保所有 `StopAccept` 完成（防止新 session 逃脱清理）
- Drain 超时不阻塞后续阶段

### 3.4 CloseAll 并发安全保证

```
阶段顺序保证了 CloseAll 的安全性：

  阶段1 StopAccept 完成后：
    → 所有新连接入口已关闭，不会产生新 session

  阶段2 Drain 完成后：
    → 所有 readLoop goroutine 已退出，不会注册新 session

  阶段3 CloseSessions 完成后：
    → 协议层 session 已关闭

  阶段5 CloseAll：
    → 清理 SessionManager 中可能遗留的 session（应为空或极少数）
    → 若遗留，说明协议层未正确清理，记录 Warn 日志
```

### 3.5 Ready 状态查询

```go
func (g *Gateway) Ready() bool {
    return g.ready.Load()
}

// 健康检查端点使用
func (g *Gateway) Health() map[string]any {
    return map[string]any{
        "status":    g.healthStatus(), // "healthy" / "degraded" / "not_ready"
        "uptime":    time.Since(g.startedAt).String(),
        "protocols": g.protocolNames(),
        "sessions":  g.runtime.Sessions().Count(),
    }
}

func (g *Gateway) healthStatus() string {
    if !g.Ready() {
        return "not_ready"
    }
    // 可扩展：检查外部依赖（Cache/Store/PubSub）
    return "healthy"
}
```

---

## 4. 协议 Server 生命周期

### 4.1 TCP Server 生命周期

```
Start() 阶段：
  1. net.Listen(addr) → listener
  2. WorkerPool.Start() → 启动核心 Worker goroutine
  3. go acceptLoop() → 独立 goroutine 等待连接

acceptLoop() 运行阶段：
  for {
      conn, err = listener.Accept()
      if err != nil {
          if errors.Is(err, net.ErrClosed) {
              return // listener 已关闭，正常退出
          }
          指数退避（5ms → 1s → 10s）
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

handleConn() 处理阶段：
  sess = newTCPSession(conn)
  manager.Register(sess) → 超容：LRU Evict 或拒绝连接
  pluginChain.OnAccept(sess) → ErrPluginBlock：Close(sess) + return
  go sess.readLoop()
  go sess.writeLoop()

readLoop() 读循环：
  for {
      payload, err = Framer.ReadFrame(conn)
      if err != nil {
          sess.Close() // 触发 Close 六步状态机
          return
      }
      sess.TouchActive()
      WorkerPool.Submit(sess, payload)
  }

writeLoop() 写循环：
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

Stop() 阶段（StagedServer 三阶段）：
  StopAccept：
    listener.Close() → acceptLoop 退出
    WorkerPool.Stop() → 等待所有 Worker 完成当前任务

  Drain：
    WaitGroup.Wait() → 等待所有 readLoop / writeLoop 退出

  CloseSessions：
    遍历所有 TCPSession → Close(ctx)
```

### 4.2 UDP Server 生命周期

```
Start() 阶段：
  1. net.ListenUDP(addr) → conn
  2. go readLoop() → 单 goroutine 读取所有 datagram
  3. go sweepLoop() → 定期清理过期伪会话

readLoop() 运行阶段：
  for {
      n, remoteAddr, err = conn.ReadFromUDP(buf)
      if err != nil {
          if errors.Is(err, net.ErrClosed) {
              return // conn 已关闭，正常退出
          }
          continue
      }

      // 查找或创建伪会话
      sess, exists = sessions.Load(remoteAddr.String())
      if !exists {
          tempSess = newUDPSession(remoteAddr)
          if err = pluginChain.OnAccept(tempSess); err != nil {
              if errors.Is(err, ErrPluginBlock) {
                  continue // 不注册伪会话，丢弃 datagram
              }
          }
          sess = tempSess
          sessions.Store(remoteAddr.String(), sess)
          manager.Register(sess)
      }

      sess.TouchActive()
      pluginChain.OnMessage(sess, buf[:n]) → handler
  }

sweepLoop() 清理阶段：
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

Stop() 阶段（非 StagedServer）：
  conn.Close() → readLoop 退出
  sweepLoop ticker.Stop()
  清理所有伪会话
```

### 4.3 WebSocket Server 生命周期

```
Start() 阶段：
  1. http.Server 启动
  2. 注册 Upgrade handler 到指定 Path
  3. go pingLoop() → 为每个 WSSession 启动心跳

Upgrade 处理阶段：
  http.HandleFunc(path, func(w, r)) {
      // Origin 检查（AllowedOrigins 白名单）
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
      pluginChain.OnAccept(sess) → ErrPluginBlock：Close(sess) + return
      go handleSession(sess)
  }

handleSession() 处理阶段：
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
      pluginChain.OnMessage(sess, data) → handler
  }

pingLoop() 心跳阶段：
  ticker := time.NewTicker(PingInterval)
  for {
      select {
      case <-ticker.C:
          sess.sendPing() → WriteMessage(PingMessage, nil)
      case <-sess.Context().Done():
          ticker.Stop()
          return
      }
  }

OnClose 单次执行保证：
  closeOnce.Do(func() {
      pluginChain.OnClose(sess)  ← 在 Once 内部，天然保证单次执行
      manager.Unregister(sess.ID())
      cancel()
      conn.Close()
  })

Stop() 阶段（StagedServer 三阶段）：
  StopAccept：
    停止 HTTP Upgrade Mux（拒绝新 Upgrade 请求）

  Drain：
    等待所有 pingLoop / readLoop 退出

  CloseSessions：
    遍历所有 WSSession → 发送 Close 帧 → Close(ctx)
```

---

## 5. 插件生命周期

### 5.1 注册阶段

```go
func (r *PluginRunner) Register(p Plugin) error {
    r.mu.Lock()
    defer r.mu.Unlock()

    // 检查重复名称
    if idx, exists := r.nameIndex[p.Name()]; exists {
        r.logger.Warn("plugin duplicate name, overwriting",
            "name", p.Name(),
            "old_priority", r.plugins[idx].Priority(),
            "new_priority", p.Priority())
        r.plugins[idx] = p
        return nil
    }

    // 添加插件
    r.plugins = append(r.plugins, p)
    r.nameIndex[p.Name()] = len(r.plugins) - 1

    // 按 Priority 升序排序（启动时一次性排序，热路径无排序开销）
    slices.SortFunc(r.plugins, func(a, b Plugin) int {
        return a.Priority() - b.Priority()
    })

    return nil
}
```

### 5.2 执行阶段

**OnAccept（按 Priority 升序）：**

```go
func (r *PluginRunner) RunAccept(sess Session) error {
    for _, p := range r.plugins {
        start := time.Now()
        err := r.safeRun(p.Name(), func() error {
            return p.OnAccept(sess)
        })
        r.metrics.Histogram("shark_plugin_duration_seconds",
            "plugin", p.Name()).Observe(time.Since(start).Seconds())

        if err != nil {
            if errors.Is(err, ErrPluginBlock) {
                sess.Close(context.Background())
                return err
            }
            if r.stopOnError {
                return err
            }
            r.logger.Warn("plugin OnAccept error",
                "plugin", p.Name(),
                "error", err,
                "session_id", sess.ID())
        }
    }
    return nil
}
```

**OnMessage（按 Priority 升序，支持 payload 改写）：**

```go
func (r *PluginRunner) RunMessage(sess Session, data []byte) ([]byte, error) {
    current := data
    for _, p := range r.plugins {
        start := time.Now()
        var out []byte
        var err error
        r.safeRun(p.Name(), func() error {
            out, err = p.OnMessage(sess, current)
            return err
        })
        r.metrics.Histogram("shark_plugin_duration_seconds",
            "plugin", p.Name()).Observe(time.Since(start).Seconds())

        if err != nil {
            if errors.Is(err, ErrPluginDrop) {
                return nil, err // 停止链，不调用 Handler
            }
            if errors.Is(err, ErrPluginBlock) {
                sess.Close(context.Background())
                return nil, err
            }
            if r.stopOnError {
                return nil, err
            }
            r.logger.Warn("plugin OnMessage error",
                "plugin", p.Name(),
                "error", err,
                "session_id", sess.ID())
            continue
        }
        current = out // 允许 plugin 改写 payload
    }
    return current, nil
}
```

**OnClose（按 Priority 逆序，不可中断）：**

```go
func (r *PluginRunner) RunClose(sess Session) {
    // 逆序执行（确保资源全部释放）
    for i := len(r.plugins) - 1; i >= 0; i-- {
        p := r.plugins[i]
        r.safeRun(p.Name(), func() error {
            p.OnClose(sess)
            return nil
        })
    }
}
```

### 5.3 panic 隔离

```go
func (r *PluginRunner) safeRun(name string, fn func() error) (err error) {
    defer func() {
        if rec := recover(); rec != nil {
            err = fmt.Errorf("plugin panic: %v\nstack: %s", rec, debug.Stack())
            r.logger.Error("plugin panic recovered",
                "plugin", name,
                "panic", rec,
                "stack", string(debug.Stack()))
            r.metrics.Counter("shark_plugin_panics_total",
                "plugin", name).Inc()
        }
    }()
    return fn()
}
```

---

## 6. 连接完整数据流

### 6.1 TCP 连接完整生命周期

```
网络 → listener.Accept()
  ↓
newTCPSession(conn)
  ↓
manager.Register(sess) → 超容：LRU Evict 或拒绝连接
  ↓
pluginChain.OnAccept(sess)
  → ErrPluginBlock：sess.Close() + return（不进入 Active）
  → nil：继续
  ↓
sess.SetState(Active)
  ↓
go sess.readLoop()  +  go sess.writeLoop()

[运行中]
readLoop：
  Framer.ReadFrame(conn) → payload
  sess.TouchActive()
  WorkerPool.Submit(sess, payload)

Worker goroutine：
  pluginChain.OnMessage(sess, payload) → data
    → ErrPluginDrop：停止，不调用 Handler
    → ErrPluginBlock：sess.Close() + return
    → nil：继续
  Handler(sess, Message{Payload: data})
    → sess.Send(response)

writeLoop：
  for data := range writeQueue：
    conn.Write(data)
    bufferpool.Put(data)

[断开]
readLoop 检测到错误（EOF / 超时）
  ↓
sess.Close(ctx)：
  步骤1：CAS Active → Draining
  步骤2：close(draining) → 触发 writeLoop 排空
  步骤3：等待 <-drained 或 DrainTimeout
  步骤4：CAS Draining → Closed
  步骤5：cancel() → 所有 <-ctx.Done() 退出
  步骤6：conn.Close()
  ↓
pluginChain.OnClose(sess)（逆序执行）
  ↓
manager.Unregister(sess.ID())
```

### 6.2 UDP 伪会话生命周期

```
网络 → conn.ReadFromUDP(buf)
  ↓
查找伪会话：sessions.Load(remoteAddr.String())
  → 已存在：复用
  → 不存在：创建新伪会话
      ↓
  newUDPSession(remoteAddr)
      ↓
  pluginChain.OnAccept(tempSess)
    → ErrPluginBlock：丢弃 datagram，不注册伪会话
    → nil：sessions.Store + manager.Register
  ↓
sess.TouchActive()
  ↓
pluginChain.OnMessage(sess, buf) → handler

[清理]
sweepLoop 定期扫描：
  time.Since(sess.LastActiveAt()) > sessionTTL
    ↓
  sess.Close()
  sessions.Delete(key)
  manager.Unregister(sess.ID())
```

---

## 7. 并发 Goroutine 管理

### 7.1 Goroutine 分类

| 类型 | 数量 | 生命周期 | 退出信号 |
|------|------|---------|---------|
| **固定系统 Goroutine** | ~10-20 | 与 Gateway 同生命周期 | Gateway.Stop() → ctx.Done() |
| **per-Protocol Goroutine** | 每协议 1-3 个 | 与 Server 同生命周期 | Server.Stop() → listener.Close() 或 ctx.Done() |
| **per-Session Goroutine** | 每连接 2-3 个 | 与 Session 同生命周期 | Session.Close() → ctx.Done() |
| **WorkerPool Goroutine** | 固定数量 + 临时扩容 | 与 WorkerPool 同生命周期 | WorkerPool.Stop() → close(taskQueue) |

### 7.2 固定系统 Goroutine

```
Gateway 级别（全局唯一）：
  1. HeartbeatPlugin 扫描（P0：ticker；P2：时间轮单 goroutine）
  2. RateLimitPlugin cleanupLoop（清理空闲 IP 桶）
  3. BlacklistPlugin cleanupLoop（清理过期 IP）
  4. PersistencePlugin batchWriter（批量刷盘）
  5. ClusterPlugin 订阅 goroutine（接收跨节点消息）
  6. ClusterPlugin 节点心跳 goroutine
  7. MemoryCache cleanupLoop（清理过期条目）
  8. Metrics HTTP Server（独立 http.Server）
  9. Health HTTP Server（独立 http.Server）
  10. slogLogger 异步写入 goroutine
```

### 7.3 per-Protocol Goroutine

```
TCP Server：
  acceptLoop（1 个）

UDP Server：
  readLoop（1 个）
  sweepLoop（1 个）

CoAP Server（基于 UDP）：
  readLoop（1 个）
  sweepLoop（1 个）
  retransmitLoop（1 个，CON 重传）

WebSocket Server：
  无独立 goroutine（复用 http.Server 的 goroutine 池）

HTTP Server：
  无独立 goroutine（复用 http.Server 的 goroutine 池）
```

### 7.4 per-Session Goroutine

```
TCP Session：
  readLoop（1 个）
  writeLoop（1 个）

UDP Session：
  无（复用 Server readLoop）

CoAP Session：
  无（复用 Server readLoop）

WebSocket Session：
  handleSession（1 个，包含 readLoop）
  pingLoop（1 个）
  writeLoop（隐式，由 handleSession 调用 Send 时触发）

HTTP Session（per-request）：
  无（http.Server 内部 goroutine）
```

### 7.5 Goroutine 泄漏防护

**退出路径保证（每个 goroutine 至少 3 个退出路径）：**

```go
// 示例：readLoop
func (s *TCPSession) readLoop() {
    defer func() {
        s.Close(context.Background()) // 路径1：defer 保证关闭
    }()

    for {
        select {
        case <-s.ctx.Done(): // 路径2：context cancel
            return
        default:
        }

        payload, err := s.framer.ReadFrame(s.conn)
        if err != nil {
            if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
                return // 路径3：连接关闭
            }
            s.logger.Error("read frame error", "error", err)
            return
        }

        // 处理消息...
    }
}
```

**WorkerPool HandlerTimeout 保护：**

```go
func (w *WorkerPool) worker() {
    for task := range w.taskQueue {
        ctx := task.sess.Context()
        if w.handlerTimeout > 0 {
            var cancel context.CancelFunc
            ctx, cancel = context.WithTimeout(ctx, w.handlerTimeout)
            defer cancel()
        }

        done := make(chan struct{})
        go func() {
            w.safeRun(func() {
                task.handler(task.sess, task.msg)
            })
            close(done)
        }()

        select {
        case <-done:
            // 正常完成
        case <-ctx.Done():
            w.logger.Warn("handler timeout",
                "session_id", task.sess.ID(),
                "timeout", w.handlerTimeout)
            // goroutine 泄漏，但不阻塞 Worker
        }
    }
}
```
