// Package run implements the coral run command for executing TypeScript scripts.
package run

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/coral-mesh/coral/internal/cli/terminal"
	"github.com/coral-mesh/coral/internal/config"
	tsdk "github.com/coral-mesh/coral/pkg/sdk/typescript"
)

// NewRunCmd creates the 'run' command.
func NewRunCmd() *cobra.Command {
	var (
		timeout int
		watch   bool
	)

	cmd := &cobra.Command{
		Use:   "run <script.ts>",
		Short: "Execute TypeScript script with Deno",
		Long: `Execute a TypeScript script locally using embedded Deno runtime.

Scripts have sandboxed access to:
- Colony gRPC API (via @coral/sdk)
- Local file reads (for imports)
- Console output (stdout/stderr)

Scripts CANNOT:
- Write to filesystem
- Execute shell commands
- Access environment variables (except CORAL_*)

Examples:
  coral run analysis.ts
  coral run latency-report.ts --timeout 120
  coral run dashboard.ts --watch
`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scriptPath := args[0]
			ctx := context.Background()

			// Apply timeout if specified
			if timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
				defer cancel()
			}

			// Execute script
			return executeScript(ctx, scriptPath, watch)
		},
	}

	cmd.Flags().IntVar(&timeout, "timeout", 60, "Script timeout in seconds (default: 60)")
	cmd.Flags().BoolVar(&watch, "watch", false, "Re-run script on file changes")

	return cmd
}

// executeScript runs the TypeScript script using Deno.
func executeScript(ctx context.Context, scriptPath string, watch bool) error {
	// Find Deno binary
	denoPath, err := findDeno()
	if err != nil {
		return fmt.Errorf("failed to find Deno binary: %w", err)
	}

	// Resolve colony address for --allow-net permission
	var colonyURL string
	resolver, err := config.NewResolver()
	if err == nil {
		colonyURL, err = resolver.ResolveColonyURL("")
	}
	if err != nil {
		// Not fatal - script may not need colony access
		colonyURL = "http://localhost:9090"
	}

	// Extract host:port from URL for --allow-net
	// Deno only accepts domain/IP, not full URLs
	colonyHost := extractHost(colonyURL)

	// Build Deno command arguments
	args := []string{
		"run",
		"--allow-net=" + colonyHost,
		"--allow-read=./",
		"--allow-env=CORAL_COLONY_ADDR,CORAL_MODE",
	}

	// Add watch mode if requested
	if watch {
		args = append(args, "--watch")
	}

	// Add script path
	args = append(args, scriptPath)

	// Create command
	//nolint:gosec // denoPath is from trusted findDeno(), not user input
	denoCmd := exec.CommandContext(ctx, denoPath, args...)

	// Capture stdout while still relaying it to the terminal.
	// io.MultiWriter fans out writes to both os.Stdout and a capture buffer.
	// This allows the render bridge to inspect the output for a SkillResult.render
	// field without breaking the existing real-time output behaviour.
	var stdoutBuf bytes.Buffer
	denoCmd.Stdin = os.Stdin
	denoCmd.Stdout = io.MultiWriter(os.Stdout, &stdoutBuf)
	denoCmd.Stderr = os.Stderr

	// Set environment variables
	denoCmd.Env = append(os.Environ(),
		"CORAL_MODE=cli",
		"CORAL_COLONY_ADDR="+colonyHost,
	)

	// Execute
	runErr := denoCmd.Run()

	// Push render event to the browser dashboard if coral terminal is running.
	// This is a best-effort operation: errors are silently ignored so they don't
	// affect the exit code of coral run.
	pushRenderEvent(&stdoutBuf, filepath.Base(scriptPath))

	if runErr != nil {
		return fmt.Errorf("script execution failed: %w", runErr)
	}

	return nil
}

