package app

import (
	"strings"
	"testing"
	"time"
)

// TestHandlerMetricsReachTheExporter is D1's end-to-end acceptance: a real
// message through a real transport leaves a histogram and a counter on the
// Prometheus exporter the app installed, labelled by protocol. Unit tests on
// either side can pass while the wiring between them is missing -- this is the
// test that fails if the pool or a transport stops passing the metrics on.
func TestHandlerMetricsReachTheExporter(t *testing.T) {
	addr := freeAddr(t)
	a := startTestApp(t, tcpConfig(addr))

	conn := dial(t, addr)
	for i := 0; i < 3; i++ {
		if got, err := exchange(conn, "ping", 2*time.Second); err != nil || got != "ping" {
			t.Fatalf("echo %d: got %q, err %v", i+1, got, err)
		}
	}

	text := a.Metrics.ExportText()
	for _, want := range []string{
		`messages_total{protocol="tcp"} 3`,
		`handler_duration_seconds_count{protocol="tcp"} 3`,
		`handler_duration_seconds_bucket{protocol="tcp",le="+Inf"} 3`,
		"# TYPE handler_duration_seconds histogram",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("metrics export is missing %q:\n%s", want, text)
		}
	}
}

// TestHandlerMetricsDoNotLeakAcrossProtocols makes sure the label actually
// separates transports: a TCP message must not appear in the UDP series, or a
// per-protocol P99 is a blend of every transport in the gateway.
func TestHandlerMetricsDoNotLeakAcrossProtocols(t *testing.T) {
	cfg := Config{
		Protocols: []ProtocolConfig{
			{Name: "tcp", Addr: freeAddr(t)},
			{Name: "udp", Addr: freeAddr(t)},
		},
	}
	a := startTestApp(t, cfg)

	conn := dial(t, cfg.Protocols[0].Addr)
	if got, err := exchange(conn, "ping", 2*time.Second); err != nil || got != "ping" {
		t.Fatalf("tcp echo: got %q, err %v", got, err)
	}

	text := a.Metrics.ExportText()
	if !strings.Contains(text, `messages_total{protocol="tcp"} 1`) {
		t.Fatalf("tcp series missing:\n%s", text)
	}
	if strings.Contains(text, `messages_total{protocol="udp"}`) {
		t.Fatalf("udp series appeared without udp traffic:\n%s", text)
	}
}

// TestSessionMetricsStillReported guards the pre-existing metrics against the
// handler-boundary change: the exporter now keys series by a struct, and a
// regression there would silently drop the session gauges.
func TestSessionMetricsStillReported(t *testing.T) {
	addr := freeAddr(t)
	a := startTestApp(t, tcpConfig(addr))
	conn := dial(t, addr)
	if _, err := exchange(conn, "ping", 2*time.Second); err != nil {
		t.Fatalf("echo: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for {
		text := a.Metrics.ExportText()
		if strings.Contains(text, "sessions_accepted_total 1") && strings.Contains(text, "sessions_active 1") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("session metrics missing:\n%s", text)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
