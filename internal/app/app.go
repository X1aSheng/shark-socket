package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/X1aSheng/shark-socket/api"
	"github.com/X1aSheng/shark-socket/internal/infra/tlsutil"
)

type App struct {
	Config       Config
	Gateway      *api.Gateway
	Metrics      *api.PrometheusMetrics
	Health       *http.Server
	MetricsHTTP  *http.Server
	Protocols    []string
	serveErrors  []error
	serveMu      sync.Mutex
	certCaches   []*tlsutil.CertCache
	certWatchers []context.CancelFunc
	certWG       sync.WaitGroup
	appCtx       context.Context
	appCancel    context.CancelFunc
}

func New(cfg Config) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	metrics := api.NewPrometheusMetrics()
	gateway := api.NewGateway(api.WithMetrics(metrics))
	app := &App{Config: cfg, Gateway: gateway, Metrics: metrics}
	if err := app.registerProtocols(cfg.Protocols); err != nil {
		return nil, err
	}
	if cfg.HealthAddr != "" {
		app.Health = &http.Server{
			Addr:              cfg.HealthAddr,
			Handler:           healthHandler(gateway),
			ReadTimeout:       5 * time.Second,
			WriteTimeout:      5 * time.Second,
			IdleTimeout:       30 * time.Second,
			ReadHeaderTimeout: 5 * time.Second,
		}
	}
	if cfg.MetricsAddr != "" {
		app.MetricsHTTP = &http.Server{
			Addr:              cfg.MetricsAddr,
			Handler:           metrics,
			ReadTimeout:       5 * time.Second,
			WriteTimeout:      5 * time.Second,
			IdleTimeout:       30 * time.Second,
			ReadHeaderTimeout: 5 * time.Second,
		}
	}
	return app, nil
}

func (a *App) Start(ctx context.Context) error {
	a.appCtx, a.appCancel = context.WithCancel(ctx)
	a.serveMu.Lock()
	a.serveErrors = nil
	a.serveMu.Unlock()
	if a.Health != nil {
		go a.serveHTTP("health", a.Health)
	}
	if a.MetricsHTTP != nil {
		go a.serveHTTP("metrics", a.MetricsHTTP)
	}
	return a.Gateway.Start(ctx)
}

// ServeErrors returns errors from the health and metrics HTTP servers.
// Call after Start() to detect port conflicts or permission errors.
func (a *App) ServeErrors() []error {
	a.serveMu.Lock()
	defer a.serveMu.Unlock()
	out := make([]error, len(a.serveErrors))
	copy(out, a.serveErrors)
	return out
}

