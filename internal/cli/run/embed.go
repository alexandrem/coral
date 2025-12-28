package run

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// hasDenoEmbedded returns true if a Deno binary is embedded for the current platform.
func hasDenoEmbedded() bool {
	return len(denoEmbeddedBinary) > 0
}

// extractDenoBinary extracts the embedded Deno binary to a temporary file.
// Returns the path to the extracted binary.
func extractDenoBinary() (string, error) {
	if !hasDenoEmbedded() {
		return "", fmt.Errorf("no embedded Deno binary for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	// Create a temporary directory for the extracted binary.
	tmpDir, err := os.MkdirTemp("", "coral-deno-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Write the embedded binary to the temp directory.
	binaryPath := filepath.Join(tmpDir, "deno")
	//nolint:gosec // G306: Binary needs execute permissions
	if err := os.WriteFile(binaryPath, denoEmbeddedBinary, 0755); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", fmt.Errorf("failed to write Deno binary: %w", err)
	}

	return binaryPath, nil
}
