package ebpf

import "time"

// UprobeFilter holds runtime filter criteria applied at the eBPF level (RFD 090).
// Zero values mean no filter is applied for that dimension, preserving backward compatibility.
type UprobeFilter struct {
	// MinDurationNs drops return events shorter than this threshold. 0 = no minimum.
	MinDurationNs uint64
	// MaxDurationNs drops return events longer than this threshold. 0 = no maximum.
	MaxDurationNs uint64
	// SampleRate emits 1 in every N events. 0 or 1 = emit all events.
	SampleRate uint32
}

// UprobeConfig contains configuration for an uprobe collector.
type UprobeConfig struct {
	ServiceName   string
	FunctionName  string
	SDKAddr       string
	PID           uint32 // Target process PID (required for agentless discovery)
	CaptureArgs   bool
	CaptureReturn bool
	SampleRate    uint32
	MaxEvents     uint32
	Duration      time.Duration
	Filter        UprobeFilter // Optional kernel-level filter (RFD 090).

	// Discovery configuration (optional, uses defaults if nil).
	DiscoveryConfig *DiscoveryConfig
}

// FunctionMetadata contains all information needed for uprobe attachment.
type FunctionMetadata struct {
	Name         string                 // Fully qualified name
	BinaryPath   string                 // Path to executable
	Offset       uint64                 // Function offset in binary
	Pid          uint32                 // Process ID
	Arguments    []*ArgumentMetadata    // Argument metadata
	ReturnValues []*ReturnValueMetadata // Return value metadata
}

// ArgumentMetadata describes a function argument.
type ArgumentMetadata struct {
	Name   string
	Type   string // Go type string
	Offset uint64 // Stack/register offset
}

// ReturnValueMetadata describes a return value.
type ReturnValueMetadata struct {
	Type   string
	Offset uint64
}
