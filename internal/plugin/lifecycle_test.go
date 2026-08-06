package plugin

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/infra/pubsub"
	"github.com/X1aSheng/shark-socket/internal/runtime"
)

// TestLifecycleConcurrentStartStop drives Start/Stop from multiple goroutines
// on the same instance. The old "reassign sync.Once{}" restart pattern was a
// data race (Once.Do reading the struct while Start wrote it); this test must
// stay clean under -race.
func TestLifecycleConcurrentStartStop(t *testing.T) {
	ab := NewAutoBan(3)
	rl := NewRateLimit(10, time.Second)
	hb := NewHeartbeat(runtime.NewSessionManager(), time.Minute)
	cl := NewCluster("n1", pubsub.New(), runtime.NewSessionManager())

	cases := []struct {
		name  string
		start func()
		stop  func()
	}{
		{"autoban", func() { _ = ab.Start() }, func() { _ = ab.Stop() }},
		{"ratelimit", func() { _ = rl.Start() }, func() { _ = rl.Stop() }},
		{"heartbeat", func() { hb.Start(time.Millisecond) }, func() { hb.Stop() }},
		{"cluster", func() { cl.Start(4) }, func() { cl.Stop() }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var wg sync.WaitGroup
			for i := 0; i < 4; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for j := 0; j < 50; j++ {
						tc.start()
						tc.stop()
					}
				}()
			}
			wg.Wait()
			// Final state must be stopped and restartable.
			tc.stop()
			tc.start()
			tc.stop()
		})
	}
}

// TestAutoBanOnMessageBans checks that OnMessage accumulates counts per IP,
// bans at the threshold, and drops the triggering message. Regression for
// AutoBan having no production call site for Record().
func TestAutoBanOnMessageBans(t *testing.T) {
	p := NewAutoBan(3)
	sess := fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.7"), Port: 1234}}

	for i := 0; i < 2; i++ {
		out, err := p.OnMessage(sess, []byte("ping"))
		if err != nil {
			t.Fatalf("message %d: unexpected error %v", i, err)
		}
		if string(out) != "ping" {
			t.Fatalf("message %d: payload corrupted", i)
		}
		if p.Banned("10.0.0.7") {
			t.Fatalf("banned too early after message %d", i+1)
		}
	}

	// Third message reaches the threshold: dropped and the IP is banned.
	out, err := p.OnMessage(sess, []byte("ping"))
	if err != core.ErrPluginDrop {
		t.Fatalf("threshold message: error = %v, want %v", err, core.ErrPluginDrop)
	}
	if out != nil {
		t.Fatalf("threshold message: payload should be dropped")
	}
	if !p.Banned("10.0.0.7") {
		t.Fatal("IP not banned after reaching threshold")
	}

	// A different IP is not affected.
	other := fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.8"), Port: 1}}
	if _, err := p.OnMessage(other, []byte("ping")); err != nil {
		t.Fatalf("other IP: unexpected error %v", err)
	}

	// OnAccept rejects the banned IP.
	if err := p.OnAccept(sess); err != core.ErrPluginBlock {
		t.Fatalf("OnAccept for banned IP = %v, want %v", err, core.ErrPluginBlock)
	}
}
