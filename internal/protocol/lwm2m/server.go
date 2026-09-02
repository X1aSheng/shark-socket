package lwm2m

import (
	"sync"
	"time"
)

type Server struct {
	mu               sync.RWMutex
	registrations    map[string]Registration
	resources        map[string]map[string]Resource
	objects          map[int]ObjectDefinition
	defaultLife      time.Duration
	maxRegistrations int
	lastSweepAt      time.Time
	OnWrite          func(resourcePath string, value []byte)
}

// Registry bounds. The registry is fed by unauthenticated network input in
// the CoAP responder mode, so both the entry count and every registration's
// lifetime are capped; expired entries are reclaimed by a throttled sweep
// when the registry is full.
const (
	defaultMaxRegistrations = 65536
	maxLifetime             = 30 * 24 * time.Hour
	sweepThrottle           = time.Minute
)

type ServerOption func(*Server)

func NewServer(opts ...ServerOption) *Server {
	s := &Server{
		registrations:    make(map[string]Registration),
		resources:        make(map[string]map[string]Resource),
		objects:          make(map[int]ObjectDefinition),
		defaultLife:      5 * time.Minute,
		maxRegistrations: defaultMaxRegistrations,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// WithMaxRegistrations caps the number of concurrent endpoint registrations.
// Values <= 0 keep the default (65536). When the cap is reached, Register
// first attempts a throttled sweep of expired registrations before refusing
// with ErrRegistrationLimit.
func WithMaxRegistrations(max int) ServerOption {
	return func(s *Server) {
		if max > 0 {
			s.maxRegistrations = max
		}
	}
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

// Register stores or replaces an endpoint registration. Lifetime is clamped
// to maxLifetime. When the registration table is full, expired entries are
// reclaimed by a throttled sweep; if the table is still full the registration
// is refused with ErrRegistrationLimit so an unauthenticated peer cannot grow
// the registry without bound.
func (s *Server) Register(endpoint string, lifetime time.Duration, objects ...ObjectPath) (Registration, error) {
	if lifetime <= 0 {
		lifetime = s.defaultLife
	}
	if lifetime > maxLifetime {
		lifetime = maxLifetime
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
	defer s.mu.Unlock()
	if _, exists := s.registrations[endpoint]; !exists {
		if len(s.registrations) >= s.maxRegistrations {
			// Throttled amortized sweep: only reclaim stale entries at most
			// once per sweepThrottle so a saturation flood cannot force an
			// O(n) scan per packet.
			if time.Since(s.lastSweepAt) >= sweepThrottle {
				s.sweepLocked(now)
				s.lastSweepAt = now
			}
			if len(s.registrations) >= s.maxRegistrations {
				return Registration{}, ErrRegistrationLimit
			}
		}
	}
	s.registrations[endpoint] = reg
	if _, ok := s.resources[endpoint]; !ok {
		s.resources[endpoint] = make(map[string]Resource)
	}
	return reg, nil
}

func (s *Server) Update(endpoint string, lifetime time.Duration) (Registration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reg, ok := s.registrations[endpoint]
	if !ok {
		return Registration{}, ErrRegistrationGone
	}
	if lifetime > 0 {
		if lifetime > maxLifetime {
			lifetime = maxLifetime
		}
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

// SweepExpired removes registrations whose lifetime has elapsed. Callers may
// invoke it periodically; the registry also sweeps internally (throttled)
// when Register hits the capacity limit.
func (s *Server) SweepExpired(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sweepLocked(now)
}

// sweepLocked removes expired registrations. Caller must hold s.mu.
func (s *Server) sweepLocked(now time.Time) int {
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
