// Command verify_quic is a throwaway client for examples/basic_quic.
//
// It exists to prove the three non-obvious QUIC requirements actually hold
// against the running example, rather than leaving them as comments that could
// drift from the code:
//
//  1. the handshake completes with the example's self-signed cert
//  2. half-closing the request stream makes the server's io.ReadAll return
//  3. the echo arrives on a server-opened stream, accepted here
//
// Not a test: tests/stress/stress_protocols_test.go already covers QUIC under
// load. This is a manual check that the example and its documentation agree.
//
// Run the example first, then:
//
//	go run ./examples/basic_quic &   # in another shell
//	go run ./examples/quic_verify_client
package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"time"

	quicgo "github.com/quic-go/quic-go"
)

func main() {
	cfg := &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         "localhost",
		NextProtos:         []string{"shark-socket-quic"},
	}

	conn, err := quicgo.DialAddr(context.Background(), "127.0.0.1:18005", cfg, nil)
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer conn.CloseWithError(0, "")

	payload := []byte("verify-quic-round-trip")

	// Requirement 2: half-close after writing, or the server never sees EOF and
	// the handler never runs.
	reqStream, err := conn.OpenStreamSync(context.Background())
	if err != nil {
		log.Fatalf("open request stream: %v", err)
	}
	if _, err := reqStream.Write(payload); err != nil {
		log.Fatalf("write: %v", err)
	}
	if err := reqStream.Close(); err != nil {
		log.Fatalf("half-close: %v", err)
	}

	// Requirement 3: the reply comes on a stream the server opens.
	acceptCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	respStream, err := conn.AcceptStream(acceptCtx)
	if err != nil {
		log.Fatalf("accept reply stream: %v", err)
	}

	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(respStream, buf); err != nil {
		log.Fatalf("read echo: %v", err)
	}

	if string(buf) != string(payload) {
		log.Fatalf("echo = %q, want %q", buf, payload)
	}
	fmt.Printf("OK: handshake, half-close and server-opened reply stream all work; echo=%q\n", buf)
}
