package runtime

import (
	"os"
	"path/filepath"
	"testing"

	agentv1 "github.com/coral-io/coral/coral/agent/v1"
)

func TestReadCapabilityBitmask(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		capName     string
		expected    uint64
		expectError bool
	}{
		{
			name: "full capabilities (eBPF Full mode)",
			content: `Name:	coral-agent
CapInh:	0000000000000000
CapPrm:	00000000a80435fb
CapEff:	00000000a80435fb
CapBnd:	00000000a80435fb
CapAmb:	0000000000000000`,
			capName:  "CapEff",
			expected: 0xa80435fb,
		},
		{
			name: "restricted mode (no capabilities)",
			content: `Name:	coral-agent
CapInh:	0000000000000000
CapPrm:	0000000000000000
CapEff:	0000000000000000
CapBnd:	0000000000000000
CapAmb:	0000000000000000`,
			capName:  "CapEff",
			expected: 0x0,
		},
		{
			name: "modern eBPF (CAP_BPF + CAP_PERFMON)",
			content: `Name:	coral-agent
CapEff:	000000c000001000`,
			capName:  "CapEff",
			expected: 0xc000001000,
		},
		{
			name: "missing capability field",
			content: `Name:	coral-agent
CapPrm:	00000000a80435fb`,
			capName:     "CapEff",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary file with test content.
			tmpDir := t.TempDir()
			tmpFile := filepath.Join(tmpDir, "status")
			if err := os.WriteFile(tmpFile, []byte(tt.content), 0644); err != nil {
				t.Fatalf("failed to create test file: %v", err)
			}

			bitmask, err := readCapabilityBitmask(tmpFile, tt.capName)
			if tt.expectError {
				if err == nil {
					t.Error("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if bitmask != tt.expected {
				t.Errorf("expected bitmask 0x%x, got 0x%x", tt.expected, bitmask)
			}
		})
	}
}

func TestHasCapability(t *testing.T) {
	tests := []struct {
		name     string
		bitmask  uint64
		capBit   int
		expected bool
	}{
		{
			name:     "CAP_NET_ADMIN set",
			bitmask:  0x1000, // bit 12
			capBit:   capNetAdmin,
			expected: true,
		},
		{
			name:     "CAP_SYS_ADMIN set",
			bitmask:  0x200000, // bit 21
			capBit:   capSysAdmin,
			expected: true,
		},
		{
			name:     "CAP_SYS_PTRACE set",
			bitmask:  0x80000, // bit 19
			capBit:   capSysPtrace,
			expected: true,
		},
		{
			name:     "CAP_SYS_RESOURCE set",
			bitmask:  0x1000000, // bit 24
			capBit:   capSysResource,
			expected: true,
		},
		{
			name:     "CAP_BPF set (kernel 5.8+)",
			bitmask:  0x8000000000, // bit 39
			capBit:   capBpf,
			expected: true,
		},
		{
			name:     "CAP_PERFMON set (kernel 5.8+)",
			bitmask:  0x4000000000, // bit 38
			capBit:   capPerfmon,
			expected: true,
		},
		{
			name:     "multiple capabilities set",
			bitmask:  0x3fffffffff, // All capabilities up to bit 37
			capBit:   capSysAdmin,
			expected: true,
		},
		{
			name:     "no capabilities set",
			bitmask:  0x0,
			capBit:   capSysAdmin,
			expected: false,
		},
		{
			name:     "capability not set",
			bitmask:  0x1000, // Only CAP_NET_ADMIN
			capBit:   capSysAdmin,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasCapability(tt.bitmask, tt.capBit)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestDetectExecCapabilities(t *testing.T) {
	tests := []struct {
		name                  string
		linuxCaps             *agentv1.LinuxCapabilities
		hasCRI                bool
		hasSharedPIDNamespace bool
		expectedMode          agentv1.ExecMode
		expectedMountNS       bool
		expectedPIDNS         bool
	}{
		{
			name: "nsenter mode (full capabilities + shared PID namespace)",
			linuxCaps: &agentv1.LinuxCapabilities{
				CapSysAdmin:  true,
				CapSysPtrace: true,
			},
			hasCRI:                true,
			hasSharedPIDNamespace: true,
			expectedMode:          agentv1.ExecMode_EXEC_MODE_NSENTER,
			expectedMountNS:       true,
			expectedPIDNS:         true,
		},
		{
			name: "CRI mode (no SYS_ADMIN, has CRI socket)",
			linuxCaps: &agentv1.LinuxCapabilities{
				CapSysAdmin:  false,
				CapSysPtrace: false,
			},
			hasCRI:                true,
			hasSharedPIDNamespace: false,
			expectedMode:          agentv1.ExecMode_EXEC_MODE_CRI,
			expectedMountNS:       false,
			expectedPIDNS:         false,
		},
		{
			name: "no exec mode (no capabilities, no CRI)",
			linuxCaps: &agentv1.LinuxCapabilities{
				CapSysAdmin:  false,
				CapSysPtrace: false,
			},
			hasCRI:                false,
			hasSharedPIDNamespace: false,
			expectedMode:          agentv1.ExecMode_EXEC_MODE_NONE,
			expectedMountNS:       false,
			expectedPIDNS:         false,
		},
		{
			name: "restricted mode (capabilities but no shared PID namespace)",
			linuxCaps: &agentv1.LinuxCapabilities{
				CapSysAdmin:  true,
				CapSysPtrace: true,
			},
			hasCRI:                true,
			hasSharedPIDNamespace: false,
			expectedMode:          agentv1.ExecMode_EXEC_MODE_CRI,
			expectedMountNS:       true,
			expectedPIDNS:         false,
		},
		{
			name: "missing SYS_PTRACE (only SYS_ADMIN)",
			linuxCaps: &agentv1.LinuxCapabilities{
				CapSysAdmin:  true,
				CapSysPtrace: false,
			},
			hasCRI:                true,
			hasSharedPIDNamespace: true,
			expectedMode:          agentv1.ExecMode_EXEC_MODE_CRI,
			expectedMountNS:       false,
			expectedPIDNS:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			execCaps := DetectExecCapabilities(tt.linuxCaps, tt.hasCRI, tt.hasSharedPIDNamespace)

			if execCaps.Mode != tt.expectedMode {
				t.Errorf("expected mode %v, got %v", tt.expectedMode, execCaps.Mode)
			}

			if execCaps.MountNamespaceAccess != tt.expectedMountNS {
				t.Errorf("expected mount namespace access %v, got %v", tt.expectedMountNS, execCaps.MountNamespaceAccess)
			}

			if execCaps.PidNamespaceAccess != tt.expectedPIDNS {
				t.Errorf("expected PID namespace access %v, got %v", tt.expectedPIDNS, execCaps.PidNamespaceAccess)
			}

			if execCaps.HasSysAdmin != tt.linuxCaps.CapSysAdmin {
				t.Errorf("has_sys_admin mismatch")
			}

			if execCaps.HasSysPtrace != tt.linuxCaps.CapSysPtrace {
				t.Errorf("has_sys_ptrace mismatch")
			}

			if execCaps.HasSharedPidNs != tt.hasSharedPIDNamespace {
				t.Errorf("has_shared_pid_ns mismatch")
			}

			if execCaps.CriSocketAvailable != tt.hasCRI {
				t.Errorf("cri_socket_available mismatch")
			}
		})
	}
}
