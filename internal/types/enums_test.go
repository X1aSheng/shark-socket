package types

import "testing"

func TestProtocolTypeString(t *testing.T) {
	tests := []struct {
		p    ProtocolType
		want string
	}{
		{TCP, "TCP"},
		{TLS, "TLS"},
		{UDP, "UDP"},
		{HTTP, "HTTP"},
		{WebSocket, "WebSocket"},
		{CoAP, "CoAP"},
		{Custom, "Custom"},
		{ProtocolType(0), "Unknown"},
		{ProtocolType(255), "Unknown"},
	}
	for _, tt := range tests {
		if got := tt.p.String(); got != tt.want {
			t.Errorf("ProtocolType(%d).String() = %q, want %q", tt.p, got, tt.want)
		}
	}
}

func TestMessageTypeString(t *testing.T) {
	tests := []struct {
		m    MessageType
		want string
	}{
		{Text, "Text"},
		{Binary, "Binary"},
		{Ping, "Ping"},
		{Pong, "Pong"},
		{Close, "Close"},
		{CoAPGet, "CoAPGet"},
		{CoAPPost, "CoAPPost"},
		{CoAPPut, "CoAPPut"},
		{CoAPDel, "CoAPDelete"},
		{CoAPACK, "CoAPACK"},
		{MessageType(0), "Unknown"},
		{MessageType(255), "Unknown"},
	}
	for _, tt := range tests {
		if got := tt.m.String(); got != tt.want {
			t.Errorf("MessageType(%d).String() = %q, want %q", tt.m, got, tt.want)
		}
	}
}

func TestSessionStateString(t *testing.T) {
	tests := []struct {
		s    SessionState
		want string
	}{
		{Connecting, "Connecting"},
		{Active, "Active"},
		{Closing, "Closing"},
		{Closed, "Closed"},
		{SessionState(99), "Unknown"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("SessionState(%d).String() = %q, want %q", tt.s, got, tt.want)
		}
	}
}

func TestProtocolLabelPooled(t *testing.T) {
	a := ProtocolLabel(TCP)
	b := ProtocolLabel(TCP)
	if a != b {
		t.Errorf("ProtocolLabel should return consistent strings")
	}
	if a != "TCP" {
		t.Errorf("ProtocolLabel(TCP) = %q, want %q", a, "TCP")
	}
}
