// Package plugin provides the plugin chain and built-in plugins for the framework.
//
// This package implements the plugin lifecycle management system, including
// PluginChain (ordered execution with panic protection) and built-in plugins
// for security, rate limiting, session management, and cross-node coordination.
//
// # Plugin Interface
//
//	type Plugin interface {
//	    Name() string
//	    Priority() int    // Lower = earlier execution
//	    OnAccept(sess RawSession) error
//	    OnMessage(sess RawSession, data []byte) ([]byte, error)
//	    OnClose(sess RawSession)
//	}
//
// # Lifecycle Hooks
//
//	┌──────────────────────────────────────────────────────────────────┐
//	│                    Plugin Execution Order                           │
//	├──────────────────────────────────────────────────────────────────┤
//	│                                                                    │
//	│  OnAccept (connection established):                               │
//	│    Blacklist(0) → RateLimit(10) → AutoBan(20) → Heartbeat(30)    │
//	│    → Cluster(40) → Persistence(50)                                │
//	│    → ErrBlock? Close session and stop                             │
//	│                                                                    │
//	│  OnMessage (data received):                                       │
//	│    Blacklist(0) → RateLimit(10) → Heartbeat(30) → Cluster(40)    │
//	│    → Persistence(50) → Handler                                    │
//	│    → ErrSkip? Stop plugins, continue to handler                  │
//	│    → ErrDrop? Silently discard message                            │
//	│    → Data transform? Pass modified data to next plugin           │
//	│                                                                    │
//	│  OnClose (session closing, reverse order):                        │
//	│    Persistence(50) → Cluster(40) → Heartbeat(30) → AutoBan(20)  │
//	│    → RateLimit(10) → Blacklist(0)                                 │
//	│    → Always executes all plugins (ignores errors)                 │
//	│                                                                    │
//	└──────────────────────────────────────────────────────────────────┘
//
// # PluginChain
//
// The chain manages plugin execution order and error handling:
//
//	chain := plugin.NewChain()
//	chain.Add(blacklistPlugin)
//	chain.Add(rateLimitPlugin)
//	chain.Add(heartbeatPlugin)
//	chain.Sort()  // Sort by Priority()
//
// # Control Flow Errors
//
// Plugins can return special errors to control chain execution:
//
//	┌──────────────────────────────────────────────────────────────┐
//	│  Error     │ OnAccept            │ OnMessage                │
//	├──────────────────────────────────────────────────────────────┤
//	│  ErrSkip   │ N/A                 │ Stop chain, pass to     │
//	│            │                     │ handler with orig data  │
//	│  ErrDrop   │ N/A                 │ Stop chain, discard msg │
//	│  ErrBlock  │ Close session,      │ N/A                     │
//	│            │ stop chain          │                          │
//	│  Other err │ Stop or continue    │ Stop or continue        │
//	│            │ (based on config)   │ (based on config)       │
//	└──────────────────────────────────────────────────────────────┘
//
// StopOnError configuration:
//   - true (default): Stop chain on any error
//   - false: Log error and continue to next plugin
//
// # Panic Protection
//
// Each plugin hook is wrapped with recover():
//
//	func (c *Chain) OnMessage(sess, data) ([]byte, error) {
//	    for _, p := range c.plugins {
//	        func() {
//	            defer func() {
//	                if r := recover(); r != nil {
//	                    log.Printf("plugin %s panic: %v", p.Name(), r)
//	                }
//	            }()
//	            out, err = p.OnMessage(sess, data)
//	        }()
//	    }
//	}
//
// This ensures:
//   - A panicking plugin cannot crash the server
//   - Subsequent plugins still execute
//   - Panic is logged for debugging
//   - shark_plugin_panics_total metric is incremented
//
// # Built-in Plugins
//
// ## BlacklistPlugin (Priority 0)
//
// IP/CIDR blocking with dual lookup:
//
//	// Exact IP match: O(1)
//	blacklist.Add("192.168.1.1", time.Hour)
//
//	// CIDR range match: O(n) on CIDR list
//	blacklist.Add("10.0.0.0/8", 0)  // permanent
//
//	// OnAccept: check IP → ErrBlock if found
//
// Features:
//   - Exact IP map: O(1) lookup
//   - CIDR list: linear scan (typically small)
//   - TTL-based expiration
//   - Background cleanup goroutine
//   - Cache double-check (distributed blacklist)
//
// ## RateLimitPlugin (Priority 10)
//
// Token bucket rate limiting with dual layers:
//
//	// Global rate: 1000 connections/sec
//	// Per-IP rate: 100 connections/sec
//	rl := plugin.NewRateLimitPlugin(1000, time.Second,
//	    plugin.WithPerIPRate(100, time.Second),
//	)
//
//	// OnAccept: connection rate limiting (ErrBlock)
//	// OnMessage: message rate limiting (ErrDrop)
//
// Token bucket algorithm:
//   - Tokens replenished based on elapsed time
//   - Atomic operations for thread safety
//   - Per-IP buckets stored in sync.Map
//   - Background cleanup (2-minute idle TTL)
//   - Consecutive rate limit triggers → AutoBanPlugin
//
// ## AutoBanPlugin (Priority 20)
//
// Automatic banning based on behavior thresholds:
//
//	ab := plugin.NewAutoBanPlugin(blacklist,
//	    plugin.WithRateLimitThreshold(10),   // 10 rate limits per minute
//	    plugin.WithErrorThreshold(5),         // 5 protocol errors per minute
//	    plugin.WithIdleThreshold(20),         // 20 idle connections per minute
//	    plugin.WithBanDuration(30*time.Minute),
//	)
//
// Trigger conditions:
//   - Rate limit exceeded N times → ban
//   - Protocol errors exceeded M times → ban
//   - Idle connections exceeded K times → ban
//
// Action: Add IP to BlacklistPlugin with configurable TTL
//
// ## HeartbeatPlugin (Priority 30)
//
// Time-wheel based idle timeout detection:
//
//	hb := plugin.NewHeartbeatPlugin(30*time.Second, 60*time.Second,
//	    func() types.SessionManager { return mgr },
//	)
//
//	// OnAccept: register session in time wheel
//	// OnMessage: reset session timer
//	// On timeout: Close() session
//
// Time wheel efficiency:
//   - Single goroutine for all sessions (vs. per-session timer)
//   - 100K connections → 1 goroutine (not 100K timers)
//   - Add/Remove/Reset: O(1)
//   - Precision: 1 second (slot interval)
//
// ## ClusterPlugin (Priority 40)
//
// Cross-node session awareness and message routing:
//
//	cp := plugin.NewClusterPlugin("node-1", pubsub, cache,
//	    plugin.WithHeartbeatTTL(30*time.Second),
//	)
//
// OnAccept: Publish session.joined + cache route
// OnClose: Publish session.left + delete route
// Cross-node routing: Cache lookup → PubSub forward
//
// ## PersistencePlugin (Priority 50)
//
// Async batch state persistence:
//
//	pp := plugin.NewPersistencePlugin(store,
//	    plugin.WithBatchSize(100),
//	    plugin.WithFlushInterval(500*time.Millisecond),
//	)
//
// OnAccept: Load session history from store
// OnMessage: Queue message for async write
// OnClose: Sync write final snapshot
//
// Features:
//   - Buffered channel (capacity 1024)
//   - Batch writer goroutine (100 records or 500ms)
//   - CircuitBreaker wrapping Store calls
//   - Non-blocking write path (drop on full)
//
// ## SlowQueryPlugin (Priority -10)
//
// Slow request detection and logging:
//
//	sq := plugin.NewSlowQueryPlugin(100*time.Millisecond)
//
//	// Logs any request exceeding threshold
//	// Metric: shark_slow_query_total
//
// # Custom Plugins
//
// Implement the Plugin interface:
//
//	type MyPlugin struct {
//	    plugin.BasePlugin  // Embed for default implementations
//	}
//
//	func (p *MyPlugin) Name() string { return "my-plugin" }
//	func (p *MyPlugin) Priority() int { return 100 }
//
//	func (p *MyPlugin) OnMessage(sess types.RawSession, data []byte) ([]byte, error) {
//	    // Transform data
//	    transformed := transform(data)
//	    return transformed, nil
//	}
//
// Register with server:
//
//	srv := api.NewTCPServer(handler,
//	    api.WithTCPPlugins(&MyPlugin{}),
//	)
//
// # Performance
//
// Plugin chain overhead:
//   - Empty chain: ~20ns (loop + nil check)
//   - 5-plugin chain: ~200ns (< 200ns/hop target)
//   - Panic recovery: ~50ns additional overhead
//
// The chain uses pre-sorted slice for O(1) plugin lookup.
// Sorting happens once at startup; hot path is direct iteration.
//
// # Metrics
//
// Plugin-specific metrics:
//
//	shark_plugin_duration_seconds{plugin}     // Execution time histogram
//	shark_plugin_panics_total{plugin}         // Panic count
//	shark_plugin_errors_total{plugin, hook}   // Error count
//	shark_autoban_total{ip, reason}           // Auto-ban count
//	shark_heartbeat_timeout_total             // Heartbeat timeout count
//	shark_ratelimit_rejected_total{ip}        // Rate limit rejections
//
package plugin
