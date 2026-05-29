package grpcweb

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket-new/internal/core"
	"github.com/X1aSheng/shark-socket-new/internal/runtime"
)

type prefixPlugin struct {
	core.BasePlugin
	prefix []byte
}

func (p prefixPlugin) Name() string { return "grpcweb-prefix" }

func (p prefixPlugin) OnMessage(_ core.Session, data []byte) ([]byte, error) {
	out := append([]byte(nil), p.prefix...)
	out = append(out, data...)
	return out, nil
}

func TestGRPCWebDirectEchoAndCleanup(t *testing.T) {
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

	resp, err := http.Post("http://"+server.Addr().String()+"/grpc", "application/grpc-web+proto", bytes.NewReader([]byte("hello")))
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

func TestGRPCWebMaxMessageSize(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithMaxMessageBytes(3),
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

	resp, err := http.Post("http://"+server.Addr().String()+"/grpc", "application/grpc-web+proto", bytes.NewReader([]byte("hello")))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusRequestEntityTooLarge)
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
