package helpers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CLIResult contains the output and exit code from a CLI command.
type CLIResult struct {
	Output   string
	ExitCode int
	Err      error
}

// getCoralBinaryPath returns the path to the coral binary.
// Looks in project bin/ folder first (for locally built binaries),
// then falls back to PATH.
func getCoralBinaryPath() string {
	// Try project bin/ folder first (relative to test directory)
	// tests/e2e/distributed -> ../../../bin/coral
	testDir, err := os.Getwd()
	if err == nil {
		projectRoot := filepath.Join(testDir, "../../..")
		localBinary := filepath.Join(projectRoot, "bin", "coral")
		if _, err := os.Stat(localBinary); err == nil {
			return localBinary
		}
	}

	// Fall back to PATH
	return "coral"
}

// RunCLI executes a coral CLI command and returns the result.
func RunCLI(ctx context.Context, args ...string) *CLIResult {
	coralBin := getCoralBinaryPath()
	cmd := exec.CommandContext(ctx, coralBin, args...)
	output, err := cmd.CombinedOutput()

	result := &CLIResult{
		Output: string(output),
		Err:    err,
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exitErr.ExitCode()
	}

	return result
}

// RunCLIWithEnv executes a coral CLI command with custom environment variables.
func RunCLIWithEnv(ctx context.Context, env map[string]string, args ...string) *CLIResult {
	coralBin := getCoralBinaryPath()
	cmd := exec.CommandContext(ctx, coralBin, args...)

	// Start with current environment
	cmd.Env = os.Environ()

	// Add custom environment variables
	for key, value := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
	}

	// Ensure the directory containing the coral binary is in PATH
	// This is required for commands that spawn subprocesses using "coral" (e.g. ask)
	coralDir := filepath.Dir(coralBin)
	pathVar := "PATH"
	currentPath := os.Getenv(pathVar)
	newPath := fmt.Sprintf("%s%c%s", coralDir, os.PathListSeparator, currentPath)
	cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", pathVar, newPath))

	output, err := cmd.CombinedOutput()

	result := &CLIResult{
		Output: string(output),
		Err:    err,
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exitErr.ExitCode()
	}

	return result
}

// AgentList executes `coral agent list` and returns the output.
func AgentList(ctx context.Context, colonyEndpoint string) *CLIResult {
	return RunCLIWithEnv(ctx, map[string]string{
		"CORAL_COLONY_ENDPOINT": colonyEndpoint,
	}, "colony", "agents")
}

// AgentListJSON executes `coral agent list --format json` and parses the output.
func AgentListJSON(ctx context.Context, colonyEndpoint string) ([]map[string]interface{}, error) {
	result := RunCLIWithEnv(ctx, map[string]string{
		"CORAL_COLONY_ENDPOINT": colonyEndpoint,
	}, "colony", "agents", "-o", "json")

	if result.Err != nil {
		return nil, fmt.Errorf("agent list failed: %w\nOutput: %s", result.Err, result.Output)
	}

	var agents []map[string]interface{}
	if err := json.Unmarshal([]byte(result.Output), &agents); err != nil {
		return nil, fmt.Errorf("failed to parse JSON output: %w\nOutput: %s", err, result.Output)
	}

	return agents, nil
}

// ServiceList executes `coral service list` and returns the output.
func ServiceList(ctx context.Context, colonyEndpoint string) *CLIResult {
	return RunCLIWithEnv(ctx, map[string]string{
		"CORAL_COLONY_ENDPOINT": colonyEndpoint,
	}, "colony", "service", "list")
}

// ServiceListJSON executes `coral service list --format json` and parses the output.
func ServiceListJSON(ctx context.Context, colonyEndpoint string) ([]map[string]interface{}, error) {
	result := RunCLIWithEnv(ctx, map[string]string{
		"CORAL_COLONY_ENDPOINT": colonyEndpoint,
	}, "colony", "service", "list", "-o", "json")

	if result.Err != nil {
		return nil, fmt.Errorf("service list failed: %w\nOutput: %s", result.Err, result.Output)
	}

	// Parse the response envelope
	var response map[string]interface{}
	if err := json.Unmarshal([]byte(result.Output), &response); err != nil {
		return nil, fmt.Errorf("failed to parse JSON output: %w\nOutput: %s", err, result.Output)
	}

	// Extract the services array from the response
	servicesRaw, ok := response["services"]
	if !ok {
		return nil, fmt.Errorf("response missing 'services' field\nOutput: %s", result.Output)
	}

	// Convert to []map[string]interface{}
	servicesArray, ok := servicesRaw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("services field is not an array\nOutput: %s", result.Output)
	}

	services := make([]map[string]interface{}, len(servicesArray))
	for i, svc := range servicesArray {
		svcMap, ok := svc.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("service at index %d is not an object\nOutput: %s", i, result.Output)
		}
		services[i] = svcMap
	}

	return services, nil
}

