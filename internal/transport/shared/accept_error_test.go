package shared

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"
)

func TestClassifyAcceptError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want AcceptErrorKind
	}{
		{"closed", net.ErrClosed, AcceptErrorClosed},
		{"wrapped closed", fmt.Errorf("proxy listener: %w", net.ErrClosed), AcceptErrorClosed},
		{"emfile", &net.OpError{Op: "accept", Err: os.NewSyscallError("accept", errEMFILE{})}, AcceptErrorResource},
		{"bare emfile", errEMFILE{}, AcceptErrorResource},
		{"temporary", errors.New("connection reset by peer"), AcceptErrorTemporary},
		{"nil", nil, AcceptErrorTemporary},
	}
	for _, c := range cases {
		if got := ClassifyAcceptError(c.err); got != c.want {
			t.Errorf("%s: ClassifyAcceptError = %v, want %v", c.name, got, c.want)
		}
	}
}

// errEMFILE stands in for the kernel's errno without depending on which errno
// the test machine would actually produce. It is a real EMFILE: Is unwraps to
// syscall.EMFILE exactly as os.NewSyscallError's chain does.
type errEMFILE struct{}

func (errEMFILE) Error() string { return "too many open files" }
func (errEMFILE) Unwrap() error { return syscall.EMFILE }

func TestAcceptErrorKindString(t *testing.T) {
	cases := map[AcceptErrorKind]string{
		AcceptErrorClosed:    "closed",
		AcceptErrorResource:  "resource",
		AcceptErrorTemporary: "temporary",
		AcceptErrorKind(99):  "unknown",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", k, got, want)
		}
	}
}
