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

func TestWebSocketGatewayEchoAndShutdown(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithPath("/ws"),
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

func TestWebSocketOnCloseRunsOnceDuringShutdown(t *testing.T) {
	plugin := &closeCountingPlugin{}
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithPath("/ws"),
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
