package distributed

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/coral-mesh/coral/tests/e2e/distributed/helpers"
)

// CLIAskConfigSuite tests the 'coral ask config' command (RFD 055).
//
// These tests do not require a live colony — coral ask config only reads and
// writes local config files. The suite uses an isolated home directory per run
// so tests do not interfere with real user configuration.
type CLIAskConfigSuite struct {
	E2EDistributedSuite

	cliEnv *helpers.CLITestEnv
}

// SetupSuite runs once before all tests in the suite.
func (s *CLIAskConfigSuite) SetupSuite() {
	s.E2EDistributedSuite.SetupSuite()

	// coral ask config doesn't connect to a colony, so pass an empty endpoint.
	var err error
	s.cliEnv, err = helpers.SetupCLIEnv(s.ctx, "test-colony-e2e", "")
	s.Require().NoError(err, "Failed to setup CLI environment")
}

// TearDownSuite cleans up after all tests.
func (s *CLIAskConfigSuite) TearDownSuite() {
	if s.cliEnv != nil {
		_ = s.cliEnv.Cleanup()
	}
	s.E2EDistributedSuite.TearDownSuite()
}

// globalConfigPath returns the path to the test environment's global config file.
func (s *CLIAskConfigSuite) globalConfigPath() string {
	return filepath.Join(s.cliEnv.ConfigDir, "config.yaml")
}

// readGlobalConfig reads and unmarshals the global config from the test environment.
func (s *CLIAskConfigSuite) readGlobalConfig() map[string]interface{} {
	data, err := s.cliEnv.ReadConfigFile("config.yaml")
	s.Require().NoError(err, "Failed to read global config")

	var cfg map[string]interface{}
	s.Require().NoError(yaml.Unmarshal(data, &cfg))
	return cfg
}

// TestAskConfigHelpText verifies that --help documents all subcommands and flags.
func (s *CLIAskConfigSuite) TestAskConfigHelpText() {
	s.T().Log("Testing 'coral ask config --help'...")

	result := helpers.RunCLIWithEnv(s.ctx, s.cliEnv.EnvVars(), "ask", "config", "--help")
	result.MustSucceed(s.T())

	s.Require().Contains(result.Output, "--provider", "Help should document --provider flag")
	s.Require().Contains(result.Output, "--model", "Help should document --model flag")
	s.Require().Contains(result.Output, "--api-key-env", "Help should document --api-key-env flag")
	s.Require().Contains(result.Output, "--yes", "Help should document --yes flag")
	s.Require().Contains(result.Output, "--dry-run", "Help should document --dry-run flag")
	s.Require().Contains(result.Output, "validate", "Help should list validate subcommand")
	s.Require().Contains(result.Output, "show", "Help should list show subcommand")
}

// TestAskConfigDryRun verifies that --dry-run shows a preview without writing config.
func (s *CLIAskConfigSuite) TestAskConfigDryRun() {
	s.T().Log("Testing 'coral ask config --dry-run'...")

	// Note the config before running.
	beforeData, _ := s.cliEnv.ReadConfigFile("config.yaml")

	envVars := s.cliEnv.EnvVars()
	envVars["GOOGLE_API_KEY"] = "fake-key-dry-run"

	result := helpers.RunAskConfig(s.ctx, envVars, "google", "gemini-3.1-flash-lite-preview", "GOOGLE_API_KEY", true)
	result.MustSucceed(s.T())

	// Should show preview.
	s.Require().Contains(result.Output, "gemini-3.1-flash-lite-preview")
	s.Require().Contains(result.Output, "dry-run")

	// Config file should be unchanged.
	afterData, err := s.cliEnv.ReadConfigFile("config.yaml")
	s.Require().NoError(err)
	s.Require().Equal(string(beforeData), string(afterData), "Config should not change in dry-run mode")
}

