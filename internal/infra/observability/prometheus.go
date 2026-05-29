package observability

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/X1aSheng/shark-socket-new/internal/core"
)

type PrometheusMetrics struct {
	mu         sync.RWMutex
	counters   map[string]promMetric
	gauges     map[string]promMetric
	histograms map[string]promHistogram
}

type promMetric struct {
	name   string
	labels []string
	value  float64
}

type promHistogram struct {
	name   string
	labels []string
	count  int
	sum    float64
}

func NewPrometheusMetrics() *PrometheusMetrics {
	return &PrometheusMetrics{
		counters:   make(map[string]promMetric),
		gauges:     make(map[string]promMetric),
		histograms: make(map[string]promHistogram),
	}
}

func (m *PrometheusMetrics) IncCounter(name string, labels ...string) {
	key := metricKey(name, labels...)
	m.mu.Lock()
	metric := m.counters[key]
	metric.name = name
	metric.labels = append(metric.labels[:0], labels...)
	metric.value++
	m.counters[key] = metric
	m.mu.Unlock()
}

func (m *PrometheusMetrics) SetGauge(name string, value float64, labels ...string) {
	key := metricKey(name, labels...)
	m.mu.Lock()
	m.gauges[key] = promMetric{name: name, labels: append([]string(nil), labels...), value: value}
	m.mu.Unlock()
}

func (m *PrometheusMetrics) ObserveHistogram(name string, value float64, labels ...string) {
	key := metricKey(name, labels...)
	m.mu.Lock()
	hist := m.histograms[key]
	hist.name = name
	hist.labels = append(hist.labels[:0], labels...)
	hist.count++
	hist.sum += value
	m.histograms[key] = hist
	m.mu.Unlock()
}

func (m *PrometheusMetrics) ExportText() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out strings.Builder
	writePromMetrics(&out, "counter", m.counters)
	writePromMetrics(&out, "gauge", m.gauges)
	writePromHistograms(&out, m.histograms)
	return out.String()
}

func (m *PrometheusMetrics) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("content-type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(m.ExportText()))
}

func writePromMetrics(out *strings.Builder, typ string, metrics map[string]promMetric) {
	keys := sortedKeys(metrics)
	declared := map[string]bool{}
	for _, key := range keys {
		metric := metrics[key]
		if !declared[metric.name] {
			fmt.Fprintf(out, "# TYPE %s %s\n", metric.name, typ)
			declared[metric.name] = true
		}
		fmt.Fprintf(out, "%s%s %s\n", metric.name, promLabels(metric.labels), formatFloat(metric.value))
	}
}

func writePromHistograms(out *strings.Builder, histograms map[string]promHistogram) {
	keys := sortedKeys(histograms)
	declared := map[string]bool{}
	for _, key := range keys {
		hist := histograms[key]
		if !declared[hist.name] {
			fmt.Fprintf(out, "# TYPE %s summary\n", hist.name)
			declared[hist.name] = true
		}
		labels := promLabels(hist.labels)
		fmt.Fprintf(out, "%s_count%s %d\n", hist.name, labels, hist.count)
		fmt.Fprintf(out, "%s_sum%s %s\n", hist.name, labels, formatFloat(hist.sum))
	}
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func promLabels(labels []string) string {
	if len(labels) == 0 {
		return ""
	}
	var pairs []string
	if len(labels)%2 == 0 {
		for i := 0; i < len(labels); i += 2 {
			pairs = append(pairs, fmt.Sprintf("%s=\"%s\"", sanitizeLabelName(labels[i]), escapeLabelValue(labels[i+1])))
		}
	} else {
		for i, value := range labels {
			pairs = append(pairs, fmt.Sprintf("label_%d=\"%s\"", i, escapeLabelValue(value)))
		}
	}
	return "{" + strings.Join(pairs, ",") + "}"
}

func sanitizeLabelName(name string) string {
	if name == "" {
		return "label"
	}
	var out strings.Builder
	for i, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (i > 0 && r >= '0' && r <= '9') {
			out.WriteRune(r)
			continue
		}
		out.WriteByte('_')
	}
	return out.String()
}

func escapeLabelValue(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\n", "\\n")
	return strings.ReplaceAll(value, "\"", "\\\"")
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

var _ core.Metrics = (*PrometheusMetrics)(nil)
var _ http.Handler = (*PrometheusMetrics)(nil)
