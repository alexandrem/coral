package agent

import (
	discoverypb "github.com/coral-mesh/coral/coral/discovery/v1"
	"github.com/coral-mesh/coral/internal/agent"
	"github.com/coral-mesh/coral/internal/cli/agent/startup"
	"github.com/coral-mesh/coral/internal/cli/agent/types"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/logging"
	"github.com/coral-mesh/coral/internal/wireguard"
)

// ConnectionManager is a re-export from the startup package for backwards compatibility.
type ConnectionManager = startup.ConnectionManager

// NewConnectionManager creates a new connection manager for agent-colony communication.
// This is a re-export from the startup package for backwards compatibility.
func NewConnectionManager(
	agentID string,
	colonyInfo *discoverypb.LookupColonyResponse,
	cfg *config.ResolvedConfig,
	serviceSpecs []*types.ServiceSpec,
	agentPubKey string,
	wgDevice *wireguard.Device,
	runtimeService *agent.RuntimeService,
	logger logging.Logger,
) *startup.ConnectionManager {
	return startup.NewConnectionManager(agentID, colonyInfo, cfg, serviceSpecs, agentPubKey, wgDevice, runtimeService, logger)
}
