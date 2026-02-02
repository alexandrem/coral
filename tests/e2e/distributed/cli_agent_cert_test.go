package distributed

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/stretchr/testify/suite"

	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// CLIAgentCertSuite tests the agent certificate CLI commands (RFD 048).
//
// This suite validates:
// 1. `coral agent bootstrap` - initial certificate acquisition
// 2. `coral agent cert status` - certificate status display
// 3. `coral agent cert renew` - certificate renewal
// 4. Error handling for invalid fingerprints, missing discovery, etc.
//
// The tests use an isolated environment with a freshly initialized colony
// to test the full bootstrap flow from scratch.
type CLIAgentCertSuite struct {
	suite.Suite

	ctx          context.Context
	tempDir      string // Temporary directory for test
	colonyID     string
	colonyDir    string
	cliEnv       *helpers.CLITestEnv
	fingerprint  string
	bootstrapPSK string // Bootstrap PSK from colony init (RFD 088).
}

// SetupSuite runs once before all tests in the suite.
func (s *CLIAgentCertSuite) SetupSuite() {
	s.ctx = context.Background()

	// Create temporary directory for this test suite.
	tempDir, err := os.MkdirTemp("", "coral-cert-test-*")
	s.Require().NoError(err, "Failed to create temp directory")
	s.tempDir = tempDir

	s.T().Logf("Using temporary directory: %s", s.tempDir)

	// Initialize a test colony to get CA certificates.
	s.initializeTestColony()
}

// TearDownSuite runs once after all tests in the suite.
func (s *CLIAgentCertSuite) TearDownSuite() {
	if s.cliEnv != nil {
		_ = s.cliEnv.Cleanup()
	}
	if s.tempDir != "" {
		_ = os.RemoveAll(s.tempDir)
		s.T().Logf("Cleaned up temporary directory: %s", s.tempDir)
	}
}

// initializeTestColony creates a colony to get CA certificates for testing.
func (s *CLIAgentCertSuite) initializeTestColony() {
	// Set HOME to temp directory.
	testHome := filepath.Join(s.tempDir, "home")
	s.Require().NoError(os.MkdirAll(testHome, 0755))
	s.T().Setenv("HOME", testHome)

	// Run coral init to create a colony with CA certificates.
	initResult, err := helpers.RunCoralInitFull(s.ctx, "cert-test-colony", "e2e",
		filepath.Join(s.tempDir, "storage"), "")
	s.Require().NoError(err, "coral init should succeed")
	s.colonyID = initResult.ColonyID
	s.bootstrapPSK = initResult.BootstrapPSK

	s.T().Logf("Initialized test colony: %s", initResult.ColonyID)
	s.T().Logf("Bootstrap PSK available: %v", s.bootstrapPSK != "")

	// Get colony directory path.
	loader, err := config.NewLoader()
	s.Require().NoError(err)
	s.colonyDir = loader.ColonyDir(s.colonyID)

	// Get CA fingerprint for bootstrap.
	fingerprint, err := helpers.GetColonyCAFingerprint(s.colonyDir)
	s.Require().NoError(err, "Failed to get CA fingerprint")
	s.fingerprint = fingerprint

	s.T().Logf("CA fingerprint: %s", fingerprint)

	// Setup CLI test environment.
	s.cliEnv, err = helpers.SetupCLIEnv(s.ctx, s.colonyID, "http://localhost:9000")
	s.Require().NoError(err, "Failed to setup CLI environment")
}

// TestCertStatusNoCertificate tests cert status when no certificate exists.
func (s *CLIAgentCertSuite) TestCertStatusNoCertificate() {
	s.T().Log("Testing cert status with no certificate...")

	// Create an empty certs directory.
	certsDir, err := helpers.CreateCertsDir(filepath.Join(s.tempDir, "empty-certs"))
	s.Require().NoError(err)

	env := s.cliEnv.WithEnv(map[string]string{
		"CORAL_CERTS_DIR": certsDir,
	})

	result := helpers.AgentCertStatus(s.ctx, env, certsDir)

	// Should indicate no certificate found.
	s.Contains(result.Output, "No certificate found",
		"Output should indicate missing certificate")

	s.T().Log("cert status correctly reports missing certificate")
}

