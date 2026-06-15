package grpcweb

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
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

func TestGRPCWebFramedUnaryEchoWithTrailers(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithCheckOrigin(func(*http.Request) bool { return true }),
		WithHandler(func(sess core.Session, msg core.Message) error {
			if string(msg.Payload) != "hello" {
				return fmt.Errorf("payload = %q, want hello", msg.Payload)
			}
			return sess.Send([]byte("world"))
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

	req, err := http.NewRequest(http.MethodPost, "http://"+server.Addr().String()+"/grpc", bytes.NewReader(testGRPCWebDataFrame([]byte("hello"))))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("content-type", "application/grpc-web+proto")
	req.Header.Set("x-grpc-web", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %q", resp.StatusCode, body)
	}
	payload, trailers := readTestGRPCWebResponse(t, body)
	if string(payload) != "world" {
		t.Fatalf("payload = %q, want world", payload)
	}
	if !bytes.Contains(trailers, []byte("grpc-status: 0")) {
		t.Fatalf("trailers = %q, want grpc-status 0", trailers)
	}
}

func TestGRPCWebMaxMessageSize(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithMaxMessageBytes(3),
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

func TestGRPCWebStrictMalformedFrameReturnsBadRequest(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
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
	defer stopGateway(t, gateway)

	req, err := http.NewRequest(http.MethodPost, "http://"+server.Addr().String()+"/grpc", bytes.NewReader([]byte{0, 0, 0, 0, 5, 'h'}))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("content-type", "application/grpc-web+proto")
	req.Header.Set("x-grpc-web", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func testGRPCWebDataFrame(payload []byte) []byte {
	frame := []byte{0}
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(payload)))
	return append(frame, payload...)
}

func readTestGRPCWebResponse(t *testing.T, body []byte) ([]byte, []byte) {
	t.Helper()
	var payload []byte
	var trailers []byte
	for len(body) > 0 {
		if len(body) < 5 {
			t.Fatalf("truncated frame header in %q", body)
		}
		flag := body[0]
		size := int(binary.BigEndian.Uint32(body[1:5]))
		body = body[5:]
		if len(body) < size {
			t.Fatalf("truncated frame payload")
		}
		switch flag {
		case 0:
			payload = append(payload, body[:size]...)
		case 0x80:
			trailers = append(trailers, body[:size]...)
		default:
			t.Fatalf("unexpected frame flag 0x%02x", flag)
		}
		body = body[size:]
	}
	return payload, trailers
}

func TestGRPCWebWebSocketEchoAndCleanup(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithWebSocketMode("/grpc/ws"),
		WithCheckOrigin(func(*http.Request) bool { return true }),
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
