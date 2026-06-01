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
	server := api.NewWebSocketServer(
		api.WithWebSocketAddr("127.0.0.1:18004"),
		api.WithWebSocketPath("/ws"),
		api.WithWebSocketHandler(echoHandler),
	)
	if err := gateway.Register(server); err != nil {
		log.Fatal(err)
	}
	if err := gateway.Start(ctx); err != nil {
		log.Fatal(err)
	}
	log.Println("websocket echo listening on ws://127.0.0.1:18004/ws")

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := gateway.Stop(shutdownCtx); err != nil {
		log.Fatal(err)
	}
}

func echoHandler(sess api.Session, msg api.Message) error {
	return sess.Send(msg.Payload)
}
