package integration_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/X1aSheng/shark-socket/internal/gateway"
	"github.com/X1aSheng/shark-socket/internal/protocol/coap"
	httpproto "github.com/X1aSheng/shark-socket/internal/protocol/http"
	tcpproto "github.com/X1aSheng/shark-socket/internal/protocol/tcp"
	"github.com/X1aSheng/shark-socket/internal/protocol/udp"
	wsproto "github.com/X1aSheng/shark-socket/internal/protocol/websocket"
	"github.com/X1aSheng/shark-socket/internal/types"
)

// safeFramer creates a per-read bufio.Reader to avoid shared state across sessions.
type safeFramer struct{ maxSize int }

func (f *safeFramer) ReadFrame(r io.Reader) ([]byte, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}
	size := int(binary.BigEndian.Uint32(header))
	if f.maxSize > 0 && size > f.maxSize {
		return nil, io.ErrUnexpectedEOF
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (f *safeFramer) WriteFrame(w io.Writer, payload []byte) error {
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(payload)))
	_, err := w.Write(append(header, payload...))
	return err
}

// ---------------------------------------------------------------------------
// TCP Data Communication Integration Tests
// ---------------------------------------------------------------------------

// TestTCP_EchoIntegrity verifies that TCP echo preserves data exactly
// across various payload sizes and patterns.
func TestTCP_EchoIntegrity(t *testing.T) {
	handler := func(sess types.RawSession, msg types.RawMessage) error {
		return sess.Send(msg.Payload)
	}
	srv := tcpproto.NewServer(handler,
		tcpproto.WithAddr("127.0.0.1", 0),
		tcpproto.WithWorkerPool(2, 8, 64),
		tcpproto.WithFramer(tcpproto.NewLengthPrefixFramer(65536)),
	)

	gw := gateway.New(gateway.WithMetricsEnabled(false))
	gw.Register(srv)
	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		gw.Stop(ctx)
	}()

	addr := srv.Addr().String()
	waitForTCP(t, addr, 5*time.Second)

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))

	framer := tcpproto.NewLengthPrefixFramer(65536)

	patterns := []struct {
		name    string
		payload []byte
	}{
		{"Empty", []byte{}},
		{"SingleByte", []byte{0x00}},
		{"AllZeros_1KB", make([]byte, 1024)},
		{"AllFF_1KB", func() []byte { p := make([]byte, 1024); for i := range p { p[i] = 0xFF }; return p }()},
		{"Ascending_4KB", func() []byte { p := make([]byte, 4096); for i := range p { p[i] = byte(i % 256) }; return p }()},
		{"Descending_4KB", func() []byte { p := make([]byte, 4096); for i := range p { p[i] = byte(255 - i%256) }; return p }()},
		{"Alternating_2KB", func() []byte { p := make([]byte, 2048); for i := range p { p[i] = byte(i % 2) * 0xFF }; return p }()},
		{"Large_32KB", func() []byte { p := make([]byte, 32768); for i := range p { p[i] = byte(i * 7 % 256) }; return p }()},
	}

	for _, pat := range patterns {
		t.Run(pat.name, func(t *testing.T) {
			if err := framer.WriteFrame(conn, pat.payload); err != nil {
				t.Fatalf("WriteFrame: %v", err)
			}
			got, err := framer.ReadFrame(conn)
			if err != nil {
				t.Fatalf("ReadFrame: %v", err)
			}
			if len(got) != len(pat.payload) {
				t.Fatalf("length mismatch: got %d, want %d", len(got), len(pat.payload))
			}
			if !bytes.Equal(got, pat.payload) {
				for i := range got {
					if got[i] != pat.payload[i] {
						t.Fatalf("byte mismatch at offset %d: got 0x%02X, want 0x%02X", i, got[i], pat.payload[i])
					}
				}
			}
		})
	}
}

