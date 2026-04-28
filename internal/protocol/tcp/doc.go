// Package tcp provides TCP/TLS protocol implementation for shark-socket.
//
// This package implements the core TCP transport layer with support for TLS,
// multiple framing strategies, and worker pool-based message processing.
//
// # Architecture
//
//	┌─────────────────────────────────────────────────────────────────┐
//	│                        TCP Server Architecture                       │
//	├─────────────────────────────────────────────────────────────────┤
//	│                                                                  │
//	│  ┌────────────┐                                                  │
//	│  │  Listener  │                                                  │
//	│  │  (net.TCP)│                                                  │
//	│  └─────┬──────┘                                                  │
//	│        │                                                         │
//	│        ▼                                                         │
//	│  ┌────────────┐    Per-Connection                                │
//	│  │ acceptLoop │ ──────────────────────────────────────────────► │
//	│  └─────┬──────┘                                                  │
//	│        │         ┌────────────┐                                  │
//	│        │         │   handleConn                                   │
//	│        │         └──────┬─────┘                                  │
//	│        │                │                                        │
//	│        │        ┌────────┴────────┐                              │
//	│        │        ▼                 ▼                              │
//	│        │  ┌──────────┐      ┌──────────┐                         │
//	│        │  │ readLoop │      │ writeLoop│                         │
//	│        │  │ (goroutine)│    │ (goroutine)│                        │
//	│        │  └────┬─────┘      └─────┬─────┘                        │
//	│        │       │                  │                              │
//	│        │       ▼                  │                              │
//	│        │  ┌──────────┐            │                              │
//	│        │  │ PluginChain│           │                              │
//	│        │  │ OnAccept   │           │                              │
//	│        │  └────┬─────┘            │                              │
//	│        │       │                  │                              │
//	│        │       ▼                  │                              │
//	│        │  ┌──────────┐      ┌──────────┐                         │
//	│        │  │ WorkerPool│      │writeQueue│                         │
//	│        │  │ (shared)  │      │(channel) │                         │
//	│        │  └──────────┘      └──────────┘                         │
//	│                                                                  │
//	└─────────────────────────────────────────────────────────────────┘
//
// # Server Lifecycle
//
//	Server.Start()
//	  1. net.Listen → TCP listener
//	  2. pool.Start() → Start worker goroutines
//	  3. acceptLoop() goroutine begins accepting
//	  4. Returns nil (async accept)
//
//	Server.Stop(ctx)
//	  1. listener.Close() → Stop accepting
//	  2. pool.Stop() → Stop workers gracefully
//	  3. manager.Close() → Close all sessions
//	  4. wg.Wait(ctx) → Wait for goroutines
//
// # Framing
//
// The Framer interface defines how bytes are framed:
//
//	type Framer interface {
//	    ReadFrame(r io.Reader) ([]byte, error)
//	    WriteFrame(w io.Writer, data []byte) error
//	}
//
// Built-in framers:
//
//	┌─────────────────────────────────────────────────────────────────┐
//	│  Framer              │ Format              │ Use Case             │
//	├─────────────────────────────────────────────────────────────────┤
//	│  LengthPrefixFramer  │ [4-byte len][data]  │ Default, binary     │
//	│  LineFramer          │ [data]\n            │ Text protocols     │
//	│  FixedSizeFramer     │ [fixed len bytes]  │ Protocol constants  │
//	│  RawFramer           │ [raw bytes]         │ Custom handling    │
//	└─────────────────────────────────────────────────────────────────┘
//
// LengthPrefixFramer format:
//
//	┌─────────────────────────────────────────────┐
//	│  [0x00] [0x00] [0x00] [0x10] [payload...]  │
//	│  ──────────────────────────────────────     │
//	│  Big-endian 32-bit length (max 1MB)         │
//	└─────────────────────────────────────────────┘
//
// # Worker Pool
//
// WorkerPool distributes message processing across goroutines:
//
//	┌──────────────────────────────────────────────────────────────┐
//	│  WorkerPool                                                   │
//	├──────────────────────────────────────────────────────────────┤
//	│                                                               │
//	│  taskQueue: chan task (bounded, size = WorkerCount × 128)   │
//	│                                                               │
//	│  ┌────────┐ ┌────────┐ ┌────────┐      ┌────────┐         │
//	│  │Worker 1│ │Worker 2│ │Worker 3│ ...  │Worker N│         │
//	│  │(routine)│ │(routine)│ │(routine)│      │(routine)│         │
//	│  └────┬───┘ └────┬───┘ └────┬───┘      └────┬───┘         │
//	│       │          │          │                │              │
//	│       └──────────┴──────────┴────────────────┘              │
//	│       Task queue is processed in order (FIFO)                │
//	│                                                               │
//	└──────────────────────────────────────────────────────────────┘
//
// # Queue Full Policies
//
// When worker queue is full:
//
//	PolicyBlock:   Block until space available (guaranteed delivery)
//	PolicyDrop:    Drop message + log + metric (recommended default)
//	PolicySpawnTemp: Spawn temporary worker (handles burst)
//	PolicyClose:   Close connection (extreme overload protection)
//
// # Write Queue
//
// Each session has a write queue channel:
//
//	TCPSession {
//	    writeQueue: chan []byte  // Default size 128
//	}
//
// Send() behavior:
//
//	err := sess.Send(data)
//	if err != nil {
//	    if errors.Is(err, errs.ErrWriteQueueFull) {
//	        // Queue full, message not sent
//	    }
//	}
//
// writeLoop goroutine drains queue:
//
//	for data := range sess.writeQueue {
//	    conn.Write(data)  // Blocking write
//	}
//
// # Close Sequence
//
// Session close is a multi-step process:
//
//  1. CAS: Active → Closing
//  2. Close writeQueue channel (signals writeLoop)
//  3. Drain writeQueue (configurable DrainTimeout)
//  4. CAS: Closing → Closed
//  5. Cancel context (stops readLoop, handlers)
//  6. conn.Close()
//  7. PluginChain.OnClose() (reverse order)
//
// # TLS Support
//
// TLS is configured via tls.Config:
//
//	tlsConfig := &tls.Config{
//	    Certificates: []tls.Certificate{cert},
//	    MinVersion: tls.VersionTLS13,  // Required for Go 1.26
//	}
//
//	srv := tcp.NewServer(handler,
//	    tcp.WithTLS(tlsConfig),
//	)
//
// Certificate hot reload via SIGHUP:
//
//	srv := tcp.NewServer(handler,
//	    tcp.WithTLS(cfg),
//	    tcp.WithTLSCertFile("cert.pem", "key.pem"),  // Auto-reload on SIGHUP
//	)
//
// # Connection Rate Limiting
//
// Per-IP connection rate limiting:
//
//	srv := tcp.NewServer(handler,
//	    tcp.WithConnRateLimit(100, 60),  // 100 connections per minute per IP
//	)
//
// Uses sliding window algorithm for accurate rate measurement.
//
// # TCP Client
//
// TCP client with auto-reconnect:
//
//	import "github.com/X1aSheng/shark-socket/internal/protocol/tcp"
//
//	client := tcp.NewClient("localhost:18000",
//	    tcpclient.WithTLS(tlsConfig),
//	    tcpclient.WithReconnect(true),
//	    tcpclient.WithBackoff(time.Second, 30*time.Second),
//	)
//
//	if err := client.Connect(); err != nil {
//	    log.Fatal(err)
//	}
//	defer client.Close()
//
//	err = client.Send([]byte("hello"))
//	response, err := client.Receive()
//
// # Performance Targets
//
//	┌──────────────────────────────────────────────────────────────┐
//	│  Metric                │ Target        │ Achieved         │
//	├──────────────────────────────────────────────────────────────┤
//	│  Throughput            │ >= 100K msg/s │ With benchmarks │
//	│  P99 Latency           │ < 1ms        │ With benchmarks │
//	│  Connection capacity   │ >= 100K      │ With tests     │
//	│  Critical path alloc   │ 0            │ BufferPool     │
//	└──────────────────────────────────────────────────────────────┘
//
// # Metrics
//
// TCP protocol emits Prometheus metrics:
//
//	shark_tcp_connections_total
//	shark_tcp_connections_active
//	shark_tcp_connection_errors_total
//	shark_tcp_messages_total
//	shark_tcp_message_bytes
//	shark_tcp_message_duration_seconds
//	shark_tcp_worker_queue_depth
//	shark_tcp_write_queue_full_total
//	shark_tcp_rejected_connections_total{reason}
//
// # Configuration Options
//
//	// Network
//	WithAddr(host, port)              // Listen address
//	WithTLS(cfg)                     // TLS configuration
//
//	// Limits
//	WithMaxSessions(n)                // Max concurrent sessions
//	WithMaxMessageSize(n)            // Max message size (default 1MB)
//
//	// Timeouts
//	WithReadTimeout(d)               // Read deadline
//	WithWriteTimeout(d)              // Write deadline
//	WithIdleTimeout(d)               // Idle connection timeout
//	WithDrainTimeout(d)              // Close drain timeout
//	WithShutdownTimeout(d)           // Server shutdown timeout
//
//	// Framing
//	WithFramer(f)                    // Custom framer
//
//	// Worker pool
//	WithWorkerCount(n)               // Worker goroutines
//	WithTaskQueueSize(n)             // Task queue capacity
//	WithFullPolicy(p)                // Queue full policy
//	WithWriteQueueSize(n)            // Per-session write queue
//	WithWriteFullPolicy(p)           // Write queue full policy
//
//	// Plugins
//	WithPlugins(p...)                // Protocol-level plugins
//
//	// TLS
//	WithTLSCertFile(cert, key)      // Certificate files
//
//	// Rate limiting
//	WithConnRateLimit(rate, window) // Connection rate limit
package tcp
