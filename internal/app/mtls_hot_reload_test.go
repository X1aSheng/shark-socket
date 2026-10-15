package app

import (
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

// testCA is a self-signed CA plus a client certificate it issued.
type testCA struct {
	pem     []byte
	subject string
	leafPEM []byte
}

func newTestCA(t *testing.T, commonName string, serial int64) testCA {
	t.Helper()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(serial),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}

	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial * 10),
		Subject:      pkix.Name{CommonName: commonName + "-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return testCA{
		pem:     pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		subject: commonName,
		leafPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
	}
}

// verifyAgainst reports whether the client leaf verifies against the pool.
func (c testCA) verifyAgainst(t *testing.T, pool *x509.CertPool) error {
	t.Helper()
	block, _ := pem.Decode(c.leafPEM)
	if block == nil {
		t.Fatal("client leaf PEM decode failed")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	_, err = leaf.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	return err
}

func writeTestServerCert(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(99),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certFile = filepath.Join(dir, "server.crt")
	keyFile = filepath.Join(dir, "server.key")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

// TestClientCAHotReloadTakesEffect verifies that rotating the mTLS client CA
// file changes the CA set used for client verification without restarting the
// process. tls.Config.ClientCAs is a snapshot taken when the config is built,
// so assigning the pool once pins the CA loaded at startup: a rotation would
// silently keep trusting the retired CA and reject certificates from the new
// one.
func TestClientCAHotReloadTakesEffect(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeTestServerCert(t, dir)
	caOne := newTestCA(t, "ca-one", 1)
	caTwo := newTestCA(t, "ca-two", 2)

	caFile := filepath.Join(dir, "client-ca.pem")
	if err := os.WriteFile(caFile, caOne.pem, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, cache, err := loadServerTLSConfig(ProtocolConfig{
		TLSCertFile:     certFile,
		TLSKeyFile:      keyFile,
		TLSClientCAFile: caFile,
		TLSClientAuth:   "require_and_verify",
	})
	if err != nil {
		t.Fatal(err)
	}

	// The pool the TLS stack uses for a handshake.
	handshakePool := func() *x509.CertPool {
		t.Helper()
		if cfg.GetConfigForClient == nil {
			if cfg.ClientCAs == nil {
				t.Fatal("no ClientCAs resolved for a config with a client CA")
			}
			return cfg.ClientCAs
		}
		next, err := cfg.GetConfigForClient(nil)
		if err != nil {
			t.Fatalf("GetConfigForClient: %v", err)
		}
		if next == nil || next.ClientCAs == nil {
			t.Fatal("per-handshake config has no ClientCAs pool")
		}
		return next.ClientCAs
	}

	if err := caOne.verifyAgainst(t, handshakePool()); err != nil {
		t.Fatalf("client signed by the configured CA does not verify: %v", err)
	}
	if err := caTwo.verifyAgainst(t, handshakePool()); err == nil {
		t.Fatal("client signed by an unrelated CA verified before rotation")
	}

	// Rotate the CA file and reload through the same cache the watcher uses.
	if err := os.WriteFile(caFile, caTwo.pem, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cache.Load(); err != nil {
		t.Fatalf("reload after CA rotation: %v", err)
	}

	if err := caTwo.verifyAgainst(t, handshakePool()); err != nil {
		t.Fatalf("client signed by the rotated CA does not verify after reload: %v", err)
	}
	if err := caOne.verifyAgainst(t, handshakePool()); err == nil {
		t.Fatal("client signed by the retired CA still verifies after reload: the CA pool is pinned to the startup value")
	}
}

var _ = tls.ClientAuthType(0)
