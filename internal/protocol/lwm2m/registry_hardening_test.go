package lwm2m

import (
	"errors"
	"strconv"
	"testing"
	"time"
)

// TestRegisterCapacityLimit verifies that the registration table cannot be
// grown without bound and that refreshing an existing endpoint still works at
// the cap.
func TestRegisterCapacityLimit(t *testing.T) {
	srv := NewServer(WithMaxRegistrations(2))
	if _, err := srv.Register("ep1", time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Register("ep2", time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Register("ep3", time.Minute); !errors.Is(err, ErrRegistrationLimit) {
		t.Fatalf("register at capacity error = %v, want %v", err, ErrRegistrationLimit)
	}
	// Re-registering an existing endpoint (lifetime refresh) is an update,
	// not a new entry, and must succeed at the cap.
	if _, err := srv.Register("ep1", time.Minute); err != nil {
		t.Fatalf("refresh at capacity failed: %v", err)
	}
	srv.Deregister("ep2")
	if _, err := srv.Register("ep3", time.Minute); err != nil {
		t.Fatalf("register after deregister failed: %v", err)
	}
}

// TestRegisterCapacitySweepsExpiredFirst verifies that a full registry first
// reclaims expired registrations (throttled sweep) before refusing.
func TestRegisterCapacitySweepsExpiredFirst(t *testing.T) {
	srv := NewServer(WithMaxRegistrations(1))
	if _, err := srv.Register("ep1", 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	// Reset the throttle so the sweep runs on this Register call.
	srv.mu.Lock()
	srv.lastSweepAt = time.Time{}
	srv.mu.Unlock()

	if _, err := srv.Register("ep2", time.Minute); err != nil {
		t.Fatalf("register should sweep expired entry first: %v", err)
	}
	if _, ok := srv.Registration("ep1"); ok {
		t.Fatal("expired ep1 registration still present")
	}
	if _, ok := srv.Registration("ep2"); !ok {
		t.Fatal("ep2 registration missing")
	}
}

// TestLifetimeClampAndParseBoundary verifies lifetime is clamped at the server
// layer and that the text protocol rejects lifetimes beyond the cap (which
// also removes the int64 seconds-to-Duration overflow path).
func TestLifetimeClampAndParseBoundary(t *testing.T) {
	srv := NewServer()
	reg, err := srv.Register("ep1", 40*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if reg.Lifetime != maxLifetime {
		t.Fatalf("lifetime = %v, want clamped to %v", reg.Lifetime, maxLifetime)
	}
	// Update clamps too.
	updated, err := srv.Update("ep1", 365*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Lifetime != maxLifetime {
		t.Fatalf("updated lifetime = %v, want %v", updated.Lifetime, maxLifetime)
	}

	maxSeconds := int64(maxLifetime / time.Second)
	if d, err := parseLifetime(strconv.FormatInt(maxSeconds, 10)); err != nil || d != maxLifetime {
		t.Fatalf("parseLifetime(%d) = %v, %v; want %v, nil", maxSeconds, d, err, maxLifetime)
	}
	if _, err := parseLifetime(strconv.FormatInt(maxSeconds+1, 10)); err == nil {
		t.Fatal("parseLifetime above cap must fail")
	}
	// Huge values (would overflow the duration multiplication) are rejected
	// by the cap check before any arithmetic.
	if _, err := parseLifetime("9223372036854775807"); err == nil {
		t.Fatal("parseLifetime max-int must fail")
	}
	if _, err := parseLifetime("not-a-number"); err == nil {
		t.Fatal("parseLifetime garbage must fail")
	}
}

// TestHandleCoAPPayloadRegistrationLimit surfaces the limit through the text
// protocol instead of silently dropping the registration.
func TestHandleCoAPPayloadRegistrationLimit(t *testing.T) {
	srv := NewServer(WithMaxRegistrations(1))
	if _, err := srv.HandleCoAPPayload([]byte("register ep1 60")); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.HandleCoAPPayload([]byte("register ep2 60")); !errors.Is(err, ErrRegistrationLimit) {
		t.Fatalf("register beyond cap error = %v, want %v", err, ErrRegistrationLimit)
	}
	// An oversized lifetime is rejected by the parser.
	if _, err := srv.HandleCoAPPayload([]byte("register ep3 99999999999999")); err == nil {
		t.Fatal("oversized lifetime must be rejected")
	}
}
