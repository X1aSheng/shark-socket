package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yourname/shark-socket/api"
	"github.com/yourname/shark-socket/internal/protocol/websocket"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	handler := func(sess api.RawSession, msg api.RawMessage) error {
		log.Printf("received %d bytes from %s (session %d)", len(msg.Payload), sess.RemoteAddr(), sess.ID())
		// Echo the message back
		return sess.Send(msg.Payload)
	}

	srv := api.NewWebSocketServer(handler,
		websocket.WithAddr("0.0.0.0", 8083),
		websocket.WithPath("/ws"),
		websocket.WithPingPong(30*time.Second, 10*time.Second),
	)

	if err := srv.Start(); err != nil {
		log.Fatalf("Failed to start WebSocket server: %v", err)
	}

	log.Println("=== Basic WebSocket Echo Server ===")
	log.Println("Listening on :8083/ws")
	log.Println("")
	log.Println("Test with:")
	log.Println("  websocat ws://localhost:8083/ws")
	log.Println("")
	log.Println("Or use a browser console:")
	log.Println("  const ws = new WebSocket('ws://localhost:8083/ws');")
	log.Println("  ws.onmessage = e => console.log('echo:', e.data);")
	log.Println("  ws.send('hello');")
	log.Println("")
	log.Println("Press Ctrl+C to stop.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
}
