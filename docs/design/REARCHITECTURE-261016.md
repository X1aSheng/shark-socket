# shark-socket 重构方案: 传输内核与运行时解耦

## meta-info

version: 0.1
date: 2026-10-16
status: draft
scope: 完整重架构 (不保持向后兼容)

---

## 1. 问题诊断

### 1.1 核心病灶: Session 接口导致的全量复制

`core.Session` 定义了 13 个方法, 覆盖身份, 状态, 生命周期, 元数据, 传输五个关注点。
任何一个传输层想接入, 都必须实现全部 13 个方法, 即使它只需要其中 2 个。

实测代价 (`internal/transport/*/session.go`):

| transport | 行数 | 实现方法数 |
|---|---|---|
| tcp | 156 | 16 |
| quic | 132 | 15 |
| coap | 103 | 14 |
| udp | 103 | 14 |
| http | 102 | 13 |
| grpcweb | 91 | 14 |
| websocket | 93 | 15 |
| 合计 | 780 | 101 |

其中约 70 个方法 (`ID`/`Protocol`/`State`/`CreatedAt`/`LastActiveAt`/`Context`/
`SetMeta`/`GetMeta`/`DelMeta`/`touch`/`closeOnce` 处理) 在 7 个文件中逐字重复。

`diff websocket/session.go grpcweb/session.go` 显示两个文件的差异仅是底层 conn 类型
(`*websocket.Conn` vs `*http.Request`+`http.ResponseWriter`), 其余 13 个方法完全一致。

这违反接口隔离原则: 接口应由**使用者**定义, 而非由最宽的用例定义。

### 1.2 性能病灶: 热路径的三次冗余

基线数据 (`logs/2026-06-25T17-10-25_benchmark.log`):

| 场景 | ns/op | allocs/op | B/op |
|---|---|---|---|
| TCPEcho 1KB | 54953 | 7 | 3092 |
| TCPEcho 63KB | 156209 | 8 | 196889 |
| LineFramer | 2394 | 12 | 1840 |
| HTTPEcho | 9807 | 90 | 8481 |

- `framer.go:45` 每帧 `make([]byte, raw)`
- `session.go:90` `Send` 中 `append([]byte(nil), payload...)` 全量复制
- `framer.go:57,60` `WriteFrame` 分两次 `w.Write` (header + payload)
- `framer.go:83-94` `LineFramer` 逐字节读
- 63KB 场景 196 KB/op = 帧本身拷贝 3 次 (读分配 + Send 复制 + 写分配)

### 1.3 元数据病灶: sync.Map 用于固定 key

`session.go:24` 每 session 一个 `sync.Map`。实际只存 2 个固定 key:
`autoBanKeyMeta` (`plugin/autoban.go:140`), `rateLimitKeyMeta` (`plugin/ratelimit.go:149`)。

`sync.Map` 适用于 key 数量不可预知的读多写少场景。此处 key 集合编译期已知,
首次 Store 还会额外分配 dirty map, 每 session 实例化一组双层 map 纯属浪费。

### 1.4 指标病灶: 全局锁 + 每次分配

`infra/observability/prometheus.go:43-49` 的 `IncCounter` 每次调用:
1. `metricKey()` 字符串拼接分配
2. 全局 `sync.Mutex`
3. `append([]string(nil), labels...)` 重新分配 labels
4. 写回 map

`runtime/session_metrics.go:24,37` 在**每条连接注册/注销时**都调 `SetGauge`。
高连接 churn 场景下这是全局锁竞争 + 分配风暴。

---

## 2. 重构总纲

### 2.1 分层模型

当前是 "transport 各写各的, runtime 被动适配", 重构为 "内核统一, transport 只实现差异"。

