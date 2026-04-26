package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/X1aSheng/shark-socket/internal/types"
)

func TestNewHTTPSession(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.1:54321"
	w := httptest.NewRecorder()

	sess := NewHTTPSession(1, w, req)
	if sess.ID() != 1 {
		t.Fatalf("ID() = %d, want 1", sess.ID())
	}
	if sess.Protocol() != types.HTTP {
		t.Fatalf("Protocol() = %v, want HTTP", sess.Protocol())
	}
	if !sess.IsAlive() {
		t.Fatal("new session should be alive")
	}
}

func TestHTTPSession_Send(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	sess := NewHTTPSession(1, w, req)
	if err := sess.Send([]byte("hello")); err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	body := w.Body.String()
	if body != "hello" {
		t.Fatalf("body = %q, want %q", body, "hello")
	}
}

func TestHTTPSession_SendTyped(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	sess := NewHTTPSession(1, w, req)
	if err := sess.SendTyped([]byte("typed")); err != nil {
		t.Fatalf("SendTyped() error: %v", err)
	}
}

func TestHTTPSession_SendClosed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	sess := NewHTTPSession(1, w, req)
	sess.Close()
	if err := sess.Send([]byte("data")); err == nil {
		t.Fatal("Send() on closed session should return error")
	}
}

func TestHTTPSession_Close(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	sess := NewHTTPSession(1, w, req)
	if err := sess.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	if sess.IsAlive() {
		t.Fatal("session should not be alive after Close()")
	}
}

func TestHTTPSession_CloseIdempotent(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	sess := NewHTTPSession(1, w, req)
	for i := 0; i < 3; i++ {
		if err := sess.Close(); err != nil {
			t.Fatalf("Close() call %d: %v", i, err)
		}
	}
}

func TestHTTPSession_Request(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader("body"))
	w := httptest.NewRecorder()

	sess := NewHTTPSession(1, w, req)
	if sess.Request().Method != http.MethodPost {
		t.Fatalf("Method = %q, want POST", sess.Request().Method)
	}
	if sess.Request().URL.Path != "/api/test" {
		t.Fatalf("Path = %q, want /api/test", sess.Request().URL.Path)
	}
}

func TestHTTPSession_ResponseWriter(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	sess := NewHTTPSession(1, w, req)
	if sess.ResponseWriter() != w {
		t.Fatal("ResponseWriter() should return the original writer")
	}
}

func TestHTTPSession_RemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:8080"
	w := httptest.NewRecorder()

	sess := NewHTTPSession(1, w, req)
	// RemoteAddr should be parsed from request
	if sess.RemoteAddr() == nil {
		t.Fatal("RemoteAddr() should not be nil")
	}
}
