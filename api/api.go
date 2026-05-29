package api

import (
	"context"
	"time"

	"github.com/X1aSheng/shark-socket-new/internal/core"
	"github.com/X1aSheng/shark-socket-new/internal/plugin"
	"github.com/X1aSheng/shark-socket-new/internal/runtime"
	"github.com/X1aSheng/shark-socket-new/internal/transport/coap"
	transporthttp "github.com/X1aSheng/shark-socket-new/internal/transport/http"
	"github.com/X1aSheng/shark-socket-new/internal/transport/tcp"
	"github.com/X1aSheng/shark-socket-new/internal/transport/udp"
	"github.com/X1aSheng/shark-socket-new/internal/transport/websocket"
)

type (
	Protocol            = core.Protocol
	Session             = core.Session
	Message             = core.Message
	Handler             = core.Handler
	Plugin              = core.Plugin
	BasePlugin          = core.BasePlugin
	Gateway             = runtime.Gateway
	GatewayOption       = runtime.GatewayOption
	TCPServer           = tcp.Server
	TCPOption           = tcp.Option
	TCPClient           = tcp.Client
	TCPClientOption     = tcp.ClientOption
	UDPServer           = udp.Server
	UDPOption           = udp.Option
	HTTPServer          = transporthttp.Server
	HTTPOption          = transporthttp.Option
	WebSocketServer     = websocket.Server
	WebSocketOption     = websocket.Option
	CoAPServer          = coap.Server
	CoAPOption          = coap.Option
	BlacklistPlugin     = plugin.Blacklist
	RateLimitPlugin     = plugin.RateLimit
	Logger              = core.Logger
	Metrics             = core.Metrics
	Tracer              = core.Tracer
	StageTimeouts       = core.StageTimeouts
	Codec[M any]        = core.Codec[M]
	TypedHandler[M any] = core.TypedHandler[M]
)

const (
	TCP    = core.ProtocolTCP
	UDP    = core.ProtocolUDP
	HTTP   = core.ProtocolHTTP
	WS     = core.ProtocolWS
	CoAP   = core.ProtocolCoAP
	QUIC   = core.ProtocolQUIC
	Custom = core.ProtocolCustom
)

func NewGateway(opts ...GatewayOption) *Gateway {
	return runtime.NewGateway(opts...)
}

func WithPlugins(plugins ...Plugin) GatewayOption {
	return runtime.WithPlugins(plugins...)
}

func WithLogger(logger Logger) GatewayOption {
	return runtime.WithLogger(logger)
}

func WithMetrics(metrics Metrics) GatewayOption {
	return runtime.WithMetrics(metrics)
}

func WithTracer(tracer Tracer) GatewayOption {
	return runtime.WithTracer(tracer)
}

func WithStageTimeouts(timeouts StageTimeouts) GatewayOption {
	return runtime.WithStageTimeouts(timeouts)
}

func NewTCPServer(opts ...TCPOption) *TCPServer {
	return tcp.NewServer(opts...)
}

func WithTCPAddr(addr string) TCPOption {
	return tcp.WithAddr(addr)
}

func WithTCPHandler(handler Handler) TCPOption {
	return tcp.WithHandler(handler)
}

func NewTCPClient(addr string, opts ...TCPClientOption) *TCPClient {
	return tcp.NewClient(addr, opts...)
}

func NewUDPServer(opts ...UDPOption) *UDPServer {
	return udp.NewServer(opts...)
}

func WithUDPAddr(addr string) UDPOption {
	return udp.WithAddr(addr)
}

func WithUDPHandler(handler Handler) UDPOption {
	return udp.WithHandler(handler)
}

func NewHTTPServer(opts ...HTTPOption) *HTTPServer {
	return transporthttp.NewServer(opts...)
}

func WithHTTPAddr(addr string) HTTPOption {
	return transporthttp.WithAddr(addr)
}

func WithHTTPHandler(handler Handler) HTTPOption {
	return transporthttp.WithHandler(handler)
}

func NewWebSocketServer(opts ...WebSocketOption) *WebSocketServer {
	return websocket.NewServer(opts...)
}

func WithWebSocketAddr(addr string) WebSocketOption {
	return websocket.WithAddr(addr)
}

func WithWebSocketPath(path string) WebSocketOption {
	return websocket.WithPath(path)
}

func WithWebSocketHandler(handler Handler) WebSocketOption {
	return websocket.WithHandler(handler)
}

func NewCoAPServer(opts ...CoAPOption) *CoAPServer {
	return coap.NewServer(opts...)
}

func WithCoAPAddr(addr string) CoAPOption {
	return coap.WithAddr(addr)
}

func WithCoAPHandler(handler Handler) CoAPOption {
	return coap.WithHandler(handler)
}

func NewBlacklistPlugin(entries ...string) *BlacklistPlugin {
	return plugin.NewBlacklist(entries...)
}

func NewRateLimitPlugin(rate int, window time.Duration) *RateLimitPlugin {
	return plugin.NewRateLimit(rate, window)
}

func AdaptTyped[M any](codec Codec[M], handler TypedHandler[M]) Handler {
	return core.AdaptTyped(codec, handler)
}

func Run(ctx context.Context, gateway *Gateway) error {
	return gateway.Start(ctx)
}