// TestAskConfigNonInteractiveGoogle tests saving a Google provider config non-interactively.
func (s *CLIAskConfigSuite) TestAskConfigNonInteractiveGoogle() {
	s.T().Log("Testing 'coral ask config' non-interactive Google setup...")

	// Use a fresh isolated env for this test to avoid cross-test pollution.
	env, err := helpers.SetupCLIEnv(s.ctx, "test-colony-e2e", "")
	s.Require().NoError(err)
	defer env.Cleanup()

	envVars := env.EnvVars()
	envVars["GOOGLE_API_KEY"] = "fake-key-for-e2e-test"

	result := helpers.RunAskConfig(s.ctx, envVars, "google", "gemini-3.1-flash-lite-preview", "GOOGLE_API_KEY", false)
	result.MustSucceed(s.T())

	// Output should show the wizard completed.
	s.Require().Contains(result.Output, "gemini-3.1-flash-lite-preview")
	s.Require().Contains(result.Output, "GOOGLE_API_KEY")

	// Config file should have the expected ask section.
	data, err := env.ReadConfigFile("config.yaml")
	s.Require().NoError(err)

	var cfg map[string]interface{}
	s.Require().NoError(yaml.Unmarshal(data, &cfg))

	ai, ok := cfg["ai"].(map[string]interface{})
	s.Require().True(ok, "Config should have 'ai' section")

	ask, ok := ai["ask"].(map[string]interface{})
	s.Require().True(ok, "Config should have 'ai.ask' section")

	s.Require().Equal("google:gemini-3.1-flash-lite-preview", ask["default_model"],
		"Config should set google:gemini-3.1-flash-lite-preview as default model")

	apiKeys, ok := ask["api_keys"].(map[string]interface{})
	s.Require().True(ok, "Config should have 'ai.ask.api_keys' section")
	s.Require().Equal("env://GOOGLE_API_KEY", apiKeys["google"],
		"Config should store API key as env:// reference")
}

// TestAskConfigNonInteractiveOpenAI tests saving an OpenAI provider config non-interactively.
func (s *CLIAskConfigSuite) TestAskConfigNonInteractiveOpenAI() {
	s.T().Log("Testing 'coral ask config' non-interactive OpenAI setup...")

	env, err := helpers.SetupCLIEnv(s.ctx, "test-colony-e2e", "")
	s.Require().NoError(err)
	defer env.Cleanup()

	envVars := env.EnvVars()
	envVars["OPENAI_API_KEY"] = "fake-openai-key-for-e2e"

	result := helpers.RunAskConfig(s.ctx, envVars, "openai", "gpt-4o-mini", "OPENAI_API_KEY", false)
	result.MustSucceed(s.T())

	data, err := env.ReadConfigFile("config.yaml")
	s.Require().NoError(err)

	var cfg map[string]interface{}
	s.Require().NoError(yaml.Unmarshal(data, &cfg))

	ai := cfg["ai"].(map[string]interface{})
	ask := ai["ask"].(map[string]interface{})
	s.Require().Equal("openai:gpt-4o-mini", ask["default_model"])

	apiKeys := ask["api_keys"].(map[string]interface{})
	s.Require().Equal("env://OPENAI_API_KEY", apiKeys["openai"])
}

// TestAskConfigDoesNotWriteAIProvider verifies that 'ai.provider' field is never written
// (it would fail GlobalConfig.Validate which only accepts "anthropic"/"openai").
func (s *CLIAskConfigSuite) TestAskConfigDoesNotWriteAIProvider() {
	s.T().Log("Verifying 'coral ask config' does not write ai.provider field...")

	env, err := helpers.SetupCLIEnv(s.ctx, "test-colony-e2e", "")
	s.Require().NoError(err)
	defer env.Cleanup()

	envVars := env.EnvVars()
	envVars["GOOGLE_API_KEY"] = "fake-key-for-e2e-test"

	result := helpers.RunAskConfig(s.ctx, envVars, "google", "gemini-3.1-flash-lite-preview", "GOOGLE_API_KEY", false)
	result.MustSucceed(s.T())

	data, err := env.ReadConfigFile("config.yaml")
	s.Require().NoError(err)

	// The raw YAML should not contain a top-level 'provider' key under 'ai'.
	s.Require().NotContains(string(data), "\n  provider:", "ai.provider must not be written by wizard")
}

