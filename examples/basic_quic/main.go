// Command basic_quic is a minimal QUIC echo server.
//
// QUIC is the one transport in this project where "just run it" does not work,
// and the reasons are worth stating up front because they cost real debugging
// time otherwise:
//
//  1. QUIC requires TLS 1.3, so the server needs a certificate. There is no
//     plaintext mode. This example generates a self-signed one for localhost,
//     which is enough for development and nothing else.
//
//  2. The server reads each stream with io.ReadAll, which returns only at EOF.
//     A client that writes a message without closing its write side leaves the
//     payload buffered on the server and the handler never runs. The client
//     must half-close (Close) to signal the end of the message.
//
//  3. Replies arrive on a stream the SERVER opens, not on the stream the request
//     was sent on: session.Send opens a fresh stream per payload. A client that
//     reads its request stream for a reply blocks forever even though the
//     handler completed. The client must AcceptStream instead.
//
// The client that matches this server is in tests/stress/stress_protocols_test.go
// (TestStressQUICStreams), which does all three correctly and measures throughput.
//
// Run:
//
//	go run ./examples/basic_quic
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log"
	"math/big"
	"net"
	"os/signal"
	"syscall"
	"time"

	"github.com/X1aSheng/shark-socket/api"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	tlsCfg := &tls.Config{
		// The certificate must carry a DNS name matching the SNI the client
		// sends. A cert with only IPAddresses is accepted by a dialer that
		// skips verification, but the handshake itself rejects it with
		// "tls: unrecognized name".
		Certificates: []tls.Certificate{selfSignedCert()},
		NextProtos:   []string{"shark-socket-quic"},
		MinVersion:   tls.VersionTLS13,
	}

	gateway := api.NewGateway()
	server := api.NewQUICServer(
		api.WithQUICAddr("127.0.0.1:18005"),
		api.WithQUICTLS(tlsCfg),
		api.WithQUICHandler(echoHandler),
	)
	if err := gateway.Register(server); err != nil {
		log.Fatal(err)
	}
	if err := gateway.Start(ctx); err != nil {
		log.Fatal(err)
	}
	log.Println("quic echo listening on 127.0.0.1:18005 (ALPN shark-socket-quic)")
	log.Println("client must: half-close the request stream, then AcceptStream for the echo")

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := gateway.Stop(shutdownCtx); err != nil {
		log.Fatal(err)
	}
}

// echoHandler returns each message to its sender.
//
// The payload is the raw bytes read from the stream. Because the transport
// frames nothing on top, the receiver gets exactly what was written.
func echoHandler(sess api.Session, msg api.Message) error {
	return sess.Send(msg.Payload)
}

// selfSignedCert builds an ECDSA P-256 certificate for localhost.
//
// ECDSA rather than RSA because TLS 1.3 negotiates it without the extra round
// trips, and P-256 is the curve quic-go expects by default. The lifetime is
// short: a long-lived development cert is how stale self-signed certs end up
// trusted by reflex.
func selfSignedCert() tls.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		log.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		log.Fatal(err)
	}
	cert, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	)
	if err != nil {
		log.Fatal(err)
	}
	return cert
}
