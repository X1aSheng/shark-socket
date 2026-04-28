package metrics

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// CounterVec is a counter metric that can be incremented with labels.
type CounterVec interface {
	Inc(labels ...string)
	Add(n float64, labels ...string)
}

// GaugeVec is a gauge metric that can be set/incremented/decremented with labels.
type GaugeVec interface {
	Set(v float64, labels ...string)
	Inc(labels ...string)
	Dec(labels ...string)
}

// HistogramVec observes values with labels.
type HistogramVec interface {
	Observe(v float64, labels ...string)
}

// TimerVec measures durations.
type TimerVec interface {
	ObserveDuration(start time.Time, labels ...string)
}

// Metrics provides the metrics collection interface.
type Metrics interface {
	Counter(name string, labels ...string) CounterVec
	Gauge(name string, labels ...string) GaugeVec
	Histogram(name string, labels ...string) HistogramVec
	Timer(name string, labels ...string) TimerVec
}

// PrometheusMetrics implements Metrics using Prometheus.
type PrometheusMetrics struct {
	reg        prometheus.Registerer
	mu         sync.RWMutex
	counters   map[string]*promCounterVec
	gauges     map[string]*promGaugeVec
	histograms map[string]*promHistogramVec
}

// NewPrometheusMetrics creates a new PrometheusMetrics with the given registerer.
func NewPrometheusMetrics(reg prometheus.Registerer) *PrometheusMetrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	return &PrometheusMetrics{
		reg:        reg,
		counters:   make(map[string]*promCounterVec),
		gauges:     make(map[string]*promGaugeVec),
		histograms: make(map[string]*promHistogramVec),
	}
}

func (m *PrometheusMetrics) Counter(name string, labels ...string) CounterVec {
	m.mu.RLock()
	if c, ok := m.counters[name]; ok {
		m.mu.RUnlock()
		return c
	}
	m.mu.RUnlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.counters[name]; ok {
		return c
	}
	cv := promauto.With(m.reg).NewCounterVec(prometheus.CounterOpts{Name: name}, labels)
	c := &promCounterVec{inner: cv}
	m.counters[name] = c
	return c
}

func (m *PrometheusMetrics) Gauge(name string, labels ...string) GaugeVec {
	m.mu.RLock()
	if g, ok := m.gauges[name]; ok {
		m.mu.RUnlock()
		return g
	}
	m.mu.RUnlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if g, ok := m.gauges[name]; ok {
		return g
	}
	gv := promauto.With(m.reg).NewGaugeVec(prometheus.GaugeOpts{Name: name}, labels)
	g := &promGaugeVec{inner: gv}
	m.gauges[name] = g
	return g
}

func (m *PrometheusMetrics) Histogram(name string, labels ...string) HistogramVec {
	m.mu.RLock()
	if h, ok := m.histograms[name]; ok {
		m.mu.RUnlock()
		return h
	}
	m.mu.RUnlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if h, ok := m.histograms[name]; ok {
		return h
	}
	hv := promauto.With(m.reg).NewHistogramVec(prometheus.HistogramOpts{Name: name}, labels)
	h := &promHistogramVec{inner: hv}
	m.histograms[name] = h
	return h
}

func (m *PrometheusMetrics) Timer(name string, labels ...string) TimerVec {
	m.mu.RLock()
	if h, ok := m.histograms[name]; ok {
		m.mu.RUnlock()
		return &promTimerVec{inner: h.inner}
	}
	m.mu.RUnlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if h, ok := m.histograms[name]; ok {
		return &promTimerVec{inner: h.inner}
	}
	hv := promauto.With(m.reg).NewHistogramVec(prometheus.HistogramOpts{Name: name}, labels)
	h := &promHistogramVec{inner: hv}
	m.histograms[name] = h
	return &promTimerVec{inner: hv}
}

type promCounterVec struct {
	inner *prometheus.CounterVec
}

func (c *promCounterVec) Inc(labels ...string)            { c.inner.WithLabelValues(labels...).Inc() }
func (c *promCounterVec) Add(n float64, labels ...string) { c.inner.WithLabelValues(labels...).Add(n) }

type promGaugeVec struct {
	inner *prometheus.GaugeVec
}

