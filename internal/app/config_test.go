package app

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultConfigIsValid(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Protocols) != 1 || cfg.Protocols[0].Name != "tcp" {
		t.Fatalf("protocols = %#v, want default tcp", cfg.Protocols)
	}
}

func TestLoadConfigFromJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data, err := json.Marshal(Config{
		ShutdownTimeout: "3s",
		HealthAddr:      "127.0.0.1:0",
		MetricsAddr:     "127.0.0.1:0",
		Protocols: []ProtocolConfig{
			{Name: "tcp", Addr: "127.0.0.1:0"},
			{Name: "websocket", Addr: "127.0.0.1:0", Path: "/ws"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Protocols) != 2 {
		t.Fatalf("protocols = %d, want 2", len(cfg.Protocols))
	}
}

func TestConfigEnvOverride(t *testing.T) {
	cfg := DefaultConfig()
	if err := applyEnv(&cfg, func(key string) (string, bool) {
		values := map[string]string{
			"SHARK_TCP_ADDR":     "127.0.0.1:19000",
			"SHARK_HEALTH_ADDR":  "127.0.0.1:19081",
			"SHARK_METRICS_ADDR": "127.0.0.1:19080",
		}
		value, ok := values[key]
		return value, ok
	}); err != nil {
		t.Fatal(err)
	}
	if cfg.Protocols[0].Addr != "127.0.0.1:19000" {
		t.Fatalf("tcp addr = %q", cfg.Protocols[0].Addr)
	}
	if cfg.HealthAddr != "127.0.0.1:19081" || cfg.MetricsAddr != "127.0.0.1:19080" {
		t.Fatalf("health=%q metrics=%q", cfg.HealthAddr, cfg.MetricsAddr)
	}
}

func TestConfigRejectsNegativeMaxMessageBytes(t *testing.T) {
	cfg := Config{
		ShutdownTimeout: "2s",
		Protocols: []ProtocolConfig{
			{Name: "grpc-web", Addr: "127.0.0.1:0", MaxMessageBytes: -1},
		},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "max_message_bytes") {
		t.Fatalf("Validate error = %v, want max_message_bytes error", err)
	}
}

func TestConfigRejectsPartialTLSFiles(t *testing.T) {
	cfg := Config{
		ShutdownTimeout: "2s",
		Protocols: []ProtocolConfig{
			{Name: "tcp", Addr: "127.0.0.1:0", TLSCertFile: "server.crt"},
		},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "must be supplied together") {
		t.Fatalf("Validate error = %v, want paired tls file error", err)
	}
}

func TestConfigRejectsTLSFilesOnUnsupportedProtocol(t *testing.T) {
	cfg := Config{
		ShutdownTimeout: "2s",
		Protocols: []ProtocolConfig{
			{Name: "http", Addr: "127.0.0.1:0", TLSCertFile: "server.crt", TLSKeyFile: "server.key"},
		},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "does not support tls_cert_file") {
		t.Fatalf("Validate error = %v, want unsupported tls file error", err)
	}
}

func TestConfigRejectsClientCAWithoutTLS(t *testing.T) {
	cfg := Config{
		ShutdownTimeout: "2s",
		Protocols: []ProtocolConfig{
			{Name: "tcp", Addr: "127.0.0.1:0", TLSClientCAFile: "ca.crt"},
		},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "tls_client_ca_file requires") {
		t.Fatalf("Validate error = %v, want client ca requires tls error", err)
	}
}

func TestConfigRejectsInvalidTLSClientAuth(t *testing.T) {
	cfg := Config{
		ShutdownTimeout: "2s",
		Protocols: []ProtocolConfig{
			{
				Name:          "tcp",
				Addr:          "127.0.0.1:0",
				TLSCertFile:   "server.crt",
				TLSKeyFile:    "server.key",
				TLSClientAuth: "strict-ish",
			},
		},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "invalid tls_client_auth") {
		t.Fatalf("Validate error = %v, want invalid tls_client_auth error", err)
	}
}

func TestConfigRejectsQUICWithoutTLSFiles(t *testing.T) {
	cfg := Config{
		ShutdownTimeout: "2s",
		Protocols: []ProtocolConfig{
			{Name: "quic", Addr: "127.0.0.1:0"},
		},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "tls_cert_file") {
		t.Fatalf("Validate error = %v, want tls file error", err)
	}
}

func TestNewRegistersConfiguredTCPWithTLSFiles(t *testing.T) {
	certFile, keyFile := writeTestCertificate(t)
	cfg := Config{
		ShutdownTimeout: "2s",
		Protocols: []ProtocolConfig{
			{Name: "tcp", Addr: "127.0.0.1:0", TLSCertFile: certFile, TLSKeyFile: keyFile},
		},
	}
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := app.Protocols, []string{"tcp"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("protocols = %#v, want %#v", got, want)
	}
}

func TestNewRegistersConfiguredTCPWithMTLS(t *testing.T) {
	certFile, keyFile := writeTestCertificate(t)
	caFile, _ := writeTestCertificate(t)
	cfg := Config{
		ShutdownTimeout: "2s",
		Protocols: []ProtocolConfig{
			{
				Name:            "tcp",
				Addr:            "127.0.0.1:0",
				TLSCertFile:     certFile,
				TLSKeyFile:      keyFile,
				TLSClientCAFile: caFile,
				TLSClientAuth:   "require_and_verify",
			},
		},
	}
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := app.Protocols, []string{"tcp"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("protocols = %#v, want %#v", got, want)
	}
}

func TestNewRegistersConfiguredQUICWithTLSFiles(t *testing.T) {
	certFile, keyFile := writeTestCertificate(t)
	cfg := Config{
		ShutdownTimeout: "2s",
		Protocols: []ProtocolConfig{
			{Name: "quic", Addr: "127.0.0.1:0", TLSCertFile: certFile, TLSKeyFile: keyFile},
		},
	}
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := app.Protocols, []string{"quic"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("protocols = %#v, want %#v", got, want)
	}
}

func TestConfigEnvOverrideQUIC(t *testing.T) {
	cfg := DefaultConfig()
	if err := applyEnv(&cfg, func(key string) (string, bool) {
		values := map[string]string{
			"SHARK_QUIC_ADDR":      "127.0.0.1:19088",
			"SHARK_QUIC_CERT_FILE": "server.crt",
			"SHARK_QUIC_KEY_FILE":  "server.key",
		}
		value, ok := values[key]
		return value, ok
	}); err != nil {
		t.Fatal(err)
	}
	var found ProtocolConfig
	for _, proto := range cfg.Protocols {
		if proto.Name == "quic" {
			found = proto
			break
		}
	}
	if found.Addr != "127.0.0.1:19088" || found.TLSCertFile != "server.crt" || found.TLSKeyFile != "server.key" {
		t.Fatalf("quic override = %#v", found)
	}
}

func TestConfigEnvOverrideTCPTLS(t *testing.T) {
	cfg := DefaultConfig()
	if err := applyEnv(&cfg, func(key string) (string, bool) {
		values := map[string]string{
			"SHARK_TCP_ADDR":      "127.0.0.1:19000",
			"SHARK_TCP_CERT_FILE": "server.crt",
			"SHARK_TCP_KEY_FILE":  "server.key",
		}
		value, ok := values[key]
		return value, ok
	}); err != nil {
		t.Fatal(err)
	}
	var found ProtocolConfig
	for _, proto := range cfg.Protocols {
		if proto.Name == "tcp" {
			found = proto
			break
		}
	}
	if found.Addr != "127.0.0.1:19000" || found.TLSCertFile != "server.crt" || found.TLSKeyFile != "server.key" {
		t.Fatalf("tcp override = %#v", found)
	}
}

func TestConfigEnvOverrideTCPMTLS(t *testing.T) {
	cfg := DefaultConfig()
	if err := applyEnv(&cfg, func(key string) (string, bool) {
		values := map[string]string{
			"SHARK_TCP_CERT_FILE":      "server.crt",
			"SHARK_TCP_KEY_FILE":       "server.key",
			"SHARK_TCP_CLIENT_CA_FILE": "ca.crt",
			"SHARK_TCP_CLIENT_AUTH":    "require_and_verify",
		}
		value, ok := values[key]
		return value, ok
	}); err != nil {
		t.Fatal(err)
	}
	var found ProtocolConfig
	for _, proto := range cfg.Protocols {
		if proto.Name == "tcp" {
			found = proto
			break
		}
	}
	if found.TLSClientCAFile != "ca.crt" || found.TLSClientAuth != "require_and_verify" {
		t.Fatalf("tcp mtls override = %#v", found)
	}
}

func TestConfigEnvOverrideTCPTLSKeepsDefaultAddr(t *testing.T) {
	cfg := DefaultConfig()
	if err := applyEnv(&cfg, func(key string) (string, bool) {
		values := map[string]string{
			"SHARK_TCP_CERT_FILE": "server.crt",
			"SHARK_TCP_KEY_FILE":  "server.key",
		}
		value, ok := values[key]
		return value, ok
	}); err != nil {
		t.Fatal(err)
	}
	var found ProtocolConfig
	for _, proto := range cfg.Protocols {
		if proto.Name == "tcp" {
			found = proto
			break
		}
	}
	if found.Addr != "127.0.0.1:18000" || found.TLSCertFile != "server.crt" || found.TLSKeyFile != "server.key" {
		t.Fatalf("tcp override = %#v", found)
	}
}

func TestConfigEnvOverrideAllowedOrigins(t *testing.T) {
	cfg := DefaultConfig()
	if err := applyEnv(&cfg, func(key string) (string, bool) {
		values := map[string]string{
			"SHARK_HTTP_ADDR":                 "127.0.0.1:19003",
			"SHARK_HTTP_ALLOWED_ORIGINS":      "https://api.example",
			"SHARK_WS_ADDR":                   "127.0.0.1:19004",
			"SHARK_WS_ALLOWED_ORIGINS":        "https://console.example, https://ops.example",
			"SHARK_GRPCWEB_ADDR":              "127.0.0.1:19009",
			"SHARK_GRPCWEB_ALLOWED_ORIGINS":   "https://grpc.example",
			"SHARK_GRPCWEB_MAX_MESSAGE_BYTES": "1024",
		}
		value, ok := values[key]
		return value, ok
	}); err != nil {
		t.Fatal(err)
	}
	var http, ws, grpcweb ProtocolConfig
	for _, proto := range cfg.Protocols {
		switch proto.Name {
		case "http":
			http = proto
		case "websocket":
			ws = proto
		case "grpc-web":
			grpcweb = proto
		}
	}
	if got, want := strings.Join(http.AllowedOrigins, ","), "https://api.example"; got != want {
		t.Fatalf("http allowed origins = %q, want %q", got, want)
	}
	if got, want := strings.Join(ws.AllowedOrigins, ","), "https://console.example,https://ops.example"; got != want {
		t.Fatalf("websocket allowed origins = %q, want %q", got, want)
	}
	if got, want := strings.Join(grpcweb.AllowedOrigins, ","), "https://grpc.example"; got != want {
		t.Fatalf("grpc-web allowed origins = %q, want %q", got, want)
	}
}

func TestAllowedOriginChecker(t *testing.T) {
	check := allowedOriginChecker([]string{"https://console.example"})
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Origin", "https://console.example")
	if !check(req) {
		t.Fatal("allowed origin was rejected")
	}
	req.Header.Set("Origin", "https://evil.example")
	if check(req) {
		t.Fatal("unexpected origin was allowed")
	}
	if !allowedOriginChecker([]string{"*"})(req) {
		t.Fatal("wildcard origin should be allowed")
	}
}

func TestLoadConfigRejectsInvalidGRPCWebMaxMessageBytesEnv(t *testing.T) {
	t.Setenv("SHARK_GRPCWEB_ADDR", "127.0.0.1:0")
	t.Setenv("SHARK_GRPCWEB_MAX_MESSAGE_BYTES", "not-a-number")
	if _, err := LoadConfig(""); err == nil || !strings.Contains(err.Error(), "SHARK_GRPCWEB_MAX_MESSAGE_BYTES") {
		t.Fatalf("LoadConfig error = %v, want env parse error", err)
	}
}

func TestNewRegistersConfiguredProtocols(t *testing.T) {
	enabled := true
	cfg := Config{
		ShutdownTimeout: "2s",
		Protocols: []ProtocolConfig{
			{Name: "tcp", Addr: "127.0.0.1:0"},
			{Name: "udp", Addr: "127.0.0.1:0", Enabled: &enabled},
			{Name: "websocket", Addr: "127.0.0.1:0", Path: "/ws"},
			{Name: "grpc-web", Addr: "127.0.0.1:0", Path: "/grpc/ws"},
		},
	}
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := app.Protocols, []string{"tcp", "udp", "websocket", "grpc-web"}; len(got) != len(want) {
		t.Fatalf("protocols = %#v, want %#v", got, want)
	}
}

func writeTestCertificate(t *testing.T) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	dir := t.TempDir()
	certFile := filepath.Join(dir, "server.crt")
	keyFile := filepath.Join(dir, "server.key")
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func TestHealthHandlerReadiness(t *testing.T) {
	app, err := New(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	healthHandler(app.Gateway).ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status before start = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}
