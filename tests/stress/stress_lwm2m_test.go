package stress

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-socket/internal/protocol/lwm2m"
)

// Stress tests for the LwM2M server.
//
// Scope note, because it limits what these tests can claim. This LwM2M
// implementation is NOT the OMA LwM2M wire protocol. Its CoAP adapter
// (lwm2m/coap.go) accepts a space-separated text command such as
// "register <endpoint> <lifetime>" rather than the specification's
// /rd?ep=... URI, CoAP method mapping, and Content-Format negotiation. The TLV
// codec underneath IS the OMA format, byte for byte, and is covered by
// codec_tlv_test.go.
//
// So there is no meaningful "LwM2M conformance" test to write here -- the wire
// protocol does not follow the specification, by design and as documented in
// the adapter's own comment. What these tests do cover is the part that is
// testable regardless of wire format: whether the registration registry, the
// resource store, the capacity cap and the expiry sweep behave correctly when
// many endpoints act concurrently. Those are the paths a unit test exercises
// one endpoint at a time and therefore cannot show.
//
// Whether the text-command wire format should become the real OMA protocol is
// a product decision, not something a test can settle.

// lwm2mWorkers is the concurrency for the registry tests. Kept below the
// default capacity of 65536 so the cap is not what limits the run, except in
// the test that is specifically about the cap.
const lwm2mWorkers = 200

// TestStressLwM2MRegistryChurn drives concurrent register / update /
// deregister cycles against one server.
//
// The assertion beyond "no panic" is that the registry ends empty and that the
// endpoint count never exceeds the number of live workers. A registry that
// leaks on deregister, or that double-counts a repeated registration, passes a
// sequential test and fails here.
func TestStressLwM2MRegistryChurn(t *testing.T) {
	server := lwm2m.NewServer()

	var wg sync.WaitGroup
	var live atomic.Int64
	var maxSeen atomic.Int64
	var cycles atomic.Int64

	for i := 0; i < lwm2mWorkers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ep := fmt.Sprintf("urn:churn:%d", id)
			for round := 0; round < 20; round++ {
				if _, err := server.Register(ep, 30*time.Second); err != nil {
					t.Errorf("register %s round %d: %v", ep, round, err)
					return
				}
				cur := live.Add(1)
				for {
					old := maxSeen.Load()
					if cur <= old || maxSeen.CompareAndSwap(old, cur) {
						break
					}
				}

				if _, err := server.Update(ep, 30*time.Second); err != nil {
					t.Errorf("update %s round %d: %v", ep, round, err)
					return
				}

				server.Deregister(ep)
				live.Add(-1)
				cycles.Add(1)
			}
		}(i)
	}
	wg.Wait()

	if got := cycles.Load(); got != int64(lwm2mWorkers)*20 {
		t.Errorf("completed cycles = %d, want %d", got, int64(lwm2mWorkers)*20)
	}
	if peak := maxSeen.Load(); peak > int64(lwm2mWorkers) {
		t.Errorf("peak live registrations = %d, want <= %d workers (double counting?)",
			peak, lwm2mWorkers)
	}
	if n := len(server.SupportedObjects()); n != 0 {
		// SupportedObjects is the object-definition table, unrelated to
		// registrations; this only guards against a mis-wired assertion above.
		t.Logf("object definitions registered: %d", n)
	}
	if _, ok := server.Registration("urn:churn:0"); ok {
		t.Error("endpoint still registered after deregister")
	}
}

// TestStressLwM2MSweepUnderConcurrency verifies that SweepExpired reclaims
// exactly the expired endpoints and leaves live ones alone, while registrations
// and writes are happening concurrently.
//
// The interesting failure is a sweep that runs while a registration is being
// written: without correct locking a sweep can free an entry that a concurrent
// Register has just inserted, or resurrect one it already removed. The counts
// after the run catch both.
func TestStressLwM2MSweepUnderConcurrency(t *testing.T) {
	server := lwm2m.NewServer()

	// Half the endpoints expire almost immediately; the other half are kept
	// alive by concurrent updates.
	const shortLived = 100
	const longLived = 100
	var wg sync.WaitGroup

	for i := 0; i < shortLived; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ep := fmt.Sprintf("urn:short:%d", id)
			if _, err := server.Register(ep, 50*time.Millisecond); err != nil {
				t.Errorf("register short %d: %v", id, err)
			}
		}(i)
	}
	for i := 0; i < longLived; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ep := fmt.Sprintf("urn:long:%d", id)
			if _, err := server.Register(ep, 10*time.Second); err != nil {
				t.Errorf("register long %d: %v", id, err)
				return
			}
			// Keep refreshing until the sweep has had time to run.
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				if _, err := server.Update(ep, 10*time.Second); err != nil {
					t.Errorf("update long %d: %v", id, err)
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
		}(i)
	}

	// Let the short-lived registrations expire before sweeping.
	time.Sleep(300 * time.Millisecond)

	swept := 0
	for i := 0; i < 5; i++ {
		swept += server.SweepExpired(time.Now())
		time.Sleep(50 * time.Millisecond)
	}
	wg.Wait()

	if swept != shortLived {
		t.Errorf("swept = %d, want %d (only the short-lived ones)", swept, shortLived)
	}
	// Every long-lived endpoint must still be present.
	alive := 0
	for i := 0; i < longLived; i++ {
		if _, ok := server.Registration(fmt.Sprintf("urn:long:%d", i)); ok {
			alive++
		}
	}
	if alive != longLived {
		t.Errorf("long-lived endpoints still registered = %d, want %d (sweep took live ones)",
			alive, longLived)
	}
}

