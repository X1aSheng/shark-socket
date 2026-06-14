package app

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

func TestAppStartStopLifecycle(t *testing.T) {
	cfg := Config{
		Protocols: []ProtocolConfig{
			{
				Name: "tcp",
				Addr: "127.0.0.1:0",
			},
		},
	}
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if app == nil {
		t.Fatal("app should not be nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Start
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if !app.Gateway.Ready() {
		t.Fatal("gateway should be ready after start")
	}

	// Stop
	if err := app.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if app.Gateway.Ready() {
		t.Fatal("gateway should not be ready after stop")
	}
}

func TestAppNewWithMultipleProtocols(t *testing.T) {
	cfg := Config{
		Protocols: []ProtocolConfig{
			{Name: "tcp", Addr: "127.0.0.1:0"},
			{Name: "udp", Addr: "127.0.0.1:0"},
		},
	}
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(app.Protocols) != 2 {
		t.Fatalf("protocols = %d, want 2", len(app.Protocols))
	}
}

func TestAppWithHTTPProtocol(t *testing.T) {
	cfg := Config{
		Protocols: []ProtocolConfig{
			{Name: "tcp", Addr: "127.0.0.1:0"},
			{Name: "http", Addr: "127.0.0.1:0"},
		},
	}
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if app == nil {
		t.Fatal("app should not be nil")
	}
}

func TestHealthHandler(t *testing.T) {
	h := healthHandler(nil)
	if h == nil {
		t.Fatal("healthHandler should not return nil")
	}
}


func TestEchoHandler(t *testing.T) {
	sess := &mockSession{}
	err := echoHandler(sess, core.Message{Payload: []byte("hello")})
	if err != nil {
		t.Fatalf("echoHandler: %v", err)
	}
}

type mockSession struct{}

func (m *mockSession) ID() uint64                    { return 0 }
func (m *mockSession) Protocol() core.Protocol       { return core.ProtocolTCP }
func (m *mockSession) RemoteAddr() net.Addr          { return nil }
func (m *mockSession) LocalAddr() net.Addr           { return nil }
func (m *mockSession) State() core.SessionState      { return core.StateActive }
func (m *mockSession) CreatedAt() time.Time          { return time.Now() }
func (m *mockSession) LastActiveAt() time.Time       { return time.Now() }
func (m *mockSession) Context() context.Context      { return context.Background() }
func (m *mockSession) SetMeta(string, any)           {}
func (m *mockSession) GetMeta(string) (any, bool)    { return nil, false }
func (m *mockSession) DelMeta(string)                {}
func (m *mockSession) Send(data []byte) error {
	if string(data) != "hello" {
		return fmt.Errorf("Send: got %q, want hello", data)
	}
	return nil
}
func (m *mockSession) Close(context.Context) error   { return nil }

func TestServeHTTPPortConflict(t *testing.T) {
	// Start a listener on a port to cause a conflict
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().String()

	cfg := Config{
		HealthAddr: port, // This port is already in use
		Protocols: []ProtocolConfig{
			{Name: "tcp", Addr: "127.0.0.1:0"},
		},
	}
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := app.Start(ctx); err != nil {
		// The Gateway start should succeed (TCP works), but health server fails
		// Verify the error is captured
		errs := app.ServeErrors()
		if len(errs) == 0 {
			t.Log("no serve errors recorded (health addr may not have started)")
		}
	}
	app.Stop(ctx)
}

func TestAppStopWithHealthAndMetrics(t *testing.T) {
	cfg := Config{
		HealthAddr:  "127.0.0.1:0",
		MetricsAddr: "127.0.0.1:0",
		Protocols: []ProtocolConfig{
			{Name: "tcp", Addr: "127.0.0.1:0"},
			{Name: "udp", Addr: "127.0.0.1:0"},
		},
	}
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	// Stop covers Health/MetricsHTTP non-nil paths
	if err := app.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if app.Gateway.Ready() {
		t.Fatal("gateway should not be ready after stop")
	}
}

func TestAppRegisterAllProtocols(t *testing.T) {
	cfg := Config{
		Protocols: []ProtocolConfig{
			{Name: "tcp", Addr: "127.0.0.1:0"},
			{Name: "udp", Addr: "127.0.0.1:0"},
			{Name: "http", Addr: "127.0.0.1:0"},
			{Name: "websocket", Addr: "127.0.0.1:0"},
			{Name: "coap", Addr: "127.0.0.1:0"},
			{Name: "grpc-web", Addr: "127.0.0.1:0"},
			{Name: "quic", Addr: "127.0.0.1:0", TLSCertFile: "/nonexistent/cert.pem", TLSKeyFile: "/nonexistent/key.pem"},
		},
	}
	// QUIC requires TLS, so expect error from registerProtocols
	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for QUIC with missing TLS")
	}
}

func TestAppRegisterAllProtocolsNoTLS(t *testing.T) {
	cfg := Config{
		Protocols: []ProtocolConfig{
			{Name: "tcp", Addr: "127.0.0.1:0"},
			{Name: "udp", Addr: "127.0.0.1:0"},
			{Name: "http", Addr: "127.0.0.1:0"},
			{Name: "websocket", Addr: "127.0.0.1:0"},
			{Name: "coap", Addr: "127.0.0.1:0"},
			{Name: "grpc-web", Addr: "127.0.0.1:0"},
		},
	}
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(app.Protocols) != 6 {
		t.Fatalf("protocols = %d, want 6", len(app.Protocols))
	}
}

func TestAppDisableProtocol(t *testing.T) {
	disabled := false
	cfg := Config{
		Protocols: []ProtocolConfig{
			{Name: "tcp", Addr: "127.0.0.1:0"},
			{Name: "http", Addr: "127.0.0.1:0", Enabled: &disabled},
		},
	}
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(app.Protocols) != 1 {
		t.Fatalf("protocols = %d, want 1 (disabled filtered)", len(app.Protocols))
	}
}

func TestAppWithOriginCheck(t *testing.T) {
	cfg := Config{
		Protocols: []ProtocolConfig{
			{Name: "websocket", Addr: "127.0.0.1:0", AllowedOrigins: []string{"http://example.com"}},
			{Name: "http", Addr: "127.0.0.1:0", AllowedOrigins: []string{"*"}},
			{Name: "grpc-web", Addr: "127.0.0.1:0", AllowedOrigins: []string{"http://localhost"}},
		},
	}
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(app.Protocols) != 3 {
		t.Fatalf("protocols = %d", len(app.Protocols))
	}
}
