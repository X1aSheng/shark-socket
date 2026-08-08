package observability

import (
	"strings"
	"sync"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type MemoryMetrics struct {
	mu         sync.RWMutex
	counters   map[string]float64
	gauges     map[string]float64
	histograms map[string][]float64
}

func NewMemoryMetrics() *MemoryMetrics {
	return &MemoryMetrics{
		counters:   make(map[string]float64),
		gauges:     make(map[string]float64),
		histograms: make(map[string][]float64),
	}
}

func (m *MemoryMetrics) IncCounter(name string, labels ...string) {
	key := metricKey(name, labels...)
	m.mu.Lock()
	m.counters[key]++
	m.mu.Unlock()
}

func (m *MemoryMetrics) SetGauge(name string, value float64, labels ...string) {
	key := metricKey(name, labels...)
	m.mu.Lock()
	m.gauges[key] = value
	m.mu.Unlock()
}

// maxMemoryObservations bounds each histogram/log buffer so a long-running
// debug process that observes a hot metric does not grow without bound.
const maxMemoryObservations = 65536

func (m *MemoryMetrics) ObserveHistogram(name string, value float64, labels ...string) {
	key := metricKey(name, labels...)
	m.mu.Lock()
	hist := append(m.histograms[key], value)
	if len(hist) > maxMemoryObservations {
		hist = hist[len(hist)-maxMemoryObservations:]
	}
	m.histograms[key] = hist
	m.mu.Unlock()
}

func (m *MemoryMetrics) Counter(name string, labels ...string) float64 {
	key := metricKey(name, labels...)
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.counters[key]
}

func (m *MemoryMetrics) Gauge(name string, labels ...string) float64 {
	key := metricKey(name, labels...)
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.gauges[key]
}

func (m *MemoryMetrics) Histogram(name string, labels ...string) []float64 {
	key := metricKey(name, labels...)
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]float64(nil), m.histograms[key]...)
}

func metricKey(name string, labels ...string) string {
	if len(labels) == 0 {
		return name
	}
	return name + "{" + strings.Join(labels, ",") + "}"
}

var _ core.Metrics = (*MemoryMetrics)(nil)
