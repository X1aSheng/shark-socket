package api

import (
	"crypto/tls"
	"time"

	"github.com/X1aSheng/shark-socket/internal/errs"
	"github.com/X1aSheng/shark-socket/internal/infra/cache"
	"github.com/X1aSheng/shark-socket/internal/infra/circuitbreaker"
	"github.com/X1aSheng/shark-socket/internal/infra/logger"
	"github.com/X1aSheng/shark-socket/internal/infra/metrics"
	"github.com/X1aSheng/shark-socket/internal/infra/pubsub"
	"github.com/X1aSheng/shark-socket/internal/infra/store"
	"github.com/X1aSheng/shark-socket/internal/infra/tracing"
	"github.com/X1aSheng/shark-socket/internal/plugin"
	"github.com/X1aSheng/shark-socket/internal/protocol/coap"
	"github.com/X1aSheng/shark-socket/internal/protocol/grpcgw"
	"github.com/X1aSheng/shark-socket/internal/protocol/http"
	"github.com/X1aSheng/shark-socket/internal/protocol/quic"
	tcpclient "github.com/X1aSheng/shark-socket/internal/protocol/tcp"
	udp "github.com/X1aSheng/shark-socket/internal/protocol/udp"
	websocket "github.com/X1aSheng/shark-socket/internal/protocol/websocket"
	"github.com/X1aSheng/shark-socket/internal/types"
	"github.com/X1aSheng/shark-socket/internal/gateway"
)

// === Type Aliases ===

type (
	Session[M types.MessageConstraint] = types.Session[M]
	RawSession                       = types.RawSession
	SessionManager                   = types.SessionManager
	SessionState                     = types.SessionState
	Message[T any]                   = types.Message[T]
	RawMessage                       = types.RawMessage
	MessageConstraint                = types.MessageConstraint
	MessageHandler[T any]            = types.MessageHandler[T]
	RawHandler                       = types.RawHandler
	Server                           = types.Server
	Plugin                           = types.Plugin
	BasePlugin                       = types.BasePlugin
	ProtocolType                     = types.ProtocolType
	MessageType                      = types.MessageType
	Logger                           = logger.Logger
	Cache                            = cache.Cache
	Store                            = store.Store
	PubSub                           = pubsub.PubSub
	CircuitBreaker                   = circuitbreaker.CircuitBreaker
	Tracer                           = tracing.Tracer
)

// === Protocol Constants ===

const (
	TCP       = types.TCP
	TLS       = types.TLS
	UDP       = types.UDP
	HTTP      = types.HTTP
	WebSocket = types.WebSocket
	CoAP      = types.CoAP
	QUIC      = types.QUIC
	Custom    = types.Custom

	Text    = types.Text
	Binary  = types.Binary
	Ping    = types.Ping
	Pong    = types.Pong
	Close   = types.Close

	Connecting = types.Connecting
	Active     = types.Active
	Closing    = types.Closing
	Closed     = types.Closed
)

// === Gateway Factory ===

func NewGateway(opts ...gateway.Option) *gateway.Gateway {
	return gateway.New(opts...)
}

// === TCP Server Factory ===

func NewTCPServer(handler RawHandler, opts ...tcpclient.Option) *tcpclient.Server {
	return tcpclient.NewServer(handler, opts...)
}

// === UDP Server Factory ===

func NewUDPServer(handler RawHandler, opts ...udp.Option) *udp.Server {
	return udp.NewServer(handler, opts...)
}

// === HTTP Server Factory ===

func NewHTTPServer(opts ...http.Option) *http.Server {
	return http.NewServer(opts...)
}

// === WebSocket Server Factory ===

func NewWebSocketServer(handler RawHandler, opts ...websocket.Option) *websocket.Server {
	return websocket.NewServer(handler, opts...)
}

func WithWebSocketAccessLogger(l logger.AccessLogger) websocket.Option {
	return websocket.WithAccessLogger(l)
}

// === CoAP Server Factory ===

func NewCoAPServer(handler RawHandler, opts ...coap.Option) *coap.Server {
	return coap.NewServer(handler, opts...)
}

// === QUIC Server Factory ===

func NewQUICServer(handler RawHandler, opts ...quic.Option) *quic.Server {
	return quic.NewServer(handler, opts...)
}

// === gRPC-Web Gateway Factory ===

func NewGRPCWebGateway(opts ...grpcgw.Option) *grpcgw.Server {
	return grpcgw.NewServer(opts...)
}

// === TCP Client Factory ===

func NewTCPRawClient(addr string, opts ...tcpclient.ClientOption) *tcpclient.Client {
	return tcpclient.NewClient(addr, opts...)
}

// === Plugin Factories ===

