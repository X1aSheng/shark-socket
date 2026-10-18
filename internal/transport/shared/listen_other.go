//go:build !unix || solaris

package shared

import (
	"syscall"
)

// listenControl has no SO_REUSEPORT to set on this platform.
//
// Requesting it is an error rather than a silent no-op: an operator who asked
// for port sharing and got a plain listener would discover the difference only
// under load, with the second process refusing to start for a reason that has
// nothing to do with the configuration they wrote.
func listenControl(reusePort bool) func(network, address string, c syscall.RawConn) error {
	if !reusePort {
		return nil
	}
	return func(_, _ string, _ syscall.RawConn) error {
		return ErrReusePortUnsupported
	}
}
