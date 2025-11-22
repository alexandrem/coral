package config

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/coral-io/coral/internal/config"
)

// TestConfigCommandsE2E tests the coral config command family with real CLI execution.
func TestConfigCommandsE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// Build the binary.
	binaryPath := filepath.Join(os.TempDir(), "coral-test-config-e2e")
	buildBinary(t, binaryPath)
	defer os.Remove(binaryPath)

	// Create isolated config directory.
	configDir := t.TempDir()

	// Set CORAL_CONFIG to isolate from real config.
	t.Setenv("CORAL_CONFIG", configDir)

	// Ensure CORAL_COLONY_ID is not set.
	t.Setenv("CORAL_COLONY_ID", "")

	t.Run("get-contexts_empty", func(t *testing.T) {
		output := runCoral(t, binaryPath, "config", "get-contexts")
		assert.Contains(t, output, "No colonies configured")
	})

	t.Run("validate_empty", func(t *testing.T) {
		output := runCoral(t, binaryPath, "config", "validate")
		assert.Contains(t, output, "No colonies configured")
	})

	// Create test colonies.
	createTestColony(t, configDir, "app1-dev-abc123", "app1", "dev")
	createTestColony(t, configDir, "app2-prod-xyz789", "app2", "prod")

	t.Run("get-contexts_with_colonies", func(t *testing.T) {
		output := runCoral(t, binaryPath, "config", "get-contexts")

		// Should show both colonies.
		assert.Contains(t, output, "app1-dev-abc123")
		assert.Contains(t, output, "app2-prod-xyz789")
		assert.Contains(t, output, "COLONY-ID")
		assert.Contains(t, output, "APPLICATION")
		assert.Contains(t, output, "ENVIRONMENT")
		assert.Contains(t, output, "RESOLUTION")
	})

	t.Run("get-contexts_json", func(t *testing.T) {
		output := runCoral(t, binaryPath, "config", "get-contexts", "--json")

		var result struct {
			CurrentColony    string `json:"current_colony"`
			ResolutionSource string `json:"resolution_source"`
			Colonies         []struct {
				ColonyID    string `json:"colony_id"`
				Application string `json:"application"`
				Environment string `json:"environment"`
				IsCurrent   bool   `json:"is_current"`
			} `json:"colonies"`
		}
		err := json.Unmarshal([]byte(output), &result)
		require.NoError(t, err, "Should parse JSON output")

		assert.Len(t, result.Colonies, 2)
	})

	t.Run("use-context", func(t *testing.T) {
		output := runCoral(t, binaryPath, "config", "use-context", "app1-dev-abc123")
		assert.Contains(t, output, "app1-dev-abc123")

		// Verify it's now the default.
		output = runCoral(t, binaryPath, "config", "current-context")
		assert.Contains(t, output, "app1-dev-abc123")
	})

	t.Run("current-context", func(t *testing.T) {
		output := runCoral(t, binaryPath, "config", "current-context")
		assert.Contains(t, output, "app1-dev-abc123")
	})

	t.Run("current-context_verbose", func(t *testing.T) {
		output := runCoral(t, binaryPath, "config", "current-context", "--verbose")
		assert.Contains(t, output, "app1-dev-abc123")
		assert.Contains(t, output, "Resolution:")
		assert.Contains(t, output, "global")
	})

	t.Run("get-contexts_shows_current_marker", func(t *testing.T) {
		output := runCoral(t, binaryPath, "config", "get-contexts")

		// Current colony should have * marker.
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			if strings.Contains(line, "app1-dev-abc123") {
				assert.Contains(t, line, "*", "Current colony should have * marker")
				assert.Contains(t, line, "global", "Current colony should show resolution source")
			}
		}
	})

	t.Run("validate_with_colonies", func(t *testing.T) {
		output := runCoral(t, binaryPath, "config", "validate")
		assert.Contains(t, output, "valid")
		assert.Contains(t, output, "Validation summary")
	})

	t.Run("validate_json", func(t *testing.T) {
		output := runCoral(t, binaryPath, "config", "validate", "--json")

		var result struct {
			Results []struct {
				ColonyID string `json:"colony_id"`
				Valid    bool   `json:"valid"`
			} `json:"results"`
			ValidCount   int `json:"valid_count"`
			InvalidCount int `json:"invalid_count"`
		}
		err := json.Unmarshal([]byte(output), &result)
		require.NoError(t, err, "Should parse JSON output")

		assert.Equal(t, 2, result.ValidCount)
		assert.Equal(t, 0, result.InvalidCount)
	})

	t.Run("view", func(t *testing.T) {
		output := runCoral(t, binaryPath, "config", "view")
		assert.Contains(t, output, "Colony: app1-dev-abc123")
		assert.Contains(t, output, "colony_id:")
		assert.Contains(t, output, "application_name:")
	})

	t.Run("view_raw", func(t *testing.T) {
		output := runCoral(t, binaryPath, "config", "view", "--raw")
		// Raw output should be valid YAML.
		var cfg map[string]interface{}
		err := yaml.Unmarshal([]byte(output), &cfg)
		require.NoError(t, err, "Raw output should be valid YAML")
	})

	t.Run("switch_context_and_verify", func(t *testing.T) {
		// Switch to app2.
		runCoral(t, binaryPath, "config", "use-context", "app2-prod-xyz789")

		// Verify switch.
		output := runCoral(t, binaryPath, "config", "current-context")
		assert.Contains(t, output, "app2-prod-xyz789")

		// Verify get-contexts shows new current.
		output = runCoral(t, binaryPath, "config", "get-contexts")
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			if strings.Contains(line, "app2-prod-xyz789") {
				assert.Contains(t, line, "*", "app2 should now have * marker")
			}
			if strings.Contains(line, "app1-dev-abc123") {
				assert.NotContains(t, line, "*", "app1 should no longer have * marker")
			}
		}
	})

	t.Run("colony_list_shows_marker", func(t *testing.T) {
		// Verify coral colony list also shows the marker (RFD 050).
		output := runCoral(t, binaryPath, "colony", "list")
		assert.Contains(t, output, "RESOLUTION")
		assert.Contains(t, output, "*")
	})

	t.Run("colony_current_verbose", func(t *testing.T) {
		output := runCoral(t, binaryPath, "colony", "current", "--verbose")
		assert.Contains(t, output, "Resolution:")
	})
}

