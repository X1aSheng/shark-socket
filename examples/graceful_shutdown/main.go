package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/yourname/shark-socket/api"
	"github.com/yourname/shark-socket/internal/protocol/tcp"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	var (
		connCount int64
		mu        sync.Mutex
	)

	handler := func(sess api.RawSession, msg api.RawMessage) error {
		mu.Lock()
		connCount++
		n := connCount
		mu.Unlock()

		log.Printf("[conn #%d] session %d from %s: %s", n, sess.ID(), sess.RemoteAddr(), string(msg.Payload))
		// Echo back
		return sess.Send(msg.Payload)
	}

	srv := api.NewTCPServer(handler,
		tcp.WithAddr("0.0.0.0", 8088),
		tcp.WithMaxSessions(5000),
		tcp.WithDrainTimeout(10),
	)

	if err := srv.Start(); err != nil {
		log.Fatalf("Failed to start TCP server: %v", err)
	}

	log.Println("=== Graceful Shutdown Demo ===")
	log.Println("Listening on :8088")
	log.Println("")
	log.Println("Test with:")
	log.Println("  nc localhost 8088")
	log.Println("")
	log.Println("Send SIGINT (Ctrl+C) or SIGTERM to trigger graceful shutdown.")
	log.Println("The server will:")
	log.Println("  1. Stop accepting new connections")
	log.Println("  2. Wait for active sessions to finish (up to 10s)")
	log.Println("  3. Close remaining sessions")
	log.Println("")

	// Set up signal handling for graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Wait for shutdown signal.
	sig := <-sigCh
	log.Printf("Received signal: %v — starting graceful shutdown...", sig)

	// Give the server a bounded amount of time to drain active connections.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Stop the server through its Server interface.
	// We access the concrete type to call Stop().
	done := make(chan error, 1)
	go func() {
		done <- srv.Stop(ctx)
	}()

	// Also demonstrate manually triggering a test connection during shutdown.
	go func() {
		time.Sleep(2 * time.Second)
		log.Println("Attempting new connection during shutdown...")
		conn, err := net.DialTimeout("tcp", "localhost:8088", 2*time.Second)
		if err != nil {
			log.Printf("New connection rejected (expected): %v", err)
			return
		}
		conn.Close()
	}()

	select {
	case err := <-done:
		if err != nil {
			log.Printf("Server stopped with error: %v", err)
		} else {
			log.Println("Server stopped gracefully.")
		}
	case <-ctx.Done():
		log.Println("Shutdown timed out — forcing exit.")
	}

	log.Println("Goodbye.")
}
