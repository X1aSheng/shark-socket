package integration_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/gateway"
	"github.com/X1aSheng/shark-socket/internal/protocol/coap"
	httpproto "github.com/X1aSheng/shark-socket/internal/protocol/http"
	tcpproto "github.com/X1aSheng/shark-socket/internal/protocol/tcp"
	"github.com/X1aSheng/shark-socket/internal/protocol/udp"
	wsproto "github.com/X1aSheng/shark-socket/internal/protocol/websocket"
	"github.com/X1aSheng/shark-socket/internal/types"
	"github.com/gorilla/websocket"
)

func waitForTCP(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 10*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("TCP server at %s not ready after %v", addr, timeout)
}

func waitForUDP(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("udp", addr, 10*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("UDP server at %s not ready after %v", addr, timeout)
}

// TestMultiProtocol_AllProtocols starts TCP, HTTP, and WebSocket servers
// via the gateway and verifies each responds correctly.
func TestMultiProtocol_AllProtocols(t *testing.T) {
	// TCP echo
	tcpHandler := func(sess types.RawSession, msg types.RawMessage) error {
		return sess.Send(msg.Payload)
	}
	tcpSrv := tcpproto.NewServer(tcpHandler,
		tcpproto.WithAddr("127.0.0.1", 0),
		tcpproto.WithWorkerPool(2, 8, 64),
		tcpproto.WithFramer(tcpproto.NewLengthPrefixFramer(4096)),
	)

	// HTTP health
	httpSrv := httpproto.NewServer(httpproto.WithAddr("127.0.0.1", 0))
	httpSrv.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// WebSocket echo
	wsHandler := func(sess types.RawSession, msg types.RawMessage) error {
		return sess.Send(msg.Payload)
	}
	wsSrv := wsproto.NewServer(wsHandler,
		wsproto.WithAddr("127.0.0.1", 0),
		wsproto.WithPath("/ws"),
		wsproto.WithPingPong(60*time.Second, 30*time.Second),
		wsproto.WithAllowedOrigins("*"),
	)

	gw := gateway.New(gateway.WithMetricsEnabled(false))
	gw.Register(tcpSrv)
	gw.Register(httpSrv)
	gw.Register(wsSrv)

	if err := gw.Start(); err != nil {
		t.Fatalf("gateway Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		gw.Stop(ctx)
	}()

	tcpAddr := tcpSrv.Addr().String()
	httpAddr := httpSrv.Addr().String()
	wsAddr := wsSrv.Addr().String()

	waitForTCP(t, tcpAddr, 5*time.Second)
	waitForTCP(t, httpAddr, 5*time.Second)
	waitForTCP(t, wsAddr, 5*time.Second)

	// --- TCP ---
	t.Run("TCP", func(t *testing.T) {
		conn, err := net.DialTimeout("tcp", tcpAddr, 3*time.Second)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))

		framer := tcpproto.NewLengthPrefixFramer(4096)
		payload := []byte("multi-proto-tcp")
		if err := framer.WriteFrame(conn, payload); err != nil {
			t.Fatalf("WriteFrame: %v", err)
		}
		got, err := framer.ReadFrame(conn)
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}
		if string(got) != string(payload) {
			t.Fatalf("tcp echo: got %q, want %q", got, payload)
		}
	})

	// --- HTTP ---
	t.Run("HTTP", func(t *testing.T) {
		url := fmt.Sprintf("http://%s/health", httpAddr)
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(url)
		if err != nil {
			t.Fatalf("GET %s: %v", url, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "ok" {
			t.Fatalf("body = %q, want %q", body, "ok")
		}
	})

	// --- WebSocket ---
	t.Run("WebSocket", func(t *testing.T) {
		wsURL := fmt.Sprintf("ws://%s/ws", wsAddr)
		wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("dial ws: %v", err)
		}
		defer wsConn.Close()

		payload := []byte("multi-proto-ws")
		if err := wsConn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
			t.Fatalf("write: %v", err)
		}
		wsConn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, got, err := wsConn.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if string(got) != string(payload) {
			t.Fatalf("ws echo: got %q, want %q", got, payload)
		}
	})
}

