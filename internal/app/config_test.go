package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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
	applyEnv(&cfg, func(key string) (string, bool) {
		values := map[string]string{
			"SHARK_TCP_ADDR":     "127.0.0.1:19000",
			"SHARK_HEALTH_ADDR":  "127.0.0.1:19081",
			"SHARK_METRICS_ADDR": "127.0.0.1:19080",
		}
		value, ok := values[key]
		return value, ok
	})
	if cfg.Protocols[0].Addr != "127.0.0.1:19000" {
		t.Fatalf("tcp addr = %q", cfg.Protocols[0].Addr)
	}
	if cfg.HealthAddr != "127.0.0.1:19081" || cfg.MetricsAddr != "127.0.0.1:19080" {
		t.Fatalf("health=%q metrics=%q", cfg.HealthAddr, cfg.MetricsAddr)
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
