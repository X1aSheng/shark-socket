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
	// MaxSessions caps how many datagram-peer sessions the server keeps. Values
	// <= 0 mean unlimited, which is what every previous version did: a single
	// forged-source flood could grow the table - and the shared SessionManager -
	// until it hit the manager's own ceiling, at which point new sessions are
	// rejected for everyone, including legitimate devices.
	MaxSessions int64
	// MaxSessionsPerPeer caps one source address. Spoofing the source spreads a
	// flood over MaxSessions; without this dimension it still fills the table.
	MaxSessionsPerPeer int64
	// MaxBlockSize enables block-wise responses (RFC 7959 Block2) and sets the
	// largest block this server will send: 16, 32, 64, 128, 256, 512 or 1024
	// bytes. Any other non-zero value is rounded down to the nearest legal
	// size, which is what the wire format can express (SZX).
	//
	// Zero means off, and off is the default. Turning it on changes the wire
	// behaviour of every response larger than the size: the peer receives the
	// first block with the M bit set, and a client that does not implement
	// Block2 must reject that response because option 23 is critical. That is
	// the RFC's design, not a defect, but it makes this a deliberate opt-in
	// rather than a default.
	MaxBlockSize int
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

// WithMaxSessions caps the number of concurrent datagram-peer sessions. Values
// <= 0 mean unlimited.
func WithMaxSessions(max int64) Option {
	return func(o *Options) { o.MaxSessions = max }
}

// WithMaxSessionsPerPeer caps concurrent sessions from one source address.
// Values <= 0 mean unlimited.
func WithMaxSessionsPerPeer(max int64) Option {
	return func(o *Options) { o.MaxSessionsPerPeer = max }
}

// WithMaxBlockSize enables block-wise responses (RFC 7959 Block2) at the given
// block size. Legal sizes are 16..1024 bytes; anything else is rounded down to
// the nearest legal size. Values <= 0 leave block-wise transfer off.
func WithMaxBlockSize(size int) Option {
	return func(o *Options) {
		if size <= 0 {
			o.MaxBlockSize = 0
			return
		}
		if size > DefaultBlockSize {
			size = DefaultBlockSize
		}
		for size >= 16 && (size&(size-1)) != 0 {
			size--
		}
		if size < 16 {
			size = 16
		}
		o.MaxBlockSize = size
	}
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