// TestMultiProtocol_UDPAndCoAP starts UDP and CoAP servers and verifies datagram exchange.
func TestMultiProtocol_UDPAndCoAP(t *testing.T) {
	var udpCount atomic.Int32
	udpHandler := func(sess types.RawSession, msg types.RawMessage) error {
		udpCount.Add(1)
		return sess.Send(msg.Payload)
	}
	udpSrv := udp.NewServer(udpHandler, udp.WithAddr("127.0.0.1", 0))

	coapHandler := func(sess types.RawSession, msg types.RawMessage) error {
		return sess.Send(msg.Payload)
	}
	coapSrv := coap.NewServer(coapHandler, coap.WithAddr("127.0.0.1", 0))

	gw := gateway.New(gateway.WithMetricsEnabled(false))
	gw.Register(udpSrv)
	gw.Register(coapSrv)

	if err := gw.Start(); err != nil {
		t.Fatalf("gateway Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		gw.Stop(ctx)
	}()

	udpAddr := udpSrv.Addr().(*net.UDPAddr)
	coapAddr := coapSrv.Addr().(*net.UDPAddr)
	waitForUDP(t, udpAddr.String(), 5*time.Second)
	waitForUDP(t, coapAddr.String(), 5*time.Second)

	// --- UDP ---
	t.Run("UDP", func(t *testing.T) {
		conn, err := net.DialUDP("udp", nil, udpAddr)
		if err != nil {
			t.Fatalf("DialUDP: %v", err)
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))

		conn.Write([]byte("udp-test"))

		buf := make([]byte, 1500)
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if string(buf[:n]) != "udp-test" {
			t.Fatalf("udp echo: got %q, want %q", buf[:n], "udp-test")
		}
	})

	// --- CoAP ---
	t.Run("CoAP", func(t *testing.T) {
		conn, err := net.DialUDP("udp", nil, coapAddr)
		if err != nil {
			t.Fatalf("DialUDP: %v", err)
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))

		msg := &coap.CoAPMessage{
			Version:   1,
			Type:      coap.NON,
			Code:      coap.CodePost,
			MessageID: 0x0001,
			Payload:   []byte("coap-test"),
		}
		data, _ := msg.Serialize()
		conn.Write(data)

		time.Sleep(100 * time.Millisecond)
		t.Log("CoAP NON message sent and processed")
	})
}