func NewBlacklistPlugin(ips ...string) *plugin.BlacklistPlugin {
	return plugin.NewBlacklistPlugin(ips...)
}

func NewRateLimitPlugin(rate, burst float64, opts ...plugin.RateLimitOption) *plugin.RateLimitPlugin {
	return plugin.NewRateLimitPlugin(rate, burst, opts...)
}

func NewHeartbeatPlugin(interval, timeout time.Duration, mgr func() SessionManager) *plugin.HeartbeatPlugin {
	return plugin.NewHeartbeatPlugin(interval, timeout, mgr)
}

func NewPersistencePlugin(s Store, opts ...plugin.PersistenceOption) *plugin.PersistencePlugin {
	return plugin.NewPersistencePlugin(s, opts...)
}

func NewAutoBanPlugin(blacklist *plugin.BlacklistPlugin, opts ...plugin.AutoBanOption) *plugin.AutoBanPlugin {
	return plugin.NewAutoBanPlugin(blacklist, opts...)
}

// === Infrastructure Factories ===

func SetLogger(l Logger) { logger.SetDefault(l) }

func DefaultMetrics() metrics.Metrics { return metrics.NopMetrics() }

func NewMemoryCache(opts ...cache.CacheOption) *cache.MemoryCache {
	return cache.NewMemoryCache(opts...)
}

func NewMemoryStore() *store.MemoryStore {
	return store.NewMemoryStore()
}

func NewChannelPubSub() *pubsub.ChannelPubSub {
	return pubsub.NewChannelPubSub()
}

func NewCircuitBreaker(threshold int64, timeout time.Duration) *circuitbreaker.CircuitBreaker {
	return circuitbreaker.New(threshold, timeout)
}

// === Configuration Pass-through ===

// TCP Options
func WithTCPAddr(host string, port int) tcpclient.Option {
	return tcpclient.WithAddr(host, port)
}

func WithTCPTLS(cfg *tls.Config) tcpclient.Option {
	return tcpclient.WithTLS(cfg)
}

func WithTCPPlugins(p ...Plugin) tcpclient.Option {
	return tcpclient.WithPlugins(p...)
}

func WithTCPMaxSessions(max int64) tcpclient.Option {
	return tcpclient.WithMaxSessions(max)
}

func WithTCPMaxMessageSize(max int) tcpclient.Option {
	return tcpclient.WithMaxMessageSize(max)
}

func WithTCPConnRateLimit(rate int, windowSec int) tcpclient.Option {
	return tcpclient.WithConnRateLimit(rate, windowSec)
}

func WithTCPTLSCertFile(certFile, keyFile string) tcpclient.Option {
	return tcpclient.WithTLSCertFile(certFile, keyFile)
}

// HTTP Options
func WithHTTPAddr(host string, port int) http.Option {
	return http.WithAddr(host, port)
}

func WithHTTPConnRateLimit(rate int, windowSec int) http.Option {
	return http.WithConnRateLimit(rate, windowSec)
}

func WithHTTPShutdownTimeout(sec int) http.Option {
	return http.WithShutdownTimeout(sec)
}

func WithHTTPAccessLogger(l logger.AccessLogger) http.Option {
	return http.WithAccessLogger(l)
}

func WithHTTP2() http.Option {
	return http.WithHTTP2()
}

func WithHTTP2Config(maxStreams int) http.Option {
	return http.WithHTTP2Config(maxStreams)
}

// Gateway Options
func WithShutdownTimeout(d time.Duration) gateway.Option {
	return gateway.WithShutdownTimeout(d)
}

func WithMetricsAddr(addr string) gateway.Option {
	return gateway.WithMetricsAddr(addr)
}

func WithMetricsEnabled(enabled bool) gateway.Option {
	return gateway.WithMetricsEnabled(enabled)
}

func WithGlobalPlugins(p ...Plugin) gateway.Option {
	return gateway.WithGlobalPlugins(p...)
}

// QUIC Options
func WithQUICAddr(host string, port int) quic.Option {
	return quic.WithAddr(host, port)
}

func WithQUICTLS(cfg *tls.Config) quic.Option {
	return quic.WithTLS(cfg)
}

func WithQUICPlugins(p ...Plugin) quic.Option {
	return quic.WithPlugins(p...)
}

func WithQUICMaxSessions(max int64) quic.Option {
	return quic.WithMaxSessions(max)
}

func WithQUICMaxMessageSize(max int) quic.Option {
	return quic.WithMaxMessageSize(max)
}

// Error variables re-export
var (
	ErrSkip            = errs.ErrSkip
	ErrDrop            = errs.ErrDrop
	ErrBlock           = errs.ErrBlock
	ErrSessionClosed   = errs.ErrSessionClosed
	ErrWriteQueueFull  = errs.ErrWriteQueueFull
)
