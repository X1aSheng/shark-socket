package core

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// ============================================================================
// Error tests
// ============================================================================
func TestErrorVariables(t *testing.T) {
	errors := []error{
		ErrClosed, ErrDuplicateProtocol, ErrDuplicateSession,
		ErrInvalidArgument, ErrNoServers, ErrPluginPanic,
		ErrSessionCapacity, ErrSessionClosed, ErrServerClosed,
		ErrWriteQueueFull, ErrFrameTooLarge, ErrUnsupportedFeature,
	}
	for i, e := range errors {
		if e == nil {
			t.Fatalf("error[%d] should not be nil", i)
		}
		if e.Error() == "" {
			t.Fatalf("error[%d] should have non-empty message", i)
		}
	}
}

func TestPluginErrors(t *testing.T) {
	if !errors.Is(ErrPluginDrop, ErrPluginDrop) {
		t.Fatal("ErrPluginDrop should be identifiable")
	}
	if !errors.Is(ErrPluginBlock, ErrPluginBlock) {
		t.Fatal("ErrPluginBlock should be identifiable")
	}
}

// ============================================================================
// Protocol tests
// ============================================================================
func TestProtocolConstants(t *testing.T) {
	protocols := map[Protocol]string{
		ProtocolTCP:     "tcp",
		ProtocolUDP:     "udp",
		ProtocolHTTP:    "http",
		ProtocolWS:      "websocket",
		ProtocolCoAP:    "coap",
		ProtocolQUIC:    "quic",
		ProtocolGRPCWeb: "grpc-web",
		ProtocolCustom:  "custom",
	}
	for p, want := range protocols {
		if string(p) != want {
			t.Fatalf("Protocol = %q, want %q", p, want)
		}
	}
}

