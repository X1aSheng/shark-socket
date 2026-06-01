package plugin

import (
	"time"

	"github.com/X1aSheng/shark-socket/internal/core"
)

type SlowHandlerOption func(*slowHandlerOptions)

type slowHandlerOptions struct {
	now func() time.Time
}

func WithSlowHandlerClock(now func() time.Time) SlowHandlerOption {
	return func(o *slowHandlerOptions) {
		if now != nil {
			o.now = now
		}
	}
}

func NewSlowHandler(logger core.Logger, threshold time.Duration, next core.Handler, opts ...SlowHandlerOption) core.Handler {
	if threshold < 0 {
		threshold = 0
	}
	cfg := slowHandlerOptions{now: time.Now}
	for _, opt := range opts {
		opt(&cfg)
	}
	return func(sess core.Session, msg core.Message) error {
		started := cfg.now()
		err := next(sess, msg)
		elapsed := cfg.now().Sub(started)
		if logger != nil && elapsed >= threshold {
			attrs := []any{
				"session_id", msg.SessionID,
				"protocol", msg.Protocol,
				"duration_ms", float64(elapsed.Microseconds()) / 1000,
				"payload_bytes", len(msg.Payload),
			}
			if err != nil {
				attrs = append(attrs, "error", err.Error())
			}
			logger.Warn("slow handler", attrs...)
		}
		return err
	}
}
