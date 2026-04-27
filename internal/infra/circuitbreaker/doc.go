// Package circuitbreaker provides the circuit breaker pattern implementation.
//
// This package implements the circuit breaker pattern to prevent cascading failures
// by stopping requests to failing services and allowing them time to recover.
//
// # Concept
//
// The circuit breaker pattern acts as a proxy for service calls:
//
//	┌─────────────────────────────────────────────────────────────────┐
//	│                    Circuit Breaker States                        │
//	├─────────────────────────────────────────────────────────────────┤
//	│                                                                 │
//	│     ┌─────────┐         ┌─────────┐         ┌──────────────┐  │
//	│     │ Closed  │ ──fail── │  Open   │ ──timeout│  Half-Open   │  │
//	│     │ (OK)   │          │ (Block) │          │  (Testing)   │  │
//	│     └─────────┘         └─────────┘         └──────┬───────┘  │
//	│          │                   ▲                    │         │
//	│          └─────────success──┘                    │         │
//	│                                                   ▼         │
//	│                                         success ─────────────┘
//	│                                         failure ─────────────┐
//	│                                                                 │
//	└─────────────────────────────────────────────────────────────────┘
//
// # State Machine
//
// | State | Behavior | Transition |
// |-------|----------|-----------|
// | Closed | Normal operation, calls succeed/fail normally | Failure count >= threshold → Open |
// | Open | Calls blocked immediately, return ErrCircuitOpen | Timeout expires → Half-Open |
// | Half-Open | Limited test calls allowed | Success → Closed, Failure → Open |
//
// # Usage
//
//	import "github.com/X1aSheng/shark-socket/internal/infra/circuitbreaker"
//
//	cb := circuitbreaker.New(5, 30*time.Second) // 5 failures, 30s timeout
//
//	result, err := cb.Do(func() error {
//	    return callExternalService()
//	})
//	if errors.Is(err, circuitbreaker.ErrCircuitOpen) {
//	    // Circuit is open, service unavailable
//	    return fallback()
//	}
//
// # Configuration
//
//	New(threshold int64, timeout time.Duration)
//
//	threshold: Consecutive failures before opening (default: 5)
//	timeout: Time to wait before testing recovery (default: 30s)
//
// # Half-Open Probing
//
// When transitioning to half-open:
//
//  1. First call is allowed through (probe)
//  2. Success: circuit closes, normal operation resumes
//  3. Failure: circuit reopens, timeout restarts
//
// This single probe prevents thundering herd while testing recovery.
//
// # State Access
//
// Query circuit state for monitoring:
//
//	state := cb.State()
//	switch state {
//	case circuitbreaker.StateClosed:
//	    log.Info("circuit normal")
//	case circuitbreaker.StateOpen:
//	    log.Warn("circuit open, calls blocked")
//	case circuitbreaker.StateHalfOpen:
//	    log.Info("circuit testing recovery")
//	}
//
// # Thread Safety
//
// Circuit breaker operations are thread-safe:
//   - Atomic state transitions (no locks in hot path)
//   - Atomic failure/success counters
//   - No blocking operations in critical path
//
// # Use Cases
//
// External service protection (e.g., database, cache):
//
//	cb := circuitbreaker.New(3, 10*time.Second)
//	_, err := cb.Do(func() error {
//	    return redisClient.Get(ctx, key)
//	})
//	if errors.Is(err, circuitbreaker.ErrCircuitOpen) {
//	    return localCache.Get(key)  // Fallback to local
//	}
//
// Cluster component protection:
//
//	cb := circuitbreaker.New(5, 30*time.Second)
//	_, err := cb.Do(func() error {
//	    return pubsub.Publish(ctx, topic, data)
//	})
//	if errors.Is(err, circuitbreaker.ErrCircuitOpen) {
//	    // PubSub unavailable, log locally
//	    log.Warn("pubsub circuit open, skipping publish")
//	}
//
// Persistence protection (PersistencePlugin):
//
//	// Wrap Store calls with circuit breaker
//	_, err := persistCB.Do(func() error {
//	    return store.Save(ctx, key, val)
//	})
//	// Circuit open: skip persistence, continue without saving
//
// # Metrics
//
// Circuit breaker emits Prometheus metrics:
//
//	shark_circuit_breaker_state{component}
//	shark_circuit_breaker_calls_total{component, state, result}
//	shark_circuit_breaker_failures_total{component}
//
// States: closed, open, half_open
// Results: success, failure, blocked
//
// # Comparison with Retries
//
// Circuit breaker vs exponential backoff:
//
//	┌───────────────────────────────────────────────────────────────┐
//	│  Retry Only: Request sent repeatedly until success or timeout  │
//	│  - Failing service receives more load during outage            │
//	│  - Caller blocked until retries exhausted                     │
//	├───────────────────────────────────────────────────────────────┤
//	│  Circuit Breaker: Fail fast, stop sending requests             │
//	│  - Failing service gets breathing room to recover             │
//	│  - Caller fails immediately, can try fallback                 │
//	└───────────────────────────────────────────────────────────────┘
//
// Best practice: Use both (with circuit breaker wrapping retry logic)
//
// # Nested Circuit Breakers
//
// For complex dependencies, consider nested circuits:
//
//	┌──────────────────────────────────────────────────────┐
//	│  High-Level Circuit (service)                        │
//	│  ┌────────────────┐  ┌────────────────┐             │
//	│  │ DB Circuit      │  │ Cache Circuit   │            │
//	│  │ threshold: 3    │  │ threshold: 10  │            │
//	│  │ timeout: 10s    │  │ timeout: 5s    │            │
//	│  └────────────────┘  └────────────────┘             │
//	└──────────────────────────────────────────────────────┘
//
// Database failures open quickly (protect DB),
// cache failures open slowly (cache is optional).
//
// # Error Handling
//
// ErrCircuitOpen is returned when circuit is open:
//
//	import "github.com/X1aSheng/shark-socket/internal/errs"
//
//	_, err := cb.Do(expensiveOperation)
//	if errors.Is(err, errs.ErrCircuitOpen) {
//	    // Circuit breaker blocked the call
//	}
//
// Callers should:
//   1. Check with errors.Is()
//   2. Return fallback value or cached data
//   3. Log circuit state for monitoring
//
package circuitbreaker