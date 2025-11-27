package runtime

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	agentv1 "github.com/coral-io/coral/coral/agent/v1"
)

// Linux capability bit positions (from include/uapi/linux/capability.h).
const (
	capNetAdmin    = 12 // CAP_NET_ADMIN
	capSysPtrace   = 19 // CAP_SYS_PTRACE
	capSysAdmin    = 21 // CAP_SYS_ADMIN
	capSysResource = 24 // CAP_SYS_RESOURCE
	capDacOverride = 1  // CAP_DAC_OVERRIDE
	capSetuid      = 7  // CAP_SETUID
	capSetgid      = 8  // CAP_SETGID
	capPerfmon     = 38 // CAP_PERFMON (kernel 5.8+)
	capBpf         = 39 // CAP_BPF (kernel 5.8+)
)

// DetectLinuxCapabilities detects Linux capabilities granted to the current process.
// Returns nil capabilities on non-Linux platforms or if detection fails.
func DetectLinuxCapabilities() (*agentv1.LinuxCapabilities, error) {
	// Only supported on Linux.
	if runtime.GOOS != "linux" {
		return &agentv1.LinuxCapabilities{}, nil
	}

	// Read /proc/self/status to get capability bitmasks.
	capEff, err := readCapabilityBitmask("/proc/self/status", "CapEff")
	if err != nil {
		return nil, fmt.Errorf("failed to read capabilities: %w", err)
	}

	// Parse capabilities from effective capability bitmask.
	return &agentv1.LinuxCapabilities{
		CapNetAdmin:    hasCapability(capEff, capNetAdmin),
		CapSysAdmin:    hasCapability(capEff, capSysAdmin),
		CapSysPtrace:   hasCapability(capEff, capSysPtrace),
		CapSysResource: hasCapability(capEff, capSysResource),
		CapBpf:         hasCapability(capEff, capBpf),
		CapPerfmon:     hasCapability(capEff, capPerfmon),
		CapDacOverride: hasCapability(capEff, capDacOverride),
		CapSetuid:      hasCapability(capEff, capSetuid),
		CapSetgid:      hasCapability(capEff, capSetgid),
	}, nil
}

// DetectExecCapabilities determines available container execution modes.
func DetectExecCapabilities(linuxCaps *agentv1.LinuxCapabilities, hasCRI bool, hasSharedPIDNamespace bool) *agentv1.ExecCapabilities {
	// Check if nsenter mode is available (requires CAP_SYS_ADMIN + CAP_SYS_PTRACE).
	hasNsenter := linuxCaps.CapSysAdmin && linuxCaps.CapSysPtrace

	// Determine exec mode.
	var mode agentv1.ExecMode
	switch {
	case hasNsenter && hasSharedPIDNamespace:
		mode = agentv1.ExecMode_EXEC_MODE_NSENTER
	case hasCRI:
		mode = agentv1.ExecMode_EXEC_MODE_CRI
	default:
		mode = agentv1.ExecMode_EXEC_MODE_NONE
	}

	return &agentv1.ExecCapabilities{
		Mode:                 mode,
		MountNamespaceAccess: hasNsenter,
		PidNamespaceAccess:   hasNsenter && hasSharedPIDNamespace,
		HasSysAdmin:          linuxCaps.CapSysAdmin,
		HasSysPtrace:         linuxCaps.CapSysPtrace,
		HasSharedPidNs:       hasSharedPIDNamespace,
		CriSocketAvailable:   hasCRI,
	}
}

// readCapabilityBitmask reads a capability bitmask from /proc/self/status.
func readCapabilityBitmask(procStatusPath, capName string) (uint64, error) {
	file, err := os.Open(procStatusPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open %s: %w", procStatusPath, err)
	}
	defer file.Close() // nolint:errcheck

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, capName+":") {
			continue
		}

		// Parse capability bitmask (hex string after colon).
		// Format: "CapEff:\t00000000a80435fb"
		parts := strings.Fields(line)
		if len(parts) < 2 {
			return 0, fmt.Errorf("invalid %s format: %s", capName, line)
		}

		// Parse hex string to uint64.
		bitmask, err := strconv.ParseUint(parts[1], 16, 64)
		if err != nil {
			return 0, fmt.Errorf("failed to parse %s bitmask: %w", capName, err)
		}

		return bitmask, nil
	}

	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("failed to scan %s: %w", procStatusPath, err)
	}

	return 0, fmt.Errorf("%s not found in %s", capName, procStatusPath)
}

// hasCapability checks if a specific capability bit is set in the bitmask.
func hasCapability(bitmask uint64, capBit int) bool {
	return (bitmask & (1 << uint(capBit))) != 0
}
