//go:build standalone

package distributed

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

// TestCLIConfigSuite runs the CLI config test suite in standalone mode.
// This is excluded by default - use -tags=standalone to run it.
// The orchestrator (TestE2EOrchestrator) runs these tests by default.
func TestCLIConfigSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI config tests in short mode")
	}

	suite.Run(t, new(CLIConfigSuite))
}
