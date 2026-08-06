package plugin

import "sync"

// lifecycle serializes Start/Stop for plugins that run a background goroutine.
// It is restartable (Stop then Start) and safe under concurrent Start/Stop
// calls. It replaces the former "reassign sync.Once{}" restart pattern, which
// was a data race: Stop's Once.Do could read the struct while Start wrote it.
//
// Each Start cycle owns a fresh WaitGroup; shutdown waits on the cycle that is
// actually running. Because `started` stays true until the goroutine calls
// done(), a Start issued while a previous Stop is still waiting is a no-op, so
// the WaitGroup is never reused across cycles (which would panic with "WaitGroup
// is reused before previous Wait has returned").
type lifecycle struct {
	mu      sync.Mutex
	started bool
	closed  bool
	stopCh  chan struct{}
	running *sync.WaitGroup // WaitGroup of the current cycle; nil when stopped
}

// begin marks the plugin as started and returns the per-cycle stop channel.
// It returns (nil, false) if the plugin is already started or still stopping.
// The returned stop channel is valid until shutdown; done() must be called by
// the background goroutine (or by the caller on an early return) to balance
// the WaitGroup Add performed here.
func (l *lifecycle) begin() (chan struct{}, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.started {
		return nil, false
	}
	l.started = true
	l.closed = false
	l.stopCh = make(chan struct{})
	l.running = &sync.WaitGroup{}
	l.running.Add(1)
	return l.stopCh, true
}

// done marks the plugin as not started and releases the WaitGroup slot added by
// begin. The background goroutine calls this when it exits so the plugin can be
// restarted.
func (l *lifecycle) done() {
	l.mu.Lock()
	wg := l.running
	l.running = nil
	l.started = false
	l.mu.Unlock()
	if wg != nil {
		wg.Done()
	}
}

// shutdown signals the background goroutine to exit and waits for it to finish.
// It is idempotent and safe to call from multiple goroutines.
func (l *lifecycle) shutdown() {
	l.mu.Lock()
	if !l.started {
		l.mu.Unlock()
		return
	}
	if !l.closed {
		l.closed = true
		close(l.stopCh)
	}
	wg := l.running
	l.mu.Unlock()
	if wg != nil {
		wg.Wait()
	}
}
