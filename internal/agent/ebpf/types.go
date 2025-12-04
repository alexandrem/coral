package ebpf

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
