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
	l.Debug("debug msg", "k1", "v1")
	l.Info("info msg", "k2", "v2")
	l.Warn("warn msg", "k3", "v3")
	l.Error("error msg", "k4", "v4")

	entries := l.Entries()
	if len(entries) != 4 {
		t.Fatalf("entries = %d, want 4", len(entries))
	}
	if entries[0].Level != "debug" || entries[1].Level != "info" ||
		entries[2].Level != "warn" || entries[3].Level != "error" {
		t.Fatalf("entry levels = %v %v %v %v",
			entries[0].Level, entries[1].Level, entries[2].Level, entries[3].Level)
	}
}