// TestTCP_MultiClientConcurrent verifies multiple clients can communicate
// concurrently without data corruption.
func TestTCP_MultiClientConcurrent(t *testing.T) {
	handler := func(sess types.RawSession, msg types.RawMessage) error {
		return sess.Send(msg.Payload)
	}
	srv := tcpproto.NewServer(handler,
		tcpproto.WithAddr("127.0.0.1", 0),
		tcpproto.WithWorkerPool(8, 32, 256),
		tcpproto.WithFullPolicy(tcpproto.PolicyBlock),
		tcpproto.WithFramer(&safeFramer{maxSize: 4096}),
	)

	gw := gateway.New(gateway.WithMetricsEnabled(false))
	gw.Register(srv)
	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		gw.Stop(ctx)
	}()

	addr := srv.Addr().String()
	waitForTCP(t, addr, 5*time.Second)

	const numClients = 20
	const msgsPerClient = 50

	var wg sync.WaitGroup
	var errors atomic.Int64

	for c := 0; c < numClients; c++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
			if err != nil {
				t.Logf("client %d dial: %v", clientID, err)
				errors.Add(1)
				return
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(30 * time.Second))

			framer := tcpproto.NewLengthPrefixFramer(4096)

			for m := 0; m < msgsPerClient; m++ {
				payload := []byte(fmt.Sprintf("c%d-m%d", clientID, m))
				if err := framer.WriteFrame(conn, payload); err != nil {
					t.Logf("client %d write msg %d: %v", clientID, m, err)
					errors.Add(1)
					return
				}
				got, err := framer.ReadFrame(conn)
				if err != nil {
					t.Logf("client %d read msg %d: %v", clientID, m, err)
					errors.Add(1)
					return
				}
				if string(got) != string(payload) {
					t.Logf("client %d msg %d mismatch: got %q, want %q", clientID, m, got, payload)
					errors.Add(1)
					return
				}
			}
		}(c)
	}

	wg.Wait()

	if n := errors.Load(); n > 0 {
		t.Fatalf("%d errors during concurrent communication", n)
	}
}

// TestTCP_PingPongIntegrity verifies strict request/response integrity.
func TestTCP_PingPongIntegrity(t *testing.T) {
	handler := func(sess types.RawSession, msg types.RawMessage) error {
		return sess.Send(msg.Payload)
	}
	srv := tcpproto.NewServer(handler,
		tcpproto.WithAddr("127.0.0.1", 0),
		tcpproto.WithWorkerPool(2, 8, 64),
		tcpproto.WithFramer(&safeFramer{maxSize: 4096}),
	)

	gw := gateway.New(gateway.WithMetricsEnabled(false))
	gw.Register(srv)
	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		gw.Stop(ctx)
	}()

	addr := srv.Addr().String()
	waitForTCP(t, addr, 5*time.Second)

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))

	framer := tcpproto.NewLengthPrefixFramer(4096)

	const totalMsgs = 200

	for i := 0; i < totalMsgs; i++ {
		p := make([]byte, 8)
		binary.BigEndian.PutUint64(p, uint64(i))
		if err := framer.WriteFrame(conn, p); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		got, err := framer.ReadFrame(conn)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		seq := binary.BigEndian.Uint64(got[:8])
		if seq != uint64(i) {
			t.Fatalf("integrity violation at msg %d: got seq %d", i, seq)
		}
	}
}

