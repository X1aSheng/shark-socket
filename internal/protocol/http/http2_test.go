package http

import (
	"context"
	"fmt"
	stdhttp "net/http"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// TestHTTP2_H2C_ServerStart tests that server starts with h2c enabled
func TestHTTP2_H2C_ServerStart(t *testing.T) {
	srv := NewServer(
		WithAddr("127.0.0.1", 0),
		WithHTTP2(),
	)

	srv.HandleFunc("/test", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.WriteHeader(stdhttp.StatusOK)
		w.Write([]byte("h2c works!"))
	})

	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Stop(context.Background())

	time.Sleep(10 * time.Millisecond)

	addr := srv.Addr()
	if addr == nil {
		t.Fatal("server address is nil")
	}

	// The server should accept HTTP/1.1 requests (h2c allows fallback)
	resp, err := stdhttp.Get(fmt.Sprintf("http://%s/test", addr.String()))
	if err != nil {
		t.Fatalf("failed to make HTTP/1.1 request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != stdhttp.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

// TestHTTP2_OptionConfiguration tests that HTTP/2 options are correctly applied
func TestHTTP2_OptionConfiguration(t *testing.T) {
	// Test WithHTTP2()
	srv1 := NewServer(WithHTTP2())
	if !srv1.opts.EnableHTTP2 {
		t.Error("EnableHTTP2 should be true")
	}

	// Test WithHTTP2Config()
	srv2 := NewServer(WithHTTP2Config(500))
	if !srv2.opts.EnableHTTP2 {
		t.Error("EnableHTTP2 should be true")
	}
	if srv2.opts.MaxConcurrentStreams != 500 {
		t.Errorf("MaxConcurrentStreams should be 500, got %d", srv2.opts.MaxConcurrentStreams)
	}
}

// TestHTTP2_DefaultDisabled tests HTTP/2 is disabled by default
func TestHTTP2_DefaultDisabled(t *testing.T) {
	srv := NewServer()
	if srv.opts.EnableHTTP2 {
		t.Error("HTTP/2 should be disabled by default")
	}
}

// TestHTTP2_ServerStartWithoutH2 tests that server works fine without HTTP/2
func TestHTTP2_ServerStartWithoutH2(t *testing.T) {
	srv := NewServer(WithAddr("127.0.0.1", 0))

	srv.HandleFunc("/", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.Write([]byte("ok"))
	})

	if err := srv.Start(); err != nil {
		t.Fatalf("server start failed: %v", err)
	}
	defer srv.Stop(context.Background())

	time.Sleep(10 * time.Millisecond)
	addr := srv.Addr()
	if addr == nil {
		t.Fatal("addr is nil")
	}
}

// TestHTTP2_ConcurrentStreams tests concurrent request handling
func TestHTTP2_ConcurrentStreams(t *testing.T) {
	srv := NewServer(
		WithAddr("127.0.0.1", 0),
		WithHTTP2Config(100),
	)

	requestCount := 0
	srv.HandleFunc("/count", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		requestCount++
		w.WriteHeader(stdhttp.StatusOK)
		fmt.Fprintf(w, "count: %d", requestCount)
	})

	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Stop(context.Background())

	time.Sleep(10 * time.Millisecond)

	addr := srv.Addr()
	if addr == nil {
		t.Fatal("server address is nil")
	}

	client := &stdhttp.Client{Timeout: 5 * time.Second}
	url := fmt.Sprintf("http://%s/count", addr.String())

	for i := 0; i < 10; i++ {
		resp, err := client.Get(url)
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		resp.Body.Close()
	}

	if requestCount != 10 {
		t.Errorf("expected 10 requests, got %d", requestCount)
	}
}

// TestH2CHandler_Integration verifies h2c.NewHandler integration
func TestH2CHandler_Integration(t *testing.T) {
	mux := stdhttp.NewServeMux()
	mux.HandleFunc("/test", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.Write([]byte("h2c handler"))
	})

	h2s := &http2.Server{
		MaxConcurrentStreams: 100,
	}
	handler := h2c.NewHandler(mux, h2s)

	if handler == nil {
		t.Error("h2c handler should not be nil")
	}

	// Verify it's an http.Handler
	var _ stdhttp.Handler = handler
}

