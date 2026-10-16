// Package main provides a rate-limited load client.
//
// Why a rate-limited client exists: the flood-style client used for
// saturation runs unthrottled, so on a single host it becomes the bottleneck
// and steals CPU from the server under test. That makes the measurement
// meaningless. This client paces sends with a ticker so the observed
// throughput reflects the server's ceiling rather than the client's greed.
package main

import (
	"flag"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	host := flag.String("host", "127.0.0.1:28000", "server address host:port")
	conns := flag.Int("conns", 1, "number of concurrent connections")
	rate := flag.Int("rate", 2000, "target messages per second across all connections")
	dur := flag.Duration("duration", 8*time.Second, "test duration")
	flag.Parse()

	var sent, recv, errs atomic.Int64
	payload := make([]byte, 256)

	perConnRate := *rate / *conns
	if perConnRate < 1 {
		perConnRate = 1
	}
	// Batch the ticker: a ticker whose period is a few microseconds cannot
	// keep up on Windows and silently caps the achieved rate. Send `batch`
	// messages per tick so that rate/conns/batch stays above the ticker's
	// practical floor (~1ms).
	batch := perConnRate / 20
	if batch < 1 {
		batch = 1
	}
	effective := perConnRate / batch
	if effective < 1 {
		effective = 1
	}
	interval := time.Second / time.Duration(effective)
	deadline := time.Now().Add(*dur)

	// Echo replies are counted on a shared atomic; the read loop must outlive
	// the send loop, so it is joined separately instead of racing it.
	var readers sync.WaitGroup
	var writers sync.WaitGroup
	// readStop bounds the reader loops: a ReadDeadline timeout is their normal
	// wake-up, so without a deadline-based exit condition they never finish.
	readStop := time.Now().Add(*dur + 500*time.Millisecond)

	for i := 0; i < *conns; i++ {
		conn, err := net.Dial("tcp", *host)
		if err != nil {
			errs.Add(1)
			continue
		}
		readers.Add(1)
		go func(c net.Conn) {
			defer readers.Done()
			defer c.Close()
			buf := make([]byte, 8192)
			for time.Now().Before(readStop) {
				_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
				n, err := c.Read(buf)
				recv.Add(int64(n))
				if err != nil {
					// A timeout is the normal wake-up: the reader polls until
					// readStop passes. Any other error ends the loop.
					if ne, ok := err.(net.Error); ok && ne.Timeout() {
						continue
					}
					return
				}
			}
		}(conn)

		writers.Add(1)
		go func(c net.Conn) {
			defer writers.Done()
			tk := time.NewTicker(interval)
			defer tk.Stop()
			for range tk.C {
				if time.Now().After(deadline) {
					return
				}
				for range batch {
					if _, err := c.Write(payload); err != nil {
						errs.Add(1)
						return
					}
					sent.Add(1)
				}
			}
		}(conn)
	}

	writers.Wait()
	// Give in-flight echoes a moment to arrive before tearing readers down.
	time.Sleep(300 * time.Millisecond)
	readers.Wait()

	fmt.Printf("sent=%-10d recv=%-10d errs=%-4d target=%d/s send_rate=%.0f/s\n",
		sent.Load(), recv.Load(), errs.Load(), *rate,
		float64(sent.Load())/dur.Seconds())
}