package main

import (
	"crypto/tls"
	"crypto/x509"
	"testing"
	"time"
)

// TestGenerateSelfSignedCertIsUsable pins the fix for a broken example: the
// embedded PEM pair it used to return did not parse, so main() died with
// "x509: malformed certificate" before the gateway ever started. This asserts
// the generated certificate parses, carries the expected SANs, and completes a
// real TLS handshake against a listener that trusts it.
func TestGenerateSelfSignedCertIsUsable(t *testing.T) {
	cert, err := generateSelfSignedCert()
	if err != nil {
		t.Fatalf("generateSelfSignedCert: %v", err)
	}
	if len(cert.Certificate) == 0 || cert.PrivateKey == nil {
		t.Fatal("certificate has no DER chain or private key")
	}

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("parse generated certificate: %v", err)
	}
	if err := leaf.VerifyHostname("localhost"); err != nil {
		t.Fatalf("certificate is not valid for localhost: %v", err)
	}
	if err := leaf.VerifyHostname("127.0.0.1"); err != nil {
		t.Fatalf("certificate is not valid for 127.0.0.1: %v", err)
	}
	if !leaf.NotAfter.After(time.Now()) {
		t.Fatalf("certificate expired at %v", leaf.NotAfter)
	}

	// Present it on a real listener and connect to it with a client that trusts
	// the generated certificate as its own root.
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatalf("tls listen: %v", err)
	}
	defer ln.Close()

	serverErr := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		// Trigger the handshake and drop the connection.
		_ = conn.(*tls.Conn).Handshake()
		_ = conn.Close()
		serverErr <- nil
	}()

	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	client, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{
		RootCAs:    roots,
		ServerName: "localhost",
		MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("tls dial with the generated certificate as root: %v", err)
	}
	_ = client.Close()
	if err := <-serverErr; err != nil {
		t.Fatalf("server side handshake: %v", err)
	}
}
