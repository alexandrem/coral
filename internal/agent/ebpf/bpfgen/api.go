// Package bpfgen contains generated eBPF bindings for the uprobe collector.
// These files are produced by bpf2go from internal/agent/ebpf/bpf/uprobe.c.
// This package is Linux-only; do not import it from platform-agnostic code.
package bpfgen

import ceebpf "github.com/cilium/ebpf"

// Objects holds all eBPF programs and maps loaded into the kernel.
type Objects = uprobeObjects

// UprobeEntryKey is the BPF map key for entry_times (RFD 073).
// Includes stack pointer for recursion safety.
type UprobeEntryKey = uprobeEntryKey

// UprobeEntryValue is the BPF map value for entry_times (RFD 073).
// Includes created_at for orphaned entry cleanup.
type UprobeEntryValue = uprobeEntryValue

// LoadObjects loads the compiled eBPF programs and maps into the kernel.
func LoadObjects(obj *Objects, opts *ceebpf.CollectionOptions) error {
	return loadUprobeObjects(obj, opts)
}
