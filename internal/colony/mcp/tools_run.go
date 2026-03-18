package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/coral-mesh/coral/internal/cli/run"
)

// sdkReferenceText is the compact SDK index served by the coral://sdk/reference
// MCP Resource. It is returned verbatim when the LLM reads the resource.
const sdkReferenceText = `@coral/sdk — Coral TypeScript SDK Reference

PRIMITIVES
  import * as coral from "@coral/sdk";

  coral.services.list(namespace?)                     → Service[]
  coral.services.get(name, namespace?)                → Service | null

  coral.metrics.getPercentile(svc, metric, pct, windowMs?)  → MetricValue  (ns)
  coral.metrics.getP99(svc, metric, windowMs?)              → MetricValue  (ns)
  coral.metrics.getP95(svc, metric, windowMs?)              → MetricValue  (ns)
  coral.metrics.getP50(svc, metric, windowMs?)              → MetricValue  (ns)

  coral.activity.listServiceActivity(windowMs?)       → ServiceActivity[]
  coral.activity.getServiceActivity(svc, windowMs?)   → ServiceActivity | null
  coral.activity.getServiceErrors(svc, windowMs?)     → ServiceErrors

  coral.traces.findSlow(svc, minDurationNs, windowMs?) → Trace[]
  coral.traces.findErrors(svc, windowMs?)              → Trace[]

  coral.system.getMetrics(svc)                        → SystemMetrics

  coral.db.query(sql)                                 → Row[]

SKILLS  (import from "@coral/sdk/skills/<name>")
  latency-report         Check P99 latency and error rates across services.
  error-correlation      Detect cascading failures via cross-service error spikes.
  memory-leak-detector   Identify services with sustained heap growth over a window.

  Usage:
    import { latencyReport } from "@coral/sdk/skills/latency-report";
    const result = await latencyReport({ threshold_ms: 500 });
    console.log(JSON.stringify(result));

TYPES
  interface SkillResult {
    summary:          string;                                      // one-line finding
    status:           "healthy" | "warning" | "critical" | "unknown";
    data:             Record<string, unknown>;                     // skill-specific
    recommendations?: string[];                                    // optional next steps
  }

OUTPUT CONVENTION
  stderr → progress logs (relayed to user terminal in real time).
  stdout → final JSON result (returned to LLM as the tool result).
  Skills return SkillResult. Free-form scripts may return any JSON object.
  status: "healthy" | "warning" | "critical" | "unknown"

  Example:
    console.error("Checking services...");        // progress → terminal
    console.log(JSON.stringify(result));           // result   → LLM
`

// RunInput is the input schema for the coral_run tool (RFD 093).
type RunInput struct {
	// Code is the TypeScript source to execute. Must write JSON to stdout last.
	Code string `json:"code" jsonschema:"description=TypeScript source to execute. Write progress to stderr (console.error) and the final JSON result to stdout (console.log). Import the SDK via: import * as coral from '@coral/sdk';"`
	// Timeout is the execution timeout in seconds (default: 60, max: 300).
	Timeout *int `json:"timeout,omitempty" jsonschema:"description=Execution timeout in seconds. Default: 60. Max: 300.,minimum=1,maximum=300"`
}

// registerRunTool registers the coral_run tool (RFD 093).
func (s *Server) registerRunTool() {
	if !s.isToolEnabled("coral_run") {
		return
	}

	inputSchema, err := generateInputSchema(RunInput{})
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to generate input schema for coral_run")
		return
	}

	schemaBytes, err := json.Marshal(inputSchema)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to marshal schema for coral_run")
		return
	}

	tool := mcpgo.NewToolWithRawSchema(
		"coral_run",
		`Execute TypeScript using the Coral SDK and return structured output.
IMPORTANT: Before writing code, you MUST read coral://sdk/reference to discover available SDK primitives and built-in skills.
Write progress messages to stderr (console.error) and the final JSON result to stdout (console.log). Only stdout is returned to you.`,
		schemaBytes,
	)

	s.mcpServer.AddTool(tool, func(ctx context.Context, request mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		var input RunInput
		if request.Params.Arguments != nil {
			argBytes, err := json.Marshal(request.Params.Arguments)
			if err != nil {
				return mcpgo.NewToolResultError(fmt.Sprintf("failed to marshal arguments: %v", err)), nil
			}
			if err := json.Unmarshal(argBytes, &input); err != nil {
				return mcpgo.NewToolResultError(fmt.Sprintf("failed to parse arguments: %v", err)), nil
			}
		}

		if strings.TrimSpace(input.Code) == "" {
			return mcpgo.NewToolResultError("code must not be empty"), nil
		}

		s.auditToolCall("coral_run", map[string]any{"code_len": len(input.Code)})

		return s.executeRunTool(ctx, input)
	})
}

