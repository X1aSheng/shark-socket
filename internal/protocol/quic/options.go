package quic

import (
	"crypto/tls"
	"fmt"
	"time"

	"github.com/X1aSheng/shark-socket/internal/types"
)

// Options holds QUIC server configuration.
type Options struct {
	Host            string
	Port            int
	WorkerCount     int
	TaskQueueSize   int
	WriteQueueSize  int
	MaxMessageSize  int
	ReadTimeout     int // seconds
	WriteTimeout    int // seconds
	IdleTimeout     int // seconds
	DrainTimeout    int // seconds
	ShutdownTimeout int // seconds
	MaxSessions     int64
	// QUIC specific
	TLSConfig          *tls.Config
	MaxIncomingStreams int // Max incoming bidirectional streams (0 = unlimited)
	MaxDatagramSize    int // Max datagram frame size
	HandshakeTimeout   int // seconds
	IdleTimeoutQUIC    int // seconds (QUIC idle timeout)
	Plugins            []types.Plugin
}

func defaultOptions() Options {
	return Options{
		Host:               "0.0.0.0",
		Port:               18900,
		WorkerCount:        4,
		TaskQueueSize:      512,
		WriteQueueSize:     16,
		MaxMessageSize:     1024 * 1024, // 1MB
		ReadTimeout:        30,
		WriteTimeout:       30,
		IdleTimeout:        120,
		DrainTimeout:       5,
		ShutdownTimeout:    15,
		MaxSessions:        100000,
		MaxIncomingStreams: 100,
		MaxDatagramSize:    1350,
		HandshakeTimeout:   10,
		IdleTimeoutQUIC:    30,
	}
}

// Addr returns the listen address.
func (o Options) Addr() string {
	return fmt.Sprintf("%s:%d", o.Host, o.Port)
}

// Option is a functional option for QUIC server.
type Option func(*Options)

// WithAddr sets the listen address.
func WithAddr(host string, port int) Option {
	return func(o *Options) { o.Host = host; o.Port = port }
}

// WithTimeouts configures read/write/idle timeouts in seconds.
func WithTimeouts(read, write, idle int) Option {
	return func(o *Options) {
		o.ReadTimeout = read
		o.WriteTimeout = write
		o.IdleTimeout = idle
	}
}

// WithTLS enables TLS (required for QUIC).
func WithTLS(cfg *tls.Config) Option {
	return func(o *Options) { o.TLSConfig = cfg }
}

// WithPlugins adds protocol-level plugins.
func WithPlugins(p ...types.Plugin) Option {
	return func(o *Options) { o.Plugins = append(o.Plugins, p...) }
}

// WithMaxMessageSize sets the maximum message size in bytes.
func WithMaxMessageSize(n int) Option {
	return func(o *Options) { o.MaxMessageSize = n }
}

// WithMaxSessions sets the maximum number of concurrent sessions.
func WithMaxSessions(n int64) Option {
	return func(o *Options) { o.MaxSessions = n }
}

// WithMaxIncomingStreams sets the maximum number of concurrent incoming streams.
func WithMaxIncomingStreams(n int) Option {
	return func(o *Options) { o.MaxIncomingStreams = n }
}

// WithQUICTimeouts configures QUIC-specific timeouts.
func WithQUICTimeouts(handshake, idle int) Option {
	return func(o *Options) {
		o.HandshakeTimeout = handshake
		o.IdleTimeoutQUIC = idle
	}
}

// WithDrainTimeout sets the drain timeout in seconds.
func WithDrainTimeout(d int) Option {
	return func(o *Options) { o.DrainTimeout = d }
}

func (o Options) validate() error {
	if o.Port < 0 || o.Port > 65535 {
		return fmt.Errorf("quic config: port must be 0-65535")
	}
	if o.TLSConfig == nil {
		return fmt.Errorf("quic config: TLS is required")
	}
	if o.MaxIncomingStreams < 0 {
		return fmt.Errorf("quic config: max incoming streams must be >= 0")
	}
	return nil
}

// GetReadTimeout returns the read timeout as time.Duration.
func (o Options) GetReadTimeout() time.Duration {
	return time.Duration(o.ReadTimeout) * time.Second
}

// GetWriteTimeout returns the write timeout as time.Duration.
func (o Options) GetWriteTimeout() time.Duration {
	return time.Duration(o.WriteTimeout) * time.Second
}

// GetIdleTimeout returns the idle timeout as time.Duration.
func (o Options) GetIdleTimeout() time.Duration {
	return time.Duration(o.IdleTimeout) * time.Second
}

// GetHandshakeTimeout returns the handshake timeout as time.Duration.
func (o Options) GetHandshakeTimeout() time.Duration {
	return time.Duration(o.HandshakeTimeout) * time.Second
}

// GetIdleTimeoutQUIC returns the QUIC idle timeout as time.Duration.
func (o Options) GetIdleTimeoutQUIC() time.Duration {
	return time.Duration(o.IdleTimeoutQUIC) * time.Second
}
