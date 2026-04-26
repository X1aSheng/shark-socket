package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/X1aSheng/shark-socket/api"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	handler := func(sess api.RawSession, msg api.RawMessage) error {
		log.Printf("received %d bytes from %s", len(msg.Payload), sess.RemoteAddr())
		// Echo the message back to the client
		return sess.Send(msg.Payload)
	}

	srv := api.NewTCPServer(handler, api.WithTCPAddr("0.0.0.0", 18000))

	if err := srv.Start(); err != nil {
		log.Fatalf("Failed to start TCP server: %v", err)
	}

	log.Println("=== Basic TCP Echo Server ===")
	log.Println("Listening on :18000")
	log.Println("")
	log.Println("Test with:")
	log.Println("  nc localhost 18000")
	log.Println("  telnet localhost 18000")
	log.Println("  echo hello | nc -q1 localhost 18000")
	log.Println("")
	log.Println("Press Ctrl+C to stop.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
	// srv.Stop is not accessible through the public API directly,
	// so we rely on process termination.
}
