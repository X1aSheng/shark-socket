package tcp

import (
	"crypto/tls"
	"fmt"
	"runtime"

	"github.com/X1aSheng/shark-socket/internal/infra/tracing"
	"github.com/X1aSheng/shark-socket/internal/types"
)

// Options holds TCP server configuration.
type Options struct {
	Host                string
	Port                int
	WorkerCount         int
	TaskQueueSize       int
	MaxWorkers          int
	FullPolicy          FullPolicy
	WriteQueueSize      int
	MaxSessions         int64
	MaxMessageSize      int
	ReadTimeout         int // seconds, 0 = no timeout
	WriteTimeout        int // seconds
	IdleTimeout         int // seconds
	HandlerTimeout      int // seconds
	DrainTimeout        int // seconds
	ShutdownTimeout     int // seconds
	MaxConsecutiveErrors int
	Framer              Framer
	TLSConfig           *tls.Config
	Tracer              tracing.Tracer
	Plugins             []types.Plugin
}

func defaultOptions() Options {
	workers := runtime.NumCPU() * 2
	return Options{
		Host:                "0.0.0.0",
		Port:                18000,
		WorkerCount:         workers,
		TaskQueueSize:       workers * 128,
		MaxWorkers:          workers * 4,
		FullPolicy:          PolicyDrop,
		WriteQueueSize:      128,
		MaxSessions:         100000,
		MaxMessageSize:      1024 * 1024, // 1MB
		DrainTimeout:        5,
		ShutdownTimeout:     10,
		MaxConsecutiveErrors: 100,
		Framer:              NewLengthPrefixFramer(1024 * 1024),
	}
}

// Addr returns the listen address.
func (o Options) Addr() string {
	return fmt.Sprintf("%s:%d", o.Host, o.Port)
}

// Option is a functional option for TCP server.
type Option func(*Options)

// WithAddr sets the listen address.
func WithAddr(host string, port int) Option {
	return func(o *Options) { o.Host = host; o.Port = port }
}

// WithTLS enables TLS.
func WithTLS(cfg *tls.Config) Option {
	return func(o *Options) { o.TLSConfig = cfg }
}

// WithWorkerPool configures the worker pool.
func WithWorkerPool(core, max, queue int) Option {
	return func(o *Options) {
		o.WorkerCount = core
		o.MaxWorkers = max
		o.TaskQueueSize = queue
	}
}

// WithFullPolicy sets the queue-full policy.
func WithFullPolicy(p FullPolicy) Option {
	return func(o *Options) { o.FullPolicy = p }
}

// WithWriteQueueSize sets the per-connection write queue size.
func WithWriteQueueSize(size int) Option {
	return func(o *Options) { o.WriteQueueSize = size }
}

// WithMaxSessions sets the maximum session count.
func WithMaxSessions(max int64) Option {
	return func(o *Options) { o.MaxSessions = max }
}

// WithMaxMessageSize sets the maximum message size.
func WithMaxMessageSize(max int) Option {
	return func(o *Options) { o.MaxMessageSize = max }
}

// WithFramer sets the framer implementation.
func WithFramer(f Framer) Option {
	return func(o *Options) { o.Framer = f }
}

// WithPlugins adds protocol-level plugins.
func WithPlugins(p ...types.Plugin) Option {
	return func(o *Options) { o.Plugins = append(o.Plugins, p...) }
}

// WithTimeouts configures timeouts in seconds.
func WithTimeouts(read, write, idle int) Option {
	return func(o *Options) {
		o.ReadTimeout = read
		o.WriteTimeout = write
		o.IdleTimeout = idle
	}
}

// WithDrainTimeout sets the drain timeout in seconds.
func WithDrainTimeout(d int) Option {
	return func(o *Options) { o.DrainTimeout = d }
}

// WithMaxConsecutiveErrors sets the max consecutive accept errors.
func WithMaxConsecutiveErrors(max int) Option {
	return func(o *Options) { o.MaxConsecutiveErrors = max }
}

// WithTracer sets the distributed tracing implementation.
//
// The tracer is stored in options and is available via opts.Tracer.
// The TCP server does not create spans internally; instead, users who
// want tracing should wrap the handler or use middleware to create spans
// around Accept / handleConn lifecycle events:
//
//	otelTracer := tracing.NopTracer{} // or your OTel wrapper
//	srv := tcp.NewServer(handler, tcp.WithTracer(otelTracer))
func WithTracer(t tracing.Tracer) Option {
	return func(o *Options) { o.Tracer = t }
}