// executeRunTool executes the coral_run tool logic.
func (s *Server) executeRunTool(ctx context.Context, input RunInput) (*mcpgo.CallToolResult, error) {
	timeoutSec := 60
	if input.Timeout != nil {
		timeoutSec = *input.Timeout
		if timeoutSec > 300 {
			timeoutSec = 300
		}
	}

	// Capture stderr to a buffer so we can include the last few lines in the
	// error message when the script fails. It is also written to os.Stderr in
	// real time so the user sees progress output while the script runs.
	var stderrBuf bytes.Buffer
	opts := run.ExecuteInlineOptions{
		Timeout: time.Duration(timeoutSec) * time.Second,
		Stderr:  io.MultiWriter(os.Stderr, &stderrBuf),
	}

	result, err := run.ExecuteInline(ctx, input.Code, opts)
	if err != nil {
		// Include the last few stderr lines for context.
		exitCode := 1
		if result != nil {
			exitCode = result.ExitCode
		}
		errMsg := fmt.Sprintf("script execution failed (exit %d): %v", exitCode, err)
		if stderr := strings.TrimSpace(stderrBuf.String()); stderr != "" {
			errMsg += fmt.Sprintf("\nstderr:\n%s", truncateLast(stderr, 1000))
		}
		if result != nil && strings.TrimSpace(result.Stdout) != "" {
			// Partial stdout may help debug.
			errMsg += fmt.Sprintf("\nstdout: %s", truncateLast(result.Stdout, 500))
		}
		return mcpgo.NewToolResultError(errMsg), nil
	}

	stdout := strings.TrimSpace(result.Stdout)
	if stdout == "" {
		return mcpgo.NewToolResultError(
			"script produced no stdout. Write the result with console.log(JSON.stringify(result)).",
		), nil
	}

	return mcpgo.NewToolResultText(stdout), nil
}

// executeRunToolByArgs parses argumentsJSON and runs the coral_run tool.
// Used by Server.ExecuteTool for RPC dispatch.
func (s *Server) executeRunToolByArgs(ctx context.Context, argumentsJSON string) (string, error) {
	var input RunInput
	if err := json.Unmarshal([]byte(argumentsJSON), &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	res, err := s.executeRunTool(ctx, input)
	if err != nil {
		return "", err
	}
	if len(res.Content) > 0 {
		if text, ok := res.Content[0].(mcpgo.TextContent); ok {
			return text.Text, nil
		}
	}
	return "", nil
}

// registerSDKReferenceResource registers the coral://sdk/reference MCP Resource
// (RFD 093). It serves a compact plain-text SDK index for on-demand discovery
// by the LLM before writing coral_run scripts.
func (s *Server) registerSDKReferenceResource() {
	s.mcpServer.AddResource(
		mcpgo.Resource{
			URI:         "coral://sdk/reference",
			Name:        "Coral SDK Reference",
			Description: "Compact index of Coral TypeScript SDK primitives and built-in skills. Read this before writing coral_run scripts.",
			MIMEType:    "text/plain",
		},
		func(_ context.Context, _ mcpgo.ReadResourceRequest) ([]mcpgo.ResourceContents, error) {
			return []mcpgo.ResourceContents{
				mcpgo.TextResourceContents{
					URI:      "coral://sdk/reference",
					MIMEType: "text/plain",
					Text:     sdkReferenceText,
				},
			}, nil
		},
	)
}

// truncateLast returns the last n bytes of s, prefixed with "..." when trimmed.
func truncateLast(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
