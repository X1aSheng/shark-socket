package shared

import (
	"testing"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/infra/observability"
)

var benchSinkErr error

func noopHandler() error { return nil }

// BenchmarkCallHandler is the baseline: panic-isolated handler invocation with
// no metrics backend, which is what a library user gets by default.
func BenchmarkCallHandler(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchSinkErr = CallHandler(noopHandler, core.NopLogger())
	}
}

// BenchmarkCallHandlerObservedNop isolates the cost this change adds to the
// default configuration: the per-protocol label lookup plus one branch. It must
// stay in the same order of magnitude as the baseline, because a user who never
// configures an exporter should not pay for one.
func BenchmarkCallHandlerObservedNop(b *testing.B) {
	metrics := core.NopMetrics()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchSinkErr = CallHandlerObserved(metrics, core.ProtocolTCP, noopHandler, core.NopLogger())
	}
}

// BenchmarkCallHandlerObservedPrometheus is the production configuration: two
// exporter calls per message.
func BenchmarkCallHandlerObservedPrometheus(b *testing.B) {
	metrics := observability.NewPrometheusMetrics()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchSinkErr = CallHandlerObserved(metrics, core.ProtocolTCP, noopHandler, core.NopLogger())
	}
}
