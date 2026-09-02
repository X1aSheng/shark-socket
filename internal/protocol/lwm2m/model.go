package lwm2m

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidPath       = errors.New("invalid lwm2m path")
	ErrRegistrationGone  = errors.New("registration not found")
	ErrReadOnly          = errors.New("resource is read-only")
	ErrNotSupported      = errors.New("operation not supported")
	ErrRegistrationLimit = errors.New("registration limit reached")
)

// ResourceType represents OMA LwM2M resource data types.
type ResourceType int

const (
	ResourceString ResourceType = iota
	ResourceInteger
	ResourceFloat
	ResourceBoolean
	ResourceOpaque
	ResourceObjLink
	ResourceTime
)

// OperationMask defines allowed operations on a resource.
type OperationMask byte

const (
	OpRead OperationMask = 1 << iota
	OpWrite
	OpExecute
	OpDelete
	OpCreate
)

func (m OperationMask) Allows(op OperationMask) bool {
	return m&op != 0
}

// ResourceDefinition describes an OMA LwM2M resource.
type ResourceDefinition struct {
	ID         int
	Name       string
	Type       ResourceType
	Operations OperationMask
	Multiple   bool
	Mandatory  bool
}

// ObjectDefinition describes an OMA LwM2M object type.
type ObjectDefinition struct {
	ID        int
	Name      string
	Version   string
	Mandatory bool
	Resources []ResourceDefinition
}

// DeviceInfo carries standard LwM2M Device object (ID 3) fields.
type DeviceInfo struct {
	Manufacturer    string
	ModelNumber     string
	SerialNumber    string
	FirmwareVersion string
	BatteryLevel    int
	PowerSource     int
	MemoryFreeKB    int64
	ErrorCodes      []int
	CurrentTime     time.Time
	UTCOffset       string
	BindingMode     string
}

type ObjectPath struct {
	ObjectID   int
	InstanceID int
	ResourceID int
}

func ParsePath(path string) (ObjectPath, error) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 3 {
		return ObjectPath{}, ErrInvalidPath
	}
	values := make([]int, 3)
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return ObjectPath{}, ErrInvalidPath
		}
		values[i] = n
	}
	return ObjectPath{ObjectID: values[0], InstanceID: values[1], ResourceID: values[2]}, nil
}

func (p ObjectPath) String() string {
	return fmt.Sprintf("/%d/%d/%d", p.ObjectID, p.InstanceID, p.ResourceID)
}

type Resource struct {
	Path      ObjectPath
	Value     []byte
	UpdatedAt time.Time
}

type Registration struct {
	Endpoint  string
	Lifetime  time.Duration
	Objects   []ObjectPath
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (r Registration) Expired(now time.Time) bool {
	if r.Lifetime <= 0 {
		return false
	}
	return now.Sub(r.UpdatedAt) > r.Lifetime
}
