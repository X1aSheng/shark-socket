package observability

import "testing"

// BenchmarkObserveHistogram measures the per-message cost the handler boundary
// pays when the Prometheus exporter is configured. The numbers are what justify
// (or refute) instrumenting the hot path at all, so they are kept next to the
// exporter rather than in a separate report.
func BenchmarkObserveHistogram(b *testing.B) {
	metrics := NewPrometheusMetrics()
	labels := []string{"protocol", "tcp"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		metrics.ObserveHistogram("handler_duration_seconds", 0.000123, labels...)
	}
}

func BenchmarkIncCounter(b *testing.B) {
	metrics := NewPrometheusMetrics()
	labels := []string{"protocol", "tcp"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		metrics.IncCounter("messages_total", labels...)
	}
}

// BenchmarkObserveHistogramParallel is the realistic shape: eight transports
// and all cores reporting through one exporter.
func BenchmarkObserveHistogramParallel(b *testing.B) {
	metrics := NewPrometheusMetrics()
	labels := []string{"protocol", "tcp"}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			metrics.ObserveHistogram("handler_duration_seconds", 0.000123, labels...)
		}
	})
}

// BenchmarkExportText measures a scrape of a populated exporter: 12 label sets,
// 22 buckets each. Prometheus scrapes this on every collection interval, and a
// slow export holds the exporter's read lock against the write path.
func BenchmarkExportText(b *testing.B) {
	metrics := NewPrometheusMetrics()
	for _, protocol := range []string{"tcp", "udp", "http", "websocket", "coap", "quic", "grpc-web"} {
		for i := 0; i < 100; i++ {
			metrics.ObserveHistogram("handler_duration_seconds", 0.000123, "protocol", protocol)
		}
		metrics.IncCounter("messages_total", "protocol", protocol)
		metrics.SetGauge("sessions_active", 3)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if text := metrics.ExportText(); len(text) == 0 {
			b.Fatal("empty export")
		}
	}
}
