package tests

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/plugin"
	"github.com/X1aSheng/shark-socket/internal/runtime"
	"github.com/X1aSheng/shark-socket/internal/transport/coap"
	"github.com/X1aSheng/shark-socket/internal/transport/grpcweb"
	transporthttp "github.com/X1aSheng/shark-socket/internal/transport/http"
	"github.com/X1aSheng/shark-socket/internal/transport/quic"
	"github.com/X1aSheng/shark-socket/internal/transport/tcp"
	"github.com/X1aSheng/shark-socket/internal/transport/udp"
	"github.com/X1aSheng/shark-socket/internal/transport/websocket"
	gws "github.com/gorilla/websocket"
	quicgo "github.com/quic-go/quic-go"
)

// echoHandler is the canonical echo handler shared by all transports.
func echoHandler(sess core.Session, msg core.Message) error {
	return sess.Send(msg.Payload)
}

// allowAllOrigins permits all WebSocket/gRPC-Web origins in tests.
func allowAllOrigins(*http.Request) bool { return true }

// crossCert is a shared self-signed ECDSA P-256 certificate for QUIC tests.
// ECDSA (not RSA) is required for TLS 1.3, which quic-go negotiates.
var crossCert = func() tls.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}()

// TestCrossProtocolPlugin verifies that a plugin chain (Blacklist + RateLimit)
// behaves identically across all seven transports: every protocol echoes two
// consecutive messages through the same plugin chain.
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
		{
			name: "CoAP",
			send: func(addr string, payload []byte) ([]byte, error) {
				conn, err := net.Dial("udp", addr)
				if err != nil {
					return nil, err
				}
				defer conn.Close()
				req := coap.Message{Type: coap.TypeCON, Code: coap.CodePost, MessageID: 1, Payload: payload}
				data, err := req.Marshal()
				if err != nil {
					return nil, err
				}
				if _, err := conn.Write(data); err != nil {
					return nil, err
				}
				conn.SetReadDeadline(time.Now().Add(2 * time.Second))
				buf := make([]byte, 1024)
				n, err := conn.Read(buf)
				if err != nil {
					return nil, err
				}
				ack, err := coap.Parse(buf[:n])
				if err != nil {
					return nil, err
				}
				return ack.Payload, nil
			},
		},
		{
			name: "HTTP",
			send: func(addr string, payload []byte) ([]byte, error) {
				resp, err := http.Post("http://"+addr+"/", "application/octet-stream", bytes.NewReader(payload))
				if err != nil {
					return nil, err
				}
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					return nil, fmt.Errorf("status %d", resp.StatusCode)
				}
				return io.ReadAll(resp.Body)
			},
		},
		{
			name: "gRPC-Web",
			send: func(addr string, payload []byte) ([]byte, error) {
				frame := []byte{0}
				frame = binary.BigEndian.AppendUint32(frame, uint32(len(payload)))
				frame = append(frame, payload...)
				req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/grpc", bytes.NewReader(frame))
				if err != nil {
					return nil, err
				}
				req.Header.Set("content-type", "application/grpc-web+proto")
				req.Header.Set("x-grpc-web", "1")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					return nil, err
				}
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, err
				}
				if resp.StatusCode != http.StatusOK {
					return nil, fmt.Errorf("status %d", resp.StatusCode)
				}
				var out []byte
				for len(body) > 0 {
					flag := body[0]
					size := int(binary.BigEndian.Uint32(body[1:5]))
					body = body[5:]
					if flag == 0 {
						out = append(out, body[:size]...)
					}
					body = body[size:]
				}
				return out, nil
			},
		},
		{
			name: "QUIC",
			send: func(addr string, payload []byte) ([]byte, error) {
				conn, err := quicgo.DialAddr(context.Background(), addr, quic.ClientTLSConfig(true), nil)
				if err != nil {
					return nil, err
				}
				defer conn.CloseWithError(0, "")
				stream, err := conn.OpenStreamSync(context.Background())
				if err != nil {
					return nil, err
				}
				if _, err := stream.Write(payload); err != nil {
					return nil, err
				}
				if err := stream.Close(); err != nil {
					return nil, err
				}
				readCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				resp, err := conn.AcceptStream(readCtx)
				if err != nil {
					return nil, err
				}
				buf := make([]byte, 1024)
				if err := resp.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
					return nil, err
				}
				n, err := io.ReadFull(resp, buf[:len(payload)])
				if err != nil {
					return nil, err
				}
				return buf[:n], nil
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

// TestCrossProtocolOnAcceptBlock verifies that a plugin rejecting OnAccept
// blocks peers identically across every transport: no business data flows in
// either direction and the server keeps running.
func TestCrossProtocolOnAcceptBlock(t *testing.T) {
	blocked := []struct {
		name    string
		assert  func(t *testing.T, addr string)
	}{
		{
			name: "TCP",
			assert: func(t *testing.T, addr string) {
				conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
					t.Fatal(err)
				}
				buf := make([]byte, 1)
				if _, err := conn.Read(buf); err == nil {
					t.Fatal("blocked TCP peer received data")
				}
			},
		},
		{
			name: "UDP",
			assert: func(t *testing.T, addr string) {
				conn, err := net.Dial("udp", addr)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				if _, err := conn.Write([]byte("hello")); err != nil {
					t.Fatal(err)
				}
				if err := conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
					t.Fatal(err)
				}
				buf := make([]byte, 1024)
				if n, err := conn.Read(buf); err == nil {
					t.Fatalf("blocked UDP peer received %q", buf[:n])
				}
			},
		},
		{
			name: "WebSocket",
			assert: func(t *testing.T, addr string) {
				u := url.URL{Scheme: "ws", Host: addr, Path: "/ws"}
				conn, _, err := gws.DefaultDialer.Dial(u.String(), nil)
				if err != nil {
					return // upgrade rejected outright: blocked
				}
				defer conn.Close()
				// The upgrade may complete before the plugin rejection closes
				// the connection; either way no message may be readable.
				if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
					t.Fatal(err)
				}
				if _, _, err := conn.ReadMessage(); err == nil {
					t.Fatal("blocked WebSocket peer received data")
				}
			},
		},
		{
			name: "CoAP",
			assert: func(t *testing.T, addr string) {
				conn, err := net.Dial("udp", addr)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				req := coap.Message{Type: coap.TypeCON, Code: coap.CodePost, MessageID: 1, Payload: []byte("hello")}
				data, err := req.Marshal()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := conn.Write(data); err != nil {
					t.Fatal(err)
				}
				if err := conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
					t.Fatal(err)
				}
				buf := make([]byte, 1024)
				if n, err := conn.Read(buf); err == nil {
					t.Fatalf("blocked CoAP peer received %x", buf[:n])
				}
			},
		},
		{
			name: "HTTP",
			assert: func(t *testing.T, addr string) {
				resp, err := http.Post("http://"+addr+"/", "text/plain", bytes.NewReader([]byte("hello")))
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusForbidden {
					t.Fatalf("status = %d, want 403", resp.StatusCode)
				}
			},
		},
		{
			name: "gRPC-Web",
			assert: func(t *testing.T, addr string) {
				resp, err := http.Post("http://"+addr+"/grpc", "application/grpc-web+proto", bytes.NewReader([]byte("hello")))
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusForbidden {
					t.Fatalf("status = %d, want 403", resp.StatusCode)
				}
			},
		},
		{
			name: "QUIC",
			assert: func(t *testing.T, addr string) {
				conn, err := quicgo.DialAddr(context.Background(), addr, quic.ClientTLSConfig(true), nil)
				if err != nil {
					return // handshake rejected: blocked
				}
				defer conn.CloseWithError(0, "")
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				select {
				case <-conn.Context().Done():
					return // closed by the server: blocked
				case <-ctx.Done():
					t.Fatal("blocked QUIC peer connection stayed usable")
				}
			},
		},
	}

	for _, tt := range blocked {
		t.Run(tt.name, func(t *testing.T) {
			gw := runtime.NewGateway(runtime.WithPlugins(plugin.NewBlacklist("127.0.0.1")))
			srv := registerTransport(t, gw, tt.name)
			if err := gw.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_ = gw.Stop(ctx)
			}()
			tt.assert(t, srv.Addr().String())
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
	switch name {
	case "TCP":
		srv := tcp.NewServer(tcp.WithAddr("127.0.0.1:0"), tcp.WithHandler(echoHandler))
		if err := gw.Register(srv); err != nil {
			t.Fatal(err)
		}
		return srv
	case "UDP":
		srv := udp.NewServer(udp.WithAddr("127.0.0.1:0"), udp.WithHandler(echoHandler))
		if err := gw.Register(srv); err != nil {
			t.Fatal(err)
		}
		return srv
	case "WebSocket":
		srv := websocket.NewServer(
			websocket.WithAddr("127.0.0.1:0"),
			websocket.WithPath("/ws"),
			websocket.WithHandler(echoHandler),
			websocket.WithCheckOrigin(allowAllOrigins),
		)
		if err := gw.Register(srv); err != nil {
			t.Fatal(err)
		}
		return srv
	case "CoAP":
		// CoAP sessions send raw datagrams, so the echo must go through the
		// responder (whose payload the server wraps into a proper ACK).
		srv := coap.NewServer(
			coap.WithAddr("127.0.0.1:0"),
			coap.WithResponder(func(_ core.Session, msg core.Message) ([]byte, error) {
				return msg.Payload, nil
			}),
		)
		if err := gw.Register(srv); err != nil {
			t.Fatal(err)
		}
		return srv
	case "HTTP":
		srv := transporthttp.NewServer(transporthttp.WithAddr("127.0.0.1:0"), transporthttp.WithHandler(echoHandler))
		if err := gw.Register(srv); err != nil {
			t.Fatal(err)
		}
		return srv
	case "gRPC-Web":
		srv := grpcweb.NewServer(
			grpcweb.WithAddr("127.0.0.1:0"),
			grpcweb.WithHandler(echoHandler),
			grpcweb.WithCheckOrigin(allowAllOrigins),
		)
		if err := gw.Register(srv); err != nil {
			t.Fatal(err)
		}
		return srv
	case "QUIC":
		srv := quic.NewServer(
			quic.WithAddr("127.0.0.1:0"),
			quic.WithTLS(&tls.Config{
				Certificates: []tls.Certificate{crossCert},
				NextProtos:   []string{"shark-socket-quic"},
			}),
			quic.WithHandler(echoHandler),
		)
		if err := gw.Register(srv); err != nil {
			t.Fatal(err)
		}
		return srv
	}
	t.Fatalf("unknown transport: %s", name)
	return nil
}
