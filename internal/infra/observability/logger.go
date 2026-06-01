package observability

import (
	"sync"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type LogEntry struct {
	Level string
	Msg   string
	Attrs []any
}

type MemoryLogger struct {
	mu      sync.Mutex
	entries []LogEntry
}

func NewMemoryLogger() *MemoryLogger {
	return &MemoryLogger{}
}

func (l *MemoryLogger) Debug(msg string, attrs ...any) { l.append("debug", msg, attrs...) }
func (l *MemoryLogger) Info(msg string, attrs ...any)  { l.append("info", msg, attrs...) }
func (l *MemoryLogger) Warn(msg string, attrs ...any)  { l.append("warn", msg, attrs...) }
func (l *MemoryLogger) Error(msg string, attrs ...any) { l.append("error", msg, attrs...) }

func (l *MemoryLogger) Entries() []LogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]LogEntry(nil), l.entries...)
}

func (l *MemoryLogger) append(level, msg string, attrs ...any) {
	l.mu.Lock()
	l.entries = append(l.entries, LogEntry{Level: level, Msg: msg, Attrs: append([]any(nil), attrs...)})
	l.mu.Unlock()
}

var _ core.Logger = (*MemoryLogger)(nil)
