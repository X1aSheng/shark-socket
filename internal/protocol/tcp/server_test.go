package tcp

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/errs"
	"github.com/X1aSheng/shark-socket/internal/types"
)

// --- Server creation and lifecycle ---

func TestNewServer_Creation(t *testing.T) {
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return nil
	}

	srv := NewServer(handler, WithAddr("127.0.0.1", 0))
	if srv == nil {
		t.Fatal("expected non-nil server")
	}
	if srv.Protocol() != types.TCP {
		t.Errorf("expected TCP protocol, got %v", srv.Protocol())
	}
}

func TestServer_ProtocolReturnsTCP(t *testing.T) {
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return nil
	}

	srv := NewServer(handler)
	if srv.Protocol() != types.TCP {
		t.Errorf("expected TCP, got %v", srv.Protocol())
	}
}

func TestServer_StartStopLifecycle(t *testing.T) {
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return nil
	}

	srv := NewServer(handler, WithAddr("127.0.0.1", 0))

	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Verify we can connect
	addr := srv.listener.Addr().String()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Verify we can no longer connect
	_, err = net.DialTimeout("tcp", addr, 100*time.Millisecond)
	if err == nil {
		t.Error("expected connection to fail after stop")
	}
}

func TestServer_StopIsIdempotent(t *testing.T) {
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return nil
	}

	srv := NewServer(handler, WithAddr("127.0.0.1", 0))
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Stop(ctx); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := srv.Stop(ctx); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

func TestServer_StopWithoutStart(t *testing.T) {
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return nil
	}

	srv := NewServer(handler)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Stopping a server that was never started should not panic
	if err := srv.Stop(ctx); err != nil {
		t.Fatalf("Stop without start: %v", err)
	}
}

func TestServer_StartOnUsedPort(t *testing.T) {
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return nil
	}

	// Listen on a port first
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()

	// Try to start server on same address
	srv := NewServer(handler, WithAddr("127.0.0.1", mustExtractPort(addr)))
	err = srv.Start()
	if err == nil {
		t.Fatal("expected error when starting on used port")
		srv.Stop(context.Background())
	}

	ln.Close()
}

// --- Echo integration test ---

func TestServer_EchoTest(t *testing.T) {
	// Echo handler: sends back whatever it receives
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return sess.Send(msg.Payload)
	}

	srv := NewServer(handler,
		WithAddr("127.0.0.1", 0),
		WithWorkerPool(2, 8, 64),
		WithFramer(NewLengthPrefixFramer(4096)),
	)

	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(context.Background())

	addr := srv.listener.Addr().String()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Second))

	framer := NewLengthPrefixFramer(4096)

	// Send a message
	payload := []byte("hello echo test")
	if err := framer.WriteFrame(conn, payload); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	// Read echo response
	got, err := framer.ReadFrame(conn)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}

	if string(got) != string(payload) {
		t.Errorf("echo mismatch: got %q, want %q", got, payload)
	}
}

func TestServer_MultipleEchoMessages(t *testing.T) {
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return sess.Send(msg.Payload)
	}

	srv := NewServer(handler,
		WithAddr("127.0.0.1", 0),
		WithWorkerPool(1, 2, 256), // single worker to preserve order
		WithWriteQueueSize(256),
		WithFramer(NewLengthPrefixFramer(4096)),
	)

	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(context.Background())

	addr := srv.listener.Addr().String()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(10 * time.Second))

	framer := NewLengthPrefixFramer(4096)

	// Send-then-read each message to avoid reordering from parallel workers
	messages := []string{"msg1", "msg2", "msg3", "msg4", "msg5"}
	for _, msg := range messages {
		if err := framer.WriteFrame(conn, []byte(msg)); err != nil {
			t.Fatalf("WriteFrame %q: %v", msg, err)
		}

		got, err := framer.ReadFrame(conn)
		if err != nil {
			t.Fatalf("ReadFrame %q: %v", msg, err)
		}
		if string(got) != msg {
			t.Errorf("echo mismatch: got %q, want %q", got, msg)
		}
	}
}

