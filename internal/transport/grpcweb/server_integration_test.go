package grpcweb

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket-new/internal/core"
	"github.com/X1aSheng/shark-socket-new/internal/runtime"
	"github.com/gorilla/websocket"
)

type prefixPlugin struct {
	core.BasePlugin
	prefix []byte
}

func (p prefixPlugin) Name() string { return "grpcweb-prefix" }

func (p prefixPlugin) OnMessage(_ core.Session, data []byte) ([]byte, error) {
	out := append([]byte(nil), p.prefix...)
	out = append(out, data...)
	return out, nil
}

func TestGRPCWebDirectEchoAndCleanup(t *testing.T) {
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
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer stopGateway(t, gateway)

	resp, err := http.Post("http://"+server.Addr().String()+"/grpc", "application/grpc-web+proto", bytes.NewReader([]byte("hello")))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "global:hello" {
		t.Fatalf("body = %q, want global:hello", body)
	}
	if count := gateway.Runtime().Sessions().Count(); count != 0 {
		t.Fatalf("session count = %d, want 0", count)
	}
}

func TestGRPCWebMaxMessageSize(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithMaxMessageBytes(3),
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
	defer stopGateway(t, gateway)

	resp, err := http.Post("http://"+server.Addr().String()+"/grpc", "application/grpc-web+proto", bytes.NewReader([]byte("hello")))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusRequestEntityTooLarge)
	}
}

func TestGRPCWebWebSocketEchoAndCleanup(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithWebSocketMode("/grpc/ws"),
		WithHandler(func(sess core.Session, msg core.Message) error {
			if msg.Protocol != core.ProtocolGRPCWeb {
				return fmt.Errorf("protocol = %s, want %s", msg.Protocol, core.ProtocolGRPCWeb)
			}
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
	defer stopGateway(t, gateway)

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+server.Addr().String()+"/grpc/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "global:hello" {
		t.Fatalf("payload = %q, want global:hello", payload)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
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

func TestGRPCWebWebSocketMaxMessageSize(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithWebSocketMode("/grpc/ws"),
		WithMaxMessageBytes(3),
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
	defer stopGateway(t, gateway)

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+server.Addr().String()+"/grpc/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("expected read error after max message size violation")
	}
}

func TestGRPCWebWebSocketOriginRejected(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithWebSocketMode("/grpc/ws"),
		WithCheckOrigin(func(*http.Request) bool { return false }),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer stopGateway(t, gateway)

	_, resp, err := websocket.DefaultDialer.Dial("ws://"+server.Addr().String()+"/grpc/ws", nil)
	if err == nil {
		t.Fatal("expected origin rejection")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("response = %#v, want 403", resp)
	}
}

func stopGateway(t *testing.T, gateway *runtime.Gateway) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := gateway.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}
