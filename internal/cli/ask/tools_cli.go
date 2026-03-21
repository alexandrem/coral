package ask

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// buildCLITools returns the tool set for CLI dispatch mode (RFD 100).
// A single coral_cli meta-tool replaces the MCP tool suite, giving the LLM
// the same vocabulary as a human operator.
func buildCLITools() []mcp.Tool {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"args": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
				"description": `Coral subcommand and flags, e.g. ["query", "traces", "--service", "api", "--since", "10m"]. ` +
					`Do not include "coral" itself. --format json is appended automatically.`,
			},
		},
		"required": []string{"args"},
	}
	schemaBytes, _ := json.Marshal(schema)
	tool := mcp.NewToolWithRawSchema(
		"coral_cli",
		"Run a coral CLI command and return its JSON output. "+
			"Use this to query metrics, traces, logs, debug sessions, and service status. "+
			"Read coral://cli/reference first to see available commands.",
		schemaBytes,
	)
	return []mcp.Tool{tool}
}

// appendFormatJSON returns args with --format json appended if not already present.
func appendFormatJSON(args []string) []string {
	for i, arg := range args {
		switch arg {
		case "--format=json", "-o=json":
			return args
		case "--format", "-o":
			if i+1 < len(args) && args[i+1] == "json" {
				return args
			}
		}
	}
	return append(args, "--format", "json")
}

// cliCommandString formats args as a human-readable coral command for display.
func cliCommandString(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \t\"'") {
			parts[i] = fmt.Sprintf("%q", a)
		} else {
			parts[i] = a
		}
	}
	return "coral " + strings.Join(parts, " ")
}

// isScriptWrite returns true when args represent a "coral script write" call.
// Used to intercept the call for the TUI approval gate.
func isScriptWrite(args []string) bool {
	return len(args) >= 2 && args[0] == "script" && args[1] == "write"
}

// scriptWriteParams extracts the --name and --content values from a
// "script write" arg list.
func scriptWriteParams(args []string) (name, content string) {
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--name":
			name = args[i+1]
		case "--content":
			content = args[i+1]
		}
	}
	return
}

// executeCLITool runs coral <args> --format json as a subprocess (RFD 100).
// Non-zero exits are returned as MCP tool errors so the LLM can handle them.
// Go errors are only returned for unexpected failures (e.g., exec.LookPath).
func executeCLITool(ctx context.Context, args []string, debug bool) (*mcp.CallToolResult, float64, error) {
	start := time.Now()

	// Append --format json automatically.
	args = appendFormatJSON(args)

	// Use the current executable so the subprocess runs the same coral build.
	coralBin, err := os.Executable()
	if err != nil {
		coralBin = "coral"
	}

	if debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] coral_cli: %s %s\n", coralBin, strings.Join(args, " "))
	}

	cmd := exec.CommandContext(ctx, coralBin, args...) // #nosec G204
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	duration := time.Since(start).Seconds()

	if debug {
		fmt.Fprintf(os.Stderr, "[DEBUG] coral_cli stdout(%d bytes): %s\n", stdout.Len(), stdout.String())
		if stderr.Len() > 0 {
			fmt.Fprintf(os.Stderr, "[DEBUG] coral_cli stderr: %s\n", stderr.String())
		}
	}

	if runErr != nil {
		msg := fmt.Sprintf("coral %s failed: %v", strings.Join(args, " "), runErr)
		if stderr.Len() > 0 {
			msg += "\nstderr: " + strings.TrimSpace(stderr.String())
		}
		return mcp.NewToolResultError(msg), duration, nil
	}

	output := strings.TrimSpace(stdout.String())
	if output == "" {
		output = `{"result":"ok","output":""}`
	}
	return mcp.NewToolResultText(output), duration, nil
}
