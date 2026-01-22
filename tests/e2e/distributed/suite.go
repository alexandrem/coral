package distributed

import (
	"context"

	"github.com/stretchr/testify/suite"

	"github.com/coral-mesh/coral/tests/e2e/distributed/fixtures"
)

// E2EDistributedSuite is the base test suite for distributed E2E tests.
// This suite uses docker-compose for infrastructure instead of testcontainers.
// All services (discovery, colony, agents, apps) are started once via docker-compose
// and shared across all tests for speed.
type E2EDistributedSuite struct {
	suite.Suite

	ctx     context.Context
	fixture *fixtures.ComposeFixture
}

// SetupSuite runs once before all tests in the suite.
func (s *E2EDistributedSuite) SetupSuite() {
	s.ctx = context.Background()

	if s.fixture != nil {
		s.T().Log("Using existing docker-compose services (reusing fixture)")
		return
	}

	s.T().Log("Connecting to docker-compose services...")
	s.T().Log("(Make sure to run 'docker-compose up -d' in tests/e2e/distributed/ first)")
	s.T().Log("Note: Services run in Linux containers (eBPF/WireGuard), test runner can be on any platform")

	// Connect to running docker-compose services.
	fixture, err := fixtures.NewComposeFixture(s.ctx)
	s.Require().NoError(err, "Failed to connect to docker-compose services")
	s.fixture = fixture

	// Create .env file for the CLI to talk to the colony in the container (RFD 031/048).
	err = s.fixture.CreateDotEnvFile(s.ctx)
	s.Require().NoError(err, "Failed to create .env file")

	s.T().Log("✓ All services connected and healthy")
	s.T().Log("✓ .env file created for CLI access")
	s.T().Log("E2E distributed suite setup complete")
}

// TearDownSuite runs once after all tests in the suite.
func (s *E2EDistributedSuite) TearDownSuite() {
	if s.fixture != nil {
		_ = s.fixture.Cleanup(s.ctx)
		s.fixture = nil
	}
	s.T().Log("E2E distributed suite teardown complete")
}

// SetupTest runs before each test.
func (s *E2EDistributedSuite) SetupTest() {
	s.T().Log("Starting test (using shared docker-compose services)...")
	// Note: We don't create containers per-test anymore.
	// All tests share the same docker-compose infrastructure.
	// If tests need isolation, they should clean up their own state.
}

// TearDownTest runs after each test.
func (s *E2EDistributedSuite) TearDownTest() {
	// Note: We don't tear down containers per-test.
	// The docker-compose services stay running for the entire test suite.
	// Tests should clean up any state they created (e.g., services, data).
	s.T().Log("Test complete (services remain running for next test)")
}
