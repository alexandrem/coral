//go:build standalone

package distributed

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

// TestCLIAskSuite runs the CLI ask test suite in standalone mode.
// This is excluded by default - use -tags=standalone to run it.
func TestCLIAskSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping CLI ask tests in short mode")
	}

	suite.Run(t, new(CLIAskSuite))
}
