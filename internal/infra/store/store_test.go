package store

import "testing"

func TestMemoryStoreSaveLoadDelete(t *testing.T) {
	s := NewMemory()
	s.Save("sessions", "1", []byte("value"))
	if got, ok := s.Load("sessions", "1"); !ok || string(got) != "value" {
		t.Fatalf("got %q ok=%v", got, ok)
	}
	s.Delete("sessions", "1")
	if _, ok := s.Load("sessions", "1"); ok {
		t.Fatal("value still present after delete")
	}
}
