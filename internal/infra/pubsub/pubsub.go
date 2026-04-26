package pubsub

import (
	"context"
	"errors"
	"sync"
)

var ErrPubSubClosed = errors.New("pubsub: closed")

// Subscription represents an active subscription to a topic.
type Subscription interface {
	Unsubscribe() error
	Topic() string
}

// PubSub provides publish-subscribe messaging.
type PubSub interface {
	Publish(ctx context.Context, topic string, data []byte) error
	Subscribe(ctx context.Context, topic string, handler func([]byte)) (Subscription, error)
}

type subscriber struct {
	handler func([]byte)
	done    chan struct{}
}

type channelSubscription struct {
	topic string
	ps    *ChannelPubSub
	index int
}

func (s *channelSubscription) Unsubscribe() error {
	s.ps.unsubscribe(s.topic, s.index)
	return nil
}

func (s *channelSubscription) Topic() string { return s.topic }

// ChannelPubSub implements PubSub using Go channels with fan-out goroutines.
type ChannelPubSub struct {
	mu          sync.RWMutex
	subscribers map[string][]subscriber
	closed      bool
}

// NewChannelPubSub creates a new channel-based PubSub.
func NewChannelPubSub() *ChannelPubSub {
	return &ChannelPubSub{
		subscribers: make(map[string][]subscriber),
	}
}

func (ps *ChannelPubSub) Publish(_ context.Context, topic string, data []byte) error {
	ps.mu.RLock()
	if ps.closed {
		ps.mu.RUnlock()
		return ErrPubSubClosed
	}
	subs := make([]subscriber, len(ps.subscribers[topic]))
	copy(subs, ps.subscribers[topic])
	ps.mu.RUnlock()

	for _, sub := range subs {
		cp := make([]byte, len(data))
		copy(cp, data)
		sub.handler(cp)
	}
	return nil
}

func (ps *ChannelPubSub) Subscribe(_ context.Context, topic string, handler func([]byte)) (Subscription, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.closed {
		return nil, ErrPubSubClosed
	}

	idx := len(ps.subscribers[topic])
	ps.subscribers[topic] = append(ps.subscribers[topic], subscriber{handler: handler})

	return &channelSubscription{topic: topic, ps: ps, index: idx}, nil
}

func (ps *ChannelPubSub) unsubscribe(topic string, index int) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	subs := ps.subscribers[topic]
	if index >= len(subs) {
		return
	}
	ps.subscribers[topic] = append(subs[:index], subs[index+1:]...)
}

// Close shuts down the PubSub, rejecting further operations.
func (ps *ChannelPubSub) Close() {
	ps.mu.Lock()
	ps.closed = true
	ps.subscribers = make(map[string][]subscriber)
	ps.mu.Unlock()
}
