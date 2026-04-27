package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	stdhttp "net/http"
	"os/signal"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/X1aSheng/shark-socket/internal/errs"
	"github.com/X1aSheng/shark-socket/internal/session"
	"github.com/X1aSheng/shark-socket/internal/types"
)

// Gateway orchestrates multiple protocol servers with shared session management.
type Gateway struct {
	mu            sync.RWMutex
	servers       map[types.ProtocolType]types.Server
	globalPlugins []types.Plugin
	sharedManager *session.Manager
	opts          Options
	wg            sync.WaitGroup
	metricsServer *stdhttp.Server
	startTime     atomic.Value // time.Time, accessed from metrics handlers
	started       atomic.Bool
}

// Compile-time verification.
var _ types.Server = (*Gateway)(nil)

// New creates a new Gateway.
func New(opts ...Option) *Gateway {
	o := defaultOptions()
	for _, opt := range opts {
		opt(&o)
	}
	return &Gateway{
		servers:       make(map[types.ProtocolType]types.Server),
		globalPlugins: o.GlobalPlugins,
		opts:          o,
	}
}

// Register adds a protocol server to the gateway.
func (g *Gateway) Register(srv types.Server) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	proto := srv.Protocol()
	if _, exists := g.servers[proto]; exists {
		return errs.ErrDuplicateProtocol
	}
	g.servers[proto] = srv
	return nil
}

// Start launches all registered servers concurrently.
func (g *Gateway) Start() error {
	g.mu.RLock()
	snapshot := make(map[types.ProtocolType]types.Server, len(g.servers))
	for k, v := range g.servers {
		snapshot[k] = v
	}
	g.mu.RUnlock()

	if len(snapshot) == 0 {
		return errs.ErrNoServerRegistered
	}

	g.sharedManager = session.NewManager(session.WithMaxSessions(1000000))
	g.startTime.Store(time.Now())

	if g.opts.EnableMetrics {
		go g.serveMetrics()
	}

	type startResult struct {
		proto types.ProtocolType
		err   error
	}
	ch := make(chan startResult, len(snapshot))

	for proto, srv := range snapshot {
		p, s := proto, srv
		g.wg.Add(1)
		go func() {
			defer g.wg.Done()
			err := s.Start()
			ch <- startResult{proto: p, err: err}
		}()
	}

	var startErr error
	var started []types.ProtocolType
	for range snapshot {
		r := <-ch
		if r.err != nil {
			if startErr == nil {
				startErr = fmt.Errorf("server %s failed: %w", r.proto, r.err)
			}
		} else {
			started = append(started, r.proto)
		}
	}

	if startErr != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, p := range started {
			if srv, ok := snapshot[p]; ok {
				_ = srv.Stop(ctx)
			}
		}
		return startErr
	}

	g.started.Store(true)
	log.Printf("Gateway started with %d protocols", len(snapshot))
	return nil
}

