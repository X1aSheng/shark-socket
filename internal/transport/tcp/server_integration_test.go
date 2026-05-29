package tcp

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket-new/internal/core"
	"github.com/X1aSheng/shark-socket-new/internal/runtime"
)

type prefixPlugin struct {
	core.BasePlugin
	prefix []byte
}

func (p prefixPlugin) Name() string { return "prefix" }

func (p prefixPlugin) OnMessage(_ core.Session, data []byte) ([]byte, error) {
	out := append([]byte(nil), p.prefix...)
	out = append(out, data...)
	return out, nil
}

func TestGatewayTCPGlobalPluginEchoAndShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	gateway := runtime.NewGateway(runtime.WithPlugins(prefixPlugin{prefix: []byte("global:")}))
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}

	if err := gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}

	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	framer := LengthPrefixFramer{MaxFrameBytes: 1024}
	if err := framer.WriteFrame(conn, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	got, err := framer.ReadFrame(conn)
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte("global:hello"); !bytes.Equal(got, want) {
		t.Fatalf("echo = %q, want %q", got, want)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	if err := gateway.Stop(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	if count := gateway.Runtime().Sessions().Count(); count != 0 {
		t.Fatalf("session count = %d, want 0", count)
	}
}

func TestTCPClientEcho(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = gateway.Stop(shutdownCtx)
	}()

	client := NewClient(server.Addr().String())
	if err := client.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Send([]byte("client-hello")); err != nil {
		t.Fatal(err)
	}
	got, err := client.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "client-hello" {
		t.Fatalf("echo = %q, want client-hello", got)
	}
}

func TestGatewayTCPRestartKeepsSessionManagerUsable(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}

	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	firstAddr := server.Addr().String()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	if err := gateway.Stop(stopCtx); err != nil {
		stopCancel()
		t.Fatal(err)
	}
	stopCancel()

	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = gateway.Stop(shutdownCtx)
	}()
	if server.Addr().String() == firstAddr {
		t.Fatalf("server reused stopped listener address %s", firstAddr)
	}

	client := NewClient(server.Addr().String())
	if err := client.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Send([]byte("restart")); err != nil {
		t.Fatal(err)
	}
	got, err := client.Receive()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "restart" {
		t.Fatalf("echo = %q, want restart", got)
	}
}
