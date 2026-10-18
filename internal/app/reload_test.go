package app

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// freeAddr reserves an ephemeral port and releases it, so the config can name a
// concrete address that the test can then dial. The reservation window is
// unavoidable without exposing the server's bound address through App, which
// would widen the API for the sake of a test.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("release port: %v", err)
	}
	return addr
}

func startTestApp(t *testing.T, cfg Config) *App {
	t.Helper()
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := a.Start(ctx); err != nil {
		cancel()
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = a.Stop(stopCtx)
		cancel()
	})
	return a
}

func dial(t *testing.T, addr string) net.Conn {
	t.Helper()
	var conn net.Conn
	var err error
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err = net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			t.Cleanup(func() { _ = conn.Close() })
			return conn
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("dial %s: %v", addr, err)
	return nil
}

// exchange writes one length-prefixed frame and returns the frame the server
// echoed. The app's TCP server uses the default LengthPrefixFramer (big-endian
// uint32), so a bare write would be read as a length header and end in a short
// read. A dropped message shows up as an error on the reply header, which is
// what the rate-limit and blacklist assertions rely on.
func exchange(conn net.Conn, payload string, timeout time.Duration) (string, error) {
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return "", err
	}
	frame := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(frame, uint32(len(payload)))
	copy(frame[4:], payload)
	if _, err := conn.Write(frame); err != nil {
		return "", err
	}
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return "", err
	}
	buf := make([]byte, binary.BigEndian.Uint32(header[:]))
	if _, err := io.ReadFull(conn, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func tcpConfig(addr string) Config {
	return Config{
		LogLevel: "info",
		Protocols: []ProtocolConfig{
			{Name: "tcp", Addr: addr},
		},
	}
}

// TestReloadBlacklistAppliesToNewConnectionsOnly is the core C6 acceptance:
// after a reload the new block list is in force for connections that arrive
// afterwards, while a connection that was already accepted keeps working -- the
// check runs on accept, so nothing reaches into a live session.
func TestReloadBlacklistAppliesToNewConnectionsOnly(t *testing.T) {
	addr := freeAddr(t)
	a := startTestApp(t, tcpConfig(addr))

	existing := dial(t, addr)
	if got, err := exchange(existing, "ping", 2*time.Second); err != nil || got != "ping" {
		t.Fatalf("before reload: got %q, err %v", got, err)
	}
	// Positive control: the same operation succeeds before the reload, so the
	// failure below can only come from the new block list.
	control := dial(t, addr)
	if got, err := exchange(control, "ping", 2*time.Second); err != nil || got != "ping" {
		t.Fatalf("control connection before reload: got %q, err %v", got, err)
	}
	_ = control.Close()

	next := tcpConfig(addr)
	next.Blacklist = []string{"127.0.0.1"}
	if err := a.Reload(next); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	// Existing connection: unaffected, because OnAccept is not re-evaluated.
	if got, err := exchange(existing, "pong", 2*time.Second); err != nil || got != "pong" {
		t.Fatalf("existing connection after reload: got %q, err %v", got, err)
	}

	// New connection: refused at accept. The session never reaches the
	// handler, so the write earns nothing but a closed socket. The elapsed
	// bound separates "closed" from "silently dropped", which would block
	// until the read deadline instead.
	blocked := dial(t, addr)
	start := time.Now()
	got, err := exchange(blocked, "ping", 2*time.Second)
	elapsed := time.Since(start)
	if err == nil && got == "ping" {
		t.Fatal("new connection was served although its address is blacklisted")
	}
	if elapsed > time.Second {
		t.Fatalf("blocked connection lingered %v; expected an immediate close", elapsed)
	}
}

// TestReloadRejectsListenChangeWithoutApplyingHotFields proves the reload is
// all-or-nothing: an edit that moves a listener is rejected, and the hot fields
// in the same document are not applied either.
func TestReloadRejectsListenChangeWithoutApplyingHotFields(t *testing.T) {
	addr := freeAddr(t)
	a := startTestApp(t, tcpConfig(addr))

	next := tcpConfig(freeAddr(t))
	next.Blacklist = []string{"127.0.0.1"}
	next.LogLevel = "debug"

	err := a.Reload(next)
	if !errors.Is(err, ErrListenChangeRequiresRestart) {
		t.Fatalf("Reload err = %v, want ErrListenChangeRequiresRestart", err)
	}
	if got := a.CurrentConfig().LogLevel; got != "info" {
		t.Fatalf("log level = %q after a rejected reload, want info", got)
	}
	if got := a.Logger.Level(); got != slog.LevelInfo {
		t.Fatalf("logger level = %v after a rejected reload, want info", got)
	}
	// The blacklist from the rejected document must not leak through: a
	// connection from the blocked address is still served.
	conn := dial(t, addr)
	if got, err := exchange(conn, "ping", 2*time.Second); err != nil || got != "ping" {
		t.Fatalf("connection after rejected reload: got %q, err %v", got, err)
	}
}

// TestReloadInvalidConfigKeepsRunningConfig covers the "失败则保留旧配置" half:
// a document that fails validation is discarded whole.
func TestReloadInvalidConfigKeepsRunningConfig(t *testing.T) {
	addr := freeAddr(t)
	cfg := tcpConfig(addr)
	cfg.Blacklist = []string{"192.0.2.10"}
	a := startTestApp(t, cfg)

	bad := tcpConfig(addr)
	bad.Blacklist = []string{"not-an-ip"}
	bad.LogLevel = "debug"
	if err := a.Reload(bad); err == nil {
		t.Fatal("Reload accepted an invalid blacklist entry")
	}
	if got := a.CurrentConfig(); len(got.Blacklist) != 1 || got.Blacklist[0] != "192.0.2.10" {
		t.Fatalf("blacklist = %v after failed reload, want the previous entry", got.Blacklist)
	}
	if got := a.Logger.Level(); got != slog.LevelInfo {
		t.Fatalf("logger level = %v after failed reload, want info", got)
	}

	// An unknown log level is rejected the same way.
	badLevel := tcpConfig(addr)
	badLevel.LogLevel = "trace"
	if err := a.Reload(badLevel); err == nil {
		t.Fatal("Reload accepted an unknown log level")
	}
}

// TestReloadRateLimitTakesEffectOnNextMessage checks the two sides of a limit
// change: a tightened limit drops traffic, a loosened one lets the very next
// message through, and the counters are not reset in between.
func TestReloadRateLimitTakesEffectOnNextMessage(t *testing.T) {
	addr := freeAddr(t)
	cfg := tcpConfig(addr)
	cfg.RateLimit = &RateLimitConfig{PerSecond: 2, Window: "1s"}
	a := startTestApp(t, cfg)

	conn := dial(t, addr)
	for i := 0; i < 2; i++ {
		if got, err := exchange(conn, "ping", time.Second); err != nil || got != "ping" {
			t.Fatalf("message %d: got %q, err %v", i+1, got, err)
		}
	}
	// Third message in the same window: dropped by the limiter.
	if got, err := exchange(conn, "ping", 300*time.Millisecond); err == nil {
		t.Fatalf("message 3 was served (%q) although the limit is 2/s", got)
	}

	loosened := tcpConfig(addr)
	loosened.RateLimit = &RateLimitConfig{PerSecond: 1000, Window: "1s"}
	loosened.LogLevel = "debug"
	loosened.ShutdownTimeout = "3s"
	if err := a.Reload(loosened); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := a.Logger.Level(); got != slog.LevelDebug {
		t.Fatalf("logger level = %v after reload, want debug", got)
	}
	if got := a.ShutdownDuration(); got != 3*time.Second {
		t.Fatalf("shutdown duration = %v after reload, want 3s", got)
	}
	if got, err := exchange(conn, "ping", time.Second); err != nil || got != "ping" {
		t.Fatalf("message after loosening the limit: got %q, err %v", got, err)
	}
}

// TestReloadCannotAddRateLimit documents the boundary: the plugin chain is
// fixed at start, so turning the limiter on is a restart, not a reload.
func TestReloadCannotAddRateLimit(t *testing.T) {
	addr := freeAddr(t)
	a := startTestApp(t, tcpConfig(addr))

	next := tcpConfig(addr)
	next.RateLimit = &RateLimitConfig{PerSecond: 10}
	err := a.Reload(next)
	if !errors.Is(err, ErrListenChangeRequiresRestart) {
		t.Fatalf("Reload err = %v, want ErrListenChangeRequiresRestart", err)
	}
}

// TestReloadFile covers the SIGHUP path end to end: read the file again, apply
// what can be applied, and leave the process alone when the file is unreadable.
// The app is started from the same file so that the health and metrics
// addresses baked into DefaultConfig match what the reload sees.
func TestReloadFile(t *testing.T) {
	addr := freeAddr(t)
	path := filepath.Join(t.TempDir(), "config.json")
	writeFile := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
	}
	writeFile(`{"protocols": [{"name": "tcp", "addr": "` + addr + `"}]}`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	a := startTestApp(t, cfg)
	writeFile(`{
		"log_level": "warn",
		"blacklist": ["198.51.100.7"],
		"protocols": [{"name": "tcp", "addr": "` + addr + `"}]
	}`)
	if err := a.ReloadFile(path); err != nil {
		t.Fatalf("ReloadFile: %v", err)
	}
	if got := a.Logger.Level(); got != slog.LevelWarn {
		t.Fatalf("logger level = %v, want warn", got)
	}
	if got := a.CurrentConfig().Blacklist; len(got) != 1 || got[0] != "198.51.100.7" {
		t.Fatalf("blacklist = %v, want the reloaded entry", got)
	}

	writeFile(`{"protocols": [`)
	if err := a.ReloadFile(path); err == nil {
		t.Fatal("ReloadFile accepted a truncated document")
	}
	if got := a.Logger.Level(); got != slog.LevelWarn {
		t.Fatalf("logger level = %v after a broken file, want warn preserved", got)
	}
}
