package unit_test

import (
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/protocol/coap"
	"github.com/X1aSheng/shark-socket/internal/protocol/http"
	tcpproto "github.com/X1aSheng/shark-socket/internal/protocol/tcp"
	"github.com/X1aSheng/shark-socket/internal/protocol/udp"
	"github.com/X1aSheng/shark-socket/internal/protocol/websocket"
	"github.com/X1aSheng/shark-socket/internal/types"
)

func TestProtocolTypes_All(t *testing.T) {
	protos := map[string]types.ProtocolType{
		"TCP":       types.TCP,
		"TLS":       types.TLS,
		"UDP":       types.UDP,
		"HTTP":      types.HTTP,
		"WebSocket": types.WebSocket,
		"CoAP":      types.CoAP,
	}
	for name, p := range protos {
		if p.String() == "" {
			t.Errorf("%s: String() returned empty", name)
		}
	}
}

func TestSessionStates_All(t *testing.T) {
	states := map[string]types.SessionState{
		"Connecting": types.Connecting,
		"Active":     types.Active,
		"Closing":    types.Closing,
		"Closed":     types.Closed,
	}
	for name, s := range states {
		if s.String() == "" {
			t.Errorf("%s: String() returned empty", name)
		}
	}
}

func TestMessageTypes_All(t *testing.T) {
	msgTypes := map[string]types.MessageType{
		"Text":   types.Text,
		"Binary": types.Binary,
		"Ping":   types.Ping,
		"Pong":   types.Pong,
		"Close":  types.Close,
	}
	for name, mt := range msgTypes {
		if mt.String() == "" {
			t.Errorf("%s: String() returned empty", name)
		}
	}
}

func TestNewRawMessage(t *testing.T) {
	msg := types.NewRawMessage(42, types.TCP, []byte("payload"))
	if msg.SessionID != 42 {
		t.Errorf("SessionID = %d, want 42", msg.SessionID)
	}
	if msg.Protocol != types.TCP {
		t.Errorf("Protocol = %v, want TCP", msg.Protocol)
	}
	if string(msg.Payload) != "payload" {
		t.Errorf("Payload = %q, want %q", msg.Payload, "payload")
	}
}

func TestDefaultPorts(t *testing.T) {
	tests := []struct {
		name     string
		expected int
	}{
		{"TCP default", 18000},
		{"UDP default", 18200},
		{"HTTP default", 18400},
		{"WebSocket default", 18600},
		{"CoAP default", 18800},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Just verify the server can be created with defaults
			var srv types.Server
			switch tt.expected {
			case 18000:
				srv = tcpproto.NewServer(nil)
			case 18200:
				srv = udp.NewServer(nil)
			case 18400:
				srv = http.NewServer()
			case 18600:
				srv = websocket.NewServer(nil)
			case 18800:
				srv = coap.NewServer(nil)
			}
			if srv == nil {
				t.Fatal("server is nil")
			}
		})
	}
}

func TestOptions_Addr(t *testing.T) {
	tests := []struct {
		name string
		addr string
	}{
		{"TCP", "127.0.0.1:18000"},
		{"UDP", "127.0.0.1:18200"},
		{"HTTP", "127.0.0.1:18400"},
		{"WebSocket", "127.0.0.1:18600"},
		{"CoAP", "127.0.0.1:18800"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, port, err := net.SplitHostPort(tt.addr)
			if err != nil {
				t.Fatalf("invalid address %q: %v", tt.addr, err)
			}
			if host != "127.0.0.1" {
				t.Errorf("host = %q, want 127.0.0.1", host)
			}
			if port == "" {
				t.Error("port is empty")
			}
		})
	}
}

func TestPluginInterface(t *testing.T) {
	p := types.BasePlugin{}
	if p.Name() != "base" {
		t.Errorf("BasePlugin Name = %q, want %q", p.Name(), "base")
	}
	if p.Priority() != 100 {
		t.Errorf("BasePlugin Priority = %d, want 100", p.Priority())
	}
	if err := p.OnAccept(nil); err != nil {
		t.Errorf("OnAccept: %v", err)
	}
	data, err := p.OnMessage(nil, []byte("test"))
	if err != nil {
		t.Errorf("OnMessage: %v", err)
	}
	if string(data) != "test" {
		t.Errorf("OnMessage data = %q, want %q", data, "test")
	}
	p.OnClose(nil)
}

func TestWebSocketOptions_All(t *testing.T) {
	srv := websocket.NewServer(nil,
		websocket.WithAddr("127.0.0.1", 9999),
		websocket.WithPath("/custom"),
		websocket.WithMaxSessions(50),
		websocket.WithMaxMessageSize(2048),
		websocket.WithPingPong(10*time.Second, 5*time.Second),
		websocket.WithAllowedOrigins("http://localhost"),
	)
	if srv == nil {
		t.Fatal("server is nil")
	}
}

func TestCoAPOptions_All(t *testing.T) {
	srv := coap.NewServer(nil,
		coap.WithAddr("127.0.0.1", 9999),
		coap.WithMaxSessions(50),
		coap.WithSessionTTL(30*time.Second),
		coap.WithAckTimeout(1*time.Second, 3),
	)
	if srv == nil {
		t.Fatal("server is nil")
	}
}

func TestTCPOptions_All(t *testing.T) {
	srv := tcpproto.NewServer(nil,
		tcpproto.WithAddr("127.0.0.1", 9999),
		tcpproto.WithWorkerPool(4, 16, 256),
		tcpproto.WithMaxSessions(50),
		tcpproto.WithMaxMessageSize(8192),
		tcpproto.WithTimeouts(5, 10, 30),
		tcpproto.WithDrainTimeout(15),
		tcpproto.WithFullPolicy(tcpproto.PolicyBlock),
	)
	if srv == nil {
		t.Fatal("server is nil")
	}
}

func TestUDPOptions_All(t *testing.T) {
	srv := udp.NewServer(nil,
		udp.WithAddr("127.0.0.1", 9999),
		udp.WithMaxSessions(50),
		udp.WithSessionTTL(30*time.Second),
	)
	if srv == nil {
		t.Fatal("server is nil")
	}
}

func TestHTTPOptions_All(t *testing.T) {
	srv := http.NewServer(
		http.WithAddr("127.0.0.1", 9999),
		http.WithTimeouts(5, 10, 30),
	)
	if srv == nil {
		t.Fatal("server is nil")
	}
}
