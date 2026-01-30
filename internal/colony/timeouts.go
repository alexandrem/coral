package colony

import "time"

const (

	// serviceQueryTimeout is for standard service discovery queries.
	serviceQueryTimeout = 5 * time.Second

	// agentQueryTimeout is for agent data collection (telemetry, metrics, CPU profiles).
	agentQueryTimeout = 10 * time.Second

	// rpcCallTimeout is for longer RPC calls (GetFunctions, event persistence).
	rpcCallTimeout = 30 * time.Second
)
