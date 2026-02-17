package distributed

import (
	"context"
	"os"
	"path/filepath"

	"github.com/stretchr/testify/suite"

	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// InitSuite tests the `coral init` command and colony initialization.
//
// This suite validates that:
// 1. `coral init` generates a valid colony configuration
// 2. Generated config has correct structure and required fields
// 3. A colony can successfully start with the generated config
// 4. CA certificates are properly generated (RFD 047)
//
// Unlike other test suites, this runs in isolation and creates its own
// temporary colony to test the init process from scratch.
type InitSuite struct {
	suite.Suite

	ctx        context.Context
	tempDir    string // Temporary directory for test colony
	colonyID   string
	configPath string
}

// SetupSuite runs once before all tests in the suite.
func (s *InitSuite) SetupSuite() {
	s.ctx = context.Background()

	// Validate coral binary exists before running any tests.
	err := helpers.EnsureCoralBinary()
	s.Require().NoError(err, "coral binary validation failed")
	s.T().Log("✓ coral binary found and accessible")

	// Create temporary directory for this test colony
	tempDir, err := os.MkdirTemp("", "coral-init-test-*")
	s.Require().NoError(err, "Failed to create temp directory")
	s.tempDir = tempDir

	s.T().Logf("Using temporary directory: %s", s.tempDir)
}

// TearDownSuite runs once after all tests in the suite.
func (s *InitSuite) TearDownSuite() {
	if s.tempDir != "" {
		_ = os.RemoveAll(s.tempDir)
		s.T().Logf("Cleaned up temporary directory: %s", s.tempDir)
	}
}

// TestInitCommandGeneratesValidConfig tests that `coral init` creates a valid colony configuration.
//
// Test flow:
// 1. Run `coral init` in isolated environment
// 2. Verify colony config file is created
// 3. Validate config structure and required fields
// 4. Verify WireGuard keys, colony secret, CA files
func (s *InitSuite) TestInitCommandGeneratesValidConfig() {
	s.T().Log("Testing coral init command...")

	// Set HOME to temp directory so config goes to temp location
	origHome := os.Getenv("HOME")
	testHome := filepath.Join(s.tempDir, "home")
	s.Require().NoError(os.MkdirAll(testHome, 0755))
	s.T().Setenv("HOME", testHome)
	defer func() {
		os.Setenv("HOME", origHome)
	}()

	// Run `coral init test-init-colony --env e2e-init`
	appName := "test-init-colony"
	environment := "e2e-init"

	// Execute init command via helper
	colonyID, err := helpers.RunCoralInit(s.ctx, appName, environment, filepath.Join(s.tempDir, "storage"))
	s.Require().NoError(err, "coral init should succeed")
	s.Require().NotEmpty(colonyID, "colony ID should be generated")

	s.colonyID = colonyID
	s.T().Logf("✓ Generated colony ID: %s", colonyID)

	// Verify colony config file exists
	loader, err := config.NewLoader()
	s.Require().NoError(err, "Failed to create config loader")

	colonyConfig, err := loader.LoadColonyConfig(colonyID)
	s.Require().NoError(err, "Failed to load generated colony config")

	// Validate config structure
	s.Equal(colonyID, colonyConfig.ColonyID, "Colony ID should match")
	s.Equal(appName, colonyConfig.ApplicationName, "Application name should match")
	s.Equal(environment, colonyConfig.Environment, "Environment should match")

	// Verify secrets and keys are generated
	s.NotEmpty(colonyConfig.ColonySecret, "Colony secret should be generated")
	s.NotEmpty(colonyConfig.WireGuard.PrivateKey, "WireGuard private key should be generated")
	s.NotEmpty(colonyConfig.WireGuard.PublicKey, "WireGuard public key should be generated")

	// Verify WireGuard configuration
	s.Equal("wg0", colonyConfig.WireGuard.InterfaceName, "WireGuard interface should be wg0")
	s.NotZero(colonyConfig.WireGuard.Port, "WireGuard port should be set")
	s.NotEmpty(colonyConfig.WireGuard.MeshIPv4, "Mesh IPv4 should be set")
	s.NotEmpty(colonyConfig.WireGuard.MeshIPv6, "Mesh IPv6 should be set")

	// Verify discovery configuration
	s.False(colonyConfig.Discovery.Disabled, "Discovery should be enabled by default")
	s.Equal(colonyID, colonyConfig.Discovery.MeshID, "Mesh ID should match colony ID")

	// Verify timestamps
	s.NotZero(colonyConfig.CreatedAt, "CreatedAt timestamp should be set")
	s.NotEmpty(colonyConfig.CreatedBy, "CreatedBy should be set")

	s.T().Log("✓ Colony config structure validated")

	// Verify CA files exist (RFD 047)
	colonyDir := loader.ColonyDir(colonyID)
	caDir := filepath.Join(colonyDir, "ca")

	requiredCAFiles := []string{
		"root-ca.crt",
		"root-ca.key",
		"server-intermediate.crt",
		"server-intermediate.key",
		"agent-intermediate.crt",
		"agent-intermediate.key",
		"policy-signing.crt",
		"policy-signing.key",
	}

	for _, file := range requiredCAFiles {
		filePath := filepath.Join(caDir, file)
		s.FileExists(filePath, "CA file should exist: %s", file)
	}

	s.T().Log("✓ Certificate Authority files validated")

	// Verify project-local config
	projectConfigPath := filepath.Join(s.tempDir, ".coral", "config.yaml")
	s.FileExists(projectConfigPath, "Project config should exist")

	s.T().Log("✓ coral init validation complete")
}

// TestGeneratedColonyCanStart verifies that a colony can start with init-generated config.
//
// This test is more involved as it requires:
// 1. Using the config from TestInitCommandGeneratesValidConfig
// 2. Starting a colony container with that config
// 3. Verifying the colony starts successfully
// 4. Cleaning up the colony
//
// TODO: Implement this test once we have helpers for starting isolated colonies.
func (s *InitSuite) TestGeneratedColonyCanStart() {
	s.T().Skip("TODO: Implement test for starting colony with generated config")

	// This would:
	// 1. Create a docker container with the generated config
	// 2. Start the colony
	// 3. Verify it's healthy
	// 4. Clean up
}

// TestInitWithCustomStoragePath tests init with custom storage path.
func (s *InitSuite) TestInitWithCustomStoragePath() {
	s.T().Log("Testing coral init with custom storage path...")

	customStorage := filepath.Join(s.tempDir, "custom-storage")

	// Set HOME to temp directory
	testHome := filepath.Join(s.tempDir, "home2")
	s.Require().NoError(os.MkdirAll(testHome, 0755))
	s.T().Setenv("HOME", testHome)

	// Run init with custom storage
	colonyID, err := helpers.RunCoralInit(s.ctx, "test-custom-storage", "e2e", customStorage)
	s.Require().NoError(err, "coral init should succeed with custom storage")

	// Verify config uses custom storage path
	loader, err := config.NewLoader()
	s.Require().NoError(err)

	colonyConfig, err := loader.LoadColonyConfig(colonyID)
	s.Require().NoError(err)

	s.Equal(customStorage, colonyConfig.StoragePath, "Storage path should match custom path")

	s.T().Log("✓ Custom storage path validated")
}

// TestInitWithDiscoveryURL tests init with custom discovery URL.
func (s *InitSuite) TestInitWithDiscoveryURL() {
	s.T().Log("Testing coral init with custom discovery URL...")

	customDiscovery := "http://custom-discovery:8080"

	// Set HOME to temp directory
	testHome := filepath.Join(s.tempDir, "home3")
	s.Require().NoError(os.MkdirAll(testHome, 0755))
	s.T().Setenv("HOME", testHome)

	// Run init with custom discovery URL
	_, err := helpers.RunCoralInitWithDiscovery(s.ctx, "test-custom-discovery", "e2e",
		filepath.Join(s.tempDir, "storage2"), customDiscovery)
	s.Require().NoError(err, "coral init should succeed with custom discovery")

	// Verify global config has custom discovery URL
	loader, err := config.NewLoader()
	s.Require().NoError(err)

	globalConfig, err := loader.LoadGlobalConfig()
	s.Require().NoError(err)

	s.Equal(customDiscovery, globalConfig.Discovery.Endpoint, "Discovery URL should match custom URL")

	s.T().Log("✓ Custom discovery URL validated")
}

// TestInitCreatesDefaultColony tests that first init sets default colony.
func (s *InitSuite) TestInitCreatesDefaultColony() {
	s.T().Log("Testing that first init sets default colony...")

	// Set HOME to fresh temp directory (no existing config)
	testHome := filepath.Join(s.tempDir, "home4")
	s.Require().NoError(os.MkdirAll(testHome, 0755))
	s.T().Setenv("HOME", testHome)

	// Run init
	colonyID, err := helpers.RunCoralInit(s.ctx, "test-default", "e2e",
		filepath.Join(s.tempDir, "storage3"))
	s.Require().NoError(err, "coral init should succeed")

	// Verify this colony is set as default
	loader, err := config.NewLoader()
	s.Require().NoError(err)

	globalConfig, err := loader.LoadGlobalConfig()
	s.Require().NoError(err)

	s.Equal(colonyID, globalConfig.DefaultColony, "First colony should be set as default")

	s.T().Log("✓ Default colony setting validated")
}