// ExecuteInlineOptions configures the execution of inline TypeScript code.
type ExecuteInlineOptions struct {
	// Timeout limits total execution time. Zero means no timeout.
	Timeout time.Duration
	// Stderr is where the script's stderr output is written (real-time relay).
	// Defaults to os.Stderr when nil.
	Stderr io.Writer
}

// ExecuteInlineResult is the result of an inline script execution.
type ExecuteInlineResult struct {
	// Stdout is the captured standard output from the script.
	Stdout string
	// ExitCode is the process exit code (0 on success).
	ExitCode int
}

// ExecuteInline runs inline TypeScript code via the embedded Deno runtime.
// It writes the code to a temp file, extracts the embedded TypeScript SDK,
// generates a deno.json import map so scripts can import from "@coral/sdk",
// then invokes Deno. Stderr is forwarded to opts.Stderr in real time; stdout
// is captured and returned in ExecuteInlineResult.
func ExecuteInline(ctx context.Context, code string, opts ExecuteInlineOptions) (*ExecuteInlineResult, error) {
	denoPath, err := findDeno()
	if err != nil {
		return nil, fmt.Errorf("failed to find Deno binary: %w", err)
	}

	// Resolve colony address for --allow-net permission.
	colonyURL := "http://localhost:9090"
	if resolver, rerr := config.NewResolver(); rerr == nil {
		if resolved, rerr := resolver.ResolveColonyURL(""); rerr == nil {
			colonyURL = resolved
		}
	}
	colonyHost := extractHost(colonyURL)

	// Create a single temp directory to hold the script, the import map, and
	// the extracted SDK. Using one directory lets --allow-read=./ cover all.
	tmpDir, err := os.MkdirTemp("", "coral-run-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Extract the embedded TypeScript SDK into tmpDir/sdk/.
	sdkDir := filepath.Join(tmpDir, "sdk")
	if err := extractSDK(sdkDir); err != nil {
		return nil, fmt.Errorf("failed to extract SDK: %w", err)
	}

	// Write the inline code to tmpDir/script.ts.
	scriptPath := filepath.Join(tmpDir, "script.ts")
	if err := os.WriteFile(scriptPath, []byte(code), 0o600); err != nil {
		return nil, fmt.Errorf("failed to write script: %w", err)
	}

	// Generate a deno.json import map so "@coral/sdk" resolves to the
	// extracted SDK without network access.
	imports := map[string]string{
		"@coral/sdk":         "./sdk/mod.ts",
		"@coral/sdk/skills/": "./sdk/skills/",
	}

	// Dynamic skill mapping to allow extension-less imports (e.g. @coral/sdk/skills/latency-report)
	skillFiles, _ := os.ReadDir(filepath.Join(tmpDir, "sdk", "skills"))
	for _, f := range skillFiles {
		if !f.IsDir() && strings.HasSuffix(f.Name(), ".ts") {
			name := strings.TrimSuffix(f.Name(), ".ts")
			imports["@coral/sdk/skills/"+name] = "./sdk/skills/" + f.Name()
		}
	}

	importMap := map[string]interface{}{
		"imports": imports,
	}
	importMapBytes, err := json.Marshal(importMap)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal import map: %w", err)
	}
	denoJSONPath := filepath.Join(tmpDir, "deno.json")
	if err := os.WriteFile(denoJSONPath, importMapBytes, 0o600); err != nil {
		return nil, fmt.Errorf("failed to write deno.json: %w", err)
	}

	// Build Deno arguments.
	args := []string{
		"run",
		"--no-remote",
		"--allow-net=" + colonyHost,
		"--allow-read=./",
		"--allow-env=CORAL_COLONY_ADDR,CORAL_MODE",
		"--config=" + denoJSONPath,
		scriptPath,
	}

	// Apply context timeout if requested.
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	//nolint:gosec // denoPath is from trusted findDeno(), args are controlled.
	denoCmd := exec.CommandContext(ctx, denoPath, args...)
	denoCmd.Dir = tmpDir

	var stdoutBuf bytes.Buffer
	denoCmd.Stdin = nil

	stderrW := io.Writer(os.Stderr)
	if opts.Stderr != nil {
		stderrW = opts.Stderr
	}

	denoCmd.Stdout = &stdoutBuf
	denoCmd.Stderr = stderrW
	denoCmd.Env = append(os.Environ(),
		"CORAL_MODE=cli",
		"CORAL_COLONY_ADDR="+colonyHost,
	)

	runErr := denoCmd.Run()

	result := &ExecuteInlineResult{
		Stdout: stdoutBuf.String(),
	}
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			return result, runErr
		}
		return result, runErr
	}

	return result, nil
}

