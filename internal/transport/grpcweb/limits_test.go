package grpcweb

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/infra/observability"
	"github.com/X1aSheng/shark-socket/internal/runtime"
	"github.com/gorilla/websocket"
)

// clientFrom returns an HTTP client whose connections leave from localIP, so a
// test can act as several distinct peers without forging anything.
func clientFrom(localIP string) *http.Client {
	return &http.Client{
		// No keep-alives: the gate counts in-flight requests, and a pooled
		// connection would hide the next request from it.
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(localIP)}}
				return d.DialContext(ctx, network, addr)
			},
		},
		Timeout: 5 * time.Second,
	}
}

func newGRPCWebRequest(t *testing.T, addr string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/grpc", bytes.NewReader(testGRPCWebDataFrame([]byte("hello"))))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("content-type", "application/grpc-web+proto")
	req.Header.Set("x-grpc-web", "1")
	return req
}

// post issues a request and returns the status code (0 on transport error).
func post(client *http.Client, req *http.Request) int {
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// TestGRPCWebPerPeerConnectionLimit is A5: one host must not be able to occupy
// the whole session budget, and refusing it must leave other peers served.
func TestGRPCWebPerPeerConnectionLimit(t *testing.T) {
	release := make(chan struct{})
	var inFlight atomic.Int64
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		// Global room for ten, one per peer: only the per-peer rule can refuse
		// the second in-flight request.
		WithMaxConnections(10),
		WithMaxConnectionsPerIP(1),
		WithHandler(func(sess core.Session, msg core.Message) error {
			inFlight.Add(1)
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

	addr := server.Addr().String()
	peer := clientFrom("127.0.0.2")

	// The first request blocks in the handler, holding the peer's only slot.
	firstDone := make(chan int, 1)
	go func() { firstDone <- post(peer, newGRPCWebRequest(t, addr)) }()
	waitForInFlight(t, &inFlight, 1)

	// Same peer, second in-flight request: refused although the global cap has
	// nine slots free.
	if got := post(peer, newGRPCWebRequest(t, addr)); got != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 for the second in-flight request of one peer", got)
	}
	if got := inFlight.Load(); got != 1 {
		t.Fatalf("in-flight requests = %d, want 1: the refused request reached the handler", got)
	}

	// A different peer must still be admitted while the throttled one waits:
	// that is what separates a per-peer rule from a global drain.
	otherDone := make(chan int, 1)
	go func() { otherDone <- post(clientFrom("127.0.0.3"), newGRPCWebRequest(t, addr)) }()
	waitForInFlight(t, &inFlight, 2)

	close(release)
	if got := <-firstDone; got != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", got)
	}
	if got := <-otherDone; got == http.StatusServiceUnavailable {
		t.Fatal("second peer was refused while the first peer held its slot")
	}
}

func waitForInFlight(t *testing.T, n *atomic.Int64, want int64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if n.Load() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d in-flight requests (got %d)", want, n.Load())
}

// TestGRPCWebRejectedRequestIsCounted pins the admission counter for direct
// mode. A request refused with 503 used to leave no trace in the metrics (D3).
func TestGRPCWebRejectedRequestIsCounted(t *testing.T) {
	metrics := observability.NewMemoryMetrics()
	release := make(chan struct{})
	var inFlight atomic.Int64
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithMaxConnections(1),
		WithMaxConnectionsPerIP(1),
		WithHandler(func(sess core.Session, msg core.Message) error {
			inFlight.Add(1)
			<-release
			return sess.Send(msg.Payload)
		}),
	)
	gateway := runtime.NewGateway(runtime.WithMetrics(metrics))
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer stopGateway(t, gateway)

	addr := server.Addr().String()
	peer := clientFrom("127.0.0.2")

	// The first request blocks in the handler, holding the peer's only slot.
	firstDone := make(chan int, 1)
	go func() { firstDone <- post(peer, newGRPCWebRequest(t, addr)) }()
	waitForInFlight(t, &inFlight, 1)

	if got := post(peer, newGRPCWebRequest(t, addr)); got != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 for the second in-flight request", got)
	}
	if got := metrics.Counter("sessions_rejected_total", "protocol", string(core.ProtocolGRPCWeb)); got != 1 {
		t.Fatalf("sessions_rejected_total{protocol=grpc-web} = %v, want 1", got)
	}

	close(release)
	if got := <-firstDone; got != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", got)
	}
}

// TestGRPCWebRejectedUpgradeIsCounted covers the second entry point: a
// WebSocket-mode upgrade refused for capacity must be counted too, otherwise
// one of the two ways into this transport is invisible (D3).
func TestGRPCWebRejectedUpgradeIsCounted(t *testing.T) {
	metrics := observability.NewMemoryMetrics()
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithWebSocketMode("/grpc/ws"),
		WithCheckOrigin(func(*http.Request) bool { return true }),
		WithMaxConnections(1),
		WithMaxConnectionsPerIP(1),
		WithHandler(func(core.Session, core.Message) error { return nil }),
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
	first, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("first upgrade with the only slot free: %v", err)
	}
	defer first.Close()

	second, resp, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err == nil {
		second.Close()
		t.Fatal("second upgrade succeeded despite MaxConnections=1")
	}
	if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %v, want 503", resp)
	}
	if got := metrics.Counter("sessions_rejected_total", "protocol", string(core.ProtocolGRPCWeb)); got != 1 {
		t.Fatalf("sessions_rejected_total{protocol=grpc-web} = %v, want 1", got)
	}
}
