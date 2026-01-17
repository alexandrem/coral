package status

import (
	"testing"

	"github.com/coral-mesh/coral/internal/constants"
)

func TestColonyStatusInfo(t *testing.T) {
	// Test that the ColonyStatusInfo struct has expected fields.
	info := ColonyStatusInfo{
		ColonyID:           "test-colony",
		Application:        "test-app",
		Environment:        "prod",
		IsDefault:          true,
		Running:            true,
		Status:             "running",
		UptimeSeconds:      3600,
		AgentCount:         5,
		ActiveAgentCount:   3,
		DegradedAgentCount: 2,
		WireGuardPort:      51820,
		ConnectPort:        constants.DefaultColonyPort,
		LocalEndpoint:      "http://localhost:9000",
		MeshEndpoint:       "http://100.64.0.1:9000",
		MeshIPv4:           "100.64.0.1",
	}

	if info.ColonyID != "test-colony" {
		t.Errorf("ColonyID = %q, want %q", info.ColonyID, "test-colony")
	}

	if !info.Running {
		t.Errorf("Running = %v, want %v", info.Running, true)
	}

	if info.UptimeSeconds != 3600 {
		t.Errorf("UptimeSeconds = %d, want %d", info.UptimeSeconds, 3600)
	}
}
