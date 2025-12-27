// Package run implements the coral run command for executing TypeScript scripts.
package run

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	colonyAddr, err := helpers.GetColonyURL("")
	if err != nil {
		// Not fatal - script may not need colony access
		colonyAddr = "localhost:9090"
	}

	// Build Deno command arguments
	args := []string{
		"run",
		"--allow-net=" + colonyAddr,
		"--allow-read=./",
	}

	// Add watch mode if requested
	if watch {
		args = append(args, "--watch")
	}

	// Add script path
	args = append(args, scriptPath)

	// Create command
	denoCmd := exec.CommandContext(ctx, denoPath, args...)

	// Pass through stdin/stdout/stderr
	denoCmd.Stdin = os.Stdin
	denoCmd.Stdout = os.Stdout
	denoCmd.Stderr = os.Stderr

	// Set environment variables
	denoCmd.Env = append(os.Environ(),
		"CORAL_MODE=cli",
		"CORAL_COLONY_ADDR="+colonyAddr,
	)

	// Execute
	if err := denoCmd.Run(); err != nil {
		return fmt.Errorf("script execution failed: %w", err)
	}

	return nil
}

// findDeno locates the Deno binary.
// Priority:
// 1. Embedded binaries (internal/cli/run/binaries/)
// 2. System PATH (fallback)
func findDeno() (string, error) {
	// Get coral binary path
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}

	exeDir := filepath.Dir(exePath)

	// Determine platform string for embedded binary
	goos := os.Getenv("GOOS")
	if goos == "" {
		goos = "linux" // Default for runtime
	}
	goarch := os.Getenv("GOARCH")
	if goarch == "" {
		goarch = "amd64" // Default
	}

	// Try to detect actual runtime platform if GOOS/GOARCH not set
	if os.Getenv("GOOS") == "" {
		goos = detectOS()
	}
	if os.Getenv("GOARCH") == "" {
		goarch = detectArch()
	}

	platform := fmt.Sprintf("%s-%s", goos, goarch)

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

// detectOS returns the current operating system.
func detectOS() string {
	switch os.Getenv("GOOS") {
	case "linux":
		return "linux"
	case "darwin":
		return "darwin"
	case "windows":
		return "windows"
	default:
		// Runtime detection
		if _, err := os.Stat("/proc"); err == nil {
			return "linux"
		}
		if _, err := os.Stat("/System/Library"); err == nil {
			return "darwin"
		}
		return "linux" // Default
	}
}

// detectArch returns the current architecture.
func detectArch() string {
	switch os.Getenv("GOARCH") {
	case "amd64":
		return "amd64"
	case "arm64":
		return "arm64"
	default:
		return "amd64" // Default
	}
}
