package tcp

import (
	"crypto/tls"
	"net"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/transport/shared"
)

type Options struct {
	Addr                string
	Handler             core.Handler
	Framer              Framer
	WriteQueue          int
	WriteTimeout        time.Duration
	ReadTimeout         time.Duration // idle read timeout; 0 disables
	ReadBufferBytes     int           // per-connection read buffer; 0 disables
	WriteBufferBytes    int           // per-connection write buffer; 0 disables
	WriteQueueHighWater float64
	WorkerCount         int
	TaskQueueSize       int
	FullPolicy          FullPolicy
	DrainTimeout        time.Duration
	MaxFrameBytes       int
	TLSConfig           *tls.Config
	MaxConnections      int64
	AcceptRate          float64
	// Listen configures socket-level listener behaviour (SO_REUSEPORT, TCP
	// keepalive). The zero value produces exactly what net.Listen does.
	Listen shared.ListenOptions
	// Proxy makes the listener expect a PROXY protocol header ahead of each
	// connection's payload, so RemoteAddr reports the client the load balancer
	// saw rather than the balancer itself. Zero value means off.
	Proxy shared.ProxyConfig
	// ElasticPool swaps the fixed WorkerPool for a ScalablePool. Nil by default:
	// a pool that grows and shrinks its own worker count should be an explicit
	// choice, not a side effect of upgrading.
	ElasticPool *ElasticPoolOptions
}

// ElasticPoolOptions configures the run-time-scaling worker pool.
type ElasticPoolOptions struct {
	// MinWorker is the floor the pool always keeps, and the count it starts at.
	// MaxWorker is the ceiling scaling may not pass. Non-positive values fall
	// back to GOMAXPROCS and 2 x GOMAXPROCS respectively.
	MinWorker int
	MaxWorker int
	// ScaleUpRatio is the queue occupancy at which a worker is added, and
	// ScaleDownRatio the occupancy below which one may be retired after
	// consecutive low readings. Zero means "keep the package default".
	ScaleUpRatio   float64
	ScaleDownRatio float64
}

type Option func(*Options)

func defaultOptions() Options {
	return Options{
		Addr:                "127.0.0.1:18000",
		Framer:              LengthPrefixFramer{MaxFrameBytes: 1024 * 1024},
		WriteQueue:          128,
		WriteTimeout:        30 * time.Second,
		ReadTimeout:         5 * time.Minute, // bounds idle connections (anti-slowloris)
		ReadBufferBytes:     1024,
		WriteBufferBytes:    0, // opt-in: only pays off for small bursts
		WriteQueueHighWater: 0.8,
		WorkerCount:         4,
		TaskQueueSize:       512,
		FullPolicy:          PolicyDrop,
		DrainTimeout:        5 * time.Second,
		MaxFrameBytes:       1024 * 1024,
	}
}

func WithAddr(addr string) Option {
	return func(o *Options) {
		o.Addr = addr
	}
}

func WithHostPort(host string, port string) Option {
	return func(o *Options) {
		o.Addr = net.JoinHostPort(host, port)
	}
}

func WithHandler(handler core.Handler) Option {
	return func(o *Options) {
		o.Handler = handler
	}
}

func WithTLS(config *tls.Config) Option {
	return func(o *Options) {
		o.TLSConfig = config
	}
}

func WithFramer(framer Framer) Option {
	return func(o *Options) {
		if framer != nil {
			o.Framer = framer
		}
	}
}

// WithReadTimeout sets the per-frame read deadline. Connections that send
// nothing within this window are closed, preventing slowloris-style resource
// exhaustion. 0 disables the timeout (assigned unconditionally so it can be
// turned off after a non-zero default).
func WithReadTimeout(timeout time.Duration) Option {
	return func(o *Options) {
		o.ReadTimeout = timeout
	}
}

// WithReadBufferBytes sets the per-connection read buffer, trading heap for
// syscalls.
//
// Without a buffer each frame costs at least two Read calls (the length header
// and the payload), and LineFramer costs one per byte. With a buffer of n
// bytes one Read serves roughly n/frameSize frames: at the 1 KiB default a
// 256 B frame costs about 0.26 reads instead of 2.
//
// The price is exactly n bytes of heap per connection, held for the
// connection's lifetime, so this is a memory-for-CPU knob rather than a free
// win: at 10k connections 1 KiB means ~10 MB. Raise it (4-8 KiB) for
// throughput-oriented deployments with few connections; lower it, or pass 0 to
// read straight from the conn, when connection count is the constraint.
//
// RawFramer is never buffered regardless of this value: its contract is
// "return what one Read yields", so buffering would shrink its frames.
func WithReadBufferBytes(n int) Option {
	return func(o *Options) {
		if n < 0 {
			n = 0
		}
		o.ReadBufferBytes = n
	}
}

// WithWriteBufferBytes sets the per-connection write buffer. 0 (the default)
// writes each frame straight to the conn.
//
// With a buffer, writeLoop drains the whole queue into it and flushes once, so
// a burst of small frames leaves in one syscall instead of two per frame
// (header + payload). At low load the queue is empty after the first frame and
// the flush is immediate, so latency is unchanged.
//
// The cost is n bytes of heap per connection, the same order as the read
// buffer, and unlike the read buffer it buys nothing for a connection that
// never bursts -- hence opt-in. Enable it for workloads that push many small
// frames per connection (telemetry fan-out, chat); leave it off when
// connection count dominates.
func WithWriteBufferBytes(n int) Option {
	return func(o *Options) {
		if n < 0 {
			n = 0
		}
		o.WriteBufferBytes = n
	}
}

func WithWriteQueue(size int) Option {
	return func(o *Options) {
		if size > 0 {
			o.WriteQueue = size
		}
	}
}

