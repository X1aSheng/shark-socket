package shared

import (
	"errors"
	"testing"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/infra/observability"
)

func TestCallHandlerReturnsError(t *testing.T) {
	errBoom := errors.New("boom")
	err := CallHandler(func() error { return errBoom }, nil)
	if !errors.Is(err, errBoom) {
		t.Fatalf("CallHandler error = %v, want %v", err, errBoom)
	}
}

func TestCallHandlerRecoversPanic(t *testing.T) {
	err := CallHandler(func() error { panic("kaboom") }, nil)
	if !errors.Is(err, core.ErrHandlerPanic) {
		t.Fatalf("CallHandler panic error = %v, want %v", err, core.ErrHandlerPanic)
	}
}

// TestCallHandlerObservedRecordsMetrics is D1's unit-level acceptance: a call
// through the instrumented path leaves one histogram observation and one
// message count, labelled with the protocol that produced them.
func TestCallHandlerObservedRecordsMetrics(t *testing.T) {
	metrics := observability.NewMemoryMetrics()
	if err := CallHandlerObserved(metrics, core.ProtocolCoAP, func() error { return nil }, nil); err != nil {
		t.Fatalf("CallHandlerObserved: %v", err)
	}

	samples := metrics.Histogram(MetricHandlerDuration, "protocol", "coap")
	if len(samples) != 1 {
		t.Fatalf("%s samples = %d, want 1", MetricHandlerDuration, len(samples))
	}
	if samples[0] < 0 {
		t.Fatalf("recorded a negative duration: %v", samples[0])
	}
	if got := metrics.Counter(MetricMessagesTotal, "protocol", "coap"); got != 1 {
		t.Fatalf("%s = %v, want 1", MetricMessagesTotal, got)
	}
	// The label keeps protocols apart: a CoAP message must not land in the TCP
	// series, or per-protocol P99 would be a blend of every transport.
	if got := metrics.Counter(MetricMessagesTotal, "protocol", "tcp"); got != 0 {
		t.Fatalf("tcp series was incremented by a coap message: %v", got)
	}
}

// TestCallHandlerObservedKeepsErrorAndPanicSemantics checks that instrumenting
// the call did not change what the transport sees.
func TestCallHandlerObservedKeepsErrorAndPanicSemantics(t *testing.T) {
	metrics := observability.NewMemoryMetrics()
	errBoom := errors.New("boom")

	if err := CallHandlerObserved(metrics, core.ProtocolTCP, func() error { return errBoom }, nil); !errors.Is(err, errBoom) {
		t.Fatalf("error = %v, want %v", err, errBoom)
	}
	if err := CallHandlerObserved(metrics, core.ProtocolTCP, func() error { panic("kaboom") }, nil); !errors.Is(err, core.ErrHandlerPanic) {
		t.Fatalf("panic error = %v, want %v", err, core.ErrHandlerPanic)
	}
	// Both calls were delivered to the handler boundary, so both are counted:
	// the counter measures traffic, not success.
	if got := metrics.Counter(MetricMessagesTotal, "protocol", "tcp"); got != 2 {
		t.Fatalf("%s = %v, want 2", MetricMessagesTotal, got)
	}
	if got := len(metrics.Histogram(MetricHandlerDuration, "protocol", "tcp")); got != 2 {
		t.Fatalf("duration samples = %d, want 2", got)
	}
}

// TestCallHandlerObservedNilMetrics makes the no-runtime path explicit: nil is
// a valid backend and must not panic.
func TestCallHandlerObservedNilMetrics(t *testing.T) {
	errBoom := errors.New("boom")
	if err := CallHandlerObserved(nil, core.ProtocolQUIC, func() error { return errBoom }, nil); !errors.Is(err, errBoom) {
		t.Fatalf("error = %v, want %v", err, errBoom)
	}
}

// TestIncProtocolCounterLabelsByProtocol covers the helper the transports use
// for admission counters: a missing label would silently create a series that
// no per-protocol query can see.
func TestIncProtocolCounterLabelsByProtocol(t *testing.T) {
	metrics := observability.NewMemoryMetrics()
	IncProtocolCounter(metrics, MetricSessionsRejected, core.ProtocolWS)
	IncProtocolCounter(metrics, MetricSessionsRejected, core.ProtocolWS)
	IncProtocolCounter(metrics, MetricSessionsRejected, core.ProtocolQUIC)

	if got := metrics.Counter(MetricSessionsRejected, "protocol", "websocket"); got != 2 {
		t.Fatalf("websocket series = %v, want 2", got)
	}
	if got := metrics.Counter(MetricSessionsRejected, "protocol", "quic"); got != 1 {
		t.Fatalf("quic series = %v, want 1", got)
	}
	if got := metrics.Counter(MetricSessionsRejected); got != 0 {
		t.Fatalf("unlabelled series = %v, want 0 (the label is mandatory)", got)
	}
	// A nil backend is legal: transports built without a runtime still call it.
	IncProtocolCounter(nil, MetricSessionsRejected, core.ProtocolTCP)
}