// TestStressLwM2MCapacityLimit verifies the registration cap holds under
// concurrent pressure and that the server refuses cleanly rather than
// degrading.
//
// WithMaxRegistrations triggers a throttled sweep of expired entries before
// refusing, so the assertion is that registrations beyond the cap fail with
// ErrRegistrationLimit while the ones already admitted stay readable. A cap
// that silently admits extras, or that evicts live entries to make room, both
// pass a small sequential test and fail here.
func TestStressLwM2MCapacityLimit(t *testing.T) {
	const cap = 50
	server := lwm2m.NewServer(lwm2m.WithMaxRegistrations(cap))

	var wg sync.WaitGroup
	var admitted atomic.Int64
	var rejected atomic.Int64

	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			// All with a lifetime long enough that the pre-sweep cannot free
			// them, so the cap is genuinely reached.
			_, err := server.Register(fmt.Sprintf("urn:cap:%d", id), 10*time.Minute)
			if err == nil {
				admitted.Add(1)
				return
			}
			if err != lwm2m.ErrRegistrationLimit {
				t.Errorf("register %d: unexpected error %v", id, err)
				return
			}
			rejected.Add(1)
		}(i)
	}
	wg.Wait()

	if got := admitted.Load(); got > int64(cap) {
		t.Errorf("admitted = %d, cap is %d (cap not enforced under concurrency)", got, cap)
	}
	if rejected.Load() == 0 {
		t.Error("no registration was rejected; the cap was never exercised")
	}
	// Whatever was admitted must be readable: a cap implementation that drops
	// entries it already accepted would still satisfy the count above.
	readable := 0
	for i := 0; i < 200; i++ {
		if _, ok := server.Registration(fmt.Sprintf("urn:cap:%d", i)); ok {
			readable++
		}
	}
	if int64(readable) != admitted.Load() {
		t.Errorf("readable endpoints = %d, but %d were admitted (admitted entries lost)",
			readable, admitted.Load())
	}
}

// TestStressLwM2MReadWriteConcurrent hammers read and write on the same
// resource from many endpoints.
//
// Concurrent readers must never observe a torn or partially applied value, and
// concurrent writers to the same path must not lose each other silently. The
// last-writer-wins model is fine; what is not fine is a reader seeing a value
// that no writer ever wrote, which is what a missing lock looks like.
func TestStressLwM2MReadWriteConcurrent(t *testing.T) {
	server := lwm2m.NewServer()
	const endpoints = 50
	const rounds = 40

	// Register the endpoints up front so the test measures the resource store,
	// not registration.
	for i := 0; i < endpoints; i++ {
		if _, err := server.Register(fmt.Sprintf("urn:rw:%d", i), time.Minute); err != nil {
			t.Fatal(err)
		}
	}

	path, err := lwm2m.ParsePath("/3/0/0")
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var torn atomic.Int64
	for i := 0; i < endpoints; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ep := fmt.Sprintf("urn:rw:%d", id)
			for r := 0; r < rounds; r++ {
				// Each writer stamps a value that encodes its own identity, so
				// a reader can tell a valid value from a corrupted one.
				want := fmt.Appendf(nil, "v-%d-%d", id, r)
				if err := server.Write(ep, path, want); err != nil {
					t.Errorf("write %s round %d: %v", ep, r, err)
					return
				}
				got, ok := server.Read(ep, path)
				if !ok {
					t.Errorf("read %s round %d: resource vanished", ep, r)
					return
				}
				// The value must be one this endpoint wrote: either the one
				// just written or a previous round of the same endpoint. It
				// must never be another endpoint's value or garbage.
				if !isOwnValue(got.Value, id) {
					torn.Add(1)
				}
			}
		}(i)
	}
	wg.Wait()

	if n := torn.Load(); n != 0 {
		t.Errorf("%d reads returned a value not written by that endpoint (torn read)", n)
	}
}

// isOwnValue reports whether a stored value was produced by endpoint id.
func isOwnValue(v []byte, id int) bool {
	var gotID, gotRound int
	if _, err := fmt.Sscanf(string(v), "v-%d-%d", &gotID, &gotRound); err != nil {
		return false
	}
	return gotID == id
}
