package lwm2m

import (
	"testing"
	"time"
)

func TestServerRegisterObject(t *testing.T) {
	srv := NewServer()
	obj := ObjectDefinition{
		ID:   3,
		Name: "Device",
		Resources: []ResourceDefinition{
			{ID: 0, Name: "Manufacturer", Type: ResourceString, Operations: OpRead},
		},
	}
	srv.RegisterObject(obj)
	objs := srv.SupportedObjects()
	if len(objs) != 1 {
		t.Fatalf("SupportedObjects = %d, want 1", len(objs))
	}
	if objs[0].Name != "Device" {
		t.Fatalf("object name = %s, want Device", objs[0].Name)
	}
}

func TestServerSupportedObjectsEmpty(t *testing.T) {
	srv := NewServer()
	objs := srv.SupportedObjects()
	if objs == nil {
		t.Fatal("SupportedObjects should not return nil")
	}
	if len(objs) != 0 {
		t.Fatalf("SupportedObjects = %d, want 0", len(objs))
	}
}

func TestServerGetResourceDefinition(t *testing.T) {
	srv := NewServer()
	obj := ObjectDefinition{
		ID:   5,
		Name: "Test",
		Resources: []ResourceDefinition{
			{ID: 1, Name: "Value", Type: ResourceInteger, Operations: OpRead | OpWrite},
		},
	}
	srv.RegisterObject(obj)

	def, ok := srv.GetResourceDefinition(ObjectPath{ObjectID: 5, InstanceID: 0, ResourceID: 1})
	if !ok {
		t.Fatal("GetResourceDefinition should find registered resource")
	}
	if def.Name != "Value" {
		t.Fatalf("resource name = %s, want Value", def.Name)
	}

	_, ok = srv.GetResourceDefinition(ObjectPath{ObjectID: 999})
	if ok {
		t.Fatal("GetResourceDefinition should not find unregistered object")
	}
}

func TestServerRegisterAndUpdate(t *testing.T) {
	srv := NewServer()
	reg := srv.Register("device-1", 3600*time.Second, ObjectPath{ObjectID: 3, InstanceID: 0})
	if reg.Endpoint != "device-1" {
		t.Fatalf("endpoint = %s, want device-1", reg.Endpoint)
	}
	_, err := srv.Update("device-1", 7200*time.Second)
	if err != nil {
		t.Fatal(err)
	}
}

func TestServerUpdateUnknown(t *testing.T) {
	srv := NewServer()
	_, err := srv.Update("unknown-device", 3600*time.Second)
	if err == nil {
		t.Fatal("expected error for unknown device update")
	}
}

// TestServerWriteReentrantOnWrite verifies Write does not deadlock when the
// OnWrite callback re-enters Server methods (e.g. Read).
func TestServerWriteReentrantOnWrite(t *testing.T) {
	s := NewServer()
	obj := ObjectDefinition{ID: 3, Resources: []ResourceDefinition{{ID: 0, Operations: OpWrite}}}
	s.RegisterObject(obj)
	s.Register("ep1", time.Minute, ObjectPath{ObjectID: 3, InstanceID: 0, ResourceID: 0})

	var onWriteCalled bool
	s.OnWrite = func(resourcePath string, value []byte) {
		onWriteCalled = true
		// Re-entrant read of the resource just written must not deadlock.
		if _, ok := s.Read("ep1", ObjectPath{ObjectID: 3, InstanceID: 0, ResourceID: 0}); !ok {
			t.Error("Read inside OnWrite did not find the resource")
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := s.Write("ep1", ObjectPath{ObjectID: 3, InstanceID: 0, ResourceID: 0}, []byte("v")); err != nil {
			t.Errorf("Write failed: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Write deadlocked inside OnWrite callback")
	}
	if !onWriteCalled {
		t.Fatal("OnWrite callback was not called")
	}
}
