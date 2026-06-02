package mqtt

import (
	"crypto/tls"
	"testing"
	"time"
)

func TestNewAdapterMissingBrokerURL(t *testing.T) {
	_, err := NewAdapter()
	if err == nil {
		t.Fatal("expected error for missing broker URL")
	}
}

func TestNewAdapterWithOptions(t *testing.T) {
	a, err := NewAdapter(
		WithBrokerURL("tcp://localhost:1883"),
		WithClientID("test-client"),
		WithConnectTimeout(5*time.Second),
		WithQoS(1),
		WithTopic("test/topic"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if a == nil {
		t.Fatal("adapter should not be nil")
	}
}

func TestNewAdapterWithCredentials(t *testing.T) {
	a, err := NewAdapter(
		WithBrokerURL("tcp://localhost:1883"),
		WithClientID("auth-test"),
		WithCredentials("user", "pass"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if a == nil {
		t.Fatal("adapter with credentials should not be nil")
	}
}

func TestNewAdapterWithTLS(t *testing.T) {
	a, err := NewAdapter(
		WithBrokerURL("tls://localhost:8883"),
		WithClientID("tls-test"),
		WithTLS(&tls.Config{InsecureSkipVerify: true}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if a == nil {
		t.Fatal("adapter with TLS should not be nil")
	}
}

func TestNewAdapterWithHandler(t *testing.T) {
	a, err := NewAdapter(
		WithBrokerURL("tcp://localhost:1883"),
		WithClientID("handler-test"),
		WithMessageHandler(func(topic string, payload []byte) {
			// mock handler
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if a == nil {
		t.Fatal("adapter with handler should not be nil")
	}
}

func TestAdapterNotConnectedBeforeStart(t *testing.T) {
	a, err := NewAdapter(WithBrokerURL("tcp://localhost:1883"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Connected() {
		t.Fatal("adapter should not be connected before Start")
	}
}

func TestAdapterStopBeforeStart(t *testing.T) {
	a, err := NewAdapter(WithBrokerURL("tcp://localhost:1883"))
	if err != nil {
		t.Fatal(err)
	}
	// Stop before Start should be safe (no-op)
	if err := a.Stop(nil); err != nil {
		t.Fatal(err)
	}
}

func TestAdapterPublishBeforeStart(t *testing.T) {
	a, err := NewAdapter(WithBrokerURL("tcp://localhost:1883"))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Publish("test", 0, []byte("data")); err == nil {
		t.Fatal("expected error for publish before start")
	}
}

func TestAdapterSubscribeBeforeStart(t *testing.T) {
	a, err := NewAdapter(WithBrokerURL("tcp://localhost:1883"))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Subscribe("test", 0, func(topic string, payload []byte) {}); err == nil {
		t.Fatal("expected error for subscribe before start")
	}
}

func TestDefaultOptions(t *testing.T) {
	opts := defaultOptions()
	if opts.BrokerURL != "" {
		t.Fatalf("default BrokerURL = %q, want empty", opts.BrokerURL)
	}
	if opts.ClientID != "shark-socket-mqtt" {
		t.Fatalf("default ClientID = %q, want shark-socket-mqtt", opts.ClientID)
	}
	if opts.ConnectTimeout != 10*time.Second {
		t.Fatalf("default ConnectTimeout = %v, want 10s", opts.ConnectTimeout)
	}
	if opts.QoS != 0 {
		t.Fatalf("default QoS = %d, want 0", opts.QoS)
	}
}

func TestPahoOptionsConversion(t *testing.T) {
	opts := Options{
		BrokerURL:      "tcp://broker:1883",
		ClientID:       "test-id",
		Username:       "admin",
		Password:       "secret",
		ConnectTimeout: 5 * time.Second,
	}
	po := pahoOptions(opts)
	if po == nil {
		t.Fatal("paho options should not be nil")
	}
	// Verify key fields are set
	if len(po.Servers) == 0 {
		t.Fatal("paho servers should not be empty")
	}
}

func TestPahoOptionsWithTLS(t *testing.T) {
	opts := Options{
		BrokerURL: "tls://broker:8883",
		ClientID:  "tls-client",
		TLSConfig: &tls.Config{InsecureSkipVerify: true},
	}
	po := pahoOptions(opts)
	if po == nil {
		t.Fatal("paho options should not be nil")
	}
}

func TestPahoOptionsWithEmptyCredentials(t *testing.T) {
	opts := Options{
		BrokerURL: "tcp://broker:1883",
		ClientID:  "no-auth",
		// Username and Password empty
	}
	po := pahoOptions(opts)
	if po == nil {
		t.Fatal("paho options should not be nil")
	}
}
