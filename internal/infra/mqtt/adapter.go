package mqtt

import (
	"context"
	"fmt"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// mqttClient is the subset of paho.Client used by Adapter.
// Defined as an interface so unit tests can substitute a mock.
type mqttClient interface {
	IsConnected() bool
	Connect() paho.Token
	Disconnect(quiesce uint)
	Publish(topic string, qos byte, retained bool, payload interface{}) paho.Token
	Subscribe(topic string, qos byte, callback paho.MessageHandler) paho.Token
}

type Adapter struct {
	opts   Options
	client mqttClient
	mu     sync.Mutex
	// gen is bumped by every Stop. Start captures it before dialing and
	// compares afterwards, so a Stop that lands inside the dial window cannot
	// be silently undone by the dial finishing and installing a live client.
	gen uint64
}

func NewAdapter(opts ...Option) (*Adapter, error) {
	cfg := defaultOptions()
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.BrokerURL == "" {
		return nil, fmt.Errorf("mqtt broker URL is required")
	}
	return &Adapter{opts: cfg}, nil
}

func (a *Adapter) Start(ctx context.Context) error {
	a.mu.Lock()
	if a.client != nil && a.client.IsConnected() {
		a.mu.Unlock()
		return nil
	}
	gen := a.gen
	a.mu.Unlock()

	opts := a.opts // copy under lock
	client := opts.clientFactory(pahoOptions(opts))
	token := client.Connect()
	if err := waitToken(ctx, token, opts.ConnectTimeout); err != nil {
		client.Disconnect(0)
		return fmt.Errorf("mqtt connect: %w", err)
	}

	if opts.Topic != "" && opts.Handler != nil {
		h := opts.Handler
		token := client.Subscribe(opts.Topic, opts.QoS, func(_ paho.Client, msg paho.Message) {
			h(msg.Topic(), msg.Payload())
		})
		if err := waitToken(ctx, token, opts.ConnectTimeout); err != nil {
			client.Disconnect(100)
			return fmt.Errorf("mqtt subscribe: %w", err)
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	// A Stop that ran while this Start was dialing must win: installing the
	// freshly connected client would resurrect the adapter after Stop
	// returned ("stopped" but actually connected).
	if gen != a.gen {
		client.Disconnect(0)
		if err := ctx.Err(); err != nil {
			return err
		}
		return fmt.Errorf("mqtt adapter stopped during start")
	}
	if a.client != nil && a.client.IsConnected() {
		// A concurrent Start connected first: discard this duplicate instead
		// of overwriting (which would leak the other client).
		client.Disconnect(250)
		return nil
	}
	if a.client != nil {
		// The previous client exists but is not connected (e.g. its paho
		// auto-reconnect loop is still running after a broker drop). It must
		// be stopped explicitly before being replaced; otherwise its
		// reconnection goroutines keep dialing with the same ClientID and
		// fight the new client at the broker.
		a.client.Disconnect(0)
	}
	a.client = client
	return nil
}

func (a *Adapter) Stop(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.gen++
	if a.client == nil {
		return nil
	}
	a.client.Disconnect(250)
	a.client = nil
	return nil
}

// waitToken waits for a paho token while honoring the caller's context and a
// hard timeout, so an already-cancelled Start aborts the dial instead of
// blocking until the connect timeout elapses.
func waitToken(ctx context.Context, token paho.Token, timeout time.Duration) error {
	select {
	case <-token.Done():
		return token.Error()
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(timeout):
		return fmt.Errorf("operation timed out")
	}
}

// Publish publishes a message and waits for the publish to complete (or time
// out). The payload is copied: paho encodes asynchronously, so handing over
// the caller's slice without a copy would race with the caller reusing it
// after Publish returns.
func (a *Adapter) Publish(topic string, qos byte, payload []byte) error {
	a.mu.Lock()
	client := a.client
	a.mu.Unlock()
	if client == nil || !client.IsConnected() {
		return fmt.Errorf("mqtt not connected")
	}
	token := client.Publish(topic, qos, false, append([]byte(nil), payload...))
	if !token.WaitTimeout(a.opts.ConnectTimeout) {
		return fmt.Errorf("mqtt publish timeout")
	}
	return token.Error()
}

func (a *Adapter) Subscribe(topic string, qos byte, handler MessageHandler) error {
	a.mu.Lock()
	client := a.client
	a.mu.Unlock()
	if client == nil || !client.IsConnected() {
		return fmt.Errorf("mqtt not connected")
	}
	token := client.Subscribe(topic, qos, func(_ paho.Client, msg paho.Message) {
		handler(msg.Topic(), msg.Payload())
	})
	if !token.WaitTimeout(a.opts.ConnectTimeout) {
		return fmt.Errorf("mqtt subscribe timeout")
	}
	return token.Error()
}

func (a *Adapter) Connected() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.client != nil && a.client.IsConnected()
}
