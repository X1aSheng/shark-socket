package runtime

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// AdminConfig configures the optional admin/debug listener. The zero value
// disables it, which is the default: a gateway that opens a port nobody asked
// for is a gateway that surprises its operator.
type AdminConfig struct {
	// Addr is the listen address, e.g. "127.0.0.1:19900". Empty means the
	// admin listener is off.
	Addr string
	// Token, when set, is required as "Authorization: Bearer <token>" on every
	// request. Required when Addr is not loopback.
	Token string
}

// WithAdmin enables the admin listener.
//
// The endpoints are read-only and intentionally narrow:
//
//	GET /healthz            same body as Gateway.Health
//	GET /sessions           live session counts, by protocol
//	GET /plugins            plugin names in execution order
//	GET /debug/pprof/*      the standard profiles
//
// Two things it does not do, on purpose. It does not mutate anything - a
// "close all sessions" button is an outage waiting for a typo. And it does not
// expose session IDs or peer addresses: the operator needs aggregates to see
// whether the process is healthy, and anything beyond aggregates belongs in
// logs or metrics with their own retention rules.
func WithAdmin(cfg AdminConfig) GatewayOption {
	return func(c *gatewayConfig) { c.admin = cfg }
}

// AdminAddr reports the address the admin listener bound to, or "" when it is
// off. It is the only way to discover the port when Addr asked for :0.
func (g *Gateway) AdminAddr() string {
	if g.admin == nil || g.admin.ln == nil {
		return ""
	}
	return g.admin.ln.Addr().String()
}

type adminServer struct {
	cfg AdminConfig
	gw  *Gateway
	srv *http.Server
	ln  net.Listener
}

func newAdminServer(gw *Gateway, cfg AdminConfig) *adminServer {
	a := &adminServer{cfg: cfg, gw: gw}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", a.handleHealth)
	mux.HandleFunc("/sessions", a.handleSessions)
	mux.HandleFunc("/plugins", a.handlePlugins)
	// pprof on an explicit mux rather than the package-level DefaultServeMux:
	// importing the package already registers there, and silently answering
	// /debug/pprof on some future listener that used the default mux is worse
	// than an import side effect.
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	a.srv = &http.Server{
		Handler:           a.requireToken(mux),
		ReadHeaderTimeout: 5 * time.Second,
		// No WriteTimeout and no IdleTimeout. A CPU profile or an execution
		// trace runs for its whole requested duration and streams at the end;
		// a write deadline would truncate it exactly when it is most needed.
		// The listener is loopback-or-token guarded, so the usual slow-client
		// argument does not apply.
	}
	return a
}

func (a *adminServer) start() error {
	ln, err := net.Listen("tcp", a.cfg.Addr)
	if err != nil {
		return fmt.Errorf("admin listen %s: %w", a.cfg.Addr, err)
	}
	// pprof can read process memory (heap profile) and stack traces of every
	// goroutine. Exposing that on a routable address with no credential is not
	// a configuration mistake worth allowing: refuse to start instead.
	if !isLoopbackAddr(ln.Addr()) && a.cfg.Token == "" {
		_ = ln.Close()
		return fmt.Errorf("admin listen %s: refusing to expose profiles on a non-loopback address without a token", ln.Addr())
	}
	a.ln = ln
	go func() {
		err := a.srv.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.gw.rt.Logger().Error("admin serve failed", "error", err)
		}
	}()
	a.gw.rt.Logger().Info("admin listener started", "addr", ln.Addr().String())
	return nil
}

func (a *adminServer) stop(ctx context.Context) error {
	if a.srv == nil {
		return nil
	}
	// Close, not Shutdown-with-a-long-deadline: an admin listener is not worth
	// stretching a stage timeout for, and an in-flight profile would otherwise
	// hold the shutdown open for its full duration.
	return a.srv.Close()
}

func isLoopbackAddr(addr net.Addr) bool {
	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok {
		return false
	}
	return tcpAddr.IP.IsLoopback()
}

func (a *adminServer) requireToken(next http.Handler) http.Handler {
	if a.cfg.Token == "" {
		return next
	}
	want := []byte("Bearer " + a.cfg.Token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Constant-time compare: a bearer token is a secret, and a byte-wise
		// early exit leaks its prefix through timing.
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="shark-socket admin"`)
			a.writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *adminServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !a.allowGet(w, r) {
		return
	}
	a.writeJSON(w, http.StatusOK, a.gw.Health())
}

// handleSessions reports the live session counts.
//
// The numbers come from SessionManager, the same authority the capacity check
// and every transport use, so they cannot drift from what the process actually
// holds. They are not a consistent snapshot: Range walks 64 shards without a
// global lock, so a session closing mid-walk can be counted in by_protocol but
// not in total, or the other way round. For a debug view that is the right
// trade - accuracy to the last session is worth less than not stopping the
// world to measure it.
func (a *adminServer) handleSessions(w http.ResponseWriter, r *http.Request) {
	if !a.allowGet(w, r) {
		return
	}
	sm := a.gw.rt.Sessions()
	byProtocol := make(map[string]int64, 8)
	sm.Range(func(sess core.Session) bool {
		byProtocol[string(sess.Protocol())]++
		return true
	})
	body := map[string]any{
		"total":       sm.Count(),
		"by_protocol": byProtocol,
	}
	// Capacity lives on the concrete manager only - core.SessionManager has no
	// reader for it, because nothing in the runtime needs one. Ask the
	// undecorated manager, since the metrics decorator embeds the interface
	// and hides it.
	if reporter, ok := a.gw.rawSessions.(interface{ Capacity() int64 }); ok {
		if capacity := reporter.Capacity(); capacity > 0 {
			body["capacity"] = capacity
		}
	}
	a.writeJSON(w, http.StatusOK, body)
}

func (a *adminServer) handlePlugins(w http.ResponseWriter, r *http.Request) {
	if !a.allowGet(w, r) {
		return
	}
	names := a.gw.rt.PluginNames()
	if names == nil {
		names = []string{}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"plugins": names})
}

func (a *adminServer) allowGet(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	a.writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	return false
}

func (a *adminServer) writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		a.gw.rt.Logger().Warn("admin response write failed", "error", err)
	}
}
