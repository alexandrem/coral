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
	"sort"
	"strings"
	"sync"
)

// symbolInfo holds normalized symbol table information.
type symbolInfo struct {
	Name  string // Function name
	Value uint64 // Address/offset
}

// FunctionMetadataProvider extracts function metadata from DWARF debug info.
type FunctionMetadataProvider struct {
	logger     *slog.Logger
	binaryPath string
	pid        int
	dwarf      *dwarf.Data
	closer     interface{ Close() error } // Either *elf.File or *macho.File
	baseAddr   uint64                     // Base address for offset calculation

	// Minimal index built at startup
	mu         sync.RWMutex
	basicIndex []*BasicInfo // Sorted by name for stable pagination
	indexMap   map[string]*BasicInfo

	// LRU cache for detailed function lookups (100 entries as per RFD 066).
	detailCache *lruCache

	// Cached binary hash (computed once at startup).
	binaryHash     string
	binaryHashOnce sync.Once
	binaryHashErr  error

	// Cached symbol table (loaded once at first use).
	cachedSymbols []symbolInfo
	symbolsOnce   sync.Once
	symbolsErr    error
}

// BasicInfo contains minimal function metadata for listing and discovery.
type BasicInfo struct {
	Name   string `json:"name"`
	Offset uint64 `json:"offset"`
	File   string `json:"file,omitempty"`
	Line   int    `json:"line,omitempty"`
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

	p := &FunctionMetadataProvider{
		logger:      logger.With("component", "metadata-provider"),
		binaryPath:  binaryPath,
		pid:         os.Getpid(),
		dwarf:       dwarfData,
		closer:      fileCloser,
		baseAddr:    baseAddr,
		indexMap:    make(map[string]*BasicInfo),
		detailCache: newLRUCache(100), // LRU cache with 100-entry limit as per RFD 066
	}

	// Build the index on startup
	if err := p.buildIndex(); err != nil {
		logger.Warn("Failed to build function index", "error", err)
	}

	return p, nil
}

// buildIndex iterates DWARF to build a minimal index of all functions.
func (p *FunctionMetadataProvider) buildIndex() error {
	if p.dwarf == nil {
		// Fallback to symbol table
		names, err := p.listFunctionsFromSymbols("")
		if err != nil {
			return err
		}

		var index []*BasicInfo
		for _, name := range names {
			// Get virtual address from symbol table.
			virtualAddr, _ := p.searchReflectionForFunction(name)

			// Convert virtual address to file offset by subtracting base address.
			fileOffset := virtualAddr
			if p.baseAddr > 0 && virtualAddr > 0 {
				fileOffset = virtualAddr - p.baseAddr
			}

			index = append(index, &BasicInfo{
				Name:   name,
				Offset: fileOffset,
			})
			p.indexMap[name] = index[len(index)-1]
		}

		// Sort by name for stable pagination
		sort.Slice(index, func(i, j int) bool {
			return index[i].Name < index[j].Name
		})

		p.basicIndex = index
		p.logger.Info("Built function index from symbols", "count", len(index))
		return nil
	}

	reader := p.dwarf.Reader()
	var index []*BasicInfo

	// Build a map of addresses to line information for efficient lookup.
	lineTable := p.buildLineTable()

	for {
		entry, err := reader.Next()
		if err != nil || entry == nil {
			break
		}

		if entry.Tag == dwarf.TagSubprogram {
			name, ok := entry.Val(dwarf.AttrName).(string)
			if !ok {
				continue
			}

			// Get function low PC (entry point address).
			lowPC, ok := entry.Val(dwarf.AttrLowpc).(uint64)
			if !ok {
				continue
			}

			// Calculate file offset
			fileOffset := lowPC
			if p.baseAddr > 0 {
				fileOffset = lowPC - p.baseAddr
			}

			// Get file and line info from line table.
			var file string
			var line int

			// Try exact match first.
			if info, ok := lineTable[lowPC]; ok {
				file = info.File
				line = info.Line
			} else if lineTable != nil {
				// No exact match - search for the closest line entry near this address.
				// This handles cases where the function start doesn't have an exact line entry.
				const searchWindow uint64 = 0x100 // Search within 256 bytes
				bestFile := ""
				bestLine := 0
				bestDistance := searchWindow

				for addr, info := range lineTable {
					// Look for entries near the function start.
					var distance uint64
					if addr >= lowPC && addr < lowPC+searchWindow {
						distance = addr - lowPC
					} else if addr < lowPC && lowPC-addr < searchWindow {
						distance = lowPC - addr
					} else {
						continue
					}

					if distance < bestDistance {
						bestDistance = distance
						bestFile = info.File
						bestLine = info.Line
					}
				}

				if bestFile != "" {
					file = bestFile
					line = bestLine
				}
			}

			// Final fallback: try DWARF attributes directly if still no match.
			if line == 0 {
				if declLine, ok := entry.Val(dwarf.AttrDeclLine).(int64); ok {
					line = int(declLine)
				}
			}

			info := &BasicInfo{
				Name:   name,
				Offset: fileOffset,
				File:   file,
				Line:   line,
			}

			index = append(index, info)
			p.indexMap[name] = info
		}
	}

	// Sort by name for stable pagination
	sort.Slice(index, func(i, j int) bool {
		return index[i].Name < index[j].Name
	})

	p.basicIndex = index
	p.logger.Info("Built function index", "count", len(index))
	return nil
}

