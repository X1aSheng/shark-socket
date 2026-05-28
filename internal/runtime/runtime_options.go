package runtime

import "github.com/X1aSheng/shark-socket-new/internal/core"

type RuntimeOption func(*Runtime)

func withRuntimeLogger(logger core.Logger) RuntimeOption {
	return func(r *Runtime) {
		if logger != nil {
			r.logger = logger
		}
	}
}

func withRuntimeMetrics(metrics core.Metrics) RuntimeOption {
	return func(r *Runtime) {
		if metrics != nil {
			r.metrics = metrics
		}
	}
}

func withRuntimeTracer(tracer core.Tracer) RuntimeOption {
	return func(r *Runtime) {
		if tracer != nil {
			r.tracer = tracer
		}
	}
}
