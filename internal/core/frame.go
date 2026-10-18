package core

import "sync/atomic"

// SharedFrame is an immutable payload handed to several sessions at once.
//
// Send copies by contract: the caller may release its buffer as soon as Send
// returns, so broadcasting to N sessions costs N copies of the same bytes. A
// shared frame replaces that with one copy whose lifetime is managed by
// reference counting: every consumer releases its reference once it has written
// the payload, and the bytes become collectable when the last one has.
//
// A frame whose references are never all released pins its payload for the
// process lifetime, so every path that enqueues a frame must also release it --
// including the failure paths (queue full, session closed before the write).
type SharedFrame struct {
	data []byte
	refs atomic.Int64
}

// NewSharedFrame copies data and prepares refs references. A non-positive refs
// is treated as 1: a frame nobody consumes would never be released.
func NewSharedFrame(data []byte, refs int) *SharedFrame {
	if refs < 1 {
		refs = 1
	}
	f := &SharedFrame{data: append([]byte(nil), data...)}
	f.refs.Store(int64(refs))
	return f
}

// Payload returns the frame bytes. Consumers must not mutate them.
func (f *SharedFrame) Payload() []byte { return f.data }

// Release drops one reference.
func (f *SharedFrame) Release() { f.refs.Add(-1) }

// Refs reports outstanding references. It exists for tests and diagnostics: at
// steady state it must reach 0, otherwise the payload is pinned.
func (f *SharedFrame) Refs() int64 { return f.refs.Load() }
