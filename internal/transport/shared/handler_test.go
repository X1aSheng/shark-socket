package shared

import (
	"errors"
	"testing"

	"github.com/X1aSheng/shark-socket/internal/core"
)

func TestCallHandlerReturnsError(t *testing.T) {
	errBoom := errors.New("boom")
	err := CallHandler(func() error { return errBoom }, nil)
	if !errors.Is(err, errBoom) {
		t.Fatalf("CallHandler error = %v, want %v", err, errBoom)
	}
}

func TestCallHandlerRecoversPanic(t *testing.T) {
	err := CallHandler(func() error { panic("kaboom") }, nil)
	if !errors.Is(err, core.ErrHandlerPanic) {
		t.Fatalf("CallHandler panic error = %v, want %v", err, core.ErrHandlerPanic)
	}
}
