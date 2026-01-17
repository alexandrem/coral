package distributed

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

func TestPublicEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E tests in short mode")
	}

	suite.Run(t, new(PublicEndpointSuite))
}
