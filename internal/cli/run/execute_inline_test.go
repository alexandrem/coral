package run

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecuteInline_SimpleScript verifies that ExecuteInline captures stdout
// and returns it in ExecuteInlineResult.
func TestExecuteInline_SimpleScript(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Deno execution test in short mode")
	}

	result, err := ExecuteInline(context.Background(), `
		console.log(JSON.stringify({ summary: "ok", status: "healthy", data: {} }));
	`, ExecuteInlineOptions{})
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &parsed))
	assert.Equal(t, "ok", parsed["summary"])
	assert.Equal(t, "healthy", parsed["status"])
}

// TestExecuteInline_StderrNotCaptured verifies that stderr output does not
// appear in Stdout — only stdout is captured.
func TestExecuteInline_StderrNotCaptured(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Deno execution test in short mode")
	}

	result, err := ExecuteInline(context.Background(), `
		console.error("progress: checking service");
		console.log(JSON.stringify({ summary: "done", status: "healthy", data: {} }));
	`, ExecuteInlineOptions{Stderr: nil})
	require.NoError(t, err)

	// Stdout should only contain the JSON line, not the stderr progress message.
	assert.NotContains(t, result.Stdout, "progress: checking service",
		"stderr output must not appear in captured stdout")

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &parsed))
	assert.Equal(t, "done", parsed["summary"])
}

// TestExecuteInline_MultipleStdoutLines verifies that all stdout lines are
// captured, including non-JSON lines before the final result.
func TestExecuteInline_MultipleStdoutLines(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Deno execution test in short mode")
	}

	result, err := ExecuteInline(context.Background(), `
		console.log("line1");
		console.log("line2");
		console.log(JSON.stringify({ summary: "multi", status: "healthy", data: {} }));
	`, ExecuteInlineOptions{})
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	assert.Equal(t, 3, len(lines), "should capture all 3 stdout lines")
	assert.Equal(t, "line1", lines[0])
	assert.Equal(t, "line2", lines[1])
	assert.Contains(t, lines[2], `"multi"`)
}

// TestExecuteInline_NonZeroExitCode verifies that a script that calls
// Deno.exit(1) causes an error return with ExitCode=1.
func TestExecuteInline_NonZeroExitCode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Deno execution test in short mode")
	}

	result, err := ExecuteInline(context.Background(), `
		Deno.exit(1);
	`, ExecuteInlineOptions{})

	assert.Error(t, err, "non-zero exit should return error")
	assert.NotNil(t, result)
	assert.Equal(t, 1, result.ExitCode)
}

// TestExecuteInline_Timeout verifies that ExecuteInline respects the timeout.
func TestExecuteInline_Timeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Deno execution test in short mode")
	}

	ctx := context.Background()
	start := time.Now()
	_, err := ExecuteInline(ctx, `
		// Run longer than the timeout.
		await new Promise(resolve => setTimeout(resolve, 10_000));
		console.log("done");
	`, ExecuteInlineOptions{Timeout: 500 * time.Millisecond})

	elapsed := time.Since(start)
	assert.Error(t, err, "timeout should produce an error")
	// Should finish well within 5s despite the 10s sleep in the script.
	assert.Less(t, elapsed, 5*time.Second, "should have been killed by timeout")
}

// TestExecuteInline_SDKImport verifies that a script can import @coral/sdk
// without a network connection (import map should resolve it from the embedded
// SDK). The script only uses the type declarations (no RPC call) to avoid
// requiring a live colony.
func TestExecuteInline_SDKImport(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Deno execution test in short mode")
	}

	// Import type-only — does not make any RPC calls.
	result, err := ExecuteInline(context.Background(), `
		import type { SkillResult } from "@coral/sdk";
		const r: SkillResult = {
			summary: "import ok",
			status: "healthy",
			data: { imported: true },
		};
		console.log(JSON.stringify(r));
	`, ExecuteInlineOptions{})
	require.NoError(t, err, "SDK type import should succeed without network")

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &parsed))
	assert.Equal(t, "import ok", parsed["summary"])
	data, ok := parsed["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, data["imported"])
}

// TestExecuteInline_SkillsImport verifies that a script can import a skill
// from @coral/sdk/skills/<name>. The skill itself is imported but not called
// (which would require a live colony), so we just verify the import and
// function type are correct.
func TestExecuteInline_SkillsImport(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Deno execution test in short mode")
	}

	result, err := ExecuteInline(context.Background(), `
		import { latencyReport } from "@coral/sdk/skills/latency-report";
		const isFunc = typeof latencyReport === "function";
		console.log(JSON.stringify({ summary: "skills import ok", status: "healthy", data: { isFunc } }));
	`, ExecuteInlineOptions{})
	require.NoError(t, err, "skills import should succeed")

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &parsed))
	assert.Equal(t, "skills import ok", parsed["summary"])
	data, ok := parsed["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, data["isFunc"], "latencyReport should be a function")
}

// TestExtractSDK verifies that all TypeScript SDK files are extracted to the
// destination directory, including the skills sub-package.
func TestExtractSDK(t *testing.T) {
	t.TempDir() // warm up

	dir := t.TempDir()
	err := extractSDK(dir)
	require.NoError(t, err)

	// Check for required top-level SDK files.
	required := []string{
		"mod.ts",
		"types.ts",
		"metrics.ts",
		"services.ts",
		"activity.ts",
		"db.ts",
		"client.ts",
	}
	for _, f := range required {
		path := dir + "/" + f
		info := require.New(t)
		info.FileExists(path, "SDK file should be extracted: %s", f)
	}

	// Check for skills sub-package.
	skillFiles := []string{
		"skills/mod.ts",
		"skills/latency-report.ts",
		"skills/error-correlation.ts",
		"skills/memory-leak-detector.ts",
	}
	for _, f := range skillFiles {
		path := dir + "/" + f
		info := require.New(t)
		info.FileExists(path, "skill file should be extracted: %s", f)
	}
}
