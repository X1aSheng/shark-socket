package plugin

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket-new/internal/core"
	"github.com/X1aSheng/shark-socket-new/internal/infra/pubsub"
	"github.com/X1aSheng/shark-socket-new/internal/runtime"
)

type captureSession struct {
	fakeSession
	received chan []byte
}

func (s *captureSession) Send(data []byte) error {
	s.received <- append([]byte(nil), data...)
	return nil
}

func TestClusterPublishesToRemoteNodeSessions(t *testing.T) {
	bus := pubsub.New()
	remoteManager := runtime.NewSessionManager()
	remoteSession := &captureSession{
		fakeSession: fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.2"), Port: 2222}},
		received:    make(chan []byte, 1),
	}
	if err := remoteManager.Register(remoteSession); err != nil {
		t.Fatal(err)
	}

	local := NewCluster("node-a", bus, nil)
	remote := NewCluster("node-b", bus, remoteManager)
	remote.Start(1)
	defer remote.Stop()

	if _, err := local.OnMessage(fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1111}}, []byte("hello")); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-remoteSession.received:
		if string(got) != "hello" {
			t.Fatalf("received = %q, want hello", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cluster broadcast")
	}
}

func TestClusterIgnoresOwnNodeMessages(t *testing.T) {
	bus := pubsub.New()
	manager := runtime.NewSessionManager()
	sess := &captureSession{
		fakeSession: fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1111}},
		received:    make(chan []byte, 1),
	}
	if err := manager.Register(sess); err != nil {
		t.Fatal(err)
	}

	cluster := NewCluster("node-a", bus, manager)
	cluster.Start(1)
	defer cluster.Stop()

	if _, err := cluster.OnMessage(sess, []byte("loopback")); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-sess.received:
		t.Fatalf("unexpected loopback broadcast: %q", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestClusterNoopsWithoutBus(t *testing.T) {
	cluster := NewCluster("node-a", nil, nil)
	payload, err := cluster.OnMessage(fakeSession{}, []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "hello" {
		t.Fatalf("payload = %q, want hello", payload)
	}
}

func TestClusterImplementsPlugin(t *testing.T) {
	var _ core.Plugin = NewCluster("node-a", pubsub.New(), runtime.NewSessionManager())
	if err := NewCluster("node-a", pubsub.New(), runtime.NewSessionManager()).Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
