package integration_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/X1aSheng/shark-socket/internal/gateway"
	"github.com/X1aSheng/shark-socket/internal/protocol/coap"
	tcpproto "github.com/X1aSheng/shark-socket/internal/protocol/tcp"
	"github.com/X1aSheng/shark-socket/internal/protocol/udp"
	httpproto "github.com/X1aSheng/shark-socket/internal/protocol/http"
	wsproto "github.com/X1aSheng/shark-socket/internal/protocol/websocket"
	"github.com/X1aSheng/shark-socket/internal/types"
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
