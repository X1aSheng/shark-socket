package observability

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// metricKey identifies one series.
//
// It is a struct rather than a rendered string because the data plane reports
// on every message: `name + "{" + join(labels) + "}"` allocated a string per
// call (and a label-slice copy on top), which at 100k msg/s is 200k allocations
// per second of pure bookkeeping. A comparable struct built from strings the
// caller already holds needs no allocation at all.
//
// The first label pair is stored inline under its own field names so that
// label names still participate in identity: ("a","x") and ("b","x") must not
// collide. Anything beyond one pair falls back to the generic joined form --
// this project's own metrics never use more than one label, and a user metric
// that does pays one concatenation, not a wrong number.
type seriesKey struct {
	name   string
	label0 string
	value0 string
	rest   string
}

func newSeriesKey(name string, labels []string) seriesKey {
	switch {
	case len(labels) == 0:
		return seriesKey{name: name}
	// The inline form is only safe when the label actually has a name: an
	// empty-name pair with empty values would produce the same struct as "no
	// labels at all" and alias two different series.
	case len(labels) == 2 && labels[0] != "":
		return seriesKey{name: name, label0: labels[0], value0: labels[1]}
	default:
		return seriesKey{name: name, rest: strings.Join(labels, "\x00")}
	}
}

type PrometheusMetrics struct {
	mu         sync.RWMutex
	counters   map[seriesKey]*promMetric
	gauges     map[seriesKey]*promMetric
	histograms map[seriesKey]*promHistogram
	buckets    map[string][]float64
}

type promMetric struct {
	name   string
	labels []string
	value  float64
}

// promHistogram is a real Prometheus histogram: per-bucket counts plus the
// running sum. It used to carry only count and sum, which the exporter then
// declared as a summary -- a type that cannot be aggregated across instances
// and, without quantiles, could not even answer a single-instance P99. The
// bucket counts are the minimum required for histogram_quantile() to work.
type promHistogram struct {
	name    string
	labels  []string
	buckets []float64 // upper bounds, ascending
	counts  []uint64  // observations per bucket, non-cumulative
	count   uint64
	sum     float64
}

// observe records one value. The bucket search is a linear scan over a fixed
// 22-entry ladder; a binary search would save a handful of comparisons on a
// path that already takes a global lock, and the linear form is what makes the
// overflow bucket ("above the last bound") fall out without a special case.
func (h *promHistogram) observe(value float64) {
	for i, bound := range h.buckets {
		if value <= bound {
			h.counts[i]++
			h.count++
			h.sum += value
			return
		}
	}
	// Above the last bucket: counted in the implicit +Inf bucket only.
	h.count++
	h.sum += value
}

// DefaultHistogramBuckets is the ladder used for any histogram whose name was
// not registered with RegisterHistogram: seconds, covering one microsecond to
// ten seconds. Handlers in this project run from sub-microsecond echoes to
// multi-second business callbacks, so a Prometheus-default ladder that starts
// at 5ms would put every echo in the first bucket and tell nobody anything.
var DefaultHistogramBuckets = []float64{
	0.000001, 0.0000025, 0.000005,
	0.00001, 0.000025, 0.00005,
	0.0001, 0.00025, 0.0005,
	0.001, 0.0025, 0.005,
	0.01, 0.025, 0.05,
	0.1, 0.25, 0.5,
	1, 2.5, 5, 10,
}

func NewPrometheusMetrics() *PrometheusMetrics {
	return &PrometheusMetrics{
		counters:   make(map[seriesKey]*promMetric),
		gauges:     make(map[seriesKey]*promMetric),
		histograms: make(map[seriesKey]*promHistogram),
		buckets:    make(map[string][]float64),
	}
}

