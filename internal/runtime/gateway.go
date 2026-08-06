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
	mu        sync.RWMutex
	startMu   sync.Mutex // serializes Start, Stop and Register against each other
	servers   map[core.Protocol]core.Server
	order     []core.Protocol
	rt        *Runtime
	timeouts  core.StageTimeouts
	startedAt atomic.Pointer[time.Time]
	started   atomic.Bool
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
	return &Gateway{
		servers:  make(map[core.Protocol]core.Server),
		rt:       NewRuntime(cfg.sessions, cfg.plugins, withRuntimeLogger(cfg.logger), withRuntimeMetrics(cfg.metrics), withRuntimeTracer(cfg.tracer)),
		timeouts: normalizeTimeouts(cfg.timeouts),
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
	started := make([]core.Server, 0, len(servers))
	for _, srv := range servers {
		if err := srv.Start(ctx); err != nil {
			g.rt.Logger().Error("server start failed", "protocol", srv.Protocol(), "error", err)
			rollbackCtx, cancel := context.WithTimeout(context.Background(), g.timeouts.Finalize)
			for i := len(started) - 1; i >= 0; i-- {
				if stopErr := started[i].Stop(rollbackCtx); stopErr != nil {
					g.rt.Logger().Error("rollback stop failed", "protocol", started[i].Protocol(), "error", stopErr)
				}
			}
			cancel()
			return err
		}
		started = append(started, srv)
	}
	now := time.Now()
	g.startedAt.Store(&now)
	g.started.Store(true)
	return nil
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
		if err := srv.Stop(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := g.rt.Sessions().CloseAll(ctx); err != nil && firstErr == nil {
		firstErr = err
	}
	// started was already cleared at the top of Stop so readyz reports
	// not-ready for the whole shutdown window; only the uptime is stale here.
	g.startedAt.Store(nil)
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
