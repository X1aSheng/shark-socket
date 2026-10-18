package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// protoSession is a fakeSession that reports a chosen protocol, so the admin
// endpoint's per-protocol grouping can be asserted without real transports.
type protoSession struct {
	fakeSession
	proto core.Protocol
}

func (s *protoSession) Protocol() core.Protocol { return s.proto }

// startAdminGateway brings up a gateway with one fake server and the admin
// listener on an ephemeral loopback port. It returns the admin base URL.
func startAdminGateway(t *testing.T, cfg AdminConfig) (*Gateway, string) {
	t.Helper()
	g := NewGateway(WithAdmin(cfg))
	if err := g.Register(&fakeServer{proto: core.ProtocolTCP}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := g.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = g.Stop(ctx)
	})
	addr := g.AdminAddr()
	if addr == "" {
		t.Fatal("AdminAddr is empty after Start")
	}
	return g, "http://" + addr
}

func getJSON(t *testing.T, url string, out any) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if out != nil && resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode %s: %v", url, err)
		}
	}
	return resp.StatusCode
}

func TestAdminDisabledByDefault(t *testing.T) {
	g := NewGateway()
	if got := g.AdminAddr(); got != "" {
		t.Fatalf("AdminAddr = %q, want empty when WithAdmin is not used", got)
	}
	if err := g.Register(&fakeServer{proto: core.ProtocolTCP}); err != nil {
		t.Fatal(err)
	}
	if err := g.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := g.AdminAddr(); got != "" {
		t.Fatalf("AdminAddr = %q after Start, want empty", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := g.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAdminSessionsMatchRuntime(t *testing.T) {
	g, base := startAdminGateway(t, AdminConfig{Addr: "127.0.0.1:0"})

	sm := g.rt.Sessions()
	for i := 0; i < 3; i++ {
		if err := sm.Register(&protoSession{fakeSession: fakeSession{id: sm.NextID()}, proto: core.ProtocolTCP}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := sm.Register(&protoSession{fakeSession: fakeSession{id: sm.NextID()}, proto: core.ProtocolUDP}); err != nil {
			t.Fatal(err)
		}
	}

	var body struct {
		Total      int64            `json:"total"`
		ByProtocol map[string]int64 `json:"by_protocol"`
		Capacity   *int64           `json:"capacity"`
	}
	if status := getJSON(t, base+"/sessions", &body); status != http.StatusOK {
		t.Fatalf("GET /sessions = %d, want 200", status)
	}
	if body.Total != sm.Count() {
		t.Fatalf("total = %d, want %d (the runtime's own count)", body.Total, sm.Count())
	}
	if body.Total != 5 {
		t.Fatalf("total = %d, want 5", body.Total)
	}
	if body.ByProtocol["tcp"] != 3 || body.ByProtocol["udp"] != 2 {
		t.Fatalf("by_protocol = %v, want tcp:3 udp:2", body.ByProtocol)
	}
	if body.Capacity == nil || *body.Capacity != 1_000_000 {
		t.Fatalf("capacity = %v, want the default 1,000,000", body.Capacity)
	}
}

func TestAdminPluginsInExecutionOrder(t *testing.T) {
	var order []string
	g := NewGateway(WithAdmin(AdminConfig{Addr: "127.0.0.1:0"}),
		WithPlugins(
			orderPlugin{name: "late", priority: 20, order: &order},
			orderPlugin{name: "early", priority: 10, order: &order},
		))
	if err := g.Register(&fakeServer{proto: core.ProtocolTCP}); err != nil {
		t.Fatal(err)
	}
	if err := g.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = g.Stop(ctx)
	}()

	var body struct {
		Plugins []string `json:"plugins"`
	}
	if status := getJSON(t, "http://"+g.AdminAddr()+"/plugins", &body); status != http.StatusOK {
		t.Fatalf("GET /plugins = %d, want 200", status)
	}
	want := []string{"early", "late"}
	if len(body.Plugins) != len(want) || body.Plugins[0] != want[0] || body.Plugins[1] != want[1] {
		t.Fatalf("plugins = %v, want %v (priority order, not append order)", body.Plugins, want)
	}
}

func TestAdminHealthAndPprof(t *testing.T) {
	_, base := startAdminGateway(t, AdminConfig{Addr: "127.0.0.1:0"})

	var health map[string]any
	if status := getJSON(t, base+"/healthz", &health); status != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want 200", status)
	}
	if started, _ := health["started"].(bool); !started {
		t.Fatalf("health.started = %v, want true", health["started"])
	}

	// pprof is the reason the listener exists, so at least one profile has to
	// answer. Index is the cheap one; goroutine is served by it for ?debug=1.
	resp, err := http.Get(base + "/debug/pprof/goroutine?debug=1")
	if err != nil {
		t.Fatalf("GET pprof: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /debug/pprof/goroutine = %d, want 200", resp.StatusCode)
	}
}

func TestAdminRejectsWrites(t *testing.T) {
	_, base := startAdminGateway(t, AdminConfig{Addr: "127.0.0.1:0"})
	req, err := http.NewRequest(http.MethodPost, base+"/sessions", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /sessions: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST /sessions = %d, want 405", resp.StatusCode)
	}
}

func TestAdminRequiresToken(t *testing.T) {
	_, base := startAdminGateway(t, AdminConfig{Addr: "127.0.0.1:0", Token: "s3cret"})

	resp, err := http.Get(base + "/sessions")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without token = %d, want 401", resp.StatusCode)
	}

	req, err := http.NewRequest(http.MethodGet, base+"/sessions", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer s3cret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("with token = %d, want 200", resp.StatusCode)
	}
}

// TestAdminRefusesPublicBindWithoutToken pins the one safety rule: profiles and
// goroutine dumps on a routable address with no credential are not allowed,
// because the failure mode is a memory disclosure nobody notices.
func TestAdminRefusesPublicBindWithoutToken(t *testing.T) {
	g := NewGateway(WithAdmin(AdminConfig{Addr: "0.0.0.0:0"}))
	if err := g.Register(&fakeServer{proto: core.ProtocolTCP}); err != nil {
		t.Fatal(err)
	}
	err := g.Start(context.Background())
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = g.Stop(ctx)
		t.Fatal("Start succeeded on a non-loopback admin address without a token")
	}
	if !strings.Contains(err.Error(), "without a token") {
		t.Fatalf("error = %v, want one mentioning the missing token", err)
	}
	// The failed Start must not leave the servers running.
	if g.Ready() {
		t.Fatal("gateway still reports ready after a failed admin start")
	}
}