// WithWorkers sets the fixed worker pool's size, task queue depth and
// back-pressure policy.
//
// It does not configure the elastic pool: that one is sized by WithElasticPool.
// A server configured with both keeps the fixed pool, since ElasticPool is what
// selects between the two and WithElasticPool is the only thing that sets it.
func WithWorkers(count int, queueSize int, policy FullPolicy) Option {
	return func(o *Options) {
		if count > 0 {
			o.WorkerCount = count
		}
		if queueSize > 0 {
			o.TaskQueueSize = queueSize
		}
		o.FullPolicy = policy
	}
}

// WithElasticPool replaces the fixed worker pool with one whose worker count
// adjusts at run time, bounded by [min, max].
//
// The judgement is queue occupancy, a leading indicator: a backlog means workers
// are consuming slower than producers submit. Growth is immediate, shrinking
// needs several consecutive low readings, because the two mistakes cost
// differently -- an unnecessary worker costs a goroutine, a missing one pushes
// work onto the survivors and raises the drop rate.
//
// Off by default. This introduces a component that adds and removes goroutines
// on its own, which belongs to the caller's explicit choice.
//
// Note what this does not do: the task queue stays global, so a single noisy
// session can still starve every other session. Elasticity answers "is there
// enough compute in total", not "does one peer get all of it".
func WithElasticPool(min, max int) Option {
	return func(o *Options) {
		if o.ElasticPool == nil {
			o.ElasticPool = &ElasticPoolOptions{}
		}
		o.ElasticPool.MinWorker = min
		o.ElasticPool.MaxWorker = max
	}
}

// WithScaleRatio sets the queue-occupancy thresholds that drive scaling. up is
// the level at which a worker is added, down the level below which one may be
// retired; down must be strictly below up or the pool oscillates around the
// boundary. Values outside (0, 1] leave the corresponding default in place.
//
// A high up threshold (0.8) is conservative: the queue must fill well before the
// pool grows. A low one (0.2) reacts to a light backlog, faster but prone to
// overshoot.
func WithScaleRatio(up, down float64) Option {
	return func(o *Options) {
		if o.ElasticPool == nil {
			o.ElasticPool = &ElasticPoolOptions{}
		}
		if up > 0 && up <= 1 {
			o.ElasticPool.ScaleUpRatio = up
		}
		if down > 0 && down <= 1 {
			o.ElasticPool.ScaleDownRatio = down
		}
	}
}

// WithMaxConnections caps concurrent accepted connections. Values <= 0 mean
// unlimited; excess connection attempts are closed immediately on accept.
func WithMaxConnections(max int64) Option {
	return func(o *Options) { o.MaxConnections = max }
}

// WithAcceptRate limits the connection acceptance rate in connections per
// second (token bucket with a burst of one). Values <= 0 mean unlimited;
// excess connection attempts are closed immediately.
func WithAcceptRate(rate float64) Option {
	return func(o *Options) { o.AcceptRate = rate }
}

// WithReusePort lets several processes (or several listeners in one process)
// bind the same address, with the kernel distributing incoming connections
// across them.
//
// It exists because accept is a single goroutine on a single socket: adding
// cores adds no accept capacity, so port sharing is the only way to scale a
// port horizontally without rewriting the accept path as a reactor.
//
// The trade-off is not free: a duplicate bind no longer fails with "address in
// use", so a deploy that accidentally starts two instances now splits traffic
// instead of refusing to start. Enable it deliberately, together with a
// process-level topology that expects it.
//
// Linux and Darwin only; elsewhere the listener reports
// shared.ErrReusePortUnsupported instead of silently ignoring the request.
func WithReusePort() Option {
	return func(o *Options) { o.Listen.ReusePort = true }
}

// WithKeepAlive overrides the TCP keepalive schedule. idle is the quiet period
// before the first probe; a negative idle disables keepalive probes entirely,
// which Go does not otherwise expose. interval and count apply once probing
// starts; zero leaves the platform default.
//
// The default (Go's 15s) is a compromise that fits neither end of the IoT
// range: too eager for a battery device on a metered link, too slow to reclaim
// a connection whose NAT mapping vanished. Both extremes are now expressible
// per listener instead of per deployment.
func WithKeepAlive(idle, interval time.Duration, count int) Option {
	return func(o *Options) {
		o.Listen.KeepAliveIdle = idle
		o.Listen.KeepAliveInterval = interval
		o.Listen.KeepAliveCount = count
	}
}

// WithProxyProtocol makes the listener require a PROXY protocol header on
// every connection and report the address it carries as the session's
// RemoteAddr.
//
// This is not a security feature by itself: whoever can reach the port can
// claim any source address it likes. It is correct only when the listener is
// reachable exclusively from the load balancer -- typically a separate port,
// bound to a private interface, with the balancer's health check using the
// LOCAL command (v2) or the UNKNOWN family (v1). A listener that accepts both
// "with header" and "without" would let a client pick its own attribution,
// which is why every mode here requires the header rather than treating it as
// optional.
func WithProxyProtocol(mode shared.ProxyProtocolMode) Option {
	return func(o *Options) { o.Proxy.Mode = mode }
}

// WithProxyHeaderTimeout bounds how long a newly accepted connection may take
// to send its PROXY header. Zero or negative keeps
// shared.DefaultProxyHeaderTimeout (5s); there is deliberately no way to wait
// forever, because a connection that never sends a header would then hold an
// accepted slot for as long as it stays open.
func WithProxyHeaderTimeout(timeout time.Duration) Option {
	return func(o *Options) {
		if timeout > 0 {
			o.Proxy.Timeout = timeout
		}
	}
}