// TestAskConfigCreatesBackup verifies that a second run creates a timestamped backup.
func (s *CLIAskConfigSuite) TestAskConfigCreatesBackup() {
	s.T().Log("Testing that 'coral ask config' creates a backup on second run...")

	env, err := helpers.SetupCLIEnv(s.ctx, "test-colony-e2e", "")
	s.Require().NoError(err)
	defer env.Cleanup()

	envVars := env.EnvVars()
	envVars["GOOGLE_API_KEY"] = "fake-key-for-e2e-backup-test"

	// First run — creates config.
	result1 := helpers.RunAskConfig(s.ctx, envVars, "google", "gemini-3.1-flash-lite-preview", "GOOGLE_API_KEY", false)
	result1.MustSucceed(s.T())

	// Second run — should create a backup.
	envVars["OPENAI_API_KEY"] = "fake-openai-key"
	result2 := helpers.RunAskConfig(s.ctx, envVars, "openai", "gpt-4o-mini", "OPENAI_API_KEY", false)
	result2.MustSucceed(s.T())

	// The output should mention a backup.
	s.Require().Contains(result2.Output, "Backup created", "Second run should create a backup")

	// A backup file should exist in the config directory.
	entries, err := os.ReadDir(env.ConfigDir)
	s.Require().NoError(err)

	hasBackup := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "config.yaml.backup.") {
			hasBackup = true
			break
		}
	}
	s.Require().True(hasBackup, "A timestamped backup file should exist")
}

// TestAskConfigShow verifies that 'coral ask config show' displays the current config.
func (s *CLIAskConfigSuite) TestAskConfigShow() {
	s.T().Log("Testing 'coral ask config show'...")

	env, err := helpers.SetupCLIEnv(s.ctx, "test-colony-e2e", "")
	s.Require().NoError(err)
	defer env.Cleanup()

	envVars := env.EnvVars()
	envVars["GOOGLE_API_KEY"] = "fake-key-for-show-test"

	// Write a config first.
	result := helpers.RunAskConfig(s.ctx, envVars, "google", "gemini-3.1-flash-lite-preview", "GOOGLE_API_KEY", false)
	result.MustSucceed(s.T())

	// Now show it — env var is set so it should show ✓.
	showResult := helpers.RunAskConfigShow(s.ctx, envVars)
	showResult.MustSucceed(s.T())

	s.Require().Contains(showResult.Output, "google:gemini-3.1-flash-lite-preview", "Show should display configured model")
	s.Require().Contains(showResult.Output, "GOOGLE_API_KEY", "Show should display the API key env var")
}

// TestAskConfigShowUnconfigured verifies 'coral ask config show' works with no ask config.
func (s *CLIAskConfigSuite) TestAskConfigShowUnconfigured() {
	s.T().Log("Testing 'coral ask config show' with no ask configuration...")

	env, err := helpers.SetupCLIEnv(s.ctx, "test-colony-e2e", "")
	s.Require().NoError(err)
	defer env.Cleanup()

	result := helpers.RunAskConfigShow(s.ctx, env.EnvVars())
	result.MustSucceed(s.T())

	s.Require().Contains(result.Output, "not configured", "Show should indicate model is not configured")
}

