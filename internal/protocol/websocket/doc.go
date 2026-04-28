// Package websocket provides WebSocket protocol implementation.
//
// WebSocket enables bidirectional, event-driven communication over persistent
// connections, ideal for real-time applications like chat, live updates, and gaming.
//
// # Architecture
//
//	┌─────────────────────────────────────────────────────────────────┐
//	│                 WebSocket Server Architecture                     │
//	├─────────────────────────────────────────────────────────────────┤
//	│                                                                  │
//	│  HTTP Server                                                    │
//	│       │                                                         │
//	│       │ Upgrade                                                │
//	│       ▼                                                         │
//	│  ┌─────────────────────────────────────────────────────────┐   │
//	│  │                   WSServer                               │   │
//	│  ├─────────────────────────────────────────────────────────┤   │
//	│  │                                                         │   │
//	│  │  ┌──────────┐      ┌──────────┐      ┌──────────┐     │   │
//	│  │  │readLoop │      │writeLoop │      │ pingLoop │     │   │
//	│  │  │(routine)│      │(routine) │      │ (routine)│     │   │
//	│  │  └────┬─────┘      └─────┬─────┘      └────┬─────┘     │   │
//	│  │       │                  │                  │           │   │
//	│  │       ▼                  │                  │           │   │
//	│  │  ┌──────────┐            │                  │           │   │
//	│  │  │ Plugin   │            │                  │           │   │
//	│  │  │ Chain    │            │                  │           │   │
//	│  │  └────┬─────┘            │                  │           │   │
//	│  │       │                  │                  │           │   │
//	│  │       ▼                  │                  │           │   │
//	│  │  ┌──────────┐      ┌──────────┐             │           │   │
//	│  │  │ Handler  │      │writeQueue│             │           │   │
//	│  │  └──────────┘      └──────────┘             │           │   │
//	│  │                                                         │   │
//	│  └─────────────────────────────────────────────────────────┘   │
//	│                                                                  │
//	└─────────────────────────────────────────────────────────────────┘
//
// # Connection Lifecycle
//
//  1. HTTP request with Upgrade header
//  2. Validate Origin (if configured)
//  3. gorilla/websocket upgrade
//  4. Create WSSession
//  5. Register with SessionManager
//  6. OnAccept() through plugin chain
//  7. Start goroutines: readLoop, writeLoop, pingLoop
//  8. On connection close: drain queue, OnClose(), unregister
//
// # Framing
//
// WebSocket uses compact framing for efficiency:
//
//	┌─────────────────────────────────────────────────────────────────┐
//	│  Frame Format (RFC 6455)                                         │
//	├─────────────────────────────────────────────────────────────────┤
//	│  Byte 0: FIN + opcode                                           │
//	│  Byte 1: MASK + payload length                                  │
//	│  Bytes 2-*: Extended payload length (if needed)                │
//	│  Bytes *: Mask key (if MASK=1)                                  │
//	│  Bytes *: Payload data                                          │
//	└─────────────────────────────────────────────────────────────────┘
//
// Opcodes:
//
//	0x0: Continuation
//	0x1: Text frame
//	0x2: Binary frame
//	0x8: Close
//	0x9: Ping
//	0xA: Pong
//
// # Ping/Pong Keep-Alive
//
// WebSocket uses Ping/Pong frames for connection health:
//
//	┌─────────────────────────────────────────────────────────────────┐
//	│  Ping/Pong Flow                                                  │
//	├─────────────────────────────────────────────────────────────────┤
//	│                                                                   │
//	│  pingLoop (server-side):                                        │
//	│    1. Every PingInterval (default 30s)                           │
//	│    2. Send Ping frame with application data                     │
//	│    3. Set read deadline (PingInterval + PongTimeout)             │
//	│    4. Wait for Pong response                                    │
//	│                                                                   │
//	│  PongHandler (auto-triggered):                                   │
//	│    1. gorilla/websocket auto-responds to Ping                   │
//	│    2. We set PongHandler to TouchActive + update deadline        │
//	│                                                                   │
//	│  Timeout: No Pong within deadline → close connection            │
//	│                                                                   │
//	└─────────────────────────────────────────────────────────────────┘
//
// # Message Handling
//
//	readLoop:
//	  1. ReadMessage() from WebSocket connection
//	  2. Handle control frames (Close, Ping, Pong)
//	  3. Validate message type (Text or Binary)
//	  4. TouchActive() update
//	  5. WorkerPool.Submit() for async handling
//
// Control frames are handled synchronously, data frames asynchronously.
//
// # Write Mutex
//
// gorilla/websocket requires serialized writes:
//
//	WSSession {
//	    writeMu sync.Mutex  // Serialize write operations
//	}
//
//	Send(data []byte) error {
//	    writeMu.Lock()
//	    defer writeMu.Unlock()
//	    return conn.WriteMessage(BinaryMessage, data)
//	}
//
// Without mutex, concurrent writes could interleave frame data.
//
// # Origin Checking
//
// Web browsers enforce same-origin policy. Validate Origin header:
//
//	srv := api.NewWebSocketServer(handler,
//	    ws.WithAllowedOrigins("https://example.com"),
//	)
//
// AllowedOrigins should include:
//   - Your production domain
//   - localhost for development
//   - Specific subdomains as needed
//
// Empty AllowedOrigins = reject all cross-origin connections.
//
// # Access Logging
//
// HTTP/WebSocket access logging integration:
//
//	import "github.com/X1aSheng/shark-socket/internal/infra/logger"
//
//	srv := api.NewWebSocketServer(handler,
//	    api.WithWebSocketAccessLogger(myAccessLogger),
//	)
//
// AccessLog fields:
//   - Timestamp, Method (WS upgrade), Path
//   - Status (101 for successful upgrade)
//   - Duration, BytesIn, BytesOut
//   - RemoteAddr, UserAgent, RequestID
//
// # Close Frame Handling
//
// Proper WebSocket close sequence:
//
//  1. Server sends Close frame (code + reason)
//  2. Client acknowledges with Close frame
//  3. Connection closed
//
// shark-socket implements graceful close:
//   - Session close triggers Close frame send
//   - writeLoop drains queue
//   - Connection closed after drain
//
// # Comparison with TCP
//
//	┌────────────────────┬─────────────────────────────────────────────────┐
//	│  Aspect            │  WebSocket vs TCP                            │
//	├────────────────────┼─────────────────────────────────────────────────┤
//	│  Protocol          │  HTTP Upgrade → persistent   vs  raw TCP      │
//	│  Browser support  │  Native (all browsers)    vs  requires proxy  │
//	│  Framing          │  Message-oriented          vs  byte stream     │
//	│  Ping/Pong        │  Built-in keep-alive       vs  custom        │
//	│  Compression      │  Per-frame (extensions)    vs  none          │
//	│  Proxy compat     │  Works through HTTP proxy   vs  needs raw TCP  │
//	│  Firewall         │  Typically allowed         vs  often blocked  │
//	└────────────────────┴─────────────────────────────────────────────────┘
//
// # Use Cases
//
// WebSocket excels for:
//   - Real-time chat applications
//   - Live dashboards and updates
//   - Multiplayer gaming
//   - Collaborative editing
//   - Streaming data feeds
//   - IoT device control
//
// # Limitations
//
//   - Upgrade overhead (initial HTTP handshake)
//   - Connection persistence (more resources than HTTP)
//   - Load balancer challenges (sticky sessions needed)
//   - Proxy timeout issues (keep-alive required)
//
// # Configuration Options
//
//	// Network
//	ws.WithAddr(host, port)                // Listen address
//	ws.WithPath(path)                      // WebSocket path (default "/")
//
//	// Security
//	ws.WithAllowedOrigins(origins...)      // Origin whitelist
//	ws.WithTLS(cfg)                        // TLS configuration
//
//	// Timeouts
//	ws.WithPingInterval(d)                // Ping interval (default 30s)
//	ws.WithPongTimeout(d)                 // Pong deadline (default 10s)
//	ws.WithReadTimeout(d)                 // Read deadline
//	ws.WithWriteTimeout(d)                // Write deadline
//	ws.WithHandshakeTimeout(d)            // Upgrade timeout (default 5s)
//
//	// Limits
//	ws.WithMaxMessageSize(n)              // Max message size (default 1MB)
//	ws.WithReadBufferSize(n)              // Read buffer (default 4KB)
//	ws.WithWriteBufferSize(n)             // Write buffer (default 4KB)
//
//	// Logging
//	ws.WithAccessLogger(logger)           // Access logger
//
//	// Plugins
//	ws.WithPlugins(p...)                  // Protocol-level plugins
//
// # Metrics
//
// WebSocket protocol emits Prometheus metrics:
//
//	shark_ws_connections_total            // Total upgrades
//	shark_ws_connections_active          // Current connections
//	shark_ws_messages_total              // Messages received
//	shark_ws_message_bytes               // Data transferred
//	shark_ws_ping_timeout_total          // Ping/Pong timeouts
//	shark_ws_errors_total                // Errors
package websocket
