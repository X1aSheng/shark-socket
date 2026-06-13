package mqtt

import (
	"context"
	"fmt"
	"sync"

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

// clientFactory creates a mqttClient from options. Overridable in tests.
var clientFactory = func(o *paho.ClientOptions) mqttClient {
	return paho.NewClient(o)
}

type Adapter struct {
	opts   Options
	client mqttClient
	mu     sync.Mutex
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
	opts := a.opts // copy under lock
	a.mu.Unlock()

	client := clientFactory(pahoOptions(opts))
	token := client.Connect()
	if !token.WaitTimeout(opts.ConnectTimeout) {
		return fmt.Errorf("mqtt connect timeout")
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("mqtt connect: %w", err)
	}

	if opts.Topic != "" && opts.Handler != nil {
		h := opts.Handler
		token := client.Subscribe(opts.Topic, opts.QoS, func(_ paho.Client, msg paho.Message) {
			h(msg.Topic(), msg.Payload())
		})
		if !token.WaitTimeout(opts.ConnectTimeout) {
			client.Disconnect(100)
			return fmt.Errorf("mqtt subscribe timeout")
		}
		if err := token.Error(); err != nil {
			client.Disconnect(100)
			return fmt.Errorf("mqtt subscribe: %w", err)
		}
	}

	a.mu.Lock()
	a.client = client
	a.mu.Unlock()
	return nil
}

func (a *Adapter) Stop(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil {
		return nil
	}
	a.client.Disconnect(250)
	a.client = nil
	return nil
}

func (a *Adapter) Publish(topic string, qos byte, payload []byte) error {
	a.mu.Lock()
	client := a.client
	a.mu.Unlock()
	if client == nil || !client.IsConnected() {
		return fmt.Errorf("mqtt not connected")
	}
	token := client.Publish(topic, qos, false, payload)
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
