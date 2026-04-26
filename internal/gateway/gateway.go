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

	"github.com/X1aSheng/shark-socket/internal/errs"
	"github.com/X1aSheng/shark-socket/internal/session"
	"github.com/X1aSheng/shark-socket/internal/types"
)

// Gateway orchestrates multiple protocol servers with shared session management.
type Gateway struct {
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
	proto := srv.Protocol()
	if _, exists := g.servers[proto]; exists {
		return errs.ErrDuplicateProtocol
	}
	g.servers[proto] = srv
	return nil
}

// Start launches all registered servers concurrently.
func (g *Gateway) Start() error {
	if len(g.servers) == 0 {
		return errs.ErrNoServerRegistered
	}

	// Initialize shared session manager
	g.sharedManager = session.NewManager(session.WithMaxSessions(1000000))

	g.startTime.Store(time.Now())

	// Start metrics HTTP server
	if g.opts.EnableMetrics {
		go g.serveMetrics()
	}

	// Start all servers concurrently, collect first error
	type startResult struct {
		proto types.ProtocolType
		err   error
	}
	ch := make(chan startResult, len(g.servers))

	for proto, srv := range g.servers {
		p, s := proto, srv
		g.wg.Add(1)
		go func() {
			defer g.wg.Done()
			err := s.Start()
			ch <- startResult{proto: p, err: err}
		}()
	}

	// Wait for all servers to report
	var startErr error
	var started []types.ProtocolType
	for range g.servers {
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
		// Rollback started servers
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, p := range started {
			if srv, ok := g.servers[p]; ok {
				_ = srv.Stop(ctx)
			}
		}
		return startErr
	}

	g.started.Store(true)
	log.Printf("Gateway started with %d protocols", len(g.servers))
	return nil
}

// Stop performs the 6-stage graceful shutdown.
func (g *Gateway) Stop(ctx context.Context) error {
	// Stage 1: Stop accepting new connections
	for _, srv := range g.servers {
		_ = srv.Stop(ctx)
	}

	// Stage 2: Signal graceful shutdown to handlers
	// (handlers check context cancellation)

	// Stage 3: Drain write queues (handled by session Close)

	// Stage 4: Plugin chain OnClose (handled by individual servers)

	// Stage 5: Close all sessions
	if g.sharedManager != nil {
		_ = g.sharedManager.Close()
	}

	// Stage 6: Wait or timeout
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
func (g *Gateway) Run() error {
	if err := g.Start(); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	<-ctx.Done()
	log.Println("Shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), g.opts.ShutdownTimeout)
	defer cancel()
	return g.Stop(shutdownCtx)
}

func (g *Gateway) serveMetrics() {
	mux := stdhttp.NewServeMux()
	mux.HandleFunc("/metrics", g.handleMetrics)
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

	status := "healthy"
	protocols := make(map[string]any)
	for proto := range g.servers {
		protocols[proto.String()] = map[string]any{
			"protocol": proto.String(),
		}
	}

	sessCount := int64(0)
	if g.sharedManager != nil {
		sessCount = g.sharedManager.Count()
	}

	var uptime string
	if t, ok := g.startTime.Load().(time.Time); ok {
		uptime = time.Since(t).String()
	}

	resp := map[string]any{
		"status":    status,
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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}

func (g *Gateway) handleMetrics(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintln(w, "# shark-socket metrics placeholder")
	if g.sharedManager != nil {
		fmt.Fprintf(w, "shark_sessions_total %d\n", g.sharedManager.Count())
	}
}

// Protocol returns Custom for gateway.
func (g *Gateway) Protocol() types.ProtocolType { return types.Custom }

// Manager returns the shared session manager.
func (g *Gateway) Manager() *session.Manager { return g.sharedManager }