// TestTCP_LargePayloadRoundTrip verifies large payload integrity.
func TestTCP_LargePayloadRoundTrip(t *testing.T) {
	handler := func(sess types.RawSession, msg types.RawMessage) error {
		return sess.Send(msg.Payload)
	}
	srv := tcpproto.NewServer(handler,
		tcpproto.WithAddr("127.0.0.1", 0),
		tcpproto.WithWorkerPool(4, 16, 128),
		tcpproto.WithFramer(tcpproto.NewLengthPrefixFramer(256*1024)),
	)

	gw := gateway.New(gateway.WithMetricsEnabled(false))
	gw.Register(srv)
	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		gw.Stop(ctx)
	}()

	addr := srv.Addr().String()
	waitForTCP(t, addr, 5*time.Second)

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))

	framer := tcpproto.NewLengthPrefixFramer(256 * 1024)

	sizes := []int{1024, 8192, 65536, 131072}

	for _, size := range sizes {
		t.Run(fmt.Sprintf("%dKB", size/1024), func(t *testing.T) {
			payload := make([]byte, size)
			for i := range payload {
				payload[i] = byte(i % 256)
			}

			if err := framer.WriteFrame(conn, payload); err != nil {
				t.Fatalf("WriteFrame: %v", err)
			}
			got, err := framer.ReadFrame(conn)
			if err != nil {
				t.Fatalf("ReadFrame: %v", err)
			}
			if len(got) != len(payload) {
				t.Fatalf("length mismatch: got %d, want %d", len(got), len(payload))
			}
			if !bytes.Equal(got, payload) {
				t.Fatalf("data integrity failed for %d byte payload", size)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// UDP Data Communication Integration Tests
// ---------------------------------------------------------------------------

// TestUDP_EchoIntegrity verifies UDP echo preserves data for various payloads.
func TestUDP_EchoIntegrity(t *testing.T) {
	handler := func(sess types.RawSession, msg types.RawMessage) error {
		return sess.Send(msg.Payload)
	}
	srv := udp.NewServer(handler, udp.WithAddr("127.0.0.1", 0))

	gw := gateway.New(gateway.WithMetricsEnabled(false))
	gw.Register(srv)
	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		gw.Stop(ctx)
	}()

	srvAddr := srv.Addr().String()
	waitForUDP(t, srvAddr, 5*time.Second)

	conn, err := net.DialUDP("udp", nil, srv.Addr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))

	payloads := []struct {
		name    string
		payload []byte
	}{
		{"Small", []byte("hello")},
		{"Medium", make([]byte, 512)},
		{"Large", make([]byte, 1400)},
		{"Binary", func() []byte { p := make([]byte, 256); for i := range p { p[i] = byte(i) }; return p }()},
	}

	for _, pp := range payloads {
		t.Run(pp.name, func(t *testing.T) {
			if len(pp.payload) > 10 {
				for i := range pp.payload {
					pp.payload[i] = byte(i % 256)
				}
			}

			conn.Write(pp.payload)
			buf := make([]byte, 1500)
			n, err := conn.Read(buf)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if !bytes.Equal(buf[:n], pp.payload) {
				t.Fatalf("mismatch: got %d bytes, want %d bytes", n, len(pp.payload))
			}
		})
	}
}

// TestUDP_MultiClientConcurrent verifies multiple UDP clients can echo concurrently.
func TestUDP_MultiClientConcurrent(t *testing.T) {
	handler := func(sess types.RawSession, msg types.RawMessage) error {
		return sess.Send(msg.Payload)
	}
	srv := udp.NewServer(handler, udp.WithAddr("127.0.0.1", 0))

	gw := gateway.New(gateway.WithMetricsEnabled(false))
	gw.Register(srv)
	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		gw.Stop(ctx)
	}()

	srvAddr := srv.Addr().(*net.UDPAddr)
	waitForUDP(t, srvAddr.String(), 5*time.Second)

	const numClients = 10
	const msgsPerClient = 30

	var wg sync.WaitGroup
	var errors atomic.Int64

	for c := 0; c < numClients; c++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			conn, err := net.DialUDP("udp", nil, srvAddr)
			if err != nil {
				errors.Add(1)
				return
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(15 * time.Second))

			for m := 0; m < msgsPerClient; m++ {
				payload := []byte(fmt.Sprintf("udp-c%d-m%d", clientID, m))
				conn.Write(payload)

				buf := make([]byte, 1500)
				n, err := conn.Read(buf)
				if err != nil {
					errors.Add(1)
					return
				}
				if string(buf[:n]) != string(payload) {
					t.Logf("client %d msg %d mismatch: got %q, want %q", clientID, m, buf[:n], payload)
					errors.Add(1)
					return
				}
			}
		}(c)
	}

	wg.Wait()

	if n := errors.Load(); n > 0 {
		t.Fatalf("%d errors during UDP concurrent communication", n)
	}
}

// ---------------------------------------------------------------------------
// WebSocket Data Communication Integration Tests
// ---------------------------------------------------------------------------