func TestServer_MultipleClients(t *testing.T) {
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return sess.Send(msg.Payload)
	}

	srv := NewServer(handler,
		WithAddr("127.0.0.1", 0),
		WithWorkerPool(2, 4, 256),
		WithWriteQueueSize(256),
		WithFramer(NewLengthPrefixFramer(4096)),
	)

	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(context.Background())

	addr := srv.listener.Addr().String()

	// Connect clients one at a time, echo test, then disconnect
	for i := 0; i < 5; i++ {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			t.Fatalf("client %d dial: %v", i, err)
		}
		conn.SetDeadline(time.Now().Add(5 * time.Second))

		framer := NewLengthPrefixFramer(4096)
		msg := []byte("client-data")
		if err := framer.WriteFrame(conn, msg); err != nil {
			t.Fatalf("client %d WriteFrame: %v", i, err)
		}

		got, err := framer.ReadFrame(conn)
		if err != nil {
			t.Fatalf("client %d ReadFrame: %v", i, err)
		}

		if string(got) != string(msg) {
			t.Errorf("client %d echo: got %q, want %q", i, got, msg)
		}
		conn.Close()
	}
}

func TestServer_ClientDisconnect(t *testing.T) {
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return nil
	}

	srv := NewServer(handler,
		WithAddr("127.0.0.1", 0),
		WithWorkerPool(2, 8, 64),
	)

	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(context.Background())

	addr := srv.listener.Addr().String()

	// Connect and immediately disconnect
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	conn.Close()

	// Server should still be healthy — accept another connection
	time.Sleep(100 * time.Millisecond)

	conn2, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("second dial after disconnect: %v", err)
	}
	conn2.Close()
}

func TestServer_ManagerNotNilAfterStart(t *testing.T) {
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return nil
	}

	srv := NewServer(handler, WithAddr("127.0.0.1", 0))

	// Before start, manager is nil
	if srv.Manager() != nil {
		t.Error("expected nil manager before start")
	}

	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(context.Background())

	// After start, manager should exist
	if srv.Manager() == nil {
		t.Error("expected non-nil manager after start")
	}
}

// --- Options tests ---

func TestOptions_Addr(t *testing.T) {
	o := Options{Host: "192.168.1.1", Port: 9090}
	if o.Addr() != "192.168.1.1:9090" {
		t.Errorf("Addr() = %q, want %q", o.Addr(), "192.168.1.1:9090")
	}
}

func TestOptions_Defaults(t *testing.T) {
	o := defaultOptions()
	if o.Host != "0.0.0.0" {
		t.Errorf("default Host = %q, want %q", o.Host, "0.0.0.0")
	}
	if o.Port != 18000 {
		t.Errorf("default Port = %d, want %d", o.Port, 18000)
	}
	if o.WriteQueueSize != 128 {
		t.Errorf("default WriteQueueSize = %d, want %d", o.WriteQueueSize, 128)
	}
	if o.MaxMessageSize != 1024*1024 {
		t.Errorf("default MaxMessageSize = %d, want %d", o.MaxMessageSize, 1024*1024)
	}
	if o.FullPolicy != PolicyDrop {
		t.Errorf("default FullPolicy = %d, want %d", o.FullPolicy, PolicyDrop)
	}
}

func TestWithAddr(t *testing.T) {
	o := defaultOptions()
	WithAddr("10.0.0.1", 3000)(&o)
	if o.Host != "10.0.0.1" {
		t.Errorf("Host = %q, want %q", o.Host, "10.0.0.1")
	}
	if o.Port != 3000 {
		t.Errorf("Port = %d, want %d", o.Port, 3000)
	}
}

