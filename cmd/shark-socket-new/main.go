package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/X1aSheng/shark-socket-new/api"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	gw := api.NewGateway()
	if err := gw.Register(api.NewTCPServer(
		api.WithTCPAddr("127.0.0.1:18000"),
		api.WithTCPHandler(func(sess api.Session, msg api.Message) error {
			return sess.Send(msg.Payload)
		}),
	)); err != nil {
		log.Fatal(err)
	}

	if err := gw.Start(ctx); err != nil {
		log.Fatal(err)
	}
	log.Println("shark-socket-new listening on tcp://127.0.0.1:18000")

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := gw.Stop(shutdownCtx); err != nil {
		log.Fatal(err)
	}
}
