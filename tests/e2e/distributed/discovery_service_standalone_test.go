//go:build e2e_standalone
// +build e2e_standalone

package distributed

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

// TestDiscoveryServiceSuite runs the consolidated discovery service tests standalone.
// Run with: go test -tags e2e_standalone -run TestDiscoveryServiceSuite ./tests/e2e/distributed/...
//
// To test against a deployed Workers service, set:
//
//	CORAL_WORKERS_DISCOVERY_ENDPOINT=https://discovery.coralmesh.workers.dev
//
// Without the environment variable, tests run against the local docker-compose discovery.
func TestDiscoveryServiceSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	suite.Run(t, new(DiscoveryServiceSuite))
}
