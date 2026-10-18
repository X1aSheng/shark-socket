package shared

import (
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// Metric names emitted at the handler boundary. They follow the project's
// unprefixed convention (sessions_active, accept_errors_total) so a single
// scrape can be summed across protocols with one name plus a label filter.
const (
	// MetricHandlerDuration is the wall-clock duration of one user handler or
	// responder invocation, in seconds.
	MetricHandlerDuration = "handler_duration_seconds"
	// MetricMessagesTotal counts messages delivered to a user handler.
	MetricMessagesTotal = "messages_total"
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
	labels := protocolLabels(protocol)
	metrics.ObserveHistogram(MetricHandlerDuration, time.Since(start).Seconds(), labels...)
	metrics.IncCounter(MetricMessagesTotal, labels...)
	return err
}

// protocolLabels returns the label pair for a protocol. A switch over the
// closed enum rather than a map lookup: this runs once per message, and the
// map hash is the single largest cost left in the no-op-metrics case, where
// nothing is recorded at all.
func protocolLabels(protocol core.Protocol) []string {
	switch protocol {
	case core.ProtocolTCP:
		return tcpLabels
	case core.ProtocolUDP:
		return udpLabels
	case core.ProtocolHTTP:
		return httpLabels
	case core.ProtocolWS:
		return wsLabels
	case core.ProtocolCoAP:
		return coapLabels
	case core.ProtocolQUIC:
		return quicLabels
	case core.ProtocolGRPCWeb:
		return grpcWebLabels
	case core.ProtocolCustom:
		return customLabels
	default:
		// An out-of-enum protocol is a caller-defined string; building the
		// labels allocates, which is the correct trade for a case that never
		// appears on a hot path.
		return []string{"protocol", string(protocol)}
	}
}

var (
	tcpLabels     = []string{"protocol", string(core.ProtocolTCP)}
	udpLabels     = []string{"protocol", string(core.ProtocolUDP)}
	httpLabels    = []string{"protocol", string(core.ProtocolHTTP)}
	wsLabels      = []string{"protocol", string(core.ProtocolWS)}
	coapLabels    = []string{"protocol", string(core.ProtocolCoAP)}
	quicLabels    = []string{"protocol", string(core.ProtocolQUIC)}
	grpcWebLabels = []string{"protocol", string(core.ProtocolGRPCWeb)}
	customLabels  = []string{"protocol", string(core.ProtocolCustom)}
)
