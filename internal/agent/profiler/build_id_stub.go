//go:build !linux

package profiler

import "fmt"

// ExtractBuildID is not supported on non-Linux platforms.
func ExtractBuildID(binaryPath string) (string, error) {
	return "", fmt.Errorf("build ID extraction not supported on this platform")
}

// ExtractBuildIDFromPID is not supported on non-Linux platforms.
func ExtractBuildIDFromPID(pid int) (string, error) {
	return "", fmt.Errorf("build ID extraction not supported on this platform")
}
