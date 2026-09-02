package coap

import (
	"testing"
)

// TestObserverRegistryReRegisterReplacesRelation verifies RFC 7641 §3.6
// semantics: a new GET+Observe for the same (resource, remote) replaces the
// previous relation even when the token changes, so rotating tokens cannot
// accumulate relations.
func TestObserverRegistryReRegisterReplacesRelation(t *testing.T) {
	reg := NewObserverRegistry()
	const resource = "/3/0/0"
	const remote = "192.168.1.1:1234"

	first := reg.Register(resource, remote, []byte{1})
	if first == nil {
		t.Fatal("first register failed")
	}
	if reg.Count() != 1 {
		t.Fatalf("count = %d, want 1", reg.Count())
	}

	second := reg.Register(resource, remote, []byte{2})
	if second == nil {
		t.Fatal("re-register with new token failed")
	}
	if reg.Count() != 1 {
		t.Fatalf("count after replace = %d, want 1", reg.Count())
	}
	subs := reg.Notify(resource)
	if len(subs) != 1 {
		t.Fatalf("observer count = %d, want 1", len(subs))
	}
	if string(subs[0].Token) != "\x02" {
		t.Fatalf("observer token = %v, want the new token", subs[0].Token)
	}
	// The old relation must be gone: removing by the old token is a no-op.
	reg.Remove(resource, remote, []byte{1})
	if reg.Count() != 1 {
		t.Fatalf("count after stale-token remove = %d, want 1", reg.Count())
	}
}

// TestObserverRegistryPerRemoteCap verifies that a single peer cannot grow
// the registry without limit, while count-neutral re-registration still works
// at the cap.
func TestObserverRegistryPerRemoteCap(t *testing.T) {
	reg := NewObserverRegistry()
	remote := "192.168.1.1:1234"
	resource := func(i int) string { return string([]byte{'/', byte('0' + i/10), '/', byte('0' + i%10)}) }

	for i := 0; i < maxObserversPerRemote; i++ {
		if reg.Register(resource(i), remote, []byte{byte(i)}) == nil {
			t.Fatalf("register %d failed below the cap", i)
		}
	}
	if reg.Register("/99/99", remote, []byte{0xEE}) != nil {
		t.Fatal("register above per-remote cap must be refused")
	}
	if reg.Count() != maxObserversPerRemote {
		t.Fatalf("count = %d, want %d", reg.Count(), maxObserversPerRemote)
	}
	// Re-registering one of the existing resources with a new token replaces
	// the relation (count-neutral) and must not be refused at the cap.
	if reg.Register(resource(0), remote, []byte{0xAA}) == nil {
		t.Fatal("count-neutral re-register at the cap must succeed")
	}
	if reg.Count() != maxObserversPerRemote {
		t.Fatalf("count after replace at cap = %d, want %d", reg.Count(), maxObserversPerRemote)
	}
}

// TestObserverRegistryRemoveByToken verifies relation cancellation by
// (remote, token) across resources, as used for RST handling.
func TestObserverRegistryRemoveByToken(t *testing.T) {
	reg := NewObserverRegistry()
	remote := "192.168.1.1:1234"
	reg.Register("/a/0/0", remote, []byte{1})
	reg.Register("/b/0/0", remote, []byte{2})
	reg.Register("/a/0/1", "192.168.1.2:5678", []byte{9})

	reg.RemoveByToken(remote, []byte{1})
	if len(reg.Notify("/a/0/0")) != 0 {
		t.Fatal("relation /a/0/0 still present after RemoveByToken")
	}
	if len(reg.Notify("/b/0/0")) != 1 {
		t.Fatal("relation /b/0/0 must be untouched")
	}
	if reg.HasObservers(remote) != true {
		t.Fatal("remote still holds /b/0/0, HasObservers must be true")
	}
	reg.RemoveByToken(remote, []byte{2})
	if reg.HasObservers(remote) {
		t.Fatal("HasObservers must be false after all relations removed")
	}
	if reg.Count() != 1 {
		t.Fatalf("count = %d, want 1 (other remote)", reg.Count())
	}
	if len(reg.Notify("/a/0/1")) != 1 {
		t.Fatal("other remote's relation must be untouched")
	}
}

// TestObserverRegistryGlobalCap sanity-checks the total relation bound.
func TestObserverRegistryGlobalCap(t *testing.T) {
	reg := NewObserverRegistry()
	// maxObserversTotal entries across distinct remotes must fit, and one
	// more must be refused.
	reg.mu.Lock()
	reg.subs["/x/0/0"] = make(map[string]*Observer)
	reg.remoteCount["probe"] = maxObserversTotal // simulate saturation
	reg.total = maxObserversTotal
	reg.mu.Unlock()
	if reg.Register("/y/0/0", "probe", []byte{1}) != nil {
		t.Fatal("register at total cap must be refused")
	}
}
