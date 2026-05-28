package runtime

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket-new/internal/core"
)

type Gateway struct {
	mu        sync.RWMutex
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
	return &Gateway{
		servers:  make(map[core.Protocol]core.Server),
		rt:       NewRuntime(cfg.sessions, cfg.plugins, withRuntimeLogger(cfg.logger), withRuntimeMetrics(cfg.metrics), withRuntimeTracer(cfg.tracer)),
		timeouts: normalizeTimeouts(cfg.timeouts),
	}
}

func (g *Gateway) Register(server core.Server) error {
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
				_ = started[i].Stop(rollbackCtx)
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
	g.started.Store(false)
	return firstErr
}

func (g *Gateway) Health() map[string]any {
	resp := map[string]any{
		"started":  g.started.Load(),
		"sessions": g.rt.Sessions().Count(),
	}
	if startedAt := g.startedAt.Load(); startedAt != nil {
		resp["started_at"] = startedAt.Format(time.RFC3339Nano)
		resp["uptime"] = time.Since(*startedAt).String()
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
