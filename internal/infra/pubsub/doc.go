// Package pubsub provides publish-subscribe messaging interfaces and channel-based implementation.
//
// This package defines the PubSub interface and built-in ChannelPubSub implementation.
// PubSub is used for cluster event broadcasting, cross-node session routing,
// and decoupled component communication.
//
// # PubSub Interface
//
//	type PubSub interface {
//	    Publish(ctx context.Context, topic string, data []byte) error
//	    Subscribe(ctx context.Context, topic string, handler func([]byte)) (Subscription, error)
//	}
//
//	type Subscription interface {
//	    Unsubscribe() error
//	    Topic() string
//	}
//
// # ChannelPubSub Implementation
//
// ChannelPubSub is an in-process publish-subscribe system using Go channels:
//
//	ps := NewChannelPubSub()
//	defer ps.Close()
//
// # Usage Examples
//
// Subscribe to a topic:
//
//	sub, _ := ps.Subscribe(ctx, "session.joined", func(data []byte) {
//	    log.Printf("session joined: %s", data)
//	})
//	defer sub.Unsubscribe()
//
// Publish a message:
//
//	ps.Publish(ctx, "session.joined", []byte(`{"id":123}`))
//
// # Architecture
//
//	┌───────────────────────────────────────────────────────────────────┐
//	│                    ChannelPubSub Architecture                      │
//	├───────────────────────────────────────────────────────────────────┤
//	│                                                                   │
//	│   Publisher ──→ Publish(topic, data) ──→ Fan-out Goroutine ──→  │
//	│                                              │                   │
//	│                                              ├──→ Subscriber 1   │
//	│                                              ├──→ Subscriber 2   │
//	│                                              └──→ Subscriber N   │
//	│                                                                   │
//	│   Each subscriber gets a buffered channel (default 256 capacity) │
//	│   Slow consumers: messages are dropped after buffer full         │
//	│                                                                   │
//	└───────────────────────────────────────────────────────────────────┘
//
// # Slow Consumer Handling
//
// When a subscriber cannot keep up with messages:
//
//	┌──────────────────────────────────────────────────────────┐
//	│  Strategy: Drop Oldest (default)                         │
//	│                                                          │
//	│  1. Subscriber channel full (buffer=256)                 │
//	│  2. New message arrives                                  │
//	│  3. Oldest message in channel is dropped                 │
//	│  4. New message is enqueued                              │
//	│  5. Metric: shark_pubsub_dropped_total incremented      │
//	│                                                          │
//	│  Rationale: Fresh data is more valuable than stale data │
//	└──────────────────────────────────────────────────────────┘
//
// # Topic Patterns
//
// Framework uses convention-based topic names:
//
// | Topic | Purpose | Publisher | Subscriber |
// |-------|---------|-----------|------------|
// | session.joined | New session | ClusterPlugin | Other nodes |
// | session.left | Session closed | ClusterPlugin | Other nodes |
// | node.{id}.route | Cross-node msg | Any node | Target node |
// | node.{id}.heartbeat | Heartbeat | Each node | Monitor |
// | config.update | Config change | Admin | All instances |
//
// # Async Distribution Model
//
// Each subscription creates:
//   - Buffered channel (capacity configurable)
//   - Goroutine for message processing
//   - Done channel for clean shutdown
//
//	type subscription struct {
//	    handler func([]byte)
//	    ch      chan []byte
//	    done    chan struct{}
//	}
//
//	// Process loop
//	func (s *subscription) process() {
//	    defer close(s.done)
//	    for data := range s.ch {
//	        s.handler(data)
//	    }
//	}
//
// This model ensures:
//   - Publisher never blocks (writes to buffered channel)
//   - Each subscriber processes at its own pace
//   - Slow consumers don't affect other subscribers
//
// # Cluster Usage (ClusterPlugin)
//
// Session join event:
//
//	ps.Publish(ctx, "session.joined", marshal(SessionEvent{
//	    SessionID: sessID,
//	    NodeID:    nodeID,
//	    Protocol:  protocol,
//	}))
//
// Cross-node routing:
//
//	// Publishing node
//	ps.Publish(ctx, "node."+targetNodeID+".route", marshal(RouteMessage{
//	    TargetID: targetSessID,
//	    Payload:  data,
//	}))
//
//	// Subscribing node
//	ps.Subscribe(ctx, "node."+myNodeID+".route", func(data []byte) {
//	    msg := unmarshal(data)
//	    sess, ok := manager.Get(msg.TargetID)
//	    if ok {
//	        sess.Send(msg.Payload)
//	    }
//	})
//
// # Adapters
//
// For production cluster deployments:
//
//	// Redis PubSub
//	import "github.com/X1aSheng/shark-socket/internal/infra/pubsub/redis"
//	ps, _ := redis.NewRedisPubSub(client)
//
//	// NATS PubSub
//	import "github.com/X1aSheng/shark-socket/internal/infra/pubsub/nats"
//	ps, _ := nats.NewNATSPubSub(conn)
//
//	// Kafka PubSub
//	import "github.com/X1aSheng/shark-socket/internal/infra/pubsub/kafka"
//	ps, _ := kafka.NewKafkaPubSub(producer, consumer)
//
// Adapter comparison:
//
//	┌──────────────┬───────────────┬───────────────┬────────────────┐
//	│ Feature      │ Channel       │ Redis         │ NATS           │
//	├──────────────┼───────────────┼───────────────┼────────────────┤
//	│ Scope        │ In-process    │ Cross-node    │ Cross-cluster  │
//	│ Latency      │ ~1μs          │ ~1ms          │ ~0.5ms         │
//	│ Persistence  │ No            │ Optional      │ Optional       │
//	│ Ordering     │ Per-subscriber│ Per-channel   │ Per-subject    │
//	│ Throughput   │ 10M+ msg/s    │ 1M+ msg/s     │ 5M+ msg/s      │
//	│ Complexity   │ None          │ Redis dep     │ NATS dep       │
//	└──────────────┴───────────────┴───────────────┴────────────────┘
//
// # Thread Safety
//
// All operations are thread-safe:
//   - sync.RWMutex for subscription management
//   - Channel operations are inherently safe
//   - Unsubscribe is idempotent
//
// # Error Handling
//
//	ErrPubSubClosed is returned when operating on a closed PubSub:
//
//	err := ps.Publish(ctx, "topic", data)
//	if errors.Is(err, errs.ErrPubSubClosed) {
//	    // PubSub was closed, reconnect or stop
//	}
//
// # Graceful Shutdown
//
// Close() ensures all messages are processed before returning:
//
//	ps.Close()
//	// Waits for all subscriber goroutines to drain channels
//	// Then closes all channels
//	// Returns when all subscribers have exited
//
// # Metrics
//
// PubSub emits Prometheus metrics:
//
//	shark_pubsub_published_total{topic}
//	shark_pubsub_subscribers{topic}
//	shark_pubsub_dropped_total{topic}
//	shark_pubsub_latency_seconds{topic}
//
// # Configuration Options
//
//	NewChannelPubSub(
//	    WithBufferSize(256),       // Per-subscriber channel capacity
//	    WithDropPolicy(DropOldest), // Slow consumer strategy
//	)
//
package pubsub
