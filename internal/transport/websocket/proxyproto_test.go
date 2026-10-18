package websocket

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
	"github.com/X1aSheng/shark-socket/internal/transport/shared"

	gws "github.com/gorilla/websocket"
)

// TestWebSocketProxyProtocolReportsRealClient proves the PROXY listener also
// works under net/http, which is where it is easiest to get wrong: Serve owns
// the accept loop and builds the request from the conn it was given, so the
// only way the header can reach the handler is if Accept hands back a conn
// whose RemoteAddr is already rewritten and whose remaining bytes are intact.
//
// The header is sent by a custom dialer, which is exactly what a balancer does:
// write the line, then let the client speak HTTP on the same socket.
func TestWebSocketProxyProtocolReportsRealClient(t *testing.T) {
	gotRemote := make(chan string, 1)
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithPath("/ws"),
		WithProxyProtocol(shared.ProxyProtocolV1),
		// CheckOrigin runs before the upgrade and receives the request, so it
		// is the least invasive place to observe RemoteAddr.
		WithCheckOrigin(func(r *http.Request) bool {
			select {
			case gotRemote <- r.RemoteAddr:
			default:
			}
			return true
		}),
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
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = gateway.Stop(shutdownCtx)
	}()

	dialer := gws.Dialer{
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := dialWithLinger(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			if _, err := conn.Write([]byte("PROXY TCP4 203.0.113.9 198.51.100.1 5555 443\r\n")); err != nil {
				_ = conn.Close()
				return nil, err
			}
			return conn, nil
		},
	}
	u := url.URL{Scheme: "ws", Host: server.Addr().String(), Path: "/ws"}
	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("handshake after PROXY header: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(gws.BinaryMessage, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, got, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("echo = %q, want hello", got)
	}

	select {
	case remote := <-gotRemote:
		if remote != "203.0.113.9:5555" {
			t.Fatalf("request RemoteAddr = %q, want 203.0.113.9:5555", remote)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CheckOrigin was never called")
	}
}
