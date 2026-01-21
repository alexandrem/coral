//go:build standalone

package distributed

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

// TestDiscoveryAuthSuite runs the discovery auth test suite in standalone mode.
// This is excluded by default - use -tags=standalone to run it.
// The orchestrator (TestE2EOrchestrator) runs these tests by default.
func TestDiscoveryAuthSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping discovery auth tests in short mode")
	}

	suite.Run(t, new(DiscoveryAuthSuite))
}
