package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrometheusMetricsExportText(t *testing.T) {
	metrics := NewPrometheusMetrics()
	metrics.IncCounter("shark_messages_total", "protocol", "tcp")
	metrics.IncCounter("shark_messages_total", "protocol", "tcp")
	metrics.SetGauge("shark_sessions", 3, "state", "active")
	metrics.ObserveHistogram("shark_handler_duration_seconds", 0.2, "protocol", "tcp")
	metrics.ObserveHistogram("shark_handler_duration_seconds", 0.3, "protocol", "tcp")

	text := metrics.ExportText()
	assertTextContains(t, text, "# TYPE shark_messages_total counter")
	assertTextContains(t, text, `shark_messages_total{protocol="tcp"} 2`)
	assertTextContains(t, text, "# TYPE shark_sessions gauge")
	assertTextContains(t, text, `shark_sessions{state="active"} 3`)
	assertTextContains(t, text, "# TYPE shark_handler_duration_seconds summary")
	assertTextContains(t, text, `shark_handler_duration_seconds_count{protocol="tcp"} 2`)
	assertTextContains(t, text, `shark_handler_duration_seconds_sum{protocol="tcp"} 0.5`)
}

func TestPrometheusMetricsEscapesLabels(t *testing.T) {
	metrics := NewPrometheusMetrics()
	metrics.SetGauge("shark_info", 1, "bad-label", "line\n\"quoted\"\\tail")

	text := metrics.ExportText()
	want := `shark_info{bad_label="` + escapeLabelValue("line\n\"quoted\"\\tail") + `"} 1`
	assertTextContains(t, text, want)
}

func TestPrometheusMetricsSupportsValueOnlyLabels(t *testing.T) {
	metrics := NewPrometheusMetrics()
	metrics.IncCounter("shark_messages_total", "tcp")

	text := metrics.ExportText()
	assertTextContains(t, text, `shark_messages_total{label_0="tcp"} 1`)
}

func TestPrometheusMetricsServeHTTP(t *testing.T) {
	metrics := NewPrometheusMetrics()
	metrics.SetGauge("shark_sessions", 1)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()

	metrics.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("content-type"); !strings.Contains(got, "text/plain") {
		t.Fatalf("content-type = %q, want text/plain", got)
	}
	assertTextContains(t, rec.Body.String(), "shark_sessions 1")
}

func assertTextContains(t *testing.T, text, want string) {
	t.Helper()
	if !strings.Contains(text, want) {
		t.Fatalf("expected %q in:\n%s", want, text)
	}
}
