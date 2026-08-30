package udp

import (
	"bytes"
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
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

// TestGatewayUDPHandlerPanicKeepsServerAlive is the regression test for the
// V8 P1-2 fix that wrapped the plain-UDP read loop in shared.CallHandler: a
// panicking user handler must be recovered (dropping the pseudo-session)
// instead of crashing the process, and the server keeps serving.
func TestGatewayUDPHandlerPanicKeepsServerAlive(t *testing.T) {
	var calls atomic.Int32
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(func(sess core.Session, msg core.Message) error {
			if calls.Add(1) == 1 {
				panic("user handler boom")
			}
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

	// First datagram panics in the handler. The panic is recovered and the
	// pseudo-session dropped; the process survives.
	if _, err := conn.Write([]byte("boom")); err != nil {
		t.Fatal(err)
	}
	// Second datagram proves the server is still alive: it is echoed back.
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "ping" {
		t.Fatalf("echo = %q, want ping", buf[:n])
	}
	if server.SessionCount() != 1 {
		t.Fatalf("session count = %d, want 1", server.SessionCount())
	}
}

// failAcceptPlugin rejects every OnAccept and counts OnAccept/OnClose calls.
type failAcceptPlugin struct {
	core.BasePlugin
	acceptCalls atomic.Int32
	closeCalls  atomic.Int32
}

func (p *failAcceptPlugin) Name() string { return "udp-fail-accept" }

func (p *failAcceptPlugin) OnAccept(core.Session) error {
	p.acceptCalls.Add(1)
	return core.ErrPluginBlock
}

func (p *failAcceptPlugin) OnClose(core.Session) { p.closeCalls.Add(1) }

// TestGatewayUDPOnAcceptFailureNoSession covers the getOrCreateSession
// OnAccept-failure branch (previously uncovered): a plugin that rejects the
// peer's OnAccept must leave no session behind, produce no response, and
// must not fire OnClose for a session that was never accepted.
func TestGatewayUDPOnAcceptFailureNoSession(t *testing.T) {
	blocker := &failAcceptPlugin{}
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	gateway := runtime.NewGateway(runtime.WithPlugins(blocker))
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
	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	// No response: the plugin rejected the peer before the handler ran.
	if err := conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1024)
	if n, err := conn.Read(buf); err == nil {
		t.Fatalf("unexpected response from rejected peer: %q", buf[:n])
	}
	// The plugin was asked exactly once and never closed (never accepted).
	if got := blocker.acceptCalls.Load(); got != 1 {
		t.Fatalf("accept calls = %d, want 1", got)
	}
	if got := blocker.closeCalls.Load(); got != 0 {
		t.Fatalf("close calls = %d, want 0 (session never accepted)", got)
	}
	if server.SessionCount() != 0 {
		t.Fatalf("session count = %d, want 0", server.SessionCount())
	}
	if count := gateway.Runtime().Sessions().Count(); count != 0 {
		t.Fatalf("runtime session count = %d, want 0", count)
	}
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
