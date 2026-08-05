package lwm2m

import (
	"sync"
	"time"
)

type Server struct {
	mu            sync.RWMutex
	registrations map[string]Registration
	resources     map[string]map[string]Resource
	objects       map[int]ObjectDefinition
	defaultLife   time.Duration
	OnWrite       func(resourcePath string, value []byte)
}

type ServerOption func(*Server)

func NewServer(opts ...ServerOption) *Server {
	s := &Server{
		registrations: make(map[string]Registration),
		resources:     make(map[string]map[string]Resource),
		objects:       make(map[int]ObjectDefinition),
		defaultLife:   5 * time.Minute,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// RegisterObject adds a supported OMA object definition to the server.
func (s *Server) RegisterObject(def ObjectDefinition) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[def.ID] = def
}

// SupportedObjects returns all registered object definitions.
func (s *Server) SupportedObjects() []ObjectDefinition {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]ObjectDefinition, 0, len(s.objects))
	for _, def := range s.objects {
		result = append(result, def)
	}
	return result
}

// GetResourceDefinition looks up the definition for a resource path.
func (s *Server) GetResourceDefinition(path ObjectPath) (ResourceDefinition, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	def, ok := s.objects[path.ObjectID]
	if !ok {
		return ResourceDefinition{}, false
	}
	for _, res := range def.Resources {
		if res.ID == path.ResourceID {
			return res, true
		}
	}
	return ResourceDefinition{}, false
}

func WithDefaultLifetime(lifetime time.Duration) ServerOption {
	return func(s *Server) {
		if lifetime > 0 {
			s.defaultLife = lifetime
		}
	}
}

func (s *Server) Register(endpoint string, lifetime time.Duration, objects ...ObjectPath) Registration {
	if lifetime <= 0 {
		lifetime = s.defaultLife
	}
	now := time.Now()
	reg := Registration{
		Endpoint:  endpoint,
		Lifetime:  lifetime,
		Objects:   append([]ObjectPath(nil), objects...),
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.mu.Lock()
	s.registrations[endpoint] = reg
	if _, ok := s.resources[endpoint]; !ok {
		s.resources[endpoint] = make(map[string]Resource)
	}
	s.mu.Unlock()
	return reg
}

func (s *Server) Update(endpoint string, lifetime time.Duration) (Registration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reg, ok := s.registrations[endpoint]
	if !ok {
		return Registration{}, ErrRegistrationGone
	}
	if lifetime > 0 {
		reg.Lifetime = lifetime
	}
	reg.UpdatedAt = time.Now()
	s.registrations[endpoint] = reg
	return reg, nil
}

func (s *Server) Deregister(endpoint string) {
	s.mu.Lock()
	delete(s.registrations, endpoint)
	delete(s.resources, endpoint)
	s.mu.Unlock()
}

func (s *Server) Registration(endpoint string) (Registration, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	reg, ok := s.registrations[endpoint]
	return reg, ok
}

func (s *Server) Write(endpoint string, path ObjectPath, value []byte) error {
	s.mu.Lock()
	if _, ok := s.registrations[endpoint]; !ok {
		s.mu.Unlock()
		return ErrRegistrationGone
	}
	if def, ok := s.objects[path.ObjectID]; ok {
		for _, res := range def.Resources {
			if res.ID == path.ResourceID && !res.Operations.Allows(OpWrite) {
				s.mu.Unlock()
				return ErrReadOnly
			}
		}
	}
	if _, ok := s.resources[endpoint]; !ok {
		s.resources[endpoint] = make(map[string]Resource)
	}
	s.resources[endpoint][path.String()] = Resource{
		Path:      path,
		Value:     append([]byte(nil), value...),
		UpdatedAt: time.Now(),
	}
	// Snapshot the callback under the lock but invoke it after unlocking so
	// a re-entrant OnWrite (or one doing network I/O) cannot deadlock on the
	// non-reentrant mutex or stall every register/read/write for all peers.
	onWrite := s.OnWrite
	s.mu.Unlock()
	if onWrite != nil {
		onWrite(path.String(), value)
	}
	return nil
}

func (s *Server) Read(endpoint string, path ObjectPath) (Resource, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	resources, ok := s.resources[endpoint]
	if !ok {
		return Resource{}, false
	}
	resource, ok := resources[path.String()]
	return resource, ok
}

func (s *Server) SweepExpired(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for endpoint, reg := range s.registrations {
		if reg.Expired(now) {
			delete(s.registrations, endpoint)
			delete(s.resources, endpoint)
			removed++
		}
	}
	return removed
}
