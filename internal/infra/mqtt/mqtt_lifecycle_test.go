package mqtt

import (
	"context"
	"sync"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// blockingMockClient completes the dial only when released, so tests can hold
// Start inside its dial window.
type blockingMockClient struct {
	connected bool
	proceed   chan struct{}
	mu        sync.Mutex
}

func (c *blockingMockClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

func (c *blockingMockClient) Connect() paho.Token {
	<-c.proceed // hold the dial until the test releases it
	c.mu.Lock()
	c.connected = true
	c.mu.Unlock()
	return newMockToken(nil)
}

func (c *blockingMockClient) Disconnect(uint) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connected = false
}

func (c *blockingMockClient) Publish(string, byte, bool, interface{}) paho.Token {
	return newMockToken(nil)
}

func (c *blockingMockClient) Subscribe(string, byte, paho.MessageHandler) paho.Token {
	return newMockToken(nil)
}

func (c *blockingMockClient) release() {
	select {
	case <-c.proceed: // already released
	default:
		close(c.proceed)
	}
}

func newBlockingMockClient() *blockingMockClient {
	return &blockingMockClient{proceed: make(chan struct{})}
}

// TestAdapterStopDuringStartWins verifies that a Stop landing inside the
// Start dial window is not undone by the dial finishing afterwards: the fresh
// client must be discarded and the adapter must stay stopped.
func TestAdapterStopDuringStartWins(t *testing.T) {
	blocking := newBlockingMockClient()
	var factoryMu sync.Mutex
	factoryCalls := 0
	clientFactoryFn := func(*paho.ClientOptions) mqttClient {
		factoryMu.Lock()
		factoryCalls++
		factoryMu.Unlock()
		return blocking
	}

	a, err := NewAdapter(WithBrokerURL("tcp://broker:1883"), WithClientFactory(clientFactoryFn))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	startDone := make(chan error, 1)
	go func() { startDone <- a.Start(ctx) }()

	// Let Start reach the (blocked) dial, then Stop while it is in flight.
	deadline := time.Now().Add(2 * time.Second)
	for {
		factoryMu.Lock()
		calls := factoryCalls
		factoryMu.Unlock()
		if calls > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Start never reached the dial")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := a.Stop(ctx); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// Release the dial: Start must notice the Stop and refuse to install.
	blocking.release()
	select {
	case err := <-startDone:
		if err == nil {
			t.Fatal("Start succeeded despite a concurrent Stop")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after the dial completed")
	}
	a.mu.Lock()
	installed := a.client
	a.mu.Unlock()
	if installed != nil {
		t.Fatal("adapter installed a client after Stop returned")
	}
	if a.Connected() {
		t.Fatal("adapter reports connected after Stop-during-Start")
	}
}

// TestAdapterStartReplacesDisconnectedClient verifies that starting over an
// existing-but-disconnected client disconnects the old one instead of leaking
// it (its auto-reconnect loop would otherwise keep dialing with the same
// ClientID and fight the replacement at the broker).
func TestAdapterStartReplacesDisconnectedClient(t *testing.T) {
	first := &mockClient{connected: false}
	second := &mockClient{connected: false}
	order := []*mockClient{first, second}
	var mu sync.Mutex
	next := 0
	clientFactoryFn := func(*paho.ClientOptions) mqttClient {
		mu.Lock()
		defer mu.Unlock()
		c := order[next]
		next++
		return c
	}

	a, err := NewAdapter(WithBrokerURL("tcp://broker:1883"), WithClientFactory(clientFactoryFn))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("first Start failed: %v", err)
	}
	// Simulate a broker drop: the installed paho client goes disconnected
	// while its auto-reconnect loop keeps running.
	first.mu.Lock()
	first.connected = false
	first.mu.Unlock()

	if err := a.Start(ctx); err != nil {
		t.Fatalf("second Start failed: %v", err)
	}
	a.mu.Lock()
	installed := a.client
	a.mu.Unlock()
	if installed != second {
		t.Fatalf("installed client is not the replacement")
	}
	first.mu.Lock()
	disconnected := !first.connected
	first.mu.Unlock()
	if !disconnected {
		t.Fatal("the replaced client was not disconnected (leaked reconnect loop)")
	}
	if !a.Connected() {
		t.Fatal("adapter should be connected to the replacement")
	}
}

// TestAdapterPublishCopiesPayload verifies the adapter does not hand the
// caller's payload slice to paho by reference.
func TestAdapterPublishCopiesPayload(t *testing.T) {
	mock := &mockClient{connected: false}
	clientFactoryFn := func(*paho.ClientOptions) mqttClient { return mock }
	a, err := NewAdapter(WithBrokerURL("tcp://broker:1883"), WithClientFactory(clientFactoryFn))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	payload := []byte("hello")
	if err := a.Publish("t", 0, payload); err != nil {
		t.Fatal(err)
	}
	payload[0] = 'X' // caller reuses its buffer immediately
}
