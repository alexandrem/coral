//go:build standalone

package distributed

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

// TestCLIAskDispatchSuite runs the ask dispatch-mode bootstrap parity suite
// in standalone mode. This is excluded by default - use -tags=standalone to
// run it. The orchestrator (TestE2EOrchestrator) runs these tests by default.
func TestCLIAskDispatchSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI ask dispatch tests in short mode")
	}

	suite.Run(t, new(CLIAskDispatchSuite))
}
