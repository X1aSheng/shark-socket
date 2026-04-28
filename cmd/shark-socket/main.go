package main

import (
	"encoding/json"
	"log"
	"os"
	"strconv"
	"time"

	stdhttp "net/http"

	"github.com/X1aSheng/shark-socket/api"
	"github.com/X1aSheng/shark-socket/internal/gateway"
	"github.com/X1aSheng/shark-socket/internal/protocol/coap"
	"github.com/X1aSheng/shark-socket/internal/protocol/grpcgw"
	"github.com/X1aSheng/shark-socket/internal/protocol/http"
	"github.com/X1aSheng/shark-socket/internal/protocol/tcp"
	"github.com/X1aSheng/shark-socket/internal/protocol/udp"
	"github.com/X1aSheng/shark-socket/internal/protocol/websocket"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	handler := func(sess api.RawSession, msg api.RawMessage) error {
		log.Printf("[%s] session %d: received %d bytes",
			sess.Protocol(), sess.ID(), len(msg.Payload))
		return sess.Send(msg.Payload)
	}

	gw := api.NewGateway(
		api.WithShutdownTimeout(envDuration("SHARK_SHUTDOWN_TIMEOUT", 30*time.Second)),
		api.WithMetricsAddr(envStr("SHARK_METRICS_ADDR", ":9091")),
		api.WithMetricsEnabled(envBool("SHARK_METRICS_ENABLED", true)),
	)

	registered := 0

	if envBool("SHARK_TCP_ENABLED", true) {
		srv := api.NewTCPServer(handler,
			tcp.WithAddr(envStr("SHARK_TCP_HOST", "0.0.0.0"), envInt("SHARK_TCP_PORT", 18000)),
		)
		mustRegister(gw, srv, "TCP")
		registered++
	}

	if envBool("SHARK_UDP_ENABLED", true) {
		srv := api.NewUDPServer(handler,
			udp.WithAddr(envStr("SHARK_UDP_HOST", "0.0.0.0"), envInt("SHARK_UDP_PORT", 18200)),
		)
		mustRegister(gw, srv, "UDP")
		registered++
	}

	if envBool("SHARK_HTTP_ENABLED", true) {
		srv := api.NewHTTPServer(
			http.WithAddr(envStr("SHARK_HTTP_HOST", "0.0.0.0"), envInt("SHARK_HTTP_PORT", 18400)),
		)
		srv.HandleFunc("/hello", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"message": "Hello from shark-socket gateway!",
			})
		})
		srv.HandleFunc("/health", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		})
		mustRegister(gw, srv, "HTTP")
		registered++
	}

	if envBool("SHARK_WS_ENABLED", true) {
		srv := api.NewWebSocketServer(handler,
			websocket.WithAddr(envStr("SHARK_WS_HOST", "0.0.0.0"), envInt("SHARK_WS_PORT", 18600)),
			websocket.WithPath(envStr("SHARK_WS_PATH", "/ws")),
			websocket.WithPingPong(30*time.Second, 10*time.Second),
		)
		mustRegister(gw, srv, "WebSocket")
		registered++
	}

	if envBool("SHARK_COAP_ENABLED", true) {
		srv := api.NewCoAPServer(handler,
			coap.WithAddr(envStr("SHARK_COAP_HOST", "0.0.0.0"), envInt("SHARK_COAP_PORT", 18800)),
		)
		mustRegister(gw, srv, "CoAP")
		registered++
	}

	// QUIC requires TLS certificates, disabled by default.
	if envBool("SHARK_QUIC_ENABLED", false) {
		log.Println("QUIC is enabled but requires TLS certificates configured.")
		log.Println("Set SHARK_QUIC_TLS_CERT and SHARK_QUIC_TLS_KEY, then rebuild with quic support.")
	}

	if envBool("SHARK_GRPCWEB_ENABLED", false) {
		srv := api.NewGRPCWebGateway(
			grpcgw.WithAddr(envStr("SHARK_GRPCWEB_HOST", "0.0.0.0"), envInt("SHARK_GRPCWEB_PORT", 18650)),
		)
		mustRegister(gw, srv, "gRPC-Web")
		registered++
	}

	if registered == 0 {
		log.Fatal("No protocols enabled. Set at least one SHARK_*_ENABLED=true.")
	}

	log.Println("=== shark-socket Gateway ===")
	log.Println("Endpoints:")
	if envBool("SHARK_TCP_ENABLED", true) {
		log.Printf("  TCP:        nc localhost %d", envInt("SHARK_TCP_PORT", 18000))
	}
	if envBool("SHARK_UDP_ENABLED", true) {
		log.Printf("  UDP:        nc -u localhost %d", envInt("SHARK_UDP_PORT", 18200))
	}
	if envBool("SHARK_HTTP_ENABLED", true) {
		log.Printf("  HTTP:       curl http://localhost:%d/hello", envInt("SHARK_HTTP_PORT", 18400))
	}
	if envBool("SHARK_WS_ENABLED", true) {
		log.Printf("  WebSocket:  websocat ws://localhost:%d%s",
			envInt("SHARK_WS_PORT", 18600), envStr("SHARK_WS_PATH", "/ws"))
	}
	if envBool("SHARK_COAP_ENABLED", true) {
		log.Printf("  CoAP:       coap-client coap://localhost:%d", envInt("SHARK_COAP_PORT", 18800))
	}
	if envBool("SHARK_QUIC_ENABLED", false) {
		log.Printf("  QUIC:       localhost:%d", envInt("SHARK_QUIC_PORT", 18900))
	}
	if envBool("SHARK_GRPCWEB_ENABLED", false) {
		log.Printf("  gRPC-Web:   curl http://localhost:%d/grpc", envInt("SHARK_GRPCWEB_PORT", 18650))
	}
	metricsAddr := envStr("SHARK_METRICS_ADDR", ":9091")
	log.Printf("  Metrics:    curl http://localhost%s/metrics", metricsAddr)
	log.Printf("  Health:     curl http://localhost%s/healthz", metricsAddr)
	log.Println("Press Ctrl+C to stop.")

	if err := gw.Run(); err != nil {
		log.Fatalf("Gateway error: %v", err)
	}
	log.Println("Gateway stopped cleanly.")
}

func mustRegister(gw *gateway.Gateway, srv api.Server, name string) {
	if err := gw.Register(srv); err != nil {
		log.Fatalf("Failed to register %s: %v", name, err)
	}
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		switch v {
		case "1", "true", "TRUE", "yes", "YES", "on", "ON":
			return true
		case "0", "false", "FALSE", "no", "NO", "off", "OFF":
			return false
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