// RegisterHistogram declares the bucket ladder for a histogram name.
//
// It must be called before the first observation: observations already
// recorded were counted against the old boundaries, and re-dividing them would
// require keeping the raw samples (the Prometheus exporter deliberately does
// not, which is what keeps it O(buckets) instead of O(observations)).
//
// The default ladder is DefaultHistogramBuckets (seconds). Register explicitly
// for any other unit -- byte sizes, counts, ratios -- because a seconds ladder
// exported under a _bytes name produces a histogram where every observation
// lands in +Inf and histogram_quantile() has nothing to interpolate.
func (m *PrometheusMetrics) RegisterHistogram(name string, buckets []float64) error {
	if name == "" {
		return fmt.Errorf("prometheus: histogram name is required")
	}
	if len(buckets) == 0 {
		return fmt.Errorf("prometheus: histogram %s needs at least one bucket", name)
	}
	clean := append([]float64(nil), buckets...)
	sort.Float64s(clean)
	for i := 1; i < len(clean); i++ {
		if clean[i] == clean[i-1] {
			return fmt.Errorf("prometheus: histogram %s has duplicate bucket %v", name, clean[i])
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key := range m.histograms {
		if key.name == name {
			return fmt.Errorf("prometheus: histogram %s was already observed; register it before traffic", name)
		}
	}
	m.buckets[name] = clean
	return nil
}

// bucketsFor returns the registered ladder for name, or the seconds default.
// Callers must hold m.mu.
func (m *PrometheusMetrics) bucketsFor(name string) []float64 {
	if buckets, ok := m.buckets[name]; ok {
		return buckets
	}
	return DefaultHistogramBuckets
}

func (m *PrometheusMetrics) IncCounter(name string, labels ...string) {
	key := newSeriesKey(name, labels)
	m.mu.Lock()
	metric := m.counters[key]
	if metric == nil {
		metric = &promMetric{name: name, labels: append([]string(nil), labels...)}
		m.counters[key] = metric
	}
	metric.value++
	m.mu.Unlock()
}

func (m *PrometheusMetrics) SetGauge(name string, value float64, labels ...string) {
	key := newSeriesKey(name, labels)
	m.mu.Lock()
	gauge := m.gauges[key]
	if gauge == nil {
		gauge = &promMetric{name: name, labels: append([]string(nil), labels...)}
		m.gauges[key] = gauge
	}
	gauge.value = value
	m.mu.Unlock()
}

func (m *PrometheusMetrics) ObserveHistogram(name string, value float64, labels ...string) {
	key := newSeriesKey(name, labels)
	m.mu.Lock()
	hist := m.histograms[key]
	if hist == nil {
		buckets := m.bucketsFor(name)
		hist = &promHistogram{
			name:    name,
			labels:  append([]string(nil), labels...),
			buckets: buckets,
			counts:  make([]uint64, len(buckets)),
		}
		m.histograms[key] = hist
	}
	hist.observe(value)
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

func writePromMetrics(out *strings.Builder, typ string, metrics map[seriesKey]*promMetric) {
	declared := map[string]bool{}
	for _, metric := range sortedMetrics(metrics) {
		// One TYPE line per family: the exposition format allows a family to
		// carry many label sets, but the declaration is per family.
		if !declared[metric.name] {
			fmt.Fprintf(out, "# TYPE %s %s\n", metric.name, typ)
			declared[metric.name] = true
		}
		fmt.Fprintf(out, "%s%s %s\n", metric.name, promLabels(metric.labels), formatFloat(metric.value))
	}
}

func writePromHistograms(out *strings.Builder, histograms map[seriesKey]*promHistogram) {
	declared := map[string]bool{}
	for _, hist := range sortedHistograms(histograms) {
		if !declared[hist.name] {
			fmt.Fprintf(out, "# TYPE %s histogram\n", hist.name)
			declared[hist.name] = true
		}
		// Buckets are exported cumulatively (le = "at most this bound"),
		// which is the only form histogram_quantile() reads.
		var cumulative uint64
		for i, bound := range hist.buckets {
			cumulative += hist.counts[i]
			fmt.Fprintf(out, "%s_bucket%s %d\n", hist.name, promLabelsWithBucket(hist.labels, formatFloat(bound)), cumulative)
		}
		// The +Inf bucket is the total, not the sum of the ladder: values above
		// the last bound are counted in count/sum only. Prometheus requires
		// bucket{le="+Inf"} == _count, so it must not be derived from the
		// ladder.
		fmt.Fprintf(out, "%s_bucket%s %d\n", hist.name, promLabelsWithBucket(hist.labels, "+Inf"), hist.count)
		fmt.Fprintf(out, "%s_count%s %d\n", hist.name, promLabels(hist.labels), hist.count)
		fmt.Fprintf(out, "%s_sum%s %s\n", hist.name, promLabels(hist.labels), formatFloat(hist.sum))
	}
}

// sortedMetrics orders the series of one type by name then rendered labels, so
// an exposition is byte-stable across scrapes and diffs are readable.
func sortedMetrics(metrics map[seriesKey]*promMetric) []*promMetric {
	ordered := make([]*promMetric, 0, len(metrics))
	for _, metric := range metrics {
		ordered = append(ordered, metric)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].name != ordered[j].name {
			return ordered[i].name < ordered[j].name
		}
		return promLabels(ordered[i].labels) < promLabels(ordered[j].labels)
	})
	return ordered
}

func sortedHistograms(histograms map[seriesKey]*promHistogram) []*promHistogram {
	ordered := make([]*promHistogram, 0, len(histograms))
	for _, hist := range histograms {
		ordered = append(ordered, hist)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].name != ordered[j].name {
			return ordered[i].name < ordered[j].name
		}
		return promLabels(ordered[i].labels) < promLabels(ordered[j].labels)
	})
	return ordered
}

// promLabelsWithBucket appends the le label without touching the histogram's
// own label slice (which is shared by the _count and _sum lines).
func promLabelsWithBucket(labels []string, le string) string {
	merged := make([]string, 0, len(labels)+2)
	merged = append(merged, labels...)
	merged = append(merged, "le", le)
	return promLabels(merged)
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
