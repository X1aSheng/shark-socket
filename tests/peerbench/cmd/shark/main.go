// Command shark is the shark-socket arm of the peer benchmark: a fixed-size
// byte echo.
//
// Byte echo rather than a framed protocol on purpose. Framing is where the three
// frameworks differ most (ring-buffer peek vs. length-prefix parse vs. callback
// with a bytes.Buffer), and putting three different parsers in the comparison
// would measure the parsers instead of the I/O path. Every arm therefore reads
// whatever arrived and writes it straight back.
//
// Only the public api package is imported: the benchmark doubles as a check
// that an embedder can express this server without reaching into internal/.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/X1aSheng/shark-socket/api"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:19001", "listen address")
	writeBuf := flag.Int("write-buffer", 0, "per-connection write buffer in bytes")
	flag.Parse()

	server := api.NewTCPServer(
		api.WithTCPAddr(*addr),
		api.WithTCPFramer(api.RawFramer{MaxFrameBytes: 4096}),
		api.WithTCPWriteBufferBytes(*writeBuf),
		api.WithTCPHandler(func(sess api.Session, msg api.Message) error {
			return sess.Send(msg.Payload)
		}),
	)
	gateway := api.NewGateway()
	if err := gateway.Register(server); err != nil {
		log.Fatalf("register: %v", err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		log.Fatalf("start: %v", err)
	}
	log.Printf("shark-socket echo on %s (write_buffer=%d)", *addr, *writeBuf)
	select {}
}
