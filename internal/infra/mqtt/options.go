package mqtt

import (
	"crypto/tls"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

type Options struct {
	BrokerURL      string
	ClientID       string
	Username       string
	Password       string
	TLSConfig      *tls.Config
	Topic          string
	QoS            byte
	Handler        MessageHandler
	ConnectTimeout time.Duration
	// clientFactory creates the paho client from options. It is a per-adapter
	// field (not a package global) so multiple adapters remain independent and
	// reentrant; tests inject a mock through WithClientFactory.
	clientFactory func(*paho.ClientOptions) mqttClient
}

type Option func(*Options)

type MessageHandler func(topic string, payload []byte)

func defaultOptions() Options {
	return Options{
		ClientID:       "shark-socket-mqtt",
		QoS:            0,
		ConnectTimeout: 10 * time.Second,
		clientFactory:  defaultClientFactory,
	}
}

func defaultClientFactory(o *paho.ClientOptions) mqttClient {
	return paho.NewClient(o)
}

func WithBrokerURL(url string) Option {
	return func(o *Options) { o.BrokerURL = url }
}

func WithClientID(id string) Option {
	return func(o *Options) { o.ClientID = id }
}

func WithCredentials(username, password string) Option {
	return func(o *Options) { o.Username = username; o.Password = password }
}

func WithTLS(cfg *tls.Config) Option {
	return func(o *Options) { o.TLSConfig = cfg }
}

func WithTopic(topic string) Option {
	return func(o *Options) { o.Topic = topic }
}

func WithQoS(qos byte) Option {
	return func(o *Options) { o.QoS = qos }
}

func WithConnectTimeout(timeout time.Duration) Option {
	return func(o *Options) {
		if timeout > 0 {
			o.ConnectTimeout = timeout
		}
	}
}

func WithMessageHandler(handler MessageHandler) Option {
	return func(o *Options) { o.Handler = handler }
}

// WithClientFactory overrides the client constructor. Intended for tests that
// inject a mock mqttClient; the factory is scoped to this adapter instance.
func WithClientFactory(factory func(*paho.ClientOptions) mqttClient) Option {
	return func(o *Options) {
		if factory != nil {
			o.clientFactory = factory
		}
	}
}

// pahoOptions converts shark-socket MQTT options to paho client options.
func pahoOptions(opts Options) *paho.ClientOptions {
	po := paho.NewClientOptions()
	po.AddBroker(opts.BrokerURL)
	po.SetClientID(opts.ClientID)
	po.SetConnectTimeout(opts.ConnectTimeout)
	po.SetAutoReconnect(true)
	po.SetCleanSession(true)
	if opts.Username != "" {
		po.SetUsername(opts.Username)
	}
	if opts.Password != "" {
		po.SetPassword(opts.Password)
	}
	if opts.TLSConfig != nil {
		po.SetTLSConfig(opts.TLSConfig)
	}
	return po
}
