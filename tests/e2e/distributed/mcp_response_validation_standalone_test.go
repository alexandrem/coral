//go:build standalone

package distributed

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

// TestResponseValidationSuite runs the MCP response validation test suite in standalone mode.
// This is excluded by default - use -tags=standalone to run it.
// The orchestrator (TestE2EOrchestrator) runs these tests by default.
func TestResponseValidationSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping MCP response validation tests in short mode")
	}

	suite.Run(t, new(ResponseValidationSuite))
}
