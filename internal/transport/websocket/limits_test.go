package websocket

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/infra/observability"
	"github.com/X1aSheng/shark-socket/internal/runtime"
	gws "github.com/gorilla/websocket"
)

// dialWS upgrades a WebSocket connection from a chosen loopback address, so a
// test can act as several distinct peers without forging anything.
func dialWS(target, localIP string) (*gws.Conn, *http.Response, error) {
	dialer := &gws.Dialer{
		HandshakeTimeout: 3 * time.Second,
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			var d net.Dialer
			if localIP != "" {
				d.LocalAddr = &net.TCPAddr{IP: net.ParseIP(localIP)}
			}
			return d.DialContext(ctx, network, addr)
		},
	}
	return dialer.Dial(target, nil)
}

// TestWebSocketPerPeerConnectionLimit is A5: the global ceiling must not be
// reachable by one host, and refusing one host must leave the others serving.
func TestWebSocketPerPeerConnectionLimit(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithPath("/ws"),
		// Global room for ten, one per peer: only the per-peer rule can refuse
		// the second upgrade, so the test cannot pass for the wrong reason.
		WithMaxConnections(10),
		WithMaxConnectionsPerIP(1),
		WithCheckOrigin(func(*http.Request) bool { return true }),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	if err := server.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Stop(ctx)
	}()

	u := url.URL{Scheme: "ws", Host: server.Addr().String(), Path: "/ws"}
	target := u.String()

	first, _, err := dialWS(target, "127.0.0.2")
	if err != nil {
		t.Fatalf("first upgrade from 127.0.0.2: %v", err)
	}
	defer first.Close()
	if err := first.WriteMessage(gws.TextMessage, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	// Reading the echo proves the session is live, not merely that the upgrade
	// returned: a slot held by a half-open connection still counts.
	if _, got, err := first.ReadMessage(); err != nil || string(got) != "ping" {
		t.Fatalf("echo %q, err %v", got, err)
	}

	// Same peer, second upgrade: refused although the global cap has nine
	// slots free.
	second, resp, err := dialWS(target, "127.0.0.2")
	if err == nil {
		second.Close()
		t.Fatal("second upgrade from one peer succeeded despite MaxConnectionsPerIP=1")
	}
	if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %v, want 503", resp)
	}

	// A different peer must still be admitted - that is the point of splitting
	// the two dimensions.
	other, _, err := dialWS(target, "")
	if err != nil {
		t.Fatalf("upgrade from a second peer: %v", err)
	}
	defer other.Close()
	if err := other.WriteMessage(gws.TextMessage, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	if _, got, err := other.ReadMessage(); err != nil || string(got) != "ping" {
		t.Fatalf("echo %q, err %v from the second peer", got, err)
	}

	// Releasing the slot must let the throttled peer back in, otherwise the
	// quota leaks a slot per refused attempt.
	_ = first.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		reclaimed, _, dialErr := dialWS(target, "127.0.0.2")
		if dialErr == nil {
			reclaimed.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("peer was not re-admitted after releasing its slot (last dial error %v)", dialErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestWebSocketHeaderReadBoundedByReadTimeout pins the actual bound on a slow
// upgrade request.
//
// gorilla's HandshakeTimeout only covers writing the 101 response (it sets a
// *write* deadline), so a peer that sends half an upgrade header is bounded by
// http.Server.ReadTimeout instead. That distinction matters when tuning: the
// handshake timeout is not a slowloris defence here.
func TestWebSocketHeaderReadBoundedByReadTimeout(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithPath("/ws"),
		WithReadTimeout(150*time.Millisecond),
		WithCheckOrigin(func(*http.Request) bool { return true }),
	)
	if err := server.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Stop(ctx)
	}()

	conn, err := net.Dial("tcp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// An upgrade request without its terminating blank line: the request is
	// never complete, so the handler never runs.
	if _, err := conn.Write([]byte("GET /ws HTTP/1.1\r\nHost: shark\r\n")); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("incomplete upgrade request was answered; expected the server to close it")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("connection held for %v, want about the 150ms ReadTimeout", elapsed)
	}
}

// TestWebSocketRejectedUpgradeIsCounted pins the admission counter. A refused
// upgrade used to answer 503 without touching any metric, so a server that was
// turning every connection away still looked idle in the scrape (D3).
func TestWebSocketRejectedUpgradeIsCounted(t *testing.T) {
	metrics := observability.NewMemoryMetrics()
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithPath("/ws"),
		WithMaxConnections(1),
		WithMaxConnectionsPerIP(1),
		WithCheckOrigin(func(*http.Request) bool { return true }),
		WithHandler(func(core.Session, core.Message) error { return nil }),
	)
	gateway := runtime.NewGateway(runtime.WithMetrics(metrics))
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
	target := u.String()

	first, _, err := dialWS(target, "")
	if err != nil {
		t.Fatalf("first upgrade with the only slot free: %v", err)
	}
	defer first.Close()

	second, resp, err := dialWS(target, "")
	if err == nil {
		second.Close()
		t.Fatal("second upgrade succeeded despite MaxConnections=1")
	}
	if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %v, want 503", resp)
	}

	// The refusal is synchronous with the response, so the counter is already
	// in place once the client has the status line.
	if got := metrics.Counter("sessions_rejected_total", "protocol", string(core.ProtocolWS)); got != 1 {
		t.Fatalf("sessions_rejected_total{protocol=websocket} = %v, want 1", got)
	}
}
