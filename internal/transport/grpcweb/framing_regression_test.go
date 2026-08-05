package grpcweb

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
)

// TestRawBodyNotMisdetectedAsFramed verifies that a raw request body whose
// first byte is 0x00 followed by a plausible 4-byte length is NOT treated as
// a gRPC-Web frame when the request does not declare the gRPC-Web content
// type. The response must be raw (no frame header, no trailer).
func TestRawBodyNotMisdetectedAsFramed(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
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

	// Raw protobuf-ish body that parses as a gRPC-Web data frame if misdetected.
	raw := []byte{0x00, 0x00, 0x00, 0x00, 0x04, 0xde, 0xad, 0xbe, 0xef}
	req, err := http.NewRequest(http.MethodPost, "http://"+server.Addr().String()+"/grpc", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("content-type", "application/protobuf")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, raw) {
		t.Fatalf("raw client response = %v, want raw passthrough %v (misdetected as gRPC-Web frame)", body, raw)
	}
}

// TestGRPCWebRequestGetsFramedResponse verifies a request that declares the
// gRPC-Web content type still receives a framed response with trailers.
func TestGRPCWebRequestGetsFramedResponse(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send([]byte("world"))
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

	frame := testGRPCWebDataFrame([]byte("hello"))
	req, err := http.NewRequest(http.MethodPost, "http://"+server.Addr().String()+"/grpc", bytes.NewReader(frame))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("content-type", "application/grpc-web+proto")
	req.Header.Set("x-grpc-web", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	payload, trailers := readTestGRPCWebResponse(t, body)
	if string(payload) != "world" {
		t.Fatalf("payload = %q, want world", payload)
	}
	if !bytes.Contains(trailers, []byte("grpc-status: 0")) {
		t.Fatalf("trailers = %q, want grpc-status 0", trailers)
	}
}
