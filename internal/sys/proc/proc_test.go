package proc

import (
	"os"
	"testing"
)

func TestGetKernelVersion(t *testing.T) {
	version := GetKernelVersion()
	if version == "" {
		t.Error("GetKernelVersion returned empty string")
	}
	// On non-Linux (like macOS), it might return "unknown", which is fine for this test logic
	// as we just want to ensure it runs.
}

func TestListPids(t *testing.T) {
	pids, err := ListPids()
	if err != nil {
		// If /proc doesn't exist (macOS), it returns error.
		if os.Getenv("GOOS") == "linux" {
			t.Errorf("ListPids returned error on Linux: %v", err)
		}
		return
	}

	if len(pids) == 0 {
		// It's possible but unlikely on a live system to have 0 PIDs visible if /proc exists.
		t.Log("ListPids returned 0 pids")
	}
}

func TestReadKallsyms(t *testing.T) {
	symbols, zeroAddresses, err := ReadKallsyms()
	if err != nil {
		// On non-Linux or without permissions, this might fail or return errors.
		// If /proc/kallsyms doesn't exist (macOS), it returns error.
		if os.Getenv("GOOS") == "linux" {
			// If we are root, we should get symbols. If not, we might get zeroAddresses > 0.
			if os.Geteuid() == 0 {
				if len(symbols) == 0 {
					t.Error("ReadKallsyms returned 0 symbols as root")
				}
			} else {
				// Non-root might see zero addresses
				if len(symbols) == 0 && zeroAddresses == 0 {
					// This could be strange on Linux unless kptr_restrict is very strict?
					t.Logf("ReadKallsyms returned error/empty on Linux: %v", err)
				}
			}
		}
		return
	}

	if len(symbols) > 0 {
		t.Logf("ReadKallsyms returned %d symbols", len(symbols))
	} else if zeroAddresses > 0 {
		t.Logf("ReadKallsyms found %d zero addresses (permissions)", zeroAddresses)
	}
}
