package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/X1aSheng/shark-socket/internal/infra/mqtt"
)

func main() {
	brokerURL := os.Getenv("SHARK_MQTT_BROKER")
	if brokerURL == "" {
		log.Fatal("SHARK_MQTT_BROKER environment variable required (e.g., tcp://localhost:1883)")
	}

	adapter, err := mqtt.NewAdapter(
		mqtt.WithBrokerURL(brokerURL),
		mqtt.WithClientID("shark-socket-bridge"),
		mqtt.WithTopic("shark/+/incoming"),
		mqtt.WithQoS(0),
		mqtt.WithConnectTimeout(10*time.Second),
		mqtt.WithMessageHandler(func(topic string, payload []byte) {
			log.Printf("received topic=%q payload=%q", topic, string(payload))
		}),
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := adapter.Start(ctx); err != nil {
		log.Fatal(err)
	}
	log.Printf("mqtt bridge connected to %s", brokerURL)
	log.Printf("subscribed to shark/+/incoming")

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := adapter.Stop(shutdownCtx); err != nil {
		log.Printf("mqtt bridge stop: %v", err)
	}
	log.Println("mqtt bridge stopped")
}
