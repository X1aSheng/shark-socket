// Package defense provides overload protection, backpressure control, and log sampling.
//
// This package implements defense mechanisms to protect the framework from
// resource exhaustion, denial-of-service attacks, and log flooding.
//
// # Defense Layers
//
//	┌─────────────────────────────────────────────────────────────────┐
//	│                Defense-in-Depth Architecture                      │
//	├─────────────────────────────────────────────────────────────────┤
//	│                                                                   │
//	│  L1: Connection Level                                            │
//	│  ┌──────────────────────────────────────────────────────────────┐│
//	│  │ MaxSessions          → Hard connection limit                  ││
//	│  │ RateLimitPlugin      → Connection + message rate limiting     ││
//	│  │ BlacklistPlugin      → IP/CIDR blocking                      ││
//	│  │ AutoBanPlugin        → Automatic IP banning                   ││
//	│  └──────────────────────────────────────────────────────────────┘│
//	│                                                                   │
//	│  L2: Resource Level                                              │
//	│  ┌──────────────────────────────────────────────────────────────┐│
//	│  │ OverloadProtector   → System resource monitoring              ││
//	│  │ BackpressureControl → Write queue management                  ││
//	│  │ WorkerPool policies → Task queue overflow handling            ││
//	│  └──────────────────────────────────────────────────────────────┘│
//	│                                                                   │
//	│  L3: Observability Level                                         │
//	│  ┌──────────────────────────────────────────────────────────────┐│
//	│  │ LogSampler          → High-frequency log sampling             ││
//	│  │ Metrics              → Resource usage tracking                ││
//	│  │ Health checks        → /healthz, /readyz                     ││
//	│  └──────────────────────────────────────────────────────────────┘│
//	│                                                                   │
//	└─────────────────────────────────────────────────────────────────┘
//
// # OverloadProtector
//
// Monitors system resources and triggers degradation when thresholds exceeded:
//
//	protector := defense.NewOverloadProtector(
//	    defense.WithHighWatermark(0.8),   // 80% triggers overload
//	    defense.WithLowWatermark(0.7),    // 70% clears overload
//	    defense.WithCheckInterval(5*time.Second),
//	)
//
//	// Check if overloaded
//	if protector.IsOverloaded() {
//	    // Reject new connections
//	    // Accelerate LRU eviction
//	    // Skip non-essential plugins
//	}
//
// Monitored resources:
//	- Memory usage (heap in-use)
//	- File descriptor usage
//	- Goroutine count
//	- Session count vs max
//	- Worker pool queue depth
//
// Hysteresis prevents oscillation:
//	- High watermark: Enter overload state
//	- Low watermark: Exit overload state
//	- Gap between thresholds prevents rapid toggling
//
// Degradation matrix:
//	┌──────────────────────┬──────────────────────────────────────────┐
//	│  Condition           │  Action                                   │
//	├──────────────────────┼──────────────────────────────────────────┤
//	│  Sessions > 80% max  │  Reject new connections                   │
//	│  WorkerPool > 90%    │  Drop messages (PolicyDrop)              │
//	│  WorkerPool 100% 30s │  Close slow connections (PolicyClose)   │
//	│  FD > 95%            │  Pause accept + evict 10% oldest         │
//	│  Memory > 80%        │  Skip persistence + accelerate LRU      │
//	│  PubSub unavailable  │  Silently discard cluster events          │
//	│  Store unavailable   │  Skip persistence + circuit breaker      │
//	└──────────────────────┴──────────────────────────────────────────┘
//
// # BackpressureControl
//
// Manages write queue pressure to prevent memory exhaustion:
//
//	┌─────────────────────────────────────────────────────────────────┐
//	│  Write Queue Backpressure                                          │
//	├─────────────────────────────────────────────────────────────────┤
//	│                                                                   │
//	│  Queue depth:  [████████░░░░░░░░░░]  50% capacity                │
//	│                                                                   │
//	│  Warning threshold (80%):                                        │
//	│    - Log warning                                                 │
//	│    - shark_backpressure_warning_total++                          │
//	│    - Consider PolicyDrop for new messages                       │
//	│                                                                   │
//	│  Critical threshold (100%):                                      │
//	│    - ErrWriteQueueFull returned to sender                       │
//	│    - shark_write_queue_full_total++                               │
//	│    - Connection may be closed if persistent                      │
//	│                                                                   │
//	│  Broadcast rate limiting:                                         │
//	│    - BroadcastMaxSize: 64KB per message                          │
//	│    - BroadcastMinInterval: 100ms between broadcasts              │
//	│    - Prevents broadcast storms from overwhelming queues          │
//	│                                                                   │
//	└─────────────────────────────────────────────────────────────────┘
//
// # LogSampler
//
// Prevents log flooding from repeated events:
//
//	sampler := defense.NewLogSampler(time.Second)
//
//	// Instead of:
//	for i := 0; i < 10000; i++ {
//	    log.Printf("rate limit exceeded: %s", ip)
//	}
//
//	// Use:
//	for i := 0; i < 10000; i++ {
//	    sampler.Log("rate_limit", fmt.Sprintf("rate limit exceeded: %s", ip))
//	}
//	// Output: first message + "9999 additional same messages"
//
// Sampling strategy:
//	- First occurrence: full log output
//	- Subsequent within interval: increment counter
//	- End of interval: summary line "N additional same messages"
//	- New interval: reset counter, full output again
//
// This prevents:
//	- Log file explosion under attack
//	- I/O bottleneck from logging
//	- Loss of other log messages in noise
//
// # Integration Points
//
// OverloadProtector integrates with Gateway:
//
//	gw := api.NewGateway(
//	    api.WithOverloadConfig(defense.OverloadConfig{
//	        HighWatermark:   0.8,
//	        LowWatermark:    0.7,
//	        CheckInterval:   5 * time.Second,
//	    }),
//	)
//
// Backpressure integrates with TCPSession:
//
//	sess.Send(data)
//	// Internally checks writeQueue depth
//	// Returns ErrWriteQueueFull when queue is full
//
// LogSampler integrates with framework logging:
//
//	// Automatic sampling for high-frequency events
//	// Rate limit exceeded
//	// Connection refused
//	// Message dropped
//
// # Metrics
//
// Defense mechanisms emit Prometheus metrics:
//
//	shark_overloaded                              // Overload state (0/1)
//	shark_overload_checks_total                   // Overload checks
//	shark_resource_exhausted_total{resource}      // Resource exhaustion
//	shark_backpressure_warning_total{protocol}    // Queue warnings
//	shark_write_queue_full_total{protocol}        // Queue full events
//	shark_fd_usage_ratio                          // FD usage percentage
//	shark_memory_usage_bytes                      // Memory usage
//	shark_goroutine_count                         // Goroutine count
//
// # Alert Recommendations
//
// Prometheus alert rules:
//
//	# High connection count
//	- alert: SharkHighConnections
//	  expr: shark_connections_active > 80000
//	  for: 5m
//
//	# Overload detected
//	- alert: SharkOverloaded
//	  expr: shark_overloaded == 1
//	  for: 1m
//
//	# Write queue pressure
//	- alert: SharkBackpressure
//	  expr: shark_backpressure_warning_total > 100
//	  for: 2m
//
//	# FD exhaustion
//	- alert: SharkFDExhaustion
//	  expr: shark_fd_usage_ratio > 0.9
//	  for: 1m
//
//	# Memory pressure
//	- alert: SharkMemoryPressure
//	  expr: shark_memory_usage_bytes > 0.8 * process_resident_memory_bytes
//	  for: 2m
//
package defense