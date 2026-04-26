package defense

import (
	"log"
	"sync"
	"time"
)

// BackpressureController monitors write queue water levels.
type BackpressureController struct {
	highWatermark float64 // 0.0 - 1.0, e.g. 0.8 = 80% full
	criticalMark  float64 // e.g. 1.0 = 100% full
}

// NewBackpressureController creates a new controller.
func NewBackpressureController(highWatermark, criticalMark float64) *BackpressureController {
	return &BackpressureController{
		highWatermark: highWatermark,
		criticalMark:  criticalMark,
	}
}

// CheckQueueLevel evaluates the write queue fill ratio and returns an action.
func (b *BackpressureController) CheckQueueLevel(currentSize, capacity int) string {
	if capacity <= 0 {
		return "ok"
	}
	ratio := float64(currentSize) / float64(capacity)

	if ratio >= b.criticalMark {
		return "close"
	}
	if ratio >= b.highWatermark {
		log.Printf("Backpressure: queue at %.0f%% capacity", ratio*100)
		return "warn"
	}
	return "ok"
}

// BroadcastLimiter controls broadcast rate to prevent storms.
type BroadcastLimiter struct {
	mu          sync.Mutex
	maxSize     int
	minInterval time.Duration
	lastSend    map[string]time.Time
}

// NewBroadcastLimiter creates a broadcast rate limiter.
func NewBroadcastLimiter(maxSize int, minInterval time.Duration) *BroadcastLimiter {
	return &BroadcastLimiter{
		maxSize:     maxSize,
		minInterval: minInterval,
		lastSend:    make(map[string]time.Time),
	}
}

// Allow checks if a broadcast of the given size is allowed.
func (bl *BroadcastLimiter) Allow(topic string, dataSize int) bool {
	bl.mu.Lock()
	defer bl.mu.Unlock()

	if dataSize > bl.maxSize {
		return false
	}
	if last, ok := bl.lastSend[topic]; ok {
		if time.Since(last) < bl.minInterval {
			return false
		}
	}
	bl.lastSend[topic] = time.Now()
	return true
}