func TestSessionStates(t *testing.T) {
	tests := []struct {
		state SessionState
		want  string
	}{
		{StateConnecting, "connecting"},
		{StateActive, "active"},
		{StateDraining, "draining"},
		{StateClosed, "closed"},
		{SessionState(99), "unknown(99)"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Fatalf("SessionState(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
	// Verify ordering
	if StateConnecting != 0 {
		t.Fatal("StateConnecting should be 0")
	}
	if StateActive != 1 {
		t.Fatal("StateActive should be 1")
	}
}

// ============================================================================
// Plugin tests
// ============================================================================
func TestBasePlugin(t *testing.T) {
	bp := BasePlugin{}
	if bp.Name() != "base" {
		t.Fatalf("Name = %s, want base", bp.Name())
	}
	if bp.Priority() != 1000 {
		t.Fatalf("Priority = %d, want 1000", bp.Priority())
	}
	if err := bp.OnAccept(nil); err != nil {
		t.Fatal(err)
	}
	data, err := bp.OnMessage(nil, []byte("test"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "test" {
		t.Fatalf("OnMessage = %q, want test", data)
	}
	bp.OnClose(nil) // should not panic
}

func TestBasePluginEmbedding(t *testing.T) {
	type myPlugin struct {
		BasePlugin
	}
	p := myPlugin{}
	if p.Name() != "base" {
		t.Fatalf("embedded Name = %s, want base", p.Name())
	}
}

// ============================================================================
// Observability tests
// ============================================================================
func TestNopLogger(t *testing.T) {
	l := NopLogger()
	if l == nil {
		t.Fatal("NopLogger should not return nil")
	}
	l.Debug("msg")
	l.Info("msg")
	l.Warn("msg")
	l.Error("msg")
	l.Debug("msg", "key", "val")
	// Should not panic
}

func TestSlogLogger(t *testing.T) {
	l := NewSlogLogger(nil)
	if l == nil {
		t.Fatal("NewSlogLogger(nil) should not return nil")
	}
	l.Info("test", "key", "value")

	l2 := NewSlogLogger(slog.New(slog.DiscardHandler))
	if l2 == nil {
		t.Fatal("NewSlogLogger with real logger should not return nil")
	}
	l2.Debug("debug", "a", 1)
	l2.Warn("warn")
	l2.Error("error")
}

func TestNopMetrics(t *testing.T) {
	m := NopMetrics()
	if m == nil {
		t.Fatal("NopMetrics should not return nil")
	}
	m.IncCounter("test", "label", "val")
	m.SetGauge("test", 1.0)
	m.SetGauge("test", 1.0, "l", "v")
	m.ObserveHistogram("test", 0.5)
	m.ObserveHistogram("test", 0.5, "l", "v")
	// Should not panic
}

func TestNopTracer(t *testing.T) {
	tr := NopTracer()
	if tr == nil {
		t.Fatal("NopTracer should not return nil")
	}
	ctx, span := tr.Start(context.Background(), "test-span", "attr", "val")
	if ctx == nil {
		t.Fatal("Start should return non-nil context")
	}
	span.End()
	span.RecordError(errors.New("test error"))
	// Should not panic
}

// ============================================================================
// Message tests
// ============================================================================
func TestMessageStruct(t *testing.T) {
	msg := Message{
		SessionID: 42,
		Protocol:  ProtocolTCP,
		Payload:   []byte("hello"),
		Meta:      map[string]string{"key": "val"},
	}
	if msg.SessionID != 42 {
		t.Fatalf("SessionID = %d, want 42", msg.SessionID)
	}
	if msg.Protocol != ProtocolTCP {
		t.Fatalf("Protocol = %s, want tcp", msg.Protocol)
	}
	if string(msg.Payload) != "hello" {
		t.Fatalf("Payload = %s, want hello", msg.Payload)
	}
	if msg.Meta["key"] != "val" {
		t.Fatalf("Meta[key] = %s, want val", msg.Meta["key"])
	}
}

func TestMessageEmptyMeta(t *testing.T) {
	msg := Message{SessionID: 1}
	// nil Meta should be fine
	if msg.Meta != nil {
		t.Log("Meta is non-nil but empty - fine")
	}
}

// ============================================================================
// Codec tests
// ============================================================================
type stringCodec struct{}

func (c stringCodec) Encode(s string) ([]byte, error) { return []byte(s), nil }
func (c stringCodec) Decode(b []byte) (string, error) { return string(b), nil }

func TestAdaptTyped(t *testing.T) {
	called := false
	handler := AdaptTyped(stringCodec{}, TypedHandler[string](func(_ Session, s string) error {
		called = true
		if s != "world" {
			t.Fatalf("decoded = %q, want world", s)
		}
		return nil
	}))

	msg := Message{Payload: []byte("world")}
	if err := handler(nil, msg); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("handler should have been called")
	}
}

func TestAdaptTypedDecodeError(t *testing.T) {
	handler := AdaptTyped(&decodeErrorCodec{}, TypedHandler[string](func(_ Session, _ string) error {
		return nil
	}))

	if err := handler(nil, Message{Payload: []byte("any")}); err == nil {
		t.Fatal("expected error from decode failure")
	}
}

type decodeErrorCodec struct{}

func (c *decodeErrorCodec) Encode(s string) ([]byte, error) { return []byte(s), nil }
func (c *decodeErrorCodec) Decode(_ []byte) (string, error) {
	return "", errors.New("decode failed")
}

// ============================================================================
// ConfigSnapshot tests
// ============================================================================
func TestConfigSnapshot(t *testing.T) {
	cs := ConfigSnapshot{
		Shutdown: StageTimeouts{
			StopAccept:    time.Second,
			Drain:         2 * time.Second,
			CloseSessions: 3 * time.Second,
			Finalize:      4 * time.Second,
		},
		Started: time.Now(),
	}
	if cs.Shutdown.StopAccept != time.Second {
		t.Fatal("wrong StopAccept")
	}
	if cs.Shutdown.Drain != 2*time.Second {
		t.Fatal("wrong Drain")
	}
	if cs.Started.IsZero() {
		t.Fatal("Started should not be zero")
	}
}

// ============================================================================
// Edge cases and string formatting
// ============================================================================
func TestSessionStateStringAllValues(t *testing.T) {
	states := []SessionState{StateConnecting, StateActive, StateDraining, StateClosed}
	for _, s := range states {
		str := s.String()
		if str == "" || strings.HasPrefix(str, "unknown") {
			t.Fatalf("valid state %d should have known name, got %q", s, str)
		}
	}
}

func TestProtocolEquality(t *testing.T) {
	p1 := Protocol("tcp")
	p2 := ProtocolTCP
	if p1 != p2 {
		t.Fatal("Protocol equality should work")
	}
	if Protocol("invalid") == ProtocolTCP {
		t.Fatal("different protocols should not be equal")
	}
}

// ============================================================================
// Server interface compile-time check
// ============================================================================
var _ interface {
	Protocol() Protocol
} = (*mockServer)(nil)

type mockServer struct{}

func (m *mockServer) Protocol() Protocol { return ProtocolCustom }
