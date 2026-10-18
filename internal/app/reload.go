package app

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/plugin"
)

// ErrListenChangeRequiresRestart reports a reload that tried to move a
// listener. Listen addresses, TLS material and the protocol set are bound to
// sockets and handlers that were created at start; rebinding them in place
// would mean tearing down and rebuilding transports, which is the same work as
// a restart -- except with live traffic in flight.
var ErrListenChangeRequiresRestart = errors.New("app: listen configuration changes require a restart")

// Reload applies the hot-reloadable subset of cfg and leaves everything else
// running untouched.
//
// The supported subset is deliberately small, because each field in it has to
// answer one question: can the new value take effect without rebuilding a
// socket, a goroutine or a handler?
//
//   - log_level        a slog.LevelVar consulted per record
//   - blacklist        an atomically swapped set, read on accept
//   - rate_limit       per-message parameters held in atomics
//   - shutdown_timeout read once, at shutdown
//
// Everything else -- health_addr, metrics_addr, protocols (address, path, mode,
// TLS, CORS) -- needs the listeners those fields configure, so Reload rejects
// the whole document with ErrListenChangeRequiresRestart.
//
// The rejection is all-or-nothing: no hot field is applied. A half-applied
// configuration is the one outcome an operator cannot reason about, and
// "changed the blacklist on the same edit as the listen port" is exactly the
// kind of mixed edit that a partial apply would silently split in two.
func (a *App) Reload(cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	level, err := core.ParseLevel(cfg.LogLevel)
	if err != nil {
		return err
	}
	window := time.Second
	if cfg.RateLimit != nil {
		if window, err = cfg.RateLimit.WindowDuration(); err != nil {
			return err
		}
	}
	// Validate the blacklist again through the plugin's parser: Validate()
	// above uses the same code, but the state that matters here is "the new set
	// parses", and Blacklist.Replace is the component that has to accept it.
	if err := plugin.ValidateBlacklist(cfg.Blacklist); err != nil {
		return err
	}
	// A rate limiter that was not installed at startup cannot be added now, and
	// one that was installed cannot be removed: the plugin chain is a fixed
	// generation, and a message either runs through the limiter or does not.
	// Adjusting an installed limiter is fine -- that is the common case of
	// tightening a limit during an incident.
	if (a.rateLimit == nil) != (cfg.RateLimit == nil) {
		return fmt.Errorf("%w: enabling or disabling rate_limit adds or removes a plugin", ErrListenChangeRequiresRestart)
	}

	a.reloadMu.Lock()
	defer a.reloadMu.Unlock()

	if diff := listenDiff(a.Config, cfg); diff != "" {
		return fmt.Errorf("%w: %s", ErrListenChangeRequiresRestart, diff)
	}

	// Past this point nothing can fail: every step below is an atomic store.
	// Applying only after validation is what makes "a failed reload keeps the
	// old configuration" true rather than aspirational.
	if a.blacklist != nil {
		// Replace parses strictly, so a malformed entry leaves the previous set
		// in force. It cannot fail here -- ValidateBlacklist already parsed the
		// same slice -- but the error is propagated rather than discarded so a
		// future edit to Validate keeps the guarantee.
		if err := a.blacklist.Replace(cfg.Blacklist); err != nil {
			return err
		}
	}
	if a.rateLimit != nil && cfg.RateLimit != nil {
		a.rateLimit.SetLimit(cfg.RateLimit.PerSecond, window)
	}
	a.Logger.SetLevel(level)
	a.Config = cfg
	return nil
}

// ReloadFile re-reads the configuration file (and the environment overrides)
// and applies the hot subset. A parse or validation failure leaves the running
// configuration untouched; the caller is expected to log the returned error.
func (a *App) ReloadFile(path string) error {
	cfg, err := LoadConfig(path)
	if err != nil {
		return err
	}
	return a.Reload(cfg)
}

// CurrentConfig returns a copy of the configuration in force.
func (a *App) CurrentConfig() Config {
	a.reloadMu.Lock()
	defer a.reloadMu.Unlock()
	return a.Config
}

// ShutdownDuration reports the shutdown grace period currently configured. It
// reads the live configuration rather than the one captured at start, so a
// reloaded shutdown_timeout is honoured by the shutdown that follows it.
func (a *App) ShutdownDuration() time.Duration {
	timeout, err := a.CurrentConfig().ShutdownDuration()
	if err != nil {
		return 10 * time.Second
	}
	return timeout
}

// listenDiff names the fields of cfg that would need a restart, or returns ""
// when the two configurations differ only in hot-reloadable fields.
func listenDiff(running, next Config) string {
	var changed []string
	if running.HealthAddr != next.HealthAddr {
		changed = append(changed, "health_addr")
	}
	if running.MetricsAddr != next.MetricsAddr {
		changed = append(changed, "metrics_addr")
	}
	if diff := protocolsDiff(running.Protocols, next.Protocols); diff != "" {
		changed = append(changed, diff)
	}
	return strings.Join(changed, ", ")
}

// protocolsDiff compares the protocol set by name, so reordering a section in
// the file is not mistaken for a listener change. A protocol that was disabled
// counts as absent: its listener was never created.
func protocolsDiff(running, next []ProtocolConfig) string {
	current := enabledProtocols(running)
	incoming := enabledProtocols(next)
	for name, proto := range incoming {
		old, ok := current[name]
		if !ok {
			return "protocols." + name + " (added)"
		}
		if !reflect.DeepEqual(old, proto) {
			return "protocols." + name
		}
	}
	for name := range current {
		if _, ok := incoming[name]; !ok {
			return "protocols." + name + " (removed)"
		}
	}
	return ""
}

func enabledProtocols(protocols []ProtocolConfig) map[string]ProtocolConfig {
	out := make(map[string]ProtocolConfig, len(protocols))
	for _, proto := range protocols {
		if !proto.IsEnabled() {
			continue
		}
		out[strings.ToLower(strings.TrimSpace(proto.Name))] = proto
	}
	return out
}
