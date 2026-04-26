package defense

import (
	"errors"
	"log"
	"sync/atomic"
	"time"
)

// OverloadProtector monitors system resources and triggers degradation.
type OverloadProtector struct {
	highWater    int64
	lowWater     int64
	overloaded   atomic.Bool
	checkInterval time.Duration
	stopCh       chan struct{}
	sessions     func() int64
}

// NewOverloadProtector creates a new overload protector.
func NewOverloadProtector(highWater, lowWater int64, sessions func() int64) *OverloadProtector {
	return &OverloadProtector{
		highWater:     highWater,
		lowWater:      lowWater,
		checkInterval: 5 * time.Second,
		stopCh:        make(chan struct{}),
		sessions:      sessions,
	}
}

// Start begins periodic resource monitoring.
func (o *OverloadProtector) Start() {
	go o.checkLoop()
}

func (o *OverloadProtector) checkLoop() {
	ticker := time.NewTicker(o.checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			count := o.sessions()
			if !o.overloaded.Load() && count >= o.highWater {
				o.overloaded.Store(true)
				log.Printf("OverloadProtector: entering overload mode (sessions=%d)", count)
			} else if o.overloaded.Load() && count <= o.lowWater {
				o.overloaded.Store(false)
				log.Printf("OverloadProtector: leaving overload mode (sessions=%d)", count)
			}
		case <-o.stopCh:
			return
		}
	}
}

// IsOverloaded returns true if the system is in overload mode.
func (o *OverloadProtector) IsOverloaded() bool {
	return o.overloaded.Load()
}

// ErrOverloaded is returned by Guard when the system is overloaded.
var ErrOverloaded = errors.New("shark: server overloaded")

// Guard checks overload state and returns an error if the system is overloaded.
// Protocol servers should call this in the accept path to actively reject new connections.
func (o *OverloadProtector) Guard() error {
	if o.overloaded.Load() {
		return ErrOverloaded
	}
	return nil
}

// Stop stops the monitor.
func (o *OverloadProtector) Stop() { close(o.stopCh) }
