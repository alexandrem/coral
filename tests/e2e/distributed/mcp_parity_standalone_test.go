//go:build standalone

package distributed

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

// TestMCPParitySuite runs the MCP parity test suite in standalone mode.
// This is excluded by default - use -tags=standalone to run it.
// The orchestrator (TestE2EOrchestrator) runs these tests by default.
func TestMCPParitySuite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping MCP parity tests in short mode")
	}

	suite.Run(t, new(MCPParitySuite))
}
