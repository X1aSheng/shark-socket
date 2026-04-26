package metrics

import "sync/atomic"

// Collector is a global metrics collector that components can use to emit metrics
// without holding a direct reference to a Metrics instance.
var Collector atomic.Pointer[Metrics]

// Setup sets the global metrics collector.
func Setup(m Metrics) { Collector.Store(&m) }

// M returns the current Metrics instance, or a no-op if none is set.
func M() Metrics {
	if p := Collector.Load(); p != nil {
		return *p
	}
	return nopMetrics{}
}

// IncCounter increments a named counter.
func IncCounter(name string, labels ...string) {
	M().Counter(name, labels...).Inc(labels...)
}

// SetGauge sets a named gauge value.
func SetGauge(name string, v float64, labels ...string) {
	M().Gauge(name, labels...).Set(v, labels...)
}

// ObserveHistogram observes a value on a named histogram.
func ObserveHistogram(name string, v float64, labels ...string) {
	M().Histogram(name, labels...).Observe(v, labels...)
}