// lineInfo holds file and line information for a program address.
type lineInfo struct {
	File string
	Line int
}

// buildLineTable builds a map of program addresses to file/line information.
// This is used for efficient lookup when building the function index.
func (p *FunctionMetadataProvider) buildLineTable() map[uint64]lineInfo {
	if p.dwarf == nil {
		return nil
	}

	lineTable := make(map[uint64]lineInfo)

	// Iterate through all compilation units.
	reader := p.dwarf.Reader()
	for {
		entry, err := reader.Next()
		if err != nil || entry == nil {
			break
		}

		// Process compilation units to extract line table information.
		if entry.Tag == dwarf.TagCompileUnit {
			lr, err := p.dwarf.LineReader(entry)
			if err != nil || lr == nil {
				continue
			}

			// Read all line table entries for this compilation unit.
			var lineEntry dwarf.LineEntry
			for {
				err := lr.Next(&lineEntry)
				if err != nil {
					break
				}

				// Store mapping from address to file/line.
				// Only store entries that mark the beginning of a statement.
				if lineEntry.IsStmt && lineEntry.File != nil {
					lineTable[lineEntry.Address] = lineInfo{
						File: lineEntry.File.Name,
						Line: lineEntry.Line,
					}
				}
			}
		}
	}

	return lineTable
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
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.basicIndex)
}

// GetBinaryHash returns the SHA256 hash of the binary.
// The hash is computed once and cached for subsequent calls.
func (p *FunctionMetadataProvider) GetBinaryHash() (string, error) {
	p.binaryHashOnce.Do(func() {
		p.binaryHash, p.binaryHashErr = p.computeBinaryHash()
	})
	return p.binaryHash, p.binaryHashErr
}

