# TRANSPORT.md — Part 1/5: TCP

> Shark-Socket 传输层：TCP 协议实现细节  
> 版本：v0.1.0  
> 最后更新：2026-06-01

---

## 目录（TCP 部分）

1. [TCP 协议概述](#1-tcp-协议概述)
2. [Framer 接口与内置实现](#2-framer-接口与内置实现)
3. [TCPSession](#3-tcpsession)
4. [TCPServer](#4-tcpserver)
5. [TCPClient](#5-tcpclient)
6. [配置项完整参考](#6-配置项完整参考)

---

## 1. TCP 协议概述

### 1.1 在框架中的定位

TCP 是框架的**核心传输协议**，其设计模式是其他协议实现的参考基础：

- **Framer 接口**：解决 TCP 字节流的帧边界问题
- **写队列 + drain**：Session 关闭的六步状态机在 TCP 中首次定义
- **WorkerPool**：消息处理并发模型
- **StagedServer**：TCP 完整实现三阶段关闭

### 1.2 文件清单

```
internal/transport/tcp/
├── framer.go    # Framer 接口 + 4 种内置实现
├── session.go   # TCPSession：写队列 + drain + 6 步 Close
├── server.go    # TCPServer：accept + Framer + WorkerPool + StagedServer
├── client.go    # TCPClient：自动重连 + 指数退避
└── options.go   # TCPOption Functional Options
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

// Framer 解决 TCP 字节流的帧边界问题。
// 实现约束：
//   - ReadFrame 必须无状态，不跨调用保持预读缓冲（禁止使用 bufio.Reader 跨调用）
//   - WriteFrame 必须保证完整写入（处理短写，不允许部分发送）
//   - ReadFrame 和 WriteFrame 不需要并发安全（调用方保证单 goroutine 调用）
type Framer interface {
    ReadFrame(r io.Reader) ([]byte, error)
    WriteFrame(w io.Writer, payload []byte) error
}
```

**禁止使用 `bufio.Reader` 跨调用的原因：**

```
问题场景：
  bufio.Reader 内部预读缓冲跨 ReadFrame 调用保持状态
  若 ReadFrame 返回后调用方切换到不同 reader 或重用连接
  预读数据已被消费，后续 ReadFrame 从错误位置读取
  导致帧错位，丢失后续消息

正确方案：
  使用 io.ReadFull 精确读取固定字节数（无状态，每次调用独立）
  逐字节读到分隔符（LineFramer 方案）
```

### 2.2 LengthPrefixFramer（默认推荐）

```go
// LengthPrefixFramer 使用 4 字节大端长度前缀分帧。
// 格式：[4 字节大端长度][payload 字节]
type LengthPrefixFramer struct {
    MaxFrameSize int // 默认 1MB，防大包攻击
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
        return []byte{}, nil // 空帧（心跳）
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

    // 一次性写入头 + payload，防止短写
    buf := make([]byte, 4+length)
    copy(buf[:4], header[:])
    copy(buf[4:], payload)
    _, err := w.Write(buf)
    return err
}
```

**短写处理说明：**

```
问题：net.Conn.Write(buf) 可能只写入部分字节（短写）
场景：buf 长度超过系统发送缓冲区，内核只接受部分数据

正确方案（当前实现）：
  将 header + payload 合并为单个 buf 一次性写入
  net.Conn 的 Write 实现会内部重试直到全部写入或返回错误
  禁止分两次调用 Write（header 和 payload 分开写可能导致帧截断）

错误方案（禁止）：
  w.Write(header[:])   ← 可能短写
  w.Write(payload)     ← 第二次写可能和其他 goroutine 的写交错
```

### 2.3 LineFramer

```go
// LineFramer 以换行符 \n 作为帧分隔符，适用于文本行协议。
// 使用逐字节读取，保持无状态（不使用 bufio.Reader）。
type LineFramer struct {
    MaxLineSize int // 默认 64KB，防内存耗尽
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
            // 去除末尾 \r（兼容 \r\n）
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
// FixedSizeFramer 使用固定长度帧，适用于硬件协议、传感器数据。
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
// RawFramer 直接透传，单次 Read 返回。
// 注意：TCP 是字节流协议，单次 Read 不保证读取完整消息，调用方需自行处理粘包。
// 适用场景：简单测试、已知对端总是发送完整消息。
type RawFramer struct {
    BufferSize int // 单次读取缓冲大小，默认 4096
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
    // 身份（不可变）
    id         uint64
    remoteAddr net.Addr
    localAddr  net.Addr
    createdAt  time.Time

    // 协议
    conn   net.Conn
    framer Framer

    // 状态
    state      atomic.Int32  // core.SessionState
    lastActive atomic.Int64  // UnixNano，TouchActive 无锁更新
    ctx        context.Context
    cancel     context.CancelFunc

    // 写队列
    writeQueue chan []byte
    draining   chan struct{} // 关闭信号：Close 步骤2 触发
    drained    chan struct{} // 完成信号：writeLoop 发送完成后关闭

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

    // 复制调用方 data，防止 writeQueue 中的 buffer 被调用方复用覆盖
    buf := make([]byte, len(data))
    copy(buf, data)

    select {
    case s.writeQueue <- buf:
        s.metrics.Counter("shark_messages_total",
            "protocol", "tcp",
            "direction", "out").Inc()
        return nil
    default:
        s.metrics.Counter("shark_write_queue_full_total",
            "protocol", "tcp").Inc()
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
    // 步骤1：CAS Active → Draining
    if !s.state.CompareAndSwap(int32(core.Active), int32(core.Draining)) {
        // 已在关闭或已关闭，直接返回
        return nil
    }

    // 步骤2：触发排空信号
    if s.writerStarted.Load() {
        close(s.draining) // 通知 writeLoop 开始排空
    } else {
        // writeLoop 未启动，直接释放 writeQueue 中的待发数据
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

    // 步骤3：等待排空完成
    if s.writerStarted.Load() {
        select {
        case <-s.drained:
            // 正常排空完成
        case <-ctx.Done():
            // DrainTimeout 超时，强制继续
            s.logger.Warn("session drain timeout",
                "session_id", s.id,
                "remote_addr", s.remoteAddr.String())
        }
    }

    // 步骤4：CAS Draining → Closed
    s.state.Store(int32(core.Closed))

    // 步骤5：CancelContext → 通知所有 <-ctx.Done() 退出
    s.cancel()

    // 步骤6：关闭底层连接
    err := s.conn.Close()

    s.metrics.Gauge("shark_sessions_active", "protocol", "tcp").Dec()
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
            s.metrics.Counter("shark_transport_errors_total",
                "protocol", "tcp",
                "type", "read").Inc()
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

        s.metrics.Counter("shark_messages_total",
            "protocol", "tcp", "direction", "in").Inc()
        s.metrics.Counter("shark_message_bytes_total",
            "protocol", "tcp", "direction", "in").Add(float64(len(payload)))

        // 插件链处理
        data, err := plugins.RunMessage(s, payload)
        if err != nil {
            if errors.Is(err, core.ErrPluginDrop) {
                continue // 消息被丢弃，继续读取
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
            if core.IsFatal(err) {
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
        s.metrics.Counter("shark_transport_errors_total",
            "protocol", "tcp", "type", "write").Inc()
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

    // Runtime（通过 UseRuntime 注入）
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
    // 构建监听地址
    addr := s.options.Addr
    var listener net.Listener
    var err error

    if s.options.TLSConfig != nil {
        listener, err = tls.Listen("tcp", addr, s.options.TLSConfig)
    } else {
        listener, err = net.Listen("tcp", addr)
    }
    if err != nil {
        return fmt.Errorf("%w: %v", core.ErrListenFailed, err)
    }

    s.listener = listener
    s.pool = newWorkerPool(s.options.WorkerPoolOptions,
        s.runtime.Logger(), s.runtime.Metrics())
    s.pool.Start()

    s.runtime.Logger().Info("tcp server started",
        "protocol", "tcp",
        "addr", addr)
    s.runtime.Metrics().Gauge("shark_sessions_active", "protocol", "tcp").Set(0)

    go s.acceptLoop()
    return nil
}
```

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
                return // listener 已关闭，正常退出
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

    metrics.Counter("shark_sessions_total", "protocol", "tcp").Inc()
    metrics.Gauge("shark_sessions_active", "protocol", "tcp").Inc()

    // 插件链 OnAccept
    if err := plugins.RunAccept(sess); err != nil {
        // RunAccept 内部已调用 sess.Close()
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
            plugins.RunClose(sess)
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
// StopAccept 停止接受新连接，停止 WorkerPool 接收新任务。
func (s *Server) StopAccept(ctx context.Context) error {
    if s.listener != nil {
        s.listener.Close() // acceptLoop 检测到 net.ErrClosed 后退出
    }
    s.pool.Stop() // 等待 Worker 完成当前任务后退出
    s.runtime.Logger().Info("tcp server: stop accept done", "protocol", "tcp")
    return nil
}

// Drain 等待所有 readLoop / writeLoop goroutine 退出。
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
        return core.ErrDrainTimeout
    }
}

// CloseSessions 关闭所有活跃 TCPSession。
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

// Stop 完整停止（非 StagedServer 场景使用）。
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
    ReadTimeout     time.Duration // 默认 0（不限）
    WriteTimeout    time.Duration // 默认 10s
    Framer          Framer        // 默认 LengthPrefixFramer
    AutoReconnect   bool          // 默认 false
    ReconnectBackoff BackoffOptions
}

type BackoffOptions struct {
    InitialDelay time.Duration // 默认 100ms
    MaxDelay     time.Duration // 默认 30s
    JitterFactor float64       // 默认 0.2（±20% 随机抖动）
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
        return fmt.Errorf("%w: %v", core.ErrListenFailed, err)
    }

    c.conn = conn
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
    Addr      string      // 默认 "0.0.0.0:18000"
    TLSConfig *tls.Config // 默认 nil（不启用 TLS）

    // 帧解析
    Framer Framer // 默认 &LengthPrefixFramer{MaxFrameSize: 1MB}

    // 会话限制
    MaxSessions     int64 // 默认 100000（0=不限制）
    MaxMessageSize  int   // 默认 1MB（1<<20）
    WriteQueueSize  int   // 默认 128（每连接写队列容量）

    // 超时
    ReadTimeout     time.Duration // 默认 0（不限）
    WriteTimeout    time.Duration // 默认 10s
    IdleTimeout     time.Duration // 默认 0（不限，HeartbeatPlugin 控制）
    HandlerTimeout  time.Duration // 默认 0（不限）
    DrainTimeout    time.Duration // 默认 5s

    // 错误容忍
    MaxConsecutiveErrors int // 默认 100，超限断连

    // WorkerPool
    WorkerPoolOptions WorkerPoolOptions
}

type WorkerPoolOptions struct {
    WorkerCount     int           // 默认 NumCPU*2
    MaxWorkers      int           // 默认 WorkerCount*4
    TaskQueueSize   int           // 默认 WorkerCount*128
    QueueFullPolicy QueuePolicy   // 默认 PolicyDrop
    HandlerTimeout  time.Duration // 默认 0（不限制）
    OverloadWindow  time.Duration // 默认 30s（PolicyClose 使用）
}
```

### 6.2 Server Functional Options

```go
type Option func(*Options)

func WithAddr(addr string) Option {
    return func(o *Options) { o.Addr = addr }
}

func WithTLS(config *tls.Config) Option {
    return func(o *Options) { o.TLSConfig = config }
}

func WithFramer(framer Framer) Option {
    return func(o *Options) { o.Framer = framer }
}

func WithMaxSessions(max int64) Option {
    return func(o *Options) { o.MaxSessions = max }
}

func WithMaxMessageSize(size int) Option {
    return func(o *Options) { o.MaxMessageSize = size }
}

func WithWriteQueueSize(size int) Option {
    return func(o *Options) { o.WriteQueueSize = size }
}

func WithReadTimeout(d time.Duration) Option {
    return func(o *Options) { o.ReadTimeout = d }
}

func WithWriteTimeout(d time.Duration) Option {
    return func(o *Options) { o.WriteTimeout = d }
}

func WithDrainTimeout(d time.Duration) Option {
    return func(o *Options) { o.DrainTimeout = d }
}

func WithWorkerCount(count int) Option {
    return func(o *Options) { o.WorkerPoolOptions.WorkerCount = count }
}

func WithQueueFullPolicy(policy QueuePolicy) Option {
    return func(o *Options) { o.WorkerPoolOptions.QueueFullPolicy = policy }
}

// NewServer 构造函数
func NewServer(handler core.Handler, opts ...Option) *Server {
    options := defaultOptions()
    for _, opt := range opts {
        opt(&options)
    }
    return &Server{
        options: options,
        handler: handler,
    }
}

func defaultOptions() Options {
    workerCount := runtime.NumCPU() * 2
    return Options{
        Addr:                 "0.0.0.0:18000",
        Framer:               &LengthPrefixFramer{MaxFrameSize: 1 << 20},
        MaxSessions:          100000,
        MaxMessageSize:       1 << 20,
        WriteQueueSize:       128,
        WriteTimeout:         10 * time.Second,
        DrainTimeout:         5 * time.Second,
        MaxConsecutiveErrors: 100,
        WorkerPoolOptions: WorkerPoolOptions{
            WorkerCount:     workerCount,
            MaxWorkers:      workerCount * 4,
            TaskQueueSize:   workerCount * 128,
            QueueFullPolicy: PolicyDrop,
            OverloadWindow:  30 * time.Second,
        },
    }
}
```

---

# TRANSPORT.md — Part 2/5: UDP

> Shark-Socket 传输层：UDP 协议实现细节  
> 版本：v0.1.0  
> 最后更新：2026-06-01

---

## 目录（UDP 部分）

1. [UDP 协议概述](#1-udp-协议概述)
2. [伪会话模型](#2-伪会话模型)
3. [Server 实现](#3-server-实现)
4. [Options 配置](#4-options-配置)
5. [数据流](#5-数据流)
6. [与 CoAP 的关系](#6-与-coap-的关系)

---

## 1. UDP 协议概述

UDP 是无连接的传输协议，Shark-Socket 在其上实现了**伪会话（pseudo-session）**模型：

- 每个唯一的远端地址 `(*net.UDPAddr)` 对应一个伪会话
- 伪会话具有完整的 `core.Session` 生命周期（Register → Active → Closed）
- 通过 TTL 和定时清扫机制自动淘汰不活跃的伪会话

**与 TCP 的关键区别：**

| 特性 | TCP | UDP |
|------|-----|-----|
| 连接模型 | 有连接，per-conn goroutine | 无连接，单 conn + 远端地址映射 |
| Session 创建时机 | Accept 时 | 首次收到数据报时 |
| Session 销毁时机 | 连接关闭 | TTL 超时或主动关闭 |
| Framer | 可插拔（LengthPrefix/Line/FixedSize/Raw） | 无（原始数据报） |
| 写入方式 | 异步写队列 + writeLoop | 直接 `WriteToUDP` |
| 并发模型 | per-connection goroutine | 单 `readLoop` + 单 `sweepLoop` |

---

## 2. 伪会话模型

### 2.1 Session 结构

```go
type session struct {
    id        uint64
    conn      *net.UDPConn      // 共享的 UDP 连接
    remote    *net.UDPAddr      // 远端地址（会话标识）
    local     net.Addr          // 本地地址
    createdAt time.Time
    activeAt  atomic.Int64      // 最后活跃时间（UnixNano）
    state     atomic.Uint32     // SessionState
    meta      sync.Map          // 元数据
    ctx       context.Context
    cancel    context.CancelFunc
    closeOnce sync.Once
}
```

**关键设计：**
- `activeAt` 使用 `atomic.Int64` 存储 `time.Time.UnixNano()`，避免锁竞争
- `touch()` 在每次收到数据报时调用，更新活跃时间
- `Close()` 使用 `sync.Once` 保证幂等
- `Send()` 直接调用 `conn.WriteToUDP()`，无需写队列（UDP 无背压）

### 2.2 Session 创建（getOrCreateSession）

```
收到 UDP 数据报
    ↓
提取 remoteAddr 作为 key
    ↓
sessions.Load(key) → 命中？→ 返回已有 session，touch()
    ↓ 未命中
NextID() → newSession()
    ↓
sessions.LoadOrStore(key, sess) → 竞态保护
    ↓ 成功存储
Register(sess) → SessionManager
    ↓
Plugins.OnAccept(sess) → 插件链
    ↓
返回 session
```

**竞态安全：** `LoadOrStore` 保证同一远端地址只创建一个 session。若两个 goroutine 同时为同一地址创建 session，后到者关闭多余的 session。

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
    // 1. 解析并监听 UDP 地址
    addr, err := net.ResolveUDPAddr("udp", s.opts.Addr)
    conn, err := net.ListenUDP("udp", addr)
    s.conn = conn

    // 2. 创建可取消的运行上下文
    runCtx, cancel := context.WithCancel(ctx)
    s.cancel = cancel

    // 3. 启动两个 goroutine
    s.wg.Add(2)
    go s.readLoop(runCtx)   // 读取数据报 → 分发
    go s.sweepLoop(runCtx)  // 定期清理过期会话
    return nil
}
```

### 3.3 readLoop

```go
func (s *Server) readLoop(ctx context.Context) {
    defer s.wg.Done()
    buf := make([]byte, s.opts.MaxDatagram)  // 默认 64KB
    for {
        n, addr, err := s.conn.ReadFromUDP(buf)
        if err != nil {
            if s.closed.Load() || ctx.Err() != nil {
                return
            }
            continue
        }
        payload := append([]byte(nil), buf[:n]...)  // 复制数据报
        sess := s.getOrCreateSession(addr)
        if sess == nil {
            continue
        }
        sess.touch()  // 更新活跃时间
        // 插件链处理
        payload, err = s.rt.Plugins().OnMessage(sess, payload)
        if err != nil {
            if err != core.ErrPluginDrop {
                _ = sess.Close(context.Background())
            }
            continue
        }
        // 调用 Handler
        if s.opts.Handler != nil {
            msg := core.Message{
                SessionID: sess.ID(),
                Protocol:  core.ProtocolUDP,
                Payload:   payload,
            }
            if err := s.opts.Handler(sess, msg); err != nil {
                _ = sess.Close(context.Background())
            }
        }
    }
}
```

**设计要点：**
- 数据报复制（`append([]byte(nil), buf[:n]...)`）：避免下次读取覆盖数据
- Handler 错误导致 session 关闭：UDP 无重传，单次处理失败即清理
- `ErrPluginDrop` 不关闭 session：限流丢弃是正常控制流

### 3.4 sweepLoop

```go
func (s *Server) sweepLoop(ctx context.Context) {
    defer s.wg.Done()
    ticker := time.NewTicker(s.opts.SweepInterval)  // 默认 30s
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

**TTL 机制：**
- 每个 `SweepInterval`（默认 30s）扫描所有伪会话
- `LastActiveAt() + SessionTTL < now` 的会话被关闭并注销
- 默认 SessionTTL 为 2 分钟，适用于 IoT 设备心跳间隔

### 3.5 StagedServer 实现

| 阶段 | 行为 |
|------|------|
| StopAccept | CAS 标记关闭 → cancel 上下文 → 关闭 UDP conn |
| Drain | WaitGroup 等待 readLoop 和 sweepLoop 退出 |
| CloseSessions | Range 遍历所有 session，调用 closeSession |

`closeSession` 执行完整清理：

```go
func (s *Server) closeSession(ctx context.Context, key string, sess *session) {
    s.sessions.Delete(key)
    s.rt.Sessions().Unregister(sess.ID())
    _ = sess.Close(ctx)
    s.rt.Plugins().OnClose(sess)
}
```

---

## 4. Options 配置

```go
type Options struct {
    Addr          string        // 监听地址，默认 "127.0.0.1:18200"
    Handler       core.Handler  // 消息处理函数
    SessionTTL    time.Duration // 伪会话 TTL，默认 2 分钟
    SweepInterval time.Duration // 清扫间隔，默认 30 秒
    MaxDatagram   int           // 最大数据报大小，默认 64KB
}
```

**Functional Options：**

| Option | 默认值 | 说明 |
|--------|--------|------|
| `WithAddr(addr)` | `127.0.0.1:18200` | UDP 监听地址 |
| `WithHandler(handler)` | nil | 消息处理回调 |
| `WithSessionTTL(ttl)` | `2m` | 伪会话空闲超时 |
| `WithSweepInterval(interval)` | `30s` | 清扫周期 |
| `WithMaxDatagram(size)` | `64KB` | 单数据报最大字节数 |

**配置建议：**

| 场景 | SessionTTL | SweepInterval | MaxDatagram |
|------|-----------|---------------|-------------|
| IoT 设备（心跳 60s） | 3m | 30s | 64KB |
| 实时游戏（高频 UDP） | 30s | 10s | 1KB |
| DNS 服务（无状态） | 10s | 5s | 512B |

---

## 5. 数据流

```
客户端发送 UDP 数据报
    ↓
Server.readLoop() 接收
    ↓
getOrCreateSession(remoteAddr)
    ├── 已有 → touch() + 返回
    └── 新建 → Register → OnAccept → 返回
    ↓
sess.touch() 更新活跃时间
    ↓
Plugins.OnMessage(sess, payload)
    ├── ErrPluginDrop → 丢弃（不关闭 session）
    ├── ErrPluginBlock → sess.Close()
    └── 正常 → payload 继续
    ↓
Handler(sess, msg)
    ├── err != nil → sess.Close()
    └── 正常 → Handler 内调用 sess.Send() 响应
    ↓
[后台] sweepLoop 定期检查
    └── LastActiveAt + TTL < now → closeSession()
```

---

## 6. 与 CoAP 的关系

CoAP（Constrained Application Protocol）是基于 UDP 的应用层协议。在 Shark-Socket 中：

| 层级 | 实现 | 位置 |
|------|------|------|
| 传输层 | CoAP Server（UDP 基础 + 帧解析 + CON/ACK） | `internal/transport/coap/` |
| 应用层 | LwM2M Server/Client（CoAP Responder 适配） | `internal/protocol/lwm2m/` |

**UDP Server 是 CoAP 的基础**，但 CoAP Server 有独立的实现（包含 CoAP 帧解析、CON/ACK 确认、MessageID 去重等），不复用 UDP Server。两者的 session 模型相同（伪会话 + TTL）。

详见 CoAP 部分（Part 3）和 PROTOCOL.md。

---

# TRANSPORT.md — Part 3/5: CoAP

> Shark-Socket 传输层：CoAP 协议实现细节  
> 版本：v0.1.0  
> 最后更新：2026-06-01

---

## 目录（CoAP 部分）

1. [CoAP 协议概述](#1-coap-协议概述)
2. [CoAP 帧结构](#2-coap-帧结构)
3. [CoAPSession](#3-coapsession)
4. [CoAPServer](#4-coapserver)
5. [配置项完整参考](#5-配置项完整参考)

---

## 1. CoAP 协议概述

### 1.1 在框架中的定位

CoAP（Constrained Application Protocol，RFC 7252）是**面向受限设备的应用层协议**，基于 UDP 传输。

**CoAP 在框架分层中的位置：**

```
┌─────────────────────────────────────┐
│ protocol/lwm2m/                     │  应用协议层（LwM2M 语义）
│   基于 CoAP transport 提供设备管理   │
├─────────────────────────────────────┤
│ transport/coap/                     │  传输层（本文档）
│   CoAP 帧解析、CON/ACK、去重        │
├─────────────────────────────────────┤
│ transport/udp/                      │  网络层（UDP 数据报）
│   单 conn + 伪会话模型               │
└─────────────────────────────────────┘
```

**CoAP 与 UDP 的关系（ADR-009 决策）：**

- CoAP Server **独立于** UDP Server，不复用 UDP Server 的伪会话模型
- CoAP Server 内部创建自己的 `*net.UDPConn`，自行管理 CoAP 级别的伪会话
- CoAP 伪会话携带额外状态（`pendingACKs`、`messageCache`），不能直接复用 UDP 伪会话
- LwM2M 基于 CoAP transport，通过 `responder.go` 接入（详见 `PROTOCOL.md`）

### 1.2 CoAP 消息类型

| 类型 | 值 | 说明 |
|------|---|------|
| CON（Confirmable） | 0 | 可靠消息，对端必须回 ACK |
| NON（Non-confirmable） | 1 | 不可靠消息，无需 ACK |
| ACK（Acknowledgement） | 2 | 对 CON 的确认 |
| RST（Reset） | 3 | 拒绝或无法处理，取消重传 |

### 1.3 P0 实现范围

| 功能 | P0（当前） | P1（后续） |
|------|-----------|-----------|
| CON 基础 ACK | ✓ | - |
| NON 消息处理 | ✓ | - |
| MessageID 去重 | ✓ | - |
| retransmitLoop | ✓ | - |
| Block-wise 传输 | ✗ | ✓ |
| Observe/Notify | ✗ | ✓ |
| DTLS | ✗ | ✓ |
| 完整 Option 编解码 | 部分（Uri-Path/Content-Format） | 完整 |

### 1.4 文件清单

```
internal/transport/coap/
├── message.go   # CoAP 帧结构、解析、序列化
├── session.go   # CoAPSession：伪会话 + pendingACK + messageCache
├── server.go    # CoAPServer：readLoop + ACK 响应 + retransmitLoop
└── options.go   # CoAPOption Functional Options
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

// MsgType 是 CoAP 消息类型。
type MsgType uint8

const (
    MsgTypeCON MsgType = 0 // Confirmable
    MsgTypeNON MsgType = 1 // Non-confirmable
    MsgTypeACK MsgType = 2 // Acknowledgement
    MsgTypeRST MsgType = 3 // Reset
)

// Code 是 CoAP 方法码或响应码。
// 格式：c.dd（class.detail），编码为 uint8：(c << 5) | dd
type Code uint8

const (
    // 方法码（请求）
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

// OptionNumber 是 CoAP Option 编号。
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

// Option 是 CoAP 选项。
type Option struct {
    Number OptionNumber
    Value  []byte
}

// Message 是 CoAP 消息结构。
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
// Parse 从字节切片解析 CoAP 消息。
func Parse(data []byte) (*Message, error) {
    if len(data) < 4 {
        return nil, fmt.Errorf("%w: message too short (%d bytes)",
            core.ErrCoAPInvalidMessage, len(data))
    }

    version := (data[0] >> 6) & 0x03
    if version != 1 {
        return nil, fmt.Errorf("%w: unsupported version %d",
            core.ErrCoAPInvalidMessage, version)
    }

    msgType := MsgType((data[0] >> 4) & 0x03)
    tokenLen := int(data[0] & 0x0F)

    if tokenLen > 8 {
        return nil, fmt.Errorf("%w: token length %d exceeds maximum 8",
            core.ErrCoAPInvalidMessage, tokenLen)
    }

    code := Code(data[1])
    messageID := binary.BigEndian.Uint16(data[2:4])

    offset := 4
    if len(data) < offset+tokenLen {
        return nil, fmt.Errorf("%w: token length %d exceeds data length",
            core.ErrCoAPInvalidMessage, tokenLen)
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

// parseOptions 解析 CoAP Options（delta 编码）。
func parseOptions(data []byte, offset int) ([]Option, int, error) {
    var options []Option
    currentOptionNumber := OptionNumber(0)

    for offset < len(data) {
        if data[offset] == 0xFF {
            // Payload Marker
            offset++ // 跳过 0xFF，后续是 payload
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
                core.ErrCoAPInvalidMessage)
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
                core.ErrCoAPInvalidMessage)
        }
        return int(data[offset]) + 13, offset + 1, nil
    case 14:
        if offset+1 >= len(data) {
            return 0, 0, fmt.Errorf("%w: option delta/length out of bounds",
                core.ErrCoAPInvalidMessage)
        }
        val := int(binary.BigEndian.Uint16(data[offset:offset+2])) + 269
        return val, offset + 2, nil
    case 15:
        return 0, 0, fmt.Errorf("%w: reserved option delta/length value 15",
            core.ErrCoAPInvalidMessage)
    default:
        return int(nibble), offset, nil
    }
}
```

### 2.3 帧序列化

```go
// Marshal 将 CoAP 消息序列化为字节切片。
func (m *Message) Marshal() ([]byte, error) {
    if len(m.Token) > 8 {
        return nil, fmt.Errorf("%w: token length %d exceeds 8",
            core.ErrCoAPInvalidMessage, len(m.Token))
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

    // Options（delta 编码）
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
    // 按 Option Number 升序排序（CoAP 规范要求）
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
// GetUriPath 提取所有 Uri-Path Option 值，拼接为路径字符串。
func (m *Message) GetUriPath() string {
    var parts []string
    for _, opt := range m.Options {
        if opt.Number == OptionUriPath {
            parts = append(parts, string(opt.Value))
        }
    }
    return "/" + strings.Join(parts, "/")
}

// GetUriQuery 提取所有 Uri-Query Option 值。
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

// GetContentFormat 提取 Content-Format Option 值。
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

// NewACK 构造对 CON 消息的 ACK 响应。
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

// NewNON 构造 NON 消息。
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

// SelectACKCode 根据请求方法码选择合适的 ACK 响应码。
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

// pendingACK 跟踪一个等待确认的 CON 消息。
type pendingACK struct {
    messageID  uint16
    ackData    []byte        // 已发送的 ACK 字节，用于重传
    sentAt     time.Time     // 首次发送时间
    lastSentAt time.Time     // 最后发送时间（重传更新）
    attempts   int           // 已重传次数
}
```

### 3.2 messageCache 结构

```go
// messageCache 是 MessageID 去重缓存（环形淘汰）。
type messageCache struct {
    mu       sync.Mutex
    cache    map[uint16][]byte // messageID → 已发送的 ACK 字节
    order    []uint16          // 插入顺序（用于淘汰最旧条目）
    maxSize  int               // 最大缓存条目数
}

func newMessageCache(maxSize int) *messageCache {
    return &messageCache{
        cache:   make(map[uint16][]byte, maxSize),
        order:   make([]uint16, 0, maxSize),
        maxSize: maxSize,
    }
}

// CheckAndRecord 检查 MessageID 是否重复。
// 返回值：(cachedACK []byte, isDuplicate bool)
// 若重复，返回缓存的 ACK 字节；若首次，记录占位（nil），等待 CacheResponse 填充。
func (c *messageCache) CheckAndRecord(messageID uint16) ([]byte, bool) {
    c.mu.Lock()
    defer c.mu.Unlock()

    if ack, exists := c.cache[messageID]; exists {
        return ack, true // 重复消息
    }

    // 环形淘汰：超过 maxSize 时删除最旧条目
    if len(c.cache) >= c.maxSize {
        oldest := c.order[0]
        c.order = c.order[1:]
        delete(c.cache, oldest)
    }

    // 记录占位（nil 表示 Handler 尚未处理完）
    c.cache[messageID] = nil
    c.order = append(c.order, messageID)
    return nil, false
}

// CacheResponse 填充 MessageID 对应的 ACK 响应（Handler 处理完成后调用）。
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
    // 身份（不可变）
    id         uint64
    remoteAddr *net.UDPAddr
    localAddr  net.Addr
    createdAt  time.Time

    // 网络（共享单 conn）
    conn *net.UDPConn

    // CoAP 状态
    messageCache  *messageCache
    pendingACKs   map[uint16]*pendingACK // 等待 ACK 的 CON 消息（由 retransmitLoop 管理）
    pendingMu     sync.Mutex             // 保护 pendingACKs

    // 活跃 MessageID 索引（避免 retransmitLoop 自锁死锁）
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

### 3.4 retransmitLoop 死锁问题与解决方案（ADR-003 修复）

**错误方案（导致死锁）：**

```
retransmitLoop 执行：
  pendingMu.Lock()              ← 获取锁，遍历 pendingACKs
  for id, pending := range pendingACKs：
    if 超时：
      delete(pendingACKs, id)   ← 在持锁状态下删除，正常
      conn.WriteToUDP(...)      ← 发送重传
  pendingMu.Unlock()

问题：若重传触发 OnMessage，OnMessage 内部可能调用：
  session.TrackCON(msgID, ackData)  ← 需要 pendingMu.Lock() → 死锁！
```

**正确方案（活跃索引）：**

```go
// retransmitLoop 只读 activeIDs 索引，不持 pendingMu 遍历
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
    // 步骤1：读取当前活跃 ID 列表（持 activeIDsMu 短暂复制）
    s.activeIDsMu.Lock()
    ids := make([]uint16, len(s.activeIDs))
    copy(ids, s.activeIDs)
    s.activeIDsMu.Unlock()

    now := time.Now()
    var expiredIDs []uint16

    for _, id := range ids {
        // 步骤2：获取 pending 状态（持 pendingMu 短暂读取）
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
            // 超过最大重传次数，放弃
            expiredIDs = append(expiredIDs, id)
            s.logger.Warn("coap retransmit max exceeded",
                "session_id", s.id,
                "message_id", id,
                "attempts", attempts)
            continue
        }

        if shouldRetransmit {
            // 步骤3：发送重传（不持锁）
            s.mu.Lock()
            _, err := s.conn.WriteToUDP(ackData, s.remoteAddr)
            s.mu.Unlock()

            if err != nil {
                s.logger.Error("coap retransmit error",
                    "session_id", s.id,
                    "message_id", id,
                    "error", err)
            }

            // 步骤4：更新重传状态（持 pendingMu 短暂更新）
            s.pendingMu.Lock()
            if p, ok := s.pendingACKs[id]; ok {
                p.lastSentAt = now
                p.attempts++
            }
            s.pendingMu.Unlock()

            s.metrics.Counter("shark_transport_errors_total",
                "protocol", "coap", "type", "retransmit").Inc()
        }
    }

    // 步骤5：清理过期 ID
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
// TrackCON 记录已发送的 ACK，供 retransmitLoop 重传使用。
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

// ResetCON 收到 RST 时取消对应 MessageID 的重传。
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
        s.metrics.Counter("shark_transport_errors_total",
            "protocol", "coap", "type", "write").Inc()
        return err
    }

    s.metrics.Counter("shark_messages_total",
        "protocol", "coap", "direction", "out").Inc()
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

        s.metrics.Gauge("shark_sessions_active", "protocol", "coap").Dec()
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
    options Options
    conn    *net.UDPConn
    handler core.Handler

    // 伪会话存储（remoteAddr.String() → *Session）
    sessions sync.Map

    // Runtime（通过 UseRuntime 注入）
    runtime core.Runtime

    // 并发控制
    closed atomic.Bool
    stopCh chan struct{}
    wg     sync.WaitGroup
}

// 编译期验证
var _ core.Server              = (*Server)(nil)
var _ core.RuntimeConfigurable = (*Server)(nil)
```

### 4.2 Start 实现

```go
func (s *Server) Start(ctx context.Context) error {
    addr, err := net.ResolveUDPAddr("udp", s.options.Addr)
    if err != nil {
        return fmt.Errorf("%w: %v", core.ErrListenFailed, err)
    }

    conn, err := net.ListenUDP("udp", addr)
    if err != nil {
        return fmt.Errorf("%w: %v", core.ErrListenFailed, err)
    }

    s.conn   = conn
    s.stopCh = make(chan struct{})

    s.runtime.Logger().Info("coap server started",
        "protocol", "coap",
        "addr", s.options.Addr)

    s.wg.Add(2)
    go s.readLoop()
    go s.sweepLoop()

    return nil
}
```

### 4.3 readLoop 实现

```go
func (s *Server) readLoop() {
    defer s.wg.Done()

    logger  := s.runtime.Logger()
    metrics := s.runtime.Metrics()
    manager := s.runtime.Sessions()
    plugins := s.runtime.Plugins()

    readBuffer := make([]byte, s.options.MaxMessageSize)

    for {
        select {
        case <-s.stopCh:
            return
        default:
        }

        s.conn.SetReadDeadline(time.Now().Add(1 * time.Second))

        n, remoteAddr, err := s.conn.ReadFromUDP(readBuffer)
        if err != nil {
            if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
                continue
            }
            if errors.Is(err, net.ErrClosed) {
                return
            }
            logger.Error("coap read error", "error", err)
            continue
        }

        if n < 4 {
            metrics.Counter("shark_transport_errors_total",
                "protocol", "coap", "type", "invalid_frame").Inc()
            continue
        }

        // 复制 datagram
        datagram := make([]byte, n)
        copy(datagram, readBuffer[:n])

        s.handleDatagram(datagram, remoteAddr, manager, plugins, logger, metrics)
    }
}
```

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
    // 解析 CoAP 消息（帧验证）
    msg, err := Parse(datagram)
    if err != nil {
        metrics.Counter("shark_transport_errors_total",
            "protocol", "coap", "type", "parse_error").Inc()
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
        // 收到 ACK，停止重传
        sess.ResetCON(msg.MessageID)
    case MsgTypeRST:
        // 收到 RST，取消重传
        sess.ResetCON(msg.MessageID)
        logger.Debug("coap RST received",
            "session_id", sess.ID(),
            "message_id", msg.MessageID)
    }
}
```

### 4.5 handleCON 实现（CON 可靠性，RFC 7252 §4.2）

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
            // 重复 CON + 已有缓存响应 → 重发缓存 ACK（不重复执行 Handler）
            sess.Send(cachedACK)
            metrics.Counter("shark_transport_errors_total",
                "protocol", "coap", "type", "duplicate_con").Inc()
            logger.Debug("coap duplicate CON, resending cached ACK",
                "session_id", sess.ID(),
                "message_id", msg.MessageID)
        }
        // cachedACK == nil 说明 Handler 仍在处理中，忽略重复 CON
        return
    }

    // 首次 CON：插件链处理
    data, err := plugins.RunMessage(sess, msg.Payload)
    if err != nil {
        if errors.Is(err, core.ErrPluginDrop) {
            // 丢弃消息，但仍需发送 ACK（否则对端会一直重传）
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

    // 缓存 ACK 响应（供去重时重发）
    sess.messageCache.CacheResponse(msg.MessageID, ackData)

    // 跟踪 CON（若对端需要重传确认，此处暂不跟踪服务端发出的 ACK）
    // 注：RFC 7252 服务端发 ACK 后对端负责确认收到，服务端无需重传 ACK
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
    // NON 消息无需 ACK，直接处理
    data, err := plugins.RunMessage(sess, msg.Payload)
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

### 4.7 getOrCreateSession 实现

```go
func (s *Server) getOrCreateSession(
    addrKey string,
    remoteAddr *net.UDPAddr,
    manager core.SessionManager,
    plugins core.PluginRunner,
    logger core.Logger,
    metrics core.Metrics,
) (*Session, error) {
    // 查找已有伪会话
    if val, exists := s.sessions.Load(addrKey); exists {
        sess := val.(*Session)
        if sess.IsAlive() {
            return sess, nil
        }
        s.sessions.Delete(addrKey)
    }

    // 创建新伪会话
    sessionID := manager.NextID()
    tempSess := newSession(sessionID, s.conn, remoteAddr,
        s.options.MessageIDCacheSize, logger, metrics)

    // OnAccept
    if err := plugins.RunAccept(tempSess); err != nil {
        if errors.Is(err, core.ErrPluginBlock) {
            metrics.Counter("shark_rejected_connections_total",
                "protocol", "coap", "reason", "plugin_block").Inc()
            return nil, err
        }
        logger.Warn("coap OnAccept error", "error", err)
    }

    // 注册
    if err := manager.Register(tempSess); err != nil {
        logger.Warn("coap session register failed", "error", err)
        return nil, err
    }

    actual, loaded := s.sessions.LoadOrStore(addrKey, tempSess)
    if loaded {
        manager.Unregister(sessionID)
        return actual.(*Session), nil
    }

    // 启动 retransmitLoop
    s.wg.Add(1)
    go func() {
        defer s.wg.Done()
        tempSess.retransmitLoop(
            s.options.AckTimeout,
            s.options.MaxRetransmit,
        )
    }()

    metrics.Counter("shark_sessions_total", "protocol", "coap").Inc()
    metrics.Gauge("shark_sessions_active", "protocol", "coap").Inc()
    logger.Info("coap session created",
        "session_id", sessionID,
        "remote_addr", addrKey)

    return tempSess, nil
}
```

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
            s.runtime.Plugins().RunClose(sess)
            sess.Close(context.Background())
        }
        return true
    })
}

func (s *Server) closeSession(sess *Session) {
    addrKey := sess.RemoteAddr().String()
    s.sessions.Delete(addrKey)
    s.runtime.Sessions().Unregister(sess.ID())
    s.runtime.Plugins().RunClose(sess)
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
        plugins.RunClose(sess)
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
    // 网络
    Addr           string // 默认 "0.0.0.0:5683"（CoAP 标准端口）
    ReadBufferSize int    // 默认 2MB

    // 消息限制
    MaxMessageSize int   // 默认 65535
    MaxSessions    int64 // 默认 100000

    // CON 可靠性（RFC 7252 §4.8）
    AckTimeout    time.Duration // 默认 2s（首次 ACK 超时）
    MaxRetransmit int           // 默认 4（最大重传次数）

    // 去重缓存
    MessageIDCacheSize int // 默认 500（最近 N 条 MessageID）

    // 伪会话 TTL
    SessionTTL    time.Duration // 默认 5m
    SweepInterval time.Duration // 默认 30s
}
```

### 5.2 默认值与 Functional Options

```go
func defaultOptions() Options {
    return Options{
        Addr:               "0.0.0.0:5683",
        ReadBufferSize:     2 * 1024 * 1024,
        MaxMessageSize:     65535,
        MaxSessions:        100000,
        AckTimeout:         2 * time.Second,
        MaxRetransmit:      4,
        MessageIDCacheSize: 500,
        SessionTTL:         5 * time.Minute,
        SweepInterval:      30 * time.Second,
    }
}

type Option func(*Options)

func WithAddr(addr string) Option {
    return func(o *Options) { o.Addr = addr }
}

func WithAckTimeout(d time.Duration) Option {
    return func(o *Options) { o.AckTimeout = d }
}

func WithMaxRetransmit(n int) Option {
    return func(o *Options) { o.MaxRetransmit = n }
}

func WithMessageIDCacheSize(size int) Option {
    return func(o *Options) { o.MessageIDCacheSize = size }
}

func WithSessionTTL(ttl time.Duration) Option {
    return func(o *Options) { o.SessionTTL = ttl }
}

func WithMaxSessions(max int64) Option {
    return func(o *Options) { o.MaxSessions = max }
}

func NewServer(handler core.Handler, opts ...Option) *Server {
    options := defaultOptions()
    for _, opt := range opts {
        opt(&options)
    }
    return &Server{
        options: options,
        handler: handler,
        stopCh:  make(chan struct{}),
    }
}
```

# TRANSPORT.md — Part 4/5: WebSocket

> Shark-Socket 传输层：WebSocket 协议实现细节  
> 版本：v0.1.0  
> 最后更新：2026-06-01

---

## 目录（WebSocket 部分）

1. [WebSocket 协议概述](#1-websocket-协议概述)
2. [WSSession](#2-wssession)
3. [WSServer](#3-wsserver)
4. [配置项完整参考](#4-配置项完整参考)

---

## 1. WebSocket 协议概述

### 1.1 在框架中的定位

WebSocket 是**基于 HTTP Upgrade 的全双工长连接协议**，与 TCP 的核心差异：

| 维度 | TCP | WebSocket |
|------|-----|-----------|
| 连接建立 | `net.Listen` + `Accept` | HTTP Upgrade（`gorilla/websocket` 处理） |
| 帧边界 | 需要 Framer 处理粘包 | 协议内置消息边界（Text/Binary/Control 帧） |
| 写并发安全 | writeLoop 单 goroutine 独占 | `writeMu sync.Mutex`（gorilla 要求） |
| 心跳机制 | 应用层自定义（HeartbeatPlugin） | 协议内置 Ping/Pong 控制帧 |
| 关闭流程 | 六步状态机（drain 写队列） | 发送 Close 帧 + 等待对端 Close 帧 |
| Goroutine 数量 | 每连接 2 个 | 每连接 2 个（handleSession + pingLoop） |

### 1.2 gorilla/websocket 并发约束

`gorilla/websocket` 库的**核心约束**（必须严格遵守）：

```
并发读：不允许（同一连接同时只能有一个 Read 调用）
并发写：不允许（同一连接同时只能有一个 Write 调用）

框架处理方式：
  读：handleSession goroutine 独占 ReadMessage（天然串行）
  写：所有写操作（Send / SendText / sendPing）加 writeMu 互斥锁
```

### 1.3 OnClose 单次执行保证

WebSocket 有两条并发退出路径：

```
路径1：Gateway.Stop() → StagedServer.CloseSessions() → sess.Close()
路径2：handleSession readLoop → conn.ReadMessage() 返回 EOF → sess.Close()

两条路径都调用 sess.Close()
→ closeOnce.Do() 保证内部逻辑只执行一次
→ RunClose(sess) 在 Do() 内部，天然单次执行
```

### 1.4 文件清单

```
internal/transport/websocket/
├── session.go   # WSSession：writeMu + Ping/Pong + OnClose 单次保证
├── server.go    # WSServer：Upgrade + handleSession + pingLoop + StagedServer
└── options.go   # WSOption Functional Options
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
    // 身份（不可变）
    id         uint64
    remoteAddr net.Addr
    localAddr  net.Addr
    createdAt  time.Time

    // 网络
    conn *websocket.Conn

    // 写并发保护（gorilla/websocket 要求）
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

### 2.4 Send 实现（Binary 消息）

```go
// Send 发送 Binary 消息。
// 必须加 writeMu，gorilla/websocket 写操作非并发安全。
func (s *Session) Send(data []byte) error {
    if !s.IsAlive() {
        return core.ErrSessionClosed
    }

    s.writeMu.Lock()
    defer s.writeMu.Unlock()

    if err := s.conn.WriteMessage(websocket.BinaryMessage, data); err != nil {
        s.metrics.Counter("shark_transport_errors_total",
            "protocol", "websocket", "type", "write").Inc()
        return err
    }

    s.metrics.Counter("shark_messages_total",
        "protocol", "websocket", "direction", "out").Inc()
    s.metrics.Counter("shark_message_bytes_total",
        "protocol", "websocket", "direction", "out").Add(float64(len(data)))

    return nil
}
```

### 2.5 SendText 实现（Text 消息）

```go
// SendText 发送 Text 消息（UTF-8 编码）。
func (s *Session) SendText(data []byte) error {
    if !s.IsAlive() {
        return core.ErrSessionClosed
    }

    s.writeMu.Lock()
    defer s.writeMu.Unlock()

    if err := s.conn.WriteMessage(websocket.TextMessage, data); err != nil {
        s.metrics.Counter("shark_transport_errors_total",
            "protocol", "websocket", "type", "write_text").Inc()
        return err
    }

    s.metrics.Counter("shark_messages_total",
        "protocol", "websocket", "direction", "out").Inc()
    return nil
}
```

### 2.6 sendPing 实现（内部心跳）

```go
// sendPing 发送 Ping 控制帧（内部方法，由 pingLoop 调用）。
func (s *Session) sendPing() error {
    s.writeMu.Lock()
    defer s.writeMu.Unlock()

    return s.conn.WriteMessage(websocket.PingMessage, nil)
}
```

**三个写方法的锁使用一致性：**

```
Send       → writeMu.Lock() → WriteMessage(BinaryMessage) → Unlock
SendText   → writeMu.Lock() → WriteMessage(TextMessage)   → Unlock
sendPing   → writeMu.Lock() → WriteMessage(PingMessage)   → Unlock
sendClose  → writeMu.Lock() → WriteMessage(CloseMessage)  → Unlock

所有写操作必须通过 writeMu，不允许绕过锁直接调用 conn.WriteMessage
```

### 2.7 Close 实现（OnClose 单次保证）

```go
// Close 关闭 WebSocket 会话。
// sync.Once 保证多条并发退出路径（Gateway Stop + readLoop EOF）只执行一次清理。
func (s *Session) Close(ctx context.Context) error {
    var closeErr error
    s.closeOnce.Do(func() {
        // 步骤1：标记状态
        s.state.Store(int32(core.Closed))

        // 步骤2：发送 Close 帧（通知对端）
        s.writeMu.Lock()
        s.conn.WriteMessage(
            websocket.CloseMessage,
            websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
        )
        s.writeMu.Unlock()

        // 步骤3：取消 context（通知 pingLoop 退出）
        s.cancel()

        // 步骤4：关闭底层连接
        closeErr = s.conn.Close()

        s.metrics.Gauge("shark_sessions_active", "protocol", "websocket").Dec()
        s.logger.Info("websocket session closed",
            "session_id", s.id,
            "remote_addr", s.remoteAddr.String())
    })
    return closeErr
}
```

**注意：** WebSocket 的 `Close` 不包含 drain 写队列步骤，因为：

```
WebSocket 无写队列：
  Send 通过 writeMu 同步写，不经过队列
  Close 发送 Close 帧后直接关闭连接
  对端收到 Close 帧后停止发送，连接双方协商关闭

与 TCP 的差异：
  TCP Close：drain 写队列（确保已入队消息全部发出）
  WebSocket Close：发送 Close 帧（协议层握手关闭）
```

---

## 3. WSServer

### 3.1 结构定义

```go
// internal/transport/websocket/server.go
type Server struct {
    options  Options
    upgrader websocket.Upgrader
    handler  core.Handler

    // HTTP Server（负责 Upgrade）
    httpServer *http.Server

    // Runtime（通过 UseRuntime 注入）
    runtime core.Runtime

    // 并发控制
    wg     sync.WaitGroup
    closed atomic.Bool

    // StagedServer 控制
    acceptStopped chan struct{} // StopAccept 后关闭，阻止新 Upgrade
}

// 编译期验证
var _ core.Server              = (*Server)(nil)
var _ core.RuntimeConfigurable = (*Server)(nil)
var _ core.StagedServer        = (*Server)(nil)
```

### 3.2 UseRuntime 与 Protocol

```go
func (s *Server) UseRuntime(rt core.Runtime) {
    s.runtime = rt
}

func (s *Server) Protocol() core.Protocol {
    return core.WebSocket
}
```

### 3.3 Start 实现

```go
func (s *Server) Start(ctx context.Context) error {
    s.acceptStopped = make(chan struct{})

    // 配置 Upgrader
    s.upgrader = websocket.Upgrader{
        ReadBufferSize:  s.options.ReadBufferSize,
        WriteBufferSize: s.options.WriteBufferSize,
        HandshakeTimeout: s.options.UpgradeTimeout,
        CheckOrigin: func(r *http.Request) bool {
            return s.checkOrigin(r)
        },
    }

    // 注册 WebSocket 路径
    mux := http.NewServeMux()
    mux.HandleFunc(s.options.Path, s.handleUpgrade)

    s.httpServer = &http.Server{
        Addr:    s.options.Addr,
        Handler: mux,
    }

    // 启动 HTTP Server（异步）
    startErr := make(chan error, 1)
    go func() {
        var err error
        if s.options.TLSConfig != nil {
            s.httpServer.TLSConfig = s.options.TLSConfig
            err = s.httpServer.ListenAndServeTLS("", "")
        } else {
            err = s.httpServer.ListenAndServe()
        }
        if err != nil && !errors.Is(err, http.ErrServerClosed) {
            startErr <- err
        }
    }()

    // 等待启动确认（短暂检查）
    select {
    case err := <-startErr:
        return fmt.Errorf("%w: %v", core.ErrListenFailed, err)
    case <-time.After(50 * time.Millisecond):
        // 启动成功（http.Server 未立即报错）
    }

    s.runtime.Logger().Info("websocket server started",
        "protocol", "websocket",
        "addr", s.options.Addr,
        "path", s.options.Path)

    return nil
}
```

### 3.4 Origin 检查

```go
// checkOrigin 检查请求来源是否在白名单中。
// AllowedOrigins 为空时拒绝所有跨域请求（生产环境必须配置）。
func (s *Server) checkOrigin(r *http.Request) bool {
    origin := r.Header.Get("Origin")

    // 无 Origin 头（非浏览器客户端）：允许
    if origin == "" {
        return true
    }

    // AllowedOrigins 为空：拒绝所有跨域
    if len(s.options.AllowedOrigins) == 0 {
        s.runtime.Logger().Warn("websocket origin rejected: AllowedOrigins not configured",
            "origin", origin,
            "remote_addr", r.RemoteAddr)
        return false
    }

    // 检查白名单
    for _, allowed := range s.options.AllowedOrigins {
        if allowed == "*" || allowed == origin {
            return true
        }
    }

    s.runtime.Logger().Warn("websocket origin rejected",
        "origin", origin,
        "allowed_origins", s.options.AllowedOrigins)
    return false
}
```

### 3.5 handleUpgrade 实现

```go
func (s *Server) handleUpgrade(w http.ResponseWriter, r *http.Request) {
    // 检查是否已停止接受（StopAccept 后拒绝新连接）
    select {
    case <-s.acceptStopped:
        http.Error(w, "service unavailable", http.StatusServiceUnavailable)
        return
    default:
    }

    manager := s.runtime.Sessions()
    plugins := s.runtime.Plugins()
    logger  := s.runtime.Logger()
    metrics := s.runtime.Metrics()

    // HTTP → WebSocket Upgrade
    conn, err := s.upgrader.Upgrade(w, r, nil)
    if err != nil {
        logger.Error("websocket upgrade failed",
            "remote_addr", r.RemoteAddr,
            "error", err)
        metrics.Counter("shark_session_errors_total",
            "protocol", "websocket",
            "reason", "upgrade_failed").Inc()
        return
    }

    // 设置消息大小限制
    conn.SetReadLimit(int64(s.options.MaxMessageSize))

    // 创建会话
    sessionID := manager.NextID()
    sess := newSession(sessionID, conn, logger, metrics)

    // 注册会话
    if err := manager.Register(sess); err != nil {
        logger.Warn("websocket session register failed",
            "error", err,
            "remote_addr", r.RemoteAddr)
        conn.Close()
        return
    }

    metrics.Counter("shark_sessions_total", "protocol", "websocket").Inc()
    metrics.Gauge("shark_sessions_active", "protocol", "websocket").Inc()

    // 插件链 OnAccept
    if err := plugins.RunAccept(sess); err != nil {
        // RunAccept 内部已调用 sess.Close()
        manager.Unregister(sessionID)
        return
    }

    // 转为 Active 状态
    sess.state.Store(int32(core.Active))
    logger.Info("websocket session accepted",
        "session_id", sessionID,
        "remote_addr", r.RemoteAddr,
        "origin", r.Header.Get("Origin"))

    // 启动会话处理（handleSession 包含 readLoop + pingLoop）
    s.wg.Add(1)
    go func() {
        defer s.wg.Done()
        s.handleSession(sess, plugins, logger)
    }()
}
```

### 3.6 handleSession 实现

```go
func (s *Server) handleSession(
    sess *Session,
    plugins core.PluginRunner,
    logger core.Logger,
) {
    manager := s.runtime.Sessions()

    // 退出时清理（保证 OnClose 单次执行）
    defer func() {
        drainCtx, cancel := context.WithTimeout(
            context.Background(), 5*time.Second)
        defer cancel()
        sess.Close(drainCtx)              // closeOnce 保证幂等
        plugins.RunClose(sess)            // closeOnce 内部已调用，此处不重复
        manager.Unregister(sess.ID())
    }()

    // 设置 Pong Handler（收到 Pong 时更新活跃时间和 ReadDeadline）
    sess.conn.SetPongHandler(func(appData string) error {
        sess.TouchActive()
        // 重设 ReadDeadline：PingInterval + PongTimeout
        deadline := time.Now().Add(
            s.options.PingInterval + s.options.PongTimeout)
        sess.conn.SetReadDeadline(deadline)
        return nil
    })

    // 设置初始 ReadDeadline
    sess.conn.SetReadDeadline(
        time.Now().Add(s.options.PingInterval + s.options.PongTimeout))

    // 启动 pingLoop
    s.wg.Add(1)
    go func() {
        defer s.wg.Done()
        s.pingLoop(sess)
    }()

    // readLoop（当前 goroutine，阻塞直到连接关闭）
    for {
        msgType, data, err := sess.conn.ReadMessage()
        if err != nil {
            if websocket.IsCloseError(err,
                websocket.CloseNormalClosure,
                websocket.CloseGoingAway,
                websocket.CloseNoStatusReceived,
            ) {
                logger.Info("websocket session closed by client",
                    "session_id", sess.ID())
            } else if !errors.Is(err, net.ErrClosed) {
                logger.Error("websocket read error",
                    "session_id", sess.ID(),
                    "error", err)
                s.runtime.Metrics().Counter("shark_transport_errors_total",
                    "protocol", "websocket", "type", "read").Inc()
            }
            return // 触发 defer 清理
        }

        // 仅处理 Text 和 Binary 消息（控制帧由 gorilla 内部处理）
        if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
            continue
        }

        sess.TouchActive()

        s.runtime.Metrics().Counter("shark_messages_total",
            "protocol", "websocket", "direction", "in").Inc()
        s.runtime.Metrics().Counter("shark_message_bytes_total",
            "protocol", "websocket", "direction", "in").Add(float64(len(data)))

        // 插件链处理
        processedData, err := plugins.RunMessage(sess, data)
        if err != nil {
            if errors.Is(err, core.ErrPluginDrop) {
                continue
            }
            if errors.Is(err, core.ErrPluginBlock) {
                return
            }
            logger.Error("websocket plugin error",
                "session_id", sess.ID(),
                "error", err)
            continue
        }

        // 调用 Handler
        msg := core.Message{
            SessionID: sess.ID(),
            Protocol:  core.WebSocket,
            Payload:   processedData,
        }
        if err := s.handler(sess, msg); err != nil {
            if core.IsFatal(err) {
                return
            }
            logger.Error("websocket handler error",
                "session_id", sess.ID(),
                "error", err)
        }
    }
}
```

**OnClose 执行时机说明：**

```
defer 中的清理顺序：
  1. sess.Close(drainCtx)
     → closeOnce.Do() 执行：
         state → Closed
         发送 Close 帧
         cancel()（通知 pingLoop 退出）
         conn.Close()

  2. plugins.RunClose(sess)
     → 逆序执行所有插件的 OnClose

  3. manager.Unregister(sess.ID())

注意：
  若 sess.Close() 已在 closeOnce.Do() 外部被调用过（如 Gateway.Stop()）
  则此处 sess.Close() 是幂等的（closeOnce 保证 Do 内逻辑不重复执行）
  但 plugins.RunClose 和 manager.Unregister 仍需在 defer 中执行
  因此 RunClose 不放在 closeOnce 内部，而是放在 defer 中显式调用
```

### 3.7 pingLoop 实现

```go
func (s *Server) pingLoop(sess *Session) {
    ticker := time.NewTicker(s.options.PingInterval)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            if !sess.IsAlive() {
                return
            }
            if err := sess.sendPing(); err != nil {
                s.runtime.Logger().Debug("websocket ping failed",
                    "session_id", sess.ID(),
                    "error", err)
                return
            }
            s.runtime.Logger().Debug("websocket ping sent",
                "session_id", sess.ID())

        case <-sess.Context().Done():
            // sess.Close() 调用 cancel() 后触发
            return
        }
    }
}
```

**ReadDeadline 动态设置时序：**

```
时间线：

T=0     建立连接，设置 ReadDeadline = T + PingInterval + PongTimeout
T=30s   pingLoop 发送 Ping
T=30s+  客户端收到 Ping，回复 Pong
T=30s+  PongHandler 收到 Pong：
          TouchActive()
          ReadDeadline = now + PingInterval + PongTimeout（重设）
T=60s   pingLoop 再次发送 Ping
...
T=45s   若客户端在 T=30s 后 15s（PongTimeout=15s）内未回 Pong：
          ReadDeadline 到期
          conn.ReadMessage() 返回 timeout 错误
          handleSession 退出 → sess.Close()

配置关系：
  PingInterval = 30s（Ping 发送间隔）
  PongTimeout  = 15s（等待 Pong 的超时）
  ReadDeadline = PingInterval + PongTimeout = 45s
  （设置为总超时，确保在两次 Ping 间有足够时间等待 Pong）
```

### 3.8 StagedServer 三阶段实现

```go
// StopAccept 停止接受新的 WebSocket 升级请求。
func (s *Server) StopAccept(ctx context.Context) error {
    if s.closed.CompareAndSwap(false, true) {
        close(s.acceptStopped) // 新 Upgrade 请求返回 503
    }
    s.runtime.Logger().Info("websocket server: stop accept done")
    return nil
}

// Drain 等待所有 handleSession + pingLoop goroutine 退出。
func (s *Server) Drain(ctx context.Context) error {
    done := make(chan struct{})
    go func() {
        s.wg.Wait()
        close(done)
    }()

    select {
    case <-done:
        s.runtime.Logger().Info("websocket server: drain done")
        return nil
    case <-ctx.Done():
        s.runtime.Logger().Warn("websocket server: drain timeout")
        return core.ErrDrainTimeout
    }
}

// CloseSessions 向所有活跃 WSSession 发送 Close 帧并关闭。
func (s *Server) CloseSessions(ctx context.Context) error {
    s.runtime.Sessions().Range(func(sess core.Session) bool {
        if sess.Protocol() == core.WebSocket {
            sess.Close(ctx)
        }
        return true
    })
    s.runtime.Logger().Info("websocket server: close sessions done")
    return nil
}

// Stop 完整停止（非 StagedServer 场景使用）。
func (s *Server) Stop(ctx context.Context) error {
    if err := s.StopAccept(ctx); err != nil {
        return err
    }

    // 关闭 HTTP Server（停止接受新的 HTTP 连接）
    if s.httpServer != nil {
        if err := s.httpServer.Shutdown(ctx); err != nil {
            s.runtime.Logger().Warn("websocket http server shutdown error",
                "error", err)
        }
    }

    if err := s.Drain(ctx); err != nil {
        return err
    }
    return s.CloseSessions(ctx)
}
```

---

## 4. 配置项完整参考

### 4.1 Options 定义

```go
// internal/transport/websocket/options.go
type Options struct {
    // 网络
    Addr      string      // 默认 "0.0.0.0:18700"
    Path      string      // 默认 "/ws"（WebSocket 升级路径）
    TLSConfig *tls.Config // 默认 nil（不启用 TLS）

    // 缓冲区
    ReadBufferSize  int // 默认 4096（gorilla Upgrader 读缓冲）
    WriteBufferSize int // 默认 4096（gorilla Upgrader 写缓冲）

    // 消息限制
    MaxMessageSize int   // 默认 1MB（conn.SetReadLimit 设置）
    MaxSessions    int64 // 默认 100000

    // 心跳
    PingInterval time.Duration // 默认 30s（Ping 发送间隔）
    PongTimeout  time.Duration // 默认 15s（等待 Pong 超时）

    // 安全
    AllowedOrigins  []string      // 默认 []（空=拒绝所有跨域，生产必须配置）
    UpgradeTimeout  time.Duration // 默认 5s（HTTP Upgrade 超时）
}
```

### 4.2 默认值

```go
func defaultOptions() Options {
    return Options{
        Addr:            "0.0.0.0:18700",
        Path:            "/ws",
        ReadBufferSize:  4096,
        WriteBufferSize: 4096,
        MaxMessageSize:  1 << 20, // 1MB
        MaxSessions:     100000,
        PingInterval:    30 * time.Second,
        PongTimeout:     15 * time.Second,
        AllowedOrigins:  []string{},
        UpgradeTimeout:  5 * time.Second,
    }
}
```

### 4.3 Functional Options

```go
type Option func(*Options)

func WithAddr(addr string) Option {
    return func(o *Options) { o.Addr = addr }
}

func WithPath(path string) Option {
    return func(o *Options) { o.Path = path }
}

func WithTLS(config *tls.Config) Option {
    return func(o *Options) { o.TLSConfig = config }
}

func WithMaxMessageSize(size int) Option {
    return func(o *Options) { o.MaxMessageSize = size }
}

func WithMaxSessions(max int64) Option {
    return func(o *Options) { o.MaxSessions = max }
}

func WithPingInterval(d time.Duration) Option {
    return func(o *Options) { o.PingInterval = d }
}

func WithPongTimeout(d time.Duration) Option {
    return func(o *Options) { o.PongTimeout = d }
}

// WithAllowedOrigins 设置允许的跨域来源。
// 生产环境必须明确配置，禁止使用 "*"（安全风险）。
func WithAllowedOrigins(origins ...string) Option {
    return func(o *Options) { o.AllowedOrigins = origins }
}

func WithUpgradeTimeout(d time.Duration) Option {
    return func(o *Options) { o.UpgradeTimeout = d }
}

// NewServer 构造函数
func NewServer(handler core.Handler, opts ...Option) *Server {
    options := defaultOptions()
    for _, opt := range opts {
        opt(&options)
    }
    return &Server{
        options: options,
        handler: handler,
    }
}
```

### 4.4 配置使用示例

```go
// 基础 WebSocket 服务
server := websocket.NewServer(
    func(sess core.Session, msg core.Message) error {
        return sess.Send(msg.Payload) // Echo
    },
    websocket.WithAddr(":18700"),
    websocket.WithPath("/ws"),
    websocket.WithAllowedOrigins(
        "https://example.com",
        "https://app.example.com",
    ),
    websocket.WithPingInterval(30*time.Second),
    websocket.WithPongTimeout(15*time.Second),
    websocket.WithMaxMessageSize(64*1024), // 64KB
)

// WSS（WebSocket over TLS）
tlsConfig := &tls.Config{
    MinVersion: tls.VersionTLS13,
    // 证书通过 GetConfigForClient 热加载
}
secureServer := websocket.NewServer(handler,
    websocket.WithAddr(":18443"),
    websocket.WithTLS(tlsConfig),
    websocket.WithAllowedOrigins("https://example.com"),
)
```

### 4.5 AllowedOrigins 配置规范

```
生产环境规范：
  ✓ 明确列出允许的来源（精确匹配）
  ✓ 使用 HTTPS 来源（防中间人）
  ✗ 禁止使用 "*"（允许任意跨域，CSRF 风险）
  ✗ 禁止留空（空列表拒绝所有跨域，非浏览器客户端不受影响）

开发环境（可临时使用）：
  websocket.WithAllowedOrigins("http://localhost:3000")

多来源示例：
  websocket.WithAllowedOrigins(
      "https://app.example.com",
      "https://admin.example.com",
  )
```

### 4.6 运维注意事项

**Ping/Pong 超时调优：**

```
问题：网络抖动导致 Pong 延迟，误判连接断开
解决：适当增大 PongTimeout（默认 15s 通常足够）

问题：移动网络客户端频繁重连（省电模式）
解决：增大 PingInterval（如 60s），减少不必要的心跳

问题：大量连接心跳检测消耗 CPU
解决：P2 引入时间轮替代 ticker（HeartbeatPlugin 也适用）
```

**消息大小限制：**

```
MaxMessageSize 通过 conn.SetReadLimit 在协议层强制：
  超限时 gorilla 自动关闭连接（发送 Close 帧，code=1009）
  不需要在 Handler 中额外检查消息大小

典型配置：
  文字聊天：64KB
  文件传输：4MB（考虑内存占用）
  IoT 控制：4KB（保持轻量）
```

# TRANSPORT.md — Part 5/5: 汇总说明

> Shark-Socket 传输层：协议汇总、对比与 StagedServer 实现表格  
> 版本：v0.1.0  
> 最后更新：2026-06-01

---

## 目录（汇总部分）

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
| 传输层 | TCP 字节流 | UDP 数据报 | UDP 数据报 | TCP（HTTP Upgrade） |
| 连接模型 | 持久连接 | 无连接（伪会话） | 无连接（伪会话） | 持久连接 |
| 消息边界 | Framer 处理 | 数据报天然边界 | 数据报天然边界 | 协议内置帧 |
| 可靠性 | OS 保证 | 无保证 | CON 类型可靠 | OS 保证 |
| 顺序性 | 有序 | 无序 | 无序（NON）/ 有序（CON+Token） |有序 |
| 双向通信 | ✓ | ✓（应用层实现） | ✓（有限） | ✓（全双工） |
| 心跳机制 | 应用层（HeartbeatPlugin） | TTL 扫描（sweepLoop） | TTL 扫描 | 协议内置 Ping/Pong |
| 标准端口 | 自定义（18000） | 自定义（18001） | 5683 | 自定义（18700） |

### 1.2 框架实现特性

| 特性 | TCP | UDP | CoAP | WebSocket |
|------|-----|-----|------|-----------|
| 写队列 | ✓（有界 channel） | ✗（直接写） | ✗（直接写） | ✗（mutex 同步写） |
| Drain 关闭 | ✓（六步状态机） | ✗（立即关闭） | ✗（立即关闭） | ✗（Close 帧协商） |
| WorkerPool | ✓ | ✗（同步 Handler） | ✗（同步 Handler） | ✗（handleSession goroutine） |
| Framer | ✓（4 种） | ✗（数据报边界） | ✗（数据报边界） | ✗（gorilla 内置） |
| 伪会话 | ✗ | ✓ | ✓ | ✗ |
| StagedServer | ✓ | ✗ | ✗ | ✓ |
| retransmitLoop | ✗ | ✗ | ✓（CON 重传） | ✗ |
| MessageID 去重 | ✗ | ✗ | ✓ | ✗ |
| Origin 检查 | ✗ | ✗ | ✗ | ✓ |

### 1.3 Goroutine 使用

| 协议 | per-Server Goroutine | per-Session Goroutine | 总计（N 连接） |
|------|---------------------|----------------------|---------------|
| TCP | 1（acceptLoop） | 2（readLoop + writeLoop） | 1 + 2N |
| UDP | 2（readLoop + sweepLoop） | 0（复用 Server readLoop） | 2 |
| CoAP | 2（readLoop + sweepLoop） | 1（retransmitLoop，per 伪会话） | 2 + N |
| WebSocket | 0（复用 http.Server） | 2（handleSession + pingLoop） | 2N |

---

## 2. StagedServer 实现汇总

### 2.1 实现状态

| 协议 | 实现 StagedServer | 原因 |
|------|-----------------|------|
| TCP | ✓ | 长连接，有写队列需要 drain |
| WebSocket | ✓ | 长连接，需要发送 Close 帧 |
| UDP | ✗ | 无 accept，无写队列，直接 Stop |
| CoAP | ✗ | 基于 UDP，无 accept，直接 Stop |

### 2.2 各阶段执行内容

```
StopAccept 阶段（并发执行）：

  TCP：
    listener.Close()          → acceptLoop 收到 net.ErrClosed 退出
    WorkerPool.Stop()         → 等待所有 Worker 完成当前任务

  WebSocket：
    close(acceptStopped)      → 新 Upgrade 请求返回 503
    （http.Server 暂不关闭，Drain 阶段再关闭）

  UDP：不参与（非 StagedServer）
  CoAP：不参与（非 StagedServer）

────────────────────────────────────────────────

Drain 阶段（并发执行）：

  TCP：
    wg.Wait()                 → 等待所有 readLoop / writeLoop 退出
    超时：强制继续，记录 Warn

  WebSocket：
    httpServer.Shutdown()     → 等待 HTTP Server 处理完在途请求
    wg.Wait()                 → 等待所有 handleSession / pingLoop 退出
    超时：强制继续，记录 Warn

────────────────────────────────────────────────

CloseSessions 阶段（并发执行）：

  TCP：
    manager.Range(TCP)        → 遍历 TCP Session
    sess.Close(ctx)           → 六步状态机关闭（drain + conn.Close）

  WebSocket：
    manager.Range(WebSocket)  → 遍历 WebSocket Session
    sess.Close(ctx)           → 发送 Close 帧 + conn.Close

────────────────────────────────────────────────

StopNonStaged 阶段（并发执行）：

  UDP：
    close(stopCh)             → readLoop / sweepLoop 退出
    conn.Close()              → ReadFromUDP 返回 net.ErrClosed
    wg.Wait()                 → 等待退出
    清理所有伪会话

  CoAP：
    close(stopCh)             → readLoop / sweepLoop 退出
    conn.Close()              → ReadFromUDP 返回 net.ErrClosed
    wg.Wait()                 → 等待所有 retransmitLoop 退出
    清理所有伪会话

────────────────────────────────────────────────

CloseAll 阶段（串行，在所有协议阶段完成后执行）：

  SessionManager.CloseAll(ctx)
    → 清理 Manager 中可能遗留的 Session（应为空或极少数）
    → 若有遗留，记录 Warn（说明协议层未正确清理）
```

### 2.3 阶段超时分配建议

```
总关闭超时（TotalShutdownTimeout）默认 30s，按比例分配：

  StopAccept：   总超时 × 1/6 ≈ 5s
    → 快速停止 listener，不需要等待业务逻辑

  Drain：        总超时 × 1/2 ≈ 15s
    → 等待在途请求完成，是最耗时的阶段

  CloseSessions：总超时 × 1/6 ≈ 5s
    → Session 已完成处理，关闭时间短

  CloseAll：     总超时 × 1/6 ≈ 5s（含 NonStaged 阶段时间）
    → 清理残留，通常为空

调整建议：
  高并发场景（100K 连接）：TotalShutdownTimeout = 60s
    StopAccept 5s / Drain 30s / CloseSessions 15s / CloseAll 10s

  IoT 场景（CoAP/UDP 为主）：TotalShutdownTimeout = 15s
    StopAccept 2s / Drain 5s / CloseSessions 5s / CloseAll 3s
```

---

## 3. Session 实现汇总

### 3.1 Session 字段对比

| 字段 | TCP | UDP | CoAP | WebSocket |
|------|-----|-----|------|-----------|
| `id` | ✓ | ✓ | ✓ | ✓ |
| `remoteAddr` | `net.Addr` | `*net.UDPAddr` | `*net.UDPAddr` | `net.Addr` |
| `conn` | `net.Conn` | `*net.UDPConn`（共享） | `*net.UDPConn`（共享） | `*websocket.Conn` |
| `writeQueue` | ✓（`chan []byte`） | ✗ | ✗ | ✗ |
| `writeMu` | ✗ | ✓ | ✓ | ✓ |
| `draining/drained` | ✓ | ✗ | ✗ | ✗ |
| `messageCache` | ✗ | ✗ | ✓ | ✗ |
| `pendingACKs` | ✗ | ✗ | ✓ | ✗ |
| `closeOnce` | ✓ | ✓ | ✓ | ✓ |
| `meta` | `sync.Map` | `sync.Map` | `sync.Map` | `sync.Map` |

### 3.2 Send 行为对比

| 行为 | TCP | UDP | CoAP | WebSocket |
|------|-----|-----|------|-----------|
| 写方式 | 异步写队列 | 同步 WriteToUDP | 同步 WriteToUDP | 同步 WriteMessage |
| 队列满处理 | `ErrWriteQueueFull` | 不存在 | 不存在 | 不存在 |
| 并发保护 | writeLoop 单 goroutine | `sync.Mutex` | `sync.Mutex` | `sync.Mutex` |
| 数据拷贝 | 必须（队列异步） | 不需要（同步发送） | 不需要（同步发送） | 不需要（同步发送） |
| 阻塞调用方 | 否（非阻塞入队） | 是（WriteToUDP 可阻塞） | 是（WriteToUDP 可阻塞） | 是（WriteMessage 可阻塞） |

### 3.3 Close 行为对比

| 行为 | TCP | UDP | CoAP | WebSocket |
|------|-----|-----|------|-----------|
| 状态转换 | Active→Draining→Closed | Active→Closed | Active→Closed | Connecting/Active→Closed |
| Drain | ✓（等待写队列排空） | ✗ | ✗ | ✗ |
| 发送关闭信号 | 无（连接直接关闭） | 无 | 无 | ✓（Close 帧） |
| 关闭共享资源 | 关闭独占 conn | 不关闭共享 UDPConn | 不关闭共享 UDPConn | 关闭独占 wsConn |
| 幂等保证 | `sync.Once` | `sync.Once` | `sync.Once` | `sync.Once` |

### 3.4 状态初始化对比

| 协议 | 创建时状态 | 转为 Active 时机 |
|------|-----------|----------------|
| TCP | `Connecting` | `handleConn` 中 `OnAccept` 成功后 |
| UDP | `Active`（直接） | 创建即 Active（`OnAccept` 在注册前执行） |
| CoAP | `Active`（直接） | 创建即 Active（同 UDP） |
| WebSocket | `Connecting` | `handleUpgrade` 中 `OnAccept` 成功后 |

---

## 4. 关键配置对比

### 4.1 地址与端口

| 协议 | 默认地址 | 标准端口 | 说明 |
|------|---------|---------|------|
| TCP | `0.0.0.0:18000` | 自定义 | 框架默认端口 |
| UDP | `0.0.0.0:18001` | 自定义 | 框架默认端口 |
| CoAP | `0.0.0.0:5683` | 5683（RFC 7252） | 标准 CoAP 端口 |
| WebSocket | `0.0.0.0:18700` | 自定义 | 框架默认端口 |
| LwM2M | `0.0.0.0:5783` | 5783（LwM2M 标准） | 由 PROTOCOL.md 描述 |

### 4.2 会话限制

| 配置项 | TCP | UDP | CoAP | WebSocket |
|--------|-----|-----|------|-----------|
| `MaxSessions` | 100000 | 100000 | 100000 | 100000 |
| `SessionTTL` | 无（连接关闭决定） | 60s | 5m | 无（Ping/Pong 决定） |
| `SweepInterval` | 无 | 30s | 30s | 无 |

### 4.3 超时配置

| 配置项 | TCP | UDP | CoAP | WebSocket |
|--------|-----|-----|------|-----------|
| `ReadTimeout` | 0（不限） | 1s（stopCh 检测用） | 1s（stopCh 检测用） | `PingInterval + PongTimeout` |
| `WriteTimeout` | 10s | 无 | 无 | 无（由 OS 决定） |
| `DrainTimeout` | 5s | 无 | 无 | 5s（Drain 阶段） |
| `PingInterval` | 无 | 无 | 无 | 30s |
| `PongTimeout` | 无 | 无 | 无 | 15s |
| `AckTimeout` | 无 | 无 | 2s（CON 首次超时） | 无 |

### 4.4 消息大小限制

| 配置项 | TCP | UDP | CoAP | WebSocket |
|--------|-----|-----|------|-----------|
| `MaxMessageSize` | 1MB | 65535B | 65535B | 1MB |
| 强制方式 | Framer `MaxFrameSize` | 读缓冲大小 | 读缓冲大小 | `conn.SetReadLimit` |
| 超限行为 | `ErrFrameTooLarge` | 截断（OS 层） | 丢弃（解析失败） | gorilla 关闭连接（code 1009） |

---

## 5. 协议选型指南

### 5.1 按场景选型

| 场景 | 推荐协议 | 原因 |
|------|---------|------|
| 设备实时控制命令 | TCP | 可靠有序，自定义帧格式灵活 |
| 受限 IoT 设备数据上报 | CoAP | 协议开销小，适合低功耗设备，CON 保证可靠 |
| IoT 设备管理（固件升级、配置下发） | LwM2M（基于 CoAP） | 标准 OMA 设备管理协议 |
| Web 端实时推送（浏览器客户端） | WebSocket | 浏览器原生支持，全双工 |
| 高频低延迟日志/指标上报 | UDP | 无连接，低开销，允许丢失 |
| 内网微服务通信 | TCP | 可靠，支持自定义 Framer |
| 移动端弱网场景 | WebSocket（带重连） | 应用层心跳，断线重连机制成熟 |

### 5.2 协议共存场景

```
典型 IoT 平台部署：

┌─────────────────────────────────────────────────────────┐
│                      Gateway                             │
├──────────────┬──────────────┬──────────────┬────────────┤
│ TCP :18000   │ WebSocket    │ CoAP :5683   │ UDP :18001 │
│ 内网设备     │ :18700/ws    │ 受限设备     │ 日志上报   │
│ 自定义协议   │ Web 客户端   │ LwM2M 接入   │ 高频指标   │
└──────────────┴──────────────┴──────────────┴────────────┘
         │              │              │            │
         └──────────────┴──────────────┴────────────┘
                    共享 SessionManager
                 跨协议广播 / 跨协议路由
```

### 5.3 不适合使用本框架的场景

| 场景 | 不适合原因 | 推荐替代 |
|------|-----------|---------|
| 纯 HTTP API 服务 | 框架 HTTP 为轻量支持，无路由、中间件生态 | `gin` / `echo` / `chi` |
| 高性能 HTTP 反代 | 无 L7 负载均衡，无缓存 | Nginx / Caddy |
| 完整 MQTT Broker | 不实现 QoS 状态机 | shark-MQTT / EMQX |
| 服务网格流量治理 | 无 sidecar 模式，无 xDS | Envoy / Istio |
| 完整 gRPC 服务 | 仅 gRPC-Web 子集 | `google.golang.org/grpc` |

---

## 6. 跨协议数据流

### 6.1 共享 SessionManager 跨协议查询

```go
// 业务层 Handler 示例：TCP 设备向 WebSocket 客户端推送消息

func tcpDeviceHandler(sess core.Session, msg core.Message) error {
    // 解析消息，提取目标 WebSocket Session ID
    targetSessionID := parseTargetID(msg.Payload)

    // 通过共享 SessionManager 查找 WebSocket Session
    manager := getSharedManager()
    targetSess, ok := manager.Get(targetSessionID)
    if !ok {
        return fmt.Errorf("target session %d not found", targetSessionID)
    }

    // 跨协议发送（TCP → WebSocket）
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

### 6.3 跨节点路由（集群模式）

```
本地节点：
  manager.Get(targetID) → 未找到
      ↓
  Cache.Get("session:route:" + targetID) → nodeID
      ↓
  PubSub.Publish("node."+nodeID+".route", {targetID, payload})

远端节点（ClusterPlugin 订阅）：
  PubSub.Subscribe("node."+nodeID+".route", handler)
      ↓
  localManager.Get(targetID) → 找到
      ↓
  sess.Send(payload)
```

---

## 7. 扩展新协议指南

### 7.1 最小实现清单

实现一个新协议传输层需要满足以下最小清单：

```
必须实现：
  □ Session 结构体（嵌入或实现 core.Session 接口）
  □ Session.Send([]byte) error
  □ Session.Close(context.Context) error（幂等）
  □ Session.State() / IsAlive() / LastActiveAt()
  □ Session.SetMeta / GetMeta / DelMeta（sync.Map 实现）
  □ var _ core.Session = (*YourSession)(nil)

  □ Server 结构体（实现 core.Server 接口）
  □ Server.Protocol() core.Protocol
  □ Server.Start(context.Context) error
  □ Server.Stop(context.Context) error
  □ var _ core.Server = (*YourServer)(nil)

  □ RuntimeConfigurable（若需要 Gateway 注入）
  □ Server.UseRuntime(core.Runtime)
  □ var _ core.RuntimeConfigurable = (*YourServer)(nil)

推荐实现（长连接协议）：
  □ StagedServer 接口（StopAccept / Drain / CloseSessions）
  □ var _ core.StagedServer = (*YourServer)(nil)

可选：
  □ Functional Options（WithAddr / WithTLS 等）
  □ 自定义 Framer（若基于 TCP）
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

    // 状态（通用）
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
    // 停止监听，等待 goroutine 退出
    done := make(chan struct{})
    go func() {
        s.wg.Wait()
        close(done)
    }()
    select {
    case <-done:
        return nil
    case <-ctx.Done():
        return core.ErrDrainTimeout
    }
}
```

### 7.4 注册到 Gateway

```go
// application/app.go 中装配新协议
yourServer := your_protocol.NewServer(
    handler,
    your_protocol.WithAddr(":18888"),
)
if err := gateway.RegisterServer(yourServer); err != nil {
    return nil, err
}
```

### 7.5 新协议检查清单

```
功能验证：
  □ 单连接 Echo 测试通过
  □ 多连接并发测试通过（go test -race）
  □ Session.Close() 幂等（多次调用不 panic）
  □ Server.Stop() 幂等（多次调用不 panic）
  □ Gateway Start → Stop → Start 可重入
  □ OnAccept ErrPluginBlock 正确拒绝连接
  □ OnMessage ErrPluginDrop 正确丢弃消息

编译期验证：
  □ var _ core.Session = (*YourSession)(nil)
  □ var _ core.Server = (*YourServer)(nil)
  □ var _ core.RuntimeConfigurable = (*YourServer)(nil)
  □ var _ core.StagedServer = (*YourServer)(nil)（若实现）

指标验证：
  □ shark_sessions_total{protocol="custom"} 正确递增
  □ shark_sessions_active{protocol="custom"} 正确增减
  □ shark_messages_total{protocol="custom", direction="in/out"} 正确
  □ shark_transport_errors_total{protocol="custom"} 正确

文档：
  □ 在 TRANSPORT.md 汇总表格中更新新协议行
  □ 在 CONFIGURATION.md 中添加配置字段
  □ 在 examples/ 中添加 basic_{protocol}/main.go
  □ 在 ROADMAP.md 中标记完成
```

---

**版权声明：** 本文档属于 Shark-Socket 项目，遵循项目许可证。

