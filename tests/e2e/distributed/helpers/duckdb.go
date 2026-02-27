package helpers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// DuckDBListAgents executes `coral duckdb list` and returns result.
func DuckDBListAgents(ctx context.Context, env *CLITestEnv) *CLIResult {
	return env.Run(ctx, "duckdb", "list")
}

// DuckDBListAgentsJSON executes `coral duckdb list -o json` and parses output.
func DuckDBListAgentsJSON(ctx context.Context, env *CLITestEnv) ([]map[string]interface{}, error) {
	result := env.Run(ctx, "duckdb", "list", "-o", "json")
	if result.Err != nil {
		return nil, fmt.Errorf("duckdb list-agents failed: %w\nOutput: %s", result.Err, result.Output)
	}

	var agents []map[string]interface{}
	if err := json.Unmarshal([]byte(result.Output), &agents); err != nil {
		return nil, fmt.Errorf("failed to parse JSON output: %w\nOutput: %s", err, result.Output)
	}
	return agents, nil
}

// DuckDBQuery executes `coral duckdb query <agentID> <sql>` and returns result.
func DuckDBQuery(ctx context.Context, env *CLITestEnv, agentID, sql string) *CLIResult {
	return env.Run(ctx, "duckdb", "query", agentID, sql)
}

// DuckDBQueryJSON executes `coral duckdb query <agentID> <sql> -o json` and parses output.
func DuckDBQueryJSON(ctx context.Context, env *CLITestEnv, agentID, sql string) ([]map[string]interface{}, error) {
	result := env.Run(ctx, "duckdb", "query", agentID, sql, "-o", "json")
	if result.Err != nil {
		return nil, fmt.Errorf("duckdb query failed: %w\nOutput: %s", result.Err, result.Output)
	}

	var results []map[string]interface{}
	if err := json.Unmarshal([]byte(result.Output), &results); err != nil {
		return nil, fmt.Errorf("failed to parse JSON output: %w\nOutput: %s", err, result.Output)
	}
	return results, nil
}

// DuckDBShell executes `coral duckdb shell <agentID>` with provided standard input.
func DuckDBShell(ctx context.Context, env *CLITestEnv, agentID string, stdin io.Reader) *CLIResult {
	return env.RunWithStdin(ctx, stdin, "duckdb", "shell", agentID)
}
