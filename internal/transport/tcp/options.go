package tcp

import (
	"crypto/tls"
	"net"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type Options struct {
	Addr                string
	Handler             core.Handler
	Framer              Framer
	WriteQueue          int
	WriteTimeout        time.Duration
	ReadTimeout         time.Duration // idle read timeout; 0 disables
	WriteQueueHighWater float64
	WorkerCount         int
	TaskQueueSize       int
	FullPolicy          FullPolicy
	DrainTimeout        time.Duration
	MaxFrameBytes       int
	TLSConfig           *tls.Config
	MaxConnections      int64
	AcceptRate          float64
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
