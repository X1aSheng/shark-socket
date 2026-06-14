package http

import (
	"bytes"
	"net"
	stdhttp "net/http"
	"testing"

	"github.com/X1aSheng/shark-socket/internal/core"
)

func TestHTTP_SessionMethods(t *testing.T) {
	// Use a pipe and httptest-style recorder for the ResponseWriter interface
	conn1, _ := net.Pipe()
	defer conn1.Close()

	body := bytes.NewReader([]byte("test"))
	req, err := stdhttp.NewRequest("POST", "/", body)
	if err != nil {
		t.Fatal(err)
	}
	// Use a simple ResponseWriter implementation
	rw := &testResponseWriter{}
	sess := newSession(42, rw, req)

	if got := sess.Protocol(); got != core.ProtocolHTTP {
		t.Errorf("Protocol() = %v, want %v", got, core.ProtocolHTTP)
	}
	if got := sess.RemoteAddr(); got == nil {
		t.Error("RemoteAddr() = nil")
	}
	if got := sess.ID(); got != 42 {
		t.Errorf("ID() = %d, want 42", got)
	}
	if got := sess.State(); got != core.StateActive {
		t.Errorf("State() = %v", got)
	}
	if got := sess.CreatedAt(); got.IsZero() {
		t.Error("CreatedAt() is zero")
	}
	if got := sess.LastActiveAt(); got.IsZero() {
		t.Error("LastActiveAt() is zero")
	}
	if got := sess.Context(); got == nil {
		t.Error("Context() = nil")
	}
	sess.SetMeta("k", "v")
	got, ok := sess.GetMeta("k")
	if !ok || got != "v" {
		t.Errorf("GetMeta = (%v, %v)", got, ok)
	}
	sess.DelMeta("k")
	if _, ok := sess.GetMeta("k"); ok {
		t.Error("DelMeta failed")
	}
}

type testResponseWriter struct {
	stdhttp.ResponseWriter
	header stdhttp.Header
}

func (w *testResponseWriter) Header() stdhttp.Header {
	if w.header == nil {
		w.header = make(stdhttp.Header)
	}
	return w.header
}

func (w *testResponseWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *testResponseWriter) WriteHeader(int)             {}
