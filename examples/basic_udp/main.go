package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/X1aSheng/shark-socket/api"
	"github.com/X1aSheng/shark-socket/internal/protocol/udp"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	handler := func(sess api.RawSession, msg api.RawMessage) error {
		log.Printf("received %d bytes from %s", len(msg.Payload), sess.RemoteAddr())
		// Echo the packet back to sender
		return sess.Send(msg.Payload)
	}

	srv := api.NewUDPServer(handler, udp.WithAddr("0.0.0.0", 18200))

	if err := srv.Start(); err != nil {
		log.Fatalf("Failed to start UDP server: %v", err)
	}

	log.Println("=== Basic UDP Echo Server ===")
	log.Println("Listening on :18200 (UDP)")
	log.Println("")
	log.Println("Test with:")
	log.Println("  echo hello | nc -u -q1 localhost 18200")
	log.Println("  echo hello > /dev/udp/localhost/18200   (bash)")
	log.Println("")
	log.Println("Press Ctrl+C to stop.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
}
