package defense

import (
	"fmt"
	"sync"
	"time"
)

type samplerEntry struct {
	count    int
	lastLog  time.Time
}

// LogSampler reduces high-frequency log output by aggregating repeated messages.
type LogSampler struct {
	mu      sync.Mutex
	entries map[string]*samplerEntry
	window  time.Duration
}

// NewLogSampler creates a log sampler with the given aggregation window.
func NewLogSampler(window time.Duration) *LogSampler {
	return &LogSampler{
		entries: make(map[string]*samplerEntry),
		window:  window,
	}
}

// ShouldLog returns true if this message should be logged.
// It tracks identical keys and only logs the first occurrence per window,
// then appends "N times omitted" to subsequent logs.
func (s *LogSampler) ShouldLog(key string) (shouldLog bool, summary string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	entry, ok := s.entries[key]

	if !ok {
		s.entries[key] = &samplerEntry{count: 1, lastLog: now}
		return true, ""
	}

	entry.count++
	if now.Sub(entry.lastLog) >= s.window {
		if entry.count > 1 {
			summary = fmt.Sprintf("(omitted %d times)", entry.count-1)
		}
		entry.count = 1
		entry.lastLog = now
		return true, summary
	}

	return false, ""
}

// Clean removes stale entries older than 2x the window.
func (s *LogSampler) Clean() {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-2 * s.window)
	for k, v := range s.entries {
		if v.lastLog.Before(cutoff) {
			delete(s.entries, k)
		}
	}
}
