package tcp

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

//NOTE: WithClientTLS expects *crypto/tls.Config. The test for it is
// TestClient_WithClientTLS_Nil which verifies the option wiring with a nil
// value (real TLS would require cert generation).

// startEchoServer starts a TCP echo server on a random port.
// It reads a length-prefixed frame and echoes it back.
// Returns the listener (caller should defer close) and the actual address.
func startEchoServer(t *testing.T) (net.Listener, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go echoConn(conn)
		}
	}()
	return ln, ln.Addr().String()
}

// echoConn reads length-prefixed frames and writes them back.
func echoConn(conn net.Conn) {
	defer conn.Close()
	for {
		// Read 4-byte header
		header := make([]byte, 4)
		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}
		size := int(binary.BigEndian.Uint32(header))
		if size > 1024*1024 {
			return
		}
		payload := make([]byte, size)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}
		// Echo back: header + payload
		if _, err := conn.Write(append(header, payload...)); err != nil {
			return
		}
	}
}

func TestNewClient_DefaultOptions(t *testing.T) {
	c := NewClient("127.0.0.1:9999")
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.addr != "127.0.0.1:9999" {
		t.Fatalf("expected addr '127.0.0.1:9999', got %q", c.addr)
	}
	if c.timeout != 10*time.Second {
		t.Fatalf("expected default timeout 10s, got %v", c.timeout)
	}
	if c.reconnect {
		t.Fatal("expected reconnect to be false by default")
	}
	if c.maxRetry != 10 {
		t.Fatalf("expected default maxRetry 10, got %d", c.maxRetry)
	}
	if _, ok := c.framer.(*LengthPrefixFramer); !ok {
		t.Fatal("expected default framer to be LengthPrefixFramer")
	}
}

func TestClient_WithClientTimeout(t *testing.T) {
	c := NewClient("127.0.0.1:0", WithClientTimeout(2*time.Second))
	if c.timeout != 2*time.Second {
		t.Fatalf("expected timeout 2s, got %v", c.timeout)
	}
}

func TestClient_WithClientReconnect(t *testing.T) {
	c := NewClient("127.0.0.1:0", WithClientReconnect(true, 5))
	if !c.reconnect {
		t.Fatal("expected reconnect to be true")
	}
	if c.maxRetry != 5 {
		t.Fatalf("expected maxRetry 5, got %d", c.maxRetry)
	}
}

func TestClient_WithClientFramer(t *testing.T) {
	framer := NewLineFramer(4096)
	c := NewClient("127.0.0.1:0", WithClientFramer(framer))
	if c.framer != framer {
		t.Fatal("expected custom framer to be set")
	}
}

func TestClient_ConnectAndIsConnected(t *testing.T) {
	ln, addr := startEchoServer(t)
	defer ln.Close()

	c := NewClient(addr, WithClientTimeout(2*time.Second))
	defer c.Close()

	if c.IsConnected() {
		t.Fatal("should not be connected before Connect()")
	}

	if err := c.Connect(); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	if !c.IsConnected() {
		t.Fatal("should be connected after Connect()")
	}
}

func TestClient_RemoteAddr(t *testing.T) {
	ln, addr := startEchoServer(t)
	defer ln.Close()

	c := NewClient(addr, WithClientTimeout(2*time.Second))
	defer c.Close()

	// Before connecting, RemoteAddr should return nil
	if ra := c.RemoteAddr(); ra != nil {
		t.Fatalf("expected nil RemoteAddr before connect, got %v", ra)
	}

	if err := c.Connect(); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	ra := c.RemoteAddr()
	if ra == nil {
		t.Fatal("expected non-nil RemoteAddr after connect")
	}
	if ra.String() == "" {
		t.Fatal("expected non-empty RemoteAddr string")
	}
}

