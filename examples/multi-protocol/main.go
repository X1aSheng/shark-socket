package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/X1aSheng/shark-socket-new/api"
	"go.opentelemetry.io/otel"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	metrics := api.NewPrometheusMetrics()
	gateway := api.NewGateway(
		api.WithMetrics(metrics),
		api.WithTracer(api.NewOpenTelemetryTracer(otel.Tracer("shark-socket-new/example"))),
	)

	registerOrExit(gateway, api.NewTCPServer(
		api.WithTCPAddr("127.0.0.1:18000"),
		api.WithTCPHandler(echoHandler),
	))
	registerOrExit(gateway, api.NewWebSocketServer(
		api.WithWebSocketAddr("127.0.0.1:18001"),
		api.WithWebSocketPath("/ws"),
		api.WithWebSocketHandler(echoHandler),
	))

	lwm2mServer := api.NewLwM2MServer()
	registerOrExit(gateway, api.NewCoAPServer(
		api.WithCoAPAddr("127.0.0.1:18002"),
		api.WithCoAPResponder(api.NewLwM2MCoAPResponder(lwm2mServer)),
	))

	metricsServer := &http.Server{
		Addr:              "127.0.0.1:18080",
		Handler:           metrics,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("metrics server failed: %v", err)
		}
	}()

	if err := gateway.Start(ctx); err != nil {
		log.Fatal(err)
	}
	log.Println("tcp echo listening on 127.0.0.1:18000")
	log.Println("websocket echo listening on ws://127.0.0.1:18001/ws")
	log.Println("coap/lwm2m listening on 127.0.0.1:18002")
	log.Println("prometheus metrics listening on http://127.0.0.1:18080/metrics")

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := gateway.Stop(shutdownCtx); err != nil {
		log.Printf("gateway shutdown failed: %v", err)
	}
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("metrics shutdown failed: %v", err)
	}
}

func registerOrExit(gateway *api.Gateway, server interface {
	api.Server
}) {
	if err := gateway.Register(server); err != nil {
		log.Fatal(err)
	}
}

func echoHandler(sess api.Session, msg api.Message) error {
	return sess.Send(msg.Payload)
}
