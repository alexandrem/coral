package distributed

import (
	"context"
	"runtime"

	"github.com/stretchr/testify/suite"

	"github.com/coral-mesh/coral/tests/e2e/distributed/fixtures"
)

// E2EDistributedSuite is the base test suite for distributed E2E tests.
type E2EDistributedSuite struct {
	suite.Suite

	ctx     context.Context
	fixture *fixtures.ContainerFixture
}

// SetupSuite runs once before all tests in the suite.
func (s *E2EDistributedSuite) SetupSuite() {
	// Check platform requirements.
	if runtime.GOOS != "linux" {
		s.T().Skip("E2E distributed tests require Linux for eBPF and WireGuard")
	}

	// TODO: Check Docker availability.
	// TODO: Check kernel features (eBPF, WireGuard support).

	s.T().Log("E2E distributed suite setup complete")
}

// TearDownSuite runs once after all tests in the suite.
func (s *E2EDistributedSuite) TearDownSuite() {
	s.T().Log("E2E distributed suite teardown complete")
}

// SetupTest runs before each test.
func (s *E2EDistributedSuite) SetupTest() {
	s.ctx = context.Background()

	s.T().Log("Setting up test environment...")

	// Create fresh container fixture for each test.
	fixture, err := fixtures.NewContainerFixture(s.ctx, fixtures.FixtureOptions{
		NumAgents: 1,
		// ColonyID and Secret will be auto-generated with unique values.
	})
	s.Require().NoError(err, "Failed to create container fixture")
	s.fixture = fixture

	s.T().Log("Test environment ready")
}

// TearDownTest runs after each test.
func (s *E2EDistributedSuite) TearDownTest() {
	if s.fixture != nil {
		s.T().Log("Cleaning up test environment...")
		err := s.fixture.Cleanup(s.ctx)
		if err != nil {
			s.T().Logf("Warning: cleanup failed: %v", err)
		}
		s.fixture = nil
	}
}
