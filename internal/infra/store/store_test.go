package store

import "testing"

func TestMemoryStoreSaveLoadDelete(t *testing.T) {
	s := NewMemory()
	if err := s.Save("sessions", "1", []byte("value")); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Load("sessions", "1")
	if err != nil || !ok || string(got) != "value" {
		t.Fatalf("got %q ok=%v err=%v", got, ok, err)
	}
	if err := s.Delete("sessions", "1"); err != nil {
		t.Fatal(err)
	}
	_, ok, err = s.Load("sessions", "1")
	if err != nil || ok {
		t.Fatal("value still present after delete")
	}
}
