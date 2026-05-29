package udp

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
	closed int
}

func (p *prefixPlugin) Name() string { return "udp-prefix" }

func (p *prefixPlugin) OnMessage(_ core.Session, data []byte) ([]byte, error) {
	out := append([]byte(nil), p.prefix...)
	out = append(out, data...)
	return out, nil
}

func (p *prefixPlugin) OnClose(core.Session) {
	p.closed++
}

type conditionalDropPlugin struct {
	core.BasePlugin
	drop string
}

func (p conditionalDropPlugin) Name() string { return "udp-conditional-drop" }

func (p conditionalDropPlugin) OnMessage(_ core.Session, data []byte) ([]byte, error) {
	if string(data) == p.drop {
		return nil, core.ErrPluginDrop
	}
	return data, nil
}

func TestGatewayUDPGlobalPluginEchoAndShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	plugin := &prefixPlugin{prefix: []byte("global:")}
	gateway := runtime.NewGateway(runtime.WithPlugins(plugin))
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}

	conn, err := net.Dial("udp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1024)
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte("global:hello"); !bytes.Equal(buf[:n], want) {
		t.Fatalf("echo = %q, want %q", buf[:n], want)
	}
	if server.SessionCount() != 1 {
		t.Fatalf("session count = %d, want 1", server.SessionCount())
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	if err := gateway.Stop(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	if count := gateway.Runtime().Sessions().Count(); count != 0 {
		t.Fatalf("runtime session count = %d, want 0", count)
	}
	if plugin.closed == 0 {
		t.Fatal("plugin OnClose was not called")
	}
}

func TestUDPSessionTTL(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithSessionTTL(20*time.Millisecond),
		WithSweepInterval(10*time.Millisecond),
		WithHandler(func(core.Session, core.Message) error {
			return nil
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

	conn, err := net.Dial("udp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("touch")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if server.SessionCount() == 0 && gateway.Runtime().Sessions().Count() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("session did not expire: server=%d runtime=%d", server.SessionCount(), gateway.Runtime().Sessions().Count())
}

func TestGatewayUDPPluginDropSuppressesResponseAndKeepsSession(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	gateway := runtime.NewGateway(runtime.WithPlugins(conditionalDropPlugin{drop: "drop"}))
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = gateway.Stop(shutdownCtx)
	}()

	conn, err := net.Dial("udp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("drop")); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1024)
	if n, err := conn.Read(buf); err == nil {
		t.Fatalf("received dropped datagram response %q", buf[:n])
	}
	if server.SessionCount() != 1 {
		t.Fatalf("session count = %d, want 1", server.SessionCount())
	}
	if _, err := conn.Write([]byte("keep")); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "keep" {
		t.Fatalf("echo = %q, want keep", buf[:n])
	}
}