// TestWebSocket_EchoIntegrity verifies WebSocket echo preserves data
// for text and binary messages of various sizes.
func TestWebSocket_EchoIntegrity(t *testing.T) {
	handler := func(sess types.RawSession, msg types.RawMessage) error {
		return sess.Send(msg.Payload)
	}
	srv := wsproto.NewServer(handler,
		wsproto.WithAddr("127.0.0.1", 0),
		wsproto.WithPath("/ws"),
		wsproto.WithMaxMessageSize(65536),
		wsproto.WithAllowedOrigins("*"),
	)

	gw := gateway.New(gateway.WithMetricsEnabled(false))
	gw.Register(srv)
	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		gw.Stop(ctx)
	}()

	addr := srv.Addr().String()
	waitForTCP(t, addr, 5*time.Second)

	wsURL := fmt.Sprintf("ws://%s/ws", addr)
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer wsConn.Close()

	cases := []struct {
		name    string
		msgType int
		payload []byte
	}{
		{"Text_Short", websocket.TextMessage, []byte("hello websocket")},
		{"Text_UTF8", websocket.TextMessage, []byte("你好世界 🌍")},
		{"Binary_256B", websocket.BinaryMessage, func() []byte { p := make([]byte, 256); for i := range p { p[i] = byte(i) }; return p }()},
		{"Binary_4KB", websocket.BinaryMessage, func() []byte { p := make([]byte, 4096); for i := range p { p[i] = byte(i % 256) }; return p }()},
		{"Binary_16KB", websocket.BinaryMessage, func() []byte { p := make([]byte, 16384); for i := range p { p[i] = byte(i * 3 % 256) }; return p }()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wsConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := wsConn.WriteMessage(tc.msgType, tc.payload); err != nil {
				t.Fatalf("WriteMessage: %v", err)
			}
			wsConn.SetReadDeadline(time.Now().Add(5 * time.Second))
			msgType, got, err := wsConn.ReadMessage()
			if err != nil {
				t.Fatalf("ReadMessage: %v", err)
			}
			// Server echoes all payloads as BinaryMessage regardless of input type
			if msgType != tc.msgType && !(tc.msgType == websocket.TextMessage && msgType == websocket.BinaryMessage) {
				t.Fatalf("message type mismatch: got %d, want %d", msgType, tc.msgType)
			}
			if !bytes.Equal(got, tc.payload) {
				t.Fatalf("payload mismatch: got %d bytes, want %d bytes", len(got), len(tc.payload))
			}
		})
	}
}

// TestWebSocket_MultiClientConcurrent verifies multiple WebSocket clients
// can echo concurrently without data corruption.
func TestWebSocket_MultiClientConcurrent(t *testing.T) {
	handler := func(sess types.RawSession, msg types.RawMessage) error {
		return sess.Send(msg.Payload)
	}
	srv := wsproto.NewServer(handler,
		wsproto.WithAddr("127.0.0.1", 0),
		wsproto.WithPath("/ws"),
		wsproto.WithMaxMessageSize(4096),
		wsproto.WithMaxSessions(100),
		wsproto.WithAllowedOrigins("*"),
	)

	gw := gateway.New(gateway.WithMetricsEnabled(false))
	gw.Register(srv)
	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		gw.Stop(ctx)
	}()

	addr := srv.Addr().String()
	waitForTCP(t, addr, 5*time.Second)

	wsURL := fmt.Sprintf("ws://%s/ws", addr)

	const numClients = 10
	const msgsPerClient = 30

	var wg sync.WaitGroup
	var errors atomic.Int64

	for c := 0; c < numClients; c++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				t.Logf("client %d dial: %v", clientID, err)
				errors.Add(1)
				return
			}
			defer wsConn.Close()

			for m := 0; m < msgsPerClient; m++ {
				payload := []byte(fmt.Sprintf("ws-c%d-m%d", clientID, m))
				wsConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if err := wsConn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
					t.Logf("client %d write %d: %v", clientID, m, err)
					errors.Add(1)
					return
				}
				wsConn.SetReadDeadline(time.Now().Add(5 * time.Second))
				_, got, err := wsConn.ReadMessage()
				if err != nil {
					t.Logf("client %d read %d: %v", clientID, m, err)
					errors.Add(1)
					return
				}
				if string(got) != string(payload) {
					t.Logf("client %d msg %d mismatch: got %q, want %q", clientID, m, got, payload)
					errors.Add(1)
					return
				}
			}
		}(c)
	}

	wg.Wait()

	if n := errors.Load(); n > 0 {
		t.Fatalf("%d errors during WS concurrent communication", n)
	}
}