// TestConfigEnvVarPriority tests that CORAL_COLONY_ID env var takes priority.
func TestConfigEnvVarPriority(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	binaryPath := filepath.Join(os.TempDir(), "coral-test-config-env-e2e")
	buildBinary(t, binaryPath)
	defer os.Remove(binaryPath)

	configDir := t.TempDir()
	t.Setenv("CORAL_CONFIG", configDir)

	// Create colonies.
	createTestColony(t, configDir, "colony-a", "app-a", "dev")
	createTestColony(t, configDir, "colony-b", "app-b", "prod")

	// Set default to colony-a.
	runCoral(t, binaryPath, "config", "use-context", "colony-a")

	// Verify default is colony-a.
	output := runCoral(t, binaryPath, "config", "current-context")
	assert.Contains(t, output, "colony-a")

	// Set env var to colony-b.
	t.Setenv("CORAL_COLONY_ID", "colony-b")

	// Now current should be colony-b (env takes priority).
	output = runCoral(t, binaryPath, "config", "current-context", "--verbose")
	assert.Contains(t, output, "colony-b")
	assert.Contains(t, output, "environment variable")

	// get-contexts should show env resolution.
	output = runCoral(t, binaryPath, "config", "get-contexts")
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "colony-b") && strings.Contains(line, "*") {
			assert.Contains(t, line, "env", "Should show env resolution")
		}
	}
}

// buildBinary builds the coral binary for testing.
func buildBinary(t *testing.T, outputPath string) {
	t.Helper()

	// Check if binary already exists and is recent.
	if info, err := os.Stat(outputPath); err == nil {
		if time.Since(info.ModTime()) < 5*time.Minute {
			t.Logf("Using existing test binary: %s", outputPath)
			return
		}
	}

	t.Logf("Building test binary: %s", outputPath)

	cmd := exec.Command("go", "build", "-o", outputPath, "./cmd/coral")
	cmd.Dir = filepath.Join("..", "..", "..")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Should build binary: %s", string(output))
}

// runCoral runs the coral CLI and returns stdout.
func runCoral(t *testing.T, binaryPath string, args ...string) string {
	t.Helper()

	cmd := exec.Command(binaryPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Allow exit code 1 for some commands (e.g., validation failures).
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return string(output)
		}
		t.Fatalf("Command failed: %v\nOutput: %s", err, string(output))
	}
	return string(output)
}

// createTestColony creates a test colony config.
func createTestColony(t *testing.T, configDir, colonyID, appName, env string) {
	t.Helper()

	coloniesDir := filepath.Join(configDir, ".coral", "colonies", colonyID)
	err := os.MkdirAll(coloniesDir, 0700)
	require.NoError(t, err)

	cfg := &config.ColonyConfig{
		Version:         "1",
		ColonyID:        colonyID,
		ApplicationName: appName,
		Environment:     env,
		WireGuard: config.WireGuardConfig{
			Port:            41580,
			MeshIPv4:        "100.64.0.1",
			MeshNetworkIPv4: "100.64.0.0/10",
			MTU:             1420,
		},
		Discovery: config.DiscoveryColony{
			Enabled: true,
			MeshID:  colonyID,
		},
		CreatedAt: time.Now(),
	}

	data, err := yaml.Marshal(cfg)
	require.NoError(t, err)

	configPath := filepath.Join(coloniesDir, "config.yaml")
	err = os.WriteFile(configPath, data, 0600)
	require.NoError(t, err)

	t.Logf("Created test colony: %s at %s", colonyID, configPath)
}
