package udp

import (
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

func TestUDPDefaultOptions(t *testing.T) {
	o := defaultOptions()
	if o.Port != 18200 {
		t.Errorf("Port = %d, want 18200", o.Port)
	}
	if o.Host != "0.0.0.0" {
		t.Errorf("Host = %q, want %q", o.Host, "0.0.0.0")
	}
	if o.SessionTTL != 60*time.Second {
		t.Errorf("SessionTTL = %v, want 60s", o.SessionTTL)
	}
	if o.MaxSessions != 100000 {
		t.Errorf("MaxSessions = %d, want 100000", o.MaxSessions)
	}
}

func TestUDPWithAddr(t *testing.T) {
	o := defaultOptions()
	WithAddr("192.168.1.1", 9999)(&o)
	if o.Host != "192.168.1.1" {
		t.Errorf("Host = %q, want %q", o.Host, "192.168.1.1")
	}
	if o.Port != 9999 {
		t.Errorf("Port = %d, want 9999", o.Port)
	}
}

func TestUDPWithMaxSessions(t *testing.T) {
	o := defaultOptions()
	WithMaxSessions(500)(&o)
	if o.MaxSessions != 500 {
		t.Errorf("MaxSessions = %d, want 500", o.MaxSessions)
	}
}

func TestUDPWithSessionTTL(t *testing.T) {
	o := defaultOptions()
	WithSessionTTL(30 * time.Second)(&o)
	if o.SessionTTL != 30*time.Second {
		t.Errorf("SessionTTL = %v, want 30s", o.SessionTTL)
	}
}

func TestUDPWithPlugins(t *testing.T) {
	o := defaultOptions()
	WithPlugins(testPlugin{})(&o)
	if len(o.Plugins) != 1 {
		t.Errorf("Plugins count = %d, want 1", len(o.Plugins))
	}
}

func TestUDPOptions_Addr(t *testing.T) {
	o := Options{Host: "127.0.0.1", Port: 18200}
	if got := o.Addr(); got != "127.0.0.1:18200" {
		t.Errorf("Addr() = %q, want %q", got, "127.0.0.1:18200")
	}
}
