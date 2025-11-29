package ebpf

import (
	"os"
	"runtime"
	"strings"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
)

// detectCapabilities detects available eBPF capabilities on this system.
func detectCapabilities() *agentv1.EbpfCapabilities {
	// Check if running on Linux (eBPF only works on Linux).
	if runtime.GOOS != "linux" {
		return &agentv1.EbpfCapabilities{
			Supported:           false,
			KernelVersion:       runtime.GOOS + " (not Linux)",
			BtfAvailable:        false,
			CapBpf:              false,
			AvailableCollectors: []agentv1.EbpfCollectorKind{},
		}
	}

	// Get kernel version.
	kernelVersion := getKernelVersion()

	// Check for BTF support.
	btfAvailable := checkBTF()

	// Check for CAP_BPF capability (simplified check).
	capBPF := checkCapBPF()

	// Determine supported collectors based on capabilities.
	// For minimal implementation, only support syscall stats.
	collectors := []agentv1.EbpfCollectorKind{
		agentv1.EbpfCollectorKind_EBPF_COLLECTOR_KIND_SYSCALL_STATS,
		agentv1.EbpfCollectorKind_EBPF_COLLECTOR_KIND_UPROBE,
	}

	return &agentv1.EbpfCapabilities{
		Supported:           true,
		KernelVersion:       kernelVersion,
		BtfAvailable:        btfAvailable,
		CapBpf:              capBPF,
		AvailableCollectors: collectors,
	}
}

// getKernelVersion reads the kernel version from /proc/version.
func getKernelVersion() string {
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return "unknown"
	}

	// Parse version from output like "Linux version 5.15.0-xxx...".
	version := string(data)
	if idx := strings.Index(version, "Linux version "); idx >= 0 {
		version = version[idx+14:] // Skip "Linux version ".
		if idx := strings.Index(version, " "); idx >= 0 {
			version = version[:idx]
		}
		return version
	}

	return "unknown"
}

// checkBTF checks if BTF (BPF Type Format) is available.
// BTF is required for CO-RE (Compile Once, Run Everywhere) support.
func checkBTF() bool {
	// Check for /sys/kernel/btf/vmlinux.
	_, err := os.Stat("/sys/kernel/btf/vmlinux")
	return err == nil
}

// checkCapBPF checks if CAP_BPF capability is available.
// This is a simplified check. Production version would use proper capability checking.
func checkCapBPF() bool {
	// CAP_BPF was introduced in kernel 5.8+.
	// For minimal implementation, we just check if we're root.
	return os.Geteuid() == 0
}
