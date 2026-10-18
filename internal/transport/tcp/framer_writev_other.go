//go:build !unix

package tcp

import "io"

// framedWrite is the two-write form used where writev is unavailable.
//
// See framer_writev_unix.go for why the vector form is not used here: without
// writev it would allocate the small piece and still perform two writes.
func framedWrite(w io.Writer, a, b []byte) error {
	if _, err := w.Write(a); err != nil {
		return err
	}
	_, err := w.Write(b)
	return err
}