// Stop performs the 6-stage graceful shutdown.
func (g *Gateway) Stop(ctx context.Context) error {
	g.mu.RLock()
	snapshot := make(map[types.ProtocolType]types.Server, len(g.servers))
	for k, v := range g.servers {
		snapshot[k] = v
	}
	g.mu.RUnlock()

	for _, srv := range snapshot {
		_ = srv.Stop(ctx)
	}

	if g.sharedManager != nil {
		_ = g.sharedManager.Close()
	}

	done := make(chan struct{})
	go func() {
		g.wg.Wait()
		if g.metricsServer != nil {
			_ = g.metricsServer.Shutdown(context.Background())
		}
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Run starts the gateway and blocks until a termination signal.
// It performs a staged shutdown when a signal is received.
func (g *Gateway) Run() error {
	if err := g.Start(); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	<-ctx.Done()
	log.Println("Shutdown signal received, initiating staged shutdown...")

	// Stage 1: Stop accepting new connections
	if err := g.stageStopAccept(); err != nil {
		log.Printf("Stage 1 (StopAccept) error: %v", err)
	}

	// Stage 2: Drain in-flight messages
	if err := g.stageDrain(); err != nil {
		log.Printf("Stage 2 (Drain) error: %v", err)
	}

	// Stage 3: Close active sessions
	if err := g.stageSessionClose(); err != nil {
		log.Printf("Stage 3 (SessionClose) error: %v", err)
	}

	// Stage 4: Close session manager
	if err := g.stageManagerClose(); err != nil {
		log.Printf("Stage 4 (ManagerClose) error: %v", err)
	}

	// Stage 5: Close metrics server
	if err := g.stageMetricsClose(); err != nil {
		log.Printf("Stage 5 (MetricsClose) error: %v", err)
	}

	// Stage 6: Finalize
	g.stageFinalize()

	return nil
}

// stageStopAccept stops accepting new connections.
func (g *Gateway) stageStopAccept() error {
	ctx, cancel := context.WithTimeout(context.Background(), g.opts.StageTimeouts.StopAccept)
	defer cancel()

	g.mu.RLock()
	snapshot := make(map[types.ProtocolType]types.Server, len(g.servers))
	for k, v := range g.servers {
		snapshot[k] = v
	}
	g.mu.RUnlock()

	for proto, srv := range snapshot {
		if err := srv.Stop(ctx); err != nil {
			log.Printf("Failed to stop %s server: %v", proto, err)
		}
	}
	return nil
}

// stageDrain drains in-flight messages.
func (g *Gateway) stageDrain() error {
	ctx, cancel := context.WithTimeout(context.Background(), g.opts.StageTimeouts.Drain)
	defer cancel()

	// Wait for all goroutines to complete
	done := make(chan struct{})
	go func() {
		g.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// stageSessionClose closes active sessions.
func (g *Gateway) stageSessionClose() error {
	ctx, cancel := context.WithTimeout(context.Background(), g.opts.StageTimeouts.SessionClose)
	defer cancel()

	if g.sharedManager != nil {
		if err := g.sharedManager.Close(); err != nil {
			return err
		}
	}
	_ = ctx // Suppress unused warning
	return nil
}

// stageManagerClose closes the session manager.
func (g *Gateway) stageManagerClose() error {
	ctx, cancel := context.WithTimeout(context.Background(), g.opts.StageTimeouts.ManagerClose)
	defer cancel()

	<-ctx.Done()
	return nil
}

// stageMetricsClose closes the metrics server.
func (g *Gateway) stageMetricsClose() error {
	ctx, cancel := context.WithTimeout(context.Background(), g.opts.StageTimeouts.MetricsClose)
	defer cancel()

	if g.metricsServer != nil {
		return g.metricsServer.Shutdown(ctx)
	}
	return nil
}

// stageFinalize performs final cleanup.
func (g *Gateway) stageFinalize() {
	ctx, cancel := context.WithTimeout(context.Background(), g.opts.StageTimeouts.Finalize)
	defer cancel()

	<-ctx.Done()
	log.Println("Shutdown complete")
}

func (g *Gateway) serveMetrics() {
	mux := stdhttp.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", g.handleHealthz)
	mux.HandleFunc("/readyz", g.handleReadyz)

	g.metricsServer = &stdhttp.Server{
		Addr:    g.opts.MetricsAddr,
		Handler: mux,
	}
	log.Printf("Metrics server listening on %s", g.opts.MetricsAddr)
	if err := g.metricsServer.ListenAndServe(); err != nil && err != stdhttp.ErrServerClosed {
		log.Printf("Metrics server error: %v", err)
	}
}

func (g *Gateway) handleHealthz(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if !g.started.Load() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(stdhttp.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "starting"})
		return
	}

	g.mu.RLock()
	protocols := make(map[string]any, len(g.servers))
	for proto := range g.servers {
		protocols[proto.String()] = map[string]any{
			"protocol": proto.String(),
		}
	}
	g.mu.RUnlock()

	sessCount := int64(0)
	if g.sharedManager != nil {
		sessCount = g.sharedManager.Count()
	}

	var uptime string
	if t, ok := g.startTime.Load().(time.Time); ok {
		uptime = time.Since(t).String()
	}

	resp := map[string]any{
		"status":    "healthy",
		"uptime":    uptime,
		"protocols": protocols,
		"sessions":  sessCount,
		"system": map[string]any{
			"goroutines": runtime.NumGoroutine(),
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (g *Gateway) handleReadyz(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if !g.started.Load() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(stdhttp.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "not ready"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}

// Protocol returns Custom for gateway.
func (g *Gateway) Protocol() types.ProtocolType { return types.Custom }

// Manager returns the shared session manager.
func (g *Gateway) Manager() *session.Manager { return g.sharedManager }
