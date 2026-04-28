package defense

import (
	"testing"
	"time"
)

func TestShouldLog_FirstTime(t *testing.T) {
	s := NewLogSampler(100 * time.Millisecond)
	defer s.Close()
	ok, summary := s.ShouldLog("key1")
	if !ok {
		t.Fatal("expected first call to return true")
	}
	if summary != "" {
		t.Fatalf("expected empty summary on first call, got %q", summary)
	}
}

func TestShouldLog_SameKeyWithinWindow(t *testing.T) {
	s := NewLogSampler(500 * time.Millisecond)
	defer s.Close()
	s.ShouldLog("key1")

	ok, _ := s.ShouldLog("key1")
	if ok {
		t.Fatal("expected second call within window to return false")
	}
}

func TestShouldLog_AfterWindow_ReturnsTrueWithSummary(t *testing.T) {
	s := NewLogSampler(50 * time.Millisecond)
	defer s.Close()
	s.ShouldLog("key1")

	// Suppress a few calls.
	s.ShouldLog("key1")
	s.ShouldLog("key1")

	time.Sleep(60 * time.Millisecond)

	ok, summary := s.ShouldLog("key1")
	if !ok {
		t.Fatal("expected call after window to return true")
	}
	if summary == "" {
		t.Fatal("expected non-empty summary after window")
	}
}

func TestShouldLog_DifferentKeys(t *testing.T) {
	s := NewLogSampler(500 * time.Millisecond)
	defer s.Close()
	if ok, _ := s.ShouldLog("a"); !ok {
		t.Fatal("key a should be allowed")
	}
	if ok, _ := s.ShouldLog("b"); !ok {
		t.Fatal("key b should be allowed independently")
	}
}

func TestClean_RemovesStaleEntries(t *testing.T) {
	s := NewLogSampler(50 * time.Millisecond)
	defer s.Close()
	s.ShouldLog("stale")

	time.Sleep(120 * time.Millisecond)
	s.Clean()

	// After clean, the stale entry should be gone so a new call acts as first-time.
	ok, summary := s.ShouldLog("stale")
	if !ok {
		t.Fatal("expected true after stale entry cleaned")
	}
	if summary != "" {
		t.Fatalf("expected empty summary after clean, got %q", summary)
	}
}