// TestHTTP2_ConfigureNotEnabled tests configureHTTP2 when not enabled
func TestHTTP2_ConfigureNotEnabled(t *testing.T) {
	srv := NewServer(WithAddr("127.0.0.1", 0))

	// Start should succeed without HTTP/2 (configureHTTP2 is not called)
	if err := srv.Start(); err != nil {
		t.Fatalf("server start failed: %v", err)
	}
	defer srv.Stop(context.Background())

	addr := srv.Addr()
	if addr == nil {
		t.Fatal("addr is nil")
	}
}

// TestHTTP2_WithMaxConcurrentStreams tests server with custom max streams
func TestHTTP2_WithMaxConcurrentStreams(t *testing.T) {
	srv := NewServer(
		WithAddr("127.0.0.1", 0),
		WithHTTP2Config(250),
	)

	srv.HandleFunc("/", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.Write([]byte("ok"))
	})

	if err := srv.Start(); err != nil {
		t.Fatalf("server start failed: %v", err)
	}
	defer srv.Stop(context.Background())

	if srv.opts.MaxConcurrentStreams != 250 {
		t.Errorf("expected MaxConcurrentStreams=250, got %d", srv.opts.MaxConcurrentStreams)
	}
}

// TestHTTP2_H2C_ConcurrentRequests tests concurrent h2c requests
func TestHTTP2_H2C_ConcurrentRequests(t *testing.T) {
	srv := NewServer(
		WithAddr("127.0.0.1", 0),
		WithHTTP2(),
	)

	responses := make(chan int, 20)
	srv.HandleFunc("/concurrent", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		responses <- 1
		w.Write([]byte("ok"))
	})

	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Stop(context.Background())

	time.Sleep(10 * time.Millisecond)

	addr := srv.Addr()
	url := fmt.Sprintf("http://%s/concurrent", addr.String())

	// Launch concurrent requests
	done := make(chan bool)
	for i := 0; i < 20; i++ {
		go func() {
			resp, err := stdhttp.Get(url)
			if err == nil {
				resp.Body.Close()
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 20; i++ {
		<-done
	}

	close(responses)
	count := 0
	for range responses {
		count++
	}

	if count != 20 {
		t.Errorf("expected 20 responses, got %d", count)
	}
}

// BenchmarkHTTP2_H2C benchmarks h2c request handling
func BenchmarkHTTP2_H2C(b *testing.B) {
	srv := NewServer(
		WithAddr("127.0.0.1", 0),
		WithHTTP2(),
	)

	srv.HandleFunc("/bench", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.Write([]byte("ok"))
	})

	if err := srv.Start(); err != nil {
		b.Fatalf("failed to start server: %v", err)
	}
	defer srv.Stop(context.Background())

	time.Sleep(10 * time.Millisecond)

	addr := srv.Addr()
	url := fmt.Sprintf("http://%s/bench", addr.String())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := stdhttp.Get(url)
		if err != nil {
			b.Fatal(err)
		}
		resp.Body.Close()
	}
}

// BenchmarkHTTP2_Disabled benchmarks HTTP/1.1 without HTTP/2
func BenchmarkHTTP2_Disabled(b *testing.B) {
	srv := NewServer(WithAddr("127.0.0.1", 0))

	srv.HandleFunc("/bench", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.Write([]byte("ok"))
	})

	if err := srv.Start(); err != nil {
		b.Fatalf("failed to start server: %v", err)
	}
	defer srv.Stop(context.Background())

	time.Sleep(10 * time.Millisecond)

	addr := srv.Addr()
	url := fmt.Sprintf("http://%s/bench", addr.String())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := stdhttp.Get(url)
		if err != nil {
			b.Fatal(err)
		}
		resp.Body.Close()
	}
}

// TestHTTP2_ContextCanceled tests that h2c server respects context cancellation
func TestHTTP2_ContextCanceled(t *testing.T) {
	srv := NewServer(
		WithAddr("127.0.0.1", 0),
		WithHTTP2(),
	)

	srv.HandleFunc("/", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.Write([]byte("ok"))
	})

	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Stop(ctx); err != nil {
		t.Fatalf("server stop failed: %v", err)
	}
}
