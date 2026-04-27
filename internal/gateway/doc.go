// Package gateway provides multi-protocol orchestration with shared session management.
//
// The gateway is the top-level component that coordinates multiple protocol servers
// (TCP, UDP, HTTP, WebSocket, CoAP, QUIC, gRPC-Web) under a unified management layer
// with shared plugins and session tracking.
//
// # Architecture
//
//	┌─────────────────────────────────────────────────────────────────┐
//	│                         Gateway Architecture                        │
//	├─────────────────────────────────────────────────────────────────┤
//	│                                                                   │
//	│  ┌──────────────────────────────────────────────────────────────┐│
//	│  │                      Gateway                                 ││
//	│  │                                                              ││
//	│  │  sharedSessionManager ←──────────────────────────────────────││
//	│  │         │                                                  ││
//	│  │         ▼                                                  ││
//	│  │  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐       ││
//	│  │  │ TCP      │ │ UDP      │ │ HTTP     │ │ WebSocket│ ...  ││
//	│  │  │ Server   │ │ Server   │ │ Server   │ │ Server   │       ││
//	│  │  └──────────┘ └──────────┘ └──────────┘ └──────────┘       ││
//	│  │         │           │           │           │              ││
//	│  │         └───────────┴───────────┴───────────┘              ││
//	│  │                          │                                   ││
//	│  │                    Protocol Servers                         ││
//	│  │                                                              ││
//	│  │  globalPlugins (applied to all protocols)                   ││
//	│  │         │                                                  ││
//	│  │         ▼                                                  ││
//	│  │  ┌──────────────────────────────────────────────────────┐  ││
//	│  │  │  PluginChain (merged + sorted by Priority)          │  ││
//	│  │  └──────────────────────────────────────────────────────┘  ││
//	│  │                                                              ││
//	│  └──────────────────────────────────────────────────────────────┘│
//	│                                                                   │
//	└─────────────────────────────────────────────────────────────────┘
//
// # Server Registration
//
// Register protocol servers with the gateway:
//
//	gw := api.NewGateway()
//
//	gw.Register(api.NewTCPServer(handler))
//	gw.Register(api.NewUDPServer(handler))
//	gw.Register(api.NewWebSocketServer(handler))
//	gw.Register(api.NewHTTPServer())
//
// Multiple servers of different protocols supported.
// Same protocol cannot be registered twice (ErrDuplicateProtocol).
//
// # Shared Session Manager
//
// All protocol servers share the same SessionManager:
//
//	┌─────────────────────────────────────────────────────────────────┐
//	│  Shared SessionManager                                           │
//	├─────────────────────────────────────────────────────────────────┤
//	│                                                                   │
//	│  Benefits:                                                        │
//	│    - Cross-protocol session lookup                              │
//	│    - Unified broadcast to all connections                        │
//	│    - Single LRU eviction for all protocols                       │
//	│    - Reduced memory overhead                                     │
//	│                                                                   │
//	│  Example:                                                        │
//	│    tcpSess, ok := manager.Get(sessionID)  // Find any protocol  │
//	│    manager.Broadcast(data)                // Send to all        │
//	│                                                                   │
//	└─────────────────────────────────────────────────────────────────┘
//
// # Global Plugins
//
// Global plugins apply to ALL registered protocols:
//
//	gw := api.NewGateway(
//	    api.WithGlobalPlugins(
//	        api.NewBlacklistPlugin("10.0.0.0/8"),
//	        api.NewRateLimitPlugin(10000, time.Second),
//	        api.NewHeartbeatPlugin(30*time.Second, 60*time.Second, getManager),
//	    ),
//	)
//
// Plugin ordering:
//	1. Global plugins merged with protocol plugins
//	2. Sorted by Priority() ascending
//	3. Same Priority: global before protocol
//	4. Same name: later registration wins
//
// # Startup Sequence
//
// Gateway.Start():
//
//	1. Validate: at least one server registered
//	2. ServeMetrics: start Prometheus HTTP server (if enabled)
//	3. StartAll: concurrently start all servers
//	4. WaitGroup: block until error or shutdown signal
//
// Concurrent startup with errgroup:
//	- All servers start in parallel
//	- Fast startup (not sequential)
//	- Any failure triggers rollback of successful starts
//
// # Graceful Shutdown (6-Phase)
//
// Gateway.Stop() implements 6-phase shutdown:
//
//	┌─────────────────────────────────────────────────────────────────┐
//	│  Shutdown Sequence                                                │
//	├─────────────────────────────────────────────────────────────────┤
//	│                                                                   │
//	│  Phase 1: Stop Accepting                                         │
//	│    - Close all listeners                                         │
//	│    - No new connections                                          │
//	│                                                                   │
//	│  Phase 2: Signal Graceful Shutdown                                │
//	│    - Publish ErrGracefulShutdown to plugin chain                 │
//	│    - Handlers can react to shutdown                              │
//	│                                                                   │
//	│  Phase 3: Drain Active Work                                       │
//	│    - WorkerPool.Stop() - wait for task queue drain               │
//	│    - All writeQueues drain (with DrainTimeout)                   │
//	│    - Pending messages are delivered                              │
//	│                                                                   │
//	│  Phase 4: Plugin Cleanup                                         │
//	│    - PluginChain.OnClose() for each session (reverse order)      │
//	│    - Persistence saves final state                               │
//	│    - Cluster unregisters from other nodes                        │
//	│                                                                   │
//	│  Phase 5: Session Cleanup                                        │
//	│    - SessionManager.Close() - close all sessions                 │
//	│    - Close all connections                                       │
//	│    - Flush logger buffer                                         │
//	│                                                                   │
//	│  Phase 6: Force Close                                           │
//	│    - If ctx deadline exceeded, force close                      │
//	│    - No further waiting                                          │
//	│                                                                   │
//	└─────────────────────────────────────────────────────────────────┘
//
// Shutdown timeout (default 15s) applies to total shutdown duration.
//
// # Run Method
//
// Run() combines Start() + graceful shutdown:
//
//	gw.Run()
//
// Internally:
//	1. Start() all servers
//	2. Wait for signal (SIGINT, SIGTERM)
//	3. Stop() with graceful shutdown
//	4. Return after shutdown complete
//
// This is the recommended entry point for production deployments.
//
// # Health Endpoints
//
// Metrics server provides health checks:
//
//	GET /healthz
//	Response:
//	  {
//	    "status": "healthy",
//	    "uptime": "2h30m",
//	    "protocols": {
//	      "tcp": {"active": 100, "total": 150},
//	      "websocket": {"active": 50, "total": 60}
//	    },
//	    "system": {
//	      "memory": "256MB",
//	      "goroutines": 150
//	    }
//	  }
//
// Status values:
//	- healthy: All systems nominal
//	- overloaded: System under stress, some features degraded
//	- degraded: Core functionality impaired
//
//	GET /readyz
//	Response: {"status": "ready"}
//
//	GET /metrics
//	Response: Prometheus text format
//
// # Configuration Options
//
//	// Shutdown
//	WithShutdownTimeout(d)          // Total shutdown duration (default 15s)
//
//	// Metrics
//	WithMetricsAddr(addr)           // Metrics server address (default :9090)
//	WithMetricsEnabled(bool)        // Enable metrics server (default true)
//
//	// Plugins
//	WithGlobalPlugins(p...)         // Plugins for all protocols
//
// # Metrics
//
// Gateway emits Prometheus metrics:
//
//	shark_gateway_servers_total           // Registered servers
//	shark_gateway_sessions_total          // All sessions (all protocols)
//	shark_gateway_startup_duration_seconds // Startup time
//	shark_gateway_shutdown_duration_seconds // Shutdown time
//
// Plus all protocol-specific metrics aggregated.
//
// # Cross-Protocol Communication
//
// With shared SessionManager, protocols can interact:
//
//	// From TCP handler, send to WebSocket client
//	tcpSess, _ := manager.Get(targetSessionID)
//	if tcpSess != nil && tcpSess.Protocol() == api.WebSocket {
//	    tcpSess.Send(data)  // Works regardless of protocol
//	}
//
//	// Broadcast to all connected clients (any protocol)
//	manager.Broadcast([]byte("maintenance warning"))
//
// This enables protocol bridging and unified message handling.
//
// # Thread Safety
//
// Gateway is safe for concurrent access:
//	- Server registration happens before Start()
//	- All servers share same SessionManager (thread-safe)
//	- Shutdown is single-use operation
//
// # Error Handling
//
// Errors from individual servers are aggregated:
//
//	err := gw.Start()
//	// Returns error if ANY server failed to start
//	// Partial successes are rolled back
//
// This prevents partial startup where some servers are running
// while others failed.
//
package gateway