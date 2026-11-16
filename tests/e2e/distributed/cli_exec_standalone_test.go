//go:build standalone

package distributed

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

// TestCLIExecSuite runs just the CLI exec tests with -tags=standalone.
// The orchestrator includes these tests by default.
func TestCLIExecSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI exec tests in short mode")
	}

	suite.Run(t, new(CLIExecSuite))
}
