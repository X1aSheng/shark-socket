# TRANSPORT.md - Part 1/5: TCP

> Shark-Socket 传输层:TCP 协议实现细节  
> 版本:v0.2.x-alpha  
> 最后更新:2026-06-01

---

## 目录(TCP 部分)

1. [TCP 协议概述](#1-tcp-协议概述)
2. [Framer 接口与内置实现](#2-framer-接口与内置实现)
3. [TCPSession](#3-tcpsession)
4. [TCPServer](#4-tcpserver)
5. [TCPClient](#5-tcpclient)
6. [配置项完整参考](#6-配置项完整参考)

---

## 1. TCP 协议概述

### 1.1 在框架中的定位

TCP 是框架的**核心传输协议**,其设计模式是其他协议实现的参考基础:

- **Framer 接口**:解决 TCP 字节流的帧边界问题
- **写队列 + drain**:Session 关闭的六步状态机在 TCP 中首次定义
- **WorkerPool**:消息处理并发模型
- **StagedServer**:TCP 完整实现三阶段关闭

### 1.2 文件清单

```
internal/transport/tcp/
+-- framer.go    # Framer 接口 + 4 种内置实现
+-- session.go   # TCPSession:写队列 + drain + 6 步 Close
+-- server.go    # TCPServer:accept + Framer + WorkerPool + StagedServer
+-- client.go    # TCPClient:自动重连 + 指数退避
+-- options.go   # TCPOption Functional Options
```

### 1.3 编译期验证

```go
// internal/transport/tcp/server.go
var _ core.Server               = (*Server)(nil)
var _ core.RuntimeConfigurable  = (*Server)(nil)
var _ core.StagedServer         = (*Server)(nil)

// internal/transport/tcp/session.go
var _ core.Session = (*Session)(nil)
```

---

## 2. Framer 接口与内置实现

### 2.1 Framer 接口

```go
// internal/transport/tcp/framer.go

// Framer 解决 TCP 字节流的帧边界问题.
// 实现约束:
//   - ReadFrame 必须无状态,不跨调用保持预读缓冲(禁止使用 bufio.Reader 跨调用)
//   - WriteFrame 必须保证完整写入(处理短写,不允许部分发送)
//   - ReadFrame 和 WriteFrame 不需要并发安全(调用方保证单 goroutine 调用)
type Framer interface {
    ReadFrame(r io.Reader) ([]byte, error)
    WriteFrame(w io.Writer, payload []byte) error
}
```

**禁止使用 `bufio.Reader` 跨调用的原因:**

```
问题场景:
  bufio.Reader 内部预读缓冲跨 ReadFrame 调用保持状态
  若 ReadFrame 返回后调用方切换到不同 reader 或重用连接
  预读数据已被消费,后续 ReadFrame 从错误位置读取
  导致帧错位,丢失后续消息

正确方案:
  使用 io.ReadFull 精确读取固定字节数(无状态,每次调用独立)
  逐字节读到分隔符(LineFramer 方案)
```

### 2.2 LengthPrefixFramer(默认推荐)

```go
// LengthPrefixFramer 使用 4 字节大端长度前缀分帧.
// 格式:[4 字节大端长度][payload 字节]
type LengthPrefixFramer struct {
    MaxFrameSize int // 默认 1MB,防大包攻击
}

func (f *LengthPrefixFramer) ReadFrame(r io.Reader) ([]byte, error) {
    // 读取 4 字节长度头
    var header [4]byte
    if _, err := io.ReadFull(r, header[:]); err != nil {
        return nil, err
    }

    length := binary.BigEndian.Uint32(header[:])

    // 帧大小检查
    if f.MaxFrameSize > 0 && int(length) > f.MaxFrameSize {
        return nil, fmt.Errorf("%w: frame size %d exceeds max %d",
            core.ErrFrameTooLarge, length, f.MaxFrameSize)
    }
    if length == 0 {
        return []byte{}, nil // 空帧(心跳)
    }

    // 读取 payload
    payload := make([]byte, length)
    if _, err := io.ReadFull(r, payload); err != nil {
        return nil, err
    }
    return payload, nil
}

func (f *LengthPrefixFramer) WriteFrame(w io.Writer, payload []byte) error {
    length := len(payload)
    if f.MaxFrameSize > 0 && length > f.MaxFrameSize {
        return fmt.Errorf("%w: payload size %d exceeds max %d",
            core.ErrFrameTooLarge, length, f.MaxFrameSize)
    }

    // 写入 4 字节长度头
    var header [4]byte
    binary.BigEndian.PutUint32(header[:], uint32(length))

    // 一次性写入头 + payload,防止短写
    buf := make([]byte, 4+length)
    copy(buf[:4], header[:])
    copy(buf[4:], payload)
    _, err := w.Write(buf)
    return err
}
```

**短写处理说明:**

```
问题:net.Conn.Write(buf) 可能只写入部分字节(短写)
场景:buf 长度超过系统发送缓冲区,内核只接受部分数据

正确方案(当前实现):
  将 header + payload 合并为单个 buf 一次性写入
  net.Conn 的 Write 实现会内部重试直到全部写入或返回错误
  禁止分两次调用 Write(header 和 payload 分开写可能导致帧截断)

错误方案(禁止):
  w.Write(header[:])   <- 可能短写
  w.Write(payload)     <- 第二次写可能和其他 goroutine 的写交错
```

### 2.3 LineFramer

```go
// LineFramer 以换行符 \n 作为帧分隔符,适用于文本行协议.
// 使用逐字节读取,保持无状态(不使用 bufio.Reader).
type LineFramer struct {
    MaxLineSize int // 默认 64KB,防内存耗尽
}

func (f *LineFramer) ReadFrame(r io.Reader) ([]byte, error) {
    var buf []byte
    oneByte := make([]byte, 1)

    for {
        _, err := r.Read(oneByte)
        if err != nil {
            return nil, err
        }

        if oneByte[0] == '\n' {
            // 去除末尾 \r(兼容 \r\n)
            if len(buf) > 0 && buf[len(buf)-1] == '\r' {
                buf = buf[:len(buf)-1]
            }
            return buf, nil
        }

        buf = append(buf, oneByte[0])

        if f.MaxLineSize > 0 && len(buf) > f.MaxLineSize {
            return nil, fmt.Errorf("%w: line exceeds max size %d",
                core.ErrFrameTooLarge, f.MaxLineSize)
        }
    }
}

func (f *LineFramer) WriteFrame(w io.Writer, payload []byte) error {
    // 一次性写入 payload + \n
    buf := make([]byte, len(payload)+1)
    copy(buf, payload)
    buf[len(payload)] = '\n'
    _, err := w.Write(buf)
    return err
}
```

### 2.4 FixedSizeFramer

```go
// FixedSizeFramer 使用固定长度帧,适用于硬件协议,传感器数据.
type FixedSizeFramer struct {
    FrameSize int // 必须 > 0
}

func (f *FixedSizeFramer) ReadFrame(r io.Reader) ([]byte, error) {
    if f.FrameSize <= 0 {
        return nil, errors.New("shark: FixedSizeFramer.FrameSize must be > 0")
    }
    payload := make([]byte, f.FrameSize)
    if _, err := io.ReadFull(r, payload); err != nil {
        return nil, err
    }
    return payload, nil
}

func (f *FixedSizeFramer) WriteFrame(w io.Writer, payload []byte) error {
    if len(payload) != f.FrameSize {
        return fmt.Errorf("shark: payload size %d != fixed frame size %d",
            len(payload), f.FrameSize)
    }
    _, err := w.Write(payload)
    return err
}
```

### 2.5 RawFramer

```go
// RawFramer 直接透传,单次 Read 返回.
// 注意:TCP 是字节流协议,单次 Read 不保证读取完整消息,调用方需自行处理粘包.
// 适用场景:简单测试,已知对端总是发送完整消息.
type RawFramer struct {
    BufferSize int // 单次读取缓冲大小,默认 4096
}

func (f *RawFramer) ReadFrame(r io.Reader) ([]byte, error) {
    size := f.BufferSize
    if size <= 0 {
        size = 4096
    }
    buf := make([]byte, size)
    n, err := r.Read(buf)
    if n > 0 {
        return buf[:n], nil
    }
    return nil, err
}

func (f *RawFramer) WriteFrame(w io.Writer, payload []byte) error {
    _, err := w.Write(payload)
    return err
}
```

---

## 3. TCPSession

### 3.1 结构定义

```go
// internal/transport/tcp/session.go
type Session struct {
    // 身份(不可变)
    id         uint64
    remoteAddr net.Addr
    localAddr  net.Addr
    createdAt  time.Time

    // 协议
    conn   net.Conn
    framer Framer

    // 状态
    state      atomic.Int32  // core.SessionState
    lastActive atomic.Int64  // UnixNano,TouchActive 无锁更新
    ctx        context.Context
    cancel     context.CancelFunc

    // 写队列
    writeQueue chan []byte
    draining   chan struct{} // 关闭信号:Close 步骤2 触发
    drained    chan struct{} // 完成信号:writeLoop 发送完成后关闭

    // 并发控制
    closeOnce     sync.Once
    writerStarted atomic.Bool

    // 元数据
    meta sync.Map

    // 可观测
    logger  core.Logger
    metrics core.Metrics
}

// 编译期验证
var _ core.Session = (*Session)(nil)
```

### 3.2 构造函数

```go
func newSession(
    id uint64,
    conn net.Conn,
    framer Framer,
    writeQueueSize int,
    logger core.Logger,
    metrics core.Metrics,
) *Session {
    ctx, cancel := context.WithCancel(context.Background())
    sess := &Session{
        id:         id,
        remoteAddr: conn.RemoteAddr(),
        localAddr:  conn.LocalAddr(),
        createdAt:  time.Now(),
        conn:       conn,
        framer:     framer,
        writeQueue: make(chan []byte, writeQueueSize),
        draining:   make(chan struct{}),
        drained:    make(chan struct{}),
        ctx:        ctx,
        cancel:     cancel,
        logger:     logger,
        metrics:    metrics,
    }
    sess.state.Store(int32(core.Connecting))
    sess.lastActive.Store(time.Now().UnixNano())
    return sess
}
```

### 3.3 核心接口实现

```go
func (s *Session) ID() uint64             { return s.id }
func (s *Session) Protocol() core.Protocol { return core.TCP }
func (s *Session) RemoteAddr() net.Addr   { return s.remoteAddr }
func (s *Session) LocalAddr() net.Addr    { return s.localAddr }
func (s *Session) CreatedAt() time.Time   { return s.createdAt }
func (s *Session) Context() context.Context { return s.ctx }

func (s *Session) State() core.SessionState {
    return core.SessionState(s.state.Load())
}

func (s *Session) IsAlive() bool {
    return s.State() == core.Active
}

func (s *Session) LastActiveAt() time.Time {
    return time.Unix(0, s.lastActive.Load())
}

func (s *Session) TouchActive() {
    s.lastActive.Store(time.Now().UnixNano())
}

// 元数据
func (s *Session) SetMeta(key string, val any) { s.meta.Store(key, val) }
func (s *Session) GetMeta(key string) (any, bool) { return s.meta.Load(key) }
func (s *Session) DelMeta(key string) { s.meta.Delete(key) }
```

### 3.4 Send 实现

```go
func (s *Session) Send(data []byte) error {
    if !s.IsAlive() {
        return core.ErrSessionClosed
    }

    // 复制调用方 data,防止 writeQueue 中的 buffer 被调用方复用覆盖
    buf := make([]byte, len(data))
    copy(buf, data)

    select {
    case s.writeQueue <- buf:
        s.metrics.IncCounter("messages_total", "protocol", "tcp",
            "direction", "out")
        return nil
    default:
        s.metrics.IncCounter("write_queue_full_total", "protocol", "tcp")
        return core.ErrWriteQueueFull
    }
}
```

### 3.5 Close 六步实现

```go
func (s *Session) Close(ctx context.Context) error {
    var closeErr error
    s.closeOnce.Do(func() {
        closeErr = s.doClose(ctx)
    })
    return closeErr
}

func (s *Session) doClose(ctx context.Context) error {
    // 步骤1:CAS Active -> Draining
    if !s.state.CompareAndSwap(int32(core.Active), int32(core.Draining)) {
        // 已在关闭或已关闭,直接返回
        return nil
    }

    // 步骤2:触发排空信号
    if s.writerStarted.Load() {
        close(s.draining) // 通知 writeLoop 开始排空
    } else {
        // writeLoop 未启动,直接释放 writeQueue 中的待发数据
        for {
            select {
            case <-s.writeQueue:
                // 丢弃
            default:
                goto skipDrain
            }
        }
    skipDrain:
    }

    // 步骤3:等待排空完成
    if s.writerStarted.Load() {
        select {
        case <-s.drained:
            // 正常排空完成
        case <-ctx.Done():
            // DrainTimeout 超时,强制继续
            s.logger.Warn("session drain timeout",
                "session_id", s.id,
                "remote_addr", s.remoteAddr.String())
        }
    }

    // 步骤4:CAS Draining -> Closed
    s.state.Store(int32(core.Closed))

    // 步骤5:CancelContext -> 通知所有 <-ctx.Done() 退出
    s.cancel()

    // 步骤6:关闭底层连接
    err := s.conn.Close()

    // sessions_active / sessions_accepted_total / sessions_closed_total
    // 由 runtime 的 metricSessionManager 统一维护,传输层不单独读写
    s.logger.Info("session closed",
        "session_id", s.id,
        "remote_addr", s.remoteAddr.String())

    return err
}
```

### 3.6 readLoop 与 writeLoop

```go
func (s *Session) readLoop(
    handler core.Handler,
    plugins core.PluginRunner,
    pool *WorkerPool,
    options *Options,
) {
    defer func() {
        drainCtx, cancel := context.WithTimeout(
            context.Background(), options.DrainTimeout)
        defer cancel()
        s.Close(drainCtx)
    }()

    consecutiveErrors := 0

    for {
        // 设置读超时
        if options.ReadTimeout > 0 {
            s.conn.SetReadDeadline(time.Now().Add(options.ReadTimeout))
        }

        payload, err := s.framer.ReadFrame(s.conn)
        if err != nil {
            if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
                return // 正常关闭
            }

            consecutiveErrors++
            s.metrics.IncCounter("transport_errors_total", "protocol", "tcp",
                "type", "read")
            s.logger.Error("read frame error",
                "session_id", s.id,
                "error", err,
                "consecutive_errors", consecutiveErrors)

            if consecutiveErrors > options.MaxConsecutiveErrors {
                s.logger.Warn("max consecutive errors exceeded, closing",
                    "session_id", s.id)
                return
            }
            continue
        }

        consecutiveErrors = 0
        s.TouchActive()

        s.metrics.IncCounter("messages_total", "protocol", "tcp", "direction", "in")
        // core.Metrics 没有"按量累加"的 API,字节数这类指标应使用
        // ObserveHistogram 记录分布,而不是把它当累加计数器

        // 插件链处理
        data, err := plugins.OnMessage(s, payload)
        if err != nil {
            if errors.Is(err, core.ErrPluginDrop) {
                continue // 消息被丢弃,继续读取
            }
            return // ErrPluginBlock 或其他致命错误
        }

        // 提交到 WorkerPool
        msg := core.Message{
            SessionID: s.id,
            Protocol:  core.TCP,
            Payload:   data,
        }
        if err := pool.Submit(s, msg, handler); err != nil {
            if errors.Is(err, core.ErrSessionClosed) {
                return
            }
            s.logger.Warn("submit to worker pool failed",
                "session_id", s.id,
                "error", err)
        }
    }
}

func (s *Session) writeLoop(options *Options) {
    s.writerStarted.Store(true)
    defer close(s.drained) // 关闭时通知 Close 步骤3

    for {
        select {
        case data, ok := <-s.writeQueue:
            if !ok {
                return // writeQueue 已关闭
            }
            s.writeWithTimeout(data, options.WriteTimeout)

        case <-s.draining:
            // 排空剩余消息
            for {
                select {
                case data := <-s.writeQueue:
                    s.writeWithTimeout(data, options.WriteTimeout)
                default:
                    return // 排空完成
                }
            }

        case <-s.ctx.Done():
            return
        }
    }
}

func (s *Session) writeWithTimeout(data []byte, timeout time.Duration) {
    if timeout > 0 {
        s.conn.SetWriteDeadline(time.Now().Add(timeout))
    }
    if _, err := s.conn.Write(data); err != nil {
        s.metrics.IncCounter("transport_errors_total", "protocol", "tcp", "type", "write")
        s.logger.Error("write error",
            "session_id", s.id,
            "error", err)
    }
}
```

---

## 4. TCPServer

### 4.1 结构定义

```go
// internal/transport/tcp/server.go
type Server struct {
    options  Options
    listener net.Listener
    pool     *WorkerPool
    handler  core.Handler

    // Runtime(通过 UseRuntime 注入)
    runtime core.Runtime

    // 并发控制
    wg      sync.WaitGroup
    closed  atomic.Bool

    // 编译期验证
    // var _ core.Server              = (*Server)(nil)
    // var _ core.RuntimeConfigurable = (*Server)(nil)
    // var _ core.StagedServer        = (*Server)(nil)
}
```

### 4.2 UseRuntime 注入

```go
func (s *Server) UseRuntime(rt core.Runtime) {
    s.runtime = rt
}

func (s *Server) Protocol() core.Protocol {
    return core.TCP
}
```

### 4.3 Start 实现

```go
func (s *Server) Start(ctx context.Context) error {
    if !s.started.CompareAndSwap(false, true) {
        return fmt.Errorf("tcp server already started")
    }
    if s.rt == nil {
        s.rt = runtime.NewRuntime(nil, nil)
    }
    s.closed.Store(false)
    // 连接准入:并发上限 + 接受速率(令牌桶,burst=1).
    s.acceptor = shared.NewAcceptor(s.opts.MaxConnections, s.opts.AcceptRate)
    s.pool = shared.NewWorkerPool(s.opts.Handler, s.rt.Logger(),
        s.opts.WorkerCount, s.opts.TaskQueueSize, s.opts.FullPolicy)
    s.pool.Start(s.opts.WorkerCount)

    ln, err := net.Listen("tcp", s.opts.Addr)
    if err != nil {
        // 监听失败必须回收已经启动的 worker,否则失败路径会泄漏 goroutine.
        s.pool.Stop()
        s.started.Store(false)
        return fmt.Errorf("tcp listen %s: %w", s.opts.Addr, err)
    }
    if s.opts.TLSConfig != nil {
        ln = tls.NewListener(ln, s.opts.TLSConfig)
    }
    s.listener = ln

    s.acceptWG.Add(1)
    go s.acceptLoop(ctx)
    return nil
}
```

四个易被忽略的点:

- **池先于监听启动**:监听失败时可以在同一函数里 `pool.Stop()` 回收;若顺序反过来,
  失败路径就多了一个"池已启动但没人会在需要时停止它"的窗口.
- **`tls.NewListener` 包装 `net.Listener`**:让绑定失败与证书失败在错误信息上区分得更清楚,
  也避免 `tls.Listen` 内部重复解析地址.
- **`started` 用 `CompareAndSwap` 而非 `Load`**:并发调用 `Start` 时只有一个能继续,
  重复启动返回错误而不是留下两个 acceptLoop.
- **`s.pool.Stop()` 先于 `s.started.Store(false)`**:保证任何观察到 `started == false`
  的调用方看到的是一个已经收拾干净的 Server.

### 4.4 acceptLoop 实现

```go
func (s *Server) acceptLoop() {
    logger := s.runtime.Logger()
    backoff := newExponentialBackoff(5*time.Millisecond, 1*time.Second)
    consecutiveErrors := 0

    for {
        conn, err := s.listener.Accept()
        if err != nil {
            if errors.Is(err, net.ErrClosed) {
                return // listener 已关闭,正常退出
            }

            consecutiveErrors++
            logger.Error("accept error",
                "protocol", "tcp",
                "error", err,
                "consecutive_errors", consecutiveErrors)

            if consecutiveErrors > s.options.MaxConsecutiveErrors {
                logger.Error("max consecutive accept errors, stopping server",
                    "protocol", "tcp")
                return
            }

            // 指数退避
            time.Sleep(backoff.Next())
            continue
        }

        consecutiveErrors = 0
        backoff.Reset()
        go s.handleConn(conn)
    }
}
```

### 4.5 handleConn 实现

```go
func (s *Server) handleConn(conn net.Conn) {
    manager := s.runtime.Sessions()
    plugins := s.runtime.Plugins()
    logger  := s.runtime.Logger()
    metrics := s.runtime.Metrics()

    // 创建 Session
    sessionID := manager.NextID()
    sess := newSession(sessionID, conn, s.options.Framer,
        s.options.WriteQueueSize, logger, metrics)

    // 注册 Session
    if err := manager.Register(sess); err != nil {
        logger.Warn("session register failed",
            "error", err,
            "remote_addr", conn.RemoteAddr().String())
        conn.Close()
        return
    }

    // sessions_accepted_total / sessions_active 由 runtime 的 metricSessionManager
    // 在 Register/Unregister 路径统一维护,传输层不单独读写

    // 插件链 OnAccept
    if err := plugins.OnAccept(sess); err != nil {
        // OnAccept 内部已调用 sess.Close()
        manager.Unregister(sessionID)
        return
    }

    // 转为 Active 状态
    sess.state.Store(int32(core.Active))
    logger.Info("session accepted",
        "session_id", sessionID,
        "remote_addr", conn.RemoteAddr().String(),
        "protocol", "tcp")

    // 启动读写 goroutine
    s.wg.Add(2)
    go func() {
        defer s.wg.Done()
        defer func() {
            plugins.OnClose(sess)
            manager.Unregister(sessionID)
        }()
        sess.readLoop(s.handler, plugins, s.pool, &s.options)
    }()
    go func() {
        defer s.wg.Done()
        sess.writeLoop(&s.options)
    }()
}
```

### 4.6 StagedServer 三阶段实现

```go
// StopAccept 停止接受新连接,停止 WorkerPool 接收新任务.
func (s *Server) StopAccept(ctx context.Context) error {
    if s.listener != nil {
        s.listener.Close() // acceptLoop 检测到 net.ErrClosed 后退出
    }
    s.pool.Stop() // 等待 Worker 完成当前任务后退出
    s.runtime.Logger().Info("tcp server: stop accept done", "protocol", "tcp")
    return nil
}

// Drain 等待所有 readLoop / writeLoop goroutine 退出.
func (s *Server) Drain(ctx context.Context) error {
    done := make(chan struct{})
    go func() {
        s.wg.Wait()
        close(done)
    }()

    select {
    case <-done:
        s.runtime.Logger().Info("tcp server: drain done", "protocol", "tcp")
        return nil
    case <-ctx.Done():
        s.runtime.Logger().Warn("tcp server: drain timeout", "protocol", "tcp")
        return ctx.Err()
    }
}

// CloseSessions 关闭所有活跃 TCPSession.
func (s *Server) CloseSessions(ctx context.Context) error {
    s.runtime.Sessions().Range(func(sess core.Session) bool {
        if sess.Protocol() == core.TCP {
            sess.Close(ctx)
        }
        return true
    })
    s.runtime.Logger().Info("tcp server: close sessions done", "protocol", "tcp")
    return nil
}

// Stop 完整停止(非 StagedServer 场景使用).
func (s *Server) Stop(ctx context.Context) error {
    if err := s.StopAccept(ctx); err != nil {
        return err
    }
    if err := s.Drain(ctx); err != nil {
        return err
    }
    return s.CloseSessions(ctx)
}
```

---

## 5. TCPClient

### 5.1 结构定义

```go
// internal/transport/tcp/client.go
type Client struct {
    options ClientOptions
    conn    net.Conn
    framer  Framer

    mu      sync.Mutex
    closed  atomic.Bool
}

type ClientOptions struct {
    Addr            string
    TLSConfig       *tls.Config
    ConnectTimeout  time.Duration // 默认 10s
    ReadTimeout     time.Duration // 默认 0(不限)
    WriteTimeout    time.Duration // 默认 10s
    Framer          Framer        // 默认 LengthPrefixFramer
    AutoReconnect   bool          // 默认 false
    ReconnectBackoff BackoffOptions
}

type BackoffOptions struct {
    InitialDelay time.Duration // 默认 100ms
    MaxDelay     time.Duration // 默认 30s
    JitterFactor float64       // 默认 0.2(+/-20% 随机抖动)
}
```

### 5.2 Connect 实现

```go
func (c *Client) Connect() error {
    c.mu.Lock()
    defer c.mu.Unlock()

    if c.closed.Load() {
        return core.ErrServerClosed
    }

    dialTimeout := c.options.ConnectTimeout
    if dialTimeout <= 0 {
        dialTimeout = 10 * time.Second
    }

    var conn net.Conn
    var err error

    if c.options.TLSConfig != nil {
        dialer := &tls.Dialer{Config: c.options.TLSConfig}
        ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
        defer cancel()
        conn, err = dialer.DialContext(ctx, "tcp", c.options.Addr)
    } else {
        conn, err = net.DialTimeout("tcp", c.options.Addr, dialTimeout)
    }

    if err != nil {
        return fmt.Errorf("tcp client dial %s: %w", c.addr, err)
    }
    if tlsConn, ok := conn.(*tls.Conn); ok {
        if err := tlsConn.HandshakeContext(ctx); err != nil {
            _ = conn.Close()
            return fmt.Errorf("tcp client tls handshake: %w", err)
        }
    }
    return nil
}
```

### 5.3 Send 与 Receive

```go
func (c *Client) Send(data []byte) error {
    c.mu.Lock()
    defer c.mu.Unlock()

    if c.conn == nil {
        return core.ErrServerNotStarted
    }

    if c.options.WriteTimeout > 0 {
        c.conn.SetWriteDeadline(time.Now().Add(c.options.WriteTimeout))
    }

    return c.options.Framer.WriteFrame(c.conn, data)
}

func (c *Client) Receive() ([]byte, error) {
    if c.conn == nil {
        return nil, core.ErrServerNotStarted
    }

    if c.options.ReadTimeout > 0 {
        c.conn.SetReadDeadline(time.Now().Add(c.options.ReadTimeout))
    }

    return c.options.Framer.ReadFrame(c.conn)
}
```

### 5.4 自动重连

```go
func (c *Client) ConnectWithRetry(ctx context.Context) error {
    backoff := newExponentialBackoff(
        c.options.ReconnectBackoff.InitialDelay,
        c.options.ReconnectBackoff.MaxDelay,
    )

    for {
        if err := c.Connect(); err == nil {
            return nil
        }

        delay := backoff.NextWithJitter(c.options.ReconnectBackoff.JitterFactor)
        select {
        case <-time.After(delay):
            // 继续重试
        case <-ctx.Done():
            return ctx.Err()
        }
    }
}
```

### 5.5 Close

```go
func (c *Client) Close() error {
    if c.closed.CompareAndSwap(false, true) {
        c.mu.Lock()
        defer c.mu.Unlock()
        if c.conn != nil {
            return c.conn.Close()
        }
    }
    return nil
}
```

---

## 6. 配置项完整参考

### 6.1 Server 配置

```go
// internal/transport/tcp/options.go
type Options struct {
    // 网络
    Addr      string      // 默认 "127.0.0.1:18000"
    TLSConfig *tls.Config // 默认 nil(不启用 TLS)

    // 帧解析
    Framer Framer // 默认 LengthPrefixFramer{MaxFrameBytes: 1MB}

    // 会话与队列
    WriteQueue          int     // 每连接写队列容量,默认 128
    WriteQueueHighWater float64 // 写队列高水位比例,默认 0.8(用于反压判定)
    MaxConnections      int64   // 并发连接上限,<= 0 不限制
    AcceptRate          float64 // 接受速率(连接/秒,令牌桶 burst=1),<= 0 不限制

    // 超时
    ReadTimeout  time.Duration // 每帧读超时,默认 5m(防止 slowloris);0 关闭
    WriteTimeout time.Duration // 默认 30s
    DrainTimeout time.Duration // 默认 5s

    // 帧长度上限(独立于 Framer 自身的限制)
    MaxFrameBytes int // 默认 1MB

    // 用户 handler(由 Gateway 注入)
    Handler core.Handler

    // WorkerPool
    WorkerCount   int        // 默认 4
    TaskQueueSize int        // 默认 512
    FullPolicy    FullPolicy // 默认 PolicyDrop
}
```

与早期草案的差异(全部为有意去除):

| 草案字段 | 现状 | 原因 |
|----------|------|------|
| `MaxSessions` | 去掉 | 会话上限由 `SessionManager` 统一管(默认 100 万),传输层不再各管一套 |
| `MaxMessageSize` | 改为 `MaxFrameBytes` | 尺寸限制属于帧解析层,与 `Framer` 同源 |
| `IdleTimeout` | 去掉 | 空闲回收由 `ReadTimeout` + `HeartbeatPlugin` 表达 |
| `HandlerTimeout` | 去掉 | 池不引入第二种并发语义,见 `LIFECYCLE.md sec.7.5` |
| `MaxConsecutiveErrors` | 去掉 | 未实现;帧错误一律关连接,不做连续计数 |
| `WorkerPoolOptions` 嵌套 | 摊平 | 只有一个使用方,嵌套结构没有收益 |
| `MaxWorkers` / `OverloadWindow` | 去掉 | 依赖未实现的 `PolicySpawnTemp` / `PolicyClose` 过载窗口 |

新增的三项是对已发现问题的直接回应:

- `WriteQueueHighWater`:`WriteQueue` 满即丢太晚,达到高水位时提前反压.
- `MaxConnections`:在 `Accept` 之后立即判定,超额连接直接 `Close`,不进会话管理.
- `AcceptRate`:`shared.Acceptor` 令牌桶(burst=1),压制连接风暴.

### 6.2 Server Functional Options

```go
type Option func(*Options)

func WithAddr(addr string) Option
func WithHostPort(host, port string) Option            // net.JoinHostPort
func WithHandler(handler core.Handler) Option
func WithTLS(config *tls.Config) Option
func WithFramer(framer Framer) Option                  // nil 忽略,保留默认
func WithReadTimeout(timeout time.Duration) Option      // 无条件赋值,可把默认关闭
func WithWriteQueue(size int) Option                   // <= 0 忽略
func WithWorkers(count, queueSize int, policy FullPolicy) Option
func WithMaxConnections(max int64) Option              // <= 0 不限制
func WithAcceptRate(rate float64) Option               // <= 0 不限制

func defaultOptions() Options {
    return Options{
        Addr:                "127.0.0.1:18000",
        Framer:              LengthPrefixFramer{MaxFrameBytes: 1024 * 1024},
        WriteQueue:          128,
        WriteTimeout:        30 * time.Second,
        ReadTimeout:         5 * time.Minute,
        WriteQueueHighWater: 0.8,
        WorkerCount:         4,
        TaskQueueSize:       512,
        FullPolicy:          PolicyDrop,
        DrainTimeout:        5 * time.Second,
        MaxFrameBytes:       1024 * 1024,
    }
}
```

**`WithReadTimeout` 是有意"无条件赋值"的:** 默认值非零(5m),而调用方需要能把它关掉,
所以这里不能像其它 option 那样写 `if timeout > 0`.其余带 `> 0` 守卫的 option 用于避免
调用方传 0 时把有用默认值覆盖成"无限制".

**WorkerCount 默认 4 而不是 `NumCPU*2`:** handler 通常是 I/O 等待型,worker 数超过 4 之后
吞吐收益迅速消失,而队列深度的影响更大(512).这个默认值来自压测,不是理论推导.

---

# TRANSPORT.md - Part 2/5: UDP

> Shark-Socket 传输层:UDP 协议实现细节  
> 版本:v0.2.x-alpha  
> 最后更新:2026-06-01

---

## 目录(UDP 部分)

1. [UDP 协议概述](#1-udp-协议概述)
2. [伪会话模型](#2-伪会话模型)
3. [Server 实现](#3-server-实现)
4. [Options 配置](#4-options-配置)
5. [数据流](#5-数据流)
6. [与 CoAP 的关系](#6-与-coap-的关系)

---

## 1. UDP 协议概述

UDP 是无连接的传输协议,Shark-Socket 在其上实现了**伪会话(pseudo-session)**模型:

- 每个唯一的远端地址 `(*net.UDPAddr)` 对应一个伪会话
- 伪会话具有完整的 `core.Session` 生命周期(Register -> Active -> Closed)
- 通过 TTL 和定时清扫机制自动淘汰不活跃的伪会话

**与 TCP 的关键区别:**

| 特性 | TCP | UDP |
|------|-----|-----|
| 连接模型 | 有连接,per-conn goroutine | 无连接,单 conn + 远端地址映射 |
| Session 创建时机 | Accept 时 | 首次收到数据报时 |
| Session 销毁时机 | 连接关闭 | TTL 超时或主动关闭 |
| Framer | 可插拔(LengthPrefix/Line/FixedSize/Raw) | 无(原始数据报) |
| 写入方式 | 异步写队列 + writeLoop | 直接 `WriteToUDP` |
| 并发模型 | per-connection goroutine | 单 `readLoop` + 单 `sweepLoop` |

---

## 2. 伪会话模型

### 2.1 Session 结构

```go
type session struct {
    id        uint64
    conn      *net.UDPConn      // 共享的 UDP 连接
    remote    *net.UDPAddr      // 远端地址(会话标识)
    local     net.Addr          // 本地地址
    createdAt time.Time
    activeAt  atomic.Int64      // 最后活跃时间(UnixNano)
    state     atomic.Uint32     // SessionState
    meta      sync.Map          // 元数据
    ctx       context.Context
    cancel    context.CancelFunc
    closeOnce sync.Once
}
```

**关键设计:**
- `activeAt` 使用 `atomic.Int64` 存储 `time.Time.UnixNano()`,避免锁竞争
- `touch()` 在每次收到数据报时调用,更新活跃时间
- `Close()` 使用 `sync.Once` 保证幂等
- `Send()` 直接调用 `conn.WriteToUDP()`,无需写队列(UDP 无背压)

### 2.2 Session 创建(getOrCreateSession)

```
收到 UDP 数据报
    v
提取 remoteAddr 作为 key
    v
sessions.Load(key) -> 命中?-> 返回已有 session,touch()
    v 未命中
NextID() -> newSession()
    v
sessions.LoadOrStore(key, sess) -> 竞态保护
    v 成功存储
Register(sess) -> SessionManager
    v
Plugins.OnAccept(sess) -> 插件链
    v
返回 session
```

**竞态安全:** `LoadOrStore` 保证同一远端地址只创建一个 session.若两个 goroutine 同时为同一地址创建 session,后到者关闭多余的 session.

---

## 3. Server 实现

### 3.1 结构

```go
type Server struct {
    opts     Options
    rt       core.Runtime
    conn     *net.UDPConn      // 单个 UDP 监听连接
    closed   atomic.Bool
    cancel   context.CancelFunc
    wg       sync.WaitGroup    // 跟踪 readLoop 和 sweepLoop
    sessions sync.Map          // key: remoteAddr string, value: *session
}
```

### 3.2 Start

```go
func (s *Server) Start(ctx context.Context) error {
    s.closed.Store(false)
    addr, err := net.ResolveUDPAddr("udp", s.opts.Addr)
    if err != nil { return fmt.Errorf("udp resolve %s: %w", s.opts.Addr, err) }

    runCtx, cancel := context.WithCancel(ctx)
    s.cancel = cancel

    if s.opts.TLSConfig != nil {
        // DTLS 模式:只起 accept 循环.每个对端一条 DTLS 连接,
        // 由 handleDTLSConn 自行持有读循环,因此不需要共享读缓冲的 readLoop,
        // 也不做伪会话 TTL 清扫(生命周期由连接本身界定).
        ln, err := dtls.Listen("udp", addr, shared.DTLSConfig(s.opts.TLSConfig))
        if err != nil { ... }
        s.dtlsLn = ln
        s.wg.Add(1)
        go s.dtlsAcceptLoop(runCtx)
    } else {
        // 纯 UDP 模式:共享读缓冲的 readLoop + 伪会话清扫循环.
        conn, err := net.ListenUDP("udp", addr)
        if err != nil { ... }
        s.conn = conn
        s.wg.Add(2)
        go s.readLoop(runCtx)
        go s.sweepLoop(runCtx)
    }
    return nil
}
```

两种模式互斥,各自启动的 goroutine 也不同:

| 模式 | 启动的 goroutine | 会话粒度 | 空闲回收 |
|------|------------------|----------|----------|
| 纯 UDP | `readLoop` + `sweepLoop` | 每远端地址一个伪会话 | `sweepLoop` 按 `SessionTTL` 清扫 |
| DTLS | `dtlsAcceptLoop`(+ 每对端一个读循环) | 每 DTLS 连接一个真实会话 | 连接读超时 |

DTLS 模式刻意不启动 `sweepLoop`:DTLS 会话与连接一一对应,没有"伪会话"需要按 TTL 回收,
再加一层定时清扫只会与连接生命周期竞争.

### 3.3 readLoop

```go
func (s *Server) readLoop(ctx context.Context) {
    defer s.wg.Done()
    buf := make([]byte, s.opts.MaxDatagram) // 默认 64KB,全体对端共用一份
    // 未被关闭却返回错误的 socket(被外部关闭,OS 层瞬时故障)不能在紧循环里
    // 重试;与 accept 循环使用同一种退避.
    backoff := shared.NewBackoff(shared.DefaultBackoffMin, shared.DefaultBackoffMax)
    for {
        n, addr, err := s.conn.ReadFromUDP(buf)
        if err != nil {
            // 已关闭的 socket(含被外部关闭)是终态:重试永远不会成功.
            if s.closed.Load() || ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
                return
            }
            if !backoff.Wait(ctx) {
                return
            }
            continue
        }
        backoff.Reset()
        payload := append([]byte(nil), buf[:n]...) // 复制数据报
        sess := s.getOrCreateSession(addr)
        if sess == nil {
            continue
        }
        sess.touch()
        payload, err = s.rt.Plugins().OnMessage(sess, payload)
        if err != nil {
            if !errors.Is(err, core.ErrPluginDrop) {
                // 纯 UDP 没有 per-peer 连接可以关闭来唤醒读循环,
                // 因此必须在这里清理,否则对端会一直被挂在一个已关闭的会话上.
                s.closeSession(context.Background(), sess.remote.String(), sess)
            }
            continue
        }
        if s.opts.Handler != nil {
            msg := core.Message{SessionID: sess.ID(), Protocol: core.ProtocolUDP, Payload: payload}
            _ = shared.CallHandler(func() error { return s.opts.Handler(sess, msg) }, s.rt.Logger())
        }
    }
}
```

**设计要点:**

- 数据报复制(`append([]byte(nil), buf[:n]...)`):避免下次读取覆盖仍在被处理的数据.
- `net.ErrClosed` 是**终态**:此前的实现只检查 `s.closed` / `ctx`,于是被外部关闭的 socket
  会让循环永远重试而 Drain 永不结束.现在把它与 `s.closed` 同等对待.
- 失败退避 `shared.Backoff`(5ms 起,1s 封顶,指数增长):修复了失败路径 100% CPU 空转.
  反向确认时,未退避版本在 250ms 内跑了约 1030 万次 accept 尝试.
- `ErrPluginDrop` 不关闭会话:限流丢弃是正常控制流.
- 纯 UDP 不经过 WorkerPool,handler 经 `shared.CallHandler` 内联调用(见 `GATEWAY.md sec.5`).

### 3.4 sweepLoop

```go
func (s *Server) sweepLoop(ctx context.Context) {
    defer s.wg.Done()
    ticker := time.NewTicker(s.opts.SweepInterval) // 默认 30s
    defer ticker.Stop()
    for {
        select {
        case <-ticker.C:
            now := time.Now()
            s.sessions.Range(func(key, value any) bool {
                sess := value.(*session)
                if now.Sub(sess.LastActiveAt()) > s.opts.SessionTTL {
                    s.closeSession(context.Background(), key.(string), sess)
                }
                return true
            })
        case <-ctx.Done():
            return
        }
    }
}
```

**TTL 机制:**

- 每个 `SweepInterval`(默认 30s)扫描所有伪会话.
- `LastActiveAt() + SessionTTL < now` 的会话被关闭并注销.
- 默认 `SessionTTL` 为 2 分钟,适用于 IoT 设备心跳间隔.

### 3.5 DTLS 连接的生命周期

DTLS 模式下每个对端占有一条独立连接与一块独立读缓冲,因此释放时机必须显式:

```go
func (s *Server) handleDTLSConn(ctx context.Context, conn net.Conn) {
    defer s.wg.Done()
    id := s.rt.Sessions().NextID()
    sess := newDTLSSession(id, conn)
    s.sessions.Store(id, sess)
    s.dtlsConns.Store(id, conn)
    accepted := false
    defer func() {
        // 释放 DTLS socket.读循环在任意读错误(对端断开,读超时,传输故障)
        // 时返回,而 dtlsConns.Delete 会让它从 StopAccept 的视野中消失 --
        // 若不在这里关闭,每个离场的 DTLS 对端都会泄漏一个 socket,
        // 直到 finalizer 碰巧运行.Close 是幂等的(closeOnce),
        // 与并发的 CloseSessions / closeSession 不冲突.
        _ = sess.Close(context.Background())
        s.dtlsConns.Delete(id)
        if _, loaded := s.sessions.LoadAndDelete(id); loaded {
            s.rt.Sessions().Unregister(id)
            if accepted {
                s.rt.Plugins().OnClose(sess)
            }
        }
    }()
    // ... Register -> OnAccept -> 读循环
}
```

`LoadAndDelete` 保证 `OnClose` 恰好触发一次:即使 `CloseSessions` 已经通过 `closeSession`
删除过该会话,这里也不会重复通知插件(V9 修复项:此前会泄漏 socket).

### 3.6 StagedServer 实现

| 阶段 | 行为 |
|------|------|
| StopAccept | CAS 标记关闭 -> cancel 上下文 -> 关闭全部 DTLS conn -> 关闭 UDP conn 或 DTLS listener |
| Drain | 起一个 goroutine `wg.Wait()`,与 `ctx` 二选一返回 |
| CloseSessions | Range 遍历所有 session,调用 closeSession |

`closeSession` 执行完整清理:

```go
func (s *Server) closeSession(ctx context.Context, key string, sess *session) {
    s.sessions.Delete(key)
    s.rt.Sessions().Unregister(sess.ID())
    _ = sess.Close(ctx)
    s.rt.Plugins().OnClose(sess)
}
```

**UDP 的 Drain 为什么能结束:** 三件事共同保证 -- `StopAccept` 关闭 socket 使 `ReadFromUDP`
与 `Accept` 返回 `net.ErrClosed`;两个循环都把 `net.ErrClosed` 当终态立即返回;退避逻辑
监听 `ctx`,即使错误不是 `net.ErrClosed` 也能在上下文取消后退出.

---

## 4. Options 配置

```go
// internal/transport/udp/options.go
type Options struct {
    Addr          string        // 监听地址,默认 "127.0.0.1:18200"
    Handler       core.Handler  // 消息处理函数
    SessionTTL    time.Duration // 伪会话 TTL,默认 2 分钟
    SweepInterval time.Duration // 清扫间隔,默认 30 秒
    MaxDatagram   int           // 纯 UDP 共享读缓冲大小,默认 64KB

    // DTLS 模式下每条连接独占一块读缓冲,因此默认值刻意小于 MaxDatagram:
    // 若沿用 64 KiB,1 万个空闲 DTLS 对端仅读缓冲就占约 640MB.
    // 16 KiB 覆盖典型 IoT/UDP 应用报文;更大的 CoAP 载荷应使用分块传输(RFC 7959).
    DTLSReadBufferBytes int

    TLSConfig *tls.Config // 非 nil 即启用 DTLS
}
```

**Functional Options:**

| Option | 默认值 | 说明 |
|--------|--------|------|
| `WithAddr(addr)` | `127.0.0.1:18200` | UDP 监听地址 |
| `WithHandler(handler)` | nil | 消息处理回调 |
| `WithSessionTTL(ttl)` | `2m` | 伪会话空闲超时,`<= 0` 忽略 |
| `WithSweepInterval(interval)` | `30s` | 清扫周期,`<= 0` 忽略 |
| `WithDTLS(cfg)` | nil | 传入 `*tls.Config` 启用 DTLS |
| `WithDTLSReadBufferBytes(size)` | `16KiB` | 每 DTLS 连接读缓冲,`<= 0` 忽略 |

`MaxDatagram` 没有对应的 setter:它是纯 UDP 读循环的单一共享缓冲,改动它需要同时评估
"单包上限"与"单包内存占用",目前只在内部配置中调整.

**配置建议:**

| 场景 | SessionTTL | SweepInterval | MaxDatagram | DTLSReadBufferBytes |
|------|-----------|---------------|-------------|---------------------|
| IoT 设备(心跳 60s) | 3m | 30s | 64KB | 16KB |
| 实时游戏(高频 UDP) | 30s | 10s | 1KB | 4KB |
| DNS 服务(无状态) | 10s | 5s | 512B | 4KB |

---

## 5. 数据流

```
客户端发送 UDP 数据报
    v
Server.readLoop() 接收
    v
getOrCreateSession(remoteAddr)
    +-- 已有 -> touch() + 返回
    +-- 新建 -> Register -> OnAccept -> 返回
    v
sess.touch() 更新活跃时间
    v
Plugins.OnMessage(sess, payload)
    +-- ErrPluginDrop -> 丢弃(不关闭 session)
    +-- ErrPluginBlock -> sess.Close()
    +-- 正常 -> payload 继续
    v
Handler(sess, msg)
    +-- err != nil -> sess.Close()
    +-- 正常 -> Handler 内调用 sess.Send() 响应
    v
[后台] sweepLoop 定期检查
    +-- LastActiveAt + TTL < now -> closeSession()
```

---

## 6. 与 CoAP 的关系

CoAP(Constrained Application Protocol)是基于 UDP 的应用层协议.在 Shark-Socket 中:

| 层级 | 实现 | 位置 |
|------|------|------|
| 传输层 | CoAP Server(UDP 基础 + 帧解析 + CON/ACK) | `internal/transport/coap/` |
| 应用层 | LwM2M Server/Client(CoAP Responder 适配) | `internal/protocol/lwm2m/` |

**UDP Server 是 CoAP 的基础**,但 CoAP Server 有独立的实现(包含 CoAP 帧解析,CON/ACK 确认,MessageID 去重等),不复用 UDP Server.两者的 session 模型相同(伪会话 + TTL).

详见 CoAP 部分(Part 3)和 PROTOCOL.md.

---

# TRANSPORT.md - Part 3/5: CoAP

> Shark-Socket 传输层:CoAP 协议实现细节  
> 版本:v0.2.x-alpha  
> 最后更新:2026-06-01

---

## 目录(CoAP 部分)

1. [CoAP 协议概述](#1-coap-协议概述)
2. [CoAP 帧结构](#2-coap-帧结构)
3. [CoAPSession](#3-coapsession)
4. [CoAPServer](#4-coapserver)
5. [配置项完整参考](#5-配置项完整参考)

---

## 1. CoAP 协议概述

### 1.1 在框架中的定位

CoAP(Constrained Application Protocol,RFC 7252)是**面向受限设备的应用层协议**,基于 UDP 传输.

**CoAP 在框架分层中的位置:**

```
+-------------------------------------+
| protocol/lwm2m/                     |  应用协议层(LwM2M 语义)
|   基于 CoAP transport 提供设备管理   |
+-------------------------------------+
| transport/coap/                     |  传输层(本文档)
|   CoAP 帧解析,CON/ACK,去重        |
+-------------------------------------+
| transport/udp/                      |  网络层(UDP 数据报)
|   单 conn + 伪会话模型               |
+-------------------------------------+
```

**CoAP 与 UDP 的关系(ADR-009 决策):**

- CoAP Server **独立于** UDP Server,不复用 UDP Server 的伪会话模型
- CoAP Server 内部创建自己的 `*net.UDPConn`,自行管理 CoAP 级别的伪会话
- CoAP 伪会话携带额外状态(`pendingACKs`,`messageCache`),不能直接复用 UDP 伪会话
- LwM2M 基于 CoAP transport,通过 `responder.go` 接入(详见 `PROTOCOL.md`)

### 1.2 CoAP 消息类型

| 类型 | 值 | 说明 |
|------|---|------|
| CON(Confirmable) | 0 | 可靠消息,对端必须回 ACK |
| NON(Non-confirmable) | 1 | 不可靠消息,无需 ACK |
| ACK(Acknowledgement) | 2 | 对 CON 的确认 |
| RST(Reset) | 3 | 拒绝或无法处理,取消重传 |

### 1.3 P0 实现范围

| 功能 | P0(当前) | P1(后续) |
|------|-----------|-----------|
| CON 基础 ACK | [OK] | - |
| NON 消息处理 | [OK] | - |
| MessageID 去重 | [OK] | - |
| retransmitLoop | [OK] | - |
| Block-wise 传输 | [X] | [OK] |
| Observe/Notify | [X] | [OK] |
| DTLS | [X] | [OK] |
| 完整 Option 编解码 | 部分(Uri-Path/Content-Format) | 完整 |

### 1.4 文件清单

```
internal/transport/coap/
+-- message.go   # CoAP 帧结构,解析,序列化
+-- session.go   # CoAPSession:伪会话 + pendingACK + messageCache
+-- server.go    # CoAPServer:readLoop + ACK 响应 + retransmitLoop
+-- options.go   # CoAPOption Functional Options
```

### 1.5 编译期验证

```go
// internal/transport/coap/session.go
var _ core.Session = (*Session)(nil)

// internal/transport/coap/server.go
var _ core.Server              = (*Server)(nil)
var _ core.RuntimeConfigurable = (*Server)(nil)
```

---

## 2. CoAP 帧结构

### 2.1 Message 结构定义

```go
// internal/transport/coap/message.go

// MsgType 是 CoAP 消息类型.
type MsgType uint8

const (
    MsgTypeCON MsgType = 0 // Confirmable
    MsgTypeNON MsgType = 1 // Non-confirmable
    MsgTypeACK MsgType = 2 // Acknowledgement
    MsgTypeRST MsgType = 3 // Reset
)

// Code 是 CoAP 方法码或响应码.
// 格式:c.dd(class.detail),编码为 uint8:(c << 5) | dd
type Code uint8

const (
    // 方法码(请求)
    CodeGET    Code = 0x01 // 0.01
    CodePOST   Code = 0x02 // 0.02
    CodePUT    Code = 0x03 // 0.03
    CodeDELETE Code = 0x04 // 0.04

    // 成功响应码
    CodeCreated  Code = 0x41 // 2.01
    CodeDeleted  Code = 0x42 // 2.02
    CodeValid    Code = 0x43 // 2.03
    CodeChanged  Code = 0x44 // 2.04
    CodeContent  Code = 0x45 // 2.05

    // 客户端错误
    CodeBadRequest         Code = 0x80 // 4.00
    CodeUnauthorized       Code = 0x81 // 4.01
    CodeBadOption          Code = 0x82 // 4.02
    CodeForbidden          Code = 0x83 // 4.03
    CodeNotFound           Code = 0x84 // 4.04
    CodeMethodNotAllowed   Code = 0x85 // 4.05
    CodeNotAcceptable      Code = 0x86 // 4.06

    // 服务端错误
    CodeInternalServerError Code = 0xA0 // 5.00
    CodeNotImplemented      Code = 0xA1 // 5.01
    CodeBadGateway          Code = 0xA2 // 5.02
    CodeServiceUnavailable  Code = 0xA3 // 5.03
    CodeGatewayTimeout      Code = 0xA4 // 5.04
)

// OptionNumber 是 CoAP Option 编号.
type OptionNumber uint16

const (
    OptionIfMatch       OptionNumber = 1
    OptionUriHost       OptionNumber = 3
    OptionETag          OptionNumber = 4
    OptionIfNoneMatch   OptionNumber = 5
    OptionUriPort       OptionNumber = 7
    OptionLocationPath  OptionNumber = 8
    OptionUriPath       OptionNumber = 11
    OptionContentFormat OptionNumber = 12
    OptionMaxAge        OptionNumber = 14
    OptionUriQuery      OptionNumber = 15
    OptionAccept        OptionNumber = 17
    OptionLocationQuery OptionNumber = 20
    OptionBlock2        OptionNumber = 23
    OptionBlock1        OptionNumber = 27
    OptionSize2         OptionNumber = 28
    OptionSize1         OptionNumber = 60
)

// Option 是 CoAP 选项.
type Option struct {
    Number OptionNumber
    Value  []byte
}

// Message 是 CoAP 消息结构.
type Message struct {
    Version   uint8   // 必须为 1
    Type      MsgType
    Code      Code
    MessageID uint16
    Token     []byte  // 长度 0-8 字节
    Options   []Option
    Payload   []byte
}
```

### 2.2 帧解析

```go
// Parse 从字节切片解析 CoAP 消息.
func Parse(data []byte) (*Message, error) {
    if len(data) < 4 {
        return nil, fmt.Errorf("%w: message too short (%d bytes)",
            coap.ErrInvalidMessage, len(data))
    }

    version := (data[0] >> 6) & 0x03
    if version != 1 {
        return nil, fmt.Errorf("%w: unsupported version %d",
            coap.ErrInvalidMessage, version)
    }

    msgType := MsgType((data[0] >> 4) & 0x03)
    tokenLen := int(data[0] & 0x0F)

    if tokenLen > 8 {
        return nil, fmt.Errorf("%w: token length %d exceeds maximum 8",
            coap.ErrInvalidMessage, tokenLen)
    }

    code := Code(data[1])
    messageID := binary.BigEndian.Uint16(data[2:4])

    offset := 4
    if len(data) < offset+tokenLen {
        return nil, fmt.Errorf("%w: token length %d exceeds data length",
            coap.ErrInvalidMessage, tokenLen)
    }

    token := make([]byte, tokenLen)
    copy(token, data[offset:offset+tokenLen])
    offset += tokenLen

    // 解析 Options
    options, payloadOffset, err := parseOptions(data, offset)
    if err != nil {
        return nil, err
    }

    var payload []byte
    if payloadOffset < len(data) {
        // 跳过 payload marker (0xFF)
        payload = make([]byte, len(data)-payloadOffset)
        copy(payload, data[payloadOffset:])
    }

    return &Message{
        Version:   1,
        Type:      msgType,
        Code:      code,
        MessageID: messageID,
        Token:     token,
        Options:   options,
        Payload:   payload,
    }, nil
}

// parseOptions 解析 CoAP Options(delta 编码).
func parseOptions(data []byte, offset int) ([]Option, int, error) {
    var options []Option
    currentOptionNumber := OptionNumber(0)

    for offset < len(data) {
        if data[offset] == 0xFF {
            // Payload Marker
            offset++ // 跳过 0xFF,后续是 payload
            return options, offset, nil
        }

        deltaNibble := (data[offset] >> 4) & 0x0F
        lenNibble   := data[offset] & 0x0F
        offset++

        // 解析 delta
        delta, newOffset, err := decodeOptionValue(data, offset, deltaNibble)
        if err != nil {
            return nil, 0, err
        }
        offset = newOffset

        // 解析 length
        optionLen, newOffset, err := decodeOptionValue(data, offset, lenNibble)
        if err != nil {
            return nil, 0, err
        }
        offset = newOffset

        currentOptionNumber += OptionNumber(delta)

        if offset+optionLen > len(data) {
            return nil, 0, fmt.Errorf("%w: option value out of bounds",
                coap.ErrInvalidMessage)
        }

        value := make([]byte, optionLen)
        copy(value, data[offset:offset+optionLen])
        offset += optionLen

        options = append(options, Option{
            Number: currentOptionNumber,
            Value:  value,
        })
    }

    return options, offset, nil
}

func decodeOptionValue(data []byte, offset int, nibble byte) (int, int, error) {
    switch nibble {
    case 13:
        if offset >= len(data) {
            return 0, 0, fmt.Errorf("%w: option delta/length out of bounds",
                coap.ErrInvalidMessage)
        }
        return int(data[offset]) + 13, offset + 1, nil
    case 14:
        if offset+1 >= len(data) {
            return 0, 0, fmt.Errorf("%w: option delta/length out of bounds",
                coap.ErrInvalidMessage)
        }
        val := int(binary.BigEndian.Uint16(data[offset:offset+2])) + 269
        return val, offset + 2, nil
    case 15:
        return 0, 0, fmt.Errorf("%w: reserved option delta/length value 15",
            coap.ErrInvalidMessage)
    default:
        return int(nibble), offset, nil
    }
}
```

### 2.3 帧序列化

```go
// Marshal 将 CoAP 消息序列化为字节切片.
func (m *Message) Marshal() ([]byte, error) {
    if len(m.Token) > 8 {
        return nil, fmt.Errorf("%w: token length %d exceeds 8",
            coap.ErrInvalidMessage, len(m.Token))
    }

    var buf bytes.Buffer

    // 固定头 4 字节
    firstByte := byte(1<<6) | // Version = 1
        byte(m.Type)<<4 |
        byte(len(m.Token))
    buf.WriteByte(firstByte)
    buf.WriteByte(byte(m.Code))

    var idBytes [2]byte
    binary.BigEndian.PutUint16(idBytes[:], m.MessageID)
    buf.Write(idBytes[:])

    // Token
    buf.Write(m.Token)

    // Options(delta 编码)
    if err := marshalOptions(&buf, m.Options); err != nil {
        return nil, err
    }

    // Payload
    if len(m.Payload) > 0 {
        buf.WriteByte(0xFF) // Payload Marker
        buf.Write(m.Payload)
    }

    return buf.Bytes(), nil
}

func marshalOptions(buf *bytes.Buffer, options []Option) error {
    // 按 Option Number 升序排序(CoAP 规范要求)
    slices.SortFunc(options, func(a, b Option) int {
        return int(a.Number) - int(b.Number)
    })

    prevNumber := OptionNumber(0)
    for _, opt := range options {
        delta := int(opt.Number - prevNumber)
        prevNumber = opt.Number

        writeOptionHeader(buf, delta, len(opt.Value))
        buf.Write(opt.Value)
    }
    return nil
}

func writeOptionHeader(buf *bytes.Buffer, delta, length int) {
    encodeDeltaOrLen := func(val int) (nibble byte, ext []byte) {
        switch {
        case val < 13:
            return byte(val), nil
        case val < 269:
            return 13, []byte{byte(val - 13)}
        default:
            b := make([]byte, 2)
            binary.BigEndian.PutUint16(b, uint16(val-269))
            return 14, b
        }
    }

    deltaNibble, deltaExt := encodeDeltaOrLen(delta)
    lenNibble, lenExt := encodeDeltaOrLen(length)

    buf.WriteByte(deltaNibble<<4 | lenNibble)
    buf.Write(deltaExt)
    buf.Write(lenExt)
}
```

### 2.4 Option 辅助函数

```go
// GetUriPath 提取所有 Uri-Path Option 值,拼接为路径字符串.
func (m *Message) GetUriPath() string {
    var parts []string
    for _, opt := range m.Options {
        if opt.Number == OptionUriPath {
            parts = append(parts, string(opt.Value))
        }
    }
    return "/" + strings.Join(parts, "/")
}

// GetUriQuery 提取所有 Uri-Query Option 值.
func (m *Message) GetUriQuery() map[string]string {
    result := make(map[string]string)
    for _, opt := range m.Options {
        if opt.Number == OptionUriQuery {
            parts := strings.SplitN(string(opt.Value), "=", 2)
            if len(parts) == 2 {
                result[parts[0]] = parts[1]
            }
        }
    }
    return result
}

// GetContentFormat 提取 Content-Format Option 值.
func (m *Message) GetContentFormat() (uint16, bool) {
    for _, opt := range m.Options {
        if opt.Number == OptionContentFormat && len(opt.Value) > 0 {
            if len(opt.Value) == 1 {
                return uint16(opt.Value[0]), true
            }
            if len(opt.Value) == 2 {
                return binary.BigEndian.Uint16(opt.Value), true
            }
        }
    }
    return 0, false
}

// NewACK 构造对 CON 消息的 ACK 响应.
func NewACK(request *Message, responseCode Code, payload []byte) *Message {
    return &Message{
        Version:   1,
        Type:      MsgTypeACK,
        Code:      responseCode,
        MessageID: request.MessageID, // ACK 必须使用相同的 MessageID
        Token:     request.Token,     // Token 保持一致
        Payload:   payload,
    }
}

// NewNON 构造 NON 消息.
func NewNON(code Code, messageID uint16, token, payload []byte) *Message {
    return &Message{
        Version:   1,
        Type:      MsgTypeNON,
        Code:      code,
        MessageID: messageID,
        Token:     token,
        Payload:   payload,
    }
}

// SelectACKCode 根据请求方法码选择合适的 ACK 响应码.
func SelectACKCode(requestCode Code) Code {
    switch requestCode {
    case CodeGET:
        return CodeContent  // 2.05
    case CodePOST:
        return CodeCreated  // 2.01
    case CodePUT:
        return CodeChanged  // 2.04
    case CodeDELETE:
        return CodeDeleted  // 2.02
    default:
        return CodeContent  // 2.05
    }
}
```

---

## 3. CoAPSession

### 3.1 pendingACK 结构

```go
// internal/transport/coap/session.go

// pendingACK 跟踪一个等待确认的 CON 消息.
type pendingACK struct {
    messageID  uint16
    ackData    []byte        // 已发送的 ACK 字节,用于重传
    sentAt     time.Time     // 首次发送时间
    lastSentAt time.Time     // 最后发送时间(重传更新)
    attempts   int           // 已重传次数
}
```

### 3.2 messageCache 结构

```go
// messageCache 是 MessageID 去重缓存(环形淘汰).
type messageCache struct {
    mu       sync.Mutex
    cache    map[uint16][]byte // messageID -> 已发送的 ACK 字节
    order    []uint16          // 插入顺序(用于淘汰最旧条目)
    maxSize  int               // 最大缓存条目数
}

func newMessageCache(maxSize int) *messageCache {
    return &messageCache{
        cache:   make(map[uint16][]byte, maxSize),
        order:   make([]uint16, 0, maxSize),
        maxSize: maxSize,
    }
}

// CheckAndRecord 检查 MessageID 是否重复.
// 返回值:(cachedACK []byte, isDuplicate bool)
// 若重复,返回缓存的 ACK 字节;若首次,记录占位(nil),等待 CacheResponse 填充.
func (c *messageCache) CheckAndRecord(messageID uint16) ([]byte, bool) {
    c.mu.Lock()
    defer c.mu.Unlock()

    if ack, exists := c.cache[messageID]; exists {
        return ack, true // 重复消息
    }

    // 环形淘汰:超过 maxSize 时删除最旧条目
    if len(c.cache) >= c.maxSize {
        oldest := c.order[0]
        c.order = c.order[1:]
        delete(c.cache, oldest)
    }

    // 记录占位(nil 表示 Handler 尚未处理完)
    c.cache[messageID] = nil
    c.order = append(c.order, messageID)
    return nil, false
}

// CacheResponse 填充 MessageID 对应的 ACK 响应(Handler 处理完成后调用).
func (c *messageCache) CacheResponse(messageID uint16, ackData []byte) {
    c.mu.Lock()
    defer c.mu.Unlock()
    if _, exists := c.cache[messageID]; exists {
        c.cache[messageID] = ackData
    }
}
```

### 3.3 CoAPSession 结构定义

```go
type Session struct {
    // 身份(不可变)
    id         uint64
    remoteAddr *net.UDPAddr
    localAddr  net.Addr
    createdAt  time.Time

    // 网络(共享单 conn)
    conn *net.UDPConn

    // CoAP 状态
    messageCache  *messageCache
    pendingACKs   map[uint16]*pendingACK // 等待 ACK 的 CON 消息(由 retransmitLoop 管理)
    pendingMu     sync.Mutex             // 保护 pendingACKs

    // 活跃 MessageID 索引(避免 retransmitLoop 自锁死锁)
    activeIDs   []uint16    // retransmitLoop 读取的索引
    activeIDsMu sync.Mutex  // 保护 activeIDs

    // 状态
    state      atomic.Int32
    lastActive atomic.Int64
    ctx        context.Context
    cancel     context.CancelFunc

    // 并发控制
    mu        sync.Mutex // 保护 WriteToUDP
    closeOnce sync.Once

    // 元数据
    meta sync.Map

    // 可观测
    logger  core.Logger
    metrics core.Metrics
}

// 编译期验证
var _ core.Session = (*Session)(nil)
```

### 3.4 retransmitLoop 死锁问题与解决方案(ADR-003 修复)

**错误方案(导致死锁):**

```
retransmitLoop 执行:
  pendingMu.Lock()              <- 获取锁,遍历 pendingACKs
  for id, pending := range pendingACKs:
    if 超时:
      delete(pendingACKs, id)   <- 在持锁状态下删除,正常
      conn.WriteToUDP(...)      <- 发送重传
  pendingMu.Unlock()

问题:若重传触发 OnMessage,OnMessage 内部可能调用:
  session.TrackCON(msgID, ackData)  <- 需要 pendingMu.Lock() -> 死锁!
```

**正确方案(活跃索引):**

```go
// retransmitLoop 只读 activeIDs 索引,不持 pendingMu 遍历
func (s *Session) retransmitLoop(
    ackTimeout time.Duration,
    maxRetransmit int,
) {
    ticker := time.NewTicker(ackTimeout)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            s.retransmitOnce(ackTimeout, maxRetransmit)
        case <-s.ctx.Done():
            return
        }
    }
}

func (s *Session) retransmitOnce(ackTimeout time.Duration, maxRetransmit int) {
    // 步骤1:读取当前活跃 ID 列表(持 activeIDsMu 短暂复制)
    s.activeIDsMu.Lock()
    ids := make([]uint16, len(s.activeIDs))
    copy(ids, s.activeIDs)
    s.activeIDsMu.Unlock()

    now := time.Now()
    var expiredIDs []uint16

    for _, id := range ids {
        // 步骤2:获取 pending 状态(持 pendingMu 短暂读取)
        s.pendingMu.Lock()
        pending, exists := s.pendingACKs[id]
        if !exists {
            s.pendingMu.Unlock()
            expiredIDs = append(expiredIDs, id)
            continue
        }

        // 检查是否需要重传
        elapsed := now.Sub(pending.lastSentAt)
        retransmitDelay := ackTimeout * (1 << uint(pending.attempts))
        shouldRetransmit := elapsed >= retransmitDelay
        maxExceeded := pending.attempts >= maxRetransmit

        ackData := pending.ackData
        attempts := pending.attempts
        s.pendingMu.Unlock()
        // 释放锁后再执行 I/O 操作

        if maxExceeded {
            // 超过最大重传次数,放弃
            expiredIDs = append(expiredIDs, id)
            s.logger.Warn("coap retransmit max exceeded",
                "session_id", s.id,
                "message_id", id,
                "attempts", attempts)
            continue
        }

        if shouldRetransmit {
            // 步骤3:发送重传(不持锁)
            s.mu.Lock()
            _, err := s.conn.WriteToUDP(ackData, s.remoteAddr)
            s.mu.Unlock()

            if err != nil {
                s.logger.Error("coap retransmit error",
                    "session_id", s.id,
                    "message_id", id,
                    "error", err)
            }

            // 步骤4:更新重传状态(持 pendingMu 短暂更新)
            s.pendingMu.Lock()
            if p, ok := s.pendingACKs[id]; ok {
                p.lastSentAt = now
                p.attempts++
            }
            s.pendingMu.Unlock()

            s.metrics.IncCounter("transport_errors_total", "protocol", "coap", "type", "retransmit")
        }
    }

    // 步骤5:清理过期 ID
    if len(expiredIDs) > 0 {
        s.pendingMu.Lock()
        for _, id := range expiredIDs {
            delete(s.pendingACKs, id)
        }
        s.pendingMu.Unlock()

        s.activeIDsMu.Lock()
        expiredSet := make(map[uint16]bool, len(expiredIDs))
        for _, id := range expiredIDs {
            expiredSet[id] = true
        }
        s.activeIDs = slices.DeleteFunc(s.activeIDs, func(id uint16) bool {
            return expiredSet[id]
        })
        s.activeIDsMu.Unlock()
    }
}
```

### 3.5 CON 跟踪方法

```go
// TrackCON 记录已发送的 ACK,供 retransmitLoop 重传使用.
func (s *Session) TrackCON(messageID uint16, ackData []byte) {
    s.pendingMu.Lock()
    s.pendingACKs[messageID] = &pendingACK{
        messageID:  messageID,
        ackData:    ackData,
        sentAt:     time.Now(),
        lastSentAt: time.Now(),
        attempts:   0,
    }
    s.pendingMu.Unlock()

    // 更新活跃 ID 索引
    s.activeIDsMu.Lock()
    s.activeIDs = append(s.activeIDs, messageID)
    s.activeIDsMu.Unlock()
}

// ResetCON 收到 RST 时取消对应 MessageID 的重传.
func (s *Session) ResetCON(messageID uint16) {
    s.pendingMu.Lock()
    delete(s.pendingACKs, messageID)
    s.pendingMu.Unlock()

    s.activeIDsMu.Lock()
    s.activeIDs = slices.DeleteFunc(s.activeIDs, func(id uint16) bool {
        return id == messageID
    })
    s.activeIDsMu.Unlock()
}
```

### 3.6 Send 实现

```go
func (s *Session) Send(data []byte) error {
    if !s.IsAlive() {
        return core.ErrSessionClosed
    }

    s.mu.Lock()
    defer s.mu.Unlock()

    _, err := s.conn.WriteToUDP(data, s.remoteAddr)
    if err != nil {
        s.metrics.IncCounter("transport_errors_total", "protocol", "coap", "type", "write")
        return err
    }

    s.metrics.IncCounter("messages_total", "protocol", "coap", "direction", "out")
    return nil
}
```

### 3.7 Close 实现

```go
func (s *Session) Close(ctx context.Context) error {
    s.closeOnce.Do(func() {
        s.state.Store(int32(core.Closed))
        s.cancel()

        // 清理所有 pending CON
        s.pendingMu.Lock()
        s.pendingACKs = make(map[uint16]*pendingACK)
        s.pendingMu.Unlock()

        s.activeIDsMu.Lock()
        s.activeIDs = s.activeIDs[:0]
        s.activeIDsMu.Unlock()

        // sessions_active 由 runtime 的 metricSessionManager 统一维护,传输层不单独读写
        s.logger.Info("coap session closed",
            "session_id", s.id,
            "remote_addr", s.remoteAddr.String())
    })
    return nil
}
```

---

## 4. CoAPServer

### 4.1 结构定义

```go
// internal/transport/coap/server.go
type Server struct {
    opts      Options
    rt        core.Runtime // 经 UseRuntime 注入
    udpConn   *net.UDPConn // 纯 UDP 模式
    dtlsLn    net.Listener // DTLS 模式
    dtlsConns sync.Map     // 活跃 DTLS 连接,关停时逐个关闭

    lastMsgID atomic.Uint32 // 出站 Message ID 分配器
    closed    atomic.Bool
    started   atomic.Bool
    cancel    context.CancelFunc
    wg        sync.WaitGroup

    sessions  sync.Map // remoteAddr -> *session(纯 UDP 伪会话)
    seen      sync.Map // CON 去重表(MessageID + 远端),见 4.4
    observers *ObserverRegistry

    retransmitMu    sync.Mutex
    pendingNotifies map[notifyKey]pendingNotify // 待确认的 Observe 通知
}

const (
    maxRetransmit      = 4               // 通知最大重传次数(RFC 7641)
    retransmitInterval = 2 * time.Second // 重传间隔
)

func NewServer(opts ...Option) *Server

// 编译期验证
var _ core.Server              = (*Server)(nil)
var _ core.RuntimeConfigurable = (*Server)(nil)
var _ core.StagedServer        = (*Server)(nil)
```

### 4.2 Start 实现

```go
func (s *Server) Start(ctx context.Context) error {
    if !s.started.CompareAndSwap(false, true) {
        return fmt.Errorf("coap server already started")
    }
    if s.rt == nil {
        s.rt = runtime.NewRuntime(nil, nil)
    }
    addr, err := net.ResolveUDPAddr("udp", s.opts.Addr)
    if err != nil {
        s.started.Store(false)
        return fmt.Errorf("coap resolve %s: %w", s.opts.Addr, err)
    }
    runCtx, cancel := context.WithCancel(ctx)
    s.cancel = cancel

    if s.opts.TLSConfig != nil {
        ln, err := dtls.Listen("udp", addr, shared.DTLSConfig(s.opts.TLSConfig))
        if err != nil { ... }
        s.dtlsLn = ln
        s.wg.Add(3)
        go s.dtlsAcceptLoop(runCtx)   // 接受 DTLS 连接
        go s.seenCleanupLoop(runCtx)  // 清理去重表
        go s.retransmitLoop(runCtx)   // 重传待确认通知
    } else {
        conn, err := net.ListenUDP("udp", addr)
        if err != nil { ... }
        s.udpConn = conn
        s.wg.Add(4)
        go s.readLoop(runCtx)         // 共享读缓冲
        go s.sweepLoop(runCtx)        // 伪会话 TTL 清扫
        go s.seenCleanupLoop(runCtx)
        go s.retransmitLoop(runCtx)
    }
    return nil
}
```

两种模式的 goroutine 组成不同,原因是会话粒度不同:

| 模式 | goroutine | 会话 |
|------|-----------|------|
| 纯 UDP | `readLoop` + `sweepLoop` + `seenCleanupLoop` + `retransmitLoop` | 每远端地址一个伪会话,按 TTL 回收 |
| DTLS | `dtlsAcceptLoop`(+ 每对端一个读循环) + `seenCleanupLoop` + `retransmitLoop` | 每 DTLS 连接一个真实会话 |

无论哪种模式,`seenCleanupLoop` 与 `retransmitLoop` 都必须运行:前者防止去重表无界增长,
后者负责 RFC 7641 的通知重传.重传循环与传输模式无关,因为它的作用对象是
`pendingNotifies`(待确认通知),而不是会话本身.

### 4.3 readLoop 实现(纯 UDP 模式)

```go
func (s *Server) readLoop(ctx context.Context) {
    defer s.wg.Done()
    buf := make([]byte, s.opts.MaxDatagram) // 默认 64KB,全体对端共用
    backoff := shared.NewBackoff(shared.DefaultBackoffMin, shared.DefaultBackoffMax)
    for {
        n, addr, err := s.udpConn.ReadFromUDP(buf)
        if err != nil {
            // 已关闭的 socket(含被外部关闭)是终态.
            if s.closed.Load() || ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
                return
            }
            if !backoff.Wait(ctx) {
                return
            }
            continue
        }
        backoff.Reset()
        sess := s.getOrCreateUDPSession(addr)
        if sess == nil {
            continue
        }
        sess.touch()
        s.handleCoAPMessage(sess, buf[:n])
    }
}
```

与早期实现的三点差异:

- **没有 `SetReadDeadline(1s)` 轮询**:旧写法用 1 秒读超时 + `continue` 模拟"可取消的读",
  代价是每个空闲 CoAP 服务端每秒醒来一次.V9 改为在 `StopAccept` 关闭 socket,
  读循环靠 `net.ErrClosed` 退出.
- **`net.ErrClosed` 是终态**:只检查 `s.closed` / `ctx` 时,被外部关闭的 socket 会让
  循环永远重试而 `Drain` 永不结束.
- **失败退避**:与 UDP DTLS accept 循环共用 `shared.Backoff`(5ms 起,1s 封顶).
  反向确认时未退避版本在 250ms 内跑了约 950 万次 accept 尝试.

`handleCoAPMessage` 的分派顺序是:先处理 ACK / RST(两者都是 Empty 消息,RFC 7252 sec.4.2),
再进入去重.ACK 与 RST 都不是 CON,顺序不影响去重结果,而把空 CON ping 排除在去重表之外可以
避免污染它.

### 4.4 handleDatagram 实现

```go
func (s *Server) handleDatagram(
    datagram []byte,
    remoteAddr *net.UDPAddr,
    manager core.SessionManager,
    plugins core.PluginRunner,
    logger core.Logger,
    metrics core.Metrics,
) {
    // 解析 CoAP 消息(帧验证)
    msg, err := Parse(datagram)
    if err != nil {
        metrics.IncCounter("transport_errors_total", "protocol", "coap", "type", "parse_error")
        logger.Debug("coap parse error",
            "remote_addr", remoteAddr.String(),
            "error", err)
        return
    }

    // 查找或创建 CoAP 伪会话
    addrKey := remoteAddr.String()
    sess, err := s.getOrCreateSession(addrKey, remoteAddr, manager, plugins, logger, metrics)
    if err != nil {
        return // ErrPluginBlock 或 Register 失败
    }

    sess.TouchActive()

    // 按消息类型分发处理
    switch msg.Type {
    case MsgTypeCON:
        s.handleCON(sess, msg, plugins, logger, metrics)
    case MsgTypeNON:
        s.handleNON(sess, msg, plugins, logger, metrics)
    case MsgTypeACK:
        // 收到 ACK,停止重传
        sess.ResetCON(msg.MessageID)
    case MsgTypeRST:
        // 收到 RST,取消重传
        sess.ResetCON(msg.MessageID)
        logger.Debug("coap RST received",
            "session_id", sess.ID(),
            "message_id", msg.MessageID)
    }
}
```

### 4.5 handleCON 实现(CON 可靠性,RFC 7252 sec.4.2)

```go
func (s *Server) handleCON(
    sess *Session,
    msg *Message,
    plugins core.PluginRunner,
    logger core.Logger,
    metrics core.Metrics,
) {
    // MessageID 去重检查
    cachedACK, isDuplicate := sess.messageCache.CheckAndRecord(msg.MessageID)

    if isDuplicate {
        if cachedACK != nil {
            // 重复 CON + 已有缓存响应 -> 重发缓存 ACK(不重复执行 Handler)
            sess.Send(cachedACK)
            metrics.IncCounter("transport_errors_total", "protocol", "coap", "type", "duplicate_con")
            logger.Debug("coap duplicate CON, resending cached ACK",
                "session_id", sess.ID(),
                "message_id", msg.MessageID)
        }
        // cachedACK == nil 说明 Handler 仍在处理中,忽略重复 CON
        return
    }

    // 首次 CON:插件链处理
    data, err := plugins.OnMessage(sess, msg.Payload)
    if err != nil {
        if errors.Is(err, core.ErrPluginDrop) {
            // 丢弃消息,但仍需发送 ACK(否则对端会一直重传)
            ackMsg := NewACK(msg, CodeContent, nil)
            ackData, _ := ackMsg.Marshal()
            sess.Send(ackData)
            sess.messageCache.CacheResponse(msg.MessageID, ackData)
            return
        }
        if errors.Is(err, core.ErrPluginBlock) {
            s.closeSession(sess)
            return
        }
    }

    // 调用 Handler
    coapMsg := core.Message{
        SessionID: sess.ID(),
        Protocol:  core.CoAP,
        Payload:   data,
        Meta: map[string]string{
            "coap_path":  msg.GetUriPath(),
            "coap_token": string(msg.Token),
        },
    }

    responseCode := SelectACKCode(msg.Code)
    var responsePayload []byte

    if err := s.handler(sess, coapMsg); err != nil {
        responseCode = CodeInternalServerError
        logger.Error("coap handler error",
            "session_id", sess.ID(),
            "error", err)
    }

    // 构造并发送 ACK
    ackMsg := NewACK(msg, responseCode, responsePayload)
    ackData, err := ackMsg.Marshal()
    if err != nil {
        logger.Error("coap marshal ACK error",
            "session_id", sess.ID(),
            "error", err)
        return
    }

    sess.Send(ackData)

    // 缓存 ACK 响应(供去重时重发)
    sess.messageCache.CacheResponse(msg.MessageID, ackData)

    // 跟踪 CON(若对端需要重传确认,此处暂不跟踪服务端发出的 ACK)
    // 注:RFC 7252 服务端发 ACK 后对端负责确认收到,服务端无需重传 ACK
}
```

### 4.6 handleNON 实现

```go
func (s *Server) handleNON(
    sess *Session,
    msg *Message,
    plugins core.PluginRunner,
    logger core.Logger,
    metrics core.Metrics,
) {
    // NON 消息无需 ACK,直接处理
    data, err := plugins.OnMessage(sess, msg.Payload)
    if err != nil {
        if errors.Is(err, core.ErrPluginDrop) {
            return
        }
        if errors.Is(err, core.ErrPluginBlock) {
            s.closeSession(sess)
            return
        }
    }

    coapMsg := core.Message{
        SessionID: sess.ID(),
        Protocol:  core.CoAP,
        Payload:   data,
        Meta: map[string]string{
            "coap_path":  msg.GetUriPath(),
            "coap_token": string(msg.Token),
        },
    }

    if err := s.handler(sess, coapMsg); err != nil {
        logger.Error("coap handler error",
            "session_id", sess.ID(),
            "error", err)
    }
}
```

### 4.7 getOrCreateUDPSession 实现

```go
func (s *Server) getOrCreateUDPSession(addr *net.UDPAddr) *session {
    key := addr.String()
    if value, ok := s.sessions.Load(key); ok {
        sess := value.(*session)
        // 插件或错误路径可能已经关闭过这个会话(例如 AutoBan 击杀,handler 失败),
        // 而对端仍在发报文.这种会话再也发不出数据,却会因为每次 touch()
        // 被刷新而一直留在表里,直到空闲清扫最终放弃 -- 期间一直占着一个死槽
        // 和一个过期的 session ID.这里主动回收(连同它注册过的 observer),
        // 让下一个报文创建全新会话并重新走 OnAccept,准入插件因此仍然生效.
        if sess.State() == core.StateClosed {
            s.closeSession(context.Background(), key, sess)
        } else {
            return sess
        }
    }
    // 先分配 ID 再创建并发布:避免并发清扫/关闭观察到临时的 id=0.
    sessionID := s.rt.Sessions().NextID()
    sess := newSession(sessionID, s.conn, addr)
    if err := s.rt.Plugins().OnAccept(sess); err != nil {
        if errors.Is(err, core.ErrPluginBlock) {
            s.rt.Metrics().IncCounter("rejected_connections_total",
                "protocol", "coap", "reason", "plugin_block")
        }
        return nil
    }
    if err := s.rt.Sessions().Register(sess); err != nil {
        return nil
    }
    // LoadOrStore 处理并发创建:输的一方注销自己刚注册的会话并复用赢家的.
    actual, loaded := s.sessions.LoadOrStore(key, sess)
    if loaded {
        s.rt.Sessions().Unregister(sessionID)
        return actual.(*session)
    }
    s.rt.Metrics().IncCounter("sessions_accepted_total", "protocol", "coap")
    return sess
}
```

三点值得单独说明:

- **先看 `State() == StateClosed` 再决定回收**:不检查状态直接复用会让已被插件封禁的对端
  继续挂在一个已关闭的会话上,读循环看起来还在服务它,实际所有出站都已失败.
- **`NextID` 在创建之前**:`newSession` 需要 ID,而会话一旦发布就不能再改 ID,
  否则并发清扫可能观察到 `id == 0`.
- **没有 per-session retransmit goroutine**:重传由全局 `retransmitLoop` 统一驱动,
  待确认通知存放在 `pendingNotifies`.因此 CoAP 的 goroutine 数量与活跃会话数无关.

### 4.8 sweepLoop 与 closeSession

```go
func (s *Server) sweepLoop() {
    defer s.wg.Done()

    ticker := time.NewTicker(s.options.SweepInterval)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            s.sweep()
        case <-s.stopCh:
            return
        }
    }
}

func (s *Server) sweep() {
    manager := s.runtime.Sessions()
    now     := time.Now()

    s.sessions.Range(func(key, val any) bool {
        sess := val.(*Session)
        if now.Sub(sess.LastActiveAt()) > s.options.SessionTTL {
            s.sessions.Delete(key)
            manager.Unregister(sess.ID())
            s.runtime.Plugins().OnClose(sess)
            sess.Close(context.Background())
        }
        return true
    })
}

func (s *Server) closeSession(sess *Session) {
    addrKey := sess.RemoteAddr().String()
    s.sessions.Delete(addrKey)
    s.runtime.Sessions().Unregister(sess.ID())
    s.runtime.Plugins().OnClose(sess)
    sess.Close(context.Background())
}
```

### 4.9 Stop 实现

```go
func (s *Server) Stop(ctx context.Context) error {
    if !s.closed.CompareAndSwap(false, true) {
        return nil
    }

    close(s.stopCh)

    if s.conn != nil {
        s.conn.Close()
    }

    done := make(chan struct{})
    go func() {
        s.wg.Wait()
        close(done)
    }()

    select {
    case <-done:
    case <-ctx.Done():
        s.runtime.Logger().Warn("coap server stop timeout")
    }

    // 清理所有伪会话
    manager := s.runtime.Sessions()
    plugins := s.runtime.Plugins()
    s.sessions.Range(func(key, val any) bool {
        sess := val.(*Session)
        s.sessions.Delete(key)
        manager.Unregister(sess.ID())
        plugins.OnClose(sess)
        sess.Close(context.Background())
        return true
    })

    s.runtime.Logger().Info("coap server stopped")
    return nil
}
```

---

## 5. 配置项完整参考

### 5.1 Options 定义

```go
// internal/transport/coap/options.go
type Options struct {
    Addr          string        // 监听地址,默认 "127.0.0.1:18500"
    Handler       core.Handler  // 收到请求后的处理函数
    Responder     func(core.Session, core.Message) ([]byte, error) // 可选应答器(response 模式)
    SessionTTL    time.Duration // 伪会话 TTL,默认 2 分钟
    SweepInterval time.Duration // 清扫间隔,默认 30 秒
    MaxDatagram   int           // 纯 UDP 共享读缓冲,默认 64KB

    // DTLS 模式每连接独占一块读缓冲,默认刻意小于 MaxDatagram:
    // 16 KiB 覆盖典型 CoAP / LwM2M 报文,更大载荷应使用分块传输(RFC 7959).
    DTLSReadBufferBytes int

    TLSConfig *tls.Config // 非 nil 即启用 DTLS
}
```

**没有可配置的重传参数:** `maxRetransmit = 4` 与 `retransmitInterval = 2s` 是包级常量,
对齐 RFC 7252 的 ACK_TIMEOUT 默认值(2s)与 MAX_RETRANSMIT 默认值(4).把两者暴露成配置项
意味着存在"不符合 RFC 默认"的部署,而互操作性问题只会在对接第三方 CoAP 栈时才暴露;
真要调整时改常量并配套更新互操作测试,比放开一个配置项更安全.

同理,`MaxDatagram` 也没有 setter:它是单一共享缓冲,改动要同时评估单包上限与内存占用.

### 5.2 默认值与 Functional Options

```go
func defaultOptions() Options {
    return Options{
        Addr:                "127.0.0.1:18500",
        SessionTTL:          2 * time.Minute,
        SweepInterval:       30 * time.Second,
        MaxDatagram:         64 * 1024,
        DTLSReadBufferBytes: 16 * 1024,
    }
}

type Option func(*Options)

func WithAddr(addr string) Option
func WithHandler(handler core.Handler) Option
func WithResponder(responder func(core.Session, core.Message) ([]byte, error)) Option
func WithSessionTTL(ttl time.Duration) Option          // <= 0 忽略
func WithSweepInterval(interval time.Duration) Option  // <= 0 忽略
func WithDTLS(cfg *tls.Config) Option
func WithDTLSReadBufferBytes(size int) Option          // <= 0 忽略

func NewServer(opts ...Option) *Server {
    cfg := defaultOptions()
    for _, opt := range opts {
        opt(&cfg)
    }
    return &Server{
        opts:            cfg,
        observers:       NewObserverRegistry(),
        pendingNotifies: make(map[notifyKey]pendingNotify),
    }
}
```

`NewServer` 不接受 handler 位置参数:handler 经 `WithHandler` 或 `WithResponder` 传入,
两者都缺省时服务器仍可启动,只是收到请求后不产生应答(用于纯 Observe 通知的部署).
`internal/app` 在 `mode=lwm2m` 时用 `WithResponder` 挂 LwM2M responder,普通模式用
`WithHandler`.

---

# TRANSPORT.md - Part 4/5: WebSocket

> Shark-Socket 传输层:WebSocket 协议实现细节  
> 版本:v0.2.x-alpha  
> 最后更新:2026-06-01

---

## 目录(WebSocket 部分)

1. [WebSocket 协议概述](#1-websocket-协议概述)
2. [WSSession](#2-wssession)
3. [WSServer](#3-wsserver)
4. [配置项完整参考](#4-配置项完整参考)

---

## 1. WebSocket 协议概述

### 1.1 在框架中的定位

WebSocket 是**基于 HTTP Upgrade 的全双工长连接协议**,与 TCP 的核心差异:

| 维度 | TCP | WebSocket |
|------|-----|-----------|
| 连接建立 | `net.Listen` + `Accept` | HTTP Upgrade(`gorilla/websocket` 处理) |
| 帧边界 | 需要 Framer 处理粘包 | 协议内置消息边界(Text/Binary/Control 帧) |
| 写并发安全 | writeLoop 单 goroutine 独占 | `writeMu sync.Mutex`(gorilla 要求) |
| 心跳机制 | 应用层自定义(HeartbeatPlugin) | 协议内置 Ping/Pong 控制帧 |
| 关闭流程 | 六步状态机(drain 写队列) | 发送 Close 帧 + 等待对端 Close 帧 |
| Goroutine 数量 | 每连接 2 个 | 每连接 2 个(handleSession + pingLoop) |

### 1.2 gorilla/websocket 并发约束

`gorilla/websocket` 库的**核心约束**(必须严格遵守):

```
并发读:不允许(同一连接同时只能有一个 Read 调用)
并发写:不允许(同一连接同时只能有一个 Write 调用)

框架处理方式:
  读:handleSession goroutine 独占 ReadMessage(天然串行)
  写:所有写操作(Send / SendText / sendPing)加 writeMu 互斥锁
```

### 1.3 OnClose 单次执行保证

WebSocket 有两条并发退出路径:

```
路径1:Gateway.Stop() -> StagedServer.CloseSessions() -> sess.Close()
路径2:handleSession readLoop -> conn.ReadMessage() 返回 EOF -> sess.Close()

两条路径都调用 sess.Close()
-> closeOnce.Do() 保证内部逻辑只执行一次
-> OnClose(sess) 在 Do() 内部,天然单次执行
```

### 1.4 文件清单

```
internal/transport/websocket/
+-- session.go   # WSSession:writeMu + Ping/Pong + OnClose 单次保证
+-- server.go    # WSServer:Upgrade + handleSession + pingLoop + StagedServer
+-- options.go   # WSOption Functional Options
```

### 1.5 编译期验证

```go
// internal/transport/websocket/session.go
var _ core.Session = (*Session)(nil)

// internal/transport/websocket/server.go
var _ core.Server              = (*Server)(nil)
var _ core.RuntimeConfigurable = (*Server)(nil)
var _ core.StagedServer        = (*Server)(nil)
```

---

## 2. WSSession

### 2.1 结构定义

```go
// internal/transport/websocket/session.go
type Session struct {
    // 身份(不可变)
    id         uint64
    remoteAddr net.Addr
    localAddr  net.Addr
    createdAt  time.Time

    // 网络
    conn *websocket.Conn

    // 写并发保护(gorilla/websocket 要求)
    writeMu sync.Mutex

    // 状态
    state      atomic.Int32
    lastActive atomic.Int64
    ctx        context.Context
    cancel     context.CancelFunc

    // 并发控制
    closeOnce sync.Once

    // 元数据
    meta sync.Map

    // 可观测
    logger  core.Logger
    metrics core.Metrics
}

// 编译期验证
var _ core.Session = (*Session)(nil)
```

### 2.2 构造函数

```go
func newSession(
    id uint64,
    conn *websocket.Conn,
    logger core.Logger,
    metrics core.Metrics,
) *Session {
    ctx, cancel := context.WithCancel(context.Background())
    sess := &Session{
        id:         id,
        remoteAddr: conn.RemoteAddr(),
        localAddr:  conn.LocalAddr(),
        createdAt:  time.Now(),
        conn:       conn,
        ctx:        ctx,
        cancel:     cancel,
        logger:     logger,
        metrics:    metrics,
    }
    sess.state.Store(int32(core.Connecting))
    sess.lastActive.Store(time.Now().UnixNano())
    return sess
}
```

### 2.3 核心接口实现

```go
func (s *Session) ID() uint64              { return s.id }
func (s *Session) Protocol() core.Protocol { return core.WebSocket }
func (s *Session) RemoteAddr() net.Addr    { return s.remoteAddr }
func (s *Session) LocalAddr() net.Addr     { return s.localAddr }
func (s *Session) CreatedAt() time.Time    { return s.createdAt }
func (s *Session) Context() context.Context { return s.ctx }

func (s *Session) State() core.SessionState {
    return core.SessionState(s.state.Load())
}

func (s *Session) IsAlive() bool {
    return s.State() == core.Active
}

func (s *Session) LastActiveAt() time.Time {
    return time.Unix(0, s.lastActive.Load())
}

func (s *Session) TouchActive() {
    s.lastActive.Store(time.Now().UnixNano())
}

func (s *Session) SetMeta(key string, val any) { s.meta.Store(key, val) }
func (s *Session) GetMeta(key string) (any, bool) { return s.meta.Load(key) }
func (s *Session) DelMeta(key string) { s.meta.Delete(key) }
```

### 2.4 Send 实现(Binary 消息)

```go
// Send 发送 Binary 消息.
// 必须加 writeMu,gorilla/websocket 写操作非并发安全.
func (s *Session) Send(data []byte) error {
    if !s.IsAlive() {
        return core.ErrSessionClosed
    }

    s.writeMu.Lock()
    defer s.writeMu.Unlock()

    if err := s.conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
        s.metrics.IncCounter("transport_errors_total", "protocol", "websocket", "type", "write")
        return err
    }

    s.metrics.IncCounter("messages_total", "protocol", "websocket", "direction", "out")
    // core.Metrics 没有"按量累加"的 API,字节数这类指标应使用
    // ObserveHistogram 记录分布,而不是把它当累加计数器

    return nil
}
```

### 2.5 SendText 实现(Text 消息)

```go
// SendText 发送 Text 消息(UTF-8 编码).
func (s *Session) SendText(data []byte) error {
    if !s.IsAlive() {
        return core.ErrSessionClosed
    }

    s.writeMu.Lock()
    defer s.writeMu.Unlock()

    if err := s.conn.WriteMessage(websocket.TextMessage, data); err != nil {
        s.metrics.IncCounter("transport_errors_total", "protocol", "websocket", "type", "write_text")
        return err
    }

    s.metrics.IncCounter("messages_total", "protocol", "websocket", "direction", "out")
    return nil
}
```

### 2.6 sendPing 实现(内部心跳)

```go
// sendPing 发送 Ping 控制帧(内部方法,由 pingLoop 调用).
func (s *Session) sendPing() error {
    s.writeMu.Lock()
    defer s.writeMu.Unlock()

    return s.conn.WriteMessage(websocket.PingMessage, nil)
}
```

**三个写方法的锁使用一致性:**

```
Send       -> writeMu.Lock() -> WriteMessage(BinaryMessage) -> Unlock
SendText   -> writeMu.Lock() -> WriteMessage(TextMessage)   -> Unlock
sendPing   -> writeMu.Lock() -> WriteMessage(PingMessage)   -> Unlock
sendClose  -> writeMu.Lock() -> WriteMessage(CloseMessage)  -> Unlock

所有写操作必须通过 writeMu,不允许绕过锁直接调用 conn.WriteMessage
```

### 2.7 Close 实现(OnClose 单次保证)

```go
// Close 关闭 WebSocket 会话.
// sync.Once 保证多条并发退出路径(Gateway Stop + readLoop EOF)只执行一次清理.
func (s *Session) Close(ctx context.Context) error {
    var closeErr error
    s.closeOnce.Do(func() {
        // 步骤1:标记状态
        s.state.Store(int32(core.Closed))

        // 步骤2:发送 Close 帧(通知对端)
        s.writeMu.Lock()
        s.conn.WriteMessage(
            websocket.CloseMessage,
            websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
        )
        s.writeMu.Unlock()

        // 步骤3:取消 context(通知 pingLoop 退出)
        s.cancel()

        // 步骤4:关闭底层连接
        closeErr = s.conn.Close()

        // sessions_active 由 runtime 的 metricSessionManager 统一维护,传输层不单独读写
        s.logger.Info("websocket session closed",
            "session_id", s.id,
            "remote_addr", s.remoteAddr.String())
    })
    return closeErr
}
```

**注意:** WebSocket 的 `Close` 不包含 drain 写队列步骤,因为:

```
WebSocket 无写队列:
  Send 通过 writeMu 同步写,不经过队列
  Close 发送 Close 帧后直接关闭连接
  对端收到 Close 帧后停止发送,连接双方协商关闭

与 TCP 的差异:
  TCP Close:drain 写队列(确保已入队消息全部发出)
  WebSocket Close:发送 Close 帧(协议层握手关闭)
```

---

## 3. WSServer

### 3.1 结构定义

```go
// internal/transport/websocket/server.go
type Server struct {
    opts     Options
    rt       core.Runtime     // 经 UseRuntime 注入
    listener net.Listener
    server   *http.Server
    acceptor *shared.Acceptor // 并发上限 + 接受速率

    closed   atomic.Bool
    started  atomic.Bool
    wg       sync.WaitGroup   // Serve 循环 + 各会话读循环
    sessions sync.Map         // id -> *session
}

// 编译期验证
var _ core.Server              = (*Server)(nil)
var _ core.RuntimeConfigurable = (*Server)(nil)
var _ core.StagedServer        = (*Server)(nil)
```

没有 `upgrader` 字段:`websocket.Upgrader` 在每次升级时按当前配置就地构造,
避免把请求级的 `CheckOrigin` 闭包固化在 Server 结构上.

### 3.2 UseRuntime 与 Protocol

```go
func (s *Server) UseRuntime(rt core.Runtime) { s.rt = rt }
func (s *Server) Protocol() core.Protocol    { return core.ProtocolWS }
```

### 3.3 Start 实现

```go
func (s *Server) Start(context.Context) error {
    if !s.started.CompareAndSwap(false, true) {
        return fmt.Errorf("websocket server already started")
    }
    s.closed.Store(false)
    if s.rt == nil {
        s.rt = runtime.NewRuntime(nil, nil)
    }
    s.acceptor = shared.NewAcceptor(s.opts.MaxConnections, s.opts.AcceptRate)

    mux := http.NewServeMux()
    mux.HandleFunc(s.opts.Path, s.handleUpgrade)

    var ln net.Listener
    var err error
    if s.opts.TLSConfig != nil {
        // 用 tls.Listen 而不是给 http.Server 设 TLSConfig:
        // 后者会协商 HTTP/2,与 WebSocket 升级冲突.
        s.server = &http.Server{
            Addr:         s.opts.Addr,
            Handler:      mux,
            ReadTimeout:  s.opts.ReadTimeout,
            WriteTimeout: s.opts.WriteTimeout,
            IdleTimeout:  s.opts.IdleTimeout,
        }
        ln, err = tls.Listen("tcp", s.opts.Addr, s.opts.TLSConfig)
    } else {
        s.server = &http.Server{
            Addr:         s.opts.Addr,
            Handler:      mux,
            ReadTimeout:  s.opts.ReadTimeout,
            WriteTimeout: s.opts.WriteTimeout,
            IdleTimeout:  s.opts.IdleTimeout,
        }
        ln, err = net.Listen("tcp", s.opts.Addr)
    }
    if err != nil {
        s.started.Store(false)
        return fmt.Errorf("websocket listen %s: %w", s.opts.Addr, err)
    }
    s.listener = ln
    s.wg.Add(1)
    go func() {
        defer s.wg.Done()
        // Serve 返回 http.ErrServerClosed 是正常关停.
        if err := s.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
            s.rt.Logger().Error("websocket serve failed", "error", err)
        }
    }()
    return nil
}
```

**为什么先 `net.Listen` 再 `Serve`,而不是 `ListenAndServe`:** `ListenAndServe` 把绑定失败
藏在内部 goroutine 里,调用方只能靠"等一小段时间看有没有报错"来猜测是否启动成功 --
既不可靠又会引入固定延迟.显式 `net.Listen` 让绑定错误同步返回,`Serve` 只负责服务.

**超时必须显式设置:** `http.Server` 的三个超时默认都是 0(不限制).不设置的话,一个慢速
HTTP 请求(未升级为 WebSocket)可以永久占用一条连接.

**StagedServer 的三阶段在这里不对称:** `StopAccept` 调 `http.Server.Shutdown(ctx)` 停止
接受新连接(并等待普通 HTTP 请求结束),`Drain` 是**空操作** -- `Shutdown` 已经等待过 Serve
循环,而 `CloseSessions` 负责关闭 WebSocket 连接.注意 `Shutdown` 不跟踪已被 hijack 的
WebSocket 连接,这正是必须由 `CloseSessions` 显式关闭它们的原因.

### 3.4 Origin 检查

Origin 校验通过 `Options.CheckOrigin`(`func(*http.Request) bool`)注入,每次升级时交给
gorilla/websocket 的 `Upgrader`:

```go
func (s *Server) handleUpgrade(w http.ResponseWriter, r *http.Request) {
    if s.acceptor != nil && !s.acceptor.TryAccept() {
        http.Error(w, "server busy", http.StatusServiceUnavailable)
        return
    }
    upgrader := websocket.Upgrader{CheckOrigin: s.opts.CheckOrigin}
    conn, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        s.acceptor.Done()
        return
    }
    // ... 设置读上限,PongTimeout,注册会话,OnAccept,readLoop
}
```

**默认拒绝:** `defaultOptions()` 里 `CheckOrigin` 返回 `false`,即未显式配置时拒绝一切
跨域升级.这是 fail-closed 的选择 -- 忘记配置会立刻暴露(握手失败),而不是静默允许任意源.
生产配置用 `app.allowedOriginChecker`,它把 `allowed_origins` 列表转成检查函数,
其中 `"*"` 表示放行全部(仅限开发).

无 `Origin` 头的请求(非浏览器客户端,如 `wscat`,Go 客户端)是否放行完全由注入的判断函数
决定;`allowedOriginChecker` 对空 Origin 的处理是查表未命中即拒绝,因此非浏览器客户端
需要在列表里显式加 `"*"` 或做代理改写.

### 3.5 handleUpgrade 实现

### 3.5 handleUpgrade 实现

```go
func (s *Server) handleUpgrade(w http.ResponseWriter, r *http.Request) {
    // 连接准入:超过 MaxConnections / AcceptRate 的请求直接返回 503,
    // 不再尝试握手(与 TCP 的 acceptor 语义一致).
    if s.acceptor != nil && !s.acceptor.TryAccept() {
        http.Error(w, "server busy", http.StatusServiceUnavailable)
        return
    }

    upgrader := websocket.Upgrader{CheckOrigin: s.opts.CheckOrigin}
    conn, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        // 升级失败必须归还准入额度,否则失败请求会永久占用一个名额.
        s.acceptor.Done()
        return
    }
    if s.opts.MaxMessageSize > 0 {
        conn.SetReadLimit(s.opts.MaxMessageSize)
    }
    // 死对端检测:有 pong 就把读截止时间后推;不再回应 ping 的对端在
    // PongTimeout 后被关闭(RFC 6455 要求回应 pong,合规客户端必然会答).
    if s.opts.PongTimeout > 0 {
        conn.SetPongHandler(func(string) error {
            return conn.SetReadDeadline(time.Now().Add(s.opts.PongTimeout))
        })
    }

    id := s.rt.Sessions().NextID()
    sess := newSession(id, conn, s.opts.WriteTimeout)
    s.sessions.Store(id, sess)
    if err := s.rt.Sessions().Register(sess); err != nil {
        s.acceptor.Done()
        // 从未被 accept:直接丢弃,不发 OnClose(插件链从未运行过).
        if _, loaded := s.sessions.LoadAndDelete(id); loaded {
            s.rt.Sessions().Unregister(id)
            _ = sess.Close(context.Background())
        }
        return
    }
    if err := s.rt.Plugins().OnAccept(sess); err != nil {
        s.acceptor.Done()
        // OnAccept 失败:插件链已回滚部分 accept,这里不再发 OnClose.
        if _, loaded := s.sessions.LoadAndDelete(id); loaded {
            s.rt.Sessions().Unregister(id)
            _ = sess.Close(context.Background())
        }
        return
    }
    s.wg.Add(1)
    go func() {
        defer s.wg.Done()
        defer s.acceptor.Done()
        s.readLoop(sess)
    }()
}
```

**为什么用 `acceptor` 而不是早期草案的 `acceptStopped` channel:** `acceptStopped` 只在 `StopAccept`
时关闭,所以它只能表达"服务正在关停";`shared.Acceptor` 同时覆盖并发上限与接受速率,
并且在 `Drain` 阶段通过 `WaitGroup`/`Done` 配对保证不会漏还额度.一个字段承担两件事,
少一处状态就少一处不一致的可能.

### 3.6 readLoop 实现

WebSocket 没有独立的 `handleSession`:升级成功后直接起一个 goroutine 跑 `readLoop`。

```go
func (s *Server) readLoop(sess *session) {
    // 退出时统一清理:关闭连接,注销会话,发 OnClose(见 sec.2.7).
    defer s.closeSession(context.Background(), sess.ID(), sess)
    for {
        // 每轮读取前重设读截止时间.有 PongTimeout 时,对端必须在窗口内
        // 发来任何数据(通常是 pong)才能续期.
        if s.opts.PongTimeout > 0 {
            if err := sess.conn.SetReadDeadline(time.Now().Add(s.opts.PongTimeout)); err != nil {
                return
            }
        }
        _, payload, err := sess.conn.ReadMessage()
        if err != nil {
            // 读截止时间到期说明对端不再回应 ping(客户端消失),计入回收指标.
            if shared.IsTimeout(err) {
                s.rt.Metrics().IncCounter("sessions_reclaimed_total")
            }
            return
        }
        sess.touch()
        payload, err = s.rt.Plugins().OnMessage(sess, payload)
        if err != nil {
            if errors.Is(err, core.ErrPluginDrop) {
                continue // 丢弃该消息,连接继续
            }
            return
        }
        if s.opts.Handler != nil {
            msg := core.Message{SessionID: sess.ID(), Protocol: core.ProtocolWS, Payload: payload}
            if err := shared.CallHandler(func() error { return s.opts.Handler(sess, msg) }, s.rt.Logger()); err != nil {
                return
            }
        }
    }
}
```

**WebSocket 不经过 WorkerPool:** `OnMessage` 与 handler 都在 `readLoop` 上串行执行,
因此同一条连接的消息天然有序.代价是一个慢 handler 会阻塞该连接的后续消息(队首阻塞,
见 `PROJECT-REVIEW-261015-172344.md` 的记录项 D1).

**`PongTimeout` 同时充当读超时:** 不另设 `ReadTimeout` 检查,因为 gorilla 的
`SetReadDeadline` 已经覆盖"任何帧到达"这一事件;再叠一层只会让两个超时互相掩盖.

### 3.7 pingLoop 与死对端检测

```go
func (s *Server) pingLoop(sess *session) {
    ticker := time.NewTicker(s.opts.PingInterval)
    defer ticker.Stop()
    for {
        select {
        case <-ticker.C:
            if err := sess.ping(); err != nil {
                // 发送失败即认为连接已不可用,直接清理.
                s.closeSession(context.Background(), sess.ID(), sess)
                return
            }
        case <-sess.Context().Done():
            // sess.Close() 触发 cancel 后退出.
            return
        }
    }
}
```

时序(默认 `PingInterval=30s`,`PongTimeout=60s`):

```
T=0      升级成功.第一次 ReadMessage 前设 ReadDeadline = T + 60s
T=30s    pingLoop 发送 Ping
T=30s+   客户端回 Pong -> PongHandler 触发 -> ReadDeadline = now + 60s
T=30s+   readLoop 收到 Pong 帧(控制帧),touch(),继续读
T=60s    pingLoop 再次发送 Ping
...

对端消失时:
  最后一次成功读之后 60s 内没有任何帧到达
  ReadDeadline 到期 -> ReadMessage 返回 timeout
  shared.IsTimeout 判定为超时 -> 计入 sessions_reclaimed_total
  readLoop 返回 -> closeSession 关闭并注销
```

`PongTimeout` 之所以能当"死对端检测",依赖 RFC 6455 强制客户端回应 Ping:
合规客户端在收到 Ping 后必然会发出 Pong,而 Pong 帧会重置读截止时间.
一个不回应 Ping 的对端本质上已经不可用,在 60s 内回收它是正确的取舍.

### 3.8 StagedServer 三阶段实现

```go
// StopAccept 停止接受新的 WebSocket 升级请求.
func (s *Server) StopAccept(ctx context.Context) error {
    if !s.closed.CompareAndSwap(false, true) {
        return nil
    }
    s.started.Store(false)
    if s.server != nil {
        return s.server.Shutdown(ctx)
    }
    return nil
}

// Drain 对 WebSocket 是空操作.
func (s *Server) Drain(ctx context.Context) error {
    // http.Server.Shutdown(在 StopAccept 中调用)已经等待 Serve 循环退出,
    // 而连接本身由 CloseSessions 关闭.
    return nil
}

// CloseSessions 关闭所有活跃 WebSocket 会话.
func (s *Server) CloseSessions(ctx context.Context) error {
    s.sessions.Range(func(_, v any) bool {
        sess := v.(*session)
        s.closeSession(ctx, sess.ID(), sess)
        return true
    })
    return nil
}
```

**为什么 `Shutdown` 不够:** `http.Server.Shutdown` 会等待普通 HTTP 请求结束并停止接受新连接,
但它**不跟踪已被 hijack 的连接**,而 WebSocket 升级正是一次 hijack.因此升级成功的连接完全
不受 `Shutdown` 影响,必须由 `CloseSessions` 逐个关闭.这也解释了为什么 `Drain` 可以是空操作:
它的职责(`Shutdown` 等待 Serve 循环)已经由 `StopAccept` 承担.

---

## 4. 配置项完整参考

### 4.1 Options 定义

```go
// internal/transport/websocket/options.go
type Options struct {
    Addr           string                   // 默认 "127.0.0.1:18700"
    Path           string                   // 默认 "/ws"
    Handler        core.Handler             // 消息处理函数
    TLSConfig      *tls.Config              // 非 nil 即用 tls.Listen
    CheckOrigin    func(*http.Request) bool // 默认恒 false(拒绝)
    PingInterval   time.Duration            // 默认 30s
    PongTimeout    time.Duration            // 默认 60s
    MaxMessageSize int64                    // 默认 1MB
    WriteTimeout   time.Duration            // 默认 10s
    ReadTimeout    time.Duration            // 默认 10s
    IdleTimeout    time.Duration            // 默认 2m
    MaxConnections int64                    // 并发连接上限,<= 0 不限制
    AcceptRate     float64                  // 接受速率(连接/秒),<= 0 不限制
}
```

与早期草案的差异:

| 草案字段 | 现状 | 原因 |
|----------|------|------|
| `ReadBufferSize` / `WriteBufferSize` | 去掉 | 未实现,gorilla 使用默认缓冲大小 |
| `UpgradeTimeout` | 去掉 | 未实现;升级耗时由 `ReadTimeout` 覆盖 |
| `AllowedOrigins []string` | 改为 `CheckOrigin func` | 传输层不知道配置字段名,只接受一个判断函数,便于组合 |
| `MaxSessions` | 去掉 | 会话上限统一由 `SessionManager` 管 |

新增 `MaxConnections` / `AcceptRate`:与 TCP 一致,用 `shared.Acceptor` 在升级前准入,
超额请求直接返回 503 而不再尝试握手.

### 4.2 默认值

```go
func defaultOptions() Options {
    return Options{
        Addr:           "127.0.0.1:18700",
        Path:           "/ws",
        PingInterval:   30 * time.Second,
        PongTimeout:    60 * time.Second,
        MaxMessageSize: 1024 * 1024,
        ReadTimeout:    10 * time.Second,
        WriteTimeout:   10 * time.Second,
        IdleTimeout:    120 * time.Second,
        CheckOrigin: func(*http.Request) bool {
            return false // reject by default; use WithCheckOrigin to allow origins
        },
    }
}
```

`PongTimeout` 是死对端检测:每个 pong 会把读截止时间往后推,而 RFC 6455 要求客户端必须
回应 ping,因此一个合规客户端永远不会触发它.`ReadTimeout` 与 `PongTimeout` 叠加后,
最坏情况下一条无响应的连接会在 10s + 60s 内被判死.

### 4.3 Functional Options

```go
type Option func(*Options)

func WithAddr(addr string) Option
func WithPath(path string) Option
func WithHandler(handler core.Handler) Option
func WithTLSConfig(cfg *tls.Config) Option
func WithCheckOrigin(fn func(*http.Request) bool) Option
func WithPingInterval(interval time.Duration) Option
func WithPongTimeout(timeout time.Duration) Option
func WithMaxConnections(max int64) Option
func WithAcceptRate(rate float64) Option
```

与 Options 字段不一致之处(有意为之,不是遗漏):

| 字段 | 有无对应 Option | 说明 |
|------|------------------|------|
| `MaxMessageSize` | 无 | 只有字段与默认值 1MB;当前没有 `WithMaxMessageSize` |
| `ReadTimeout` / `WriteTimeout` / `IdleTimeout` | 无 | 只有字段与默认值;当前没有对应 Option |
| `TLSConfig` | 有,名为 `WithTLSConfig` | 不是 `WithTLS` |

即上述四个超时/大小字段目前只能通过默认值生效;需要运行期可调时应先补一个 Option 函数,
再在 `internal/app` 里接线,而不是假定它已经存在.

### 4.4 配置使用示例

`NewServer` 只接受 Option,不接受位置参数的 handler:

```go
// 基础 WebSocket 服务
server := websocket.NewServer(
    websocket.WithAddr(":18700"),
    websocket.WithPath("/ws"),
    websocket.WithHandler(func(sess core.Session, msg core.Message) error {
        return sess.Send(msg.Payload) // Echo
    }),
    websocket.WithCheckOrigin(allowedOriginChecker(
        "https://example.com", "https://app.example.com")),
    websocket.WithPingInterval(30*time.Second),
    websocket.WithPongTimeout(15*time.Second),
)

// WSS(WebSocket over TLS)
tlsConfig := &tls.Config{
    MinVersion: tls.VersionTLS13,
    // 证书通过 GetConfigForClient 热加载
}
secureServer := websocket.NewServer(
    websocket.WithAddr(":18443"),
    websocket.WithHandler(handler),
    websocket.WithTLSConfig(tlsConfig),
    websocket.WithCheckOrigin(allowedOriginChecker("https://example.com")),
)
```

`allowedOriginChecker` 是 `internal/app` 里的私有辅助函数:把配置中的
`allowed_origins []string` 编译成一个 `func(*http.Request) bool`,再交给 `WithCheckOrigin`.
传输层只认这个判断函数,不认配置字段名.

### 4.5 Origin 校验配置规范

传输层没有 `WithAllowedOrigins`;Origin 校验统一走 `WithCheckOrigin(func(*http.Request) bool)`,
默认实现恒返回 false(拒绝一切跨域升级).配置层面的 `allowed_origins` 由 `internal/app`
翻译成判断函数.

```
生产环境规范:
  [OK] 明确列出允许的来源(精确匹配)
  [OK] 使用 HTTPS 来源(防中间人)
  [X] 禁止恒返回 true(等价于允许任意跨域,CSRF 风险)
  [X] 禁止留空配置导致默认拒绝所有跨域;非浏览器客户端不受 Origin 影响

开发环境(可临时使用):
  websocket.WithCheckOrigin(allowedOriginChecker("http://localhost:3000"))

多来源示例:
  websocket.WithCheckOrigin(allowedOriginChecker(
      "https://app.example.com",
      "https://admin.example.com",
  ))
```

### 4.6 运维注意事项

**Ping/Pong 超时调优:**

```
问题:网络抖动导致 Pong 延迟,误判连接断开
解决:适当增大 PongTimeout(默认 15s 通常足够)

问题:移动网络客户端频繁重连(省电模式)
解决:增大 PingInterval(如 60s),减少不必要的心跳

问题:大量连接心跳检测消耗 CPU
解决:P2 引入时间轮替代 ticker(HeartbeatPlugin 也适用)
```

**消息大小限制:**

```
MaxMessageSize 通过 conn.SetReadLimit 在协议层强制:
  超限时 gorilla 自动关闭连接(发送 Close 帧,code=1009)
  不需要在 Handler 中额外检查消息大小

典型配置:
  文字聊天:64KB
  文件传输:4MB(考虑内存占用)
  IoT 控制:4KB(保持轻量)
```

# TRANSPORT.md - Part 5/5: 汇总说明

> Shark-Socket 传输层:协议汇总,对比与 StagedServer 实现表格  
> 版本:v0.2.x-alpha  
> 最后更新:2026-06-01

---

## 目录(汇总部分)

1. [协议特性对比](#1-协议特性对比)
2. [StagedServer 实现汇总](#2-stagedserver-实现汇总)
3. [Session 实现汇总](#3-session-实现汇总)
4. [关键配置对比](#4-关键配置对比)
5. [协议选型指南](#5-协议选型指南)
6. [跨协议数据流](#6-跨协议数据流)
7. [扩展新协议指南](#7-扩展新协议指南)

---

## 1. 协议特性对比

### 1.1 基本特性

| 特性 | TCP | UDP | CoAP | WebSocket |
|------|-----|-----|------|-----------|
| 传输层 | TCP 字节流 | UDP 数据报 | UDP 数据报 | TCP(HTTP Upgrade) |
| 连接模型 | 持久连接 | 无连接(伪会话) | 无连接(伪会话) | 持久连接 |
| 消息边界 | Framer 处理 | 数据报天然边界 | 数据报天然边界 | 协议内置帧 |
| 可靠性 | OS 保证 | 无保证 | CON 类型可靠 | OS 保证 |
| 顺序性 | 有序 | 无序 | 无序(NON)/ 有序(CON+Token) |有序 |
| 双向通信 | [OK] | [OK](应用层实现) | [OK](有限) | [OK](全双工) |
| 心跳机制 | 应用层(HeartbeatPlugin) | TTL 扫描(sweepLoop) | TTL 扫描 | 协议内置 Ping/Pong |
| 标准端口 | 自定义(18000) | 自定义(18001) | 5683 | 自定义(18700) |

### 1.2 框架实现特性

| 特性 | TCP | UDP | CoAP | WebSocket |
|------|-----|-----|------|-----------|
| 写队列 | [OK](有界 channel) | [X](直接写) | [X](直接写) | [X](mutex 同步写) |
| Drain 关闭 | [OK](六步状态机) | [X](立即关闭) | [X](立即关闭) | [X](Close 帧协商) |
| WorkerPool | [OK] | [X](同步 Handler) | [X](同步 Handler) | [X](handleSession goroutine) |
| Framer | [OK](4 种) | [X](数据报边界) | [X](数据报边界) | [X](gorilla 内置) |
| 伪会话 | [X] | [OK] | [OK] | [X] |
| StagedServer | [OK] | [X] | [X] | [OK] |
| retransmitLoop | [X] | [X] | [OK](CON 重传) | [X] |
| MessageID 去重 | [X] | [X] | [OK] | [X] |
| Origin 检查 | [X] | [X] | [X] | [OK] |

### 1.3 Goroutine 使用

| 协议 | per-Server Goroutine | per-Session Goroutine | 总计(N 连接) |
|------|---------------------|----------------------|---------------|
| TCP | 1(acceptLoop) | 2(readLoop + writeLoop) | 1 + 2N |
| UDP | 2(readLoop + sweepLoop) | 0(复用 Server readLoop) | 2 |
| CoAP | 2(readLoop + sweepLoop) | 1(retransmitLoop,per 伪会话) | 2 + N |
| WebSocket | 0(复用 http.Server) | 2(handleSession + pingLoop) | 2N |

---

## 2. StagedServer 实现汇总

### 2.1 实现状态

| 协议 | 实现 StagedServer | StopAccept 的行为 |
|------|-----------------|------------------|
| TCP | [OK] | 关闭 listener + 等待 accept 退出 |
| WebSocket | [OK] | http.Server.Shutdown(不接受新升级) |
| UDP | [OK] | cancel 上下文 + 关闭 UDP conn / DTLS 连接与 listener |
| CoAP | [OK] | cancel 上下文 + 关闭 UDP conn / DTLS 连接与 listener |

本部分覆盖的四个传输层全部实现 `core.StagedServer`(`grpc-web` / `http` / `quic`
三个传输层同样实现).UDP 与 CoAP 虽然没有 per-connection accept,
但 DTLS 模式下有连接级别的 accept 循环与独立的会话,因此同样需要分阶段关停;
纯 UDP 模式下也至少需要"先停止收包再关闭会话",否则清扫循环会在关闭过程中继续造出新会话.

### 2.2 各阶段执行内容

```
StopAccept 阶段(并发执行):

  TCP:
    listener.Close()          -> acceptLoop 收到 net.ErrClosed 退出
    WorkerPool.Stop()         -> 等待所有 Worker 完成当前任务

  WebSocket:
    httpServer.Shutdown(ctx)  -> 停止接受新升级,等待普通 HTTP 请求结束

  UDP:
    cancel()                  -> 通知 readLoop / dtlsAcceptLoop
    dtlsConns.Range(Close)    -> 关闭全部 DTLS 连接
    dtlsLn.Close() / conn.Close()

  CoAP:
    cancel()
    dtlsConns.Range(Close)
    dtlsLn.Close() / udpConn.Close()

------------------------------------------------

Drain 阶段(并发执行):

  TCP:
    wg.Wait()                 -> 等待所有 readLoop / writeLoop 退出
    超时:强制继续,记录 Warn

  WebSocket:
    空操作(Shutdown 已等待 Serve 循环;连接由 CloseSessions 关闭)

  UDP:
    wg.Wait()                 -> readLoop / sweepLoop / dtlsAcceptLoop / 各 DTLS 读循环
    超时:返回 ctx.Err(),阶段继续

  CoAP:
    wg.Wait()                 -> readLoop / sweepLoop / dtlsAcceptLoop /
                                 seenCleanupLoop / retransmitLoop / 各 DTLS 读循环
    超时:返回 ctx.Err(),阶段继续

------------------------------------------------

CloseSessions 阶段(并发执行):

  TCP:
    manager.Range(TCP)        -> 遍历 TCP Session
    sess.Close(ctx)           -> 六步状态机关闭(drain + conn.Close)

  WebSocket:
    manager.Range(WebSocket)  -> 遍历 WebSocket Session
    sess.Close(ctx)           -> 发送 Close 帧 + conn.Close

------------------------------------------------

StopNonStaged 阶段(并发执行):

  UDP:
    close(stopCh)             -> readLoop / sweepLoop 退出
    conn.Close()              -> ReadFromUDP 返回 net.ErrClosed
    wg.Wait()                 -> 等待退出
    清理所有伪会话

  CoAP:
    close(stopCh)             -> readLoop / sweepLoop 退出
    conn.Close()              -> ReadFromUDP 返回 net.ErrClosed
    wg.Wait()                 -> 等待所有 retransmitLoop 退出
    清理所有伪会话

------------------------------------------------

CloseAll 阶段(串行,在所有协议阶段完成后执行):

  SessionManager.CloseAll(ctx)
    -> 清理 Manager 中可能遗留的 Session(应为空或极少数)
    -> 若有遗留,记录 Warn(说明协议层未正确清理)
```

### 2.3 阶段超时分配建议

```
总关闭超时(TotalShutdownTimeout)默认 30s,按比例分配:

  StopAccept:   总超时 x 1/6 ~= 5s
    -> 快速停止 listener,不需要等待业务逻辑

  Drain:        总超时 x 1/2 ~= 15s
    -> 等待在途请求完成,是最耗时的阶段

  CloseSessions:总超时 x 1/6 ~= 5s
    -> Session 已完成处理,关闭时间短

  CloseAll:     总超时 x 1/6 ~= 5s(含 NonStaged 阶段时间)
    -> 清理残留,通常为空

调整建议:
  高并发场景(100K 连接):TotalShutdownTimeout = 60s
    StopAccept 5s / Drain 30s / CloseSessions 15s / CloseAll 10s

  IoT 场景(CoAP/UDP 为主):TotalShutdownTimeout = 15s
    StopAccept 2s / Drain 5s / CloseSessions 5s / CloseAll 3s
```

---

## 3. Session 实现汇总

### 3.1 Session 字段对比

| 字段 | TCP | UDP | CoAP | WebSocket |
|------|-----|-----|------|-----------|
| `id` | [OK] | [OK] | [OK] | [OK] |
| `remoteAddr` | `net.Addr` | `*net.UDPAddr` | `*net.UDPAddr` | `net.Addr` |
| `conn` | `net.Conn` | `*net.UDPConn`(共享) | `*net.UDPConn`(共享) | `*websocket.Conn` |
| `writeQueue` | [OK](`chan []byte`) | [X] | [X] | [X] |
| `writeMu` | [X] | [OK] | [OK] | [OK] |
| `draining/drained` | [OK] | [X] | [X] | [X] |
| `messageCache` | [X] | [X] | [OK] | [X] |
| `pendingACKs` | [X] | [X] | [OK] | [X] |
| `closeOnce` | [OK] | [OK] | [OK] | [OK] |
| `meta` | `sync.Map` | `sync.Map` | `sync.Map` | `sync.Map` |

### 3.2 Send 行为对比

| 行为 | TCP | UDP | CoAP | WebSocket |
|------|-----|-----|------|-----------|
| 写方式 | 异步写队列 | 同步 WriteToUDP | 同步 WriteToUDP | 同步 WriteMessage |
| 队列满处理 | `ErrWriteQueueFull` | 不存在 | 不存在 | 不存在 |
| 并发保护 | writeLoop 单 goroutine | `sync.Mutex` | `sync.Mutex` | `sync.Mutex` |
| 数据拷贝 | 必须(队列异步) | 不需要(同步发送) | 不需要(同步发送) | 不需要(同步发送) |
| 阻塞调用方 | 否(非阻塞入队) | 是(WriteToUDP 可阻塞) | 是(WriteToUDP 可阻塞) | 是(WriteMessage 可阻塞) |

### 3.3 Close 行为对比

| 行为 | TCP | UDP | CoAP | WebSocket |
|------|-----|-----|------|-----------|
| 状态转换 | Active->Draining->Closed | Active->Closed | Active->Closed | Connecting/Active->Closed |
| Drain | [OK](等待写队列排空) | [X] | [X] | [X] |
| 发送关闭信号 | 无(连接直接关闭) | 无 | 无 | [OK](Close 帧) |
| 关闭共享资源 | 关闭独占 conn | 不关闭共享 UDPConn | 不关闭共享 UDPConn | 关闭独占 wsConn |
| 幂等保证 | `sync.Once` | `sync.Once` | `sync.Once` | `sync.Once` |

### 3.4 状态初始化对比

| 协议 | 创建时状态 | 转为 Active 时机 |
|------|-----------|----------------|
| TCP | `Connecting` | `handleConn` 中 `OnAccept` 成功后 |
| UDP | `Active`(直接) | 创建即 Active(`OnAccept` 在注册前执行) |
| CoAP | `Active`(直接) | 创建即 Active(同 UDP) |
| WebSocket | `Connecting` | `handleUpgrade` 中 `OnAccept` 成功后 |

---

## 4. 关键配置对比

### 4.1 地址与端口

| 协议 | 默认地址 | 标准端口 | 说明 |
|------|---------|---------|------|
| TCP | `127.0.0.1:18000` | 自定义 | 框架默认端口,默认只监听本机 |
| UDP | `127.0.0.1:18200` | 自定义 | 框架默认端口 |
| CoAP | `127.0.0.1:18500` | 5683(RFC 7252) | 默认端口仅承袭惯例,不等于 5683 |
| WebSocket | `127.0.0.1:18700` | 自定义 | 框架默认端口 |
| gRPC-Web | 见 `PROTOCOL.md` | 自定义 | 由 app 配置注入 |
| LwM2M | `0.0.0.0:5783` | 5783(LwM2M 标准) | 由 PROTOCOL.md 描述 |

### 4.2 会话限制

| 配置项 | TCP | UDP | CoAP | WebSocket |
|--------|-----|-----|------|-----------|
| 会话上限 | `SessionManager` 默认 100 万(全局共享) | 同左 | 同左 | 同左 |
| `SessionTTL` | 无(连接关闭决定) | 2m | 2m | 无(Ping/Pong 决定) |
| `SweepInterval` | 无 | 30s | 30s | 无 |
| `MaxConnections` | 有(`shared.Acceptor`) | 无 | 无 | 有 |
| `AcceptRate` | 有(令牌桶) | 无 | 无 | 有 |

会话上限不再由各传输层各自配置,而是由 `SessionManager` 统一持有(默认 100 万,
`runtime.WithMaxSessions` 可调),这样多协议共存时上限是总量而不是每协议配额.
`MaxConnections` 与 `AcceptRate` 是传输层的连接准入,与会话上限是两个层次的限制.

### 4.3 超时配置

| 配置项 | TCP | UDP | CoAP | WebSocket |
|--------|-----|-----|------|-----------|
| `ReadTimeout` | 5m(每帧) | 无(读循环靠 socket 关闭退出) | 无(同左) | 10s |
| `WriteTimeout` | 30s | 无 | 无 | 10s |
| `DrainTimeout` | 5s | 无 | 无 | 无(`Drain` 是空操作) |
| `IdleTimeout` | 无(由 `ReadTimeout` + HeartbeatPlugin 表达) | 无(由 `SessionTTL` 表达) | 无(同左) | 2m |
| `PingInterval` | 无 | 无 | 无 | 30s |
| `PongTimeout` | 无 | 无 | 无 | 60s |
| CON 重传间隔 | 无 | 无 | 2s(包级常量) | 无 |
| CON 最大重传 | 无 | 无 | 4(包级常量) | 无 |

关停阶段的超时不在传输层配置里,而是由 `Gateway` 的 `core.StageTimeouts`
(`StopAccept` / `Drain` / `CloseSessions`,默认 5s / 5s / 10s)统一下发.

### 4.4 消息大小限制

| 配置项 | TCP | UDP | CoAP | WebSocket |
|--------|-----|-----|------|-----------|
| 上限字段 | `MaxFrameBytes` | `MaxDatagram` | `MaxDatagram` | `MaxMessageSize` |
| 默认值 | 1MB | 64KB | 64KB | 1MB |
| 强制方式 | Framer 按前缀判长 | 读缓冲大小 | 读缓冲大小 | `conn.SetReadLimit` |
| 超限行为 | `ErrFrameTooLarge`,关连接 | 读缓冲截断,多余部分丢弃 | 解析失败即丢弃 | gorilla 关闭连接(code 1009) |

TCP 的 `Framer` 自身也带 `MaxFrameBytes`(同为 1MB),两者任一超限都会拒绝该帧;
保留两份配置是因为 `Options.MaxFrameBytes` 表达的是**连接级**上限,而 `Framer` 可以是
外部实现(见 sec.2.5 `RawFramer`),不一定自带长度判断.

---

## 5. 协议选型指南

### 5.1 按场景选型

| 场景 | 推荐协议 | 原因 |
|------|---------|------|
| 设备实时控制命令 | TCP | 可靠有序,自定义帧格式灵活 |
| 受限 IoT 设备数据上报 | CoAP | 协议开销小,适合低功耗设备,CON 保证可靠 |
| IoT 设备管理(固件升级,配置下发) | LwM2M(基于 CoAP) | 标准 OMA 设备管理协议 |
| Web 端实时推送(浏览器客户端) | WebSocket | 浏览器原生支持,全双工 |
| 高频低延迟日志/指标上报 | UDP | 无连接,低开销,允许丢失 |
| 内网微服务通信 | TCP | 可靠,支持自定义 Framer |
| 移动端弱网场景 | WebSocket(带重连) | 应用层心跳,断线重连机制成熟 |

### 5.2 协议共存场景

```
典型 IoT 平台部署:

+---------------------------------------------------------+
|                      Gateway                             |
+--------------+--------------+--------------+------------+
| TCP :18000   | WebSocket    | CoAP :5683   | UDP :18001 |
| 内网设备     | :18700/ws    | 受限设备     | 日志上报   |
| 自定义协议   | Web 客户端   | LwM2M 接入   | 高频指标   |
+--------------+--------------+--------------+------------+
         |              |              |            |
         +--------------+--------------+------------+
                    共享 SessionManager
                 跨协议广播 / 跨协议路由
```

### 5.3 不适合使用本框架的场景

| 场景 | 不适合原因 | 推荐替代 |
|------|-----------|---------|
| 纯 HTTP API 服务 | 框架 HTTP 为轻量支持,无路由,中间件生态 | `gin` / `echo` / `chi` |
| 高性能 HTTP 反代 | 无 L7 负载均衡,无缓存 | Nginx / Caddy |
| 完整 MQTT Broker | 不实现 QoS 状态机 | shark-MQTT / EMQX |
| 服务网格流量治理 | 无 sidecar 模式,无 xDS | Envoy / Istio |
| 完整 gRPC 服务 | 仅 gRPC-Web 子集 | `google.golang.org/grpc` |

---

## 6. 跨协议数据流

### 6.1 共享 SessionManager 跨协议查询

```go
// 业务层 Handler 示例:TCP 设备向 WebSocket 客户端推送消息

func tcpDeviceHandler(sess core.Session, msg core.Message) error {
    // 解析消息,提取目标 WebSocket Session ID
    targetSessionID := parseTargetID(msg.Payload)

    // 通过共享 SessionManager 查找 WebSocket Session
    manager := getSharedManager()
    targetSess, ok := manager.Get(targetSessionID)
    if !ok {
        return fmt.Errorf("target session %d not found", targetSessionID)
    }

    // 跨协议发送(TCP -> WebSocket)
    if err := targetSess.Send(msg.Payload); err != nil {
        if errors.Is(err, core.ErrSessionClosed) {
            return nil // 目标已下线
        }
        return err
    }
    return nil
}
```

### 6.2 跨协议广播

```go
// 向所有协议的所有 Session 广播消息
func broadcastToAll(manager core.SessionManager, data []byte) {
    if err := manager.Broadcast(data); err != nil {
        log.Println("broadcast partial failure:", err)
    }
}

// 向特定协议的 Session 广播
func broadcastToProtocol(manager core.SessionManager, proto core.Protocol, data []byte) {
    manager.Range(func(sess core.Session) bool {
        if sess.Protocol() == proto {
            sess.Send(data)
        }
        return true
    })
}
```

### 6.3 跨节点路由(集群模式)

```
本地节点:
  manager.Get(targetID) -> 未找到
      v
  Cache.Get("session:route:" + targetID) -> nodeID
      v
  PubSub.Publish("node."+nodeID+".route", {targetID, payload})

远端节点(ClusterPlugin 订阅):
  PubSub.Subscribe("node."+nodeID+".route", handler)
      v
  localManager.Get(targetID) -> 找到
      v
  sess.Send(payload)
```

---

## 7. 扩展新协议指南

### 7.1 最小实现清单

实现一个新协议传输层需要满足以下最小清单:

```
必须实现:
  [ ] Session 结构体(嵌入或实现 core.Session 接口)
  [ ] Session.Send([]byte) error
  [ ] Session.Close(context.Context) error(幂等)
  [ ] Session.State() / IsAlive() / LastActiveAt()
  [ ] Session.SetMeta / GetMeta / DelMeta(sync.Map 实现)
  [ ] var _ core.Session = (*YourSession)(nil)

  [ ] Server 结构体(实现 core.Server 接口)
  [ ] Server.Protocol() core.Protocol
  [ ] Server.Start(context.Context) error
  [ ] Server.Stop(context.Context) error
  [ ] var _ core.Server = (*YourServer)(nil)

  [ ] RuntimeConfigurable(若需要 Gateway 注入)
  [ ] Server.UseRuntime(core.Runtime)
  [ ] var _ core.RuntimeConfigurable = (*YourServer)(nil)

推荐实现(长连接协议):
  [ ] StagedServer 接口(StopAccept / Drain / CloseSessions)
  [ ] var _ core.StagedServer = (*YourServer)(nil)

可选:
  [ ] Functional Options(WithAddr / WithTLS 等)
  [ ] 自定义 Framer(若基于 TCP)
```

### 7.2 Session 实现模板

```go
// 新协议 Session 最小实现模板
type YourSession struct {
    id         uint64
    remoteAddr net.Addr
    localAddr  net.Addr
    createdAt  time.Time

    // 协议特定字段
    conn YourConn

    // 状态(通用)
    state      atomic.Int32
    lastActive atomic.Int64
    ctx        context.Context
    cancel     context.CancelFunc
    closeOnce  sync.Once
    meta       sync.Map

    logger  core.Logger
    metrics core.Metrics
}

var _ core.Session = (*YourSession)(nil)

func (s *YourSession) ID() uint64               { return s.id }
func (s *YourSession) Protocol() core.Protocol  { return core.Custom }
func (s *YourSession) RemoteAddr() net.Addr     { return s.remoteAddr }
func (s *YourSession) LocalAddr() net.Addr      { return s.localAddr }
func (s *YourSession) CreatedAt() time.Time     { return s.createdAt }
func (s *YourSession) Context() context.Context { return s.ctx }

func (s *YourSession) State() core.SessionState {
    return core.SessionState(s.state.Load())
}
func (s *YourSession) IsAlive() bool {
    return s.State() == core.Active
}
func (s *YourSession) LastActiveAt() time.Time {
    return time.Unix(0, s.lastActive.Load())
}
func (s *YourSession) TouchActive() {
    s.lastActive.Store(time.Now().UnixNano())
}

func (s *YourSession) SetMeta(key string, val any) { s.meta.Store(key, val) }
func (s *YourSession) GetMeta(key string) (any, bool) { return s.meta.Load(key) }
func (s *YourSession) DelMeta(key string)           { s.meta.Delete(key) }

func (s *YourSession) Send(data []byte) error {
    if !s.IsAlive() {
        return core.ErrSessionClosed
    }
    // 协议特定发送逻辑
    return nil
}

func (s *YourSession) Close(ctx context.Context) error {
    s.closeOnce.Do(func() {
        s.state.Store(int32(core.Closed))
        s.cancel()
        s.conn.Close()
    })
    return nil
}
```

### 7.3 Server 实现模板

```go
// 新协议 Server 最小实现模板
type YourServer struct {
    options YourOptions
    handler core.Handler
    runtime core.Runtime
    closed  atomic.Bool
    wg      sync.WaitGroup
}

var _ core.Server              = (*YourServer)(nil)
var _ core.RuntimeConfigurable = (*YourServer)(nil)

func (s *YourServer) Protocol() core.Protocol { return core.Custom }

func (s *YourServer) UseRuntime(rt core.Runtime) {
    s.runtime = rt
}

func (s *YourServer) Start(ctx context.Context) error {
    // 1. 建立监听
    // 2. 启动 acceptLoop 或 readLoop
    s.wg.Add(1)
    go s.loop()
    return nil
}

func (s *YourServer) loop() {
    defer s.wg.Done()
    // 协议特定逻辑
}

func (s *YourServer) Stop(ctx context.Context) error {
    if !s.closed.CompareAndSwap(false, true) {
        return nil // 幂等
    }
    // 停止监听,等待 goroutine 退出
    done := make(chan struct{})
    go func() {
        s.wg.Wait()
        close(done)
    }()
    select {
    case <-done:
        return nil
    case <-ctx.Done():
        return ctx.Err()
    }
}
```

### 7.4 注册到 Gateway

```go
// internal/app/app.go 中装配新协议
yourServer := your_protocol.NewServer(
    your_protocol.WithAddr(":18888"),
    your_protocol.WithHandler(handler),
)
if err := gateway.Register(yourServer); err != nil {
    return nil, err
}
```

### 7.5 新协议检查清单

```
功能验证:
  [ ] 单连接 Echo 测试通过
  [ ] 多连接并发测试通过(go test -race)
  [ ] Session.Close() 幂等(多次调用不 panic)
  [ ] Server.Stop() 幂等(多次调用不 panic)
  [ ] Gateway Start -> Stop -> Start 可重入
  [ ] OnAccept ErrPluginBlock 正确拒绝连接
  [ ] OnMessage ErrPluginDrop 正确丢弃消息

编译期验证:
  [ ] var _ core.Session = (*YourSession)(nil)
  [ ] var _ core.Server = (*YourServer)(nil)
  [ ] var _ core.RuntimeConfigurable = (*YourServer)(nil)
  [ ] var _ core.StagedServer = (*YourServer)(nil)(若实现)

指标验证:
  [ ] sessions_accepted_total{protocol="custom"} 正确递增(由 SessionManager 装饰器记录)
  [ ] sessions_active{protocol="custom"} 正确增减(同上)
  [ ] 自定义协议如需自己的指标,按 core.Metrics 三方法接入,并避免高基数标签

文档:
  [ ] 在 TRANSPORT.md 汇总表格中更新新协议行
  [ ] 在 CONFIGURATION.md 中添加配置字段
  [ ] 在 examples/ 中添加 basic_{protocol}/main.go
  [ ] 在 ROADMAP.md 中标记完成
```

---


---

## 附:指标录制 API 约定

框架的 `core.Metrics` 只有三个方法,没有 `Counter()/.Inc()` 之类的链式工厂:

```go
IncCounter(name string, labels ...string)
SetGauge(name string, value float64, labels ...string)
ObserveHistogram(name string, value float64, labels ...string)
```

`labels` 以 key/value 交替传入.本文档中的指标名是**示意名**,用于说明接入方式的形状;
框架自身实际录制的指标只有 5 个(`sessions_accepted_total` / `sessions_closed_total` /
`sessions_active` / `sessions_reclaimed_total` / `tcp_task_queue_full_total`),
清单见 `docs/guides/Architecture.md` 9.1 节.实现参考
`internal/infra/observability/prometheus.go`.

---

**版权声明:** 本文档属于 Shark-Socket 项目,遵循项目许可证.

