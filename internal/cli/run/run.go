// Package run implements the coral run command for executing TypeScript scripts.
package run

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/coral-mesh/coral/internal/cli/helpers"
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
	colonyURL, err := helpers.GetColonyURL("")
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

	// Pass through stdin/stdout/stderr
	denoCmd.Stdin = os.Stdin
	denoCmd.Stdout = os.Stdout
	denoCmd.Stderr = os.Stderr

	// Set environment variables
	denoCmd.Env = append(os.Environ(),
		"CORAL_MODE=cli",
		"CORAL_COLONY_ADDR="+colonyHost,
	)

	// Execute
	if err := denoCmd.Run(); err != nil {
		return fmt.Errorf("script execution failed: %w", err)
	}

	return nil
}

// findDeno locates the Deno binary.
// Priority:
// 1. Embedded binaries (same directory as coral binary)
// 2. System PATH (fallback)
func findDeno() (string, error) {
	// Get coral binary path
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}

	exeDir := filepath.Dir(exePath)

	// Use runtime package for actual platform (not build-time env vars)
	platform := fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)

	// Check for embedded Deno binary (relative to executable)
	// The binaries are placed in the same directory as the coral binary during build
	embeddedDeno := filepath.Join(exeDir, fmt.Sprintf("deno-%s", platform))
	if _, err := os.Stat(embeddedDeno); err == nil {
		return embeddedDeno, nil
	}

	// Also try without platform suffix (for backwards compatibility)
	simpleDeno := filepath.Join(exeDir, "deno")
	if _, err := os.Stat(simpleDeno); err == nil {
		return simpleDeno, nil
	}

	// Fallback to system PATH
	systemDeno, err := exec.LookPath("deno")
	if err != nil {
		return "", fmt.Errorf("deno not found (checked %s, %s, and system PATH)", embeddedDeno, simpleDeno)
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
