package lwm2m

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

var ErrInvalidCoAPPayload = errors.New("invalid lwm2m coap payload")

// NewCoAPResponder adapts a small text command protocol to the LwM2M server.
//
// Supported payloads:
//   - register <endpoint> <lifetime-seconds> [object-path...]
//   - update <endpoint> <lifetime-seconds>
//   - deregister <endpoint>
//   - write <endpoint> <resource-path> <value>
//   - read <endpoint> <resource-path>
func NewCoAPResponder(server *Server) func(core.Session, core.Message) ([]byte, error) {
	return func(_ core.Session, msg core.Message) ([]byte, error) {
		return server.HandleCoAPPayload(msg.Payload)
	}
}

func (s *Server) HandleCoAPPayload(payload []byte) ([]byte, error) {
	fields := strings.Fields(string(payload))
	if len(fields) == 0 {
		return nil, ErrInvalidCoAPPayload
	}
	switch strings.ToLower(fields[0]) {
	case "register":
		return s.handleRegister(fields)
	case "update":
		return s.handleUpdate(fields)
	case "deregister":
		return s.handleDeregister(fields)
	case "write":
		return s.handleWrite(fields)
	case "read":
		return s.handleRead(fields)
	case "discover":
		return s.handleDiscover(fields)
	default:
		return nil, fmt.Errorf("%w: operation %q", ErrInvalidCoAPPayload, fields[0])
	}
}

func (s *Server) handleRegister(fields []string) ([]byte, error) {
	if len(fields) < 3 {
		return nil, ErrInvalidCoAPPayload
	}
	lifetime, err := parseLifetime(fields[2])
	if err != nil {
		return nil, err
	}
	objects := make([]ObjectPath, 0, len(fields)-3)
	for _, raw := range fields[3:] {
		path, err := ParsePath(raw)
		if err != nil {
			return nil, err
		}
		objects = append(objects, path)
	}
	reg, err := s.Register(fields[1], lifetime, objects...)
	if err != nil {
		return nil, err
	}
	return []byte("registered " + reg.Endpoint), nil
}

func (s *Server) handleUpdate(fields []string) ([]byte, error) {
	if len(fields) != 3 {
		return nil, ErrInvalidCoAPPayload
	}
	lifetime, err := parseLifetime(fields[2])
	if err != nil {
		return nil, err
	}
	reg, err := s.Update(fields[1], lifetime)
	if err != nil {
		return nil, err
	}
	return []byte("updated " + reg.Endpoint), nil
}

func (s *Server) handleDeregister(fields []string) ([]byte, error) {
	if len(fields) != 2 {
		return nil, ErrInvalidCoAPPayload
	}
	s.Deregister(fields[1])
	return []byte("deregistered " + fields[1]), nil
}

func (s *Server) handleWrite(fields []string) ([]byte, error) {
	if len(fields) < 4 {
		return nil, ErrInvalidCoAPPayload
	}
	path, err := ParsePath(fields[2])
	if err != nil {
		return nil, err
	}
	value := strings.Join(fields[3:], " ")
	if err := s.Write(fields[1], path, []byte(value)); err != nil {
		return nil, err
	}
	return []byte("changed " + fields[2]), nil
}

func (s *Server) handleRead(fields []string) ([]byte, error) {
	if len(fields) != 3 {
		return nil, ErrInvalidCoAPPayload
	}
	path, err := ParsePath(fields[2])
	if err != nil {
		return nil, err
	}
	resource, ok := s.Read(fields[1], path)
	if !ok {
		return nil, ErrRegistrationGone
	}
	return append([]byte(nil), resource.Value...), nil
}

func (s *Server) handleDiscover(_ []string) ([]byte, error) {
	objects := s.SupportedObjects()
	if len(objects) == 0 {
		return []byte("no objects registered"), nil
	}
	var lines []string
	for _, obj := range objects {
		lines = append(lines, fmt.Sprintf("%d/%s/%s", obj.ID, obj.Name, obj.Version))
		for _, res := range obj.Resources {
			ops := ""
			if res.Operations.Allows(OpRead) {
				ops += "R"
			}
			if res.Operations.Allows(OpWrite) {
				ops += "W"
			}
			if res.Operations.Allows(OpExecute) {
				ops += "E"
			}
			lines = append(lines, fmt.Sprintf("  /%d/%d/%d %s %s", obj.ID, 0, res.ID, res.Name, ops))
		}
	}
	return []byte(strings.Join(lines, "\n")), nil
}

// parseLifetime parses a registration lifetime in seconds. Values above
// maxLifetime are rejected so a peer cannot register (or refresh) a
// near-infinite lifetime, and the value is bounded far below the point where
// the seconds-to-Duration multiplication could overflow int64.
func parseLifetime(raw string) (time.Duration, error) {
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 || int64(seconds) > int64(maxLifetime/time.Second) {
		return 0, ErrInvalidCoAPPayload
	}
	return time.Duration(seconds) * time.Second, nil
}
