// Command basic_grpcweb is a minimal gRPC-Web echo server.
//
// gRPC-Web here has two routes, and the difference is easy to trip over:
//
//   - POST /grpc with Content-Type application/grpc-web+proto is the unary
//     route. One request carries one length-prefixed message and gets one back,
//     followed by a trailer. This is the primary path and the one this example
//     logs.
//
//   - The WebSocket route (WithGRPCWebWebSocketMode) is secondary. It registers
//     a DIFFERENT path from the unary one, and pointing both at the same pattern
//     makes the upgrade request land on the unary handler, which answers 400.
//
// Framing, on both routes: each message is a 1-byte compression flag (0 for
// uncompressed) plus a 4-byte big-endian length plus the message bytes. The
// reply is the same framing, and on the unary route a trailer follows.
//
// A matching client is in tests/cross_protocol_test.go, and the load version in
// tests/stress/stress_protocols_test.go (TestStressGRPCWebMessages).
//
// Run:
//
//	go run ./examples/basic_grpcweb
package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/X1aSheng/shark-socket/api"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	gateway := api.NewGateway()
	server := api.NewGRPCWebServer(
		api.WithGRPCWebAddr("127.0.0.1:18006"),
		api.WithGRPCWebHandler(echoHandler),
		// Optional: enable the WebSocket route on its own path. Left commented
		// because enabling it without using it is just another handler to
		// reason about, and the two paths must not collide.
		//
		// api.WithGRPCWebWebSocketMode("/grpc/ws"),
	)
	if err := gateway.Register(server); err != nil {
		log.Fatal(err)
	}
	if err := gateway.Start(ctx); err != nil {
		log.Fatal(err)
	}
	log.Println("grpc-web echo listening on http://127.0.0.1:18006/grpc")
	log.Println("POST with Content-Type application/grpc-web+proto")
	log.Println("frame: 1 byte flag (0=uncompressed) + 4 byte big-endian length + message")

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := gateway.Stop(shutdownCtx); err != nil {
		log.Fatal(err)
	}
}

// echoHandler returns each message to its sender.
//
// The handler sees the decoded message payload, not the frame header; the
// transport strips the flag and length before the handler runs and re-adds them
// on the way out.
func echoHandler(sess api.Session, msg api.Message) error {
	return sess.Send(msg.Payload)
}
