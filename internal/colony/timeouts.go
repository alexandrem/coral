package colony

import (
	"github.com/coral-mesh/coral/internal/constants"
)

const (
	// serviceQueryTimeout is for querying service metadata.
	serviceQueryTimeout = constants.DefaultColonyServiceQueryTimeout

	// agentQueryTimeout is for polling agent data.
	agentQueryTimeout = constants.DefaultColonyAgentQueryTimeout

	// rpcCallTimeout is for general colony RPC operations.
	rpcCallTimeout = constants.DefaultColonyRPCCallTimeout
)
