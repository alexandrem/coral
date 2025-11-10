package ebpf

import (
	agentv1 "github.com/coral-io/coral/coral/agent/v1"
)

// DetectCapabilities detects eBPF capabilities without creating a manager.
// This is useful for reporting capabilities before the manager is created.
func DetectCapabilities() *agentv1.EbpfCapabilities {
	return detectCapabilities()
}
