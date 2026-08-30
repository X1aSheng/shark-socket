package http

import (
	"bytes"
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
)

type prefixPlugin struct {
	core.BasePlugin
	prefix []byte
}

func (p prefixPlugin) Name() string { return "http-prefix" }

func (p prefixPlugin) OnMessage(_ core.Session, data []byte) ([]byte, error) {
	out := append([]byte(nil), p.prefix...)
	out = append(out, data...)
	return out, nil
}

func TestHTTPModeAPlainRouter(t *testing.T) {
	server := NewServer(WithAddr("127.0.0.1:0"))
	server.HandleFunc("/hello", func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		_, _ = w.Write([]byte("world"))
	})
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer stopGateway(t, gateway)

	resp, err := stdhttp.Get("http://" + server.Addr().String() + "/hello")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "world" {
		t.Fatalf("body = %q, want world", body)
	}
}

func TestHTTPCORSAllowedOrigins(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithCORSAllowedOrigins([]string{"https://console.example"}),
	)
	server.HandleFunc("/hello", func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		_, _ = w.Write([]byte("world"))
	})
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer stopGateway(t, gateway)

	req, err := stdhttp.NewRequest(stdhttp.MethodOptions, "http://"+server.Addr().String()+"/hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "https://console.example")
	resp, err := stdhttp.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != stdhttp.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, stdhttp.StatusNoContent)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://console.example" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
}

func TestHTTPModeBPluginHandlerAndCleanup(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
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

	resp, err := stdhttp.Post("http://"+server.Addr().String()+"/", "text/plain", bytes.NewReader([]byte("hello")))
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

func TestHTTPModeBBodyLimit(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithMaxBodyBytes(3),
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

	resp, err := stdhttp.Post("http://"+server.Addr().String()+"/", "text/plain", bytes.NewReader([]byte("hello")))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != stdhttp.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", resp.StatusCode, stdhttp.StatusRequestEntityTooLarge)
	}
}

func TestHTTPModeBHandlerErrorReturns500AndCleansSession(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(func(core.Session, core.Message) error {
			return errors.New("handler failed")
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

	resp, err := stdhttp.Post("http://"+server.Addr().String()+"/", "text/plain", bytes.NewReader([]byte("hello")))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != stdhttp.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", resp.StatusCode, stdhttp.StatusInternalServerError)
	}
	if count := gateway.Runtime().Sessions().Count(); count != 0 {
		t.Fatalf("session count = %d, want 0", count)
	}
}

// countPlugin tracks OnAccept/OnClose calls so tests can assert rollback
// semantics (a failed OnAccept must not produce a second transport-level
// OnClose for a session that was never fully accepted).
type countPlugin struct {
	core.BasePlugin
	acceptCalls atomic.Int32
	closeCalls  atomic.Int32
	failAccept  bool
}

func (p *countPlugin) Name() string { return "http-count" }

func (p *countPlugin) OnAccept(core.Session) error {
	p.acceptCalls.Add(1)
	if p.failAccept {
		return errors.New("rejected")
	}
	return nil
}

func (p *countPlugin) OnClose(core.Session) { p.closeCalls.Add(1) }

// TestHTTPOnAcceptFailureNoDoubleClose is the regression test for the V8 P2-4
// fix: when a plugin rejects OnAccept, the plugin chain rolls back the
// already-accepted plugin (OnClose exactly once), and the HTTP transport must
// not fire OnClose again for a session that was never fully accepted.
func TestHTTPOnAcceptFailureNoDoubleClose(t *testing.T) {
	okPlugin := &countPlugin{}
	badPlugin := &countPlugin{failAccept: true}
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	gateway := runtime.NewGateway(runtime.WithPlugins(okPlugin, badPlugin))
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer stopGateway(t, gateway)

	resp, err := stdhttp.Post("http://"+server.Addr().String()+"/", "text/plain", bytes.NewReader([]byte("hello")))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != stdhttp.StatusForbidden {
		t.Fatalf("status = %d, want %d", resp.StatusCode, stdhttp.StatusForbidden)
	}
	if badPlugin.acceptCalls.Load() != 1 {
		t.Fatalf("bad plugin accept calls = %d, want 1", badPlugin.acceptCalls.Load())
	}
	// Give any (incorrect) transport-level OnClose time to fire.
	time.Sleep(200 * time.Millisecond)
	if got := okPlugin.closeCalls.Load(); got != 1 {
		t.Fatalf("ok plugin close calls = %d, want exactly 1 (chain rollback only)", got)
	}
	if got := badPlugin.closeCalls.Load(); got != 0 {
		t.Fatalf("bad plugin close calls = %d, want 0 (never accepted)", got)
	}
	if count := gateway.Runtime().Sessions().Count(); count != 0 {
		t.Fatalf("session count = %d, want 0", count)
	}
}

// TestHTTPServerDirectStop covers the non-staged Stop path (previously 0%):
// a server started without a gateway serves traffic and Stop shuts the
// listener down.
func TestHTTPServerDirectStop(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Start(ctx); err != nil {
		t.Fatal(err)
	}

	resp, err := stdhttp.Post("http://"+server.Addr().String()+"/", "text/plain", bytes.NewReader([]byte("hello")))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello" {
		t.Fatalf("body = %q, want hello", body)
	}

	if err := server.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := stdhttp.Get("http://" + server.Addr().String() + "/"); err == nil {
		t.Fatal("server still accepting after Stop")
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
