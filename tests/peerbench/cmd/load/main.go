// Command load is the load generator for the peer benchmark.
//
// One binary drives every arm, so the client cost is identical across the
// comparison; that is the whole point of a same-host peer benchmark. It speaks
// the fixed-size byte echo the three servers implement: each connection keeps
// `pipeline` messages in flight, then reads exactly as many bytes back before
// starting the next round.
//
// A round is therefore a pipelined round trip of pipeline messages, which is
// both the throughput unit and the latency sample. Output is one tab-separated
// line so the runner can paste it straight into a table.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:19001", "server address")
	conns := flag.Int("conns", 8, "concurrent connections")
	size := flag.Int("size", 256, "message size in bytes")
	pipeline := flag.Int("pipeline", 16, "messages in flight per connection")
	duration := flag.Duration("duration", 5*time.Second, "measurement window after warmup")
	warmup := flag.Duration("warmup", 1*time.Second, "traffic discarded before measuring")
	label := flag.String("label", "server", "row label")
	flag.Parse()

	var (
		total atomic.Int64
		wg    sync.WaitGroup
		mu    sync.Mutex
		lats  []time.Duration
	)

	start := time.Now()
	warmUntil := start.Add(*warmup)
	end := warmUntil.Add(*duration)

	for i := 0; i < *conns; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			conn, err := net.Dial("tcp", *addr)
			if err != nil {
				fmt.Fprintf(os.Stderr, "dial: %v\n", err)
				return
			}
			defer conn.Close()

			msg := bytes.Repeat([]byte("x"), *size)
			// The first byte identifies the connection: if a server ever mixed
			// streams up, the check below fails instead of producing a number.
			msg[0] = byte(id)
			readBuf := make([]byte, *size**pipeline)

			var local []time.Duration
			for time.Now().Before(end) {
				roundStart := time.Now()
				for p := 0; p < *pipeline; p++ {
					if _, err := conn.Write(msg); err != nil {
						return
					}
				}
				if _, err := io.ReadFull(conn, readBuf); err != nil {
					fmt.Fprintf(os.Stderr, "conn %d read: %v\n", id, err)
					return
				}
				if readBuf[0] != byte(id) {
					fmt.Fprintf(os.Stderr, "conn %d: echoed byte %d, want %d (streams crossed)\n",
						id, readBuf[0], byte(id))
					return
				}
				if roundStart.After(warmUntil) {
					total.Add(int64(*pipeline))
					local = append(local, time.Since(roundStart))
				}
			}
			mu.Lock()
			lats = append(lats, local...)
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	secs := end.Sub(warmUntil).Seconds()
	sort.Slice(lats, func(i, j int) bool { return lats[i] < lats[j] })
	pick := func(q float64) time.Duration {
		if len(lats) == 0 {
			return 0
		}
		return lats[int(q*float64(len(lats)-1))]
	}
	msgs := total.Load()
	if os.Getenv("LOAD_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "samples=%d min=%v p10=%v p25=%v p50=%v p75=%v p90=%v p99=%v max=%v\n",
			len(lats), pick(0), pick(0.10), pick(0.25), pick(0.50), pick(0.75), pick(0.90), pick(0.99), pick(1))
		zero := 0
		for _, d := range lats {
			if d == 0 {
				zero++
			}
		}
		fmt.Fprintf(os.Stderr, "exact-zero samples: %d (%.1f%%)\n", zero, 100*float64(zero)/float64(len(lats)))
	}
	fmt.Printf("%s\t%d\t%d\t%d\t%d\t%.2f\t%.0f\t%.1f\t%d\t%d\n",
		*label, *conns, *size, *pipeline, msgs, secs,
		float64(msgs)/secs,
		float64(msgs)*float64(*size)/secs/1e6,
		pick(0.50).Microseconds(), pick(0.99).Microseconds())
}
