package tests

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/plugin"
	"github.com/X1aSheng/shark-socket/internal/runtime"
	"github.com/X1aSheng/shark-socket/internal/transport/tcp"
	"github.com/X1aSheng/shark-socket/internal/transport/udp"
	"github.com/X1aSheng/shark-socket/internal/transport/websocket"
	gws "github.com/gorilla/websocket"
)

// TestCrossProtocolPlugin verifies that a plugin chain (Blacklist + RateLimit)
// behaves identically across TCP, UDP, and WebSocket transports.
func TestCrossProtocolPlugin(t *testing.T) {
	blacklist := plugin.NewBlacklist("10.0.0.0/8")
	ratelimit := plugin.NewRateLimit(1000000, time.Second)

	tests := []struct {
		name string
		send func(addr string, payload []byte) ([]byte, error)
	}{
		{
			name: "TCP",
			send: func(addr string, payload []byte) ([]byte, error) {
				c := tcp.NewClient(addr, tcp.WithClientLinger(0))
				if err := c.Connect(context.Background()); err != nil {
					return nil, err
				}
				defer c.Close()
				if err := c.Send(payload); err != nil {
					return nil, err
				}
				return c.Receive()
			},
		},
		{
			name: "UDP",
			send: func(addr string, payload []byte) ([]byte, error) {
				conn, err := net.Dial("udp", addr)
				if err != nil {
					return nil, err
				}
				defer conn.Close()
				conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
				if _, err := conn.Write(payload); err != nil {
					return nil, err
				}
				conn.SetReadDeadline(time.Now().Add(2 * time.Second))
				buf := make([]byte, 1024)
				n, err := conn.Read(buf)
				if err != nil {
					return nil, err
				}
				return buf[:n], nil
			},
		},
		{
			name: "WebSocket",
			send: func(addr string, payload []byte) ([]byte, error) {
				u := url.URL{Scheme: "ws", Host: addr, Path: "/ws"}
				conn, _, err := gws.DefaultDialer.Dial(u.String(), nil)
				if err != nil {
					return nil, err
				}
				defer conn.Close()
				if err := conn.WriteMessage(gws.BinaryMessage, payload); err != nil {
					return nil, err
				}
				conn.SetReadDeadline(time.Now().Add(2 * time.Second))
				_, got, err := conn.ReadMessage()
				return got, err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gw := runtime.NewGateway(runtime.WithPlugins(blacklist, ratelimit))
			srv := registerTransport(t, gw, tt.name)
			// Start first; listener is only created during Start().
			if err := gw.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			addr := srv.Addr().String()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_ = gw.Stop(ctx)
			}()

			payload := []byte("cross-proto-test")
			got, err := tt.send(addr, payload)
			if err != nil {
				t.Fatalf("send: %v", err)
			}
			if !bytes.Equal(got, payload) {
				t.Fatalf("echo = %q, want %q", got, payload)
			}

			// Second message also passes (RateLimit has headroom).
			got, err = tt.send(addr, payload)
			if err != nil {
				t.Fatalf("second send: %v", err)
			}
			if !bytes.Equal(got, payload) {
				t.Fatalf("second echo = %q, want %q", got, payload)
			}
		})
	}
}

// registerTransport registers a protocol-specific echo server and returns its address.
// The gateway must be started before the address is valid.
type addrProvider interface {
	Addr() net.Addr
}

// registerTransport registers a protocol-specific echo server.
// Returns the server for post-Start address retrieval.
func registerTransport(t *testing.T, gw *runtime.Gateway, name string) addrProvider {
	t.Helper()
	handler := func(sess core.Session, msg core.Message) error {
		return sess.Send(msg.Payload)
	}
	switch name {
	case "TCP":
		srv := tcp.NewServer(tcp.WithAddr("127.0.0.1:0"), tcp.WithHandler(handler))
		if err := gw.Register(srv); err != nil {
			t.Fatal(err)
		}
		return srv
	case "UDP":
		srv := udp.NewServer(udp.WithAddr("127.0.0.1:0"), udp.WithHandler(handler))
		if err := gw.Register(srv); err != nil {
			t.Fatal(err)
		}
		return srv
	case "WebSocket":
		srv := websocket.NewServer(
			websocket.WithAddr("127.0.0.1:0"),
			websocket.WithPath("/ws"),
			websocket.WithHandler(handler),
			websocket.WithCheckOrigin(func(*http.Request) bool { return true }),
		)
		if err := gw.Register(srv); err != nil {
			t.Fatal(err)
		}
		return srv
	}
	t.Fatalf("unknown transport: %s", name)
	return nil
}