```
internal/
  core/                  契约层 (对外稳定)
    session.go           Session 拆分为 Conn + SessionState
    codec.go             Codec / Handler (泛型业务层)
    plugin.go            Plugin 保持不变 (设计已完善, 不动)
  kernel/                传输内核 (新增, 共享实现)
    conn.go              共享连接基座: 生命周期 + 元数据 + 状态机
    frame.go零拷贝分帧器
    loop.go              共享读写循环骨架
    pool.go              分片工作池
    registry.go          分片会话注册表
  transport/
    tcp/                 仅实现: conn 适配 + 分帧策略
    udp/
    ...
  runtime/               编排层
    gateway.go
    registry.go
    metrics.go           原子化指标
  observability/         原子化 Prometheus 实现
```

### 2.2 契约层核心变更

**Session 拆分为三个关注点:**

```go
// core/conn.go

// Conn 是传输层唯一必须实现的接口。
// 回答的问题只有一个: "这字节怎么进, 怎么出"。
type Conn interface {
    // Read 读取一批字节到 buf, 返回读取量。
    // 返回 n == 0, err == nil 表示暂时无数据 (非阻塞传输如 UDP)。
    Read(buf []byte) (n int, err error)

    // Write 写出字节。实现必须保证并发安全且不乱序
    // (Go 1.20+ 对 net.Conn 的保证; 非 net.Conn 实现需自行加写锁)。
    Write(buf []byte) (int, error)

    // Close 释放底层资源。可重复调用。
    Close() error

    // RemoteAddr / LocalAddr 返回连接地址。
    RemoteAddr() net.Addr
    LocalAddr() net.Addr
}

// Session 是运行时视角的会话句柄, 由 kernel 提供, 传输层不实现。
type Session interface {
    ID() uint64
    Protocol() Protocol
    State() SessionState
    CreatedAt() time.Time
    LastActiveAt() time.Time

    // Raw 返回底层连接, 供需要特殊 IO 的场景 (如 HTTP Hijack) 使用。
    Raw() any

    // Send 写入一帧。buf 的所有权转移给 Session, 调用方不得再触碰。
    // 实现会零拷贝地写入 (预留头 + 一次 syscall)。
    Send(buf []byte) error

    Close(context.Context) error
    Context() context.Context

    // Attr 提供类型安全的元数据, 替代 SetMeta/GetMeta 字符串键。
    Attr() *Attrs
}
```

**收益**: 传输层实现的方法数从 13 降到 **5**, 且全部是真正的差异点。

### 2.3 传输层实现示例

重构后 TCP transport 的全部实现:

```go
// transport/tcp/conn.go
type tcpConn struct {
    c  net.Conn
    rd *bufio.Reader    // 有状态, 挂在连接上
}

func (t *tcpConn) Read(buf []byte) (int, error) { return t.c.Read(buf) }
func (t *tcpConn) Write(b []byte) (int, error) { return t.c.Write(b) }
func (t *tcpConn) Close() error                { return t.c.Close() }
func (t *tcpConn) RemoteAddr() net.Addr        { return t.c.RemoteAddr() }
func (t *tcpConn) LocalAddr() net.Addr         { return t.c.LocalAddr() }

// transport/tcp/transport.go
type transport struct{}

func (transport) Accept(l net.Listener) (core.Conn, error) {
    c, err := l.Accept()
    if err != nil {
        return nil, err
    }
    return &tcpConn{c: c, rd: bufio.NewReaderSize(c, defaultReadBufSize)}, nil
}

func (transport) Protocol() core.Protocol { return core.ProtocolTCP }
```

**从 156 行降到约 45 行**, 且不再需要 `sync.Map` / `context.WithCancel` /
`atomic.Uint32` 状态机 / `sync.Once` -- 这些全部移入 `kernel.ConnBase`。

---

## 3. 零拷贝分帧设计

### 3.1 问题

当前 `ReadFrame(r io.Reader) ([]byte, error)` 无法复用缓冲: 函数内部不知道调用者是否
持有旧缓冲, 也无法保证返回的切片生命周期。

### 3.2 新契约

