package shared

import (
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// Metric names emitted at the handler and admission boundaries.
//
// Naming rule: a metric that more than one transport writes carries a
// `protocol` label; a metric only one transport can write keeps the protocol in
// its name (tcp_task_queue_full_total, tls_handshake_failed_total). The label is
// the only form that survives a sum across transports -- "how many sessions were
// rejected in total" stays one expression -- while a per-transport name can only
// be aggregated with a regex over metric names.
const (
	// MetricHandlerDuration is the wall-clock duration of one user handler or
	// responder invocation, in seconds.
	MetricHandlerDuration = "handler_duration_seconds"
	// MetricMessagesTotal counts messages delivered to a user handler.
	MetricMessagesTotal = "messages_total"
	// MetricSessionsRejected counts connections or pseudo-sessions refused by
	// an admission check.
	MetricSessionsRejected = "sessions_rejected_total"
	// MetricSessionsReclaimed counts sessions reclaimed because the peer went
	// silent (read deadline expired), as opposed to a clean close.
	MetricSessionsReclaimed = "sessions_reclaimed_total"
)

// CallHandler invokes a user handler/responder with panic recovery. A
// panicking user handler would otherwise crash the whole process, because Go
// has no per-goroutine recovery and the transport goroutines (worker, read
// loop, stream) invoke handlers directly. Instead the panic is logged and a
// non-nil error is returned, so the transport treats it like a failing handler
// (e.g. closes the session) — matching the failure-isolation design principle.
func CallHandler(fn func() error, logger core.Logger) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if logger != nil {
				logger.Error("user handler panic", "panic", r)
			}
			err = core.ErrHandlerPanic
		}
	}()
	return fn()
}

// CallHandlerObserved is CallHandler plus the two metrics that make latency
// self-evident in production: handler_duration_seconds{protocol} and
// messages_total{protocol}.
//
// The pair belongs at this boundary rather than in each transport because this
// is the one place every protocol funnels through, so the numbers are
// comparable across transports and cannot silently go missing when a new
// transport is added. Timing wraps the panic-isolated call, so the recorded
// duration is the handler's own work (including its Send), not the runtime's.
//
// metrics may be nil: QUIC and tests can run without a runtime, and a nil
// backend must cost nothing beyond one branch, not a panic on the hot path.
func CallHandlerObserved(metrics core.Metrics, protocol core.Protocol, fn func() error, logger core.Logger) error {
	start := time.Now()
	err := CallHandler(fn, logger)
	if metrics == nil {
		return err
	}
	labels := core.ProtocolLabel(protocol)
	metrics.ObserveHistogram(MetricHandlerDuration, time.Since(start).Seconds(), labels...)
	metrics.IncCounter(MetricMessagesTotal, labels...)
	return err
}

// IncProtocolCounter increments a protocol-scoped counter of the calling
// transport.
//
// It exists so a transport reports an event without repeating the nil check and
// the label pair at every call site: those two are the easy part to forget, and
// forgetting them produces a counter that is silently missing series -- an
// uncounted rejection is indistinguishable from no rejection (ADR-020).
func IncProtocolCounter(metrics core.Metrics, name string, protocol core.Protocol) {
	if metrics == nil {
		return
	}
	metrics.IncCounter(name, core.ProtocolLabel(protocol)...)
}
