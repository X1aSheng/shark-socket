package runtime

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type Gateway struct {
	mu       sync.RWMutex
	startMu  sync.Mutex // serializes Start, Stop and Register against each other
	servers  map[core.Protocol]core.Server
	order    []core.Protocol
	rt       *Runtime
	timeouts core.StageTimeouts
	adminCfg AdminConfig
	admin    *adminServer // set while the admin listener is running
	// rawSessions is the manager before metricSessionManager wraps it. The
	// decorator embeds the core.SessionManager interface, so methods that only
	// the concrete type has - Capacity, today - are not reachable through it.
	rawSessions core.SessionManager
	startedAt   atomic.Pointer[time.Time]
	started     atomic.Bool
}

func NewGateway(opts ...GatewayOption) *Gateway {
	cfg := gatewayConfig{
		sessions: NewSessionManager(),
		plugins:  NewPluginChain(),
		logger:   core.NopLogger(),
		metrics:  core.NopMetrics(),
		tracer:   core.NopTracer(),
		timeouts: defaultStageTimeouts(),
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if chain, ok := cfg.plugins.(*PluginChain); ok {
		chain.SetLogger(cfg.logger)
	}
	// Install a metrics decorator so every transport's session register/
	// unregister flows into the configured metrics backend (Prometheus
	// exporter, in-memory metrics, ...) without per-server metric calls.
	sessions := cfg.sessions
	if cfg.metrics != nil {
		sessions = &metricSessionManager{SessionManager: cfg.sessions, metrics: cfg.metrics}
	}
	return &Gateway{
		servers:     make(map[core.Protocol]core.Server),
		rt:          NewRuntime(sessions, cfg.plugins, withRuntimeLogger(cfg.logger), withRuntimeMetrics(cfg.metrics), withRuntimeTracer(cfg.tracer)),
		timeouts:    normalizeTimeouts(cfg.timeouts),
		adminCfg:    cfg.admin,
		rawSessions: cfg.sessions,
	}
}

func (g *Gateway) Register(server core.Server) error {
	if server == nil {
		return core.ErrNoServers
	}
	g.startMu.Lock()
	defer g.startMu.Unlock()
	if g.started.Load() {
		return fmt.Errorf("gateway: cannot register %q after Start", server.Protocol())
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	proto := server.Protocol()
	if _, exists := g.servers[proto]; exists {
		return core.ErrDuplicateProtocol
	}
	g.servers[proto] = server
	g.order = append(g.order, proto)
	return nil
}

func (g *Gateway) Runtime() *Runtime {
	return g.rt
}

func (g *Gateway) Start(ctx context.Context) error {
	g.startMu.Lock()
	defer g.startMu.Unlock()
	if g.started.Load() {
		return fmt.Errorf("gateway: already started")
	}
	servers := g.snapshot()
	if len(servers) == 0 {
		return core.ErrNoServers
	}
	for _, srv := range servers {
		if configurable, ok := srv.(core.RuntimeConfigurable); ok {
			configurable.UseRuntime(g.rt)
		}
	}
	// Start lifecycle-aware plugins (AutoBan/RateLimit/Heartbeat/Cluster and
	// user plugins implementing Start/Stop) before any server accepts traffic,
	// so their cleanup/consume loops are running from the first message.
	// On failure nothing has been started yet and StartAll rolls back.
	if err := g.rt.Plugins().StartAll(); err != nil {
		return err
	}
	started := make([]core.Server, 0, len(servers))
	for _, srv := range servers {
		if err := srv.Start(ctx); err != nil {
			g.rt.Logger().Error("server start failed", "protocol", srv.Protocol(), "error", err)
			g.rollbackStart(started)
			return err
		}
		started = append(started, srv)
	}
	// The admin listener comes up last: it reports on servers, so it should not
	// be answering while they are still half-started.
	if g.adminCfg.Addr != "" {
		admin := newAdminServer(g, g.adminCfg)
		if err := admin.start(); err != nil {
			g.rollbackStart(started)
			return err
		}
		g.admin = admin
	}
	now := time.Now()
	g.startedAt.Store(&now)
	g.started.Store(true)
	return nil
}

// rollbackStart undoes a partial Start: servers in reverse order, then the
// plugin chain. Errors are logged rather than returned, because the caller
// already has the failure that triggered the rollback and that is the one worth
// reporting.
func (g *Gateway) rollbackStart(started []core.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), g.timeouts.Finalize)
	defer cancel()
	for i := len(started) - 1; i >= 0; i-- {
		if err := started[i].Stop(ctx); err != nil {
			g.rt.Logger().Error("rollback stop failed", "protocol", started[i].Protocol(), "error", err)
		}
	}
	if err := g.rt.Plugins().StopAll(); err != nil {
		g.rt.Logger().Error("plugin stop during rollback failed", "error", err)
	}
}

