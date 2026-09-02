package shared

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"
)

// testCA creates a CA and one client leaf signed by it.
func testCA(t *testing.T) (pool *x509.CertPool, trustedDER [][]byte, untrustedDER [][]byte) {
	t.Helper()
	makeCA := func() (*x509.Certificate, *ecdsa.PrivateKey) {
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		tmpl := &x509.Certificate{
			SerialNumber:          big.NewInt(time.Now().UnixNano()),
			Subject:               pkix.Name{CommonName: "test-ca"},
			NotBefore:             time.Now().Add(-time.Hour),
			NotAfter:              time.Now().Add(24 * time.Hour),
			IsCA:                  true,
			BasicConstraintsValid: true,
			KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
		if err != nil {
			t.Fatal(err)
		}
		ca, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		return ca, priv
	}

	leaf := func(ca *x509.Certificate, caPriv *ecdsa.PrivateKey) [][]byte {
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(time.Now().UnixNano()),
			Subject:      pkix.Name{CommonName: "test-client"},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(24 * time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &priv.PublicKey, caPriv)
		if err != nil {
			t.Fatal(err)
		}
		return [][]byte{der}
	}

	ca1, ca1Priv := makeCA()
	ca2, ca2Priv := makeCA()
	pool = x509.NewCertPool()
	pool.AddCert(ca1)
	return pool, leaf(ca1, ca1Priv), leaf(ca2, ca2Priv)
}

func TestVerifyClientAuthPolicies(t *testing.T) {
	pool, trusted, untrusted := testCA(t)

	tests := []struct {
		name    string
		cfg     *tls.Config
		certs   [][]byte
		wantErr bool
	}{
		{name: "no policy accepts anything", cfg: nil, certs: nil, wantErr: false},
		{name: "request-only does not require a cert", cfg: &tls.Config{ClientAuth: tls.RequestClientCert}, certs: nil, wantErr: false},
		{name: "require-any rejects no cert", cfg: &tls.Config{ClientAuth: tls.RequireAnyClientCert}, certs: nil, wantErr: true},
		{name: "require-any accepts any cert", cfg: &tls.Config{ClientAuth: tls.RequireAnyClientCert}, certs: untrusted, wantErr: false},
		{name: "verify-if-given accepts no cert", cfg: &tls.Config{ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: pool}, certs: nil, wantErr: false},
		{name: "verify-if-given rejects untrusted", cfg: &tls.Config{ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: pool}, certs: untrusted, wantErr: true},
		{name: "verify-if-given accepts trusted", cfg: &tls.Config{ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: pool}, certs: trusted, wantErr: false},
		{name: "require-and-verify rejects no cert", cfg: &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}, certs: nil, wantErr: true},
		{name: "require-and-verify rejects untrusted", cfg: &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}, certs: untrusted, wantErr: true},
		{name: "require-and-verify accepts trusted", cfg: &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}, certs: trusted, wantErr: false},
		{name: "require-and-verify without pool rejects", cfg: &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert}, certs: trusted, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := verifyClientAuth(tt.cfg, tt.certs)
			if (err != nil) != tt.wantErr {
				t.Fatalf("verifyClientAuth error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
