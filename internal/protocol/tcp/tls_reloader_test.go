package tcp

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTLSReloader_InitialLoad(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateTestCertPEM(t, dir, "initial")

	r, err := NewTLSReloader(certFile, keyFile)
	if err != nil {
		t.Fatalf("NewTLSReloader: %v", err)
	}
	defer r.Close()

	cert := r.cert.Load()
	if cert == nil {
		t.Fatal("expected cert to be loaded")
	}
	if cert.Leaf == nil {
		t.Log("cert.Leaf is nil (expected until first TLS handshake)")
	}
}

func TestTLSReloader_ReloadSwapsCert(t *testing.T) {
	dir := t.TempDir()
	certFile1, keyFile1 := generateTestCertPEM(t, dir, "first")
	certFile2, keyFile2 := generateTestCertPEM(t, dir, "second")

	// Use same file paths: write first cert, then overwrite with second.
	sharedCert := filepath.Join(dir, "server.crt")
	sharedKey := filepath.Join(dir, "server.key")

	copyFile(t, certFile1, sharedCert)
	copyFile(t, keyFile1, sharedKey)

	r, err := NewTLSReloader(sharedCert, sharedKey)
	if err != nil {
		t.Fatalf("NewTLSReloader: %v", err)
	}
	defer r.Close()

	// Capture initial cert serial.
	initial := r.cert.Load().Leaf

	// Overwrite cert files with second cert.
	copyFile(t, certFile2, sharedCert)
	copyFile(t, keyFile2, sharedKey)

	// Trigger reload.
	if err := r.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	// The cert should now be different.
	after := r.cert.Load().Leaf
	if after == nil {
		t.Fatal("expected Leaf to be parsed after reload")
	}
	if initial != nil && after.SerialNumber.Cmp(initial.SerialNumber) == 0 {
		t.Error("expected cert serial to change after reload")
	}
}

func TestTLSReloader_GetConfigForClient(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateTestCertPEM(t, dir, "cfg")

	r, err := NewTLSReloader(certFile, keyFile)
	if err != nil {
		t.Fatalf("NewTLSReloader: %v", err)
	}
	defer r.Close()

	cfg, err := r.GetConfigForClient(nil)
	if err != nil {
		t.Fatalf("GetConfigForClient: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil tls.Config")
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("expected 1 certificate, got %d", len(cfg.Certificates))
	}
}

func TestTLSReloader_ReloadFailureKeepsOldCert(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateTestCertPEM(t, dir, "keep")

	r, err := NewTLSReloader(certFile, keyFile)
	if err != nil {
		t.Fatalf("NewTLSReloader: %v", err)
	}
	defer r.Close()

	// Delete the cert file to force a reload failure.
	os.Remove(certFile)

	if err := r.Reload(); err == nil {
		t.Fatal("expected reload error when cert file is missing")
	}

	// The old cert should still be there.
	cert := r.cert.Load()
	if cert == nil {
		t.Fatal("expected old cert to be retained after failed reload")
	}
}

func TestTLSReloader_CloseStopsWatcher(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateTestCertPEM(t, dir, "close")

	r, err := NewTLSReloader(certFile, keyFile)
	if err != nil {
		t.Fatalf("NewTLSReloader: %v", err)
	}

	r.Close()

	// Double close should not panic.
	r.Close()

	// Reload after close should still work (direct call, not via channel).
	if err := r.Reload(); err != nil {
		t.Fatalf("Reload after Close: %v", err)
	}
}

func TestTLSReloader_TLSConfig(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateTestCertPEM(t, dir, "tlsconfig")

	r, err := NewTLSReloader(certFile, keyFile)
	if err != nil {
		t.Fatalf("NewTLSReloader: %v", err)
	}
	defer r.Close()

	cfg := r.TLSConfig()
	if cfg == nil {
		t.Fatal("expected non-nil tls.Config")
	}
	if cfg.GetConfigForClient == nil {
		t.Fatal("expected GetConfigForClient to be set")
	}
}

func TestTLSReloader_ReloadChan(t *testing.T) {
	dir := t.TempDir()
	certFile1, keyFile1 := generateTestCertPEM(t, dir, "ch1")
	certFile2, keyFile2 := generateTestCertPEM(t, dir, "ch2")

	sharedCert := filepath.Join(dir, "server.crt")
	sharedKey := filepath.Join(dir, "server.key")

	copyFile(t, certFile1, sharedCert)
	copyFile(t, keyFile1, sharedKey)

	r, err := NewTLSReloader(sharedCert, sharedKey)
	if err != nil {
		t.Fatalf("NewTLSReloader: %v", err)
	}
	defer r.Close()

	initialSerial := r.cert.Load().Leaf.SerialNumber

	// Overwrite and trigger reload via channel.
	copyFile(t, certFile2, sharedCert)
	copyFile(t, keyFile2, sharedKey)

	ch := r.ReloadChan()
	ch <- struct{}{}

	// Give the watcher goroutine time to process.
	time.Sleep(200 * time.Millisecond)

	afterSerial := r.cert.Load().Leaf.SerialNumber
	if initialSerial.Cmp(afterSerial) == 0 {
		t.Error("expected cert to change after channel-triggered reload")
	}
}

// --- Helpers ---

func generateTestCertPEM(t *testing.T, dir, suffix string) (certFile, keyFile string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "test-" + suffix},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}

	certFile = filepath.Join(dir, "cert-"+suffix+".pem")
	keyFile = filepath.Join(dir, "key-"+suffix+".pem")

	writePEMFile(t, certFile, "CERTIFICATE", certDER, 0644)

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	writePEMFile(t, keyFile, "EC PRIVATE KEY", keyDER, 0600)

	return certFile, keyFile
}

func writePEMFile(t *testing.T, path, blockType string, data []byte, perm os.FileMode) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: data}); err != nil {
		t.Fatalf("pem encode %s: %v", path, err)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0644); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}
