package quic

import (
	"crypto/tls"
	"testing"

	"github.com/X1aSheng/shark-socket/internal/types"
)

func TestNewServer_Creation(t *testing.T) {
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return nil
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:        []string{"quic-test"},
	}

	srv := NewServer(handler,
		WithAddr("127.0.0.1", 0),
		WithTLS(tlsConfig),
	)
	if srv == nil {
		t.Fatal("expected non-nil server")
	}
	if srv.Protocol() != types.QUIC {
		t.Errorf("expected QUIC protocol, got %v", srv.Protocol())
	}
}

func TestServer_ProtocolReturnsQUIC(t *testing.T) {
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return nil
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:        []string{"quic-test"},
	}

	srv := NewServer(handler, WithTLS(tlsConfig))
	if srv.Protocol() != types.QUIC {
		t.Errorf("expected QUIC, got %v", srv.Protocol())
	}
}

func TestServer_SetHandler(t *testing.T) {
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return nil
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:        []string{"quic-test"},
	}

	srv := NewServer(handler, WithTLS(tlsConfig))

	newHandler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return nil
	}
	srv.SetHandler(newHandler)
}

func TestServer_Options(t *testing.T) {
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return nil
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:        []string{"quic-test"},
	}

	srv := NewServer(handler,
		WithAddr("0.0.0.0", 18601),
		WithTLS(tlsConfig),
		WithMaxMessageSize(2048),
		WithMaxSessions(500),
		WithMaxIncomingStreams(50),
		WithTimeouts(10, 20, 60),
		WithQUICTimeouts(5, 15),
	)

	if srv.opts.Port != 18601 {
		t.Errorf("expected port 18601, got %d", srv.opts.Port)
	}
	if srv.opts.MaxMessageSize != 2048 {
		t.Errorf("expected max message size 2048, got %d", srv.opts.MaxMessageSize)
	}
	if srv.opts.MaxSessions != 500 {
		t.Errorf("expected max sessions 500, got %d", srv.opts.MaxSessions)
	}
	if srv.opts.MaxIncomingStreams != 50 {
		t.Errorf("expected max incoming streams 50, got %d", srv.opts.MaxIncomingStreams)
	}
	if srv.opts.ReadTimeout != 10 {
		t.Errorf("expected read timeout 10, got %d", srv.opts.ReadTimeout)
	}
}

func TestDefaultOptions(t *testing.T) {
	opts := defaultOptions()

	if opts.Host != "0.0.0.0" {
		t.Errorf("expected default host 0.0.0.0, got %s", opts.Host)
	}
	if opts.Port != 18900 {
		t.Errorf("expected default port 18900, got %d", opts.Port)
	}
	if opts.WorkerCount != 4 {
		t.Errorf("expected default worker count 4, got %d", opts.WorkerCount)
	}
	if opts.MaxMessageSize != 1024*1024 {
		t.Errorf("expected default max message size 1MB, got %d", opts.MaxMessageSize)
	}
}

func TestOptions_Addr(t *testing.T) {
	opts := Options{Host: "127.0.0.1", Port: 1234}
	addr := opts.Addr()
	if addr != "127.0.0.1:1234" {
		t.Errorf("expected addr 127.0.0.1:1234, got %s", addr)
	}
}

func TestOptions_GetDurations(t *testing.T) {
	opts := Options{
		ReadTimeout:      10,
		WriteTimeout:     20,
		IdleTimeout:      30,
		HandshakeTimeout: 5,
		IdleTimeoutQUIC:  15,
	}

	if opts.GetReadTimeout() != 10*1e9 {
		t.Errorf("expected read timeout 10s, got %v", opts.GetReadTimeout())
	}
	if opts.GetWriteTimeout() != 20*1e9 {
		t.Errorf("expected write timeout 20s, got %v", opts.GetWriteTimeout())
	}
	if opts.GetIdleTimeout() != 30*1e9 {
		t.Errorf("expected idle timeout 30s, got %v", opts.GetIdleTimeout())
	}
	if opts.GetHandshakeTimeout() != 5*1e9 {
		t.Errorf("expected handshake timeout 5s, got %v", opts.GetHandshakeTimeout())
	}
	if opts.GetIdleTimeoutQUIC() != 15*1e9 {
		t.Errorf("expected QUIC idle timeout 15s, got %v", opts.GetIdleTimeoutQUIC())
	}
}

func TestOptions_Validate(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		wantErr bool
	}{
		{
			name: "valid options",
			opts: Options{Port: 18900, TLSConfig: &tls.Config{}},
			wantErr: false,
		},
		{
			name: "invalid port negative",
			opts: Options{Port: -1, TLSConfig: &tls.Config{}},
			wantErr: true,
		},
		{
			name: "invalid port too large",
			opts: Options{Port: 70000, TLSConfig: &tls.Config{}},
			wantErr: true,
		},
		{
			name: "missing TLS config",
			opts: Options{Port: 18900, TLSConfig: nil},
			wantErr: true,
		},
		{
			name: "negative max streams",
			opts: Options{Port: 18900, TLSConfig: &tls.Config{}, MaxIncomingStreams: -1},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestWithPlugins(t *testing.T) {
	handler := func(sess types.RawSession, msg types.Message[[]byte]) error {
		return nil
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:        []string{"quic-test"},
	}

	srv := NewServer(handler,
		WithTLS(tlsConfig),
		WithPlugins(&mockPlugin{}),
	)

	if len(srv.opts.Plugins) != 1 {
		t.Errorf("expected 1 plugin, got %d", len(srv.opts.Plugins))
	}
}

type mockPlugin struct{}

func (m *mockPlugin) Name() string         { return "mock" }
func (m *mockPlugin) Priority() int     { return 0 }
func (m *mockPlugin) OnAccept(s types.RawSession) error { return nil }
func (m *mockPlugin) OnMessage(s types.RawSession, data []byte) ([]byte, error) {
	return data, nil
}
func (m *mockPlugin) OnClose(s types.RawSession) {}

// Compile-time verification
var _ types.Server = (*Server)(nil)
var _ types.Plugin = (*mockPlugin)(nil)
