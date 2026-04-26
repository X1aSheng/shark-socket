package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// newTestRegistry creates a fresh prometheus.Registry for each test so
// metrics do not collide across tests.
func newTestRegistry() *prometheus.Registry {
	return prometheus.NewRegistry()
}

func TestNewPrometheusMetrics_Creation(t *testing.T) {
	reg := newTestRegistry()
	m := NewPrometheusMetrics(reg)

	if m == nil {
		t.Fatal("expected non-nil PrometheusMetrics")
	}
	if len(m.counters) != 0 || len(m.gauges) != 0 || len(m.histograms) != 0 {
		t.Fatal("expected empty internal maps on creation")
	}
}

func TestCounter_BasicOperations(t *testing.T) {
	reg := newTestRegistry()
	m := NewPrometheusMetrics(reg)

	c := m.Counter("test_counter_total", "method")
	if c == nil {
		t.Fatal("expected non-nil CounterVec")
	}

	// Inc should register and increment.
	c.Inc("GET")
	count := testutil.ToFloat64(m.counters["test_counter_total"].inner)
	if count != 1 {
		t.Fatalf("expected counter value 1, got %v", count)
	}

	// Add should add the given delta.
	c.Add(4, "GET")
	count = testutil.ToFloat64(m.counters["test_counter_total"].inner)
	if count != 5 {
		t.Fatalf("expected counter value 5, got %v", count)
	}
}

func TestCounter_CachedOnSecondCall(t *testing.T) {
	reg := newTestRegistry()
	m := NewPrometheusMetrics(reg)

	c1 := m.Counter("cached_counter_total")
	c2 := m.Counter("cached_counter_total")

	if c1 != c2 {
		t.Fatal("expected same CounterVec instance for same name")
	}
}

func TestGauge_BasicOperations(t *testing.T) {
	reg := newTestRegistry()
	m := NewPrometheusMetrics(reg)

	g := m.Gauge("test_gauge", "instance")
	if g == nil {
		t.Fatal("expected non-nil GaugeVec")
	}

	// Set sets the gauge to an absolute value.
	g.Set(10, "a")
	val := testutil.ToFloat64(m.gauges["test_gauge"].inner)
	if val != 10 {
		t.Fatalf("expected gauge value 10, got %v", val)
	}

	// Inc increments by 1.
	g.Inc("a")
	val = testutil.ToFloat64(m.gauges["test_gauge"].inner)
	if val != 11 {
		t.Fatalf("expected gauge value 11, got %v", val)
	}

	// Dec decrements by 1.
	g.Dec("a")
	val = testutil.ToFloat64(m.gauges["test_gauge"].inner)
	if val != 10 {
		t.Fatalf("expected gauge value 10, got %v", val)
	}
}

func TestHistogram_BasicOperations(t *testing.T) {
	reg := newTestRegistry()
	m := NewPrometheusMetrics(reg)

	h := m.Histogram("test_histogram", "path")
	if h == nil {
		t.Fatal("expected non-nil HistogramVec")
	}

	// Observe should not panic and should record values.
	h.Observe(0.5, "/api")
	h.Observe(1.5, "/api")

	// Gather the histogram metric and verify sample count.
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather returned error: %v", err)
	}
	var found bool
	for _, mf := range mfs {
		if mf.GetName() == "test_histogram" {
			found = true
			for _, m := range mf.GetMetric() {
				hist := m.GetHistogram()
				if hist.GetSampleCount() != 2 {
					t.Fatalf("expected histogram sample count 2, got %d", hist.GetSampleCount())
				}
				if hist.GetSampleSum() != 2.0 {
					t.Fatalf("expected histogram sample sum 2.0, got %v", hist.GetSampleSum())
				}
			}
		}
	}
	if !found {
		t.Fatal("test_histogram not found in gathered metrics")
	}
}

func TestTimer_BasicOperations(t *testing.T) {
	reg := newTestRegistry()
	m := NewPrometheusMetrics(reg)

	tv := m.Timer("test_timer_seconds", "op")
	if tv == nil {
		t.Fatal("expected non-nil TimerVec")
	}

	// ObserveDuration with a start time in the past.
	start := time.Now().Add(-100 * time.Millisecond)
	tv.ObserveDuration(start, "query")
	// If this completes without panicking the timer works.
}

func TestNopMetrics_NoPanic(t *testing.T) {
	m := NopMetrics()

	c := m.Counter("any")
	c.Inc("label")
	c.Add(1, "label")

	g := m.Gauge("any")
	g.Set(1, "label")
	g.Inc("label")
	g.Dec("label")

	h := m.Histogram("any")
	h.Observe(3.14, "label")

	tv := m.Timer("any")
	tv.ObserveDuration(time.Now(), "label")
}