```go
// kernel/frame.go

// Frame 是解码后的帧视图。Data 指向底层读缓冲, 仅在本次回调内有效。
type Frame struct {
    Data []byte    // 不含长度头
    // 完整帧(含长度头), 用于零拷贝回写
    Raw []byte
}

// Decoder 是分帧器契约。
type Decoder interface {
    // Decode 将 buf 中已读到的数据解码为一帧。
    // 返回 consumed = 本帧消耗的字节数, 调用方据此推进缓冲。
    Decode(buf []byte) (frame Frame, consumed int, err error)

    // HeaderSize 返回该分帧器的帧头字节数, 用于分配带预留头的缓冲。
    HeaderSize() int

    // MaxFrame 返回单帧上限, 用于入参校验。
    MaxFrame() int
}
```

### 3.3 读循环

```go
// kernel/loop.go
type readBuf struct {
    buf []byte
    off int
}

func (l *Loop) readLoop() {
    deferl.Close()
    for {
        // 1. 预留帧头空间, 一次分配, 永不逐帧make
        if cap(l.buf) - len(l.data) < l.decoder.MaxFrame() {
            l.grow()
        }
        // 2. 直接读入现有缓冲的空闲区
        n, err := l.conn.Read(l.data[len(l.data):cap(l.buf)])
        l.data = l.data[:len(l.data)+n]
        // 3. 循环解码尽可能多的帧
        for len(l.data) > 0 {
            frame, consumed, err := l.decoder.Decode(l.data)
            if err == ErrNeedMore {
                break
            }
            if err != nil {
                return
            }
            l.data = l.data[consumed:]
            l.dispatch(frame)
        }
    }
}
```

**收益**:
- 稳态下**零分配** (缓冲只增不减, 复用)
- TCP 小包路径 allocs/op 从 7 降到 1~2 (只剩 handler 侧)
- 批量读取天然支持 (一次 Read 拿到多帧)

### 3.4 零拷贝写

因为 `Frame.Raw` 保留了长度头, 回写时无需重建:

```go
// kernel/conn_base.go 写路径
func (b *ConnBase) Send(frame []byte) error {
    // frame 已由 WriteFrame 预先写入帧头
    _, err := b.conn.Write(frame)
}
```

对需要拼装的场景 (用户调用 `s.Send(payload)` 而非回显帧), 内置 `WriteFrame`:
在 buffer 前部预留 HeaderSize 字节, 写入 payload, 回填长度头, **一次 Write 完成**。

**收益**: 63KB 场景 B/op 从 196889 降到约 65536 (仅一次分配), syscalls 从 2 降到 1。

### 3.5 LineFramer 修正

```go
// kernel/frame.go
type lineDecoder struct {
    delim byte
    max   int
}

func (d *lineDecoder) Decode(buf []byte) (Frame, int, error) {
    i := bytes.IndexByte(buf, d.delim)
    if i < 0 {
        if len(buf) > d.max {
            return Frame{}, 0, ErrFrameTooLarge
        }
        return Frame{}, 0, ErrNeedMore   // 一次 IndexByte 扫描全部数据
    }
    if i > d.max {
        return Frame{}, 0, ErrFrameTooLarge
    }
    return Frame{Data: buf[:i], Raw: buf[:i+1]}, i + 1, nil
}
```

**单次 `IndexByte` 替换逐字节 read**: LineFramer 从 2394 ns/op / 12 allocs
降到约 100 ns/op / 0 allocs。

---

## 4. 共享连接基座

```go
// kernel/conn.go
type ConnBase struct {
    core.Conn                 // 嵌入传输层实现 (由 factory 构造)

    id       uint64
    proto    core.Protocol
    created  int64            // unix nano
    active   atomic.Int64
    state    atomic.Uint32
    attrs    Attrs
    ctx      context.Context
    cancel   context.CancelFunc
    closeOne sync.Once
    wq       *WriteQueue
}

func NewConnBase(id uint64, p core.Protocol, c core.Conn, opts ConnOptions) *ConnBase {
    ctx, cancel := context.WithCancel(context.Background())
    b := &ConnBase{
        Conn: c, id: id, proto: p,
        created: time.Now().UnixNano(),
        ctx: ctx, cancel: cancel,
        wq: NewWriteQueue(opts.WriteQueue, opts.WriteHighWater),
    }
    b.active.Store(b.created)
    b.state.Store(uint32(core.StateActive))
    return b
}
```

