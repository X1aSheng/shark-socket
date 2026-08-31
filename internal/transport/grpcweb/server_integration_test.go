package grpcweb

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/infra/observability"
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

// TestGRPCWebHandlerErrorSendsOnlyTrailers is the regression test for the
// V8 P3-7 fix: a handler error must be reported exclusively through the
// trailer frame (grpc-status 13). Issuing http.Error afterwards would append
// a superfluous HTTP status + text body after the committed trailer frame —
// a gRPC-Web protocol violation. readTestGRPCWebResponse fails on any
// non-frame content, so this test fails if anything is written after the
// trailer.
func TestGRPCWebHandlerErrorSendsOnlyTrailers(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithCheckOrigin(func(*http.Request) bool { return true }),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return fmt.Errorf("handler boom")
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
	// Frame-level errors keep HTTP 200; the failure travels in the trailer.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %q", resp.StatusCode, body)
	}
	payload, trailers := readTestGRPCWebResponse(t, body)
	if len(payload) != 0 {
		t.Fatalf("data payload = %q, want none after handler error", payload)
	}
	if !bytes.Contains(trailers, []byte("grpc-status: 13")) {
		t.Fatalf("trailers = %q, want grpc-status 13", trailers)
	}
	if !bytes.Contains(trailers, []byte("grpc-message: handler boom")) {
		t.Fatalf("trailers = %q, want grpc-message handler boom", trailers)
	}
	if count := gateway.Runtime().Sessions().Count(); count != 0 {
		t.Fatalf("session count = %d, want 0", count)
	}
}

// TestGRPCWebMaxConnectionsRejectsExcess verifies the accept-cap wiring end
// to end: with a cap of 1, a second concurrent request is rejected with 503
// while the first is still in flight.
func TestGRPCWebMaxConnectionsRejectsExcess(t *testing.T) {
	release := make(chan struct{})
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithMaxConnections(1),
		WithCheckOrigin(func(*http.Request) bool { return true }),
		WithHandler(func(sess core.Session, msg core.Message) error {
			<-release
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

	newRequest := func() *http.Request {
		req, err := http.NewRequest(http.MethodPost, "http://"+server.Addr().String()+"/grpc", bytes.NewReader(testGRPCWebDataFrame([]byte("hello"))))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("content-type", "application/grpc-web+proto")
		req.Header.Set("x-grpc-web", "1")
		return req
	}

	// First request blocks in the handler, holding the only slot.
	done1 := make(chan error, 1)
	resp1Ch := make(chan *http.Response, 1)
	go func() {
		resp, err := http.DefaultClient.Do(newRequest())
		if err != nil {
			done1 <- err
			return
		}
		resp1Ch <- resp
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		done1 <- nil
	}()
	// Give the first request time to register before the second arrives.
	time.Sleep(200 * time.Millisecond)

	resp2, err := http.DefaultClient.Do(newRequest())
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp2.StatusCode)
	}

	close(release)
	if err := <-done1; err != nil {
		t.Fatal(err)
	}
	resp1 := <-resp1Ch
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", resp1.StatusCode)
	}
}

// TestGRPCWebServerDirectStop covers the non-staged Stop path (previously
// 0%): a server started without a gateway serves traffic and Stop shuts the
// listener down.
func TestGRPCWebServerDirectStop(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
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

	post := func() error {
		resp, err := http.Post("http://"+server.Addr().String()+"/grpc", "text/plain", bytes.NewReader([]byte("hello")))
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		if string(body) != "hello" {
			return fmt.Errorf("body = %q, want hello", body)
		}
		return nil
	}
	if err := post(); err != nil {
		t.Fatal(err)
	}

	if err := server.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := post(); err == nil {
		t.Fatal("server still accepting after Stop")
	}
}

// TestGRPCWebWSDeadPeerReclaimed verifies that a WebSocket-mode peer which
// never answers the server's pings (a vanished client) is reclaimed after
// PongTimeout instead of holding a goroutine/session forever, and that the
// reclaim is counted in sessions_reclaimed_total.
func TestGRPCWebWSDeadPeerReclaimed(t *testing.T) {
	metrics := observability.NewMemoryMetrics()
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithWebSocketMode("/grpc/ws"),
		WithCheckOrigin(func(*http.Request) bool { return true }),
		WithPingInterval(50*time.Millisecond),
		WithPongTimeout(150*time.Millisecond),
		WithHandler(func(sess core.Session, msg core.Message) error { return nil }),
	)
	gateway := runtime.NewGateway(runtime.WithMetrics(metrics))
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer stopGateway(t, gateway)

	u := url.URL{Scheme: "ws", Host: server.Addr().String(), Path: "/grpc/ws"}
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Wait for the session to register (the upgrade returns before the
	// server-side session is stored), then never read: the peer never answers
	// the server's pings, so the read deadline expires after PongTimeout.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if gateway.Runtime().Sessions().Count() >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("session not registered after upgrade")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for {
		if gateway.Runtime().Sessions().Count() == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("dead gRPC-Web WebSocket peer not reclaimed after PongTimeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := metrics.Counter("sessions_reclaimed_total"); got < 1 {
		t.Fatalf("sessions_reclaimed_total = %v, want >= 1", got)
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
