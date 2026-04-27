// Package quic provides QUIC protocol implementation for shark-socket.
//
// # Overview
//
// QUIC (Quick UDP Internet Connections) is a multiplexed stream transport over UDP.
// It combines the best of TCP and UDP:
//
//	┌─────────────────────────────────────────────────────────────────┐
//	│                        QUIC Features                              │
//	├─────────────────────────────────────────────────────────────────┤
//	│  Connection    │ TLS 1.3 handshake (1-RTT or 0-RTT with 0-RTT)    │
//	│  Establishment │                                           │
//	├─────────────────────────────────────────────────────────────────┤
//	│  Multiplexing │ Multiple streams per connection, no HOLB      │
//	├─────────────────────────────────────────────────────────────────┤
//	│  Connection   │ Seamless migration when IP/port changes        │
//	│  Migration    │ (mobile network switch)                       │
//	├─────────────────────────────────────────────────────────────────┤
//	│  Congestion   │ Pluggable algorithms (Reno, Cubic, BBR)      │
//	│  Control      │                                           │
//	├─────────────────────────────────────────────────────────────────┤
//	│  Low Latency  │ No head-of-line blocking between streams      │
//	└─────────────────────────────────────────────────────────────────┘
//
// # Connection Model
//
// QUIC connections are identified by Connection IDs (CID), not 4-tuple.
// This enables connection migration across IP/port changes.
//
//	Connection Lifecycle:
//
//	1. Client → Server: Initial packet (with crypto handshake)
//	2. Server → Client: Handshake response + Retry (if needed)
//	3. Client → Server: handshake complete
//	4. Streams: Bidirectional data exchange
//	5. Connection: Close/Idle timeout
//
// # Stream Types
//
// | Type | ID Pattern | Description | Flow Control |
// |------|------------|-------------|--------------|
// | Unidirectional | 0x00-0x3F | Send-only streams | Sender controls |
// | Bidirectional | 0x40+ | Full-duplex | Both ends control |
//
// Stream ordering is preserved within a stream but independent between streams.
// Loss of one stream does not block others (unlike TCP).
//
// # Frame Types
//
// QUIC uses frames within packets:
//
//	┌────────────────────────────────────────────────────────────┐
//	│ Required Frames                                               │
//	├────────────────────────────────────────────────────────────┤
//	│ CRYPTO     │ Crypto handshake data (TLS)                    │
//	│ PING/PONG  │ Keep-alive, idle timeout prevention            │
//	├────────────────────────────────────────────────────────────┤
//	│ STREAM     │ Application data (multiple per packet)         │
//	│ ACK        │ Received packet acknowledgments               │
//	│ MAX_DATA   │ Flow control: max bytes per connection         │
//	│ MAX_STREAM_DATA │ Flow control: max bytes per stream        │
//	├────────────────────────────────────────────────────────────┤
//	│ HANDSHAKE_DONE │ Server confirms handshake completion       │
//	│ NEW_TOKEN │ Server provides migration token               │
//	└────────────────────────────────────────────────────────────┘
//
// # Performance Characteristics
//
// | Metric | QUIC | TCP+HTTPS | Improvement |
// |--------|------|-----------|-------------|
// | Connection setup | 1-RTT | 2-RTT | 50% reduction |
// | 0-RTT data | Yes | No | Latency elimination |
// | Stream latency | Isolated | Shared | P99 improvement |
// | Loss sensitivity | Per-stream | Per-connection | 30% better |
//
// # 0-RTT Resumption
//
// QUIC supports 0-RTT data on resumption:
//
//  1. First connection: Full handshake (1-RTT)
//  2. Session tickets: Client caches keys
//  3. Resumption: Client sends data with Initial packet
//  4. Server: Decrypts and responds immediately
//
// Caveat: 0-RTT data may be replayed (use idempotent operations).
//
// # Connection Migration
//
// When network changes (WiFi ↔ Cellular):
//
//  1. Client detects path change
//  2. Client sends packets on new path with same CID
//  3. Server updates path and responds
//  4. Connection continues seamlessly
//
// # Integration with shark-socket
//
// QUIC server integrates with the framework:
//
//	import (
//	    "github.com/X1aSheng/shark-socket/api"
//	    "github.com/X1aSheng/shark-socket/internal/protocol/quic"
//	)
//
//	// Basic server
//	srv := api.NewQUICServer(handler,
//	    api.WithQUICAddr("0.0.0.0", 18900),
//	    api.WithQUICTLS(tlsConfig),
//	)
//
//	// With plugins
//	srv := api.NewQUICServer(handler,
//	    api.WithQUICAddr("0.0.0.0", 18900),
//	    api.WithQUICPlugins(
//	        api.NewBlacklistPlugin(),
//	        api.NewRateLimitPlugin(1000, time.Second),
//	    ),
//	)
//
// # Configuration Options
//
//	quic.WithAddr(host, port)           // Listen address
//	quic.WithTLS(cfg)                   // TLS config (required)
//	quic.WithMaxIdleTimeout(d)          // Connection idle timeout
//	quic.WithMaxBidirectionalStreams(n) // Max streams per direction
//	quic.WithMaxMessageSize(n)          // Application message limit
//	quic.WithKeepAlive(enabled)         // Send keep-alive packets
//	quic.WithInitialRTT(d)             // Initial RTT estimate
//	quic.WithCongestionControl(algo)   // CUBIC/RENO/BBR
//
// # Session Management
//
// QUIC sessions map to framework Session interface:
//
//	- Each QUIC connection = one framework RawSession
//  - Each QUIC stream within connection = application message boundary
//  - Stream data reassembled into Message for plugin chain
//
// Session ID uses QUIC connection ID (CID) for cross-node routing.
//
// # Use Cases
//
// Best for applications requiring:
//
//   - Real-time communication (gaming, trading)
//   - Mobile clients with frequent network switches
//   - High-throughput bidirectional streaming
//   - Privacy (connection ID hides client location)
//
// Less suitable for:
//
//   - Firewall-restricted environments (UDP may be blocked)
//   - High-security environments requiring deep packet inspection
//   - Legacy network infrastructure
//
// # Security Properties
//
// | Property | Implementation |
// |---------|---------------|
// | Encryption | TLS 1.3 mandatory (all packets encrypted) |
// | Key Update | 0-RTT keys can be updated mid-connection |
// | Connection ID | Hides client IP/dentity from observers |
// | Retry | Forward secrecy for replay attack protection |
//
// # Comparison with TCP/TLS
//
//	┌────────────────────────────────────────────────────────────┐
//	│                    TCP/TLS vs QUIC                          │
//	├──────────────┬───────────────────┬─────────────────────────┤
//	│ Aspect       │ TCP + TLS 1.3      │ QUIC                    │
//	├──────────────┼───────────────────┼─────────────────────────┤
//	│ Handshake    │ 2 RTT             │ 1 RTT (0-RTT possible)   │
//	│ Framing      │ TCP byte stream    │ Message-oriented frames  │
//	│ Streams      │ One per connection │ Multiple, independent   │
//	│ HOLB         │ Between streams    │ Isolated per stream     │
//	│ Head-of-Line │ TLS record layer   │ Stream frames          │
//	│ Migration    │ Connection break   │ Seamless (CID)          │
//	│ Packet loss  │ All streams        │ Affected stream only    │
//	└──────────────┴───────────────────┴─────────────────────────┘
//
// # Metrics and Observability
//
// Available metrics:
//
//	shark_quic_connections_total          // Total connections opened
//	shark_quic_connections_active        // Current connections
//	shark_quic_streams_total            // Streams created
//	shark_quic_bytes_sent/received       // Data transfer
//	shark_quic_rtt_seconds              // Round-trip time
//	shark_quic_congestion_events        // Congestion signals
//	shark_quic_migration_events         // Connection migrations
//
// # Limitations and Considerations
//
//   - Requires UDP support (some firewalls block UDP)
//   - Kernel bypass recommended for high performance (io_uring)
//   - 0-RTT data replay protection is application's responsibility
//   - TLS certificates must support key exchange (ECDHE required)
//
package quic