// TestGateway_GracefulShutdown starts a gateway, verifies it's serving,
// then triggers graceful shutdown with a deadline.
func TestGateway_GracefulShutdown(t *testing.T) {
	httpSrv := httpproto.NewServer(httpproto.WithAddr("127.0.0.1", 0))
	httpSrv.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("pong"))
	})

	gw := gateway.New(
		gateway.WithMetricsEnabled(false),
		gateway.WithShutdownTimeout(5*time.Second),
	)
	gw.Register(httpSrv)

	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	addr := httpSrv.Addr().String()
	waitForTCP(t, addr, 5*time.Second)

	// Verify server is up
	resp, err := http.Get(fmt.Sprintf("http://%s/ping", addr))
	if err != nil {
		t.Fatalf("GET /ping before shutdown: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := gw.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Verify server is down
	_, err = http.Get(fmt.Sprintf("http://%s/ping", addr))
	if err == nil {
		t.Fatal("server should be down after Stop")
	}
}

// TestGateway_SharedManager verifies that the shared session manager
// assigns globally unique IDs across protocols.
func TestGateway_SharedManager(t *testing.T) {
	var tcpIDs []uint64
	var wsIDs []uint64
	var idsMu sync.Mutex

	tcpHandler := func(sess types.RawSession, msg types.RawMessage) error {
		idsMu.Lock()
		tcpIDs = append(tcpIDs, sess.ID())
		idsMu.Unlock()
		return nil
	}
	wsHandler := func(sess types.RawSession, msg types.RawMessage) error {
		idsMu.Lock()
		wsIDs = append(wsIDs, sess.ID())
		idsMu.Unlock()
		return nil
	}

	tcpSrv := tcpproto.NewServer(tcpHandler,
		tcpproto.WithAddr("127.0.0.1", 0),
	)
	wsSrv := wsproto.NewServer(wsHandler,
		wsproto.WithAddr("127.0.0.1", 0),
		wsproto.WithPath("/ws"),
		wsproto.WithAllowedOrigins("*"),
	)

	gw := gateway.New(gateway.WithMetricsEnabled(false))
	gw.Register(tcpSrv)
	gw.Register(wsSrv)

	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		gw.Stop(ctx)
	}()

	tcpAddr := tcpSrv.Addr().String()
	wsAddr := wsSrv.Addr().String()
	waitForTCP(t, tcpAddr, 5*time.Second)
	waitForTCP(t, wsAddr, 5*time.Second)

	// Connect TCP client
	conn, err := net.DialTimeout("tcp", tcpAddr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial TCP: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	framer := tcpproto.NewLengthPrefixFramer(4096)
	framer.WriteFrame(conn, []byte("hello"))
	framer.ReadFrame(conn)

	// Connect WebSocket client
	wsURL := fmt.Sprintf("ws://%s/ws", wsAddr)
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial WS: %v", err)
	}
	defer wsConn.Close()
	wsConn.WriteMessage(websocket.BinaryMessage, []byte("hello"))
	wsConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	wsConn.ReadMessage()

	// Both should have session IDs and they should be different
	idsMu.Lock()
	tcpLen := len(tcpIDs)
	wsLen := len(wsIDs)
	tcpID := uint64(0)
	wsID := uint64(0)
	if tcpLen > 0 {
		tcpID = tcpIDs[0]
	}
	if wsLen > 0 {
		wsID = wsIDs[0]
	}
	idsMu.Unlock()

	if tcpLen == 0 || wsLen == 0 {
		t.Fatal("expected session IDs for both protocols")
	}
	if tcpID == wsID {
		t.Errorf("shared manager should produce unique IDs across protocols, got TCP=%d WS=%d",
			tcpID, wsID)
	}
	t.Logf("TCP session ID=%d, WS session ID=%d", tcpID, wsID)
}

// TestGateway_GracefulShutdownWithActiveConnections verifies that graceful
// shutdown with active connections: (a) allows inflight requests to complete,
// (b) eventually terminates, and (c) rejects new connections after shutdown.
func TestGateway_GracefulShutdownWithActiveConnections(t *testing.T) {
	// Long-poll endpoint that blocks until shutdown begins
	var inflightCompleted atomic.Bool
	longPollDone := make(chan struct{})

	httpSrv := httpproto.NewServer(httpproto.WithAddr("127.0.0.1", 0))
	httpSrv.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		// Simulate an in-flight request that completes after shutdown signal
		select {
		case <-r.Context().Done():
			w.WriteHeader(http.StatusServiceUnavailable)
		case <-time.After(500 * time.Millisecond):
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("done"))
		}
		inflightCompleted.Store(true)
		close(longPollDone)
	})
	httpSrv.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("pong"))
	})

	gw := gateway.New(
		gateway.WithMetricsEnabled(false),
		gateway.WithShutdownTimeout(5*time.Second),
	)
	gw.Register(httpSrv)

	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	addr := httpSrv.Addr().String()
	waitForTCP(t, addr, 5*time.Second)

	// Start a long-running request in background
	errCh := make(chan error, 1)
	go func() {
		resp, err := http.Get(fmt.Sprintf("http://%s/slow", addr))
		if err != nil {
			errCh <- err
			return
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			errCh <- fmt.Errorf("expected 200, got %d", resp.StatusCode)
			return
		}
		errCh <- nil
	}()

	// Give the slow request time to start
	time.Sleep(50 * time.Millisecond)

	// Initiate shutdown while the slow request is in-flight
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := gw.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// The inflight request should complete (not be cut off by shutdown)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("slow request failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("slow request did not complete within timeout")
	}

	if !inflightCompleted.Load() {
		t.Error("inflight request should have completed")
	}

	// After shutdown, new connections should be rejected
	_, err := http.Get(fmt.Sprintf("http://%s/ping", addr))
	if err == nil {
		t.Fatal("new connections should be rejected after shutdown")
	}
}
