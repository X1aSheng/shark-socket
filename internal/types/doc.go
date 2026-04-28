// Package types defines the core type contracts for the shark-socket framework.
//
// This package provides the foundational type system that all framework components
// use, ensuring compile-time type safety and runtime interoperability.
//
// # Type Hierarchy
//
//	┌──────────────────────────────────────────────────────────────┐
//	│                      types Package                           │
//	├──────────────────────────────────────────────────────────────┤
//	│  Enumerations   │ ProtocolType, MessageType, SessionState    │
//	├──────────────────────────────────────────────────────────────┤
//	│  Messages      │ Message[T], RawMessage, MessageConstraint  │
//	├──────────────────────────────────────────────────────────────┤
//	│  Sessions      │ Session[M], RawSession, SessionManager      │
//	├──────────────────────────────────────────────────────────────┤
//	│  Handlers      │ MessageHandler[T], RawHandler, Server       │
//	├──────────────────────────────────────────────────────────────┤
//	│  Plugins       │ Plugin, BasePlugin, Priority                │
//	└──────────────────────────────────────────────────────────────┘
//
// # Core Abstractions
//
// ## ProtocolType
//
// Supported protocols (uint8 constants):
//
//	TCP(1) TLS(2) UDP(3) HTTP(4) WebSocket(5) CoAP(6) QUIC(7) Custom(99)
//
// ## SessionState
//
// Session lifecycle states with atomic CAS transitions:
//
//	Connecting(0) → Active(1) → Closing(2) → Closed(3)
//	      │              │
//	      └──────────────┘ (connection failure)
//
// ## Generic Message System
//
// The framework uses generics to provide type safety while maintaining
// interoperability at the transport layer:
//
//	Message[T any] struct {
//	    ID        string           // UUID v7, globally unique
//	    SessionID uint64           // Associated session ID
//	    Protocol  ProtocolType     // Source protocol
//	    Type      MessageType      // Message category
//	    Payload   T                // Generic payload
//	    Timestamp time.Time        // Creation time
//	    Metadata  map[string]any   // Extension metadata
//	}
//
// RawMessage = Message[[]byte] is the most common type alias.
// When T = []byte, all operations are zero-overhead.
//
// # Session Interface
//
//	Session[M MessageConstraint] interface {
//	    // Identity (immutable)
//	    ID() uint64
//	    Protocol() ProtocolType
//	    RemoteAddr() net.Addr
//	    LocalAddr() net.Addr
//	    CreatedAt() time.Time
//
//	    // State (atomic operations)
//	    State() SessionState
//	    IsAlive() bool
//	    LastActiveAt() time.Time
//
//	    // Core sending (two paths, shared writeQueue)
//	    Send(data []byte) error     // Common path: direct bytes
//	    SendTyped(msg M) error      // Type-safe path: encode → Send
//
//	    // Lifecycle
//	    Close() error
//	    Context() context.Context
//
//	    // Metadata (thread-safe KV)
//	    SetMeta(key string, val any)
//	    GetMeta(key string) (any, bool)
//	    DelMeta(key string)
//	}
//
// # SendTyped Path
//
// The SendTyped method provides compile-time type checking:
//
//	SendTyped(msg MyCustomType) error
//	  → encode(msg M) → ([]byte, error)
//	  → Send([]byte)  ← unified write queue
//	  → return encoding error if failed
//
// When M = []byte, SendTyped is semantically identical to Send,
// and the compiler eliminates the redundant encode step.
//
// # SessionManager Interface
//
//	SessionManager interface {
//	    Register(sess RawSession) error
//	    Unregister(id uint64)
//	    Get(id uint64) (RawSession, bool)
//	    Count() int64
//	    All() iter.Seq[RawSession]  // Go 1.26 iterators
//	    Range(fn func(RawSession) bool)
//	    Broadcast(data []byte)
//	    Close() error
//	}
//
// # Plugin Interface
//
//	Plugin interface {
//	    Name() string
//	    Priority() int              // Lower = earlier execution
//	    OnAccept(sess RawSession) error
//	    OnMessage(sess RawSession, data []byte) ([]byte, error)
//	    OnClose(sess RawSession)
//	}
//
// Special control errors (not actual errors):
//
//	ErrSkip  = Skip remaining plugins, continue to handler
//	ErrDrop  = Silently drop the message
//	ErrBlock = Reject the connection
//
// # Design Decisions
//
//  1. Generic Session[M] over concrete interfaces
//     Provides compile-time type safety while allowing runtime polymorphism
//
//  2. Send([]byte) as the universal transport
//     All protocol sessions can exchange raw bytes regardless of message type
//
//  3. RawSession as the canonical type
//     Session[[]byte] is aliased as RawSession for 90% use case simplicity
//
//  4. ProtocolType as uint8
//     Minimizes memory footprint, enables efficient switch statements
//
//  5. Metadata via sync.Map
//     Thread-safe without RWMutex contention on every field access
//
// # Compilation Verification
//
// Each implementation should include compile-time verification:
//
//	var _ types.RawSession = (*TCPSession)(nil)
//	var _ types.SessionManager = (*Manager)(nil)
//	var _ types.Server = (*TCPServer)(nil)
//
// This ensures the implementation satisfies the interface at compile time.
package types
