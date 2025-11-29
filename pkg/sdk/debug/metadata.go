package debug

import (
	"debug/dwarf"
	"debug/elf"
	"debug/macho"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/rs/zerolog"
)

// FunctionMetadataProvider extracts function metadata from DWARF debug info.
type FunctionMetadataProvider struct {
	logger     zerolog.Logger
	binaryPath string
	pid        int
	dwarf      *dwarf.Data
	closer     interface{ Close() error } // Either *elf.File or *macho.File

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
func NewFunctionMetadataProvider(logger zerolog.Logger) (*FunctionMetadataProvider, error) {
	// Get current binary path.
	binaryPath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("failed to get executable path: %w", err)
	}

	// Extract DWARF debug info (platform-specific).
	var dwarfData *dwarf.Data
	var fileCloser interface{ Close() error }

	switch runtime.GOOS {
	case "linux":
		// Open ELF file (Linux).
		elfFile, err := elf.Open(binaryPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open ELF file %s: %w", binaryPath, err)
		}
		fileCloser = elfFile

		dwarfData, err = elfFile.DWARF()
		if err != nil {
			elfFile.Close()
			return nil, fmt.Errorf("failed to extract DWARF data (binary may not have debug symbols): %w", err)
		}

	case "darwin":
		// Open Mach-O file (macOS).
		machoFile, err := macho.Open(binaryPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open Mach-O file %s: %w", binaryPath, err)
		}
		fileCloser = machoFile

		dwarfData, err = machoFile.DWARF()
		if err != nil {
			machoFile.Close()
			return nil, fmt.Errorf("failed to extract DWARF data (binary may not have debug symbols): %w", err)
		}

	default:
		return nil, fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	logger.Info().
		Str("binary", binaryPath).
		Int("pid", os.Getpid()).
		Str("platform", runtime.GOOS).
		Msg("Initialized function metadata provider with DWARF symbols")

	return &FunctionMetadataProvider{
		logger:        logger.With().Str("component", "metadata-provider").Logger(),
		binaryPath:    binaryPath,
		pid:           os.Getpid(),
		dwarf:         dwarfData,
		closer:        fileCloser,
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

// GetFunctionMetadata retrieves metadata for a specific function.
func (p *FunctionMetadataProvider) GetFunctionMetadata(functionName string) (*FunctionMetadata, error) {
	// Check cache first.
	p.mu.RLock()
	if cached, ok := p.functionCache[functionName]; ok {
		p.mu.RUnlock()
		p.logger.Debug().Str("function", functionName).Msg("Cache hit for function metadata")
		return cached, nil
	}
	p.mu.RUnlock()

	// Search DWARF for function.
	p.logger.Debug().Str("function", functionName).Msg("Searching DWARF for function")

	offset, args, retVals, err := p.searchDWARFForFunction(functionName)
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

	p.logger.Info().
		Str("function", functionName).
		Uint64("offset", offset).
		Int("args", len(args)).
		Int("returns", len(retVals)).
		Msg("Found function metadata")

	return metadata, nil
}

// ListFunctions returns all discoverable functions matching the pattern.
func (p *FunctionMetadataProvider) ListFunctions(packagePattern string) ([]string, error) {
	p.logger.Debug().Str("pattern", packagePattern).Msg("Listing functions")

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

	p.logger.Info().
		Str("pattern", packagePattern).
		Int("count", len(functions)).
		Msg("Listed functions")

	return functions, nil
}

// searchDWARFForFunction searches DWARF debug info for a function and extracts metadata.
func (p *FunctionMetadataProvider) searchDWARFForFunction(funcName string) (uint64, []*ArgumentMetadata, []*ReturnValueMetadata, error) {
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

				return lowPC, args, retVals, nil
			}
		}
	}

	return 0, nil, nil, fmt.Errorf("function not found in DWARF symbols")
}

// parseFunctionParameters extracts argument and return value metadata from a function entry.
func (p *FunctionMetadataProvider) parseFunctionParameters(reader *dwarf.Reader, funcEntry *dwarf.Entry) ([]*ArgumentMetadata, []*ReturnValueMetadata) {
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
