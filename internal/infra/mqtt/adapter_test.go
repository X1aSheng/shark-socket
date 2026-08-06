package mqtt

import (
	"context"
	"crypto/tls"
	"errors"
	"sync"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// ---------------------------------------------------------------------------
// mock paho.Client
// ---------------------------------------------------------------------------

type mockToken struct {
	doneCh chan struct{}
	err    error
	errSet sync.Once
}

func newMockToken(err error) *mockToken {
	t := &mockToken{
		doneCh: make(chan struct{}),
		err:    err,
	}
	close(t.doneCh) // complete immediately
	return t
}

func (t *mockToken) Wait() bool                     { return true }
func (t *mockToken) WaitTimeout(time.Duration) bool { return true }
func (t *mockToken) Done() <-chan struct{}          { return t.doneCh }
func (t *mockToken) Error() error                   { return t.err }

type mockClient struct {
	connected    bool
	connectErr   error
	publishErr   error
	subscribeErr error
	mu           sync.Mutex
}

func (c *mockClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

func (c *mockClient) Connect() paho.Token {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connectErr == nil {
		c.connected = true
	}
	return newMockToken(c.connectErr)
}

func (c *mockClient) Disconnect(_ uint) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connected = false
}

func (c *mockClient) Publish(_ string, _ byte, _ bool, _ interface{}) paho.Token {
	return newMockToken(c.publishErr)
}

func (c *mockClient) Subscribe(_ string, _ byte, _ paho.MessageHandler) paho.Token {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connected = true
	return newMockToken(c.subscribeErr)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

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
		WithMessageHandler(func(topic string, payload []byte) {}),
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
	}
	po := pahoOptions(opts)
	if po == nil {
		t.Fatal("paho options should not be nil")
	}
}

// ---------------------------------------------------------------------------
// Mock-based lifecycle tests (no real broker needed)
// ---------------------------------------------------------------------------

func TestAdapterStartSuccess(t *testing.T) {
	mock := &mockClient{connected: false}
	clientFactoryFn := func(_ *paho.ClientOptions) mqttClient {
		return mock
	}

	a, err := NewAdapter(WithBrokerURL("tcp://broker:1883"), WithClientFactory(clientFactoryFn))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if !a.Connected() {
		t.Fatal("expected connected after Start")
	}
}

func TestAdapterStartConnectError(t *testing.T) {
	mock := &mockClient{connectErr: errors.New("connection refused")}
	clientFactoryFn := func(_ *paho.ClientOptions) mqttClient {
		return mock
	}

	a, err := NewAdapter(WithBrokerURL("tcp://broker:1883"), WithClientFactory(clientFactoryFn))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := a.Start(ctx); err == nil {
		t.Fatal("expected connect error")
	}
}

func TestAdapterStartIdempotent(t *testing.T) {
	mock := &mockClient{connected: true}
	clientFactoryFn := func(_ *paho.ClientOptions) mqttClient {
		return mock
	}

	a, err := NewAdapter(WithBrokerURL("tcp://broker:1883"), WithClientFactory(clientFactoryFn))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("first Start failed: %v", err)
	}
	if err := a.Start(ctx); err != nil {
		t.Fatalf("second Start (idempotent) failed: %v", err)
	}
	if !a.Connected() {
		t.Fatal("expected connected after idempotent Start")
	}
}

func TestAdapterStopDisconnects(t *testing.T) {
	mock := &mockClient{connected: false}
	clientFactoryFn := func(_ *paho.ClientOptions) mqttClient {
		return mock
	}

	a, err := NewAdapter(WithBrokerURL("tcp://broker:1883"), WithClientFactory(clientFactoryFn))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if err := a.Stop(ctx); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if a.Connected() {
		t.Fatal("expected disconnected after Stop")
	}
}

func TestAdapterStopIdempotent(t *testing.T) {
	a, err := NewAdapter(WithBrokerURL("tcp://broker:1883"))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Stop(nil); err != nil {
		t.Fatal("first Stop should succeed")
	}
	if err := a.Stop(nil); err != nil {
		t.Fatal("second Stop (idempotent) should succeed")
	}
}

func TestAdapterPublishSuccess(t *testing.T) {
	mock := &mockClient{connected: false}
	clientFactoryFn := func(_ *paho.ClientOptions) mqttClient {
		return mock
	}

	a, err := NewAdapter(WithBrokerURL("tcp://broker:1883"), WithClientFactory(clientFactoryFn))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if err := a.Publish("test/topic", 0, []byte("hello")); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}
}

func TestAdapterPublishAfterStop(t *testing.T) {
	mock := &mockClient{connected: false}
	clientFactoryFn := func(_ *paho.ClientOptions) mqttClient {
		return mock
	}

	a, err := NewAdapter(WithBrokerURL("tcp://broker:1883"), WithClientFactory(clientFactoryFn))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if err := a.Stop(ctx); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	if err := a.Publish("test", 0, []byte("x")); err == nil {
		t.Fatal("expected error for publish after stop")
	}
}

func TestAdapterSubscribeSuccess(t *testing.T) {
	mock := &mockClient{connected: false}
	clientFactoryFn := func(_ *paho.ClientOptions) mqttClient {
		return mock
	}

	a, err := NewAdapter(WithBrokerURL("tcp://broker:1883"), WithClientFactory(clientFactoryFn))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	received := make(chan struct {
		topic   string
		payload []byte
	}, 1)

	if err := a.Subscribe("test/topic", 0, func(t string, p []byte) {
		received <- struct {
			topic   string
			payload []byte
		}{t, p}
	}); err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
}

func TestAdapterSubscribeAfterStop(t *testing.T) {
	mock := &mockClient{connected: false}
	clientFactoryFn := func(_ *paho.ClientOptions) mqttClient {
		return mock
	}

	a, err := NewAdapter(WithBrokerURL("tcp://broker:1883"), WithClientFactory(clientFactoryFn))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if err := a.Stop(ctx); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	if err := a.Subscribe("test", 0, func(t string, p []byte) {}); err == nil {
		t.Fatal("expected error for subscribe after stop")
	}
}
