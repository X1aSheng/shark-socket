package utils

import (
	"sync"
	"testing"
)

func TestAtomicBool_DefaultFalse(t *testing.T) {
	var b AtomicBool
	if b.Get() {
		t.Fatal("default value should be false")
	}
}

func TestAtomicBool_SetGet(t *testing.T) {
	var b AtomicBool
	b.Set(true)
	if !b.Get() {
		t.Fatal("Get() = false after Set(true)")
	}
	b.Set(false)
	if b.Get() {
		t.Fatal("Get() = true after Set(false)")
	}
}

func TestAtomicBool_CompareAndSwap_Success(t *testing.T) {
	var b AtomicBool
	if !b.CompareAndSwap(false, true) {
		t.Fatal("CAS(false,true) should succeed when value is false")
	}
	if !b.Get() {
		t.Fatal("value should be true after CAS")
	}
	if !b.CompareAndSwap(true, false) {
		t.Fatal("CAS(true,false) should succeed when value is true")
	}
	if b.Get() {
		t.Fatal("value should be false after CAS")
	}
}

func TestAtomicBool_CompareAndSwap_Failure(t *testing.T) {
	var b AtomicBool
	if b.CompareAndSwap(true, false) {
		t.Fatal("CAS(true,false) should fail when value is false")
	}
	b.Set(true)
	if b.CompareAndSwap(false, true) {
		t.Fatal("CAS(false,true) should fail when value is true")
	}
}

func TestAtomicBool_Concurrent(t *testing.T) {
	var b AtomicBool
	var wg sync.WaitGroup
	const goroutines = 100
	trueCount := 0

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			if idx%2 == 0 {
				b.Set(true)
			} else {
				b.Set(false)
			}
			_ = b.Get()
		}(i)
	}
	wg.Wait()
	_ = trueCount
}
