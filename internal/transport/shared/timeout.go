package shared

import (
	"errors"
	"net"
)

// IsTimeout reports whether err is a network timeout (e.g. a read-deadline
// expiry). Transports use it to distinguish a peer reclaimed by an idle
// timeout (a "fake" connection that went silent) from a clean disconnect, so
// reclaim events can be counted in metrics.
func IsTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