func (a *App) Stop(ctx context.Context) error {
	if a.appCancel != nil {
		a.appCancel()
	}
	for _, cancel := range a.certWatchers {
		cancel()
	}
	a.certWG.Wait()
	var firstErr error
	if err := a.Gateway.Stop(ctx); err != nil && firstErr == nil {
		firstErr = err
	}
	if a.Health != nil {
		if err := a.Health.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if a.MetricsHTTP != nil {
		if err := a.MetricsHTTP.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (a *App) registerProtocols(protocols []ProtocolConfig) error {
	lwm2mServer := api.NewLwM2MServer()
	for _, proto := range protocols {
		if !proto.IsEnabled() {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(proto.Name))
		var server api.Server
		switch name {
		case "tcp":
			opts := []api.TCPOption{api.WithTCPAddr(proto.Addr), api.WithTCPHandler(echoHandler)}
			if proto.TLSCertFile != "" || proto.TLSKeyFile != "" {
				tlsConfig, cache, err := loadServerTLSConfig(proto)
				if err != nil {
					return err
				}
				opts = append(opts, api.WithTCPTLS(tlsConfig))
				a.certCaches = append(a.certCaches, cache)
			}
			server = api.NewTCPServer(opts...)
		case "udp":
			opts := []api.UDPOption{api.WithUDPAddr(proto.Addr), api.WithUDPHandler(echoHandler)}
			if proto.TLSCertFile != "" || proto.TLSKeyFile != "" {
				tlsConfig, cache, err := loadServerTLSConfig(proto)
				if err != nil {
					return err
				}
				opts = append(opts, api.WithUDPDTLS(tlsConfig))
				a.certCaches = append(a.certCaches, cache)
			}
			server = api.NewUDPServer(opts...)
		case "http":
			opts := []api.HTTPOption{api.WithHTTPAddr(proto.Addr), api.WithHTTPHandler(echoHandler)}
			if len(proto.AllowedOrigins) > 0 {
				opts = append(opts, api.WithHTTPCORSAllowedOrigins(proto.AllowedOrigins))
			}
			server = api.NewHTTPServer(opts...)
		case "websocket":
			path := proto.Path
			if path == "" {
				path = "/ws"
			}
			opts := []api.WebSocketOption{api.WithWebSocketAddr(proto.Addr), api.WithWebSocketPath(path), api.WithWebSocketHandler(echoHandler)}
			if len(proto.AllowedOrigins) > 0 {
				opts = append(opts, api.WithWebSocketCheckOrigin(allowedOriginChecker(proto.AllowedOrigins)))
			}
			server = api.NewWebSocketServer(opts...)
		case "coap":
			coapOpts := []api.CoAPOption{api.WithCoAPAddr(proto.Addr)}
			if proto.TLSCertFile != "" || proto.TLSKeyFile != "" {
				tlsConfig, cache, err := loadServerTLSConfig(proto)
				if err != nil {
					return err
				}
				coapOpts = append(coapOpts, api.WithCoAPDTLS(tlsConfig))
				a.certCaches = append(a.certCaches, cache)
			}
			if strings.EqualFold(proto.Mode, "lwm2m") {
				coapOpts = append(coapOpts, api.WithCoAPResponder(api.NewLwM2MCoAPResponder(lwm2mServer)))
			} else {
				coapOpts = append(coapOpts, api.WithCoAPHandler(echoHandler))
			}
			coapSrv := api.NewCoAPServer(coapOpts...)
			if strings.EqualFold(proto.Mode, "lwm2m") {
				lwm2mServer.OnWrite = coapSrv.NotifyObservers
			}
			server = coapSrv
		case "grpc-web":
			opts := []api.GRPCWebOption{api.WithGRPCWebAddr(proto.Addr), api.WithGRPCWebHandler(echoHandler)}
			if proto.MaxMessageBytes > 0 {
				opts = append(opts, api.WithGRPCWebMaxMessageBytes(proto.MaxMessageBytes))
			}
			if proto.Path != "" {
				opts = append(opts, api.WithGRPCWebWebSocketMode(proto.Path))
			}
			if len(proto.AllowedOrigins) > 0 {
				opts = append(opts, api.WithGRPCWebCheckOrigin(allowedOriginChecker(proto.AllowedOrigins)))
			}
			server = api.NewGRPCWebServer(opts...)
		case "quic":
			tlsConfig, cache, err := loadServerTLSConfig(proto, "shark-socket-quic")
			if err != nil {
				return err
			}
			server = api.NewQUICServer(api.WithQUICAddr(proto.Addr), api.WithQUICTLS(tlsConfig), api.WithQUICHandler(echoHandler))
			a.certCaches = append(a.certCaches, cache)
		default:
			return fmt.Errorf("unsupported protocol %q", proto.Name)
		}
		if err := a.Gateway.Register(server); err != nil {
			return err
		}
		a.Protocols = append(a.Protocols, name)
	}

	// Start cert file watchers for hot-reload
	for _, cache := range a.certCaches {
		c := cache
		if a.appCtx == nil {
			a.appCtx = context.Background()
		}
		cancel := tlsutil.WatchFilesWithWG(a.appCtx, 30*time.Second, func() {
			if err := c.Load(); err != nil {
				a.Gateway.Runtime().Logger().Error("cert reload failed", "error", err)
			} else {
				a.Gateway.Runtime().Logger().Info("cert reload successful")
			}
		}, &a.certWG, c.Files()...)
		a.certWatchers = append(a.certWatchers, cancel)
	}

	return nil
}

// allowedOriginChecker returns a CheckOrigin function for the given allowed origins.
// Using "*" as an origin allows all origins — only use in development.
func allowedOriginChecker(allowed []string) func(*http.Request) bool {
	set := make(map[string]struct{}, len(allowed))
	allowAll := false
	for _, origin := range allowed {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			continue
		}
		if origin == "*" {
			allowAll = true
			continue
		}
		set[origin] = struct{}{}
	}
	return func(r *http.Request) bool {
		if allowAll {
			return true
		}
		origin := r.Header.Get("Origin")
		_, ok := set[origin]
		return ok
	}
}

func healthHandler(gateway *api.Gateway) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !gateway.Ready() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready\n"))
	})
	return mux
}

func echoHandler(sess api.Session, msg api.Message) error {
	return sess.Send(msg.Payload)
}

func (a *App) serveHTTP(name string, server *http.Server) {
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		a.serveMu.Lock()
		a.serveErrors = append(a.serveErrors, fmt.Errorf("%s: %w", name, err))
		a.serveMu.Unlock()
		a.Gateway.Runtime().Logger().Error("server failed", "name", name, "error", err)
	}
}
