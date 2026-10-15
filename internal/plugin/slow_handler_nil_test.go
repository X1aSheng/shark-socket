package plugin

import (
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// TestSlowHandlerNilNextDoesNotPanic verifies that a nil next handler is
// tolerated regardless of the threshold. The disabled path (threshold <= 0)
// already substituted a no-op, but the enabled path called next directly, so
// NewSlowHandler(logger, time.Second, nil) returned a handler that panicked on
// its first message.
func TestSlowHandlerNilNextDoesNotPanic(t *testing.T) {
	h := NewSlowHandler(core.NopLogger(), time.Second, nil)
	if h == nil {
		t.Fatal("NewSlowHandler returned nil")
	}
	if err := h(&fakeSession{}, core.Message{Payload: []byte("x")}); err != nil {
		t.Fatalf("handler with nil next returned %v, want nil", err)
	}
}

// TestSlowHandlerDelegatesToNext verifies the wrapped handler is still invoked
// once the nil case is handled.
func TestSlowHandlerDelegatesToNext(t *testing.T) {
	called := 0
	next := func(core.Session, core.Message) error {
		called++
		return nil
	}
	h := NewSlowHandler(core.NopLogger(), time.Hour, next)
	if err := h(&fakeSession{}, core.Message{}); err != nil {
		t.Fatalf("handler returned %v", err)
	}
	if called != 1 {
		t.Fatalf("next called %d times, want 1", called)
	}
}
