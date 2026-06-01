package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"sync"
)

// CertCache loads and caches TLS certificates from disk, supporting
// hot-reload through GetCertificate.
type CertCache struct {
	mu           sync.RWMutex
	cert         *tls.Certificate
	certFile     string
	keyFile      string
	clientCAPool *x509.CertPool
	clientCAFile string
}

// NewCertCache creates a CertCache for the given certificate and key files.
func NewCertCache(certFile, keyFile string) *CertCache {
	return &CertCache{certFile: certFile, keyFile: keyFile}
}

// SetClientCA configures an optional client CA file for mTLS.
func (c *CertCache) SetClientCA(caFile string) {
	c.clientCAFile = caFile
}

// Load reads the certificate, key, and optional client CA from disk.
func (c *CertCache) Load() error {
	cert, err := tls.LoadX509KeyPair(c.certFile, c.keyFile)
	if err != nil {
		return fmt.Errorf("load cert: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cert = &cert

	if c.clientCAFile != "" {
		data, err := os.ReadFile(c.clientCAFile)
		if err != nil {
			return fmt.Errorf("read client CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(data) {
			return fmt.Errorf("parse client CA %q: no certificates found", c.clientCAFile)
		}
		c.clientCAPool = pool
	}
	return nil
}

// GetCertificate satisfies tls.Config.GetCertificate for hot-reload.
func (c *CertCache) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.cert == nil {
		return nil, fmt.Errorf("certificate not loaded")
	}
	return c.cert, nil
}

// GetClientCAPool returns the cached client CA pool for mTLS.
func (c *CertCache) GetClientCAPool() *x509.CertPool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.clientCAPool
}

// Files returns the file paths being watched.
func (c *CertCache) Files() []string {
	files := []string{c.certFile, c.keyFile}
	if c.clientCAFile != "" {
		files = append(files, c.clientCAFile)
	}
	return files
}
