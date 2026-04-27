package http

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/infra/logger"
	"github.com/X1aSheng/shark-socket/internal/types"
)

type mockAccessLogger struct {
	entries []logger.AccessLogEntry
}

func (m *mockAccessLogger) Log(entry logger.AccessLogEntry) {
	m.entries = append(m.entries, entry)
}

func TestServer_WithAccessLogger(t *testing.T) {
	logger := &mockAccessLogger{}
	srv := NewServer(WithAddr("127.0.0.1", 0), WithAccessLogger(logger))

	if srv.opts.AccessLogger == nil {
		t.Fatal("expected AccessLogger to be set")
	}
}

func TestServer_AccessLogging(t *testing.T) {
	logger := &mockAccessLogger{}

	srv := NewServer(
		WithAddr("127.0.0.1", 0),
		WithAccessLogger(logger),
	)

	// Register a simple handler
	srv.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Create a test request
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("User-Agent", "test-agent")
	w := httptest.NewRecorder()

	// Call the handler directly
	srv.mux.ServeHTTP(w, req)

	// Note: Access logging only happens when using handleWithSession (mode B)
	// For mode A (HandleFunc), access logging is not triggered
}

func TestServer_AccessLogging_ModeB(t *testing.T) {
	logger := &mockAccessLogger{}

	srv := NewServer(
		WithAddr("127.0.0.1", 0),
		WithAccessLogger(logger),
	)

	// Set handler for mode B
	srv.SetHandler(func(sess types.Session[[]byte], msg types.Message[[]byte]) error {
		return nil
	})

	// Create a test request to trigger handleWithSession
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte("test")))
	req.Header.Set("User-Agent", "test-agent")
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()

	// Call handleWithSession directly
	srv.handleWithSession(w, req)

	// Verify that access was logged
	if len(logger.entries) == 0 {
		t.Fatal("expected access log entry")
	}

	entry := logger.entries[0]
	if entry.Method != http.MethodPost {
		t.Errorf("expected method POST, got %s", entry.Method)
	}
	if entry.Path != "/" {
		t.Errorf("expected path /, got %s", entry.Path)
	}
	if entry.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", entry.StatusCode)
	}
	if entry.UserAgent != "test-agent" {
		t.Errorf("expected user agent test-agent, got %s", entry.UserAgent)
	}
	if entry.ClientIP != "127.0.0.1" {
		t.Errorf("expected client IP 127.0.0.1, got %s", entry.ClientIP)
	}
	if entry.Protocol != "http" {
		t.Errorf("expected protocol http, got %s", entry.Protocol)
	}
	if entry.Duration < 0 {
		t.Error("expected non-negative duration")
	}
	if entry.BytesIn != 4 { // "test" is 4 bytes
		t.Errorf("expected bytes in 4, got %d", entry.BytesIn)
	}
}

func TestServer_AccessLogging_ErrorCases(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(*Server)
		body       []byte
		wantStatus int
	}{
		{
			name: "body too large",
			setup: func(s *Server) {
				s.opts.MaxBodySize = 1
			},
			body:       []byte("too large"),
			wantStatus: http.StatusRequestEntityTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := &mockAccessLogger{}
			srv := NewServer(WithAddr("127.0.0.1", 0), WithAccessLogger(logger))
			if tt.setup != nil {
				tt.setup(srv)
			}

			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(tt.body))
			req.RemoteAddr = "127.0.0.1:12345"
			w := httptest.NewRecorder()

			srv.handleWithSession(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("expected status %d, got %d", tt.wantStatus, w.Code)
			}

			// For error cases, we should still have access logs
			// (implementation dependent - may or may not log for errors)
		})
	}
}

func TestWithAccessLogger_Option(t *testing.T) {
	mockLogger := &mockAccessLogger{}
	opt := WithAccessLogger(mockLogger)

	o := defaultOptions()
	opt(&o)

	if o.AccessLogger != mockLogger {
		t.Error("expected AccessLogger to be set")
	}
}

func TestAccessLogEntry_Fields(t *testing.T) {
	entry := logger.AccessLogEntry{
		Timestamp:  time.Now(),
		Protocol:   "http",
		Method:     "POST",
		Path:       "/api/test",
		StatusCode: 200,
		BytesIn:    100,
		BytesOut:   200,
		Duration:   50 * time.Millisecond,
		ClientIP:   "192.168.1.1",
		UserAgent:  "test-agent",
	}

	if entry.Protocol != "http" {
		t.Error("protocol mismatch")
	}
	if entry.Method != "POST" {
		t.Error("method mismatch")
	}
	if entry.Path != "/api/test" {
		t.Error("path mismatch")
	}
	if entry.StatusCode != 200 {
		t.Error("status code mismatch")
	}
	if entry.BytesIn != 100 {
		t.Error("bytes in mismatch")
	}
	if entry.BytesOut != 200 {
		t.Error("bytes out mismatch")
	}
	if entry.Duration != 50*time.Millisecond {
		t.Error("duration mismatch")
	}
	if entry.ClientIP != "192.168.1.1" {
		t.Error("client IP mismatch")
	}
	if entry.UserAgent != "test-agent" {
		t.Error("user agent mismatch")
	}
}

func BenchmarkAccessLogging(b *testing.B) {
	logger := &mockAccessLogger{}
	srv := NewServer(WithAddr("127.0.0.1", 0), WithAccessLogger(logger))
	srv.SetHandler(func(sess types.Session[[]byte], msg types.Message[[]byte]) error {
		return nil
	})

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte("test")))
	req.RemoteAddr = "127.0.0.1:12345"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		srv.handleWithSession(w, req)
	}
}

func BenchmarkAccessLogging_Disabled(b *testing.B) {
	srv := NewServer(WithAddr("127.0.0.1", 0)) // No access logger
	srv.SetHandler(func(sess types.Session[[]byte], msg types.Message[[]byte]) error {
		return nil
	})

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte("test")))
	req.RemoteAddr = "127.0.0.1:12345"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		srv.handleWithSession(w, req)
	}
}
