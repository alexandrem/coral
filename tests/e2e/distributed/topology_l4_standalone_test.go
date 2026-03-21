//go:build standalone

package distributed

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

// TestL4TopologySuite runs the L4 topology test suite in standalone mode.
// This is excluded by default - use -tags=standalone to run it.
// The orchestrator (TestE2EOrchestrator) runs these tests by default.
func TestL4TopologySuite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping L4 topology tests in short mode")
	}

	suite.Run(t, new(L4TopologySuite))
}