// extractSDK extracts the embedded TypeScript SDK file tree to destDir.
func extractSDK(destDir string) error {
	return fs.WalkDir(tsdk.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dest := filepath.Join(destDir, path)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o700)
		}
		data, err := tsdk.FS.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0o600)
	})
}

// skillResultRender is used to extract the render field from stdout JSON.
type skillResultRender struct {
	Render *terminal.RenderSpec `json:"render"`
}

// pushRenderEvent scans captured stdout for a SkillResult.render field and
// pushes a RenderEvent to the active terminal server. No-op when the server
// is nil (coral terminal is not running).
func pushRenderEvent(buf *bytes.Buffer, skillName string) {
	srv := terminal.GetActiveServer()
	if srv == nil {
		return
	}

	data := buf.Bytes()
	if len(data) == 0 {
		return
	}

	// Find the last JSON object in the output.
	line := lastJSONLine(data)
	if len(line) == 0 {
		return
	}

	var result skillResultRender
	if err := json.Unmarshal(line, &result); err != nil || result.Render == nil {
		return
	}

	srv.Push(terminal.RenderEvent{
		ID:        uuid.New().String(),
		TS:        time.Now().UnixMilli(),
		SkillName: skillName,
		Spec:      *result.Render,
	})
}

// lastJSONLine scans data for the last line that looks like a JSON object.
func lastJSONLine(data []byte) []byte {
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) > 0 && line[0] == '{' {
			return line
		}
	}
	return nil
}

// findDeno locates the Deno binary.
// Priority:
// 1. Embedded binary (extracted from binary)
// 2. External binaries (same directory as coral binary)
// 3. System PATH (fallback)
func findDeno() (string, error) {
	// Try to extract embedded binary first.
	embeddedDeno, err := extractDenoBinary()
	if err == nil {
		return embeddedDeno, nil
	}

	// Get coral binary path for checking external binaries.
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}

	exeDir := filepath.Dir(exePath)

	// Use runtime package for actual platform (not build-time env vars)
	platform := fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)

	// Check for external Deno binary (relative to executable)
	// The binaries are placed in the same directory as the coral binary during build
	externalDeno := filepath.Join(exeDir, fmt.Sprintf("deno-%s", platform))
	if _, err := os.Stat(externalDeno); err == nil {
		return externalDeno, nil
	}

	// Also try without platform suffix (for backwards compatibility)
	simpleDeno := filepath.Join(exeDir, "deno")
	if _, err := os.Stat(simpleDeno); err == nil {
		return simpleDeno, nil
	}

	// Fallback to system PATH
	systemDeno, err := exec.LookPath("deno")
	if err != nil {
		return "", fmt.Errorf("deno not found (checked embedded, %s, %s, and system PATH)", externalDeno, simpleDeno)
	}

	return systemDeno, nil
}

// extractHost extracts the host:port from a URL.
// Examples:
//   - "http://localhost:9090" -> "localhost:9090"
//   - "https://colony.example.com:443" -> "colony.example.com:443"
//   - "localhost:9090" -> "localhost:9090"
func extractHost(urlStr string) string {
	// Try to parse as URL
	u, err := url.Parse(urlStr)
	if err == nil && u.Host != "" {
		return u.Host
	}

	// If parsing failed or no host, assume it's already host:port
	return urlStr
}
