package plugin

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"time"

	"github.com/X1aSheng/shark-socket/internal/errs"
	"github.com/X1aSheng/shark-socket/internal/infra/cache"
	"github.com/X1aSheng/shark-socket/internal/infra/pubsub"
	"github.com/X1aSheng/shark-socket/internal/types"
)

// ClusterPlugin enables cross-node session routing and event propagation.
type ClusterPlugin struct {
	nodeID       string
	pubsub       pubsub.PubSub
	cache        cache.Cache
	sessionTTL   time.Duration
	heartbeatTTL time.Duration
	manager      func() types.SessionManager
	stopCh       chan struct{}
	wg           sync.WaitGroup
	routeSub     pubsub.Subscription
}

// ClusterOption configures ClusterPlugin.
type ClusterOption func(*ClusterPlugin)

// WithClusterSessionTTL sets session route cache TTL.
func WithClusterSessionTTL(d time.Duration) ClusterOption {
	return func(p *ClusterPlugin) { p.sessionTTL = d }
}

// WithClusterHeartbeatTTL sets node heartbeat TTL.
func WithClusterHeartbeatTTL(d time.Duration) ClusterOption {
	return func(p *ClusterPlugin) { p.heartbeatTTL = d }
}

// NewClusterPlugin creates a new cluster plugin.
func NewClusterPlugin(nodeID string, ps pubsub.PubSub, c cache.Cache, mgr func() types.SessionManager, opts ...ClusterOption) *ClusterPlugin {
	p := &ClusterPlugin{
		nodeID:       nodeID,
		pubsub:       ps,
		cache:        c,
		sessionTTL:   5 * time.Minute,
		heartbeatTTL: 30 * time.Second,
		manager:      mgr,
		stopCh:       make(chan struct{}),
	}
	for _, opt := range opts {
		opt(p)
	}
	p.wg.Add(2)
	go p.heartbeatLoop()
	go p.routeSubscribe()
	return p
}

func (p *ClusterPlugin) Name() string  { return "cluster" }
func (p *ClusterPlugin) Priority() int { return 40 }

func (p *ClusterPlugin) OnAccept(sess types.RawSession) error {
	ctx := context.Background()
	routeKey := "session:route:" + strconv.FormatUint(sess.ID(), 10)
	_ = p.cache.Set(ctx, routeKey, []byte(p.nodeID), p.sessionTTL)

	msg, _ := json.Marshal(map[string]string{
		"session_id": strconv.FormatUint(sess.ID(), 10),
		"node_id":    p.nodeID,
		"event":      "joined",
	})
	_ = p.pubsub.Publish(ctx, "session.events", msg)
	return nil
}

func (p *ClusterPlugin) OnMessage(sess types.RawSession, data []byte) ([]byte, error) {
	return data, nil
}

func (p *ClusterPlugin) OnClose(sess types.RawSession) {
	ctx := context.Background()
	routeKey := "session:route:" + strconv.FormatUint(sess.ID(), 10)
	_ = p.cache.Del(ctx, routeKey)

	msg, _ := json.Marshal(map[string]string{
		"session_id": strconv.FormatUint(sess.ID(), 10),
		"node_id":    p.nodeID,
		"event":      "left",
	})
	_ = p.pubsub.Publish(ctx, "session.events", msg)
}

// Route sends data to a session, potentially on another node.
func (p *ClusterPlugin) Route(targetID uint64, data []byte) error {
	if m := p.manager(); m != nil {
		if sess, ok := m.Get(targetID); ok {
			return sess.Send(data)
		}
	}
	ctx := context.Background()
	routeKey := "session:route:" + strconv.FormatUint(targetID, 10)
	nodeBytes, err := p.cache.Get(ctx, routeKey)
	if err != nil {
		return errs.ErrSessionNotFound
	}
	msg, _ := json.Marshal(map[string]any{
		"target_id": targetID,
		"node_id":   string(nodeBytes),
		"payload":   data,
	})
	return p.pubsub.Publish(ctx, "node."+string(nodeBytes)+".route", msg)
}

func (p *ClusterPlugin) heartbeatLoop() {
	defer p.wg.Done()
	ticker := time.NewTicker(p.heartbeatTTL / 2)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			ctx := context.Background()
			meta, _ := json.Marshal(map[string]string{
				"node_id": p.nodeID,
				"seen_at": time.Now().Format(time.RFC3339),
			})
			_ = p.cache.Set(ctx, "node:"+p.nodeID, meta, p.heartbeatTTL)
		case <-p.stopCh:
			return
		}
	}
}

func (p *ClusterPlugin) routeSubscribe() {
	defer p.wg.Done()
	sub, err := p.pubsub.Subscribe(context.Background(), "node."+p.nodeID+".route", func(msg []byte) {
		var routeMsg struct {
			TargetID uint64 `json:"target_id"`
			Payload  []byte `json:"payload"`
		}
		if err := json.Unmarshal(msg, &routeMsg); err != nil {
			return
		}
		if m := p.manager(); m != nil {
			if sess, ok := m.Get(routeMsg.TargetID); ok {
				_ = sess.Send(routeMsg.Payload)
			}
		}
	})
	if err != nil {
		return
	}
	p.routeSub = sub
	<-p.stopCh
}

// Close stops the cluster plugin goroutines and unsubscribes.
func (p *ClusterPlugin) Close() {
	close(p.stopCh)
	p.wg.Wait()
	if p.routeSub != nil {
		_ = p.routeSub.Unsubscribe()
	}
}
