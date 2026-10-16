package tcp

import (
	"context"
	"encoding/binary"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
	"github.com/X1aSheng/shark-socket/internal/runtime"
	"github.com/X1aSheng/shark-socket/internal/transport/shared"
)

// writeFrame encodes one length-prefixed frame, which is what
// LengthPrefixFramer expects: a 4-byte big-endian length then the payload.
// Encoding a bare payload gets the connection reset instead.
func writeFrame(conn net.Conn, payload []byte) error {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := conn.Write(hdr[:]); err != nil {
		return err
	}
	_, err := conn.Write(payload)
	return err
}

// TestDefaultServerUsesFixedPool asserts the default stays the fixed pool.
// Elasticity is opt-in, so a server built with no pool option must come up on
// WorkerPool and expose no elastic handle.
func TestDefaultServerUsesFixedPool(t *testing.T) {
	server := NewServer(WithAddr("127.0.0.1:0"), WithHandler(func(core.Session, core.Message) error { return nil }))
	if server.opts.ElasticPool != nil {
		t.Fatal("ElasticPool set by default: elasticity must be opt-in")
	}

	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if server.pool == nil {
		t.Fatal("pool is nil after Start")
	}
	if server.elastic != nil {
		t.Fatal("elastic handle set for a fixed pool")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = gateway.Stop(ctx)
}

// TestElasticPoolServerStartsAndServes is the end-to-end check: a server
// configured with the elastic pool must actually dispatch traffic through it,
// not merely construct without error. It would pass even if scaling never
// triggered, so it is deliberately a separate test from the worker-count
// assertions below.
func TestElasticPoolServerStartsAndServes(t *testing.T) {
	var handled atomic.Int64
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(func(sess core.Session, msg core.Message) error {
			handled.Add(1)
			_ = sess.Send(msg.Payload)
			return nil
		}),
		WithElasticPool(2, 4),
		WithWorkers(0, 512, PolicyDrop),
	)

	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if server.elastic == nil {
		t.Fatal("elastic handle nil: WithElasticPool did not take effect")
	}
	minW, maxW := server.elastic.WorkerBounds()
	if minW != 2 || maxW != 4 {
		t.Fatalf("worker bounds = (%d, %d), want (2, 4)", minW, maxW)
	}
	if got := server.elastic.WorkerCount(); got != 2 {
		t.Fatalf("worker count at start = %d, want minWorker 2", got)
	}

	conn, err := dialWithLinger(context.Background(), "tcp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := writeFrame(conn, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	hdr := make([]byte, 4)
	if _, err := readFull(conn, hdr); err != nil {
		t.Fatalf("read reply header: %v", err)
	}
	if got := binary.BigEndian.Uint32(hdr); got != 5 {
		t.Fatalf("reply length = %d, want 5", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = gateway.Stop(ctx)
	if handled.Load() == 0 {
		t.Fatal("handler never ran: traffic did not reach the elastic pool")
	}
}

// readFull reads exactly len(buf) bytes.
func readFull(conn net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := conn.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// TestElasticPoolScalesUnderLoad is the behaviour the option exists for: a
// backlog must grow the worker count above the floor.
//
// A parking handler is what makes the queue actually back up. With a handler
// that returns immediately, workers drain as fast as the client submits and
// occupancy never crosses the growth threshold, so the count would stay at
// minWorker and the test would prove nothing.
func TestElasticPoolScalesUnderLoad(t *testing.T) {
	release := make(chan struct{})
	var handled atomic.Int64
	server := NewServer(
		WithAddr("127.0.0.1:0"),
		WithHandler(func(core.Session, core.Message) error {
			handled.Add(1)
			<-release
			return nil
		}),
		WithElasticPool(1, 4),
		WithScaleRatio(0.5, 0.1),
		WithWorkers(0, 4096, PolicyDrop),
	)

	gateway := runtime.NewGateway()
	if err := gateway.Register(server); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		close(release)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = gateway.Stop(ctx)
	}()

	conn, err := dialWithLinger(context.Background(), "tcp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Far more frames than scaleCheckInterval (1024) submits, written without
	// reading replies so nothing throttles the producer.
	deadline := time.Now().Add(5 * time.Second)
	frame := []byte("x")
	for i := 0; i < 6000 && time.Now().Before(deadline); i++ {
		if err := writeFrame(conn, frame); err != nil {
			break
		}
	}

	// Growth is sampled every 1024 submits, so poll rather than assert once.
	grew := false
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if server.elastic.WorkerCount() > 1 {
			grew = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !grew {
		t.Fatalf("worker count stayed at %d under a full queue: the elastic pool never grew",
			server.elastic.WorkerCount())
	}
	if got, max := server.elastic.WorkerCount(), 4; got > max {
		t.Fatalf("worker count = %d, must not exceed maxWorker %d", got, max)
	}
}

// TestWithElasticPoolOptionValidation covers the normalisation rules: bounds
// that make no sense must not survive into the pool, and a partially specified
// ratio must not blank the other one.
func TestWithElasticPoolOptionValidation(t *testing.T) {
	// Option order must not matter: WithScaleRatio before WithElasticPool has to
	// survive, which is why both options lazily create the config.
	cfg := defaultOptions()
	WithScaleRatio(0.9, 0.3)(&cfg)
	WithElasticPool(3, 6)(&cfg)
	if cfg.ElasticPool == nil {
		t.Fatal("ElasticPool nil")
	}
	if cfg.ElasticPool.ScaleUpRatio != 0.9 || cfg.ElasticPool.ScaleDownRatio != 0.3 {
		t.Fatalf("ratios = (%v, %v), want (0.9, 0.3): WithScaleRatio before WithElasticPool was lost",
			cfg.ElasticPool.ScaleUpRatio, cfg.ElasticPool.ScaleDownRatio)
	}
	if cfg.ElasticPool.MinWorker != 3 || cfg.ElasticPool.MaxWorker != 6 {
		t.Fatalf("bounds = (%d, %d), want (3, 6)", cfg.ElasticPool.MinWorker, cfg.ElasticPool.MaxWorker)
	}

	// Out-of-range ratios are ignored rather than stored.
	out := defaultOptions()
	WithScaleRatio(1.5, -0.2)(&out)
	if out.ElasticPool == nil {
		t.Fatal("ElasticPool nil")
	}
	if out.ElasticPool.ScaleUpRatio != 0 || out.ElasticPool.ScaleDownRatio != 0 {
		t.Fatalf("out-of-range ratios were stored: (%v, %v)",
			out.ElasticPool.ScaleUpRatio, out.ElasticPool.ScaleDownRatio)
	}

	// The pool constructor is the last line of defence: max below min is raised.
	p := shared.NewScalablePool(nil, nil, 5, 2, 8, shared.PolicyDrop)
	defer p.Stop()
	minW, maxW := p.WorkerBounds()
	if maxW < minW {
		t.Fatalf("bounds inverted: min=%d max=%d", minW, maxW)
	}
}
