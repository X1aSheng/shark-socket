package coap

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

func TestCoAPDefaultOptions(t *testing.T) {
	o := defaultOptions()
	if o.Port != 18800 {
		t.Errorf("Port = %d, want 18800", o.Port)
	}
	if o.Host != "0.0.0.0" {
		t.Errorf("Host = %q, want %q", o.Host, "0.0.0.0")
	}
	if o.MaxSessions != 100000 {
		t.Errorf("MaxSessions = %d, want 100000", o.MaxSessions)
	}
	if o.SessionTTL != 5*time.Minute {
		t.Errorf("SessionTTL = %v, want 5m", o.SessionTTL)
	}
	if o.AckTimeout != 2*time.Second {
		t.Errorf("AckTimeout = %v, want 2s", o.AckTimeout)
	}
	if o.MaxRetransmit != 4 {
		t.Errorf("MaxRetransmit = %d, want 4", o.MaxRetransmit)
	}
}

func TestCoAPWithAddr(t *testing.T) {
	o := defaultOptions()
	WithAddr("192.168.1.1", 9999)(&o)
	if o.Host != "192.168.1.1" {
		t.Errorf("Host = %q, want %q", o.Host, "192.168.1.1")
	}
	if o.Port != 9999 {
		t.Errorf("Port = %d, want 9999", o.Port)
	}
}

func TestCoAPWithMaxSessions(t *testing.T) {
	o := defaultOptions()
	WithMaxSessions(500)(&o)
	if o.MaxSessions != 500 {
		t.Errorf("MaxSessions = %d, want 500", o.MaxSessions)
	}
}

func TestCoAPWithSessionTTL(t *testing.T) {
	o := defaultOptions()
	WithSessionTTL(30 * time.Second)(&o)
	if o.SessionTTL != 30*time.Second {
		t.Errorf("SessionTTL = %v, want 30s", o.SessionTTL)
	}
}

func TestCoAPWithAckTimeout(t *testing.T) {
	o := defaultOptions()
	WithAckTimeout(1*time.Second, 3)(&o)
	if o.AckTimeout != 1*time.Second {
		t.Errorf("AckTimeout = %v, want 1s", o.AckTimeout)
	}
	if o.MaxRetransmit != 3 {
		t.Errorf("MaxRetransmit = %d, want 3", o.MaxRetransmit)
	}
}

func TestCoAPWithPlugins(t *testing.T) {
	o := defaultOptions()
	WithPlugins(testPlugin{}, testPlugin{})(&o)
	if len(o.Plugins) != 2 {
		t.Errorf("Plugins count = %d, want 2", len(o.Plugins))
	}
}

func TestCoAPOptions_Addr(t *testing.T) {
	o := Options{Host: "127.0.0.1", Port: 18800}
	if got := o.Addr(); got != "127.0.0.1:18800" {
		t.Errorf("Addr() = %q, want %q", got, "127.0.0.1:18800")
	}
}
