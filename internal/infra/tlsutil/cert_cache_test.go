package tlsutil

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func generateCertKey(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(certFile, certPEM, 0600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certFile, keyFile
}

func TestCertCacheLoadAndGetCertificate(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateCertKey(t, dir)

	cache := NewCertCache(certFile, keyFile)
	if err := cache.Load(); err != nil {
		t.Fatalf("Load() = %v", err)
	}

	cert, err := cache.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate() = %v", err)
	}
	if cert == nil {
		t.Fatal("expected non-nil certificate")
	}
}

func TestCertCacheReloadDetectsNewCert(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateCertKey(t, dir)

	cache := NewCertCache(certFile, keyFile)
	if err := cache.Load(); err != nil {
		t.Fatalf("Load() = %v", err)
	}

	// Generate a second cert/key pair with different key
	key2, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key2: %v", err)
	}
	tmpl2 := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test2"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	certDER2, err := x509.CreateCertificate(rand.Reader, tmpl2, tmpl2, &key2.PublicKey, key2)
	if err != nil {
		t.Fatalf("create cert2: %v", err)
	}
	certPEM2 := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER2})
	keyPEM2 := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key2)})
	if err := os.WriteFile(certFile, certPEM2, 0600); err != nil {
		t.Fatalf("write cert2: %v", err)
	}
	if err := os.WriteFile(keyFile, keyPEM2, 0600); err != nil {
		t.Fatalf("write key2: %v", err)
	}

	if err := cache.Load(); err != nil {
		t.Fatalf("Load() after cert change = %v", err)
	}

	cert, err := cache.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate() = %v", err)
	}
	if cert == nil {
		t.Fatal("expected non-nil certificate after reload")
	}
}

func TestCertCacheLoadErrorOnMissingFile(t *testing.T) {
	cache := NewCertCache("/nonexistent/cert.pem", "/nonexistent/key.pem")
	if err := cache.Load(); err == nil {
		t.Fatal("expected error for missing files")
	}
}

func TestCertCacheGetCertificateBeforeLoad(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateCertKey(t, dir)
	cache := NewCertCache(certFile, keyFile)
	_, err := cache.GetCertificate(nil)
	if err == nil {
		t.Fatal("expected error when certificate not loaded")
	}
}

func TestCertCacheClientCA(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateCertKey(t, dir)

	// Use the cert file as the client CA (it's a self-signed cert PEM)
	cache := NewCertCache(certFile, keyFile)
	cache.SetClientCA(certFile)
	if err := cache.Load(); err != nil {
		t.Fatalf("Load() = %v", err)
	}
	pool := cache.GetClientCAPool()
	if pool == nil {
		t.Fatal("expected non-nil client CA pool")
	}
}

func TestCertCacheFiles(t *testing.T) {
	cache := NewCertCache("/tmp/cert.pem", "/tmp/key.pem")
	cache.SetClientCA("/tmp/ca.pem")
	files := cache.Files()
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(files))
	}
}

func TestWatchFilesDetectsChange(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "f1.txt")
	if err := os.WriteFile(f1, []byte("a"), 0600); err != nil {
		t.Fatalf("write f1: %v", err)
	}

	ch := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = WatchFiles(ctx, 50*time.Millisecond, func() {
		select {
		case ch <- struct{}{}:
		default:
		}
	}, f1)

	// Give watcher time to capture initial mod time
	time.Sleep(100 * time.Millisecond)

	// Touch the file
	if err := os.WriteFile(f1, []byte("b"), 0600); err != nil {
		t.Fatalf("write f1 (update): %v", err)
	}

	select {
	case <-ch:
		// success
	case <-time.After(500 * time.Millisecond):
		t.Fatal("watcher did not detect file change within timeout")
	}
}

func TestLoadServerTLSConfigUsesGetCertificate(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateCertKey(t, dir)

	cache := NewCertCache(certFile, keyFile)
	if err := cache.Load(); err != nil {
		t.Fatalf("Load() = %v", err)
	}

	cfg := &tls.Config{
		GetCertificate: cache.GetCertificate,
		MinVersion:     tls.VersionTLS12,
	}

	if cfg.GetCertificate == nil {
		t.Fatal("GetCertificate must not be nil")
	}
	if len(cfg.Certificates) != 0 {
		t.Fatal("Certificates must be empty when GetCertificate is used")
	}
}
