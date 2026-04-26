package tcp

import (
	"crypto/tls"
	"runtime"
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

func TestTCPDefaultOptions(t *testing.T) {
	o := defaultOptions()
	if o.Port != 18000 {
		t.Errorf("Port = %d, want 18000", o.Port)
	}
	if o.Host != "0.0.0.0" {
		t.Errorf("Host = %q, want %q", o.Host, "0.0.0.0")
	}
	expectedWorkers := runtime.NumCPU() * 2
	if o.WorkerCount != expectedWorkers {
		t.Errorf("WorkerCount = %d, want %d", o.WorkerCount, expectedWorkers)
	}
	if o.FullPolicy != PolicyDrop {
		t.Errorf("FullPolicy = %d, want PolicyDrop", o.FullPolicy)
	}
	if o.MaxSessions != 100000 {
		t.Errorf("MaxSessions = %d, want 100000", o.MaxSessions)
	}
	if o.MaxMessageSize != 1024*1024 {
		t.Errorf("MaxMessageSize = %d, want 1048576", o.MaxMessageSize)
	}
	if o.WriteQueueSize != 128 {
		t.Errorf("WriteQueueSize = %d, want 128", o.WriteQueueSize)
	}
	if o.DrainTimeout != 5 {
		t.Errorf("DrainTimeout = %d, want 5", o.DrainTimeout)
	}
	if o.ShutdownTimeout != 10 {
		t.Errorf("ShutdownTimeout = %d, want 10", o.ShutdownTimeout)
	}
	if o.MaxConsecutiveErrors != 100 {
		t.Errorf("MaxConsecutiveErrors = %d, want 100", o.MaxConsecutiveErrors)
	}
	if o.Framer == nil {
		t.Error("Framer should not be nil by default")
	}
}

func TestTCPWithAddr(t *testing.T) {
	o := defaultOptions()
	WithAddr("192.168.1.1", 9999)(&o)
	if o.Host != "192.168.1.1" {
		t.Errorf("Host = %q, want %q", o.Host, "192.168.1.1")
	}
	if o.Port != 9999 {
		t.Errorf("Port = %d, want 9999", o.Port)
	}
}

func TestTCPWithTLS(t *testing.T) {
	o := defaultOptions()
	cfg := &tls.Config{}
	WithTLS(cfg)(&o)
	if o.TLSConfig != cfg {
		t.Error("TLSConfig not set correctly")
	}
}

func TestTCPWithWorkerPool(t *testing.T) {
	o := defaultOptions()
	WithWorkerPool(4, 16, 256)(&o)
	if o.WorkerCount != 4 {
		t.Errorf("WorkerCount = %d, want 4", o.WorkerCount)
	}
	if o.MaxWorkers != 16 {
		t.Errorf("MaxWorkers = %d, want 16", o.MaxWorkers)
	}
	if o.TaskQueueSize != 256 {
		t.Errorf("TaskQueueSize = %d, want 256", o.TaskQueueSize)
	}
}

func TestTCPWithFullPolicy(t *testing.T) {
	o := defaultOptions()
	WithFullPolicy(PolicyBlock)(&o)
	if o.FullPolicy != PolicyBlock {
		t.Errorf("FullPolicy = %d, want PolicyBlock", o.FullPolicy)
	}
}

func TestTCPWithMaxSessions(t *testing.T) {
	o := defaultOptions()
	WithMaxSessions(500)(&o)
	if o.MaxSessions != 500 {
		t.Errorf("MaxSessions = %d, want 500", o.MaxSessions)
	}
}

func TestTCPWithMaxMessageSize(t *testing.T) {
	o := defaultOptions()
	WithMaxMessageSize(8192)(&o)
	if o.MaxMessageSize != 8192 {
		t.Errorf("MaxMessageSize = %d, want 8192", o.MaxMessageSize)
	}
}

func TestTCPWithFramer(t *testing.T) {
	o := defaultOptions()
	f := NewLengthPrefixFramer(4096)
	WithFramer(f)(&o)
	if o.Framer != f {
		t.Error("Framer not set correctly")
	}
}

func TestTCPWithPlugins(t *testing.T) {
	o := defaultOptions()
	WithPlugins(testPlugin{})(&o)
	if len(o.Plugins) != 1 {
		t.Errorf("Plugins count = %d, want 1", len(o.Plugins))
	}
}

func TestTCPWithTimeouts(t *testing.T) {
	o := defaultOptions()
	WithTimeouts(5, 10, 30)(&o)
	if o.ReadTimeout != 5 {
		t.Errorf("ReadTimeout = %d, want 5", o.ReadTimeout)
	}
	if o.WriteTimeout != 10 {
		t.Errorf("WriteTimeout = %d, want 10", o.WriteTimeout)
	}
	if o.IdleTimeout != 30 {
		t.Errorf("IdleTimeout = %d, want 30", o.IdleTimeout)
	}
}

func TestTCPWithDrainTimeout(t *testing.T) {
	o := defaultOptions()
	WithDrainTimeout(15)(&o)
	if o.DrainTimeout != 15 {
		t.Errorf("DrainTimeout = %d, want 15", o.DrainTimeout)
	}
}

func TestTCPWithMaxConsecutiveErrors(t *testing.T) {
	o := defaultOptions()
	WithMaxConsecutiveErrors(50)(&o)
	if o.MaxConsecutiveErrors != 50 {
		t.Errorf("MaxConsecutiveErrors = %d, want 50", o.MaxConsecutiveErrors)
	}
}

func TestTCPOptions_Addr(t *testing.T) {
	o := Options{Host: "127.0.0.1", Port: 18000}
	if got := o.Addr(); got != "127.0.0.1:18000" {
		t.Errorf("Addr() = %q, want %q", got, "127.0.0.1:18000")
	}
}
