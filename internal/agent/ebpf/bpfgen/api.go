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
// Fixes the uprobe_return program's attach type: the .o declares it as
// uretprobe (SEC("uretprobe/...")) but RFD 073 attaches it as a regular
// uprobe to RET instruction offsets.
func LoadObjects(obj *Objects, opts *ceebpf.CollectionOptions) error {
	spec, err := loadUprobe()
	if err != nil {
		return err
	}

	// RFD 073 attaches uprobe_return as a regular uprobe to RET instruction offsets,
	// not via the kernel's uretprobe mechanism. Clear any uretprobe attach type that
	// cilium/ebpf infers from the ELF section name, so the kernel accepts attachment.
	// Safe to keep after the .o is regenerated with the corrected SEC("uprobe/...").
	if prog, ok := spec.Programs["uprobe_return"]; ok {
		prog.AttachType = ceebpf.AttachNone
	}

	return spec.LoadAndAssign(obj, opts)
}
