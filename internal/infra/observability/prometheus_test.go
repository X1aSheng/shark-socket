package observability

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/X1aSheng/shark-socket/internal/core"
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
	assertTextContains(t, text, "# TYPE shark_handler_duration_seconds histogram")
	assertTextContains(t, text, `shark_handler_duration_seconds_count{protocol="tcp"} 2`)
	assertTextContains(t, text, `shark_handler_duration_seconds_sum{protocol="tcp"} 0.5`)
	// 0.2 and 0.3 fall into the 0.25 and 0.5 buckets, so the cumulative count
	// below 0.1 must stay 0 and the +Inf bucket must equal the total.
	assertTextContains(t, text, `shark_handler_duration_seconds_bucket{protocol="tcp",le="0.1"} 0`)
	assertTextContains(t, text, `shark_handler_duration_seconds_bucket{protocol="tcp",le="0.25"} 1`)
	assertTextContains(t, text, `shark_handler_duration_seconds_bucket{protocol="tcp",le="0.5"} 2`)
	assertTextContains(t, text, `shark_handler_duration_seconds_bucket{protocol="tcp",le="+Inf"} 2`)
}

// TestPrometheusHistogramQuantileIsComputable is D2's acceptance: the exported
// buckets must let PromQL's histogram_quantile() answer. It reimplements the
// function's core step (find the bucket that crosses the rank, interpolate
// linearly inside it) over the parsed output, because there is no Prometheus in
// the test environment. A summary export -- count and sum only -- cannot pass
// this: there is no bucket to find.
func TestPrometheusHistogramQuantileIsComputable(t *testing.T) {
	metrics := NewPrometheusMetrics()
	// 100 observations: 90 at 1ms, 9 at 10ms, 1 at 100ms. P99 rank = 99.01.
	for i := 0; i < 90; i++ {
		metrics.ObserveHistogram("handler_duration_seconds", 0.001)
	}
	for i := 0; i < 9; i++ {
		metrics.ObserveHistogram("handler_duration_seconds", 0.01)
	}
	metrics.ObserveHistogram("handler_duration_seconds", 0.1)

	quantile := histogramQuantileFromText(t, metrics.ExportText(), "handler_duration_seconds", 0.99)
	if quantile <= 0 {
		t.Fatalf("P99 = %v, want a positive interpolated value", quantile)
	}
	// The rank falls inside (0.01, 0.025], so the interpolated value must be in
	// that interval -- loose enough for the bucket ladder, tight enough to fail
	// if the export were a summary or the buckets were cumulative in the wrong
	// order.
	if quantile < 0.01 || quantile > 0.025 {
		t.Fatalf("P99 = %v, want it inside (0.01, 0.025]", quantile)
	}
}

func TestPrometheusHistogramImplicitInfBucket(t *testing.T) {
	metrics := NewPrometheusMetrics()
	metrics.ObserveHistogram("h_seconds", 0.001)
	// Above the highest bound: counted in count/sum and only in +Inf.
	metrics.ObserveHistogram("h_seconds", 3600)

	text := metrics.ExportText()
	assertTextContains(t, text, `h_seconds_bucket{le="10"} 1`)
	assertTextContains(t, text, `h_seconds_bucket{le="+Inf"} 2`)
	assertTextContains(t, text, `h_seconds_count 2`)
	assertTextContains(t, text, `h_seconds_sum 3600.001`)
}

func TestRegisterHistogramUsesCustomBuckets(t *testing.T) {
	metrics := NewPrometheusMetrics()
	if err := metrics.RegisterHistogram("payload_bytes", []float64{1024, 4096, 65536}); err != nil {
		t.Fatalf("RegisterHistogram: %v", err)
	}
	metrics.ObserveHistogram("payload_bytes", 2000)

	text := metrics.ExportText()
	assertTextContains(t, text, `payload_bytes_bucket{le="1024"} 0`)
	assertTextContains(t, text, `payload_bytes_bucket{le="4096"} 1`)
	assertTextContains(t, text, `payload_bytes_bucket{le="65536"} 1`)
	if strings.Contains(text, `payload_bytes_bucket{le="10"}`) {
		t.Fatal("registered buckets were replaced by the seconds default")
	}
}