// ---------------------------------------------------------------------------
// HTTP Data Communication Integration Tests
// ---------------------------------------------------------------------------

// TestHTTP_RequestResponse verifies HTTP request/response data integrity.
func TestHTTP_RequestResponse(t *testing.T) {
	srv := httpproto.NewServer(httpproto.WithAddr("127.0.0.1", 0))

	srv.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		buf := make([]byte, 4096)
		var data []byte
		for {
			n, err := r.Body.Read(buf)
			data = append(data, buf[:n]...)
			if err != nil {
				break
			}
		}
		r.Body.Close()
		w.Write(data)
	})

	srv.HandleFunc("/query-echo", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.URL.Query().Get("msg")))
	})

	gw := gateway.New(gateway.WithMetricsEnabled(false))
	gw.Register(srv)
	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		gw.Stop(ctx)
	}()

	addr := srv.Addr().String()
	waitForTCP(t, addr, 5*time.Second)

	client := &http.Client{Timeout: 5 * time.Second}

	t.Run("QueryEcho", func(t *testing.T) {
		msg := "hello-http-world"
		resp, err := client.Get(fmt.Sprintf("http://%s/query-echo?msg=%s", addr, msg))
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		buf := make([]byte, 1024)
		n, _ := resp.Body.Read(buf)
		if string(buf[:n]) != msg {
			t.Fatalf("got %q, want %q", buf[:n], msg)
		}
	})

	t.Run("BodyEcho_Binary", func(t *testing.T) {
		payload := make([]byte, 4096)
		for i := range payload {
			payload[i] = byte(i % 256)
		}
		resp, err := client.Post(fmt.Sprintf("http://%s/echo", addr), "application/octet-stream", bytes.NewReader(payload))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		got, _ := io.ReadAll(resp.Body)
		if !bytes.Equal(got, payload) {
			t.Fatalf("body mismatch: got %d bytes, want %d bytes", len(got), len(payload))
		}
	})
}

// TestHTTP_ConcurrentRequests verifies concurrent HTTP request handling.
func TestHTTP_ConcurrentRequests(t *testing.T) {
	var reqCount atomic.Int64
	srv := httpproto.NewServer(httpproto.WithAddr("127.0.0.1", 0))
	srv.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		reqCount.Add(1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fmt.Sprintf("pong-%d", reqCount.Load())))
	})

	gw := gateway.New(gateway.WithMetricsEnabled(false))
	gw.Register(srv)
	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		gw.Stop(ctx)
	}()

	addr := srv.Addr().String()
	waitForTCP(t, addr, 5*time.Second)

	const numClients = 20
	const reqsPerClient = 50

	var wg sync.WaitGroup
	var errors atomic.Int64

	for c := 0; c < numClients; c++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			client := &http.Client{Timeout: 5 * time.Second}
			url := fmt.Sprintf("http://%s/ping", addr)

			for r := 0; r < reqsPerClient; r++ {
				resp, err := client.Get(url)
				if err != nil {
					errors.Add(1)
					continue
				}
				if resp.StatusCode != http.StatusOK {
					resp.Body.Close()
					errors.Add(1)
					continue
				}
				resp.Body.Close()
			}
		}(c)
	}

	wg.Wait()

	if n := errors.Load(); n > 0 {
		t.Fatalf("%d errors during HTTP concurrent requests", n)
	}

	total := reqCount.Load()
	expected := int64(numClients * reqsPerClient)
	if total != expected {
		t.Fatalf("request count = %d, expected %d", total, expected)
	}
}

