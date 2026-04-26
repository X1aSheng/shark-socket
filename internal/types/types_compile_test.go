package types

import "testing"

// Compile-time interface satisfaction checks.
func TestRawSessionAlias(t *testing.T) {
	// This test verifies RawSession = Session[[]byte] compiles correctly.
	var _ RawSession = (Session[[]byte])(nil)
	t.Log("RawSession = Session[[]byte] compile check passed")
}

func TestRawHandlerAlias(t *testing.T) {
	// RawHandler = MessageHandler[[]byte] compile check.
	var f RawHandler = func(sess Session[[]byte], msg Message[[]byte]) error {
		return nil
	}
	if f == nil {
		t.Error("handler should not be nil")
	}
}
