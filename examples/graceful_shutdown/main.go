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

	gateway := api.NewGateway(
		api.WithStageTimeouts(api.StageTimeouts{
			StopAccept:  5 * time.Second,
			Drain:       10 * time.Second,
			CloseSessions: 10 * time.Second,
		}),
	)
	server := api.NewTCPServer(
		api.WithTCPAddr("127.0.0.1:18008"),
		api.WithTCPHandler(echoHandler),
	)
	if err := gateway.Register(server); err != nil {
		log.Fatal(err)
	}
	if err := gateway.Start(ctx); err != nil {
		log.Fatal(err)
	}
	log.Println("graceful shutdown tcp listening on 127.0.0.1:18008")
	log.Println("press Ctrl+C to initiate staged shutdown")

	<-ctx.Done()
	log.Println("shutting down gracefully (stop accept → drain → close sessions)...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	if err := gateway.Stop(shutdownCtx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
	log.Printf("shutdown completed in %s", time.Since(start).Round(time.Millisecond))
}

func echoHandler(sess api.Session, msg api.Message) error {
	return sess.Send(msg.Payload)
}
