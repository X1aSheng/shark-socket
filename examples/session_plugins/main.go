package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/X1aSheng/shark-socket/api"
	"github.com/X1aSheng/shark-socket/internal/plugin"
	"github.com/X1aSheng/shark-socket/internal/protocol/tcp"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// --- Plugin 1: Blacklist ---
	// Block specific IPs from connecting.
	blacklist := api.NewBlacklistPlugin(
		"192.168.1.100", // block a single IP
		"10.0.0.0/8",    // block an entire CIDR range
	)
	defer blacklist.Close()

	// You can also add IPs dynamically with a TTL:
	// blacklist.Add("192.168.1.200", 5 * time.Minute)

	// --- Plugin 2: Rate Limit ---
	// 100 connections/second global burst, 10 per-IP burst.
	// Per-IP message rate: 1000 messages/second with 100 burst.
	rateLimit := api.NewRateLimitPlugin(
		100, // rate: connections per second
		200, // burst: max concurrent connections
		plugin.WithRateLimitMessageRate(1000, 1000), // message rate per IP
	)
	defer rateLimit.Close()

	// --- Handler ---
	handler := func(sess api.RawSession, msg api.RawMessage) error {
		log.Printf("[session %d] %s: %s", sess.ID(), sess.RemoteAddr(), string(msg.Payload))
		// Echo back
		return sess.Send(msg.Payload)
	}

	// --- TCP Server with Plugins ---
	srv := api.NewTCPServer(handler,
		tcp.WithAddr("0.0.0.0", 18000),
		tcp.WithPlugins(blacklist, rateLimit),
		tcp.WithMaxSessions(10000),
	)

	if err := srv.Start(); err != nil {
		log.Fatalf("Failed to start TCP server: %v", err)
	}

	log.Println("=== TCP Server with Plugins ===")
	log.Println("Listening on :18000")
	log.Println("")
	log.Println("Active plugins:")
	log.Println("  1. BlacklistPlugin - blocks 192.168.1.100 and 10.0.0.0/8")
	log.Println("  2. RateLimitPlugin  - 100 conn/s global, 10/s per-IP burst")
	log.Println("")
	log.Println("Test with:")
	log.Println("  nc localhost 18000")
	log.Println("")
	log.Println("Plugin chain order (by priority):")
	log.Println("  blacklist (priority 0) -> ratelimit (priority 10)")
	log.Println("")
	log.Println("Press Ctrl+C to stop.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
}
