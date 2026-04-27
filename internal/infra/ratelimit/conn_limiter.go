package ratelimit

import (
	"net"
	"sync"
	"time"
)

// ConnectionLimiter limits connections per IP address.
type ConnectionLimiter struct {
	mu       sync.RWMutex
	counts   map[string]*ipCount
	rate     int           // max connections per window
	window   time.Duration // time window
	cleanupInterval time.Duration
}

// ipCount tracks connection count for an IP.
type ipCount struct {
	count      int
	lastUpdate time.Time
}

// NewConnectionLimiter creates a connection rate limiter.
// rate: max connections allowed per window duration.
func NewConnectionLimiter(rate int, window time.Duration) *ConnectionLimiter {
	cl := &ConnectionLimiter{
		counts: make(map[string]*ipCount),
		rate:   rate,
		window: window,
		cleanupInterval: window * 2,
	}
	go cl.cleanupLoop()
	return cl
}

// Allow checks if a new connection from the given IP is allowed.
// Returns true if allowed, false if rate limit exceeded.
func (c *ConnectionLimiter) Allow(ip string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	ic, exists := c.counts[ip]

	if !exists || now.Sub(ic.lastUpdate) > c.window {
		// First connection or window expired - start new window
		c.counts[ip] = &ipCount{count: 1, lastUpdate: now}
		return true
	}

	if ic.count >= c.rate {
		// Rate limit exceeded
		return false
	}

	// Increment count within window
	ic.count++
	ic.lastUpdate = now
	return true
}

// AllowAddr checks if a connection from the net.Addr is allowed.
func (c *ConnectionLimiter) AllowAddr(addr net.Addr) bool {
	if addr == nil {
		return true
	}
	ip, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		// If we can't parse, allow by default
		return true
	}
	return c.Allow(ip)
}

// Remove decrements the connection count for an IP.
// Use when a connection closes.
func (c *ConnectionLimiter) Remove(ip string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	ic, exists := c.counts[ip]
	if !exists {
		return
	}
	ic.count--
	if ic.count <= 0 {
		delete(c.counts, ip)
	}
}

// RemoveAddr removes the connection from the rate limiter.
func (c *ConnectionLimiter) RemoveAddr(addr net.Addr) {
	if addr == nil {
		return
	}
	ip, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return
	}
	c.Remove(ip)
}

// SetRate updates the rate limit.
func (c *ConnectionLimiter) SetRate(rate int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rate = rate
}

// SetWindow updates the time window.
func (c *ConnectionLimiter) SetWindow(window time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.window = window
}

// Count returns the current connection count for an IP.
func (c *ConnectionLimiter) Count(ip string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if ic, exists := c.counts[ip]; exists {
		return ic.count
	}
	return 0
}

// ActiveCount returns the number of IPs currently being tracked.
func (c *ConnectionLimiter) ActiveCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.counts)
}

// Reset clears all tracked connections.
func (c *ConnectionLimiter) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts = make(map[string]*ipCount)
}

func (c *ConnectionLimiter) cleanupLoop() {
	ticker := time.NewTicker(c.cleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		c.cleanup()
	}
}

func (c *ConnectionLimiter) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for ip, ic := range c.counts {
		if now.Sub(ic.lastUpdate) > c.window*2 {
			delete(c.counts, ip)
		}
	}
}