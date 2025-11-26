package beyla

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// hasBeylaEmbedded returns true if a Beyla binary is embedded for the current platform.
func hasBeylaEmbedded() bool {
	return len(beylaEmbeddedBinary) > 0
}

// extractBeylaBinary extracts the embedded Beyla binary to a temporary file.
// Returns the path to the extracted binary.
func extractBeylaBinary() (string, error) {
	if !hasBeylaEmbedded() {
		return "", fmt.Errorf("no embedded Beyla binary for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	// Create a temporary directory for the extracted binary.
	tmpDir, err := os.MkdirTemp("", "coral-beyla-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Write the embedded binary to the temp directory.
	binaryPath := filepath.Join(tmpDir, "beyla")
	if err := os.WriteFile(binaryPath, beylaEmbeddedBinary, 0755); err != nil {
		_ = os.RemoveAll(tmpDir) // TODO: errcheck
		return "", fmt.Errorf("failed to write Beyla binary: %w", err)
	}

	return binaryPath, nil
}

// getBeylaBinaryPath returns the path to the Beyla binary.
// Priority order:
// 1. BEYLA_PATH environment variable (user-specified path)
// 2. Embedded binary (extracted to temp directory)
// 3. System PATH (beyla command available)
// 4. Error if none of the above.
func getBeylaBinaryPath() (string, error) {
	// 1. Check for BEYLA_PATH environment variable.
	if envPath := os.Getenv("BEYLA_PATH"); envPath != "" {
		if _, err := os.Stat(envPath); err == nil {
			return envPath, nil
		}
	}

	// 2. Check if we have an embedded binary.
	if hasBeylaEmbedded() {
		return extractBeylaBinary()
	}

	// 3. Check system PATH for beyla command.
	if path, err := findBeylaInPath(); err == nil {
		return path, nil
	}

	return "", fmt.Errorf("beyla binary not found: set BEYLA_PATH, embed binary via 'go generate', or install Beyla in PATH")
}

// findBeylaInPath searches for the beyla binary in system PATH.
func findBeylaInPath() (string, error) {
	// Check common installation locations.
	locations := []string{
		"/usr/local/bin/beyla",
		"/usr/bin/beyla",
		filepath.Join(os.Getenv("HOME"), ".local/bin/beyla"),
	}

	for _, loc := range locations {
		if _, err := os.Stat(loc); err == nil {
			return loc, nil
		}
	}

	return "", fmt.Errorf("beyla not found in PATH")
}
