package lwm2m

import (
	"strings"
	"testing"
	"time"
)

func TestParsePath(t *testing.T) {
	path, err := ParsePath("/3/0/1")
	if err != nil {
		t.Fatal(err)
	}
	if path.String() != "/3/0/1" {
		t.Fatalf("path = %s, want /3/0/1", path.String())
	}
}

func TestRegistrationLifecycleAndResources(t *testing.T) {
	server := NewServer(WithDefaultLifetime(time.Minute))
	path, err := ParsePath("/3/0/1")
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient("device-1", server, WithObjects(path))
	reg, err := client.Register()
	if err != nil {
		t.Fatal(err)
	}
	if reg.Endpoint != "device-1" || len(reg.Objects) != 1 {
		t.Fatalf("registration = %#v", reg)
	}
	if _, ok := server.Registration("device-1"); !ok {
		t.Fatal("registration missing")
	}
	if _, err := client.Update(2 * time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := client.Write(path, []byte("online")); err != nil {
		t.Fatal(err)
	}
	resource, ok := client.Read(path)
	if !ok || string(resource.Value) != "online" {
		t.Fatalf("resource = %#v ok=%v", resource, ok)
	}
	client.Deregister()
	if _, ok := server.Registration("device-1"); ok {
		t.Fatal("registration still present after deregister")
	}
}

func TestSweepExpired(t *testing.T) {
	server := NewServer()
	client := NewClient("device-1", server, WithLifetime(10*time.Millisecond))
	client.Register()
	if removed := server.SweepExpired(time.Now().Add(time.Second)); removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
}

func TestInvalidCoAPPayloadDoesNotMutateRegistrations(t *testing.T) {
	server := NewServer()
	if _, err := server.HandleCoAPPayload([]byte("register device-1 bad-lifetime")); err == nil {
		t.Fatal("HandleCoAPPayload succeeded, want error")
	}
	if _, ok := server.Registration("device-1"); ok {
		t.Fatal("invalid command created registration")
	}
	if _, err := server.HandleCoAPPayload([]byte("unknown device-1")); err == nil {
		t.Fatal("unknown command succeeded, want error")
	}
}

// TestHandleCoAPPayloadUpdate covers the update command path of the text
// responder (previously 0% coverage; only Server.Update itself was tested).
func TestHandleCoAPPayloadUpdate(t *testing.T) {
	server := NewServer()
	if _, err := server.HandleCoAPPayload([]byte("register device-1 60 /3/0/1")); err != nil {
		t.Fatal(err)
	}
	out, err := server.HandleCoAPPayload([]byte("update device-1 120"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "updated device-1" {
		t.Fatalf("output = %q, want updated device-1", out)
	}
	reg, ok := server.Registration("device-1")
	if !ok {
		t.Fatal("registration missing after update")
	}
	if reg.Lifetime != 120*time.Second {
		t.Fatalf("lifetime = %v, want 2m", reg.Lifetime)
	}
	// Invalid field count must not mutate anything.
	if _, err := server.HandleCoAPPayload([]byte("update device-1")); err != ErrInvalidCoAPPayload {
		t.Fatalf("short update error = %v, want %v", err, ErrInvalidCoAPPayload)
	}
	// Unknown endpoint reports a registration error.
	if _, err := server.HandleCoAPPayload([]byte("update missing 60")); err != ErrRegistrationGone {
		t.Fatalf("unknown update error = %v, want %v", err, ErrRegistrationGone)
	}
}

// TestHandleCoAPPayloadDiscover covers the discover command path of the text
// responder (previously 0% coverage): an empty server reports no objects, and
// a populated one lists objects and resources with their operation letters.
func TestHandleCoAPPayloadDiscover(t *testing.T) {
	server := NewServer()
	out, err := server.HandleCoAPPayload([]byte("discover"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "no objects registered" {
		t.Fatalf("empty discover = %q, want no objects registered", out)
	}

	server.RegisterObject(ObjectDefinition{
		ID:      3,
		Name:    "Device",
		Version: "1.0",
		Resources: []ResourceDefinition{
			{ID: 0, Name: "Manufacturer", Operations: OpRead},
			{ID: 1, Name: "Mode", Operations: OpRead | OpWrite},
			{ID: 2, Name: "Reboot", Operations: OpExecute},
		},
	})
	out, err = server.HandleCoAPPayload([]byte("discover"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, want := range []string{
		"3/Device/1.0",
		"/3/0/0 Manufacturer R",
		"/3/0/1 Mode RW",
		"/3/0/2 Reboot E",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("discover output missing %q:\n%s", want, text)
		}
	}
}
