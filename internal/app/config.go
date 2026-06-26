package app

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/X1aSheng/shark-socket/internal/infra/tlsutil"
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
	TLSClientCAFile string   `json:"tls_client_ca_file,omitempty"`
	TLSClientAuth   string   `json:"tls_client_auth,omitempty"`
	TLSMinVersion   string   `json:"tls_min_version,omitempty"`
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
		if proto.TLSCertFile != "" && name != "tcp" && name != "quic" && name != "coap" && name != "udp" {
			return fmt.Errorf("protocol %q does not support tls_cert_file", name)
		}
		if proto.TLSClientCAFile != "" && name != "tcp" && name != "quic" && name != "coap" && name != "udp" {
			return fmt.Errorf("protocol %q does not support tls_client_ca_file", name)
		}
		if proto.TLSClientAuth != "" && name != "tcp" && name != "quic" && name != "coap" && name != "udp" {
			return fmt.Errorf("protocol %q does not support tls_client_auth", name)
		}
		if proto.TLSClientCAFile != "" && proto.TLSCertFile == "" {
			return fmt.Errorf("protocol %q tls_client_ca_file requires tls_cert_file and tls_key_file", name)
		}
		if proto.TLSClientAuth != "" {
			if _, err := parseTLSClientAuth(proto.TLSClientAuth); err != nil {
				return fmt.Errorf("protocol %q %w", name, err)
			}
			if proto.TLSCertFile == "" {
				return fmt.Errorf("protocol %q tls_client_auth requires tls_cert_file and tls_key_file", name)
			}
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
	tcpClientCAFile, hasTCPClientCAFile := lookup("SHARK_TCP_CLIENT_CA_FILE")
	tcpClientAuth, hasTCPClientAuth := lookup("SHARK_TCP_CLIENT_AUTH")
	tcpTLSMinVersion, hasTCPTLSMinVersion := lookup("SHARK_TCP_TLS_MIN_VERSION")
	if hasTCPAddr || hasTCPCertFile || hasTCPKeyFile || hasTCPClientCAFile || hasTCPClientAuth || hasTCPTLSMinVersion {
		upsertProtocol(cfg, ProtocolConfig{
			Name:            "tcp",
			Addr:            tcpAddr,
			TLSCertFile:     tcpCertFile,
			TLSKeyFile:      tcpKeyFile,
			TLSClientCAFile: tcpClientCAFile,
			TLSClientAuth:   tcpClientAuth,
			TLSMinVersion:   tcpTLSMinVersion,
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
			Name:            "quic",
			Addr:            value,
			TLSCertFile:     envOrDefault(lookup, "SHARK_QUIC_CERT_FILE", ""),
			TLSKeyFile:      envOrDefault(lookup, "SHARK_QUIC_KEY_FILE", ""),
			TLSClientCAFile: envOrDefault(lookup, "SHARK_QUIC_CLIENT_CA_FILE", ""),
			TLSClientAuth:   envOrDefault(lookup, "SHARK_QUIC_CLIENT_AUTH", ""),
			TLSMinVersion:   envOrDefault(lookup, "SHARK_QUIC_TLS_MIN_VERSION", ""),
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
	if override.TLSClientCAFile != "" {
		base.TLSClientCAFile = override.TLSClientCAFile
	}
	if override.TLSClientAuth != "" {
		base.TLSClientAuth = override.TLSClientAuth
	}
		if override.TLSMinVersion != "" {
			base.TLSMinVersion = override.TLSMinVersion
		}
	if override.AllowedOrigins != nil {
		base.AllowedOrigins = append([]string(nil), override.AllowedOrigins...)
	}
	return base
}

func loadServerTLSConfig(proto ProtocolConfig, nextProtos ...string) (*tls.Config, *tlsutil.CertCache, error) {
	cache := tlsutil.NewCertCache(proto.TLSCertFile, proto.TLSKeyFile)
	if proto.TLSClientCAFile != "" {
		cache.SetClientCA(proto.TLSClientCAFile)
	}
	if err := cache.Load(); err != nil {
		return nil, nil, fmt.Errorf("load tls certificate: %w", err)
	}
	cfg := &tls.Config{
		GetCertificate: cache.GetCertificate,
		NextProtos:     nextProtos,
		MinVersion:     parseTLSMinVersion(proto.TLSMinVersion),
	}
	if proto.TLSClientAuth != "" {
		clientAuth, err := parseTLSClientAuth(proto.TLSClientAuth)
		if err != nil {
			return nil, nil, err
		}
		cfg.ClientAuth = clientAuth
	}
	if pool := cache.GetClientCAPool(); pool != nil {
		cfg.ClientCAs = pool
	}
	return cfg, cache, nil
}

// parseTLSMinVersion converts a version string ("1.2", "1.3") to a tls version constant.
// Returns tls.VersionTLS12 by default for backward compatibility.
func parseTLSMinVersion(version string) uint16 {
	switch strings.TrimSpace(version) {
	case "1.3", "13":
		return tls.VersionTLS13
	case "1.2", "12", "":
		return tls.VersionTLS12
	default:
		// Versions below 1.2 are insecure; default to 1.2.
		return tls.VersionTLS12
	}
}

func parseTLSClientAuth(value string) (tls.ClientAuthType, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "none", "no_client_cert":
		return tls.NoClientCert, nil
	case "request", "request_client_cert":
		return tls.RequestClientCert, nil
	case "require_any", "require_any_client_cert":
		return tls.RequireAnyClientCert, nil
	case "verify_if_given", "verify_client_cert_if_given":
		return tls.VerifyClientCertIfGiven, nil
	case "require_and_verify", "require_and_verify_client_cert":
		return tls.RequireAndVerifyClientCert, nil
	default:
		return tls.NoClientCert, fmt.Errorf("invalid tls_client_auth %q", value)
	}
}
