//go:build windows

package runtime

import (
	"os/exec"
	"strings"
)

// detectOSVersion detects Windows version and kernel.
func detectOSVersion() (string, string) {
	osVersion := detectWindowsOSVersion()
	kernel := detectWindowsKernel()
	return osVersion, kernel
}

// detectWindowsOSVersion detects the Windows version.
func detectWindowsOSVersion() string {
	cmd := exec.Command("cmd", "/c", "ver")
	output, err := cmd.Output()
	if err == nil {
		version := strings.TrimSpace(string(output))
		return version
	}

	return "Windows (unknown)"
}

// detectWindowsKernel detects the Windows kernel version.
func detectWindowsKernel() string {
	// Windows doesn't have a traditional kernel version like Linux.
	// Use the build number instead.
	cmd := exec.Command("cmd", "/c", "ver")
	output, err := cmd.Output()
	if err == nil {
		return strings.TrimSpace(string(output))
	}

	return "unknown"
}
