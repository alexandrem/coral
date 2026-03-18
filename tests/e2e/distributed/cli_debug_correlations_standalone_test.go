//go:build standalone

package distributed

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

// TestCLIDebugCorrelationsSuite runs the CLI debug correlations test suite in standalone mode.
// This is excluded by default - use -tags=standalone to run it.
// The orchestrator (TestE2EOrchestrator) runs these tests by default.
func TestCLIDebugCorrelationsSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI debug correlations tests in short mode")
	}

	suite.Run(t, new(CLIDebugCorrelationsSuite))
}
