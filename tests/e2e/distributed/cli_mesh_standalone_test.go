//go:build standalone

package distributed

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

// TestCLIMeshSuite runs the CLI mesh test suite in standalone mode.
// This is excluded by default - use -tags=standalone to run it.
// The orchestrator (TestE2EOrchestrator) runs these tests by default.
func TestCLIMeshSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI mesh tests in short mode")
	}

	suite.Run(t, new(CLIMeshSuite))
}