所有 7 个 transport 共享的 70 个方法**一次性实现在这里**, 传输层不再重复。

### 4.1 Attrs 替代 sync.Map

```go
// kernel/attrs.go

// Attrs 是每会话的类型安全属性表。
// 与 sync.Map 的差异: 无内部双层 map, 无读接口开销;
// key 集合由调用方通过 AttrSlot 预声明, 避免字符串驻留。
type Attrs struct {
    mu sync.RWMutex
    m  map[attrKey]any
}

type attrKey struct {
    owner string   // 插件名, 天然隔离不同插件的命名空间
    name  string
}

// Slot 返回一个稳定的属性句柄, 启动时创建一次, 热路径零字符串比较。
func (a *Attrs) Slot(owner, name string) *AttrSlot
```

`autoBanKeyMeta` / `rateLimitKeyMeta` 改为包级 `Slot` 变量, 每 session 只有一个
小 map 而非 sync.Map 双层结构。

---

## 5. 运行时层重构

### 5.1 分片会话注册表

```go
// runtime/registry.go
const registryShards = 32

type Registry struct {
    shards [registryShards]registryShard
    nextID atomic.Uint64
    count  atomic.Int64
}

type registryShard struct {
    mu sync.RWMutex
    m  map[uint64]core.Session
    _  [40]byte // 避免 false sharing, 补足至 cache line
}

func (r *Registry) shardFor(id uint64) *registryShard {
    return &r.shards[id%registryShards]
}
```

**收益**: 注册/注销的锁竞争从全局单点降到 32 分片, 线性扩展。

`Snapshot` / `Range` / `Broadcast` 逐分片遍历, 语义不变。

### 5.2 原子化指标

```go
// observability/prometheus.go
type PrometheusMetrics struct {
    counters   map[string]*promCounter   // 值改指针
    gauges     map[string]*promGauge
    histograms map[string]*promHistogram
}

type promCounter struct {
    name   string
    labels []string   // 只在创建时分配一次
    value  atomic.Uint64
}

func (m *PrometheusMetrics) IncCounter(name string, labels ...string) {
    if c, ok := m.counters[name]; ok {
        c.value.Add(1)     // 无锁, 无分配
        return
    }
    m.slowCreateCounter(name, labels)     // 加锁慢路径
}
```

`SetGauge` 用 `atomic.StoreUint64(&g.bits, math.Float64bits(v))`,
读侧 `math.Float64frombits`。

**收益**: 消除每连接注册/注销时的全局锁 + 2 次分配。

### 5.3 分片工作池

```go
// kernel/pool.go
type ShardedPool struct {
    shards []poolShard
    mask   uint32
}

func (p *ShardedPool) Submit(sess core.Session, data []byte) error {
    // 按 session ID 分片, 让同一连接的连续任务落在同一 shard,
    // 消除 channel 跨核 cache line 争用
    sh := &p.shards[uint32(sess.ID())&p.mask]
    return sh.submit(PoolTask{Session: sess, Data: data})
}
```

**收益**: 高并发下 channel 争用降为原来的 1/N。

---

## 6. 保留不动的设计

以下设计经检验是完善的, 重构中**明确保留**:

| 设计 | 位置 | 保留理由 |
|---|---|---|
| Plugin 优先级 + 生命周期 + 回滚 | `core/plugin.go`, `runtime/plugin_chain.go` | 比 smart-socket 的单接口模型更完善 |
| `snapshot()` copy-on-write 零分配 | `plugin_chain.go:139` | 每消息零分配的快照, 设计精妙 |
| 协作式优雅停机四阶段 | `core.StageTimeouts`, `StagedServer` | 业界标准做法 |
| panic 隔离 | `shared/handler.go` | Go 无 per-goroutine recover, 必须显式隔离 |
| 每连接读写双 goroutine | `tcp/server.go:198-202` | Go 的 netpoll 已解决写乱序, 多一个 goroutine 换写侧无阻塞, 划算 |
| 高水位背压 | `session.go:81-89` | 慢消费者提前甩负载, 设计正确 |

