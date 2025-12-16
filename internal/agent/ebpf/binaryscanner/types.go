package binaryscanner

// FunctionMetadata contains function information extracted from binary scanning.
type FunctionMetadata struct {
	// Name is the function name.
	Name string

	// BinaryPath is the path to the binary containing this function.
	BinaryPath string

	// Offset is the function entry point offset within the binary.
	Offset uint64

	// PID is the process ID (0 if not associated with a running process).
	PID uint32
}

// BasicInfo contains minimal function metadata for listing.
type BasicInfo struct {
	// Name is the function name.
	Name string `json:"name"`

	// Offset is the function entry point offset.
	Offset uint64 `json:"offset"`

	// File is the source file path (from DWARF).
	File string `json:"file,omitempty"`

	// Line is the source line number (from DWARF).
	Line int `json:"line,omitempty"`
}
