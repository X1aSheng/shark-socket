package udp

import (
	"crypto/tls"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/pion/dtls/v3"
)

type Options struct {
	Addr          string
	Handler       core.Handler
	SessionTTL    time.Duration
	SweepInterval time.Duration
	MaxDatagram   int
	TLSConfig     *tls.Config
}

type Option func(*Options)

func defaultOptions() Options {
	return Options{
		Addr:          "127.0.0.1:18200",
		SessionTTL:    2 * time.Minute,
		SweepInterval: 30 * time.Second,
		MaxDatagram:   64 * 1024,
	}
}

func WithAddr(addr string) Option {
	return func(o *Options) {
		o.Addr = addr
	}
}

func WithHandler(handler core.Handler) Option {
	return func(o *Options) {
		o.Handler = handler
	}
}

func WithSessionTTL(ttl time.Duration) Option {
	return func(o *Options) {
		if ttl > 0 {
			o.SessionTTL = ttl
		}
	}
}

func WithSweepInterval(interval time.Duration) Option {
	return func(o *Options) {
		if interval > 0 {
			o.SweepInterval = interval
		}
	}
}

func WithDTLS(cfg *tls.Config) Option {
	return func(o *Options) {
		o.TLSConfig = cfg
	}
}

// dtlsConfig converts a *tls.Config to *dtls.Config.
// Maps the most important security-relevant fields.
func dtlsConfig(tlsCfg *tls.Config) *dtls.Config {
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
	// Note: GetCertificate, GetClientCertificate, and MinVersion have different
	// signatures/semantics in crypto/tls vs pion/dtls, so they cannot be directly
	// mapped. DTLS version negotiation is handled through cipher suite selection:
	// restrict CipherSuites to DTLS 1.3 suites if TLS 1.3 is required.
	return cfg
}
