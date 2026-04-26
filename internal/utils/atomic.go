package utils

import "sync/atomic"

// AtomicBool provides an atomic boolean.
type AtomicBool struct {
	val atomic.Int32
}

func (b *AtomicBool) Set(v bool) {
	if v {
		b.val.Store(1)
	} else {
		b.val.Store(0)
	}
}

func (b *AtomicBool) Get() bool {
	return b.val.Load() == 1
}

func (b *AtomicBool) CompareAndSwap(old, new bool) bool {
	var o, n int32
	if old {
		o = 1
	}
	if new {
		n = 1
	}
	return b.val.CompareAndSwap(o, n)
}
