package api

import (
	"context"
	"crypto/tls"
	"net/http"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/infra/observability"
	"github.com/X1aSheng/shark-socket/internal/infra/pubsub"
	"github.com/X1aSheng/shark-socket/internal/infra/store"
	"github.com/X1aSheng/shark-socket/internal/plugin"
	"github.com/X1aSheng/shark-socket/internal/protocol/lwm2m"
	"github.com/X1aSheng/shark-socket/internal/runtime"
	"github.com/X1aSheng/shark-socket/internal/transport/coap"
	"github.com/X1aSheng/shark-socket/internal/transport/grpcweb"
	transporthttp "github.com/X1aSheng/shark-socket/internal/transport/http"
	"github.com/X1aSheng/shark-socket/internal/transport/quic"
	"github.com/X1aSheng/shark-socket/internal/transport/tcp"
	"github.com/X1aSheng/shark-socket/internal/transport/udp"
	"github.com/X1aSheng/shark-socket/internal/transport/websocket"
	"go.opentelemetry.io/otel/trace"
)

type (
	Protocol            = core.Protocol
	Session             = core.Session
	SessionManager      = core.SessionManager
	Server              = core.Server
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
	LwM2MServer         = lwm2m.Server
	LwM2MServerOption   = lwm2m.ServerOption
	LwM2MClient         = lwm2m.Client
	LwM2MClientOption   = lwm2m.ClientOption
	LwM2MObjectPath     = lwm2m.ObjectPath
	LwM2MRegistration   = lwm2m.Registration
	LwM2MResource       = lwm2m.Resource
	QUICServer          = quic.Server
	QUICOption          = quic.Option
	GRPCWebServer       = grpcweb.Server
	GRPCWebOption       = grpcweb.Option
	BlacklistPlugin     = plugin.Blacklist
	RateLimitPlugin     = plugin.RateLimit
	AutoBanPlugin       = plugin.AutoBan
	ClusterPlugin       = plugin.Cluster
	HeartbeatPlugin     = plugin.Heartbeat
	PersistencePlugin     = plugin.Persistence
	PersistenceV2Plugin   = plugin.PersistenceV2
	SlowHandlerOption     = plugin.SlowHandlerOption
	PrometheusMetrics   = observability.PrometheusMetrics
	OpenTelemetryTracer = observability.OpenTelemetryTracer
	PubSub              = pubsub.PubSub
	StoreV2             = store.StoreV2
	BoltStore           = store.BoltStore
	MemoryStore         = store.Memory
	MessageLog          = store.MessageLog
	SessionStore        = store.SessionStore
	SessionSnapshot     = store.SessionSnapshot
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

func WithTCPTLS(config *tls.Config) TCPOption {
	return tcp.WithTLS(config)
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

func WithUDPDTLS(config *tls.Config) UDPOption {
	return udp.WithDTLS(config)
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

func WithHTTPCORSAllowedOrigins(origins []string) HTTPOption {
	return transporthttp.WithCORSAllowedOrigins(origins)
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

func WithWebSocketCheckOrigin(fn func(*http.Request) bool) WebSocketOption {
	return websocket.WithCheckOrigin(fn)
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

func WithCoAPDTLS(config *tls.Config) CoAPOption {
	return coap.WithDTLS(config)
}

func WithCoAPResponder(responder func(Session, Message) ([]byte, error)) CoAPOption {
	return coap.WithResponder(responder)
}

func NewLwM2MServer(opts ...LwM2MServerOption) *LwM2MServer {
	return lwm2m.NewServer(opts...)
}

func NewLwM2MClient(endpoint string, server *LwM2MServer, opts ...LwM2MClientOption) *LwM2MClient {
	return lwm2m.NewClient(endpoint, server, opts...)
}

func ParseLwM2MPath(path string) (LwM2MObjectPath, error) {
	return lwm2m.ParsePath(path)
}

func NewLwM2MCoAPResponder(server *LwM2MServer) func(Session, Message) ([]byte, error) {
	return lwm2m.NewCoAPResponder(server)
}

func NewQUICServer(opts ...QUICOption) *QUICServer {
	return quic.NewServer(opts...)
}

func WithQUICAddr(addr string) QUICOption {
	return quic.WithAddr(addr)
}

func WithQUICTLS(config *tls.Config) QUICOption {
	return quic.WithTLS(config)
}

func WithQUICHandler(handler Handler) QUICOption {
	return quic.WithHandler(handler)
}

func NewGRPCWebServer(opts ...GRPCWebOption) *GRPCWebServer {
	return grpcweb.NewServer(opts...)
}

func WithGRPCWebAddr(addr string) GRPCWebOption {
	return grpcweb.WithAddr(addr)
}

func WithGRPCWebHandler(handler Handler) GRPCWebOption {
	return grpcweb.WithHandler(handler)
}

func WithGRPCWebMaxMessageBytes(max int64) GRPCWebOption {
	return grpcweb.WithMaxMessageBytes(max)
}

func WithGRPCWebWebSocketMode(path string) GRPCWebOption {
	return grpcweb.WithWebSocketMode(path)
}

func WithGRPCWebCheckOrigin(fn func(*http.Request) bool) GRPCWebOption {
	return grpcweb.WithCheckOrigin(fn)
}

func NewBlacklistPlugin(entries ...string) *BlacklistPlugin {
	return plugin.NewBlacklist(entries...)
}

func NewRateLimitPlugin(rate int, window time.Duration) *RateLimitPlugin {
	return plugin.NewRateLimit(rate, window)
}

func NewAutoBanPlugin(threshold int) *AutoBanPlugin {
	return plugin.NewAutoBan(threshold)
}

func NewClusterPlugin(nodeID string, bus *PubSub, manager SessionManager) *ClusterPlugin {
	return plugin.NewCluster(nodeID, bus, manager)
}

func NewPubSub() *PubSub {
	return pubsub.New()
}

func NewHeartbeatPlugin(manager SessionManager, timeout time.Duration) *HeartbeatPlugin {
	return plugin.NewHeartbeat(manager, timeout)
}

func NewPersistencePlugin(s store.Store, bucket string) *PersistencePlugin {
	return plugin.NewPersistence(s, bucket)
}

func NewPersistenceV2Plugin(s StoreV2, bucket string) *PersistenceV2Plugin {
	return plugin.NewPersistenceV2(s, bucket)
}

func NewMemoryStore() *MemoryStore {
	return store.NewMemory()
}

func NewBoltStore(path string) (*BoltStore, error) {
	return store.NewBoltStore(path)
}

func NewMessageLog(s StoreV2, bucket string) (*MessageLog, error) {
	return store.NewMessageLog(s, bucket)
}

func NewSessionStore(s StoreV2, bucket string) *SessionStore {
	return store.NewSessionStore(s, bucket)
}

func NewSlowHandler(logger Logger, threshold time.Duration, next Handler, opts ...SlowHandlerOption) Handler {
	return plugin.NewSlowHandler(logger, threshold, next, opts...)
}

func NewPrometheusMetrics() *PrometheusMetrics {
	return observability.NewPrometheusMetrics()
}

func NewOpenTelemetryTracer(tracer trace.Tracer) *OpenTelemetryTracer {
	return observability.NewOpenTelemetryTracer(tracer)
}

func AdaptTyped[M any](codec Codec[M], handler TypedHandler[M]) Handler {
	return core.AdaptTyped(codec, handler)
}

func Run(ctx context.Context, gateway *Gateway) error {
	return gateway.Start(ctx)
}

