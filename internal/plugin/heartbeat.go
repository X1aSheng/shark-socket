package plugin

import (
	"sync"
	"time"

	"github.com/X1aSheng/shark-socket/internal/types"
)

// TimeWheel manages session timeouts with a single goroutine.
type TimeWheel struct {
	mu      sync.Mutex
	slots   []map[uint64]time.Time
	current int
	tickInterval time.Duration
	size    int
	done    chan struct{}
	onTimeout func(uint64)
	wg      sync.WaitGroup
}

// NewTimeWheel creates a time wheel with the given tick interval and number of slots.
func NewTimeWheel(tickInterval time.Duration, slots int, onTimeout func(uint64)) *TimeWheel {
	tw := &TimeWheel{
		slots:        make([]map[uint64]time.Time, slots),
		tickInterval: tickInterval,
		size:         slots,
		done:         make(chan struct{}),
		onTimeout:    onTimeout,
	}
	for i := range tw.slots {
		tw.slots[i] = make(map[uint64]time.Time)
	}
	tw.wg.Add(1)
	go tw.tickLoop()
	return tw
}

func (tw *TimeWheel) tickLoop() {
	defer tw.wg.Done()
	ticker := time.NewTicker(tw.tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			tw.advance()
		case <-tw.done:
			return
		}
	}
}

func (tw *TimeWheel) advance() {
	tw.mu.Lock()
	defer tw.mu.Unlock()

	now := time.Now()

	// Only check the current slot — entries land in exactly one slot.
	slot := tw.slots[tw.current]
	for id, expiry := range slot {
		if now.After(expiry) {
			delete(slot, id)
			if tw.onTimeout != nil {
				tw.onTimeout(id)
			}
		}
	}
	tw.current = (tw.current + 1) % tw.size
}

// Add adds a session timeout.
func (tw *TimeWheel) Add(id uint64, timeout time.Duration) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	slot := tw.slotFor(timeout)
	tw.slots[slot][id] = time.Now().Add(timeout)
}

// Reset resets a session timeout.
func (tw *TimeWheel) Reset(id uint64, timeout time.Duration) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	// Remove from all slots
	for _, slot := range tw.slots {
		delete(slot, id)
	}
	slot := tw.slotFor(timeout)
	tw.slots[slot][id] = time.Now().Add(timeout)
}

// Remove removes a session from the time wheel.
func (tw *TimeWheel) Remove(id uint64) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	for _, slot := range tw.slots {
		delete(slot, id)
	}
}

func (tw *TimeWheel) slotFor(timeout time.Duration) int {
	ticks := int(timeout / tw.tickInterval)
	if ticks < 1 {
		ticks = 1
	}
	return (tw.current + ticks) % tw.size
}

// Stop stops the time wheel goroutine.
func (tw *TimeWheel) Stop() {
	close(tw.done)
	tw.wg.Wait()
}

// HeartbeatPlugin detects idle sessions using a time wheel.
type HeartbeatPlugin struct {
	timeWheel *TimeWheel
	interval  time.Duration
	timeout   time.Duration
	manager   func() types.SessionManager
}

// NewHeartbeatPlugin creates a heartbeat plugin.
func NewHeartbeatPlugin(interval, timeout time.Duration, mgr func() types.SessionManager) *HeartbeatPlugin {
	p := &HeartbeatPlugin{
		interval: interval,
		timeout:  timeout,
		manager:  mgr,
	}
	slots := int(timeout/interval) + 1
	if slots < 10 {
		slots = 10
	}
	p.timeWheel = NewTimeWheel(interval, slots, p.onTimeout)
	return p
}

func (p *HeartbeatPlugin) Name() string  { return "heartbeat" }
func (p *HeartbeatPlugin) Priority() int { return 30 }

func (p *HeartbeatPlugin) OnAccept(sess types.RawSession) error {
	p.timeWheel.Add(sess.ID(), p.timeout)
	return nil
}

func (p *HeartbeatPlugin) OnMessage(sess types.RawSession, data []byte) ([]byte, error) {
	p.timeWheel.Reset(sess.ID(), p.timeout)
	return data, nil
}

func (p *HeartbeatPlugin) OnClose(sess types.RawSession) {
	p.timeWheel.Remove(sess.ID())
}

func (p *HeartbeatPlugin) onTimeout(id uint64) {
	if m := p.manager(); m != nil {
		if sess, ok := m.Get(id); ok {
			_ = sess.Close()
		}
	}
}

// Close stops the time wheel.
func (p *HeartbeatPlugin) Close() { p.timeWheel.Stop() }