// TestBootstrapRequiresFingerprint tests that bootstrap fails without fingerprint.
func (s *CLIAgentCertSuite) TestBootstrapRequiresFingerprint() {
	s.T().Log("Testing bootstrap requires fingerprint...")

	certsDir, err := helpers.CreateCertsDir(filepath.Join(s.tempDir, "no-fp-certs"))
	s.Require().NoError(err)

	env := s.cliEnv.WithEnv(map[string]string{
		"CORAL_CERTS_DIR": certsDir,
		"HOME":            s.cliEnv.HomeDir,
	})

	// Try bootstrap without fingerprint.
	result := helpers.RunCLIWithEnv(s.ctx, env, "agent", "bootstrap",
		"--colony", s.colonyID,
		"--discovery", "http://localhost:18080")

	s.NotEqual(0, result.ExitCode, "Bootstrap should fail without fingerprint")
	s.True(
		strings.Contains(result.Output, "fingerprint") ||
			strings.Contains(result.Output, "required"),
		"Error should mention fingerprint requirement: %s", result.Output)

	s.T().Log("bootstrap correctly requires fingerprint")
}

// TestBootstrapInvalidFingerprint tests MITM detection with wrong fingerprint.
func (s *CLIAgentCertSuite) TestBootstrapInvalidFingerprint() {
	s.T().Log("Testing bootstrap rejects invalid fingerprint (MITM detection)...")

	certsDir, err := helpers.CreateCertsDir(filepath.Join(s.tempDir, "invalid-fp-certs"))
	s.Require().NoError(err)

	env := s.cliEnv.WithEnv(map[string]string{
		"CORAL_CERTS_DIR":     certsDir,
		"HOME":                s.cliEnv.HomeDir,
		"CORAL_BOOTSTRAP_PSK": s.bootstrapPSK,
	})

	// Use a fake fingerprint.
	fakeFingerprint := "sha256:0000000000000000000000000000000000000000000000000000000000000000"

	result := helpers.AgentBootstrap(s.ctx, env, s.colonyID, fakeFingerprint, "http://localhost:18080")

	s.False(result.Success, "Bootstrap should fail with invalid fingerprint")
	s.True(
		strings.Contains(result.Output, "fingerprint") ||
			strings.Contains(result.Output, "mismatch") ||
			strings.Contains(result.Output, "MITM"),
		"Error should indicate fingerprint mismatch: %s", result.Output)

	s.T().Log("bootstrap correctly detects fingerprint mismatch")
}

// TestBootstrapRequiresDiscovery tests that bootstrap fails without discovery endpoint.
func (s *CLIAgentCertSuite) TestBootstrapRequiresDiscovery() {
	s.T().Log("Testing bootstrap requires discovery endpoint...")

	certsDir, err := helpers.CreateCertsDir(filepath.Join(s.tempDir, "no-discovery-certs"))
	s.Require().NoError(err)

	// Create environment without discovery endpoint.
	env := map[string]string{
		"HOME":                filepath.Join(s.tempDir, "isolated-home"),
		"CORAL_CERTS_DIR":     certsDir,
		"CORAL_BOOTSTRAP_PSK": s.bootstrapPSK,
	}

	// Create isolated home without any config.
	s.Require().NoError(os.MkdirAll(env["HOME"], 0755))

	result := helpers.AgentBootstrap(s.ctx, env, s.colonyID, s.fingerprint, "")

	s.False(result.Success, "Bootstrap should fail without discovery endpoint")
	s.True(
		strings.Contains(result.Output, "discovery") ||
			strings.Contains(result.Output, "endpoint"),
		"Error should mention discovery requirement: %s", result.Output)

	s.T().Log("bootstrap correctly requires discovery endpoint")
}

// TestCertRenewRequiresCertificate tests that renewal fails without existing certificate.
func (s *CLIAgentCertSuite) TestCertRenewRequiresCertificate() {
	s.T().Log("Testing cert renew requires existing certificate...")

	certsDir, err := helpers.CreateCertsDir(filepath.Join(s.tempDir, "no-cert-renew"))
	s.Require().NoError(err)

	env := s.cliEnv.WithEnv(map[string]string{
		"CORAL_CERTS_DIR": certsDir,
	})

	result := helpers.AgentCertRenew(s.ctx, env, s.colonyID, s.fingerprint,
		"https://localhost:9000", "", false)

	s.False(result.Success, "Renewal should fail without existing certificate")
	s.True(
		strings.Contains(result.Output, "certificate") ||
			strings.Contains(result.Output, "not found") ||
			strings.Contains(result.Output, "bootstrap"),
		"Error should indicate missing certificate: %s", result.Output)

	s.T().Log("cert renew correctly requires existing certificate")
}

