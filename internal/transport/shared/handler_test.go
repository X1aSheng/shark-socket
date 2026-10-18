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

// TestProtocolLabelsAreStable guards the label table against returning a slice
// the caller could mutate into shared state.
func TestProtocolLabelsAreStable(t *testing.T) {
	for _, protocol := range []core.Protocol{
		core.ProtocolTCP, core.ProtocolUDP, core.ProtocolHTTP, core.ProtocolWS,
		core.ProtocolCoAP, core.ProtocolQUIC, core.ProtocolGRPCWeb, core.ProtocolCustom,
	} {
		labels := protocolLabels(protocol)
		if len(labels) != 2 || labels[0] != "protocol" || labels[1] != string(protocol) {
			t.Fatalf("protocolLabels(%q) = %v", protocol, labels)
		}
	}
	if got := protocolLabels("mqtt"); len(got) != 2 || got[1] != "mqtt" {
		t.Fatalf("protocolLabels(out-of-enum) = %v", got)
	}
}
