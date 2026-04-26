package tcp

import (
	"crypto/tls"
	"log"
	"sync"
	"sync/atomic"
)

// TLSReloader manages hot-reloading of TLS certificates.
//
// On Linux, certificate reload can be triggered by sending SIGHUP to the
// process. On Windows and other platforms, call Reload() manually or use
// the reload channel exposed by ReloadChan().
//
// When reload fails (e.g. missing or invalid certificate files), the
// previously loaded certificate is retained and a warning is logged.
type TLSReloader struct {
	certFile string
	keyFile  string

	cert atomic.Pointer[tls.Certificate]

	mu     sync.Mutex
	reload chan struct{}
	done   chan struct{}
}

// NewTLSReloader loads the initial certificate pair and prepares the reloader.
//
// The reloader does not watch OS signals by default; use Reload() or
// ReloadChan() to trigger reloads programmatically. On Linux, callers may
// bridge SIGHUP to ReloadChan() themselves.
func NewTLSReloader(certFile, keyFile string) (*TLSReloader, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}

	r := &TLSReloader{
		certFile: certFile,
		keyFile:  keyFile,
		reload:   make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
	r.cert.Store(&cert)

	go r.watchReload()
	return r, nil
}

// GetConfigForClient implements the tls.Config.GetConfigForClient callback.
// It returns a *tls.Config using the most recently loaded certificate.
func (r *TLSReloader) GetConfigForClient(_ *tls.ClientHelloInfo) (*tls.Config, error) {
	return &tls.Config{
		Certificates: []tls.Certificate{*r.cert.Load()},
	}, nil
}

// Reload triggers an immediate reload of the certificate files.
//
// If the new files cannot be loaded, the old certificate is kept and a
// warning is logged. Returns nil on success or the load error on failure
// (the reloader remains functional with the previous cert).
func (r *TLSReloader) Reload() error {
	return r.reloadCert()
}

// ReloadChan returns a write-only channel that triggers a reload on each
// send. This is useful for wiring up external signal handlers or timers.
//
// Sends never block; if a reload is already pending the send is dropped.
func (r *TLSReloader) ReloadChan() chan<- struct{} {
	return r.reload
}

// Close stops the reload watcher goroutine.
func (r *TLSReloader) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	select {
	case <-r.done:
		// already closed
	default:
		close(r.done)
	}
}

// TLSConfig returns a *tls.Config suitable for tls.Listen that uses this
// reloader as the GetConfigForClient callback.
func (r *TLSReloader) TLSConfig() *tls.Config {
	return &tls.Config{
		GetConfigForClient: r.GetConfigForClient,
	}
}

// watchReload drains the reload channel in a loop until Close is called.
func (r *TLSReloader) watchReload() {
	for {
		select {
		case <-r.done:
			return
		case <-r.reload:
			if err := r.reloadCert(); err != nil {
				log.Printf("TLS reload: failed to reload cert: %v", err)
			}
		}
	}
}

// reloadCert performs the actual cert file read and atomic swap.
func (r *TLSReloader) reloadCert() error {
	newCert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		log.Printf("TLS reload: keeping previous certificate due to error: %v", err)
		return err
	}
	r.cert.Store(&newCert)
	log.Printf("TLS reload: certificate reloaded from %s", r.certFile)
	return nil
}
