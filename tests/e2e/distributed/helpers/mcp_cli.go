package helpers

import (
	"context"
	"encoding/json"
	"fmt"
)

// MCPListTools executes `coral colony mcp list-tools` and returns the output.
func MCPListTools(ctx context.Context, colonyEndpoint string) *CLIResult {
	return RunCLIWithEnv(ctx, map[string]string{
		"CORAL_COLONY_ENDPOINT": colonyEndpoint,
	}, "colony", "mcp", "list-tools")
}

// MCPListToolsJSON executes `coral colony mcp list-tools --format json` and parses the output.
func MCPListToolsJSON(ctx context.Context, colonyEndpoint string) ([]map[string]interface{}, error) {
	result := RunCLIWithEnv(ctx, map[string]string{
		"CORAL_COLONY_ENDPOINT": colonyEndpoint,
	}, "colony", "mcp", "list-tools", "-o", "json")

	if result.Err != nil {
		return nil, fmt.Errorf("mcp list-tools failed: %w\nOutput: %s", result.Err, result.Output)
	}

	var tools []map[string]interface{}
	if err := json.Unmarshal([]byte(result.Output), &tools); err != nil {
		return nil, fmt.Errorf("failed to parse JSON output: %w\nOutput: %s", err, result.Output)
	}

	return tools, nil
}

// MCPTestTool executes `coral colony mcp test-tool <toolName>` and returns the output.
func MCPTestTool(ctx context.Context, colonyEndpoint, toolName, argsJSON string) *CLIResult {
	args := []string{"colony", "mcp", "test-tool", toolName}

	if argsJSON != "" {
		args = append(args, "--args", argsJSON)
	}

	return RunCLIWithEnv(ctx, map[string]string{
		"CORAL_COLONY_ENDPOINT": colonyEndpoint,
	}, args...)
}

// MCPGenerateConfig executes `coral colony mcp generate-config` and returns the output.
func MCPGenerateConfig(ctx context.Context, colonyEndpoint, colonyID string, allColonies bool) *CLIResult {
	args := []string{"colony", "mcp", "generate-config"}

	if allColonies {
		args = append(args, "--all-colonies")
	} else if colonyID != "" {
		args = append(args, "--colony", colonyID)
	}

	return RunCLIWithEnv(ctx, map[string]string{
		"CORAL_COLONY_ENDPOINT": colonyEndpoint,
	}, args...)
}
