// Package api provides the unified entry point and public API for the shark-socket framework.
//
// This package re-exports all framework types, interfaces, and factory functions,
// providing a single import point for users of the framework.
//
// # Quick Start
//
// ## TCP Echo Server
//
//	package main
//
//	import (
//	    "log"
//	    "github.com/X1aSheng/shark-socket/api"
//	)
//
//	func main() {
//	    handler := func(sess api.RawSession, msg api.RawMessage) error {
//	        return sess.Send(msg.Payload) // Echo back
//	    }
//
//	    srv := api.NewTCPServer(handler, api.WithTCPAddr("0.0.0.0", 18000))
//	    log.Fatal(srv.Start())
//	}
//
// ## Multi-Protocol Gateway
//
//	package main
//
//	import (
//	    "time"
//	    "github.com/X1aSheng/shark-socket/api"
//	)
//
//	func main() {
//	    handler := func(sess api.RawSession, msg api.RawMessage) error {
//	        return sess.Send(msg.Payload)
//	    }
//
//	    // Create servers
//	    tcpSrv := api.NewTCPServer(handler, api.WithTCPAddr("0.0.0.0", 18000))
//	    wsSrv := api.NewWebSocketServer(handler,
//	        api.WithWebSocketAddr("0.0.0.0", 18600),
//	        api.WithWebSocketPath("/ws"),
//	    )
//
//	    // Create gateway
//	    gw := api.NewGateway(
//	        api.WithShutdownTimeout(15*time.Second),
//	        api.WithMetricsAddr(":9091"),
//	        api.WithMetricsEnabled(true),
//	    )
//
//	    // Register servers
//	    gw.Register(tcpSrv)
//	    gw.Register(wsSrv)
//
//	    // Run until signal
//	    gw.Run()
//	}
//
// ## TCP Server with Plugins
//
//	package main
//
//	import (
//	    "time"
//	    "github.com/X1aSheng/shark-socket/api"
//	)
//
//	func main() {
//	    handler := func(sess api.RawSession, msg api.RawMessage) error {
//	        return sess.Send(msg.Payload)
//	    }
//
//	    srv := api.NewTCPServer(handler,
//	        api.WithTCPAddr("0.0.0.0", 18000),
//	        api.WithTCPPlugins(
//	            api.NewBlacklistPlugin("10.0.0.0/8", "192.168.0.0/16"),
//	            api.NewRateLimitPlugin(1000, time.Second),
//	        ),
//	    )
//
//	    log.Fatal(srv.Start())
//	}
//
// # Type System
//
// The framework uses generics for compile-time type safety:
//
//	┌─────────────────────────────────────────────────────────────────┐
//	│  Generic Type Hierarchy                                           │
//	├─────────────────────────────────────────────────────────────────┤
//	│                                                                   │
//	│  Session[M MessageConstraint]                                     │
//	│      │                                                          │
//	│      ├─── RawSession = Session[[]byte] (most common)           │
//	│      │                                                          │
//	│      └─── Session[CustomType] (application-specific)           │
//	│                                                                   │
//	│  Message[T any]                                                 │
//	│      │                                                          │
//	│      ├─── RawMessage = Message[[]byte] (most common)           │
//	│      │                                                          │
//	│      └─── Message[CustomType] (application-specific)           │
//	│                                                                   │
//	│  MessageHandler[T any]                                          │
//	│      │                                                          │
//	│      └─── RawHandler = MessageHandler[[]byte]                  │
//	│                                                                   │
//	└─────────────────────────────────────────────────────────────────┘
//
// # Send vs SendTyped
//
// The framework provides two sending methods:
//
//	// Send - common path, direct bytes
//	err := sess.Send([]byte("hello"))
//
//	// SendTyped - type-safe path with encoding
//	err := sess.SendTyped(myMessage)  // Compile-time checked
//
// SendTyped internals:
//  1. encode(msg) → []byte using registered encoder
//  2. Send([]byte) → unified write queue
//  3. When T=[]byte, encode is identity, zero overhead
//
// # Protocol Support
//
//	┌─────────────────────────────────────────────────────────────────┐
//	│  Protocol Support Matrix                                          │
//	├─────────────────────────────────────────────────────────────────┤
//	│  Protocol    │ Factory Function      │ Default Port │ TLS Support │
//	├─────────────────────────────────────────────────────────────────┤
//	│  TCP        │ NewTCPServer         │ 18000       │ Yes         │
//	│  UDP        │ NewUDPServer         │ 18200       │ N/A         │
//	│  HTTP       │ NewHTTPServer        │ 18400       │ Yes         │
//	│  WebSocket  │ NewWebSocketServer   │ 18600       │ Yes         │
//	│  CoAP       │ NewCoAPServer        │ 18800       │ DTLS        │
//	│  QUIC       │ NewQUICServer        │ 18900       │ Yes         │
//	│  gRPC-Web   │ NewGRPCWebGateway    │ 18650       │ Yes         │
//	└─────────────────────────────────────────────────────────────────┘
//
// # Built-in Plugins
//
// Plugins intercept session lifecycle events:
//
//	┌─────────────────────────────────────────────────────────────────┐
//	│  Plugin Priority and Purpose                                      │
//	├─────────────────────────────────────────────────────────────────┤
//	│  Priority │ Plugin               │ Purpose                       │
//	├─────────────────────────────────────────────────────────────────┤
//	│  0        │ BlacklistPlugin     │ IP/CIDR blocking              │
//	│  10       │ RateLimitPlugin     │ Token bucket rate limiting     │
//	│  20       │ AutoBanPlugin       │ Automatic IP banning           │
//	│  30       │ HeartbeatPlugin     │ Idle timeout detection        │
//	│  40       │ ClusterPlugin       │ Cross-node routing            │
//	│  50       │ PersistencePlugin  │ Async state persistence       │
//	│  -10      │ SlowQueryPlugin    │ Slow request logging          │
//	└─────────────────────────────────────────────────────────────────┘
//
// Plugin control flow via errors:
//
//	ErrSkip  = Skip remaining plugins, continue to handler
//	ErrDrop  = Silently discard the message
//	ErrBlock = Reject the connection
//
// Check with: IsPluginSkip(err), IsPluginDrop(err), IsPluginBlock(err)
//
// # Error Handling
//
// The framework defines comprehensive errors in the errs package.
//
// Classification functions for proper handling:
//
//	IsRetryable(err)          // WriteQueueFull, CircuitOpen
//	IsFatal(err)              // SessionClosed, ServerClosed
//	IsRecoverable(err)        // CircuitOpen, Infrastructure
//	IsSecurityRejection(err)  // Blacklisted, AutoBanned, RateLimited
//	IsPluginControl(err)      // ErrSkip, ErrDrop, ErrBlock
//
// Example:
//
//	err := handler.Process(msg)
//	switch {
//	case api.IsPluginSkip(err):
//	    return nil  // Message handled by plugin
//	case api.IsSecurityRejection(err):
//	    log.Warn("security rejection:", err)
//	default:
//	    log.Error("unexpected error:", err)
//	}
//
// # Session State Machine
//
//	Connecting ──[accept]──────────────────────────────┐
//	   │                                              │
//	   │ (error)                                     ▼
//	   └──────────────────────────→ Closed ◄─────────┤
//	                                              │
//	   Active ◄───────────[Active!]───────┬─────────┘
//	      │                               │
//	      │ (Close())                     │
//	      ▼                               │
//	   Closing ──[drain]─────────────────┘
//	      │
//	      │ (drain complete)
//	      ▼
//	   Closed
//
// State transitions use CAS (Compare-And-Swap) for thread safety.
//
// # Infrastructure Components
//
// Create infrastructure components for plugin dependencies:
//
//	// Memory cache with TTL
//	cache := api.NewMemoryCache(
//	    cache.WithMaxSize(100000),
//	    cache.WithCleanInterval(5*time.Minute),
//	)
//
//	// Memory store for persistence
//	store := api.NewMemoryStore()
//
//	// In-process pub/sub
//	pubsub := api.NewChannelPubSub()
//
//	// Circuit breaker
//	cb := api.NewCircuitBreaker(5, 30*time.Second)
//
// # Metrics
//
// Enable Prometheus metrics at /metrics endpoint:
//
//	gw := api.NewGateway(
//	    api.WithMetricsAddr(":9091"),
//	    api.WithMetricsEnabled(true),
//	)
//
// Built-in metrics include:
//   - Connections by protocol
//   - Messages by protocol and type
//   - Session LRU evictions
//   - Buffer pool hit rates (6 levels)
//   - Plugin execution times
//   - Worker pool queue depth
//
// # Logging
//
// Set custom logger:
//
//	logger := myLogger{} // implements Logger interface
//	api.SetLogger(logger)
//
// Default uses Go 1.21+ slog with JSON output.
//
// # Configuration Patterns
//
// Functional Options for all configurations:
//
//	// TCP with full options
//	api.NewTCPServer(handler,
//	    api.WithTCPAddr("0.0.0.0", 18000),
//	    api.WithTCPTLS(tlsConfig),
//	    api.WithTCPMaxSessions(100000),
//	    api.WithTCPMaxMessageSize(1024*1024),
//	    api.WithTCPConnRateLimit(1000, 60),
//	    api.WithTCPPlugins(blacklistPlugin),
//	)
//
//	// WebSocket with CORS
//	api.NewWebSocketServer(handler,
//	    api.WithWebSocketAddr("0.0.0.0", 18600),
//	    api.WithWebSocketPath("/ws"),
//	    api.WithWebSocketAllowedOrigins("https://example.com"),
//	    api.WithWebSocketPingInterval(30*time.Second),
//	)
//
// # Constants
//
// Protocol constants:
//
//	api.TCP       = 1
//	api.TLS       = 2
//	api.UDP       = 3
//	api.HTTP      = 4
//	api.WebSocket = 5
//	api.CoAP      = 6
//	api.QUIC      = 7
//	api.Custom    = 99
//
// Message type constants:
//
//	api.Text    = 1
//	api.Binary  = 2
//	api.Ping    = 3
//	api.Pong    = 4
//	api.Close   = 5
//
// Session state constants:
//
//	api.Connecting = 0
//	api.Active     = 1
//	api.Closing    = 2
//	api.Closed     = 3
//
// # See Also
//
// For detailed architecture documentation, see:
//   - docs/shark-socket ARCHITECTURE.md
//   - docs/IMPROVEMENT_PLAN.md
//   - docs/TECH_PREPARATION.md
package api
