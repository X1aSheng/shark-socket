package lwm2m

import (
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
	reg := client.Register()
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