func TestWithWorkerPool(t *testing.T) {
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

func TestWithFullPolicy(t *testing.T) {
	o := defaultOptions()
	WithFullPolicy(PolicyBlock)(&o)
	if o.FullPolicy != PolicyBlock {
		t.Errorf("FullPolicy = %d, want %d", o.FullPolicy, PolicyBlock)
	}
}

func TestWithWriteQueueSize(t *testing.T) {
	o := defaultOptions()
	WithWriteQueueSize(256)(&o)
	if o.WriteQueueSize != 256 {
		t.Errorf("WriteQueueSize = %d, want 256", o.WriteQueueSize)
	}
}

func TestWithMaxSessions(t *testing.T) {
	o := defaultOptions()
	WithMaxSessions(500)(&o)
	if o.MaxSessions != 500 {
		t.Errorf("MaxSessions = %d, want 500", o.MaxSessions)
	}
}

func TestWithMaxMessageSize(t *testing.T) {
	o := defaultOptions()
	WithMaxMessageSize(2048)(&o)
	if o.MaxMessageSize != 2048 {
		t.Errorf("MaxMessageSize = %d, want 2048", o.MaxMessageSize)
	}
}

func TestWithFramer(t *testing.T) {
	o := defaultOptions()
	f := NewLineFramer(1024)
	WithFramer(f)(&o)
	if o.Framer != f {
		t.Error("Framer not set correctly")
	}
}

func TestWithTimeouts(t *testing.T) {
	o := defaultOptions()
	WithTimeouts(30, 15, 60)(&o)
	if o.ReadTimeout != 30 {
		t.Errorf("ReadTimeout = %d, want 30", o.ReadTimeout)
	}
	if o.WriteTimeout != 15 {
		t.Errorf("WriteTimeout = %d, want 15", o.WriteTimeout)
	}
	if o.IdleTimeout != 60 {
		t.Errorf("IdleTimeout = %d, want 60", o.IdleTimeout)
	}
}

func TestWithDrainTimeout(t *testing.T) {
	o := defaultOptions()
	WithDrainTimeout(20)(&o)
	if o.DrainTimeout != 20 {
		t.Errorf("DrainTimeout = %d, want 20", o.DrainTimeout)
	}
}

func TestWithMaxConsecutiveErrors(t *testing.T) {
	o := defaultOptions()
	WithMaxConsecutiveErrors(5)(&o)
	if o.MaxConsecutiveErrors != 5 {
		t.Errorf("MaxConsecutiveErrors = %d, want 5", o.MaxConsecutiveErrors)
	}
}

// --- acceptBackoff test ---

func TestAcceptBackoff(t *testing.T) {
	tests := []struct {
		errors int
		want   time.Duration
		capped bool
	}{
		{1, 5 * time.Millisecond, false},     // 5 << 0
		{2, 10 * time.Millisecond, false},    // 5 << 1
		{3, 20 * time.Millisecond, false},    // 5 << 2
		{11, 5120 * time.Millisecond, false}, // 5 << 10 = 5120ms
		{20, 5120 * time.Millisecond, false}, // 5 << 10 = 5120ms (min caps shift at 10)
	}

	for _, tt := range tests {
		d := acceptBackoff(tt.errors)
		if d != tt.want {
			t.Errorf("acceptBackoff(%d) = %v, want %v", tt.errors, d, tt.want)
		}
	}
}

// --- LineFramer-based echo test ---

func TestServer_LineFramerEcho(t *testing.T) {
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return sess.Send(msg.Payload)
	}

	srv := NewServer(handler,
		WithAddr("127.0.0.1", 0),
		WithWorkerPool(2, 8, 64),
		WithFramer(NewLineFramer(4096)),
	)

	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop(context.Background())

	addr := srv.listener.Addr().String()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Write line-delimited message
	_, err = conn.Write([]byte("hello line\n"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Read response
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	got := string(buf[:n])
	if got != "hello line\n" {
		t.Errorf("echo mismatch: got %q, want %q", got, "hello line\n")
	}
}

// --- Stop with context cancellation ---

func TestServer_StopWithContextCancel(t *testing.T) {
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return nil
	}

	srv := NewServer(handler, WithAddr("127.0.0.1", 0))
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := srv.Stop(ctx)
	if err != nil {
		// context.Canceled is acceptable
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Stop with cancelled context: %v", err)
		}
	}
}

// --- helper ---

func mustExtractPort(addr string) int {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		panic(err)
	}
	var port int
	for _, c := range portStr {
		if c >= '0' && c <= '9' {
			port = port*10 + int(c-'0')
		}
	}
	return port
}

// --- errs package integration ---

func TestErrsPackageIntegration(t *testing.T) {
	_ = errs.ErrSessionClosed
	_ = errs.ErrWriteQueueFull
	_ = errs.ErrServerClosed
	_ = errs.ErrMessageTooLarge
}
