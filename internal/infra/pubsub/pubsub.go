package pubsub

import (
	"context"
	"errors"
	"log"
	"sync"
	"sync/atomic"
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
	id      uint64
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
	id    uint64
}

func (s *channelSubscription) Unsubscribe() error {
	s.ps.unsubscribe(s.topic, s.id)
	return nil
}

func (s *channelSubscription) Topic() string { return s.topic }

// ChannelPubSub implements PubSub using Go channels with fan-out goroutines.
type ChannelPubSub struct {
	mu          sync.RWMutex
	subscribers map[string]map[uint64]*subscriber
	closed      bool
	nextID      atomic.Uint64
}

// NewChannelPubSub creates a new channel-based PubSub.
func NewChannelPubSub() *ChannelPubSub {
	return &ChannelPubSub{
		subscribers: make(map[string]map[uint64]*subscriber),
	}
}

func (ps *ChannelPubSub) Publish(_ context.Context, topic string, data []byte) error {
	ps.mu.RLock()
	if ps.closed {
		ps.mu.RUnlock()
		return ErrPubSubClosed
	}
	subs := make([]*subscriber, 0, len(ps.subscribers[topic]))
	for _, sub := range ps.subscribers[topic] {
		subs = append(subs, sub)
	}
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

	sub := newSubscriber(handler)
	id := ps.nextID.Add(1)
	sub.id = id
	if ps.subscribers[topic] == nil {
		ps.subscribers[topic] = make(map[uint64]*subscriber)
	}
	ps.subscribers[topic][id] = sub

	return &channelSubscription{topic: topic, ps: ps, id: id}, nil
}

func (ps *ChannelPubSub) unsubscribe(topic string, id uint64) {
	ps.mu.Lock()
	subs := ps.subscribers[topic]
	sub, ok := subs[id]
	if !ok {
		ps.mu.Unlock()
		return
	}
	delete(subs, id)
	if len(subs) == 0 {
		delete(ps.subscribers, topic)
	}
	ps.mu.Unlock()
	sub.stop()
}

// Close shuts down the PubSub, rejecting further operations.
func (ps *ChannelPubSub) Close() {
	ps.mu.Lock()
	allSubs := ps.subscribers
	ps.subscribers = make(map[string]map[uint64]*subscriber)
	ps.closed = true
	ps.mu.Unlock()

	for _, subs := range allSubs {
		for _, sub := range subs {
			sub.stop()
		}
	}
}
