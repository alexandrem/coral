//go:build darwin

package colony

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// getInterfaceInfo returns information about WireGuard interfaces on macOS.
func getInterfaceInfo() string {
	// Check for active utun interfaces
	cmd := exec.Command("ifconfig")
	output, err := cmd.Output()
	if err != nil {
		return "utun* (next available will be assigned by macOS)"
	}

	// Look for utun interfaces
	utunRegex := regexp.MustCompile(`(utun\d+):`)
	matches := utunRegex.FindAllStringSubmatch(string(output), -1)

	if len(matches) > 0 {
		var numbers []int
		for _, match := range matches {
			if len(match) > 1 {
				// Extract number from utunN
				numStr := strings.TrimPrefix(match[1], "utun")
				if num, err := strconv.Atoi(numStr); err == nil {
					numbers = append(numbers, num)
				}
			}
		}
		if len(numbers) > 0 {
			// Find the highest number
			maxNum := 0
			for _, num := range numbers {
				if num > maxNum {
					maxNum = num
				}
			}
			nextNum := maxNum + 1
			return fmt.Sprintf("utun%d (next available, %d active utun interfaces)", nextNum, len(numbers))
		}
	}

	return "utun0 (no active utun interfaces)"
}
