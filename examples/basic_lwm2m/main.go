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

	lwm2mServer := api.NewLwM2MServer()

	gateway := api.NewGateway()
	coapServer := api.NewCoAPServer(
		api.WithCoAPAddr("127.0.0.1:18003"),
		api.WithCoAPResponder(api.NewLwM2MCoAPResponder(lwm2mServer)),
	)
	if err := gateway.Register(coapServer); err != nil {
		log.Fatal(err)
	}
	if err := gateway.Start(ctx); err != nil {
		log.Fatal(err)
	}
	log.Println("lwm2m listening on coap://127.0.0.1:18003")
	log.Println("supported commands: register <endpoint> <lifetime> [objects...]")
	log.Println("                  update <endpoint> <lifetime>")
	log.Println("                  deregister <endpoint>")
	log.Println("                  write <endpoint> <path> <value>")
	log.Println("                  read <endpoint> <path>")

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := gateway.Stop(shutdownCtx); err != nil {
		log.Fatal(err)
	}
}
