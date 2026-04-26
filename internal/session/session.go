package session

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-socket/internal/types"
)

// BaseSession provides the core session state machine and metadata management.
// Protocol-specific sessions embed this and implement Send/SendTyped/Close.
type BaseSession struct {
	id         uint64
	protocol   types.ProtocolType
	remoteAddr net.Addr
	localAddr  net.Addr
	createdAt  time.Time

	state      atomic.Int32
	lastActive atomic.Int64

	ctx    context.Context
	cancel context.CancelFunc

	meta sync.Map
}

// NewBase creates a new BaseSession in the Connecting state.
func NewBase(id uint64, proto types.ProtocolType, remote, local net.Addr) *BaseSession {
	ctx, cancel := context.WithCancel(context.Background())
	b := &BaseSession{
		id:         id,
		protocol:   proto,
		remoteAddr: remote,
		localAddr:  local,
		createdAt:  time.Now(),
		ctx:        ctx,
		cancel:     cancel,
	}
	b.state.Store(int32(types.Connecting))
	b.lastActive.Store(time.Now().UnixNano())
	return b
}

// ID returns the unique session identifier.
func (b *BaseSession) ID() uint64 { return b.id }

// Protocol returns the protocol type.
func (b *BaseSession) Protocol() types.ProtocolType { return b.protocol }

// RemoteAddr returns the remote network address.
func (b *BaseSession) RemoteAddr() net.Addr { return b.remoteAddr }

// LocalAddr returns the local network address.
func (b *BaseSession) LocalAddr() net.Addr { return b.localAddr }

// CreatedAt returns the session creation time.
func (b *BaseSession) CreatedAt() time.Time { return b.createdAt }

// State returns the current session state.
func (b *BaseSession) State() types.SessionState {
	return types.SessionState(b.state.Load())
}

// IsAlive returns true if the session is in the Active state.
func (b *BaseSession) IsAlive() bool {
	return types.SessionState(b.state.Load()) == types.Active
}

// LastActiveAt returns the last activity time.
func (b *BaseSession) LastActiveAt() time.Time {
	return time.Unix(0, b.lastActive.Load())
}

// TouchActive atomically updates the last activity time.
func (b *BaseSession) TouchActive() {
	b.lastActive.Store(time.Now().UnixNano())
}

// SetState attempts a CAS state transition. Returns true if successful.
func (b *BaseSession) SetState(newState types.SessionState) bool {
	for {
		current := b.state.Load()
		if types.SessionState(current) == newState {
			return false
		}
		if b.state.CompareAndSwap(current, int32(newState)) {
			return true
		}
	}
}

// Context returns the session context (cancelled on Close).
func (b *BaseSession) Context() context.Context { return b.ctx }

// CancelContext cancels the session context.
func (b *BaseSession) CancelContext() { b.cancel() }

// SetMeta stores a metadata value.
func (b *BaseSession) SetMeta(key string, val any) { b.meta.Store(key, val) }

// GetMeta retrieves a metadata value.
func (b *BaseSession) GetMeta(key string) (any, bool) { return b.meta.Load(key) }

// DelMeta deletes a metadata value.
func (b *BaseSession) DelMeta(key string) { b.meta.Delete(key) }

// DoClose performs the base close logic (CAS to Closed + cancel context).
// Should be called via sync.Once in the protocol-specific Close method.
func (b *BaseSession) DoClose() {
	b.SetState(types.Closed)
	b.cancel()
}
