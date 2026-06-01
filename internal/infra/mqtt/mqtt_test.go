package mqtt

import (
	"context"
	"os"
	"testing"
	"time"
)

func brokerURL() string {
	if url := os.Getenv("SHARK_MQTT_BROKER"); url != "" {
		return url
	}
	return ""
}

func TestAdapterConnect(t *testing.T) {
	url := brokerURL()
	if url == "" {
		t.Skip("SHARK_MQTT_BROKER not set, skipping integration test")
	}

	adapter, err := NewAdapter(
		WithBrokerURL(url),
		WithClientID("shark-test-connect"),
		WithConnectTimeout(5*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := adapter.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer adapter.Stop(ctx)

	if !adapter.Connected() {
		t.Fatal("expected connected")
	}
}

func TestAdapterPublishSubscribe(t *testing.T) {
	url := brokerURL()
	if url == "" {
		t.Skip("SHARK_MQTT_BROKER not set, skipping integration test")
	}

	received := make(chan struct {
		topic   string
		payload []byte
	}, 1)

	subAdapter, err := NewAdapter(
		WithBrokerURL(url),
		WithClientID("shark-test-sub"),
		WithConnectTimeout(5*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}

	pubAdapter, err := NewAdapter(
		WithBrokerURL(url),
		WithClientID("shark-test-pub"),
		WithConnectTimeout(5*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := subAdapter.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer subAdapter.Stop(ctx)

	topic := "shark/test/publish"
	if err := subAdapter.Subscribe(topic, 0, func(t string, p []byte) {
		received <- struct {
			topic   string
			payload []byte
		}{t, p}
	}); err != nil {
		t.Fatal(err)
	}

	// Wait for subscription to propagate
	time.Sleep(500 * time.Millisecond)

	if err := pubAdapter.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer pubAdapter.Stop(ctx)

	payload := []byte("hello-from-shark")
	if err := pubAdapter.Publish(topic, 0, payload); err != nil {
		t.Fatal(err)
	}

	select {
	case msg := <-received:
		if msg.topic != topic {
			t.Fatalf("topic = %q, want %q", msg.topic, topic)
		}
		if string(msg.payload) != string(payload) {
			t.Fatalf("payload = %q, want %q", string(msg.payload), string(payload))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for message")
	}
}
