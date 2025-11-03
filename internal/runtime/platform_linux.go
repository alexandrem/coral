//go:build linux

package runtime

import (
	"os"
	"os/exec"
	"strings"
)

// detectOSVersion detects Linux OS version and kernel.
func detectOSVersion() (string, string) {
	osVersion := detectLinuxOSVersion()
	kernel := detectLinuxKernel()
	return osVersion, kernel
}

// detectLinuxOSVersion detects the Linux distribution and version.
func detectLinuxOSVersion() string {
	// Try /etc/os-release first (standard).
	data, err := os.ReadFile("/etc/os-release")
	if err == nil {
		lines := strings.Split(string(data), "\n")
		var name, version string
		for _, line := range lines {
			if strings.HasPrefix(line, "NAME=") {
				name = strings.Trim(strings.TrimPrefix(line, "NAME="), "\"")
			} else if strings.HasPrefix(line, "VERSION=") {
				version = strings.Trim(strings.TrimPrefix(line, "VERSION="), "\"")
			}
		}
		if name != "" {
			if version != "" {
				return name + " " + version
			}
			return name
		}
	}

	// Fallback to lsb_release.
	cmd := exec.Command("lsb_release", "-d")
	output, err := cmd.Output()
	if err == nil {
		parts := strings.SplitN(string(output), ":", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(parts[1])
		}
	}

	return "Linux (unknown)"
}

// detectLinuxKernel detects the Linux kernel version.
func detectLinuxKernel() string {
	cmd := exec.Command("uname", "-r")
	output, err := cmd.Output()
	if err == nil {
		return strings.TrimSpace(string(output))
	}

	return "unknown"
}
