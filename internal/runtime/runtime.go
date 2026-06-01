package runtime

import "github.com/X1aSheng/shark-socket/internal/core"

type Runtime struct {
	sessions core.SessionManager
	plugins  core.PluginRunner
	logger   core.Logger
	metrics  core.Metrics
	tracer   core.Tracer
}

func NewRuntime(sessions core.SessionManager, plugins core.PluginRunner, opts ...RuntimeOption) *Runtime {
	if sessions == nil {
		sessions = NewSessionManager()
	}
	if plugins == nil {
		plugins = NewPluginChain()
	}
	r := &Runtime{
		sessions: sessions,
		plugins:  plugins,
		logger:   core.NopLogger(),
		metrics:  core.NopMetrics(),
		tracer:   core.NopTracer(),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func (r *Runtime) Sessions() core.SessionManager { return r.sessions }
func (r *Runtime) Plugins() core.PluginRunner    { return r.plugins }
func (r *Runtime) Logger() core.Logger           { return r.logger }
func (r *Runtime) Metrics() core.Metrics         { return r.metrics }
func (r *Runtime) Tracer() core.Tracer           { return r.tracer }

var _ core.Runtime = (*Runtime)(nil)