// QuerySummary executes `coral query summary` and returns the output.
func QuerySummary(ctx context.Context, colonyEndpoint, service, timeRange string) *CLIResult {
	args := []string{"query", "summary"}

	if service != "" {
		args = append(args, service)
	}

	if timeRange != "" {
		args = append(args, "--since", timeRange)
	}

	return RunCLIWithEnv(ctx, map[string]string{
		"CORAL_COLONY_ENDPOINT": colonyEndpoint,
	}, args...)
}

// QuerySummaryJSON executes `coral query summary --format json` and parses the output.
func QuerySummaryJSON(ctx context.Context, colonyEndpoint, service, timeRange string) (map[string]interface{}, error) {
	args := []string{"query", "summary", "--format", "json"}

	if service != "" {
		args = append(args, "--service", service)
	}

	if timeRange != "" {
		args = append(args, "--since", timeRange)
	}

	result := RunCLIWithEnv(ctx, map[string]string{
		"CORAL_COLONY_ENDPOINT": colonyEndpoint,
	}, args...)

	if result.Err != nil {
		return nil, fmt.Errorf("query summary failed: %w\nOutput: %s", result.Err, result.Output)
	}

	var summary map[string]interface{}
	if err := json.Unmarshal([]byte(result.Output), &summary); err != nil {
		return nil, fmt.Errorf("failed to parse JSON output: %w\nOutput: %s", err, result.Output)
	}

	return summary, nil
}

// QueryTraces executes `coral query traces` and returns the output.
func QueryTraces(ctx context.Context, colonyEndpoint, service, timeRange string, limit int) *CLIResult {
	args := []string{"query", "traces"}

	if service != "" {
		args = append(args, service)
	}

	if timeRange != "" {
		args = append(args, "--since", timeRange)
	}

	if limit > 0 {
		args = append(args, "--limit", fmt.Sprintf("%d", limit))
	}

	return RunCLIWithEnv(ctx, map[string]string{
		"CORAL_COLONY_ENDPOINT": colonyEndpoint,
	}, args...)
}

// QueryTracesJSON executes `coral query traces --format json` and parses the output.
func QueryTracesJSON(ctx context.Context, colonyEndpoint, service, timeRange string, limit int) (map[string]interface{}, error) {
	args := []string{"query", "traces", "--format", "json"}

	if service != "" {
		args = append(args, service)
	}

	if timeRange != "" {
		args = append(args, "--since", timeRange)
	}

	if limit > 0 {
		args = append(args, "--limit", fmt.Sprintf("%d", limit))
	}

	result := RunCLIWithEnv(ctx, map[string]string{
		"CORAL_COLONY_ENDPOINT": colonyEndpoint,
	}, args...)

	if result.Err != nil {
		return nil, fmt.Errorf("query traces failed: %w\nOutput: %s", result.Err, result.Output)
	}

	var traces map[string]interface{}
	if err := json.Unmarshal([]byte(result.Output), &traces); err != nil {
		return nil, fmt.Errorf("failed to parse JSON output: %w\nOutput: %s", err, result.Output)
	}

	return traces, nil
}

// QueryMetrics executes `coral query metrics` and returns the output.
func QueryMetrics(ctx context.Context, colonyEndpoint, service, timeRange string) *CLIResult {
	args := []string{"query", "metrics"}

	if service != "" {
		args = append(args, service)
	}

	if timeRange != "" {
		args = append(args, "--since", timeRange)
	}

	return RunCLIWithEnv(ctx, map[string]string{
		"CORAL_COLONY_ENDPOINT": colonyEndpoint,
	}, args...)
}

