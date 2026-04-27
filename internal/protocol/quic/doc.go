// Package quic provides QUIC protocol implementation.
//
// QUIC (Quick UDP Internet Connections) is a multiplexed stream transport over UDP.
// It combines the best of TCP and UDP:
// - Connection establishment with TLS 1.3 (1-RTT or 0-RTT)
// - Multiplexed streams without head-of-line blocking
// - Connection migration when IP changes
// - Built-in congestion control
//
// Example usage:
//
//	server := quic.NewServer(handler,
//	    quic.WithAddr("0.0.0.0", 18600),
//	    quic.WithTLS(tlsConfig),
//	)
//	if err := server.Start(); err != nil {
//	    log.Fatal(err)
//	}
//	defer server.Stop(context.Background())
package quic
