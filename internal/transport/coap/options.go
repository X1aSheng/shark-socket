package coap

import (
	"crypto/tls"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type Options struct {
	Addr          string
	Handler       core.Handler
	Responder     func(core.Session, core.Message) ([]byte, error)
	SessionTTL    time.Duration
	SweepInterval time.Duration
	MaxDatagram   int
	// DTLSReadBufferBytes is the per-connection read buffer for DTLS peers.
	// Every DTLS connection holds its own buffer for its lifetime, so the
	// default is smaller than MaxDatagram (used by the single shared
	// plain-UDP buffer): 16 KiB covers typical CoAP/LwM2M messages with
	// headroom; larger payloads should use blockwise transfer (RFC 7959).
	DTLSReadBufferBytes int
	TLSConfig           *tls.Config
}

type Option func(*Options)

func defaultOptions() Options {
	return Options{
		Addr:                "127.0.0.1:18500",
		SessionTTL:          2 * time.Minute,
		SweepInterval:       30 * time.Second,
		MaxDatagram:         64 * 1024,
		DTLSReadBufferBytes: 16 * 1024,
	}
}

func WithAddr(addr string) Option {
	return func(o *Options) { o.Addr = addr }
}

func WithHandler(handler core.Handler) Option {
	return func(o *Options) { o.Handler = handler }
}

func WithResponder(responder func(core.Session, core.Message) ([]byte, error)) Option {
	return func(o *Options) { o.Responder = responder }
}

func WithSessionTTL(ttl time.Duration) Option {
	return func(o *Options) {
		if ttl > 0 {
			o.SessionTTL = ttl
		}
	}
}

func WithSweepInterval(interval time.Duration) Option {
	return func(o *Options) {
		if interval > 0 {
			o.SweepInterval = interval
		}
	}
}

func WithDTLS(cfg *tls.Config) Option {
	return func(o *Options) {
		o.TLSConfig = cfg
	}
}

// WithDTLSReadBufferBytes sets the per-connection DTLS read buffer size.
// Values <= 0 are ignored. See Options.DTLSReadBufferBytes for sizing
// guidance.
func WithDTLSReadBufferBytes(size int) Option {
	return func(o *Options) {
		if size > 0 {
			o.DTLSReadBufferBytes = size
		}
	}
}