// computeBinaryHash calculates the SHA256 hash of the binary file.
func (p *FunctionMetadataProvider) computeBinaryHash() (string, error) {
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

// loadSymbols loads and caches the symbol table from the binary.
// This method is called once via sync.Once to avoid repeated file I/O.
func (p *FunctionMetadataProvider) loadSymbols() ([]symbolInfo, error) {
	p.symbolsOnce.Do(func() {
		var symbols []symbolInfo

		switch runtime.GOOS {
		case "linux":
			f, err := elf.Open(p.binaryPath)
			if err != nil {
				p.symbolsErr = err
				return
			}
			defer f.Close() // nolint:errcheck

			elfSymbols, err := f.Symbols()
			if err != nil {
				p.symbolsErr = fmt.Errorf("failed to read ELF symbols: %w", err)
				return
			}

			for _, sym := range elfSymbols {
				if elf.ST_TYPE(sym.Info) == elf.STT_FUNC && sym.Name != "" {
					symbols = append(symbols, symbolInfo{
						Name:  sym.Name,
						Value: sym.Value,
					})
				}
			}

		case "darwin":
			f, err := macho.Open(p.binaryPath)
			if err != nil {
				p.symbolsErr = err
				return
			}
			defer f.Close() // nolint:errcheck

			if f.Symtab == nil {
				p.symbolsErr = fmt.Errorf("no symbol table found")
				return
			}

			for _, sym := range f.Symtab.Syms {
				name := strings.TrimPrefix(sym.Name, "_")
				if name != "" {
					symbols = append(symbols, symbolInfo{
						Name:  name,
						Value: sym.Value,
					})
				}
			}

		default:
			p.symbolsErr = fmt.Errorf("unsupported platform: %s", runtime.GOOS)
			return
		}

		p.cachedSymbols = symbols
	})

	return p.cachedSymbols, p.symbolsErr
}

// GetFunctionMetadata retrieves metadata for a specific function.
func (p *FunctionMetadataProvider) GetFunctionMetadata(functionName string) (*FunctionMetadata, error) {
	// Check LRU cache first.
	if cached, ok := p.detailCache.Get(functionName); ok {
		p.logger.Debug("Cache hit for function metadata", "function", functionName)
		return cached, nil
	}

	// Check if it exists in index
	p.mu.RLock()
	if _, ok := p.indexMap[functionName]; !ok {
		p.mu.RUnlock()
		return nil, fmt.Errorf("function %s not found", functionName)
	}
	p.mu.RUnlock()

	// Search DWARF for function details.
	p.logger.Debug("Searching for function details", "function", functionName)

	var (
		offset  uint64
		args    []*ArgumentMetadata
		retVals []*ReturnValueMetadata
		err     error
	)

	if p.dwarf != nil {
		offset, args, retVals, err = p.searchDWARFForFunction(functionName)
	} else {
		// Fallback to symbol table
		offset, err = p.searchReflectionForFunction(functionName)
		if err == nil {
			// Found in symbol table.
			// We don't have args/retVals from symbols.
			args = []*ArgumentMetadata{}
			retVals = []*ReturnValueMetadata{}
		}
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

	// Cache the result in LRU cache.
	p.detailCache.Put(functionName, metadata)

	return metadata, nil
}

// ListFunctions returns a page of functions matching the pattern.
func (p *FunctionMetadataProvider) ListFunctions(pattern string, limit, offset int) ([]*BasicInfo, int) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var filtered []*BasicInfo

	// Filter
	if pattern == "" {
		filtered = p.basicIndex
	} else {
		for _, info := range p.basicIndex {
			if matchesPattern(info.Name, pattern) {
				filtered = append(filtered, info)
			}
		}
	}

	total := len(filtered)

	// Paginate
	if offset >= total {
		return []*BasicInfo{}, total
	}

	end := offset + limit
	if end > total {
		end = total
	}

	return filtered[offset:end], total
}

// ListAllFunctions returns all functions (for export).
func (p *FunctionMetadataProvider) ListAllFunctions() []*BasicInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.basicIndex
}

// CountFunctions returns the number of functions matching the pattern.
func (p *FunctionMetadataProvider) CountFunctions(pattern string) int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if pattern == "" {
		return len(p.basicIndex)
	}

	count := 0
	for _, info := range p.basicIndex {
		if matchesPattern(info.Name, pattern) {
			count++
		}
	}
	return count
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
				fileOffset := lowPC
				if p.baseAddr > 0 {
					fileOffset = lowPC - p.baseAddr
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
