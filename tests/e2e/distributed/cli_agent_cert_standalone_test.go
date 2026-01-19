//go:build standalone

package distributed

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

// TestCLIAgentCertSuite runs the agent certificate CLI test suite in standalone mode.
// This is excluded by default - use -tags=standalone to run it.
// The orchestrator (TestE2EOrchestrator) runs these tests by default.
func TestCLIAgentCertSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping agent cert tests in short mode")
	}

	suite.Run(t, new(CLIAgentCertSuite))
}