func TestClient_SendReceive_RoundTrip(t *testing.T) {
	ln, addr := startEchoServer(t)
	defer ln.Close()

	c := NewClient(addr, WithClientTimeout(2*time.Second))
	defer c.Close()

	if err := c.Connect(); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	payload := []byte("hello world")
	if err := c.Send(payload); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	got, err := c.Receive()
	if err != nil {
		t.Fatalf("Receive failed: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("expected %q, got %q", payload, got)
	}
}

func TestClient_SendReceive_Multiple(t *testing.T) {
	ln, addr := startEchoServer(t)
	defer ln.Close()

	c := NewClient(addr, WithClientTimeout(2*time.Second))
	defer c.Close()

	if err := c.Connect(); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	for i := 0; i < 10; i++ {
		msg := []byte("msg-" + string(rune('A'+i)))
		if err := c.Send(msg); err != nil {
			t.Fatalf("Send #%d failed: %v", i, err)
		}
		got, err := c.Receive()
		if err != nil {
			t.Fatalf("Receive #%d failed: %v", i, err)
		}
		if string(got) != string(msg) {
			t.Fatalf("msg #%d: expected %q, got %q", i, msg, got)
		}
	}
}

func TestClient_SendReceive_Combined(t *testing.T) {
	ln, addr := startEchoServer(t)
	defer ln.Close()

	c := NewClient(addr, WithClientTimeout(2*time.Second))
	defer c.Close()

	if err := c.Connect(); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	payload := []byte("sendreceive test")
	got, err := c.SendReceive(payload)
	if err != nil {
		t.Fatalf("SendReceive failed: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("expected %q, got %q", payload, got)
	}
}

func TestClient_Close_IsSafe(t *testing.T) {
	ln, addr := startEchoServer(t)
	defer ln.Close()

	c := NewClient(addr, WithClientTimeout(2*time.Second))
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Close should not error
	if err := c.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Double close should be safe
	if err := c.Close(); err != nil {
		t.Fatalf("Double Close failed: %v", err)
	}

	if c.IsConnected() {
		t.Fatal("should not be connected after Close()")
	}
}

func TestClient_Close_BeforeConnect(t *testing.T) {
	c := NewClient("127.0.0.1:0")
	// Close before connecting should not panic or error
	if err := c.Close(); err != nil {
		t.Fatalf("Close before connect failed: %v", err)
	}
}

func TestClient_Send_AfterClose_ReturnsError(t *testing.T) {
	ln, addr := startEchoServer(t)
	defer ln.Close()

	c := NewClient(addr, WithClientTimeout(2*time.Second))
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	err := c.Send([]byte("should fail"))
	if err == nil {
		t.Fatal("expected error when sending on closed client")
	}
	if !errors.Is(err, errNotConnected) {
		t.Fatalf("expected errNotConnected, got %v", err)
	}
}

func TestClient_Receive_AfterClose_ReturnsError(t *testing.T) {
	ln, addr := startEchoServer(t)
	defer ln.Close()

	c := NewClient(addr, WithClientTimeout(2*time.Second))
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	_, err := c.Receive()
	if err == nil {
		t.Fatal("expected error when receiving on closed client")
	}
	if !errors.Is(err, errNotConnected) {
		t.Fatalf("expected errNotConnected, got %v", err)
	}
}

func TestClient_Send_BeforeConnect_ReturnsError(t *testing.T) {
	c := NewClient("127.0.0.1:0")
	err := c.Send([]byte("should fail"))
	if err == nil {
		t.Fatal("expected error when sending before connect")
	}
	if !errors.Is(err, errNotConnected) {
		t.Fatalf("expected errNotConnected, got %v", err)
	}
}

func TestClient_Receive_BeforeConnect_ReturnsError(t *testing.T) {
	c := NewClient("127.0.0.1:0")
	_, err := c.Receive()
	if err == nil {
		t.Fatal("expected error when receiving before connect")
	}
	if !errors.Is(err, errNotConnected) {
		t.Fatalf("expected errNotConnected, got %v", err)
	}
}

func TestClient_Connect_UnreachableHost(t *testing.T) {
	// Use a short timeout so the test does not hang
	c := NewClient("127.0.0.1:1", WithClientTimeout(100*time.Millisecond))
	err := c.Connect()
	if err == nil {
		c.Close()
		t.Fatal("expected error when connecting to unreachable host")
	}
}

func TestClient_Connect_UnreachableHost_WithReconnect(t *testing.T) {
	// With reconnect enabled but low maxRetry and short timeout
	c := NewClient("127.0.0.1:1",
		WithClientTimeout(50*time.Millisecond),
		WithClientReconnect(true, 2),
	)
	start := time.Now()
	err := c.Connect()
	elapsed := time.Since(start)
	if err == nil {
		c.Close()
		t.Fatal("expected error when connecting to unreachable host with reconnect")
	}
	// Should have retried a couple of times, so elapsed should be > 50ms
	if elapsed < 50*time.Millisecond {
		t.Fatalf("expected retries to take some time, only took %v", elapsed)
	}
}

func TestClient_Send_AfterServerDisconnect_WithReconnect(t *testing.T) {
	// Start a server, connect, then close the server and force a write error
	// by closing the underlying connection directly.
	ln, addr := startEchoServer(t)

	c := NewClient(addr,
		WithClientTimeout(100*time.Millisecond),
		WithClientReconnect(true, 1),
	)
	defer c.Close()

	if err := c.Connect(); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Close the server so no new connections can be accepted
	ln.Close()

	// Force-close the underlying connection to guarantee a write error.
	// This triggers the reconnect path inside Send().
	c.mu.Lock()
	if c.conn != nil {
		c.conn.Close()
	}
	c.mu.Unlock()

	// Send should attempt reconnect and fail because the server is down
	err := c.Send([]byte("after disconnect"))
	if err == nil {
		t.Fatal("expected error when sending after server disconnect with reconnect")
	}
}

func TestClient_Concurrent_MultipleClients(t *testing.T) {
	ln, addr := startEchoServer(t)
	defer ln.Close()

	const clients = 5
	const messages = 20
	var wg sync.WaitGroup
	wg.Add(clients)

	for w := 0; w < clients; w++ {
		go func(id int) {
			defer wg.Done()
			c := NewClient(addr, WithClientTimeout(5*time.Second))
			defer c.Close()

			if err := c.Connect(); err != nil {
				t.Errorf("worker %d connect: %v", id, err)
				return
			}
			for i := 0; i < messages; i++ {
				msg := []byte("hello world")
				if err := c.Send(msg); err != nil {
					t.Errorf("worker %d send %d: %v", id, i, err)
					return
				}
				got, err := c.Receive()
				if err != nil {
					t.Errorf("worker %d recv %d: %v", id, i, err)
					return
				}
				if string(got) != string(msg) {
					t.Errorf("worker %d msg %d: expected %q, got %q", id, i, msg, got)
				}
			}
		}(w)
	}
	wg.Wait()
}

func TestClient_WithClientTLS_Nil(t *testing.T) {
	c := NewClient("127.0.0.1:0", WithClientTLS(nil))
	if c.tlsConfig != nil {
		t.Fatal("expected nil TLS config")
	}
}
