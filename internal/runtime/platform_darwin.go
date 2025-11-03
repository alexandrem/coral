//go:build darwin

package runtime

import (
	"os/exec"
	"strings"
)

// detectOSVersion detects macOS version and kernel.
func detectOSVersion() (string, string) {
	osVersion := detectMacOSVersion()
	kernel := detectDarwinKernel()
	return osVersion, kernel
}

// detectMacOSVersion detects the macOS version.
func detectMacOSVersion() string {
	cmd := exec.Command("sw_vers", "-productVersion")
	output, err := cmd.Output()
	if err == nil {
		version := strings.TrimSpace(string(output))
		return "macOS " + version
	}

	return "macOS (unknown)"
}

// detectDarwinKernel detects the Darwin kernel version.
func detectDarwinKernel() string {
	cmd := exec.Command("uname", "-r")
	output, err := cmd.Output()
	if err == nil {
		kernel := strings.TrimSpace(string(output))
		return "Darwin " + kernel
	}

	return "unknown"
}
