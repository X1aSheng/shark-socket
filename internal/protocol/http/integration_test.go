package http

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/types"
)

// TestIntegration_HTTP_ModeA uses httptest to verify Mode A route handling
// without starting a real listener. Tests multiple routes end-to-end.
func TestIntegration_HTTP_ModeA(t *testing.T) {
	srv := NewServer()

	// Register routes using Mode A API.
	srv.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("pong"))
	})

	srv.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(body)
	})

	srv.HandleFunc("/status/", func(w http.ResponseWriter, r *http.Request) {
		code := strings.TrimPrefix(r.URL.Path, "/status/")
		if code == "404" {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("not found"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Verify /ping
	t.Run("PingRoute", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		rec := httptest.NewRecorder()
		srv.mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		body := rec.Body.String()
		if body != "pong" {
			t.Fatalf("body = %q, want %q", body, "pong")
		}
	})

	// Verify /echo
	t.Run("EchoRoute", func(t *testing.T) {
		payload := "hello http echo"
		req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(payload))
		rec := httptest.NewRecorder()
		srv.mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if rec.Body.String() != payload {
			t.Fatalf("body = %q, want %q", rec.Body.String(), payload)
		}
	})

	// Verify /status/404
	t.Run("StatusNotFoundRoute", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/status/404", nil)
		rec := httptest.NewRecorder()
		srv.mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
		}
	})

	// Verify /status/200
	t.Run("StatusOKRoute", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/status/200", nil)
		rec := httptest.NewRecorder()
		srv.mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if rec.Body.String() != "ok" {
			t.Fatalf("body = %q, want %q", rec.Body.String(), "ok")
		}
	})

	// Verify protocol type.
	if proto := srv.Protocol(); proto != types.HTTP {
		t.Fatalf("Protocol() = %v, want %v", proto, types.HTTP)
	}
}

// TestIntegration_HTTP_ModeA_RealServer starts an actual HTTP server on a
// random port and makes real network requests using httptest.Client.
func TestIntegration_HTTP_ModeA_RealServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	srv := NewServer(WithAddr("127.0.0.1", port))

	srv.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("healthy"))
	})

	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		srv.Stop(stopCtx)
	}()

	// Wait for server to be ready.
	waitForTCPServer(t, fmt.Sprintf("127.0.0.1:%d", port), 3*time.Second)

	url := "http://127.0.0.1:" + itoa(port) + "/health"
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	if string(body) != "healthy" {
		t.Fatalf("body = %q, want %q", body, "healthy")
	}
	t.Logf("Real HTTP server verified at %s", url)
}

// TestIntegration_HTTP_ModeB_SessionHandler tests Mode B with a raw handler
// that receives per-request sessions, using a real server.
func TestIntegration_HTTP_ModeB_SessionHandler(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	srv := NewServer(WithAddr("127.0.0.1", port))

	// Set Mode B handler that echoes the request body.
	srv.SetHandler(func(sess types.RawSession, msg types.Message[[]byte]) error {
		return sess.Send(msg.Payload)
	})

	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		srv.Stop(stopCtx)
	}()

	waitForTCPServer(t, fmt.Sprintf("127.0.0.1:%d", port), 3*time.Second)

	url := "http://127.0.0.1:" + itoa(port) + "/"
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Post(url, "text/plain", strings.NewReader("mode-b-test"))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	if string(body) != "mode-b-test" {
		t.Fatalf("body = %q, want %q", string(body), "mode-b-test")
	}
	t.Log("Mode B session handler verified")
}