// QueryMetricsJSON executes `coral query metrics --format json` and parses the output.
func QueryMetricsJSON(ctx context.Context, colonyEndpoint, service, timeRange string) (map[string]interface{}, error) {
	args := []string{"query", "metrics", "--format", "json"}

	if service != "" {
		args = append(args, service)
	}

	if timeRange != "" {
		args = append(args, "--since", timeRange)
	}

	result := RunCLIWithEnv(ctx, map[string]string{
		"CORAL_COLONY_ENDPOINT": colonyEndpoint,
	}, args...)

	if result.Err != nil {
		return nil, fmt.Errorf("query metrics failed: %w\nOutput: %s", result.Err, result.Output)
	}

	var metrics map[string]interface{}
	if err := json.Unmarshal([]byte(result.Output), &metrics); err != nil {
		return nil, fmt.Errorf("failed to parse JSON output: %w\nOutput: %s", err, result.Output)
	}

	return metrics, nil
}

// DebugCPUProfile executes `coral profile cpu` and returns the output.
func DebugCPUProfile(ctx context.Context, colonyEndpoint, agentID, serviceName string, duration int) *CLIResult {
	args := []string{"profile", "cpu"}

	if agentID != "" {
		args = append(args, "--agent", agentID)
	}

	if serviceName != "" {
		args = append(args, "--service", serviceName)
	}

	if duration > 0 {
		args = append(args, "--duration", fmt.Sprintf("%d", duration))
	}

	return RunCLIWithEnv(ctx, map[string]string{
		"CORAL_COLONY_ENDPOINT": colonyEndpoint,
	}, args...)
}

// ColonyStatus executes `coral colony status` and returns the output.
func ColonyStatus(ctx context.Context, colonyEndpoint string) *CLIResult {
	return RunCLIWithEnv(ctx, map[string]string{
		"CORAL_COLONY_ENDPOINT": colonyEndpoint,
	}, "colony", "status")
}

// ColonyStatusJSON executes `coral colony status --format json` and parses the output.
func ColonyStatusJSON(ctx context.Context, colonyEndpoint string) (map[string]interface{}, error) {
	result := RunCLIWithEnv(ctx, map[string]string{
		"CORAL_COLONY_ENDPOINT": colonyEndpoint,
	}, "colony", "status", "-o", "json")

	if result.Err != nil {
		return nil, fmt.Errorf("colony status failed: %w\nOutput: %s", result.Err, result.Output)
	}

	var status map[string]interface{}
	if err := json.Unmarshal([]byte(result.Output), &status); err != nil {
		return nil, fmt.Errorf("failed to parse JSON output: %w\nOutput: %s", err, result.Output)
	}

	return status, nil
}

// ColonyAgents executes `coral colony agents` and returns the output.
func ColonyAgents(ctx context.Context, colonyEndpoint string) *CLIResult {
	return RunCLIWithEnv(ctx, map[string]string{
		"CORAL_COLONY_ENDPOINT": colonyEndpoint,
	}, "colony", "agents")
}

// ColonyAgentsJSON executes `coral colony agents --format json` and parses the output.
func ColonyAgentsJSON(ctx context.Context, colonyEndpoint string) ([]map[string]interface{}, error) {
	result := RunCLIWithEnv(ctx, map[string]string{
		"CORAL_COLONY_ENDPOINT": colonyEndpoint,
	}, "colony", "agents", "-o", "json")

	if result.Err != nil {
		return nil, fmt.Errorf("colony agents failed: %w\nOutput: %s", result.Err, result.Output)
	}

	var agents []map[string]interface{}
	if err := json.Unmarshal([]byte(result.Output), &agents); err != nil {
		return nil, fmt.Errorf("failed to parse JSON output: %w\nOutput: %s", err, result.Output)
	}

	return agents, nil
}

// ColonyTokenCreate executes `coral colony token create` and returns the output.
func ColonyTokenCreate(ctx context.Context, env map[string]string, tokenID, permissions string) *CLIResult {
	args := []string{"colony", "token", "create", tokenID}
	if permissions != "" {
		args = append(args, "--permissions", permissions)
	}
	// Note: We use the provided env which should have CORAL_CONFIG or HOME set
	return RunCLIWithEnv(ctx, env, args...)
}

// AgentStatus executes `coral agent status` for a specific agent and returns the output.
func AgentStatus(ctx context.Context, colonyEndpoint, agentID string) *CLIResult {
	args := []string{"agent", "status"}

	if agentID != "" {
		args = append(args, "--agent", agentID)
	}

	return RunCLIWithEnv(ctx, map[string]string{
		"CORAL_COLONY_ENDPOINT": colonyEndpoint,
	}, args...)
}

