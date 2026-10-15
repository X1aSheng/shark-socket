package tcp

import "github.com/X1aSheng/shark-socket/internal/transport/shared"

// The queue-full policy is implemented once in the shared worker pool and
// re-exported here so the TCP API (Options.FullPolicy, WithWorkers) keeps its
// established names.
type FullPolicy = shared.FullPolicy

const (
	PolicyBlock = shared.PolicyBlock
	PolicyDrop  = shared.PolicyDrop
	PolicyClose = shared.PolicyClose
)
