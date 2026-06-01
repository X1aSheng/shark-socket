package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/X1aSheng/shark-socket/api"
)

type LogPlugin struct {
	api.BasePlugin
}

func (p LogPlugin) Name() string { return "log-plugin" }

func (p LogPlugin) Priority() int { return 100 }

func (p LogPlugin) OnAccept(sess api.Session) error {
	log.Printf("accept: id=%d addr=%s proto=%s", sess.ID(), sess.RemoteAddr(), sess.Protocol())
	return nil
}

func (p LogPlugin) OnMessage(sess api.Session, data []byte) ([]byte, error) {
	log.Printf("message: id=%d size=%d", sess.ID(), len(data))
	return data, nil
}

func (p LogPlugin) OnClose(sess api.Session) {
	log.Printf("close: id=%d addr=%s", sess.ID(), sess.RemoteAddr())
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	gateway := api.NewGateway(
		api.WithPlugins(LogPlugin{}),
	)
	server := api.NewTCPServer(
		api.WithTCPAddr("127.0.0.1:18007"),
		api.WithTCPHandler(echoHandler),
	)
	if err := gateway.Register(server); err != nil {
		log.Fatal(err)
	}
	if err := gateway.Start(ctx); err != nil {
		log.Fatal(err)
	}
	log.Println("custom plugin tcp listening on 127.0.0.1:18007")

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
