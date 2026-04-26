package websocket

import (
	"crypto/tls"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/types"
)

type testPlugin struct{}

func (testPlugin) Name() string                              { return "test" }
func (testPlugin) Priority() int                             { return 0 }
func (testPlugin) OnAccept(sess types.RawSession) error      { return nil }
func (testPlugin) OnMessage(sess types.RawSession, data []byte) ([]byte, error) {
	return data, nil
}
func (testPlugin) OnClose(sess types.RawSession) {}

func TestWSDefaultOptions(t *testing.T) {
	o := defaultOptions()
	if o.Port != 18600 {
		t.Errorf("Port = %d, want 18600", o.Port)
	}
	if o.Host != "0.0.0.0" {
		t.Errorf("Host = %q, want %q", o.Host, "0.0.0.0")
	}
	if o.Path != "/ws" {
		t.Errorf("Path = %q, want /ws", o.Path)
	}
	if o.MaxMessageSize != 1024*1024 {
		t.Errorf("MaxMessageSize = %d, want 1048576", o.MaxMessageSize)
	}
	if o.PingInterval != 30*time.Second {
		t.Errorf("PingInterval = %v, want 30s", o.PingInterval)
	}
	if o.PongTimeout != 10*time.Second {
		t.Errorf("PongTimeout = %v, want 10s", o.PongTimeout)
	}
	if o.MaxSessions != 100000 {
		t.Errorf("MaxSessions = %d, want 100000", o.MaxSessions)
	}
}

func TestWSWithAddr(t *testing.T) {
	o := defaultOptions()
	WithAddr("192.168.1.1", 9999)(&o)
	if o.Host != "192.168.1.1" {
		t.Errorf("Host = %q, want %q", o.Host, "192.168.1.1")
	}
	if o.Port != 9999 {
		t.Errorf("Port = %d, want 9999", o.Port)
	}
}

func TestWSWithPath(t *testing.T) {
	o := defaultOptions()
	WithPath("/custom")(&o)
	if o.Path != "/custom" {
		t.Errorf("Path = %q, want /custom", o.Path)
	}
}

func TestWSWithMaxSessions(t *testing.T) {
	o := defaultOptions()
	WithMaxSessions(500)(&o)
	if o.MaxSessions != 500 {
		t.Errorf("MaxSessions = %d, want 500", o.MaxSessions)
	}
}

func TestWSWithMaxMessageSize(t *testing.T) {
	o := defaultOptions()
	WithMaxMessageSize(4096)(&o)
	if o.MaxMessageSize != 4096 {
		t.Errorf("MaxMessageSize = %d, want 4096", o.MaxMessageSize)
	}
}

func TestWSWithPingPong(t *testing.T) {
	o := defaultOptions()
	WithPingPong(15*time.Second, 3*time.Second)(&o)
	if o.PingInterval != 15*time.Second {
		t.Errorf("PingInterval = %v, want 15s", o.PingInterval)
	}
	if o.PongTimeout != 3*time.Second {
		t.Errorf("PongTimeout = %v, want 3s", o.PongTimeout)
	}
}

func TestWSWithAllowedOrigins(t *testing.T) {
	o := defaultOptions()
	WithAllowedOrigins("http://a.com", "http://b.com")(&o)
	if len(o.AllowedOrigins) != 2 {
		t.Errorf("AllowedOrigins count = %d, want 2", len(o.AllowedOrigins))
	}
}

func TestWSWithTLS(t *testing.T) {
	o := defaultOptions()
	cfg := &tls.Config{}
	WithTLS(cfg)(&o)
	if o.TLSConfig != cfg {
		t.Error("TLSConfig not set correctly")
	}
}

func TestWSWithPlugins(t *testing.T) {
	o := defaultOptions()
	WithPlugins(testPlugin{})(&o)
	if len(o.Plugins) != 1 {
		t.Errorf("Plugins count = %d, want 1", len(o.Plugins))
	}
}

func TestWSOptions_Addr(t *testing.T) {
	o := Options{Host: "127.0.0.1", Port: 18600}
	if got := o.Addr(); got != "127.0.0.1:18600" {
		t.Errorf("Addr() = %q, want %q", got, "127.0.0.1:18600")
	}
}
