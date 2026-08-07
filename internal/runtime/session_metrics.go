package runtime

import (
	"context"

	"github.com/X1aSheng/shark-socket/internal/core"
)

// metricSessionManager decorates a SessionManager and emits session lifecycle
// metrics through the configured core.Metrics backend. NewGateway installs it
// so every transport reports session counts to the Prometheus exporter (or
// whatever backend is configured) without each server needing its own metric
// calls. Metric names follow the Prometheus *_total / gauge convention.
type metricSessionManager struct {
	core.SessionManager
	metrics core.Metrics
}

func (m *metricSessionManager) Register(sess core.Session) error {
	if err := m.SessionManager.Register(sess); err != nil {
		return err
	}
	m.metrics.IncCounter("sessions_accepted_total")
	m.metrics.SetGauge("sessions_active", float64(m.Count()))
	return nil
}

func (m *metricSessionManager) Unregister(id uint64) {
	m.SessionManager.Unregister(id)
	m.metrics.IncCounter("sessions_closed_total")
	m.metrics.SetGauge("sessions_active", float64(m.Count()))
}

// CloseAll mirrors SessionManager.CloseAll but routes each unregister through
// the metric-emitting Unregister, so batch shutdown is counted per session.
func (m *metricSessionManager) CloseAll(ctx context.Context) error {
	sessions := m.Snapshot()
	var firstErr error
	for _, sess := range sessions {
		if err := sess.Close(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
		m.Unregister(sess.ID())
	}
	return firstErr
}

var _ core.SessionManager = (*metricSessionManager)(nil)