func TestRegisterHistogramRejectsBadInput(t *testing.T) {
	metrics := NewPrometheusMetrics()
	if err := metrics.RegisterHistogram("", []float64{1}); err == nil {
		t.Fatal("empty name accepted")
	}
	if err := metrics.RegisterHistogram("x_seconds", nil); err == nil {
		t.Fatal("empty bucket list accepted")
	}
	if err := metrics.RegisterHistogram("x_seconds", []float64{1, 1}); err == nil {
		t.Fatal("duplicate bucket accepted")
	}

	metrics.ObserveHistogram("y_seconds", 0.1)
	if err := metrics.RegisterHistogram("y_seconds", []float64{0.5}); err == nil {
		t.Fatal("registration after the first observation must fail: the counts already recorded use the old ladder")
	}
	// A different name that merely shares a prefix is not "already observed".
	if err := metrics.RegisterHistogram("y_seconds_extra", []float64{0.5}); err != nil {
		t.Fatalf("prefix collision rejected a distinct metric: %v", err)
	}
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

// TestSharedCounterIsSummableAcrossProtocols is the reason the shared counters
// carry a label instead of a per-transport name: one series per protocol keeps
// the total one expression away (`sum`), whereas a name prefix can only be
// aggregated with a regex over metric names (D3).
func TestSharedCounterIsSummableAcrossProtocols(t *testing.T) {
	metrics := NewPrometheusMetrics()
	metrics.IncCounter("sessions_rejected_total", core.ProtocolLabel(core.ProtocolTCP)...)
	metrics.IncCounter("sessions_rejected_total", core.ProtocolLabel(core.ProtocolTCP)...)
	metrics.IncCounter("sessions_rejected_total", core.ProtocolLabel(core.ProtocolUDP)...)

	text := metrics.ExportText()
	assertTextContains(t, text, `sessions_rejected_total{protocol="tcp"} 2`)
	assertTextContains(t, text, `sessions_rejected_total{protocol="udp"} 1`)
	// One family, one TYPE line: the per-protocol series must not be mistaken
	// for separate metric names.
	if got := strings.Count(text, "# TYPE sessions_rejected_total"); got != 1 {
		t.Fatalf("# TYPE lines = %d, want 1:\n%s", got, text)
	}
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

// histogramQuantileFromText extracts the buckets of name from an exposition and
// evaluates quantile over them. It is deliberately small: the point is to prove
// the exported shape is sufficient, not to reimplement PromQL.
func histogramQuantileFromText(t *testing.T, text, name string, quantile float64) float64 {
	t.Helper()
	var bounds []float64
	var counts []float64
	var total float64
	for _, line := range strings.Split(text, "\n") {
		prefix := name + "_bucket{le=\""
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		leEnd := strings.Index(line, "\"}")
		if leEnd < len(prefix) {
			continue
		}
		le := line[len(prefix):leEnd]
		valueEnd := strings.LastIndex(line, " ")
		value, err := strconv.ParseFloat(strings.TrimSpace(line[valueEnd+1:]), 64)
		if err != nil {
			t.Fatalf("parse bucket value in %q: %v", line, err)
		}
		if le == "+Inf" {
			total = value
			continue
		}
		bound, err := strconv.ParseFloat(le, 64)
		if err != nil {
			t.Fatalf("parse le in %q: %v", line, err)
		}
		bounds = append(bounds, bound)
		counts = append(counts, value)
	}
	if total == 0 {
		t.Fatalf("no +Inf bucket for %s in:\n%s", name, text)
	}
	rank := quantile * total
	for i, count := range counts {
		if count < rank {
			continue
		}
		upper := bounds[i]
		lower := 0.0
		lowerCount := 0.0
		if i > 0 {
			lower = bounds[i-1]
			lowerCount = counts[i-1]
		}
		if count == lowerCount {
			return upper
		}
		return lower + (upper-lower)*((rank-lowerCount)/(count-lowerCount))
	}
	return 0
}

func assertTextContains(t *testing.T, text, want string) {
	t.Helper()
	if !strings.Contains(text, want) {
		t.Fatalf("expected %q in:\n%s", want, text)
	}
}

// TestSeriesKeyDoesNotAliasDifferentLabels is the guard on the struct key's
// fast path: a label pair with an empty name must not fold into the "no
// labels" series, and label names must participate in identity.
func TestSeriesKeyDoesNotAliasDifferentLabels(t *testing.T) {
	metrics := NewPrometheusMetrics()
	metrics.IncCounter("c_total")
	metrics.IncCounter("c_total", "", "")
	metrics.IncCounter("c_total", "a", "x")
	metrics.IncCounter("c_total", "b", "x")

	text := metrics.ExportText()
	assertTextContains(t, text, "c_total 1\n")
	// An empty label name is sanitised to "label" by promLabels; the point
	// is that it is a separate series from the unlabelled one.
	assertTextContains(t, text, `c_total{label=""} 1`)
	assertTextContains(t, text, `c_total{a="x"} 1`)
	assertTextContains(t, text, `c_total{b="x"} 1`)
}