### 6.1 明确不做的优化

| 项 | 决策 | 理由 |
|---|---|---|
| 引入 sync.Pool 帧缓冲 | **不做** | Go 堆分配器已 per-P 池化, 小对象分配 ~20ns; payload 跨 goroutine 生命周期不确定, 池化会引入"借了没还"的泄漏面。正确做法是消除拷贝 |
| 合并读写 goroutine | **不做** | 写变同步阻塞 = 队头阻塞回归, 远差于多一个 goroutine |
| 引入时间轮定时器 | **不做** | Go 的 `time.Timer` 挂在 per-P heap 且 1.23+ 已优化; `Heartbeat.Sweep` 已是惰性扫描, 无 per-session timer |
| 降低 HTTP/gRPC-Web 分配 | **推迟** | 90~100 allocs 主要在第三方库, 改动收益有限 |

---

## 7. 实施序列

每阶段独立可提交, 每阶段有基准数据验证。

| 阶段 | 内容 | 验证指标 | 依赖 |
|---|---|---|---|
| M1 | `core.Conn` / `core.Session` 新契约 + `kernel.ConnBase` | 7 份 session 收敛为 1 份 | - |
| M2 | TCP transport 迁移到新内核 | 行为等价, `tests/` 全绿 | M1 |
| M3 | 零拷贝 Decoder + 共享 readLoop | LineFramer <200ns; TCP 63KB B/op <70K | M2 |
| M4 | 零拷贝写路径 | TCP 63KB B/op ~65K, syscalls -50% | M3 |
| M5 | 其余 6 个 transport 迁移 | session.go 全部删除 | M1 |
| M6 | 原子化指标 + 分片注册表 + 分片池 | 高并发基准, 连接 churn 无锁竞争 | M1 |
| M7 | `api/` 门面重写, 删除全部别名 | 公开 API 面从 40+ 别名收敛 | M1~M6 |

**基线锚点** (`logs/2026-06-25T17-10-25_benchmark.log`), 每一阶段必须对比:

```
BenchmarkLineFramerRoundTrip      223.6 ns/op -> 目标 < 200 ns/op
BenchmarkLengthPrefixFramer       57960ns/op  -> 目标 allocs 7 -> < 2
BenchmarkTCPEcho_PayloadSize/63KB 156209 ns/op 156209 B -> 目标 < 70000 B/op
BenchmarkHTTPEcho                 9807 ns/op   -> 保持 (不劣化)
```

---

## 8. 风险

| 风险 | 影响 | 缓解 |
|---|---|---|
| `core.Session` 签名变更波及全部 transport | 大 | M1-M5 按序迁移, 每步 `go build ./...` + `go test ./...` |
| 缓冲复用引入use-after-free | 严重(数据错乱/崩溃) | `Frame` 生命周期契约: `Data` 仅在回调内有效; 需要跨回调持有必须显式 `Clone()` |
| 零拷贝写与插件链的 `[]byte` 变换冲突 | 中 | 插件链约定: 插件返回新切片则所有权转移; 返回入参则复用原缓冲。需在 `PLUGIN.md` 明确 |
| 分片注册表改变 Snapshot 顺序 | 低 | 文档化为无序; 若需有序由调用方排序 |
| 无法复现基线 | 中 | M1 前先跑一次 `scripts/run_benchmarks.go` 固化基线到 `docs/reports/` |

---

## 9. 待决策项

1. **`Frame` 生命周期契约**: 回调内有效 (零拷贝, 需用户配合) vs 始终有效 (需复制, 退回现状)。建议前者 + 提供显式 `Clone()`。
2. **是否保留 `api/` 的类型别名门面**: 当前 40+ 别名让内部重构成本变高。建议收敛为少量构造器 + 直接导出核心接口。
3. **M1 是否与 M2 合并提交**: M1 会让全部 transport 编译失败一段时间。分提交便于 review, 合提交便于 CI 绿灯。