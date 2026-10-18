//go:build unix && !solaris

package shared

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// listenControl returns the RawConn hook that applies SO_REUSEPORT, or nil when
// reuse is not requested (nil is exactly what net.Listen passes).
//
// The hook is applied to the listening socket before bind, which is the only
// point where SO_REUSEPORT takes effect: setting it on an accepted connection
// does nothing. Both sockets must set it, otherwise the second bind still fails
// with "address already in use".
//
// The constant comes from x/sys/unix rather than syscall: the value differs per
// platform (0xf on Linux, 0x200 on Darwin) and syscall does not export
// SO_REUSEPORT on Linux at all.
func listenControl(reusePort bool) func(network, address string, c syscall.RawConn) error {
	if !reusePort {
		return nil
	}
	return func(_, _ string, c syscall.RawConn) error {
		var sockErr error
		if err := c.Control(func(fd uintptr) {
			sockErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
		}); err != nil {
			return err
		}
		return sockErr
	}
}