func (g *promGaugeVec) Set(v float64, labels ...string) { g.inner.WithLabelValues(labels...).Set(v) }
func (g *promGaugeVec) Inc(labels ...string)            { g.inner.WithLabelValues(labels...).Inc() }
func (g *promGaugeVec) Dec(labels ...string)            { g.inner.WithLabelValues(labels...).Dec() }

type promHistogramVec struct {
	inner *prometheus.HistogramVec
}

func (h *promHistogramVec) Observe(v float64, labels ...string) {
	h.inner.WithLabelValues(labels...).Observe(v)
}

type promTimerVec struct {
	inner *prometheus.HistogramVec
}

func (t *promTimerVec) ObserveDuration(start time.Time, labels ...string) {
	t.inner.WithLabelValues(labels...).Observe(time.Since(start).Seconds())
}

// RegisterDefaultMetrics pre-registers all shark_* metrics.
func RegisterDefaultMetrics(m Metrics) {
	// Connection metrics
	m.Counter("shark_connections_total", "protocol", "direction")
	m.Counter("shark_connection_errors_total", "protocol", "error_type")
	m.Counter("shark_rejected_connections_total", "reason", "protocol")

	// Session metrics
	m.Counter("shark_sessions_total", "protocol", "session_state")
	m.Gauge("shark_sessions_active", "protocol")

	// Message metrics
	m.Counter("shark_messages_total", "protocol", "message_type", "direction")
	m.Counter("shark_errors_total", "protocol", "error_kind")

	// Worker metrics
	m.Counter("shark_worker_panics_total", "protocol")
	m.Gauge("shark_worker_queue_depth", "protocol")

	// Session management
	m.Counter("shark_session_lru_evictions_total", "protocol")
	m.Counter("shark_write_queue_full_total", "protocol")

	// Defense metrics
	m.Counter("shark_autoban_total", "reason")
	m.Gauge("shark_overloaded", "protocol")

	// Buffer pool metrics
	m.Counter("shark_bufferpool_hits_total", "size_level")
	m.Counter("shark_bufferpool_misses_total", "size_level")

	// Network monitoring
	m.Gauge("shark_fd_usage_ratio")

	// Histograms
	m.Histogram("shark_message_bytes", "protocol", "direction")
	m.Histogram("shark_message_duration_seconds", "protocol", "operation")
	m.Histogram("shark_plugin_duration_seconds", "plugin", "operation")
}

// SessionState labels for session metrics.
const (
	SessionStateActive  = "active"
	SessionStateClosed  = "closed"
	SessionStateTimeout = "timeout"
	SessionStateError   = "error"
)

// Direction labels.
const (
	DirectionIn  = "in"
	DirectionOut = "out"
)

// Operation labels for histogram.
const (
	OperationEncode  = "encode"
	OperationDecode  = "decode"
	OperationSend    = "send"
	OperationReceive = "receive"
)

// NopMetrics returns a no-op metrics implementation for testing.
func NopMetrics() Metrics { return nopMetrics{} }

type nopMetrics struct{}

func (nopMetrics) Counter(_ string, _ ...string) CounterVec     { return nopCounter{} }
func (nopMetrics) Gauge(_ string, _ ...string) GaugeVec         { return nopGauge{} }
func (nopMetrics) Histogram(_ string, _ ...string) HistogramVec { return nopHistogram{} }
func (nopMetrics) Timer(_ string, _ ...string) TimerVec         { return nopTimer{} }

type nopCounter struct{}
type nopGauge struct{}
type nopHistogram struct{}
type nopTimer struct{}

func (nopCounter) Inc(...string)                      {}
func (nopCounter) Add(float64, ...string)             {}
func (nopGauge) Set(float64, ...string)               {}
func (nopGauge) Inc(...string)                        {}
func (nopGauge) Dec(...string)                        {}
func (nopHistogram) Observe(float64, ...string)       {}
func (nopTimer) ObserveDuration(time.Time, ...string) {}

var (
	_ Metrics    = (*PrometheusMetrics)(nil)
	_ CounterVec = (*promCounterVec)(nil)
	_ GaugeVec   = (*promGaugeVec)(nil)
	_ TimerVec   = (*promTimerVec)(nil)
)

// NopContext is a convenience for passing a background context.
var NopContext = context.Background()
