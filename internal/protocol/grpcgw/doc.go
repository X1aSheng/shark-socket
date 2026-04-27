// Package grpcgw provides a gRPC-Web gateway server for shark-socket.
//
// # Overview
//
// gRPC-Web is a browser-compatible protocol that wraps gRPC calls in HTTP/2.
// This gateway translates gRPC-Web requests and forwards them to internal
// protocol handlers, enabling browser-based clients to access shark-socket services.
//
//	┌──────────────┐     gRPC-Web      ┌──────────────┐    Protocol    ┌──────────────┐
//	│   Browser    │ ──────────────── │   gRPC-Web   │ ───────────── │   Handler    │
//	│   (fetch)    │ HTTP/2 + Binary  │   Gateway    │               │ (WS/TCP/...) │
//	└──────────────┘                   └──────────────┘               └──────────────┘
//
// # Architecture
//
// The gateway supports two forwarding modes:
//
// | Mode | Protocol | Use Case |
// |------|----------|----------|
// | WebSocket | WSServer | Bidirectional streaming, firewall-friendly |
// | Direct | Internal | Low-latency, same-process routing |
//
// # Protocol Specification
//
// gRPC-Web framing (as per Envoy proxy specification):
//
//	┌─────────────────┬──────────────────────┬─────────────────┐
//	│ Flags (1 byte)   │ Length (3 bytes)     │ Payload         │
//	└─────────────────┴──────────────────────┴─────────────────┘
//
// Flags:
//   - 0x00: No compression
//   - 0x01: Compressed (gzip)
//   - 0x80: Compressed (using grpc-encoding header)
//
// # Message Types
//
// | Type | Value | Description |
// |------|-------|-------------|
// | REQUEST | 0x00 | Full request message |
// | | | | START_STREAM | 0x01 | Begin streaming (if supported) |
// | | | | END_STREAM | 0x02 | End of stream |
//
// # Usage Example
//
//	import (
//	    "github.com/X1aSheng/shark-socket/api"
//	    "github.com/X1aSheng/shark-socket/internal/protocol/grpcgw"
//	)
//
//	// Create gRPC-Web gateway forwarding to WebSocket
//	gw := api.NewGRPCWebGateway(
//	    grpcgw.WithAddr("127.0.0.1", 18650),
//	    grpcgw.WithTargetProtocol(grpcgw.WebSocket),
//	    grpcgw.WithMaxFrameSize(1024*1024),
//	)
//
//	// Or forward to internal handler directly
//	gw := api.NewGRPCWebGateway(
//	    grpcgw.WithAddr("127.0.0.1", 18650),
//	    grpcgw.WithTargetProtocol(grpcgw.Direct),
//	    grpcgw.WithHandler(myHandler),
//	)
//
// # Protobuf Serialization
//
// gRPC-Web uses binary protobuf encoding by default:
//
//	// Message format
//	message GrpcWebMessage {
//	    uint32 flags = 1;
//	    uint32 length = 2;
//	    bytes payload = 3;
//	}
//
// The payload contains serialized protobuf messages per the service definition.
// For unary calls, the payload is a single compressed/ framed message.
// For streaming, multiple payloads are sent sequentially.
//
// # HTTP Headers
//
// Required headers for gRPC-Web requests:
//
//	Content-Type: application/grpc-web[+protobuf]
//	X-Grpc-Accept-Encoding: identity, gzip, deflate
//
// Response headers include:
//
//	Content-Type: application/grpc-web[+protobuf]
//	Grpc-Encoding: identity
//
// # Binary Frame Format
//
// In binary mode (application/grpc-web):
//
//	Frame = Flags (1) + Length (3) + Data
//	Flags: 0x80 = compressed
//	Length: big-endian 24-bit integer
//	Data: protobuf encoded message(s)
//
// Text mode (application/grpc-web-text) base64 encodes the binary frames.
//
// # Configuration Options
//
//	grpcgw.WithAddr(host, port)         // Listen address
//	grpcgw.WithTargetProtocol(proto)    // Forwarding mode
//	grpcgw.WithMaxFrameSize(size)        // Max message size
//	grpcgw.WithCompression(algo)        // Compression algorithm
//	grpcgw.WithTimeout(duration)        // Request timeout
//
// # Browser Compatibility
//
// Supported browsers:
//   - Chrome/Edge (Chromium 79+)
//   - Firefox 65+
//   - Safari 14+
//
// Not supported: IE 11, legacy mobile browsers
//
// # Limitations
//
// - No bidirectional streaming in all browsers (use WebSocket fallback)
// - Per-message overhead of gRPC framing
// - Requires gRPC-Web compatible client library
//
// # Performance
//
// | Metric | Value | Notes |
// |--------|-------|-------|
// | Framing overhead | ~4 bytes | Per message |
// | Serialization | Protobuf | Binary efficient |
// | Latency | P99 < 5ms | Internal forwarding |
//
// # Security Considerations
//
// - TLS required in production (enforce via configuration)
// - Validate Content-Type header
// - Limit max frame size to prevent memory exhaustion
// - Consider rate limiting per IP
//
// # Middleware Integration
//
// The gateway integrates with shark-socket's plugin system:
//
//	pluginChain.OnAccept(httpSess)  // Not applicable for gRPC-Web
//	pluginChain.OnMessage(...)     // Message-level processing
//	pluginChain.OnClose(...)        // Stream end cleanup
//
// Note: gRPC-Web connections may span multiple HTTP requests (streaming).
// Plugin hooks follow HTTP session semantics (not TCP connection semantics).
//
package grpcgw