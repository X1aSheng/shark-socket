package grpcweb

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
)

func startTestServer(t *testing.T, handler core.Handler, opts ...Option) (*Server, *runtime.Gateway) {
	t.Helper()
	all := []Option{WithAddr("127.0.0.1:0")}
	if handler != nil {
		all = append(all, WithHandler(handler))
	}
	all = append(all, opts...)
	server := NewServer(all...)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopGateway(t, gateway) })
	return server, gateway
}

func doPost(t *testing.T, url string, body []byte, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// TestPassthroughResponseKeepsContentType verifies that a request which does
// NOT declare gRPC-Web receives a raw response whose Content-Type is not
// rewritten to application/grpc-web+proto. Advertising a gRPC-Web content
// type on a raw body makes conformant clients parse plain bytes as frames.
func TestPassthroughResponseKeepsContentType(t *testing.T) {
	server, _ := startTestServer(t, func(sess core.Session, msg core.Message) error {
		return sess.Send([]byte("plain"))
	})

	resp := doPost(t, "http://"+server.Addr().String()+"/grpc", []byte("hello"),
		map[string]string{"content-type": "application/protobuf"})
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, []byte("plain")) {
		t.Fatalf("body = %q, want plain", body)
	}
	if got := resp.Header.Get("content-type"); strings.HasPrefix(got, "application/grpc-web") {
		t.Fatalf("passthrough response content-type = %q, want non-grpc-web", got)
	}
}

// TestDeclaredGRPCWebWithEmptyBodyStillGetsTrailers verifies that a request
// declaring the gRPC-Web content type always receives a framed response with
// a grpc-status trailer, even when the request body carries no data frame
// (for example an empty body). Without that trailer a gRPC-Web client cannot
// determine whether the call succeeded.
func TestDeclaredGRPCWebWithEmptyBodyStillGetsTrailers(t *testing.T) {
	server, _ := startTestServer(t, func(sess core.Session, msg core.Message) error {
		return sess.Send([]byte("ok"))
	})

	resp := doPost(t, "http://"+server.Addr().String()+"/grpc", nil,
		map[string]string{"content-type": "application/grpc-web+proto"})
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	payload, trailers := readTestGRPCWebResponse(t, body)
	if string(payload) != "ok" {
		t.Fatalf("payload = %q, want ok", payload)
	}
	if !bytes.Contains(trailers, []byte("grpc-status: 0")) {
		t.Fatalf("trailers = %q, want grpc-status 0", trailers)
	}
}

// TestGRPCMessageIsPercentEncoded verifies that the grpc-message trailer value
// is percent-encoded per the gRPC wire spec. A raw CR/LF or non-ASCII byte in
// the message would otherwise corrupt the trailer framing (header injection)
// and could not be decoded by the client.
func TestGRPCMessageIsPercentEncoded(t *testing.T) {
	// A message containing a percent sign, CR and a non-ASCII byte: all three
	// must be escaped so the trailer block stays well-formed and decodable.
	boom := errors.New("boom 100% \r\n\345\244\261\350\264\245")
	server, _ := startTestServer(t, func(sess core.Session, msg core.Message) error {
		return boom
	})

	resp := doPost(t, "http://"+server.Addr().String()+"/grpc", testGRPCWebDataFrame([]byte("x")),
		map[string]string{"content-type": "application/grpc-web+proto", "x-grpc-web": "1"})
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	_, trailers := readTestGRPCWebResponse(t, body)
	text := string(trailers)
	idx := strings.Index(text, "grpc-message: ")
	if idx < 0 {
		t.Fatalf("trailers = %q, want a grpc-message", text)
	}
	value := text[idx+len("grpc-message: "):]
	if i := strings.Index(value, "\r\n"); i >= 0 {
		value = value[:i]
	}
	if strings.ContainsAny(value, "\r\n") {
		t.Fatalf("grpc-message contains a raw line break: %q", value)
	}
	for _, want := range []string{"%25", "%0D", "%0A", "%E5"} {
		if !strings.Contains(value, want) {
			t.Fatalf("grpc-message = %q, want it to contain %s", value, want)
		}
	}
}
