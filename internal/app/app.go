package app

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/X1aSheng/shark-socket-new/api"
)

type App struct {
	Config      Config
	Gateway     *api.Gateway
	Metrics     *api.PrometheusMetrics
	Health      *http.Server
	MetricsHTTP *http.Server
	Protocols   []string
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
			ReadHeaderTimeout: 5 * time.Second,
		}
	}
	if cfg.MetricsAddr != "" {
		app.MetricsHTTP = &http.Server{
			Addr:              cfg.MetricsAddr,
			Handler:           metrics,
			ReadHeaderTimeout: 5 * time.Second,
		}
	}
	return app, nil
}

func (a *App) Start(ctx context.Context) error {
	if a.Health != nil {
		go serveHTTP("health", a.Health)
	}
	if a.MetricsHTTP != nil {
		go serveHTTP("metrics", a.MetricsHTTP)
	}
	return a.Gateway.Start(ctx)
}

func (a *App) Stop(ctx context.Context) error {
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
			server = api.NewTCPServer(api.WithTCPAddr(proto.Addr), api.WithTCPHandler(echoHandler))
		case "udp":
			server = api.NewUDPServer(api.WithUDPAddr(proto.Addr), api.WithUDPHandler(echoHandler))
		case "http":
			server = api.NewHTTPServer(api.WithHTTPAddr(proto.Addr), api.WithHTTPHandler(echoHandler))
		case "websocket":
			path := proto.Path
			if path == "" {
				path = "/ws"
			}
			server = api.NewWebSocketServer(api.WithWebSocketAddr(proto.Addr), api.WithWebSocketPath(path), api.WithWebSocketHandler(echoHandler))
		case "coap":
			if strings.EqualFold(proto.Mode, "lwm2m") {
				server = api.NewCoAPServer(api.WithCoAPAddr(proto.Addr), api.WithCoAPResponder(api.NewLwM2MCoAPResponder(lwm2mServer)))
			} else {
				server = api.NewCoAPServer(api.WithCoAPAddr(proto.Addr), api.WithCoAPHandler(echoHandler))
			}
		case "grpc-web":
			opts := []api.GRPCWebOption{api.WithGRPCWebAddr(proto.Addr), api.WithGRPCWebHandler(echoHandler)}
			if proto.MaxMessageBytes > 0 {
				opts = append(opts, api.WithGRPCWebMaxMessageBytes(proto.MaxMessageBytes))
			}
			if proto.Path != "" {
				opts = append(opts, api.WithGRPCWebWebSocketMode(proto.Path))
			}
			server = api.NewGRPCWebServer(opts...)
		default:
			return fmt.Errorf("unsupported protocol %q", proto.Name)
		}
		if err := a.Gateway.Register(server); err != nil {
			return err
		}
		a.Protocols = append(a.Protocols, name)
	}
	return nil
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

func serveHTTP(name string, server *http.Server) {
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("%s server failed: %v", name, err)
	}
}
