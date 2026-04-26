package main

import (
	"encoding/json"
	"log"
	stdhttp "net/http"
	"time"

	"github.com/X1aSheng/shark-socket/api"
	"github.com/X1aSheng/shark-socket/internal/protocol/coap"
	"github.com/X1aSheng/shark-socket/internal/protocol/http"
	"github.com/X1aSheng/shark-socket/internal/protocol/tcp"
	"github.com/X1aSheng/shark-socket/internal/protocol/websocket"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Shared handler for TCP, WebSocket, and CoAP:
	// broadcasts every received message to all sessions across protocols.
	sharedHandler := func(sess api.RawSession, msg api.RawMessage) error {
		log.Printf("[%s] session %d: received %d bytes: %s",
			sess.Protocol(), sess.ID(), len(msg.Payload), string(msg.Payload))
		// Echo back to the sender
		return sess.Send(msg.Payload)
	}

	// --- TCP Server (port 8084) ---
	tcpSrv := api.NewTCPServer(sharedHandler,
		tcp.WithAddr("0.0.0.0", 8084),
	)

	// --- HTTP Server (port 8085) ---
	httpSrv := api.NewHTTPServer(http.WithAddr("0.0.0.0", 8085))
	httpSrv.HandleFunc("/hello", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"message": "Hello from multi-protocol gateway!",
		})
	})
	httpSrv.HandleFunc("/health", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
		})
	})

	// --- WebSocket Server (port 8086) ---
	wsSrv := api.NewWebSocketServer(sharedHandler,
		websocket.WithAddr("0.0.0.0", 8086),
		websocket.WithPath("/ws"),
		websocket.WithPingPong(30*time.Second, 10*time.Second),
	)

	// --- CoAP Server (port 5684) ---
	coapSrv := api.NewCoAPServer(sharedHandler,
		coap.WithAddr("0.0.0.0", 5684),
	)

	// --- Gateway ---
	gw := api.NewGateway(
		api.WithShutdownTimeout(15*time.Second),
		api.WithMetricsAddr(":9091"),
		api.WithMetricsEnabled(true),
	)

	if err := gw.Register(tcpSrv); err != nil {
		log.Fatalf("Failed to register TCP: %v", err)
	}
	if err := gw.Register(httpSrv); err != nil {
		log.Fatalf("Failed to register HTTP: %v", err)
	}
	if err := gw.Register(wsSrv); err != nil {
		log.Fatalf("Failed to register WebSocket: %v", err)
	}
	if err := gw.Register(coapSrv); err != nil {
		log.Fatalf("Failed to register CoAP: %v", err)
	}

	log.Println("=== Multi-Protocol Gateway ===")
	log.Println("Registered protocols: TCP, HTTP, WebSocket, CoAP")
	log.Println("")
	log.Println("Endpoints:")
	log.Println("  TCP:        nc localhost 8084")
	log.Println("  HTTP:       curl http://localhost:8085/hello")
	log.Println("  WebSocket:  websocat ws://localhost:8086/ws")
	log.Println("  CoAP:       coap-client coap://localhost:5684")
	log.Println("  Metrics:    curl http://localhost:9091/metrics")
	log.Println("  Health:     curl http://localhost:9091/healthz")
	log.Println("")
	log.Println("Press Ctrl+C to stop (graceful shutdown with 15s timeout).")

	// Run blocks until SIGINT/SIGTERM, then performs graceful shutdown.
	if err := gw.Run(); err != nil {
		log.Fatalf("Gateway error: %v", err)
	}

	log.Println("Gateway stopped cleanly.")
}
