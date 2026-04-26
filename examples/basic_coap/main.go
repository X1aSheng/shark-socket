package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/X1aSheng/shark-socket/api"
	"github.com/X1aSheng/shark-socket/internal/protocol/coap"
	"github.com/X1aSheng/shark-socket/internal/types"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	handler := func(sess api.RawSession, msg api.RawMessage) error {
		log.Printf("CoAP request from %s: %d bytes, type=%s", sess.RemoteAddr(), len(msg.Payload), msg.Type)

		switch msg.Type {
		case types.CoAPGet:
			log.Printf("  GET request: %s", string(msg.Payload))
			response := []byte("hello from shark-socket CoAP server")
			return sess.Send(response)

		case types.CoAPPost:
			log.Printf("  POST request: %s", string(msg.Payload))
			response := []byte("received: " + string(msg.Payload))
			return sess.Send(response)

		default:
			log.Printf("  other request type: %s", msg.Type)
			return sess.Send(msg.Payload)
		}
	}

	srv := api.NewCoAPServer(handler,
		coap.WithAddr("0.0.0.0", 18800),
	)

	if err := srv.Start(); err != nil {
		log.Fatalf("Failed to start CoAP server: %v", err)
	}

	log.Println("=== Basic CoAP Server ===")
	log.Println("Listening on :18800 (UDP)")
	log.Println("")
	log.Println("Test with (install coap-client from libcoap):")
	log.Println("  coap-client coap://localhost:18800")
	log.Println("  echo 'test data' | coap-client -m post coap://localhost:18800")
	log.Println("")
	log.Println("Or use Copper (Firefox extension) at coap://localhost:18800")
	log.Println("")
	log.Println("Press Ctrl+C to stop.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
}
