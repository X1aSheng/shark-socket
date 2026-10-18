//go:build unix

package tcp

import (
	"io"
	"net"
)

// framedWrite sends a frame's two adjacent pieces (a length header before the
// payload, or a terminator after it) as one writev where the platform has it.
//
// net.Buffers.WriteTo falls back to sequential Write calls for writers without
// writev support (tls.Conn, for one), so the call is correct for any writer;
// this build just means the common *net.TCPConn case pays one syscall instead
// of two.
//
// The unix-only build is deliberate. The vector form still allocates the small
// piece (a 4-byte header cannot be handed to WriteTo from the stack), so on a
// platform without writev it would add an allocation and keep the two writes --
// a net loss. See framer_writev_other.go.
func framedWrite(w io.Writer, a, b []byte) error {
	// WriteTo has a pointer receiver, so the vector needs a variable.
	buffers := net.Buffers{a, b}
	_, err := buffers.WriteTo(w)
	return err
}
