package app

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ShutdownTimeout string           `json:"shutdown_timeout"`
	HealthAddr      string           `json:"health_addr"`
	MetricsAddr     string           `json:"metrics_addr"`
	Protocols       []ProtocolConfig `json:"protocols"`
}

type ProtocolConfig struct {
	Name            string   `json:"name"`
	Enabled         *bool    `json:"enabled,omitempty"`
	Addr            string   `json:"addr"`
	Path            string   `json:"path,omitempty"`
	Mode            string   `json:"mode,omitempty"`
	MaxMessageBytes int64    `json:"max_message_bytes,omitempty"`
	TLSCertFile     string   `json:"tls_cert_file,omitempty"`
	TLSKeyFile      string   `json:"tls_key_file,omitempty"`
	AllowedOrigins  []string `json:"allowed_origins,omitempty"`
}

func DefaultConfig() Config {
	return Config{
		ShutdownTimeout: "10s",
		HealthAddr:      "127.0.0.1:18081",
		MetricsAddr:     "127.0.0.1:18080",
		Protocols: []ProtocolConfig{
			{Name: "tcp", Addr: "127.0.0.1:18000"},
		},
	}
}

func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config %s: %w", path, err)
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse config %s: %w", path, err)
		}
	}
	if err := applyEnv(&cfg, os.LookupEnv); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) ShutdownDuration() (time.Duration, error) {
	if c.ShutdownTimeout == "" {
		return 10 * time.Second, nil
	}
	timeout, err := time.ParseDuration(c.ShutdownTimeout)
	if err != nil {
		return 0, fmt.Errorf("invalid shutdown_timeout %q: %w", c.ShutdownTimeout, err)
	}
	if timeout <= 0 {
		return 0, fmt.Errorf("shutdown_timeout must be positive")
	}
	return timeout, nil
}

func (c Config) Validate() error {
	if _, err := c.ShutdownDuration(); err != nil {
		return err
	}
	enabled := 0
	seen := map[string]bool{}
	for _, proto := range c.Protocols {
		if !proto.IsEnabled() {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(proto.Name))
		if name == "" {
			return fmt.Errorf("protocol name is required")
		}
		if seen[name] {
			return fmt.Errorf("duplicate protocol %q", name)
		}
		seen[name] = true
		if proto.Addr == "" {
			return fmt.Errorf("protocol %q addr is required", name)
		}
		if proto.MaxMessageBytes < 0 {
			return fmt.Errorf("protocol %q max_message_bytes must not be negative", name)
		}
		if (proto.TLSCertFile == "") != (proto.TLSKeyFile == "") {
			return fmt.Errorf("protocol %q tls_cert_file and tls_key_file must be supplied together", name)
		}
		if proto.TLSCertFile != "" && name != "tcp" && name != "quic" {
			return fmt.Errorf("protocol %q does not support tls_cert_file", name)
		}
		if name == "quic" && (proto.TLSCertFile == "" || proto.TLSKeyFile == "") {
			return fmt.Errorf("protocol %q tls_cert_file and tls_key_file are required", name)
		}
		switch name {
		case "tcp", "udp", "http", "websocket", "coap", "grpc-web", "quic":
		default:
			return fmt.Errorf("unsupported protocol %q", proto.Name)
		}
		enabled++
	}
	if enabled == 0 {
		return fmt.Errorf("at least one protocol must be enabled")
	}
	return nil
}

func (p ProtocolConfig) IsEnabled() bool {
	return p.Enabled == nil || *p.Enabled
}

