package websocket

import (
	"context"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
	gws "github.com/gorilla/websocket"
)

type prefixPlugin struct {
	core.BasePlugin
	prefix []byte
}

func (p prefixPlugin) Name() string { return "ws-prefix" }

func (p prefixPlugin) OnMessage(_ core.Session, data []byte) ([]byte, error) {
	out := append([]byte(nil), p.prefix...)
	out = append(out, data...)
	return out, nil
}

type closeCountingPlugin struct {
	core.BasePlugin
	closed atomic.Int32
}

func (p *closeCountingPlugin) Name() string { return "close-counting" }

func (p *closeCountingPlugin) OnClose(core.Session) {
	p.closed.Add(1)
}

// TestWebSocketHandlerPanicClosesSession verifies that a panicking user
// handler is recovered by shared.CallHandler: the session is closed (the
// client sees the connection drop) and the server keeps accepting new
// connections.
func TestWebSocketHandlerPanicClosesSession(t *testing.T) {
	var calls atomic.Int32
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithPath("/ws"),
		WithCheckOrigin(func(*http.Request) bool { return true }),
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

	u := url.URL{Scheme: "ws", Host: server.Addr().String(), Path: "/ws"}

	// First connection: the panicking handler must close the session, so the
	// client read observes the close instead of the process crashing.
	conn, _, err := gws.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(gws.BinaryMessage, []byte("boom")); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("expected connection to be closed after handler panic")
	}
	_ = conn.Close()

	// Second connection proves the server is still alive and serving.
	conn2, _, err := gws.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("server not serving after handler panic: %v", err)
	}
	defer conn2.Close()
	if err := conn2.WriteMessage(gws.BinaryMessage, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	if err := conn2.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, got, err := conn2.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ping" {
		t.Fatalf("echo = %q, want ping", got)
	}
}

// TestWebSocketMaxConnectionsRejectsExcess verifies the accept-cap wiring end
// to end: with a cap of 1, the first upgrade succeeds and a second
// concurrent upgrade is rejected with 503.
func TestWebSocketMaxConnectionsRejectsExcess(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithPath("/ws"),
		WithMaxConnections(1),
		WithCheckOrigin(func(*http.Request) bool { return true }),
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
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = gateway.Stop(shutdownCtx)
	}()

	u := url.URL{Scheme: "ws", Host: server.Addr().String(), Path: "/ws"}
	// First connection is served and stays open, exhausting the cap.
	conn1, _, err := gws.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn1.Close()

	_, resp, err := gws.DefaultDialer.Dial(u.String(), nil)
	if err == nil {
		t.Fatal("excess upgrade succeeded")
	}
	if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %v, want 503", resp)
	}
}

// TestWebSocketServerDirectStop covers the non-staged Stop path (previously
// 0%): a server started without a gateway serves traffic and Stop shuts the
// listener down.
func TestWebSocketServerDirectStop(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithPath("/ws"),
		WithCheckOrigin(func(*http.Request) bool { return true }),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Start(ctx); err != nil {
		t.Fatal(err)
	}

	u := url.URL{Scheme: "ws", Host: server.Addr().String(), Path: "/ws"}
	conn, _, err := gws.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(gws.BinaryMessage, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, got, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ping" {
		t.Fatalf("echo = %q, want ping", got)
	}
	_ = conn.Close()

	if err := server.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := gws.DefaultDialer.Dial(u.String(), nil); err == nil {
		t.Fatal("server still accepting after Stop")
	}
}

func TestWebSocketGatewayEchoAndShutdown(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithPath("/ws"),
		WithCheckOrigin(func(*http.Request) bool { return true }),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	gateway := runtime.NewGateway(runtime.WithPlugins(prefixPlugin{prefix: []byte("global:")}))
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	u := url.URL{Scheme: "ws", Host: server.Addr().String(), Path: "/ws"}
	conn, _, err := gws.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(gws.BinaryMessage, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, got, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "global:hello" {
		t.Fatalf("echo = %q, want global:hello", got)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := gateway.Stop(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	if count := gateway.Runtime().Sessions().Count(); count != 0 {
		t.Fatalf("session count = %d, want 0", count)
	}
}

// TestWebSocketDeadPeerReclaimed verifies that a peer which never answers the
// server's pings (a vanished client) is reclaimed after PongTimeout instead of
// holding a goroutine/session forever.
func TestWebSocketDeadPeerReclaimed(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithPath("/ws"),
		WithCheckOrigin(func(*http.Request) bool { return true }),
		WithPingInterval(50*time.Millisecond),
		WithPongTimeout(150*time.Millisecond),
		WithHandler(func(sess core.Session, msg core.Message) error { return nil }),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = gateway.Stop(shutdownCtx)
	}()

	u := url.URL{Scheme: "ws", Host: server.Addr().String(), Path: "/ws"}
	conn, _, err := gws.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Never read from the connection, so the peer never answers the server's
	// pings; the server must reclaim the session after PongTimeout.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if gateway.Runtime().Sessions().Count() == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("dead WebSocket peer not reclaimed after PongTimeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWebSocketOnCloseRunsOnceDuringShutdown(t *testing.T) {
	plugin := &closeCountingPlugin{}
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithPath("/ws"),
		WithCheckOrigin(func(*http.Request) bool { return true }),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	gateway := runtime.NewGateway(runtime.WithPlugins(plugin))
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	u := url.URL{Scheme: "ws", Host: server.Addr().String(), Path: "/ws"}
	conn, _, err := gws.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if gateway.Runtime().Sessions().Count() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if count := gateway.Runtime().Sessions().Count(); count != 1 {
		t.Fatalf("session count = %d, want 1", count)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := gateway.Stop(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	if got := plugin.closed.Load(); got != 1 {
		t.Fatalf("OnClose calls = %d, want 1", got)
	}
}

func TestWebSocketOriginRejected(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithCheckOrigin(func(*http.Request) bool { return false }),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = gateway.Stop(ctx)
	}()

	u := url.URL{Scheme: "ws", Host: server.Addr().String(), Path: "/ws"}
	_, _, err := gws.DefaultDialer.Dial(u.String(), nil)
	if err == nil {
		t.Fatal("dial succeeded, want origin rejection")
	}
}

func TestWebSocketMaxMessageSizeClosesAndCleansSession(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithCheckOrigin(func(*http.Request) bool { return true }),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	server.opts.MaxMessageSize = 3
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = gateway.Stop(ctx)
	}()

	u := url.URL{Scheme: "ws", Host: server.Addr().String(), Path: "/ws"}
	conn, _, err := gws.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(gws.BinaryMessage, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("expected read error after max message size violation")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if gateway.Runtime().Sessions().Count() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("session count = %d, want 0", gateway.Runtime().Sessions().Count())
}
