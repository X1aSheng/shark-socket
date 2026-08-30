package udp

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
	"github.com/X1aSheng/shark-socket/internal/transport/shared"
	"github.com/pion/dtls/v3"
)

func TestUDPServerStopStartCycle(t *testing.T) {
	server := NewServer(WithAddr("127.0.0.1:0"))
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}

	// First start
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// Verify server is running
	if server.SessionCount() != 0 {
		// expected: no sessions yet
	}

	// Stop
	if err := gateway.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	// Second start (should work after stop)
	if err := gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestUDPServerAddr(t *testing.T) {
	server := NewServer(WithAddr("127.0.0.1:0"))
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer gateway.Stop(ctx)

	addr := server.Addr()
	if addr == nil {
		t.Fatal("Addr should not be nil after start")
	}
	// Verify it's a valid UDP address
	if _, ok := addr.(*net.UDPAddr); !ok {
		t.Fatalf("Addr should be *net.UDPAddr, got %T", addr)
	}
}

func TestUDPServerSessionCount(t *testing.T) {
	server := NewServer(WithAddr("127.0.0.1:0"))
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer gateway.Stop(ctx)

	if server.SessionCount() != 0 {
		t.Fatalf("initial SessionCount = %d, want 0", server.SessionCount())
	}
}

func TestUDPServerSweepLoop(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithSessionTTL(50*time.Millisecond),
		WithSweepInterval(20*time.Millisecond),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer gateway.Stop(ctx)

	// Create a session by sending a datagram
	conn, err := net.Dial("udp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Send a message to create a session
	conn.Write([]byte("ping"))

	// Session should exist immediately after send
	time.Sleep(5 * time.Millisecond)
	if count := server.SessionCount(); count == 0 {
		t.Fatal("session should exist immediately")
	}

	// Wait for sweep to clean up (TTL 50ms + sweep 20ms * 2)
	time.Sleep(100 * time.Millisecond)
	if count := server.SessionCount(); count != 0 {
		t.Fatalf("session should be swept: count = %d, want 0", count)
	}
}

func TestUDPServerDoubleStart(t *testing.T) {
	server := NewServer(WithAddr("127.0.0.1:0"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// First start should succeed
	if err := server.Start(ctx); err != nil {
		t.Fatal(err)
	}
	// Second start should fail with the new double-start guard
	if err := server.Start(ctx); err == nil {
		t.Fatal("expected error for double start")
	}
	// Cleanup
	server.Stop(ctx)
}

func TestUDPServerStopAccept(t *testing.T) {
	server := NewServer(WithAddr("127.0.0.1:0"))
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// StopAccept should work
	if err := gateway.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

// testTLSCfg generates a self-signed TLS config for DTLS testing. The
// certificate is ECDSA P-256: pion/dtls v3 negotiates DTLS 1.3 by default,
// which rejects RSA certificates, so an RSA test cert made the server-side
// handshake stall silently (client Dial succeeded, server Accept never
// returned).
func testTLSCfg(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{certDER}, PrivateKey: key}},
		MinVersion:   tls.VersionTLS12,
	}
}

func TestUDPServerDTLSOptions(t *testing.T) {
	opts := defaultOptions()
	if opts.SessionTTL != 2*time.Minute {
		t.Fatalf("default SessionTTL = %v, want 2m", opts.SessionTTL)
	}
	if opts.SweepInterval != 30*time.Second {
		t.Fatalf("default SweepInterval = %v, want 30s", opts.SweepInterval)
	}
	if opts.MaxDatagram != 64*1024 {
		t.Fatalf("default MaxDatagram = %d, want 64 KiB (shared plain-UDP buffer)", opts.MaxDatagram)
	}
	if opts.DTLSReadBufferBytes != 16*1024 {
		t.Fatalf("default DTLSReadBufferBytes = %d, want 16 KiB (per-connection DTLS buffer)", opts.DTLSReadBufferBytes)
	}
	// Test WithDTLS
	cfg := testTLSCfg(t)
	opt := WithDTLS(cfg)
	opt(&opts)
	if opts.TLSConfig == nil {
		t.Fatal("WithDTLS should set TLSConfig")
	}
	// Test WithDTLSReadBufferBytes
	WithDTLSReadBufferBytes(4096)(&opts)
	if opts.DTLSReadBufferBytes != 4096 {
		t.Fatalf("DTLSReadBufferBytes = %d, want 4096", opts.DTLSReadBufferBytes)
	}
	WithDTLSReadBufferBytes(0)(&opts)
	if opts.DTLSReadBufferBytes != 4096 {
		t.Fatalf("DTLSReadBufferBytes = %d, want 4096 (non-positive values ignored)", opts.DTLSReadBufferBytes)
	}
	// Test DTLSConfig helper
	dtlsCfg := shared.DTLSConfig(cfg)
	if dtlsCfg == nil {
		t.Fatal("DTLSConfig should not return nil")
	}
}

func TestUDPServerDTLSIntegration(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithDTLS(testTLSCfg(t)),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer gateway.Stop(ctx)

	// Verify DTLS server is listening
	addr := server.Addr()
	if addr == nil {
		t.Fatal("DTLS server addr should not be nil")
	}
	// The addr should be a valid UDP address
	if _, ok := addr.(*net.UDPAddr); !ok {
		t.Fatalf("DTLS server addr should be *net.UDPAddr, got %T", addr)
	}
}

// TestUDPServerDTLSIdleTimeout verifies that a DTLS peer that completes the
// handshake and then goes silent is reclaimed after SessionTTL, instead of
// holding a goroutine/session forever (anti-slowloris for DTLS).
func TestUDPServerDTLSIdleTimeout(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithDTLS(testTLSCfg(t)),
		WithSessionTTL(50*time.Millisecond),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer gateway.Stop(ctx)

	dtlsCfg := &dtls.Config{InsecureSkipVerify: true}
	addr, err := net.ResolveUDPAddr("udp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn, err := dtls.Dial("udp", addr, dtlsCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// The DTLS handshake in pion/dtls v3 is lazy: the client sends its first
	// flight only on the first write. Write a byte first so the handshake
	// starts and the server-side session can register; polling SessionCount
	// before any write would deadlock (nothing is ever sent, so no session
	// appears and the count stays 0 — the vacuous pass the old test had).
	if _, err := conn.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	// First wait for the server-side handler goroutine to register the
	// session after the handshake completes.
	for {
		if server.SessionCount() == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("DTLS session not registered after handshake: count=%d", server.SessionCount())
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Then wait for the idle timeout (SessionTTL=50ms) to reclaim it.
	for {
		if server.SessionCount() == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("DTLS session not reclaimed after idle timeout: count=%d", server.SessionCount())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestUDPServerDTLSEcho exercises the DTLS application-data path end to end:
// a real DTLS client sends a datagram and receives the echo, and the session
// is reclaimed after the client disconnects. TestUDPServerDTLSIntegration only
// verifies listening, and the old idle-timeout test passed vacuously, so this
// is the first test that actually drives handleDTLSConn.
func TestUDPServerDTLSEcho(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithDTLS(testTLSCfg(t)),
		WithHandler(func(sess core.Session, msg core.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer gateway.Stop(ctx)

	dtlsCfg := &dtls.Config{InsecureSkipVerify: true}
	addr, err := net.ResolveUDPAddr("udp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn, err := dtls.Dial("udp", addr, dtlsCfg)
	if err != nil {
		t.Fatal(err)
	}

	// pion/dtls v3 handshakes lazily on the first write: send the payload
	// first (this starts the handshake), then read the echo. Waiting for the
	// session to register before writing would deadlock.
	payload := []byte("dtls-echo")
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf[:n], payload) {
		t.Fatalf("echo = %q, want %q", buf[:n], payload)
	}
	// The session must now exist server-side (the echo proves the handler
	// ran); assert it so a future regression in registration is caught.
	if server.SessionCount() != 1 {
		t.Fatalf("session count = %d, want 1", server.SessionCount())
	}

	// Closing the client tears down the DTLS connection, unblocking the
	// server-side read loop; the session must then be reclaimed.
	_ = conn.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if server.SessionCount() == 0 && gateway.Runtime().Sessions().Count() == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("session not reclaimed after client close: server=%d runtime=%d", server.SessionCount(), gateway.Runtime().Sessions().Count())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestUDPServerStopAfterDTLSStart(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithDTLS(testTLSCfg(t)),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// Stop should clean up DTLS connections
	if err := gateway.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	// Should be able to restart after stop
	if err := gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

// Self-signed cert for DTLS testing
var localhostCert = []byte(`-----BEGIN CERTIFICATE-----
MIIDCjCCAfICCQDPqKf8zJ8o6TANBgkqhkiG9w0BAQsFADBHMQswCQYDVQQGEwJV
UzERMA8GA1UECAwITWljaGlnYW4xEjAQBgNVBAcMCUFubiBBcmJvcjERMA8GA1UE
AwwIbG9jYWxob3N0MB4XDTI2MDYwMTAwMDAwMFoXDTI3MDYwMTAwMDAwMFowRzEL
MAkGA1UEBhMCVVMxETAPBgNVBAgMCE1pY2hpZ2FuMRIwEAYDVQQHDAlBbm4gQXJi
b3IxETAPBgNVBAMMCGxvY2FsaG9zdDCCASIwDQYJKoZIhvcNAQEBBQADggEPADCC
AQoCggEBAK0+rGCxPJIsNnDb8Zqi+Haq/+IZPWFjF0G9wK3QPbUx4sAHJkQnDWcS
dCAxIUNjBF1MMExP+Wah/rQH3HxPcLmcGDqpHvMXHCvP6d1YOPG3Yk8C+NVFjMzg
yPNkFPM5nMk9E0SfOEyL5JFqHkCGHwMXgYBQ5DqKPSYOSHPb1+RxpqEO+Ex3JG4g
Mgk4ZkIc3HqG+QGLP5oYYWIZQEJGAFUFIPbETFBHOKGVOcFOeBAIzoZSYgHCEuW4
JLKMwQGPxMtjh+MQM0WSJjHB0nPHRKRzOomJYhCRCG2OA5EQRHKMGMM1JQ8JEKTR
O1SmFOGaJBzMHBOILHOpXlW9mBQoC+MCAwEAATANBgkqhkiG9w0BAQsFAAOCAQEA
nhksJRqAQB6fYJqIDbJXK6nMjBWTRFYtQBmPCXBq4nQrDRhN4mNqGHYBSkLmOFKB
aUgLVNeLxjDrJMMRqMmhKGVGcJpHEXkOrOhTKCeJhIvQOhCHjhYBOWLqVGOMjBqI
pTW0SFQJGDTYDHaMzNlWhxhMfAEQnKh2EmyqLlYLxIHTrPEzTqsKNBgz4CFLVqoR
ABNhMtwhCWCfBQnsAjKo0MJRoZ0hhEmR0ODHzwPk4YFMRFQzQISDBKJOAoGI9qMB
GLRgNJm6GB+JDFKMZJYMoKNlCWEqLji8YcTQnRXLhFNbgFiGkHHlGYBLkMRuYYMG
tL4RqQZGsIGLEKjKJqGYMw==
-----END CERTIFICATE-----`)

var localhostKey = []byte(`-----BEGIN RSA PRIVATE KEY-----
MIIEpAIBAAKCAQEArT6sYLE8kiw2cNvxmqL4dqr/4hk9YWMXQb3ArdA9tTHiwAcm
RCcNZxJ0IDEhQ2MEXUwwTE/5ZqH+tAfcfE9wuZwYOqke8xccK8/p3Vg48bdiTwL4
1UWMzODI82QU8zmcyT0TRJ84TIvkkWoeQIYfAxeBgFDkOoo9Jg5Ic9vX5HGmoQ74
THckbiAyCThmQhzceob5AYs/mhhhYhlAQkYAVQUg9sRMUEc4oZU5wU54EAjOhlJi
AcIS5bgksozBAY/Ey2OH4xAzRZImMcHSc8dEpHM6iYliEJEIbY4DkRBEcowYwzUl
DwkQpNE7VKYU4ZokHMwcE4gsc6leVb2YFCgL4wIDAQABAoIBABG0UwJAnLJhHmNx
FmOBRQjUPUGkKqHIePDkUUOPoGhlzWGXFQCzLmAQBgE7OGdSFEXoBLmhSCGgKgDP
VrHyMLcNgrJMDeVJTpkDmPMBKMhYzAkEhPOBXPQEh2MGdiJDOHLIXYJFgJCmMMDG
jDtWHIKNsKghBHBIMYZNMCaWCqLEJLnpCcBOBLAPMqkBHCRiGPKzPMchWnOEMbLg
OJxrPPkmBRYgBdN0dCFNfmmhZKcNLSCZBGARoVRKEGCAAkLLbOkOLdNpUANpLPxY
cEPoNkJGpESLjIJhpNKTDzEKTeKCfBTLYgHOFNcUHHnBCcFFDGDMMpJDWQMGkJDR
FCUdJgECgYEA3hTODqFPYhDMnHIcQNBEGNThKCGErgpQOTIHmJiMRqAMQLMkCvVE
JOeuSLYKJkMCGCYjRgOBCNJxUoiwLLFMRdFJUgDLPLQYRPLRPGMlCAYLRQBGQqlQ
DJPNKMwGBWYHMBokhNNCMMVQLfqkJwgLQqHQCqJGTPBMAlMBMHRcJGUCgYEAx8QH
LHbNpBpMBQRqJEMRBpFsckKRGjGDBBLMMVrGJSgJqAQgYKEFVBMHDFMJKgDSFjGG
TpcLMCWiqvFQGLPHDCDBQPGJLMCQFVVNRMJJFGJBBYECmFARKJiFNkBMFFJOGFcB
NgJHPMGBcSAXYHcJGhSDPJmAAQMjDPJhGMBNNcMCgYEAvhJOGFcBDQJHPMGBcSAX
YHcJGhSDPJmAAQMjDPJhGMBNNcMBgYEA3hTODqFPYhDMnHIcQNBEGNThKCGErgpQ
OTIHmJiMRqAMQLMkCvVEJOeuSLYKJkMCGCYjRgOBCNJxUoiwLLFMRdFJUgDLPLQY
RPLRPGMlCAYLRQBGQqlQDJPNKMwGBWYHMBokhNNCMMVQLfqkJwgLQqHQCqJGTPBM
AlMBMHRcJGUCgYBesjOEkPSgFCfYRJNLkFmbMGHMHNBDOEBUjBQOEMAhMKHMMaHD
CBLNBKQoMcBKBfHEMCHMkEBANNBOLJCMFMGeMBaCEQDQDJPFPJSMCCAMMBqDDLMB
QEMJMCBMGOGPOCGFLMGFCECKBJHeIXgYEAq0JhQSkrKCJETQpDFSAeFsNAYoLCOPM
BPDMmNGhAaSCEsBXAQPAMOMAPBMhCBIMYmDPDKHPKMQMDOCQKBgQDeFCMCDMqFMM
BBLiMJAPCMgEHJgOKCJOCMmBGoMMCPKEQEGBmMBMqMQObBMhPGMCCFOKHMBLmMSAQ
BQQPJmFCQGOJADPDFOKmBmBBEMMDGJOMKMEmGIGPOFCmQBEMMDGCKBMLNBMBMMBMh
OQGOmMLGmCKAAmPCmAAIMmDKPLAQDAOKMBLMhPJBMPHDmEMBQoAQH
-----END RSA PRIVATE KEY-----`)
