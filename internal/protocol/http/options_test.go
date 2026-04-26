package http

import (
	"crypto/tls"
	"testing"

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

func TestHTTPDefaultOptions(t *testing.T) {
	o := defaultOptions()
	if o.Port != 18400 {
		t.Errorf("Port = %d, want 18400", o.Port)
	}
	if o.Host != "0.0.0.0" {
		t.Errorf("Host = %q, want %q", o.Host, "0.0.0.0")
	}
	if o.ReadTimeout != 30 {
		t.Errorf("ReadTimeout = %d, want 30", o.ReadTimeout)
	}
	if o.WriteTimeout != 30 {
		t.Errorf("WriteTimeout = %d, want 30", o.WriteTimeout)
	}
	if o.IdleTimeout != 120 {
		t.Errorf("IdleTimeout = %d, want 120", o.IdleTimeout)
	}
}

func TestHTTPWithAddr(t *testing.T) {
	o := defaultOptions()
	WithAddr("192.168.1.1", 9090)(&o)
	if o.Host != "192.168.1.1" {
		t.Errorf("Host = %q, want %q", o.Host, "192.168.1.1")
	}
	if o.Port != 9090 {
		t.Errorf("Port = %d, want 9090", o.Port)
	}
}

func TestHTTPWithTLS(t *testing.T) {
	o := defaultOptions()
	cfg := &tls.Config{}
	WithTLS(cfg)(&o)
	if o.TLSConfig != cfg {
		t.Error("TLSConfig not set correctly")
	}
}

func TestHTTPWithTimeouts(t *testing.T) {
	o := defaultOptions()
	WithTimeouts(5, 10, 15)(&o)
	if o.ReadTimeout != 5 {
		t.Errorf("ReadTimeout = %d, want 5", o.ReadTimeout)
	}
	if o.WriteTimeout != 10 {
		t.Errorf("WriteTimeout = %d, want 10", o.WriteTimeout)
	}
	if o.IdleTimeout != 15 {
		t.Errorf("IdleTimeout = %d, want 15", o.IdleTimeout)
	}
}

func TestHTTPWithPlugins(t *testing.T) {
	o := defaultOptions()
	WithPlugins(testPlugin{})(&o)
	if len(o.Plugins) != 1 {
		t.Errorf("Plugins count = %d, want 1", len(o.Plugins))
	}
}

func TestHTTPOptions_Addr(t *testing.T) {
	o := Options{Host: "127.0.0.1", Port: 18400}
	if got := o.Addr(); got != "127.0.0.1:18400" {
		t.Errorf("Addr() = %q, want %q", got, "127.0.0.1:18400")
	}
}
