package tcp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// TestLengthPrefixFramerRejectsHugePrefix exercises the uint32-vs-int size
// guard: a prefix >= 2^31 must be rejected as too large on every platform
// (on 32-bit builds a naive int() conversion would go negative and panic in
// make([]byte, negative) before this guard).
func TestLengthPrefixFramerRejectsHugePrefix(t *testing.T) {
	var buf bytes.Buffer
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, 0xFFFFFFFF) // 4 GiB frame
	buf.Write(header)

	framer := LengthPrefixFramer{MaxFrameBytes: 1024}
	_, err := framer.ReadFrame(&buf)
	if !errors.Is(err, core.ErrFrameTooLarge) {
		t.Fatalf("ReadFrame error = %v, want %v", err, core.ErrFrameTooLarge)
	}
}
