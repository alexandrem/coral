//go:build linux

package colony

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// getInterfaceInfo returns information about WireGuard interfaces on Linux.
func getInterfaceInfo() string {
	// Check for active wg interfaces using 'ip link'
	cmd := exec.Command("ip", "link", "show")
	output, err := cmd.Output()
	if err != nil {
		return "wg0 (standard WireGuard interface)"
	}

	// Look for wg interfaces
	wgRegex := regexp.MustCompile(`\d+:\s+(wg\d+):`)
	matches := wgRegex.FindAllStringSubmatch(string(output), -1)

	if len(matches) > 0 {
		var interfaces []string
		for _, match := range matches {
			if len(match) > 1 {
				interfaces = append(interfaces, match[1])
			}
		}
		if len(interfaces) > 0 {
			return fmt.Sprintf("wg0 (found active: %s)", strings.Join(interfaces, ", "))
		}
	}

	return "wg0 (standard WireGuard interface)"
}
