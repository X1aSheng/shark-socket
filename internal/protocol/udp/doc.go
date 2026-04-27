// Package udp provides UDP protocol implementation.
//
// UDP differs from TCP in being connectionless. This implementation uses
// pseudo-sessions based on remote addresses to provide session semantics
// for stateless request-response patterns.
//
// # Architecture
//
// UDP pseudo-sessions:
//   - Single shared connection (UDPConn)
//   - Sessions identified by remote address (IP:port)
//   - TTL-based cleanup for stale sessions
//
// Data flow:
//
//	readLoop:
//	  1. ReadFromUDP() receives datagram
//	  2. Lookup or create UDPSession by remote address
//	  3. PluginChain.OnMessage(sess, data)
//	  4. Handler(sess, msg)
//	  5. sess.Send() → WriteToUDP (direct, no queue)
//
// # Session Management
//
// Pseudo-sessions use sync.Map for concurrent access:
//
//	Server.sessions: map[string]*UDPSession
//	               key = addr.String()
//
// Session lifecycle:
//	- Created on first datagram from remote address
//	- Updated on activity (lastActive timestamp)
//	- Cleaned by sweepLoop after TTL inactivity
//
// # TTL Cleanup
//
// Background sweepLoop manages session lifecycle:
//
//	- Runs every sweepInterval (default 60s)
//	- Checks all sessions for inactivity
//	- Closes and unregisters sessions exceeding TTL
//
// # No Write Queue
//
// Unlike TCP, UDP Send() is direct:
//
//	sess.Send(data) → conn.WriteToUDP(data, addr)
//
// UDP is datagram-oriented with no connection state:
//	- No need for write queue
//	- Writes are best-effort
//	- No ordering guarantees
//
// # Use Cases
//
// UDP is suitable for:
//	- DNS-style query/response
//	- Gaming (low latency, loss-tolerant)
//	- IoT devices
//	- Streaming media
//
// # Limitations
//
//   - No reliability (packets may be lost)
//	- No flow control
//	- Datagram size typically limited to ~1500 bytes
//	- NAT traversal challenges
//
// Consider CoAP for reliable UDP transport at application layer.
//
package udp