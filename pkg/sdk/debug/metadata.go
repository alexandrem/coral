package debug

import (
	"crypto/sha256"
	"debug/dwarf"
	"debug/elf"
	"debug/macho"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"
)

// FunctionMetadataProvider extracts function metadata from DWARF debug info.
type FunctionMetadataProvider struct {
	logger     *slog.Logger
	binaryPath string
	pid        int
	dwarf      *dwarf.Data
	closer     interface{ Close() error } // Either *elf.File or *macho.File
	baseAddr   uint64                     // Base address for offset calculation

	// Cache for function lookups.
	mu            sync.RWMutex
	functionCache map[string]*FunctionMetadata
}

// FunctionMetadata contains all information needed for uprobe attachment.
type FunctionMetadata struct {
	Name       string
	BinaryPath string
	Offset     uint64
	PID        uint32

	// Argument and return value metadata (from DWARF).
	Arguments    []*ArgumentMetadata
	ReturnValues []*ReturnValueMetadata
}

// ArgumentMetadata describes a function argument.
type ArgumentMetadata struct {
	Name   string
	Type   string
	Offset uint64 // Stack/register offset
}

// ReturnValueMetadata describes a return value.
type ReturnValueMetadata struct {
	Type   string
	Offset uint64
}

// NewFunctionMetadataProvider creates a new metadata provider for the current process.
func NewFunctionMetadataProvider(logger *slog.Logger) (*FunctionMetadataProvider, error) {
	// Get current binary path.
	binaryPath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("failed to get executable path: %w", err)
	}

	var (
		dwarfData  *dwarf.Data // Extract DWARF debug info (platform-specific).
		fileCloser interface{ Close() error }
		dwarfErr   error
		baseAddr   uint64 // Base address for offset calculation
	)

	switch runtime.GOOS {
	case "linux":
		// Open ELF file (Linux).
		elfFile, err := elf.Open(binaryPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open ELF file %s: %w", binaryPath, err)
		}
		fileCloser = elfFile

		dwarfData, dwarfErr = elfFile.DWARF()
		if dwarfErr != nil {
			// Don't return error yet, allow fallback
			if err := elfFile.Close(); err != nil {
				logger.Warn("Failed to close ELF file", "error", err)
			}
			fileCloser = nil
		}

		// Get base address from ELF for offset calculation
		if elfFile != nil {
			for _, prog := range elfFile.Progs {
				if prog.Type == elf.PT_LOAD && prog.Flags&elf.PF_X != 0 {
					baseAddr = prog.Vaddr
					break
				}
			}
		}

	case "darwin":
		// Open Mach-O file (macOS).
		machoFile, err := macho.Open(binaryPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open Mach-O file %s: %w", binaryPath, err)
		}
		fileCloser = machoFile

		dwarfData, dwarfErr = machoFile.DWARF()
		if dwarfErr != nil {
			// Don't return error yet, allow fallback
			if err := machoFile.Close(); err != nil {
				logger.Warn("Failed to close macho file", "error", err)
			}
			fileCloser = nil
		}

	default:
		return nil, fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	// If DWARF extraction failed, log warning and use reflection fallback.
	if dwarfErr != nil {
		logger.Warn("No DWARF debug info found in binary, falling back to runtime reflection", "error", dwarfErr)
		logger.Warn("For full uprobe support (arguments/return values), rebuild without -ldflags=\"-w\"")

		// Close file handle if it was opened
		if fileCloser != nil {
			if err := fileCloser.Close(); err != nil {
				logger.Warn("Failed to close file", "error", err)
			}
			fileCloser = nil
		}

		dwarfData = nil
	} else {
		logger.Info("Initialized function metadata provider with DWARF symbols",
			"binary", binaryPath,
			"pid", os.Getpid(),
			"platform", runtime.GOOS)
	}

	return &FunctionMetadataProvider{
		logger:        logger.With("component", "metadata-provider"),
		binaryPath:    binaryPath,
		pid:           os.Getpid(),
		dwarf:         dwarfData,
		closer:        fileCloser,
		baseAddr:      baseAddr,
		functionCache: make(map[string]*FunctionMetadata),
	}, nil
}