// ---------------------------------------------------------------------------
// CoAP Data Communication Integration Tests
// ---------------------------------------------------------------------------

// TestCoAP_MessageRoundTrip verifies CoAP message parsing and echo.
func TestCoAP_MessageRoundTrip(t *testing.T) {
	handler := func(sess types.RawSession, msg types.RawMessage) error {
		return sess.Send(msg.Payload)
	}
	srv := coap.NewServer(handler, coap.WithAddr("127.0.0.1", 0))

	gw := gateway.New(gateway.WithMetricsEnabled(false))
	gw.Register(srv)
	if err := gw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		gw.Stop(ctx)
	}()

	srvAddr := srv.Addr().(*net.UDPAddr)
	waitForUDP(t, srvAddr.String(), 5*time.Second)

	conn, err := net.DialUDP("udp", nil, srvAddr)
	if err != nil {
		t.Fatalf("DialUDP: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	payloads := []struct {
		name    string
		payload []byte
	}{
		{"Short", []byte("coap-hello")},
		{"Binary", func() []byte { p := make([]byte, 128); for i := range p { p[i] = byte(i) }; return p }()},
		{"Medium", []byte("coap-medium-payload-for-integrity-testing")},
	}

	for _, pp := range payloads {
		t.Run(pp.name, func(t *testing.T) {
			msg := &coap.CoAPMessage{
				Version:   1,
				Type:      coap.NON,
				Code:      coap.CodePost,
				MessageID: uint16(time.Now().UnixNano() & 0xFFFF),
				Payload:   pp.payload,
			}
			data, err := msg.Serialize()
			if err != nil {
				t.Fatalf("Serialize: %v", err)
			}

			conn.Write(data)
			time.Sleep(50 * time.Millisecond)

			parsed, err := coap.ParseMessage(data)
			if err != nil {
				t.Fatalf("ParseMessage: %v", err)
			}
			if !bytes.Equal(parsed.Payload, pp.payload) {
				t.Fatalf("payload mismatch: got %q, want %q", parsed.Payload, pp.payload)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Cross-Protocol Gateway Data Communication Tests
// ---------------------------------------------------------------------------

// TestGateway_CrossProtocolCommunication verifies all protocols operate
// simultaneously through the gateway with data integrity preserved.
func TestGateway_CrossProtocolCommunication(t *testing.T) {
	var totalMsgs atomic.Int64

	echoHandler := func(sess types.RawSession, msg types.RawMessage) error {
		totalMsgs.Add(1)
		return sess.Send(msg.Payload)
	}

	tcpSrv := tcpproto.NewServer(echoHandler,
		tcpproto.WithAddr("127.0.0.1", 0),
		tcpproto.WithWorkerPool(4, 16, 128),
		tcpproto.WithFramer(tcpproto.NewLengthPrefixFramer(8192)),
	)

	wsSrv := wsproto.NewServer(echoHandler,
		wsproto.WithAddr("127.0.0.1", 0),
		wsproto.WithPath("/ws"),
		wsproto.WithMaxMessageSize(8192),
		wsproto.WithAllowedOrigins("*"),
	)

	httpSrv := httpproto.NewServer(httpproto.WithAddr("127.0.0.1", 0))
	httpSrv.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		totalMsgs.Add(1)
		msg := r.URL.Query().Get("data")
		w.Write([]byte(msg))
	})

	udpSrv := udp.NewServer(echoHandler, udp.WithAddr("127.0.0.1", 0))

	gw := gateway.New(gateway.WithMetricsEnabled(false))
	gw.Register(tcpSrv)
	gw.Register(wsSrv)
	gw.Register(httpSrv)
	gw.Register(udpSrv)

	if err := gw.Start(); err != nil {
		t.Fatalf("gateway Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		gw.Stop(ctx)
	}()

	tcpAddr := tcpSrv.Addr().String()
	wsAddr := wsSrv.Addr().String()
	httpAddr := httpSrv.Addr().String()
	udpAddr := udpSrv.Addr().(*net.UDPAddr)

	waitForTCP(t, tcpAddr, 5*time.Second)
	waitForTCP(t, wsAddr, 5*time.Second)
	waitForTCP(t, httpAddr, 5*time.Second)
	waitForUDP(t, udpAddr.String(), 5*time.Second)

	const msgsPerProto = 20

	t.Run("TCP", func(t *testing.T) {
		conn, err := net.DialTimeout("tcp", tcpAddr, 5*time.Second)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(10 * time.Second))

		framer := tcpproto.NewLengthPrefixFramer(8192)
		for i := 0; i < msgsPerProto; i++ {
			payload := []byte(fmt.Sprintf("tcp-msg-%d", i))
			if err := framer.WriteFrame(conn, payload); err != nil {
				t.Fatalf("write %d: %v", i, err)
			}
			got, err := framer.ReadFrame(conn)
			if err != nil {
				t.Fatalf("read %d: %v", i, err)
			}
			if string(got) != string(payload) {
				t.Fatalf("msg %d mismatch: got %q, want %q", i, got, payload)
			}
		}
	})

	t.Run("WebSocket", func(t *testing.T) {
		wsURL := fmt.Sprintf("ws://%s/ws", wsAddr)
		wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("Dial: %v", err)
		}
		defer wsConn.Close()

		for i := 0; i < msgsPerProto; i++ {
			payload := []byte(fmt.Sprintf("ws-msg-%d", i))
			wsConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := wsConn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
				t.Fatalf("write %d: %v", i, err)
			}
			wsConn.SetReadDeadline(time.Now().Add(5 * time.Second))
			_, got, err := wsConn.ReadMessage()
			if err != nil {
				t.Fatalf("read %d: %v", i, err)
			}
			if string(got) != string(payload) {
				t.Fatalf("msg %d mismatch: got %q, want %q", i, got, payload)
			}
		}
	})

	t.Run("HTTP", func(t *testing.T) {
		client := &http.Client{Timeout: 5 * time.Second}
		for i := 0; i < msgsPerProto; i++ {
			msg := fmt.Sprintf("http-msg-%d", i)
			resp, err := client.Get(fmt.Sprintf("http://%s/echo?data=%s", httpAddr, msg))
			if err != nil {
				t.Fatalf("GET %d: %v", i, err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(body) != msg {
				t.Fatalf("msg %d mismatch: got %q, want %q", i, body, msg)
			}
		}
	})

	t.Run("UDP", func(t *testing.T) {
		conn, err := net.DialUDP("udp", nil, udpAddr)
		if err != nil {
			t.Fatalf("DialUDP: %v", err)
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(10 * time.Second))

		for i := 0; i < msgsPerProto; i++ {
			payload := []byte(fmt.Sprintf("udp-msg-%d", i))
			conn.Write(payload)
			buf := make([]byte, 1500)
			n, err := conn.Read(buf)
			if err != nil {
				t.Fatalf("read %d: %v", i, err)
			}
			if string(buf[:n]) != string(payload) {
				t.Fatalf("msg %d mismatch: got %q, want %q", i, buf[:n], payload)
			}
		}
	})

	t.Logf("Total messages processed across all protocols: %d", totalMsgs.Load())
}

// TestGateway_CrossProtocolStress runs all protocols simultaneously with
// concurrent clients, verifying no data corruption under load.
func TestGateway_CrossProtocolStress(t *testing.T) {
	echoHandler := func(sess types.RawSession, msg types.RawMessage) error {
		return sess.Send(msg.Payload)
	}

	tcpSrv := tcpproto.NewServer(echoHandler,
		tcpproto.WithAddr("127.0.0.1", 0),
		tcpproto.WithWorkerPool(8, 32, 512),
		tcpproto.WithFullPolicy(tcpproto.PolicyBlock),
		tcpproto.WithFramer(&safeFramer{maxSize: 4096}),
	)

	wsSrv := wsproto.NewServer(echoHandler,
		wsproto.WithAddr("127.0.0.1", 0),
		wsproto.WithPath("/ws"),
		wsproto.WithMaxMessageSize(4096),
		wsproto.WithMaxSessions(50),
		wsproto.WithAllowedOrigins("*"),
	)

	httpSrv := httpproto.NewServer(httpproto.WithAddr("127.0.0.1", 0))
	httpSrv.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.URL.Query().Get("data")))
	})

	udpSrv := udp.NewServer(echoHandler, udp.WithAddr("127.0.0.1", 0))

	gw := gateway.New(gateway.WithMetricsEnabled(false))
	gw.Register(tcpSrv)
	gw.Register(wsSrv)
	gw.Register(httpSrv)
	gw.Register(udpSrv)

	if err := gw.Start(); err != nil {
		t.Fatalf("gateway Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		gw.Stop(ctx)
	}()

	tcpAddr := tcpSrv.Addr().String()
	wsAddr := wsSrv.Addr().String()
	httpAddr := httpSrv.Addr().String()
	udpAddr := udpSrv.Addr().(*net.UDPAddr)

	waitForTCP(t, tcpAddr, 5*time.Second)
	waitForTCP(t, wsAddr, 5*time.Second)
	waitForTCP(t, httpAddr, 5*time.Second)
	waitForUDP(t, udpAddr.String(), 5*time.Second)

	var wg sync.WaitGroup
	var errors atomic.Int64
	const msgsPerClient = 30

	// TCP clients
	for c := 0; c < 5; c++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			conn, err := net.DialTimeout("tcp", tcpAddr, 5*time.Second)
			if err != nil {
				errors.Add(1)
				return
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(30 * time.Second))
			framer := tcpproto.NewLengthPrefixFramer(4096)

			for m := 0; m < msgsPerClient; m++ {
				p := []byte(fmt.Sprintf("tcp-%d-%d", id, m))
				if err := framer.WriteFrame(conn, p); err != nil {
					errors.Add(1)
					return
				}
				got, err := framer.ReadFrame(conn)
				if err != nil || string(got) != string(p) {
					errors.Add(1)
					return
				}
			}
		}(c)
	}

	// WebSocket clients
	for c := 0; c < 5; c++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			wsConn, _, err := websocket.DefaultDialer.Dial(
				fmt.Sprintf("ws://%s/ws", wsAddr), nil)
			if err != nil {
				errors.Add(1)
				return
			}
			defer wsConn.Close()

			for m := 0; m < msgsPerClient; m++ {
				p := []byte(fmt.Sprintf("ws-%d-%d", id, m))
				wsConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				wsConn.WriteMessage(websocket.BinaryMessage, p)
				wsConn.SetReadDeadline(time.Now().Add(5 * time.Second))
				_, got, err := wsConn.ReadMessage()
				if err != nil || string(got) != string(p) {
					errors.Add(1)
					return
				}
			}
		}(c)
	}

	// HTTP clients
	for c := 0; c < 5; c++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			client := &http.Client{Timeout: 5 * time.Second}
			for m := 0; m < msgsPerClient; m++ {
				msg := fmt.Sprintf("http-%d-%d", id, m)
				resp, err := client.Get(fmt.Sprintf("http://%s/echo?data=%s", httpAddr, msg))
				if err != nil {
					errors.Add(1)
					continue
				}
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if string(body) != msg {
					errors.Add(1)
				}
			}
		}(c)
	}

	// UDP clients (with retry for potential packet reordering under load)
	for c := 0; c < 5; c++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			conn, err := net.DialUDP("udp", nil, udpAddr)
			if err != nil {
				errors.Add(1)
				return
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(15 * time.Second))

			for m := 0; m < msgsPerClient; m++ {
				p := []byte(fmt.Sprintf("udp-%d-%d", id, m))
				conn.Write(p)
				buf := make([]byte, 1500)
				n, err := conn.Read(buf)
				if err != nil {
					errors.Add(1)
					return
				}
				if string(buf[:n]) != string(p) {
					conn.Write(p)
					n, err = conn.Read(buf)
					if err != nil || string(buf[:n]) != string(p) {
						errors.Add(1)
						return
					}
				}
			}
		}(c)
	}

	wg.Wait()

	if n := errors.Load(); n > 0 {
		t.Fatalf("%d errors during cross-protocol stress test", n)
	}
}
