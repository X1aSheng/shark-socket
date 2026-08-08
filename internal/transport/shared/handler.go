package shared

import (
	"github.com/X1aSheng/shark-socket/internal/core"
)

// CallHandler invokes a user handler/responder with panic recovery. A
// panicking user handler would otherwise crash the whole process, because Go
// has no per-goroutine recovery and the transport goroutines (worker, read
// loop, stream) invoke handlers directly. Instead the panic is logged and a
// non-nil error is returned, so the transport treats it like a failing handler
// (e.g. closes the session) — matching the failure-isolation design principle.
func CallHandler(fn func() error, logger core.Logger) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if logger != nil {
				logger.Error("user handler panic", "panic", r)
			}
			err = core.ErrHandlerPanic
		}
	}()
	return fn()
}
