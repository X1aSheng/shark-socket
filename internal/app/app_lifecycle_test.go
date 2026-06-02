package app

import (
	"context"
	"testing"
	"time"
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