// TestAskConfigValidateWithEnvVar verifies validation passes when the API key env var is set.
func (s *CLIAskConfigSuite) TestAskConfigValidateWithEnvVar() {
	s.T().Log("Testing 'coral ask config validate' with API key set...")

	env, err := helpers.SetupCLIEnv(s.ctx, "test-colony-e2e", "")
	s.Require().NoError(err)
	defer env.Cleanup()

	envVars := env.EnvVars()
	envVars["GOOGLE_API_KEY"] = "fake-key-for-validate-test"

	// Write config.
	writeResult := helpers.RunAskConfig(s.ctx, envVars, "google", "gemini-3.1-flash-lite-preview", "GOOGLE_API_KEY", false)
	writeResult.MustSucceed(s.T())

	// Validate.
	validateResult := helpers.RunAskConfigValidate(s.ctx, envVars)
	validateResult.MustSucceed(s.T())

	s.Require().Contains(validateResult.Output, "✓", "Validate should show success indicators")
	s.Require().Contains(validateResult.Output, "valid", "Validate should confirm config is valid")
	s.Require().Contains(validateResult.Output, "GOOGLE_API_KEY", "Validate should check the API key env var")
}

// TestAskConfigValidateMissingModel verifies that validate fails with no default_model set.
func (s *CLIAskConfigSuite) TestAskConfigValidateMissingModel() {
	s.T().Log("Testing 'coral ask config validate' with missing model...")

	env, err := helpers.SetupCLIEnv(s.ctx, "test-colony-e2e", "")
	s.Require().NoError(err)
	defer env.Cleanup()

	// Do not write any ask config — model is empty.
	result := helpers.RunAskConfigValidate(s.ctx, env.EnvVars())

	// Should fail because default_model is required.
	s.Require().True(result.HasError(), "Validate should fail when no model is configured")
}

// TestAskConfigUnknownProvider verifies that an unknown provider returns a useful error.
func (s *CLIAskConfigSuite) TestAskConfigUnknownProvider() {
	s.T().Log("Testing 'coral ask config' with unknown provider...")

	result := helpers.RunAskConfig(s.ctx, s.cliEnv.EnvVars(), "nonexistent-provider", "some-model", "SOME_KEY", false)

	s.Require().True(result.HasError(), "Should fail for unknown provider")
	s.Require().True(
		result.ContainsOutput("nonexistent-provider") || result.ContainsOutput("unknown provider"),
		"Error should mention the unknown provider name",
	)
}

// TestAskConfigListProvidersShowsModels verifies that wizard metadata is visible
// via 'coral ask list-providers --models'.
func (s *CLIAskConfigSuite) TestAskConfigListProvidersShowsModels() {
	s.T().Log("Testing 'coral ask list-providers --models' shows wizard model metadata...")

	result := helpers.RunCLIWithEnv(s.ctx, s.cliEnv.EnvVars(), "ask", "list-providers", "--models")
	result.MustSucceed(s.T())

	// All three registered providers should appear.
	s.Require().Contains(result.Output, "google", "google provider should be listed")
	s.Require().Contains(result.Output, "openai", "openai provider should be listed")
	s.Require().Contains(result.Output, "coral", "coral provider should be listed")

	// Registered models should appear.
	s.Require().Contains(result.Output, "gemini-3.1-flash-lite-preview", "gemini-3.1-flash-lite-preview should appear")
	s.Require().Contains(result.Output, "gpt-4o-mini", "gpt-4o-mini should appear")
}

// TestAskConfigMissingAPIKeyEnvVar verifies that the wizard fails when the specified
// env var is not set.
func (s *CLIAskConfigSuite) TestAskConfigMissingAPIKeyEnvVar() {
	s.T().Log("Testing 'coral ask config' fails when env var is not set...")

	// Make sure MISSING_KEY_VAR is genuinely not set.
	envVars := s.cliEnv.EnvVars()
	delete(envVars, "MISSING_KEY_VAR")

	result := helpers.RunAskConfig(s.ctx, envVars, "google", "gemini-3.1-flash-lite-preview", "MISSING_KEY_VAR", false)

	s.Require().True(result.HasError(), "Should fail when API key env var is not set")
	s.Require().True(
		result.ContainsOutput("MISSING_KEY_VAR") || result.ContainsOutput("not set"),
		"Error should mention the missing env var",
	)
}
