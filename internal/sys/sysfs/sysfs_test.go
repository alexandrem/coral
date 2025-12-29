package sysfs

import (
	"os"
	"testing"
)

func TestCheckBTFAvailable(t *testing.T) {
	// This test depends on the system state.
	// We can just verify it doesn't panic.
	available := CheckBTFAvailable()
	if available {
		if _, err := os.Stat("/sys/kernel/btf/vmlinux"); err != nil {
			t.Errorf("CheckBTFAvailable returned true but file not found or error: %v", err)
		}
	} else {
		if _, err := os.Stat("/sys/kernel/btf/vmlinux"); err == nil {
			t.Error("CheckBTFAvailable returned false but file exists")
		}
	}
}
