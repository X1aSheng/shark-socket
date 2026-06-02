package coap

import (
	"context"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
)

func TestCoAPObserveNotificationE2E(t *testing.T) {
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(func(sess core.Session, msg core.Message) error {
			sess.SetMeta("payload", string(msg.Payload))
			return nil
		}),
	)
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer stopGateway(t, gateway)

	// Register observer for /sensor/temp
	observerToken := []byte{0x01, 0x02}
	obs := server.observers.Register("/sensor/temp", "127.0.0.1:15683", observerToken)
	if obs == nil {
		t.Fatal("failed to register observer")
	}

	// Verify observer registered via Notify
	subs := server.observers.Notify("/sensor/temp")
	if len(subs) != 1 {
		t.Fatalf("observer count = %d, want 1", len(subs))
	}

	// Initial sequence should be 0 before any notifications
	if seq := subs[0].NextSeq(); seq != 0 {
		t.Fatalf("initial seq = %d, want 0", seq)
	}

	// After NextSeq, the sequence should have incremented
	if seq := subs[0].NextSeq(); seq != 1 {
		t.Fatalf("seq after increment = %d, want 1", seq)
	}
}

func TestCoAPObserveUnregisterRemovesObserver(t *testing.T) {
	server := NewServer(WithAddr("127.0.0.1:0"))
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer stopGateway(t, gateway)

	token := []byte{0xAA}
	server.observers.Register("/sensor/humidity", "127.0.0.1:15684", token)

	// Remove observer
	server.observers.Remove("/sensor/humidity", "127.0.0.1:15684", token)

	// Verify removed
	subs := server.observers.Notify("/sensor/humidity")
	if len(subs) != 0 {
		t.Fatalf("observer count after remove = %d, want 0", len(subs))
	}
}

func TestCoAPObserveRemoveBySessionCleansAll(t *testing.T) {
	server := NewServer(WithAddr("127.0.0.1:0"))
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer stopGateway(t, gateway)

	remote := "127.0.0.1:15685"
	server.observers.Register("/sensor/temp", remote, []byte{1})
	server.observers.Register("/sensor/humidity", remote, []byte{2})
	server.observers.Register("/sensor/pressure", remote, []byte{3})

	// Remove all observers for this remote
	server.observers.RemoveBySession(remote)

	// Verify all removed
	for _, resource := range []string{"/sensor/temp", "/sensor/humidity", "/sensor/pressure"} {
		subs := server.observers.Notify(resource)
		if len(subs) != 0 {
			t.Fatalf("%s: observer count = %d, want 0", resource, len(subs))
		}
	}
}

func TestCoAPObserveMultipleResources(t *testing.T) {
	server := NewServer(WithAddr("127.0.0.1:0"))
	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := gateway.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer stopGateway(t, gateway)

	remote1 := "127.0.0.1:15686"
	remote2 := "127.0.0.1:15687"

	server.observers.Register("/sensor/temp", remote1, []byte{1})
	server.observers.Register("/sensor/temp", remote2, []byte{2})
	server.observers.Register("/sensor/humidity", remote1, []byte{3})

	// Notify temp: both observers should be notified
	tempSubs := server.observers.Notify("/sensor/temp")
	if len(tempSubs) != 2 {
		t.Fatalf("/sensor/temp observer count = %d, want 2", len(tempSubs))
	}

	// Notify humidity: only remote1 should be notified
	humSubs := server.observers.Notify("/sensor/humidity")
	if len(humSubs) != 1 {
		t.Fatalf("/sensor/humidity observer count = %d, want 1", len(humSubs))
	}
}

// sweepLoopOnce runs one sweep iteration for testing
func (s *Server) sweepLoopOnce() {
	now := time.Now()
	s.sessions.Range(func(key, value any) bool {
		sess := value.(*session)
		if now.Sub(sess.LastActiveAt()) > s.opts.SessionTTL {
			s.closeSession(context.Background(), key, sess)
		}
		return true
	})
}
