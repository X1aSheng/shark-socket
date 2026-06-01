package main

import (
	"context"
	"encoding/json"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/X1aSheng/shark-socket/api"
)

type Greeting struct {
	Text   string `json:"text"`
	Sender string `json:"sender"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	handler := api.Handler(func(sess api.Session, msg api.Message) error {
		var greeting Greeting
		if err := json.Unmarshal(msg.Payload, &greeting); err != nil {
			return err
		}
		log.Printf("received from %s: %s", greeting.Sender, greeting.Text)
		reply := Greeting{Text: "hello back", Sender: "shark-socket"}
		data, err := json.Marshal(reply)
		if err != nil {
			return err
		}
		return sess.Send(data)
	})

	gateway := api.NewGateway()
	server := api.NewTCPServer(
		api.WithTCPAddr("127.0.0.1:18006"),
		api.WithTCPHandler(handler),
	)
	if err := gateway.Register(server); err != nil {
		log.Fatal(err)
	}
	if err := gateway.Start(ctx); err != nil {
		log.Fatal(err)
	}
	log.Println("typed handler tcp listening on 127.0.0.1:18006")
	log.Println(`send JSON: {"text":"hello","sender":"client"}`)

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := gateway.Stop(shutdownCtx); err != nil {
		log.Fatal(err)
	}
}