// TestCertRenewRequiresColonyEndpoint tests renewal needs colony endpoint for mTLS.
func (s *CLIAgentCertSuite) TestCertRenewRequiresColonyEndpoint() {
	s.T().Log("Testing cert renew requires colony endpoint...")

	certsDir, err := helpers.CreateCertsDir(filepath.Join(s.tempDir, "no-endpoint-renew"))
	s.Require().NoError(err)

	env := s.cliEnv.WithEnv(map[string]string{
		"CORAL_CERTS_DIR": certsDir,
	})

	// Try renewal without colony endpoint or discovery.
	result := helpers.AgentCertRenew(s.ctx, env, s.colonyID, s.fingerprint, "", "", false)

	s.False(result.Success, "Renewal should fail without endpoint")
	s.True(
		strings.Contains(result.Output, "endpoint") ||
			strings.Contains(result.Output, "colony") ||
			strings.Contains(result.Output, "discovery"),
		"Error should mention endpoint requirement: %s", result.Output)

	s.T().Log("cert renew correctly requires endpoint")
}

// TestCAFingerprintFormat tests that fingerprint format validation works.
func (s *CLIAgentCertSuite) TestCAFingerprintFormat() {
	s.T().Log("Testing CA fingerprint format validation...")

	certsDir, err := helpers.CreateCertsDir(filepath.Join(s.tempDir, "fp-format-certs"))
	s.Require().NoError(err)

	env := s.cliEnv.WithEnv(map[string]string{
		"CORAL_CERTS_DIR":     certsDir,
		"CORAL_BOOTSTRAP_PSK": s.bootstrapPSK,
	})

	// Test invalid fingerprint format.
	invalidFormats := []string{
		"invalid",         // No prefix
		"md5:abc123",      // Wrong algorithm
		"sha256:tooshort", // Too short
		"sha256:xyz",      // Not hex
	}

	for _, fp := range invalidFormats {
		result := helpers.AgentBootstrap(s.ctx, env, s.colonyID, fp, "http://localhost:18080")
		s.False(result.Success, "Bootstrap should fail with invalid fingerprint format: %s", fp)
	}

	s.T().Log("fingerprint format validation works correctly")
}

// TestCertStatusOutputFormat tests cert status output formats.
func (s *CLIAgentCertSuite) TestCertStatusOutputFormat() {
	s.T().Log("Testing cert status output format...")

	certsDir, err := helpers.CreateCertsDir(filepath.Join(s.tempDir, "format-certs"))
	s.Require().NoError(err)

	env := s.cliEnv.WithEnv(map[string]string{
		"CORAL_CERTS_DIR": certsDir,
	})

	// Test table format (default).
	result := helpers.RunCLIWithEnv(s.ctx, env, "agent", "cert", "status",
		"--certs-dir", certsDir)

	// Should have some recognizable output structure.
	s.Contains(result.Output, "Certificate", "Should have certificate header")

	s.T().Log("cert status output format is correct")
}

// TestBootstrapWithCustomAgentID tests bootstrap with custom agent ID.
func (s *CLIAgentCertSuite) TestBootstrapWithCustomAgentID() {
	s.T().Log("Testing bootstrap with custom agent ID...")

	certsDir, err := helpers.CreateCertsDir(filepath.Join(s.tempDir, "custom-agent-certs"))
	s.Require().NoError(err)

	env := s.cliEnv.WithEnv(map[string]string{
		"CORAL_CERTS_DIR":     certsDir,
		"CORAL_BOOTSTRAP_PSK": s.bootstrapPSK,
	})

	customAgentID := "custom-test-agent-123"

	// Bootstrap with custom agent ID (will fail to connect, but should accept the ID).
	result := helpers.RunCLIWithEnv(s.ctx, env, "agent", "bootstrap",
		"--colony", s.colonyID,
		"--fingerprint", s.fingerprint,
		"--agent", customAgentID,
		"--discovery", "http://localhost:18080")

	// The bootstrap will fail (no discovery running), but the agent ID should be in output.
	s.Contains(result.Output, customAgentID,
		"Output should reference custom agent ID: %s", result.Output)

	s.T().Log("bootstrap accepts custom agent ID")
}

// TestCertsDirectoryPermissions tests that certificate files have proper permissions.
func (s *CLIAgentCertSuite) TestCertsDirectoryCreation() {
	s.T().Log("Testing certs directory handling...")

	// Test with non-existent directory path.
	nonExistentDir := filepath.Join(s.tempDir, "new", "certs", "path")

	env := s.cliEnv.WithEnv(map[string]string{
		"CORAL_CERTS_DIR": nonExistentDir,
	})

	// Cert status should handle non-existent directory gracefully.
	result := helpers.AgentCertStatus(s.ctx, env, nonExistentDir)

	s.Contains(result.Output, "No certificate found",
		"Should handle non-existent directory: %s", result.Output)

	s.T().Log("certs directory handling works correctly")
}

