// Package coap provides CoAP protocol implementation with CON retransmission and Block-wise transfer.
//
// CoAP (Constrained Application Protocol) is designed for resource-constrained IoT devices,
// providing reliable UDP-based communication with a RESTful interface.
//
// # Protocol Overview
//
//	┌─────────────────────────────────────────────────────────────────┐
//	│  CoAP vs HTTP/TCP vs MQTT                                        │
//	├─────────────────────────────────────────────────────────────────┤
//	│                                                                   │
//	│  CoAP (UDP)                                                      │
//	│    - Designed for IoT/M2M                                       │
//	│    - Small header (~4 bytes)                                     │
//	│    - CON/NON reliable/unreliable                                │
//	│    - Built-in discovery (/well-known/core)                      │
//	│    - Observe for subscriptions                                   │
//	│                                                                   │
//	│  HTTP/TCP                                                        │
//	│    - General purpose                                            │
//	│    - Larger overhead                                            │
//	│    - Connection-oriented                                        │
//	│                                                                   │
//	│  MQTT/TCP                                                        │
//	│    - Pub/Sub messaging                                          │
//	│    - Broker-based                                               │
//	│    - QoS levels (0, 1, 2)                                      │
//	│                                                                   │
//	└─────────────────────────────────────────────────────────────────┘
//
// # Message Format
//
//	┌─────────────────────────────────────────────────────────────────┐
//	│  CoAP Header (4 bytes minimum)                                  │
//	├─────────────────────────────────────────────────────────────────┤
//	│  Ver | T | TKL | Code          | Message ID                    │
//	│  2b  | 2b| 4b  | 8b           | 16b                          │
//	│                                                                   │
//	│  Then: Token (0-8 bytes based on TKL)                          │
//	│  Then: Options (TLV encoded)                                    │
//	│  Then: Payload marker (0xFF) + payload                           │
//	└─────────────────────────────────────────────────────────────────┘
//
// # Message Types
//
//	┌─────────────────────────────────────────────────────────────────┐
//	│  Type │ Full Name         │ Description                      │
//	├─────────────────────────────────────────────────────────────────┤
//	│  CON  │ Confirmable       │ Requires ACK (reliable)           │
//	│  NON  │ Non-confirmable   │ No ACK (unreliable)             │
//	│  ACK  │ Acknowledgement   │ Response to CON                  │
//	│  RST  │ Reset            │ Abort (invalid message)          │
//	└─────────────────────────────────────────────────────────────────┘
//
// # CON Retransmission
//
// CON messages require acknowledgment. If ACK not received, resend:
//
//	┌─────────────────────────────────────────────────────────────────┐
//	│  CON Retransmission Flow                                          │
//	├─────────────────────────────────────────────────────────────────┤
//	│                                                                   │
//	│  Client                          Server                         │
//	│     │                                │                           │
//	│     │──── CON (msgID=123) ──────────▶│                          │
//	│     │                                │                           │
//	│     │ (wait AckTimeout=2s)          │ Process request           │
//	│     │                                │                           │
//	│     │◀─────── ACK (msgID=123) ──────│                          │
//	│     │                                │                           │
//	│     │                                │                           │
//	│  If no ACK received:                                               │
//	│     │──── CON (retry 1) ────────────▶│                          │
//	│     │──── CON (retry 2) ────────────▶│                          │
//	│     │──── CON (retry 3) ────────────▶│                          │
//	│     │──── CON (retry 4) ────────────▶│  (max retransmits)      │
//	│     │                                │                           │
//	│     │ (give up)                      │                           │
//	│                                                                   │
//	└─────────────────────────────────────────────────────────────────┘
//
// Exponential backoff: timeout = AckTimeout × 2^attempt
// Default: AckTimeout=2s, MaxRetransmit=4, so max wait ~62s
//
// # Message ID Deduplication
//
// Message IDs enable duplicate detection:
//
//   - 16-bit ID (0-65535)
//   - Server tracks recent MessageIDs (LRU cache, 500 entries)
//   - Duplicate CON → resend previous ACK (don't reprocess)
//   - Duplicate NON → silently ignore
//
// # Code Values
//
// Request codes (Class 0):
//
//	0.00 Empty
//	0.01 GET
//	0.02 POST
//	0.03 PUT
//	0.04 DELETE
//
// Response codes (Class 2/4/5):
//
//	2.00 Created
//	2.01 Deleted
//	2.02 Valid
//	2.03 Changed
//	2.04 Content
//	4.00 Bad Request
//	4.04 Not Found
//	5.00 Internal Server Error
//
// # Options
//
// Options are TLV-encoded with delta/length encoding:
//
//	┌─────────────────────────────────────────────────────────────────┐
//	│  Option Format                                                   │
//	├─────────────────────────────────────────────────────────────────┤
//	│  Delta | Length | Value                                        │
//	│  4b    | 4b     | 0-8 bytes based on delta/length             │
//	│                                                                   │
//	│  Common options:                                                │
//	│    Uri-Path (11): /path/components                             │
//	│    Uri-Query (15): key=value                                   │
//	│    Content-Format (12): application/json                       │
//	│    Observe (6): subscription control                           │
//	│    Block2 (23): response block-wise transfer                   │
//	│    Block1 (27): request block-wise transfer                    │
//	└─────────────────────────────────────────────────────────────────┘
//
// # Block-wise Transfer
//
// Large payloads are transferred in blocks:
//
//	┌─────────────────────────────────────────────────────────────────┐
//	│  Block Transfer Example (1MB payload, 1KB blocks)                │
//	├─────────────────────────────────────────────────────────────────┤
//	│                                                                   │
//	│  Block2:0:  [0-1023 bytes]          (response)                 │
//	│  Block2:1:  [1024-2047 bytes]       (response)                 │
//	│  Block2:2:  [2048-3071 bytes]       (response)                 │
//	│  ...                                                           │
//	│  Block2:1023: [1048064-1049087]    (response)                  │
//	│                                                                   │
//	│  Request: GET /large-resource                                  │
//	│  Response: Content: 2.05 Content                               │
//	│            Block2: 0/1/1024 (more follows)                    │
//	│                                                                   │
//	│  Client iterates until Block2.num=0 and Block2.m=0            │
//	│                                                                   │
//	└─────────────────────────────────────────────────────────────────┘
//
// Block size exponents: 0=16B, 1=32B, ..., 6=1024B
//
// # Observe (Subscriptions)
//
// Observe allows server-to-client notifications:
//
//	GET /temperature
//	Observe: 0
//	→ Server sends current value
//	→ Server sends updates when value changes
//
//	Cancel observe:
//	GET /temperature
//	Observe: 1 (register)
//	...later...
//
// GET /temperature
// Observe: 1, Option: 6 (cancel)
//
//	→ Server stops sending updates
//
// # DTLS Support
//
// CoAP can use DTLS for security (like TLS for UDP):
//
//	coap.WithDTLS(dtlsConfig)
//
// DTLS is required for:
//   - IoT with security requirements
//   - Certificate-based authentication
//   - Encrypted communication
//
// # Use Cases
//
// CoAP is ideal for:
//   - IoT sensor networks (constrained devices)
//   - Smart home protocols
//   - Industrial automation
//   - Smart city infrastructure
//   - Energy management systems
//
// # Configuration Options
//
//	// Network
//	coap.WithAddr(host, port)              // Listen address
//
//	// Reliability
//	coap.WithAckTimeout(d)               // Wait for ACK (default 2s)
//	coap.WithMaxRetransmit(n)            // Max retries (default 4)
//
//	// Sessions
//	coap.WithSessionTTL(d)               // Session TTL (default 5m)
//	coap.WithMaxSessions(n)              // Max sessions
//	coap.WithMessageIDCacheSize(n)        // Deduplication cache (default 500)
//
//	// Security
//	coap.WithDTLS(cfg)                  // DTLS configuration
//
//	// Plugins
//	coap.WithPlugins(p...)              // Protocol-level plugins
//
// # Metrics
//
// CoAP protocol emits Prometheus metrics:
//
//	shark_coap_connections_total        // Sessions created
//	shark_coap_connections_active      // Current sessions
//	shark_coap_messages_total          // Messages received
//	shark_coap_messages_con/non/ack/rst  // By type
//	shark_coap_retransmit_total        // Retransmissions
//	shark_coap_block_total             // Block transfers
//	shark_coap_errors_total           // Errors
//
// # BufferPool Integration
//
// ACK responses use Micro-level BufferPool (≤512 bytes):
//
//   - ACK packets are small (header + token)
//   - Micro pool prevents fragmentation
//   - Reduces memory allocations
//
// # Thread Safety
//
// Concurrent handling:
//   - readLoop: single goroutine per server
//   - Retransmission: dedicated goroutine per session
//   - Message cache: protected by mutex
//
// All shared state (pendingACKs, msgCache) is protected by appropriate synchronization.
package coap
