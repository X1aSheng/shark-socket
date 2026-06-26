package shared

import (
	"crypto/tls"

	"github.com/pion/dtls/v3"
)

// DTLSConfig converts a *tls.Config to *dtls.Config.
// Maps the most important security-relevant fields.
// NOTE: MinVersion is not directly mappable to pion/dtls v3 (no equivalent field).
// DTLS version is negotiated through cipher suite selection. Callers requiring
// TLS 1.3 should restrict CipherSuites to DTLS 1.3 suites.
func DTLSConfig(tlsCfg *tls.Config) *dtls.Config {
	cfg := &dtls.Config{
		Certificates:       tlsCfg.Certificates,
		InsecureSkipVerify: tlsCfg.InsecureSkipVerify,
		RootCAs:            tlsCfg.RootCAs,
		ClientCAs:          tlsCfg.ClientCAs,
		ServerName:         tlsCfg.ServerName,
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
	// Note: GetCertificate and GetClientCertificate have different signatures
	// in crypto/tls vs pion/dtls, so they cannot be directly mapped.
	return cfg
}