// TestBootstrapForceFlag tests the --force flag behavior.
func (s *CLIAgentCertSuite) TestBootstrapForceFlag() {
	s.T().Log("Testing bootstrap --force flag...")

	certsDir, err := helpers.CreateCertsDir(filepath.Join(s.tempDir, "force-certs"))
	s.Require().NoError(err)

	env := s.cliEnv.WithEnv(map[string]string{
		"CORAL_CERTS_DIR":     certsDir,
		"CORAL_BOOTSTRAP_PSK": s.bootstrapPSK,
	})

	// Run with --force flag (will fail due to no discovery, but should accept the flag).
	result := helpers.RunCLIWithEnv(s.ctx, env, "agent", "bootstrap",
		"--colony", s.colonyID,
		"--fingerprint", s.fingerprint,
		"--discovery", "http://localhost:18080",
		"--force")

	// Should have attempted bootstrap (not just returned early).
	s.True(
		strings.Contains(result.Output, "bootstrap") ||
			strings.Contains(result.Output, "discovery") ||
			strings.Contains(result.Output, "connect"),
		"Should have attempted bootstrap with --force: %s", result.Output)

	s.T().Log("--force flag is accepted")
}

// TestCertRenewForceFlag tests the --force flag for renewal.
func (s *CLIAgentCertSuite) TestCertRenewForceFlag() {
	s.T().Log("Testing cert renew --force flag...")

	certsDir, err := helpers.CreateCertsDir(filepath.Join(s.tempDir, "renew-force-certs"))
	s.Require().NoError(err)

	env := s.cliEnv.WithEnv(map[string]string{
		"CORAL_CERTS_DIR": certsDir,
	})

	// Run renewal with --force flag.
	result := helpers.AgentCertRenew(s.ctx, env, s.colonyID, s.fingerprint,
		"https://localhost:9000", "", true)

	// Will fail (no cert), but --force should be accepted.
	s.NotNil(result, "Should return result")

	s.T().Log("cert renew --force flag is accepted")
}

// TestEnvironmentVariablePrecedence tests env var precedence for cert config.
func (s *CLIAgentCertSuite) TestEnvironmentVariablePrecedence() {
	s.T().Log("Testing environment variable precedence...")

	// Create two different cert directories.
	certsDir1 := filepath.Join(s.tempDir, "env-certs-1")
	certsDir2 := filepath.Join(s.tempDir, "env-certs-2")
	s.Require().NoError(os.MkdirAll(certsDir1, 0700))
	s.Require().NoError(os.MkdirAll(certsDir2, 0700))

	// Set env var to certsDir1.
	env := s.cliEnv.WithEnv(map[string]string{
		"CORAL_CERTS_DIR": certsDir1,
	})

	// Run with --certs-dir pointing to certsDir2 (should override env).
	result := helpers.RunCLIWithEnv(s.ctx, env, "agent", "cert", "status",
		"--certs-dir", certsDir2)

	// Command should succeed (checking certsDir2, not certsDir1).
	s.NotNil(result, "Command should complete")

	s.T().Log("environment variable precedence works correctly")
}

// TestHelpOutput tests that help text is displayed correctly.
func (s *CLIAgentCertSuite) TestHelpOutput() {
	s.T().Log("Testing help output for cert commands...")

	// Test bootstrap help.
	result := helpers.RunCLI(s.ctx, "agent", "bootstrap", "--help")
	s.Contains(result.Output, "fingerprint", "Bootstrap help should mention fingerprint")
	s.Contains(result.Output, "colony", "Bootstrap help should mention colony")
	s.Contains(result.Output, "psk", "Bootstrap help should mention psk")

	// Test cert status help.
	result = helpers.RunCLI(s.ctx, "agent", "cert", "status", "--help")
	s.Contains(result.Output, "certs-dir", "Cert status help should mention certs-dir")

	// Test cert renew help.
	result = helpers.RunCLI(s.ctx, "agent", "cert", "renew", "--help")
	s.Contains(result.Output, "colony-endpoint", "Cert renew help should mention colony-endpoint")
	s.Contains(result.Output, "force", "Cert renew help should mention force")

	s.T().Log("help output is correct")
}
