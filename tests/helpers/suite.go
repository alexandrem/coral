// Package helpers provides common utilities for E2E tests.
package helpers

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

// E2ETestSuite is the base suite for all E2E tests.
// It provides common setup, teardown, and utility methods.
type E2ETestSuite struct {
	suite.Suite
	TempDir     string
	Ctx         context.Context
	Cancel      context.CancelFunc
	StartTime   time.Time
	TestTimeout time.Duration
}

// SetupSuite runs once before all tests in the suite.
func (s *E2ETestSuite) SetupSuite() {
	s.T().Log("Setting up E2E test suite")
	s.StartTime = time.Now()

	// Set default test timeout
	if s.TestTimeout == 0 {
		s.TestTimeout = 5 * time.Minute
	}

	// Create temporary directory for test artifacts
	tempDir, err := os.MkdirTemp("", "coral-e2e-*")
	s.Require().NoError(err, "Failed to create temp directory")
	s.TempDir = tempDir
	s.T().Logf("Test temp directory: %s", s.TempDir)
}

// TearDownSuite runs once after all tests in the suite.
func (s *E2ETestSuite) TearDownSuite() {
	s.T().Log("Tearing down E2E test suite")

	// Clean up temporary directory
	if s.TempDir != "" {
		if err := os.RemoveAll(s.TempDir); err != nil {
			s.T().Logf("Warning: Failed to remove temp directory %s: %v", s.TempDir, err)
		}
	}

	elapsed := time.Since(s.StartTime)
	s.T().Logf("Suite completed in %s", elapsed)
}

// SetupTest runs before each test.
func (s *E2ETestSuite) SetupTest() {
	// Create a context with timeout for each test
	s.Ctx, s.Cancel = context.WithTimeout(context.Background(), s.TestTimeout)
	s.T().Logf("Starting test: %s", s.T().Name())
}

// TearDownTest runs after each test.
func (s *E2ETestSuite) TearDownTest() {
	if s.Cancel != nil {
		s.Cancel()
	}
	s.T().Logf("Completed test: %s", s.T().Name())
}

// GetTestDataDir returns a test-specific directory.
func (s *E2ETestSuite) GetTestDataDir(testName string) string {
	dir := filepath.Join(s.TempDir, testName)
	err := os.MkdirAll(dir, 0755)
	s.Require().NoError(err, "Failed to create test data directory")
	return dir
}

// Eventually retries a condition until it returns true or times out.
// This is useful for waiting for async operations to complete.
func (s *E2ETestSuite) Eventually(condition func() bool, timeout time.Duration, tick time.Duration, msgAndArgs ...interface{}) {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	for {
		select {
		case <-s.Ctx.Done():
			s.Fail("Context cancelled while waiting for condition", msgAndArgs...)
			return
		case <-ticker.C:
			if condition() {
				return
			}
			if time.Now().After(deadline) {
				s.Fail(fmt.Sprintf("Condition not met within %s", timeout), msgAndArgs...)
				return
			}
		}
	}
}

// GetFreePort finds and returns a free TCP port.
func (s *E2ETestSuite) GetFreePort() int {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	s.Require().NoError(err, "Failed to get free port")
	defer listener.Close()

	addr := listener.Addr().(*net.TCPAddr)
	return addr.Port
}

// WaitForPort waits for a TCP port to become available (listening).
func (s *E2ETestSuite) WaitForPort(host string, port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	tick := 100 * time.Millisecond

	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), tick)
		if err == nil {
			conn.Close()
			return true
		}
		time.Sleep(tick)
	}
	return false
}

// AssertEventually is like Eventually but fails the test if condition is not met.
func (s *E2ETestSuite) AssertEventually(condition func() bool, timeout time.Duration, msgAndArgs ...interface{}) {
	s.Eventually(condition, timeout, 100*time.Millisecond, msgAndArgs...)
}

// RequireEventually is like AssertEventually but stops test execution on failure.
func (s *E2ETestSuite) RequireEventually(condition func() bool, timeout time.Duration, msgAndArgs ...interface{}) {
	s.Eventually(condition, timeout, 100*time.Millisecond, msgAndArgs...)
}
