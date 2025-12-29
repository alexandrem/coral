// Package sysfs provides utilities for interacting with the /sys filesystem.
package sysfs

import (
	"os"
)

// CheckBTFAvailable checks if BTF (BPF Type Format) is available.
// BTF is required for CO-RE (Compile Once, Run Everywhere) support.
func CheckBTFAvailable() bool {
	// Check for /sys/kernel/btf/vmlinux.
	_, err := os.Stat("/sys/kernel/btf/vmlinux")
	return err == nil
}