func (g *Gateway) Stop(ctx context.Context) error {
	g.startMu.Lock()
	defer g.startMu.Unlock()

	// Mark not-started up front so Register() is rejected and Ready()/readyz
	// report not-ready for the whole shutdown window.
	g.started.Store(false)

	servers := g.snapshot()
	var firstErr error

	for _, srv := range servers {
		if staged, ok := srv.(core.StagedServer); ok {
			if err := runStage(ctx, g.timeouts.StopAccept, staged.StopAccept); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	for _, srv := range servers {
		if staged, ok := srv.(core.StagedServer); ok {
			if err := runStage(ctx, g.timeouts.Drain, staged.Drain); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	for _, srv := range servers {
		if staged, ok := srv.(core.StagedServer); ok {
			if err := runStage(ctx, g.timeouts.CloseSessions, staged.CloseSessions); err != nil && firstErr == nil {
				firstErr = err
			}
			continue
		}
		// Non-staged servers get the same CloseSessions deadline; otherwise a
		// Stop that blocks on I/O would hang indefinitely (and hold startMu,
		// blocking Register) when the caller's ctx has no deadline.
		if err := runStage(ctx, g.timeouts.CloseSessions, func(stageCtx context.Context) error {
			return srv.Stop(stageCtx)
		}); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := runStage(ctx, g.timeouts.CloseSessions, func(stageCtx context.Context) error {
		return g.rt.Sessions().CloseAll(stageCtx)
	}); err != nil && firstErr == nil {
		firstErr = err
	}
	// Stop lifecycle-aware plugins only after every session is closed, so
	// plugin background loops (heartbeat sweeps, cluster consumers) never
	// race the transport teardown above. Stop failures and plugin panics are
	// aggregated into the returned error instead of being discarded: a
	// plugin that failed to release resources must be visible to the caller.
	if err := g.rt.Plugins().StopAll(); err != nil {
		g.rt.Logger().Error("plugin stop failed", "error", err)
		if firstErr == nil {
			firstErr = err
		}
	}
	// started was already cleared at the top of Stop so readyz reports
	// not-ready for the whole shutdown window; only the uptime is stale here.
	g.startedAt.Store(nil)
	// The admin listener is stopped last so the drain is observable through
	// /sessions while it happens. Stop never returns early above - it
	// accumulates firstErr - so this still runs when a stage timed out.
	if g.admin != nil {
		if err := g.admin.stop(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (g *Gateway) Health() map[string]any {
	started := g.started.Load()
	resp := map[string]any{
		"started":  started,
		"sessions": g.rt.Sessions().Count(),
	}
	// Only report start time/uptime while the gateway is started, so a Health
	// call during the shutdown window cannot return started=false together
	// with a live uptime.
	if started {
		if startedAt := g.startedAt.Load(); startedAt != nil {
			resp["started_at"] = startedAt.Format(time.RFC3339Nano)
			resp["uptime"] = time.Since(*startedAt).String()
		}
	}
	resp["protocols"] = g.Protocols()
	return resp
}

func (g *Gateway) Ready() bool {
	return g.started.Load()
}

func (g *Gateway) Protocols() []core.Protocol {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return append([]core.Protocol(nil), g.order...)
}

func (g *Gateway) snapshot() []core.Server {
	g.mu.RLock()
	defer g.mu.RUnlock()
	servers := make([]core.Server, 0, len(g.order))
	for _, proto := range g.order {
		servers = append(servers, g.servers[proto])
	}
	return servers
}

func runStage(parent context.Context, timeout time.Duration, fn func(context.Context) error) error {
	ctx := parent
	cancel := func() {}
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, timeout)
	}
	defer cancel()
	return fn(ctx)
}
