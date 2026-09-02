package shared

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"

	"github.com/pion/dtls/v3"
)

// EnforceServerClientAuth validates a freshly accepted DTLS connection
// against the server-side tls.Config client-authentication policy
// (RequestClientCert is never enforced — it only asks). pion/dtls v3 carries
// its own ClientAuth handling, but its chain verification is not reliably
// applied on every internal path (and its v3.1.2 client-auth server handshake
// can stall entirely), so DTLS transports call this immediately after Accept
// and close the connection when the peer fails the policy. That restores the
// crypto/tls semantics that the application configuration (tls_client_auth)
// promises, independent of pion's behaviour.
func EnforceServerClientAuth(tlsCfg *tls.Config, conn net.Conn) error {
	policy := tls.NoClientCert
	if tlsCfg != nil {
		policy = tlsCfg.ClientAuth
	}
	if policy < tls.RequireAnyClientCert {
		return nil // none / request-only: no server-side enforcement
	}
	dtlsConn, ok := conn.(*dtls.Conn)
	if !ok {
		return fmt.Errorf("dtls: cannot inspect peer certificate of %T", conn)
	}
	state, ok := dtlsConn.ConnectionState()
	if !ok {
		return fmt.Errorf("dtls: connection state unavailable")
	}
	return verifyClientAuth(tlsCfg, state.PeerCertificates)
}

// verifyClientAuth applies the crypto/tls client-authentication policy to a
// peer certificate list (the pure policy check, unit-testable).
func verifyClientAuth(tlsCfg *tls.Config, peerCerts [][]byte) error {
	policy := tls.NoClientCert
	if tlsCfg != nil {
		policy = tlsCfg.ClientAuth
	}
	if policy < tls.RequireAnyClientCert {
		return nil // none / request-only: nothing to enforce
	}
	if len(peerCerts) == 0 {
		switch policy {
		case tls.VerifyClientCertIfGiven:
			return nil // certificate optional under this policy
		default:
			return fmt.Errorf("dtls: client certificate required")
		}
	}
	if policy == tls.RequireAnyClientCert {
		return nil // presence is enough, nothing to verify against
	}
	// VerifyClientCertIfGiven and RequireAndVerifyClientCert verify the chain.
	if tlsCfg.ClientCAs == nil {
		return fmt.Errorf("dtls: client certificate cannot be verified: no client CA pool configured")
	}
	leaf, err := x509.ParseCertificate(peerCerts[0])
	if err != nil {
		return fmt.Errorf("dtls: parse client certificate: %w", err)
	}
	intermediates := x509.NewCertPool()
	for _, der := range peerCerts[1:] {
		if cert, parseErr := x509.ParseCertificate(der); parseErr == nil {
			intermediates.AddCert(cert)
		}
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:         tlsCfg.ClientCAs,
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		return fmt.Errorf("dtls: verify client certificate: %w", err)
	}
	return nil
}

// DTLSConfig converts a *tls.Config to *dtls.Config.
// Maps the most important security-relevant fields, including ClientAuth
// (pion/dtls v3 uses the same iota ordering as crypto/tls for its
// ClientAuthType, so a numeric conversion is safe) and the GetCertificate
// callback.
// NOTE: MinVersion is not directly mappable to pion/dtls v3 (no equivalent
// field). DTLS version is negotiated through cipher suite selection. Callers
// requiring TLS 1.3 should restrict CipherSuites to DTLS 1.3 suites.
func DTLSConfig(tlsCfg *tls.Config) *dtls.Config {
	cfg := &dtls.Config{
		Certificates:       tlsCfg.Certificates,
		InsecureSkipVerify: tlsCfg.InsecureSkipVerify,
		RootCAs:            tlsCfg.RootCAs,
		ClientCAs:          tlsCfg.ClientCAs,
		ServerName:         tlsCfg.ServerName,
		ClientAuth:         dtls.ClientAuthType(tlsCfg.ClientAuth),
	}
	// Map CipherSuites ([]uint16 → []CipherSuiteID)
	if len(tlsCfg.CipherSuites) > 0 {
		cfg.CipherSuites = make([]dtls.CipherSuiteID, len(tlsCfg.CipherSuites))
		for i, id := range tlsCfg.CipherSuites {
			cfg.CipherSuites[i] = dtls.CipherSuiteID(id)
		}
	}
	if tlsCfg.VerifyPeerCertificate != nil {
		cfg.VerifyPeerCertificate = tlsCfg.VerifyPeerCertificate
	}
	// Bridge crypto/tls GetCertificate into pion/dtls. This is what makes the
	// application configuration path work: loadServerTLSConfig keeps
	// Certificates empty and serves certificates through GetCertificate
	// (tlsutil.CertCache, which also enables hot reload). pion/dtls has its
	// own ClientHelloInfo type that cannot be populated from the crypto/tls
	// one, so the callback is invoked with a nil info — CertCache ignores it;
	// custom callbacks that dereference it must provide their own bridge. A
	// panicking callback fails that one handshake instead of crashing the
	// process.
	if len(tlsCfg.Certificates) == 0 && tlsCfg.GetCertificate != nil {
		getCertificate := tlsCfg.GetCertificate
		cfg.GetCertificate = func(*dtls.ClientHelloInfo) (cert *tls.Certificate, err error) {
			defer func() {
				if r := recover(); r != nil {
					cert, err = nil, fmt.Errorf("dtls GetCertificate panic: %v", r)
				}
			}()
			return getCertificate(nil)
		}
	}
	return cfg
}
