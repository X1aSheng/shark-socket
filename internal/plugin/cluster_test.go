package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/infra/observability"
	"github.com/X1aSheng/shark-socket/internal/infra/pubsub"
	"github.com/X1aSheng/shark-socket/internal/runtime"
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

	local := NewCluster("node-a", bus, nil, 1)
	remote := NewCluster("node-b", bus, remoteManager, 1)
	_ = remote.Start()
	defer func() { _ = remote.Stop() }()

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

	cluster := NewCluster("node-a", bus, manager, 1)
	_ = cluster.Start()
	defer func() { _ = cluster.Stop() }()

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
	cluster := NewCluster("node-a", nil, nil, 0)
	payload, err := cluster.OnMessage(fakeSession{}, []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "hello" {
		t.Fatalf("payload = %q, want hello", payload)
	}
}

func TestClusterImplementsPlugin(t *testing.T) {
	var _ core.Plugin = NewCluster("node-a", pubsub.New(), runtime.NewSessionManager(), 0)
	if err := NewCluster("node-a", pubsub.New(), runtime.NewSessionManager(), 0).Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// errorSession fails every Send so Broadcast surfaces an error.
type errorSession struct {
	fakeSession
}

func (s *errorSession) Send([]byte) error { return errors.New("send failed") }

// publishEnvelope marshals and publishes a cluster envelope on the given bus
// and topic, as a remote node would.
func publishEnvelope(t *testing.T, bus *pubsub.PubSub, topic string, env clusterEnvelope) {
	t.Helper()
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	bus.Publish(topic, data)
}

// TestClusterDropsMalformedMessage covers the malformed-envelope path: the
// message is dropped and a warning is logged instead of crashing the consumer.
func TestClusterDropsMalformedMessage(t *testing.T) {
	bus := pubsub.New()
	manager := runtime.NewSessionManager()
	sess := &captureSession{
		fakeSession: fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1111}},
		received:    make(chan []byte, 1),
	}
	if err := manager.Register(sess); err != nil {
		t.Fatal(err)
	}
	logger := observability.NewMemoryLogger()
	cluster := NewCluster("node-a", bus, manager, 1)
	cluster.SetLogger(logger)
	_ = cluster.Start()
	defer func() { _ = cluster.Stop() }()

	bus.Publish("shark.cluster.messages", []byte("not-json"))
	time.Sleep(50 * time.Millisecond)

	select {
	case got := <-sess.received:
		t.Fatalf("malformed message broadcast: %q", got)
	default:
	}
	if entries := logger.Entries(); len(entries) != 1 || entries[0].Level != "warn" {
		t.Fatalf("logger entries = %#v, want one warn", entries)
	}
}

// TestClusterDropsWrongTopicAndEmptyPayload covers the two silent-drop
// guards in handleClusterMessage: an envelope for another topic and an
// envelope with an empty payload must not be broadcast.
func TestClusterDropsWrongTopicAndEmptyPayload(t *testing.T) {
	bus := pubsub.New()
	manager := runtime.NewSessionManager()
	sess := &captureSession{
		fakeSession: fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1111}},
		received:    make(chan []byte, 1),
	}
	if err := manager.Register(sess); err != nil {
		t.Fatal(err)
	}
	cluster := NewCluster("node-a", bus, manager, 1)
	_ = cluster.Start()
	defer func() { _ = cluster.Stop() }()

	publishEnvelope(t, bus, "shark.cluster.messages", clusterEnvelope{
		NodeID: "node-b", Topic: "other-topic", Protocol: "tcp", Payload: []byte("hello"),
	})
	publishEnvelope(t, bus, "shark.cluster.messages", clusterEnvelope{
		NodeID: "node-b", Topic: "shark.cluster.messages", Protocol: "tcp", Payload: nil,
	})
	time.Sleep(50 * time.Millisecond)

	select {
	case got := <-sess.received:
		t.Fatalf("unexpected broadcast: %q", got)
	default:
	}
}

// TestClusterBroadcastErrorLogged covers the Broadcast-failure path: a
// session whose Send fails surfaces a warning instead of being silent.
func TestClusterBroadcastErrorLogged(t *testing.T) {
	bus := pubsub.New()
	manager := runtime.NewSessionManager()
	sess := &errorSession{fakeSession: fakeSession{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1111}}}
	if err := manager.Register(sess); err != nil {
		t.Fatal(err)
	}
	logger := observability.NewMemoryLogger()
	cluster := NewCluster("node-a", bus, manager, 1)
	cluster.SetLogger(logger)
	_ = cluster.Start()
	defer func() { _ = cluster.Stop() }()

	publishEnvelope(t, bus, "shark.cluster.messages", clusterEnvelope{
		NodeID: "node-b", Topic: "shark.cluster.messages", Protocol: "tcp", Payload: []byte("hello"),
	})
	time.Sleep(50 * time.Millisecond)

	entries := logger.Entries()
	if len(entries) != 1 || entries[0].Level != "warn" {
		t.Fatalf("logger entries = %#v, want one warn for broadcast error", entries)
	}
}
