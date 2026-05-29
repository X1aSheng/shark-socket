package lwm2m

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/X1aSheng/shark-socket-new/internal/core"
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
	reg := s.Register(fields[1], lifetime, objects...)
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

func parseLifetime(raw string) (time.Duration, error) {
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 0 {
		return 0, ErrInvalidCoAPPayload
	}
	return time.Duration(seconds) * time.Second, nil
}
