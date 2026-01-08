//go:build standalone

package distributed

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

// TestInitSuite runs the initialization test suite in standalone mode.
// This is excluded by default - use -tags=standalone to run it.
// The orchestrator (TestE2EOrchestrator) runs these tests by default.
func TestInitSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping init tests in short mode")
	}

	suite.Run(t, new(InitSuite))
}