// AgentStatusJSON executes `coral agent status --format json` and parses the output.
func AgentStatusJSON(ctx context.Context, colonyEndpoint, agentID string) (map[string]interface{}, error) {
	args := []string{"agent", "status", "--format", "json"}

	if agentID != "" {
		args = append(args, "--agent", agentID)
	}

	result := RunCLIWithEnv(ctx, map[string]string{
		"CORAL_COLONY_ENDPOINT": colonyEndpoint,
	}, args...)

	if result.Err != nil {
		return nil, fmt.Errorf("agent status failed: %w\nOutput: %s", result.Err, result.Output)
	}

	var status map[string]interface{}
	if err := json.Unmarshal([]byte(result.Output), &status); err != nil {
		return nil, fmt.Errorf("failed to parse JSON output: %w\nOutput: %s", err, result.Output)
	}

	return status, nil
}

// QueryServices executes `coral query services` and returns the output.
func QueryServices(ctx context.Context, colonyEndpoint string) *CLIResult {
	return RunCLIWithEnv(ctx, map[string]string{
		"CORAL_COLONY_ENDPOINT": colonyEndpoint,
	}, "query", "services")
}

// QueryServicesJSON executes `coral query services --format json` and parses the output.
func QueryServicesJSON(ctx context.Context, colonyEndpoint string) ([]map[string]interface{}, error) {
	result := RunCLIWithEnv(ctx, map[string]string{
		"CORAL_COLONY_ENDPOINT": colonyEndpoint,
	}, "query", "services", "--format", "json")

	if result.Err != nil {
		return nil, fmt.Errorf("query services failed: %w\nOutput: %s", result.Err, result.Output)
	}

	var services []map[string]interface{}
	if err := json.Unmarshal([]byte(result.Output), &services); err != nil {
		return nil, fmt.Errorf("failed to parse JSON output: %w\nOutput: %s", err, result.Output)
	}

	return services, nil
}

// ConfigGetContexts executes `coral config get-contexts` and returns the output.
func ConfigGetContexts(ctx context.Context) *CLIResult {
	return RunCLI(ctx, "config", "get-contexts")
}

// ConfigGetContextsJSON executes `coral config get-contexts --format json` and parses the output.
func ConfigGetContextsJSON(ctx context.Context) ([]map[string]interface{}, error) {
	result := RunCLI(ctx, "config", "get-contexts", "--format", "json")

	if result.Err != nil {
		return nil, fmt.Errorf("config get-contexts failed: %w\nOutput: %s", result.Err, result.Output)
	}

	var contexts []map[string]interface{}
	if err := json.Unmarshal([]byte(result.Output), &contexts); err != nil {
		return nil, fmt.Errorf("failed to parse JSON output: %w\nOutput: %s", err, result.Output)
	}

	return contexts, nil
}

// ConfigCurrentContext executes `coral config current-context` and returns the output.
func ConfigCurrentContext(ctx context.Context) *CLIResult {
	return RunCLI(ctx, "config", "current-context")
}

// ConfigUseContext executes `coral config use-context` to switch colonies.
func ConfigUseContext(ctx context.Context, colonyID string) *CLIResult {
	return RunCLI(ctx, "config", "use-context", colonyID)
}

// ParseTableOutput parses CLI table output into rows.
// Useful for validating human-readable table output.
func ParseTableOutput(output string) [][]string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	var rows [][]string

	for _, line := range lines {
		// Skip empty lines and separator lines
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "---") || strings.HasPrefix(line, "===") {
			continue
		}

		// Split by multiple spaces (table columns)
		fields := strings.Fields(line)
		if len(fields) > 0 {
			rows = append(rows, fields)
		}
	}

	return rows
}

// ContainsOutput checks if the CLI output contains a specific substring.
func (r *CLIResult) ContainsOutput(substr string) bool {
	return strings.Contains(r.Output, substr)
}

// HasError checks if the CLI command returned an error.
func (r *CLIResult) HasError() bool {
	return r.Err != nil || r.ExitCode != 0
}

// MustSucceed panics if the CLI command failed (useful in tests).
func (r *CLIResult) MustSucceed(t interface {
	Fatalf(format string, args ...interface{})
}) {
	if r.HasError() {
		t.Fatalf("CLI command failed (exit code %d): %v\nOutput: %s", r.ExitCode, r.Err, r.Output)
	}
}

// MustFail panics if the CLI command succeeded (useful for testing error cases).
func (r *CLIResult) MustFail(t interface {
	Fatalf(format string, args ...interface{})
}) {
	if !r.HasError() {
		t.Fatalf("CLI command should have failed but succeeded\nOutput: %s", r.Output)
	}
}
