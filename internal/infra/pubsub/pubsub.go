package pubsub

import (
	"context"
	"errors"
	"log"
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

const subscriberBufSize = 256

type subscriber struct {
	handler func([]byte)
	ch      chan []byte
	done    chan struct{}
}

func newSubscriber(handler func([]byte)) *subscriber {
	s := &subscriber{
		handler: handler,
		ch:      make(chan []byte, subscriberBufSize),
		done:    make(chan struct{}),
	}
	go s.process()
	return s
}

func (s *subscriber) process() {
	defer close(s.done)
	for data := range s.ch {
		s.handler(data)
	}
}

func (s *subscriber) stop() {
	close(s.ch)
	<-s.done
}

type channelSubscription struct {
	topic string
	ps    *ChannelPubSub
	key   subKey
}

type subKey struct {
	topic string
	idx   int
}

func (s *channelSubscription) Unsubscribe() error {
	s.ps.unsubscribe(s.topic, s.key)
	return nil
}

func (s *channelSubscription) Topic() string { return s.topic }

// ChannelPubSub implements PubSub using Go channels with fan-out goroutines.
type ChannelPubSub struct {
	mu          sync.RWMutex
	subscribers map[string][]*subscriber
	closed      bool
}

// NewChannelPubSub creates a new channel-based PubSub.
func NewChannelPubSub() *ChannelPubSub {
	return &ChannelPubSub{
		subscribers: make(map[string][]*subscriber),
	}
}

func (ps *ChannelPubSub) Publish(_ context.Context, topic string, data []byte) error {
	ps.mu.RLock()
	if ps.closed {
		ps.mu.RUnlock()
		return ErrPubSubClosed
	}
	subs := make([]*subscriber, len(ps.subscribers[topic]))
	copy(subs, ps.subscribers[topic])
	ps.mu.RUnlock()

	for _, sub := range subs {
		cp := make([]byte, len(data))
		copy(cp, data)
		select {
		case sub.ch <- cp:
		default:
			log.Printf("pubsub: message dropped on topic %q (slow consumer)", topic)
		}
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
	sub := newSubscriber(handler)
	ps.subscribers[topic] = append(ps.subscribers[topic], sub)

	return &channelSubscription{topic: topic, ps: ps, key: subKey{topic: topic, idx: idx}}, nil
}

func (ps *ChannelPubSub) unsubscribe(topic string, key subKey) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	subs := ps.subscribers[topic]
	if key.idx >= len(subs) {
		return
	}
	sub := subs[key.idx]
	ps.subscribers[topic] = append(subs[:key.idx], subs[key.idx+1:]...)
	ps.mu.Unlock()
	sub.stop()
	ps.mu.Lock()
}

// Close shuts down the PubSub, rejecting further operations.
func (ps *ChannelPubSub) Close() {
	ps.mu.Lock()
	allSubs := ps.subscribers
	ps.subscribers = make(map[string][]*subscriber)
	ps.closed = true
	ps.mu.Unlock()

	for _, subs := range allSubs {
		for _, sub := range subs {
			sub.stop()
		}
	}
}