func applyEnv(cfg *Config, lookup func(string) (string, bool)) error {
	if value, ok := lookup("SHARK_SHUTDOWN_TIMEOUT"); ok {
		cfg.ShutdownTimeout = value
	}
	if value, ok := lookup("SHARK_HEALTH_ADDR"); ok {
		cfg.HealthAddr = value
	}
	if value, ok := lookup("SHARK_METRICS_ADDR"); ok {
		cfg.MetricsAddr = value
	}
	httpAddr, hasHTTPAddr := lookup("SHARK_HTTP_ADDR")
	httpOrigins := splitCSVEnv(lookup, "SHARK_HTTP_ALLOWED_ORIGINS")
	if hasHTTPAddr || httpOrigins != nil {
		upsertProtocol(cfg, ProtocolConfig{
			Name:           "http",
			Addr:           httpAddr,
			AllowedOrigins: httpOrigins,
		})
	}
	tcpAddr, hasTCPAddr := lookup("SHARK_TCP_ADDR")
	tcpCertFile, hasTCPCertFile := lookup("SHARK_TCP_CERT_FILE")
	tcpKeyFile, hasTCPKeyFile := lookup("SHARK_TCP_KEY_FILE")
	if hasTCPAddr || hasTCPCertFile || hasTCPKeyFile {
		upsertProtocol(cfg, ProtocolConfig{
			Name:        "tcp",
			Addr:        tcpAddr,
			TLSCertFile: tcpCertFile,
			TLSKeyFile:  tcpKeyFile,
		})
	}
	if value, ok := lookup("SHARK_WS_ADDR"); ok {
		upsertProtocol(cfg, ProtocolConfig{
			Name:           "websocket",
			Addr:           value,
			Path:           envOrDefault(lookup, "SHARK_WS_PATH", "/ws"),
			AllowedOrigins: splitCSVEnv(lookup, "SHARK_WS_ALLOWED_ORIGINS"),
		})
	}
	if value, ok := lookup("SHARK_GRPCWEB_ADDR"); ok {
		proto := ProtocolConfig{
			Name:           "grpc-web",
			Addr:           value,
			Path:           envOrDefault(lookup, "SHARK_GRPCWEB_PATH", "/grpc"),
			AllowedOrigins: splitCSVEnv(lookup, "SHARK_GRPCWEB_ALLOWED_ORIGINS"),
		}
		if max, found := lookup("SHARK_GRPCWEB_MAX_MESSAGE_BYTES"); found {
			parsed, err := strconv.ParseInt(max, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid SHARK_GRPCWEB_MAX_MESSAGE_BYTES %q: %w", max, err)
			}
			proto.MaxMessageBytes = parsed
		}
		upsertProtocol(cfg, proto)
	}
	if value, ok := lookup("SHARK_QUIC_ADDR"); ok {
		upsertProtocol(cfg, ProtocolConfig{
			Name:        "quic",
			Addr:        value,
			TLSCertFile: envOrDefault(lookup, "SHARK_QUIC_CERT_FILE", ""),
			TLSKeyFile:  envOrDefault(lookup, "SHARK_QUIC_KEY_FILE", ""),
		})
	}
	return nil
}

func envOrDefault(lookup func(string) (string, bool), key string, fallback string) string {
	if value, ok := lookup(key); ok && value != "" {
		return value
	}
	return fallback
}

func splitCSVEnv(lookup func(string) (string, bool), key string) []string {
	value, ok := lookup(key)
	if !ok {
		return nil
	}
	parts := strings.Split(value, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

func upsertProtocol(cfg *Config, proto ProtocolConfig) {
	for i := range cfg.Protocols {
		if strings.EqualFold(cfg.Protocols[i].Name, proto.Name) {
			cfg.Protocols[i] = mergeProtocol(cfg.Protocols[i], proto)
			return
		}
	}
	cfg.Protocols = append(cfg.Protocols, proto)
}

func mergeProtocol(base, override ProtocolConfig) ProtocolConfig {
	base.Name = override.Name
	if override.Enabled != nil {
		base.Enabled = override.Enabled
	}
	if override.Addr != "" {
		base.Addr = override.Addr
	}
	if override.Path != "" {
		base.Path = override.Path
	}
	if override.Mode != "" {
		base.Mode = override.Mode
	}
	if override.MaxMessageBytes > 0 {
		base.MaxMessageBytes = override.MaxMessageBytes
	}
	if override.TLSCertFile != "" {
		base.TLSCertFile = override.TLSCertFile
	}
	if override.TLSKeyFile != "" {
		base.TLSKeyFile = override.TLSKeyFile
	}
	if override.AllowedOrigins != nil {
		base.AllowedOrigins = append([]string(nil), override.AllowedOrigins...)
	}
	return base
}

func loadServerTLSConfig(certFile string, keyFile string, nextProtos ...string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load tls certificate: %w", err)
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: nextProtos}, nil
}
