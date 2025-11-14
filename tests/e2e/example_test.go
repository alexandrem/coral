package e2e

import (
	"testing"

	"github.com/coral-io/coral/tests/helpers"
	"github.com/stretchr/testify/suite"
)

// ExampleE2ESuite is an example test suite showing basic patterns.
// This serves as a template for writing new E2E tests.
type ExampleE2ESuite struct {
	helpers.E2ETestSuite
}

// TestExampleE2E is the entry point for the example test suite.
func TestExampleE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E tests in short mode")
	}
	suite.Run(t, new(ExampleE2ESuite))
}

// TestBasicAssertions demonstrates basic testify assertions.
func (s *ExampleE2ESuite) TestBasicAssertions() {
	// Basic equality
	s.Equal(1, 1, "1 should equal 1")

	// Checking conditions
	s.True(true, "true should be true")
	s.False(false, "false should be false")

	// String checks
	s.Contains("hello world", "world")
	s.NotEmpty("not empty")

	// Numeric checks
	s.Greater(10, 5)
	s.GreaterOrEqual(10, 10)

	s.T().Log("Basic assertions passed")
}

// TestTempDirectory demonstrates using temporary directories.
func (s *ExampleE2ESuite) TestTempDirectory() {
	// Get test-specific directory
	testDir := s.GetTestDataDir("example-test")

	// Directory should exist
	s.DirExists(testDir)

	s.T().Logf("Test directory: %s", testDir)
}

// TestEventuallyPattern demonstrates testing async operations.
func (s *ExampleE2ESuite) TestEventuallyPattern() {
	counter := 0

	// Eventually will retry until condition is true or timeout
	condition := func() bool {
		counter++
		return counter >= 3
	}

	s.Eventually(condition, s.TestTimeout, 100, "Counter should reach 3")
	s.Equal(3, counter)

	s.T().Log("Eventually pattern worked correctly")
}

// TestContextUsage demonstrates context usage in tests.
func (s *ExampleE2ESuite) TestContextUsage() {
	// Each test gets a context with timeout
	s.Require().NotNil(s.Ctx)

	// Context should not be cancelled initially
	select {
	case <-s.Ctx.Done():
		s.Fail("Context should not be cancelled")
	default:
		// Expected - context is still active
	}

	s.T().Log("Context is available and active")
}

// TestPortAllocation demonstrates getting free ports for services.
func (s *ExampleE2ESuite) TestPortAllocation() {
	port1 := s.GetFreePort()
	port2 := s.GetFreePort()

	// Ports should be different
	s.NotEqual(port1, port2, "Should get different ports")

	// Ports should be valid
	s.Greater(port1, 0)
	s.Greater(port2, 0)

	s.T().Logf("Allocated ports: %d, %d", port1, port2)
}
