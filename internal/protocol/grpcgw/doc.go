// Package grpcgw provides a gRPC-Web gateway server.
//
// This package implements a gateway that accepts gRPC-Web requests and
// can forward them to various protocol handlers (WebSocket, direct, etc.)
//
// gRPC-Web is a browser-compatible protocol that wraps gRPC calls in HTTP/2.
// It uses binary protobuf encoding for efficient data transfer.
//
// Usage:
//
//	import "github.com/X1aSheng/shark-socket/internal/protocol/grpcgw"
//
//	// Create gRPC-Web gateway
//	gw := grpcgw.NewServer(
//		grpcgw.WithAddr("127.0.0.1", 18650),
//		grpcgw.WithTargetProtocol(grpcgw.WebSocket), // forward to WebSocket
//	)
//
//	if err := gw.Start(); err != nil {
//		log.Fatal(err)
//	}
//	defer gw.Stop(context.Background())
//
// Note: This gateway requires a gRPC-Web compatible client.
// Browsers use native fetch or the grpc-web library.
package grpcgw