// Close releases resources held by the provider.
func (p *FunctionMetadataProvider) Close() error {
	if p.closer != nil {
		return p.closer.Close()
	}
	return nil
}

// HasDWARF returns true if DWARF debug info is available.
func (p *FunctionMetadataProvider) HasDWARF() bool {
	return p.dwarf != nil
}

// BinaryPath returns the path to the executable.
func (p *FunctionMetadataProvider) BinaryPath() string {
	return p.binaryPath
}

// GetFunctionCount returns the total number of discoverable functions.
func (p *FunctionMetadataProvider) GetFunctionCount() int {
	funcs, err := p.ListFunctions("")
	if err != nil {
		return 0
	}
	return len(funcs)
}

// GetBinaryHash returns the SHA256 hash of the binary.
func (p *FunctionMetadataProvider) GetBinaryHash() (string, error) {
	f, err := os.Open(p.binaryPath)
	if err != nil {
		return "", err
	}
	defer f.Close() // nolint:errcheck

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// GetFunctionMetadata retrieves metadata for a specific function.
func (p *FunctionMetadataProvider) GetFunctionMetadata(functionName string) (*FunctionMetadata, error) {
	// Check cache first.
	p.mu.RLock()
	if cached, ok := p.functionCache[functionName]; ok {
		p.mu.RUnlock()
		p.logger.Debug("Cache hit for function metadata", "function", functionName)
		return cached, nil
	}
	p.mu.RUnlock()

	// Search DWARF for function.
	p.logger.Debug("Searching for function", "function", functionName)

	var (
		offset  uint64
		args    []*ArgumentMetadata
		retVals []*ReturnValueMetadata
		err     error
	)

	if p.dwarf != nil {
		offset, args, retVals, err = p.searchDWARFForFunction(functionName)
	} else {
		offset, err = p.searchReflectionForFunction(functionName)
	}

	if err != nil {
		return nil, fmt.Errorf("function %s not found: %w", functionName, err)
	}

	metadata := &FunctionMetadata{
		Name:         functionName,
		BinaryPath:   p.binaryPath,
		Offset:       offset,
		PID:          uint32(p.pid),
		Arguments:    args,
		ReturnValues: retVals,
	}

	// Cache the result.
	p.mu.Lock()
	p.functionCache[functionName] = metadata
	p.mu.Unlock()

	p.logger.Info("Found function metadata",
		"function", functionName,
		"offset", offset,
		"args", len(args),
		"returns", len(retVals))

	return metadata, nil
}

// ListFunctions returns all discoverable functions matching the pattern.
func (p *FunctionMetadataProvider) ListFunctions(packagePattern string) ([]string, error) {
	p.logger.Debug("Listing functions", "pattern", packagePattern)

	if p.dwarf == nil {
		return p.listFunctionsFromSymbols(packagePattern)
	}

	var functions []string
	reader := p.dwarf.Reader()

	for {
		entry, err := reader.Next()
		if err != nil || entry == nil {
			break
		}

		// Look for subprogram entries (functions).
		if entry.Tag == dwarf.TagSubprogram {
			if name, ok := entry.Val(dwarf.AttrName).(string); ok {
				// Filter by package pattern if specified.
				if packagePattern == "" || matchesPattern(name, packagePattern) {
					functions = append(functions, name)
				}
			}
		}
	}

	p.logger.Info("Listed functions",
		"pattern", packagePattern,
		"count", len(functions))

	return functions, nil
}

// searchDWARFForFunction searches DWARF debug info for a function and extracts metadata.
func (p *FunctionMetadataProvider) searchDWARFForFunction(
	funcName string,
) (uint64, []*ArgumentMetadata, []*ReturnValueMetadata, error) {
	reader := p.dwarf.Reader()

	for {
		entry, err := reader.Next()
		if err != nil || entry == nil {
			break
		}

		// Look for subprogram entries (functions).
		if entry.Tag == dwarf.TagSubprogram {
			name, _ := entry.Val(dwarf.AttrName).(string)

			// Match function name (exact match or suffix match for Go packages).
			if name == funcName || strings.HasSuffix(name, "."+funcName) {
				// Get function low PC (entry point address).
				lowPC, ok := entry.Val(dwarf.AttrLowpc).(uint64)
				if !ok {
					continue
				}

				// Parse function arguments and return values.
				args, retVals := p.parseFunctionParameters(reader, entry)

				// Convert virtual address to file offset by subtracting base address
				// This is needed when attaching uprobes with PID=0 (all processes)
				// cilium/ebpf expects file offset in this case, not virtual address
				fileOffset := lowPC
				if p.baseAddr > 0 {
					fileOffset = lowPC - p.baseAddr
					p.logger.Debug("Converted virtual address to file offset",
						"function", funcName,
						"virtual_addr", lowPC,
						"base_addr", p.baseAddr,
						"file_offset", fileOffset)
				}

				return fileOffset, args, retVals, nil
			}
		}
	}

	return 0, nil, nil, fmt.Errorf("function not found in DWARF symbols")
}

// parseFunctionParameters extracts argument and return value metadata from a function entry.
func (p *FunctionMetadataProvider) parseFunctionParameters(
	reader *dwarf.Reader,
	funcEntry *dwarf.Entry,
) ([]*ArgumentMetadata, []*ReturnValueMetadata) {
	var args []*ArgumentMetadata
	var retVals []*ReturnValueMetadata

	// Read child entries (parameters, local variables, etc.).
	depth := 0
	for {
		entry, err := reader.Next()
		if err != nil || entry == nil {
			break
		}

		// Track depth to stay within the function scope.
		if entry.Tag == 0 {
			depth--
			if depth < 0 {
				break // Exited function scope
			}
			continue
		}

		depth++

		// Look for formal parameters (function arguments).
		if entry.Tag == dwarf.TagFormalParameter {
			arg := &ArgumentMetadata{
				Name: getEntryName(entry),
				Type: getEntryType(entry, p.dwarf),
			}

			// Try to get location (offset).
			if loc, ok := entry.Val(dwarf.AttrLocation).([]byte); ok && len(loc) > 0 {
				// Simplified: just store first byte as offset hint.
				// Full implementation would parse DWARF location expressions.
				arg.Offset = uint64(loc[0])
			}

			args = append(args, arg)
		}

		// Note: Go return values are typically not in DWARF as formal parameters.
		// They're part of the function type. This is a simplified implementation.
	}

	return args, retVals
}

// getEntryName extracts the name attribute from a DWARF entry.
func getEntryName(entry *dwarf.Entry) string {
	if name, ok := entry.Val(dwarf.AttrName).(string); ok {
		return name
	}
	return "<unnamed>"
}

// getEntryType extracts the type name from a DWARF entry.
func getEntryType(entry *dwarf.Entry, dwarfData *dwarf.Data) string {
	typeOffset, ok := entry.Val(dwarf.AttrType).(dwarf.Offset)
	if !ok {
		return "<unknown>"
	}

	// Look up the type entry.
	reader := dwarfData.Reader()
	reader.Seek(typeOffset)
	typeEntry, err := reader.Next()
	if err != nil || typeEntry == nil {
		return "<unknown>"
	}

	// Get type name.
	if typeName, ok := typeEntry.Val(dwarf.AttrName).(string); ok {
		return typeName
	}

	// For basic types, try to infer from the type tag.
	switch typeEntry.Tag {
	case dwarf.TagPointerType:
		return "*" + getEntryType(typeEntry, dwarfData)
	case dwarf.TagBaseType:
		if name, ok := typeEntry.Val(dwarf.AttrName).(string); ok {
			return name
		}
		return "<base-type>"
	default:
		return fmt.Sprintf("<type-%s>", typeEntry.Tag)
	}
}

// matchesPattern checks if a function name matches the given pattern.
// Supports simple wildcard matching with *.
func matchesPattern(name, pattern string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}

	// Simple suffix matching for package patterns like "github.com/myapp/*".
	if strings.HasSuffix(pattern, "/*") {
		prefix := strings.TrimSuffix(pattern, "/*")
		return strings.HasPrefix(name, prefix)
	}

	// Exact match.
	return name == pattern
}
