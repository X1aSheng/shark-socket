package mqtt

import (
	"context"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// ---------------------------------------------------------------------------
// A mock client that captures the subscribed handler, so the inbound path can
// be exercised without a broker.
// ---------------------------------------------------------------------------

type captureToken struct{ ch chan struct{} }

func newCaptureToken() *captureToken {
	t := &captureToken{ch: make(chan struct{})}
	close(t.ch)
	return t
}

func (t *captureToken) Wait() bool                     { return true }
func (t *captureToken) WaitTimeout(time.Duration) bool { return true }
func (t *captureToken) Done() <-chan struct{}          { return t.ch }
func (t *captureToken) Error() error                   { return nil }

type captureClient struct {
	handler   paho.MessageHandler
	connected bool
}

func (c *captureClient) IsConnected() bool          { return c.connected }
func (c *captureClient) Disconnect(uint)            { c.connected = false }
func (c *captureClient) Connect() paho.Token        { c.connected = true; return newCaptureToken() }
func (c *captureClient) Publish(string, byte, bool, interface{}) paho.Token {
	return newCaptureToken()
}
func (c *captureClient) Subscribe(_ string, _ byte, h paho.MessageHandler) paho.Token {
	c.handler = h
	return newCaptureToken()
}

type fakeMessage struct {
	topic   string
	payload []byte
}

func (m *fakeMessage) Duplicate() bool   { return false }
func (m *fakeMessage) Qos() byte         { return 0 }
func (m *fakeMessage) Retained() bool    { return false }
func (m *fakeMessage) Topic() string     { return m.topic }
func (m *fakeMessage) MessageID() uint16 { return 0 }
func (m *fakeMessage) Payload() []byte   { return m.payload }
func (m *fakeMessage) Ack()              {}

// TestPahoOptionsResumeSubscriptionsAfterReconnect verifies that the paho
// client is configured to re-establish subscriptions after an automatic
// reconnect. With auto-reconnect enabled but resume disabled, a broker restart
// or network blip leaves the adapter connected and silent forever: Start only
// subscribes once, so no message is ever delivered again.
func TestPahoOptionsResumeSubscriptionsAfterReconnect(t *testing.T) {
	po := pahoOptions(Options{BrokerURL: "tcp://localhost:1883", ClientID: "c"})
	if !po.AutoReconnect {
		t.Fatal("AutoReconnect is disabled")
	}
	if !po.ResumeSubs {
		t.Fatal("ResumeSubs is disabled: subscriptions are lost after an automatic reconnect")
	}
}

// TestInboundPayloadIsCopied verifies that the payload handed to the user
// handler is a copy. paho recycles the buffer backing msg.Payload() once the
// callback returns, so retaining the slice (or reading it later) would observe
// corrupted data.
func TestInboundPayloadIsCopied(t *testing.T) {
	client := &captureClient{}
	var received []byte
	a, err := NewAdapter(
		WithBrokerURL("tcp://localhost:1883"),
		WithTopic("t"),
		WithMessageHandler(func(_ string, payload []byte) { received = payload }),
		WithClientFactory(func(*paho.ClientOptions) mqttClient { return client }),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.handler == nil {
		t.Fatal("adapter did not subscribe during Start")
	}

	buf := []byte("abc")
	client.handler(nil, &fakeMessage{topic: "t", payload: buf})
	// Simulate paho reusing the buffer for the next inbound message.
	copy(buf, "XYZ")

	if string(received) != "abc" {
		t.Fatalf("handler payload = %q after the source buffer was reused, want abc", received)
	}
}

// TestSubscribeValidatesArguments verifies that Subscribe rejects invalid
// arguments before reaching the client: a nil handler would panic inside
// paho's own goroutine (unrecoverable) and an empty topic is a programming
// error rather than a broker round trip.
func TestSubscribeValidatesArguments(t *testing.T) {
	client := &captureClient{connected: true}
	a, err := NewAdapter(
		WithBrokerURL("tcp://localhost:1883"),
		WithClientFactory(func(*paho.ClientOptions) mqttClient { return client }),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.Subscribe("t", 0, nil); err == nil {
		t.Fatal("Subscribe with a nil handler must return an error")
	}
	if err := a.Subscribe("", 0, func(string, []byte) {}); err == nil {
		t.Fatal("Subscribe with an empty topic must return an error")
	}
}
