package observability

import "testing"

func TestMemoryMetrics(t *testing.T) {
	m := NewMemoryMetrics()
	m.IncCounter("messages", "tcp")
	m.SetGauge("sessions", 3, "active")
	m.ObserveHistogram("latency", 1.5, "tcp")
	if got := m.Counter("messages", "tcp"); got != 1 {
		t.Fatalf("counter = %v, want 1", got)
	}
	if got := m.Gauge("sessions", "active"); got != 3 {
		t.Fatalf("gauge = %v, want 3", got)
	}
	if got := m.Histogram("latency", "tcp"); len(got) != 1 || got[0] != 1.5 {
		t.Fatalf("histogram = %#v", got)
	}
}

func TestMemoryLogger(t *testing.T) {
	l := NewMemoryLogger()
	l.Info("hello", "k", "v")
	entries := l.Entries()
	if len(entries) != 1 || entries[0].Level != "info" || entries[0].Msg != "hello" {
		t.Fatalf("entries = %#v", entries)
	}
}
