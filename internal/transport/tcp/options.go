package tcp

import (
	"crypto/tls"
	"net"
	"time"

	"github.com/X1aSheng/shark-socket-new/internal/core"
)

type Options struct {
	Addr          string
	Handler       core.Handler
	Framer        Framer
	WriteQueue    int
	WorkerCount   int
	TaskQueueSize int
	FullPolicy    FullPolicy
	DrainTimeout  time.Duration
	MaxFrameBytes int
	TLSConfig     *tls.Config
}

type Option func(*Options)

func defaultOptions() Options {
	return Options{
		Addr:          "127.0.0.1:18000",
		Framer:        LengthPrefixFramer{MaxFrameBytes: 1024 * 1024},
		WriteQueue:    128,
		WorkerCount:   4,
		TaskQueueSize: 512,
		FullPolicy:    PolicyBlock,
		DrainTimeout:  5 * time.Second,
		MaxFrameBytes: 1024 * 1024,
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

func WithWriteQueue(size int) Option {
	return func(o *Options) {
		if size > 0 {
			o.WriteQueue = size
		}
	}
}

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
