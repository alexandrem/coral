//go:build linux
// +build linux

package debug

import (
	"debug/dwarf"
	"debug/elf"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/rs/zerolog"
)

// Symbol represents a resolved symbol with function name and location.
type Symbol struct {
	FunctionName string
	FileName     string
	Line         int
}

// Symbolizer resolves memory addresses to function names using DWARF debug info.
type Symbolizer struct {
	binaryPath      string
	pid             int
	elfFile         *elf.File
	dwarfData       *dwarf.Data
	symtab          []elf.Symbol
	cache           map[uint64]Symbol
	mu              sync.RWMutex
	logger          zerolog.Logger
	runtimeLoadAddr uint64 // Runtime load address from /proc/PID/maps
	elfBaseAddr     uint64 // Base address from ELF PT_LOAD segment
}

// NewSymbolizer creates a new symbolizer for the given binary and PID.
func NewSymbolizer(binaryPath string, pid int, logger zerolog.Logger) (*Symbolizer, error) {
	// Open ELF file
	f, err := elf.Open(binaryPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open binary: %w", err)
	}

	// Get ELF base address from PT_LOAD segment
	var elfBaseAddr uint64
	for _, prog := range f.Progs {
		if prog.Type == elf.PT_LOAD && prog.Flags&elf.PF_X != 0 {
			elfBaseAddr = prog.Vaddr
			break
		}
	}

	// Get runtime load address from /proc/PID/maps
	runtimeLoadAddr, err := getRuntimeLoadAddress(pid, binaryPath)
	if err != nil {
		logger.Warn().Err(err).Msg("Failed to get runtime load address, symbolization may be incorrect for PIE binaries")
		runtimeLoadAddr = 0
	}

	s := &Symbolizer{
		binaryPath:      binaryPath,
		pid:             pid,
		elfFile:         f,
		cache:           make(map[uint64]Symbol),
		logger:          logger.With().Str("component", "symbolizer").Logger(),
		runtimeLoadAddr: runtimeLoadAddr,
		elfBaseAddr:     elfBaseAddr,
	}

	s.logger.Info().
		Uint64("elf_base", elfBaseAddr).
		Uint64("runtime_load", runtimeLoadAddr).
		Int("pid", pid).
		Msg("Symbolizer initialized with address mapping")

	// Try to load DWARF debug info
	dwarfData, err := f.DWARF()
	if err != nil {
		s.logger.Debug().Err(err).Msg("DWARF debug info not available, using symbol table only")
	} else {
		s.dwarfData = dwarfData
		s.logger.Debug().Msg("DWARF debug info loaded")
	}

	// Load symbol table
	symbols, err := f.Symbols()
	if err != nil {
		s.logger.Debug().Err(err).Msg("Symbol table not available")
	} else {
		s.symtab = symbols
		s.logger.Debug().Int("symbol_count", len(symbols)).Msg("Symbol table loaded")
	}

	if s.dwarfData == nil && len(s.symtab) == 0 {
		f.Close() // nolint:errcheck
		return nil, fmt.Errorf("binary has no debug info or symbol table (stripped binary?)")
	}

	return s, nil
}

// Resolve resolves a memory address to a symbol.
// Returns a Symbol with function name and optionally file:line info.
// For PIE binaries, addr is the runtime virtual address which needs to be
// converted to a file offset before symbol lookup.
func (s *Symbolizer) Resolve(addr uint64) (Symbol, error) {
	// Check cache first
	s.mu.RLock()
	if sym, ok := s.cache[addr]; ok {
		s.mu.RUnlock()
		return sym, nil
	}
	s.mu.RUnlock()

	// Convert runtime address to file offset for PIE binaries
	// fileOffset = runtimeAddr - runtimeLoadAddr + elfBaseAddr
	fileOffset := addr
	if s.runtimeLoadAddr > 0 {
		fileOffset = addr - s.runtimeLoadAddr + s.elfBaseAddr
		s.logger.Debug().
			Uint64("runtime_addr", addr).
			Uint64("file_offset", fileOffset).
			Msg("Converted runtime address to file offset")
	}

	// Try DWARF first (best: includes file:line)
	if s.dwarfData != nil {
		if sym, err := s.resolveDWARF(fileOffset); err == nil {
			s.cacheSymbol(addr, sym)
			return sym, nil
		}
	}

	// Fallback to symbol table (okay: function name only)
	if len(s.symtab) > 0 {
		if sym, err := s.resolveSymTab(fileOffset); err == nil {
			s.cacheSymbol(addr, sym)
			return sym, nil
		}
	}

	return Symbol{}, fmt.Errorf("symbol not found for address 0x%x (file offset 0x%x)", addr, fileOffset)
}

// resolveDWARF resolves an address using DWARF debug info.
func (s *Symbolizer) resolveDWARF(addr uint64) (Symbol, error) {
	reader := s.dwarfData.Reader()

	for {
		entry, err := reader.Next()
		if err != nil || entry == nil {
			break
		}

		// Look for subprogram (function) entries
		if entry.Tag != dwarf.TagSubprogram {
			continue
		}

		// Get function name
		nameAttr := entry.Val(dwarf.AttrName)
		if nameAttr == nil {
			continue
		}
		funcName := nameAttr.(string)

		// Get PC range
		lowPC := entry.Val(dwarf.AttrLowpc)
		highPC := entry.Val(dwarf.AttrHighpc)
		if lowPC == nil || highPC == nil {
			continue
		}

		low, ok := lowPC.(uint64)
		if !ok {
			continue
		}

		// highPC can be either absolute address (uint64) or offset from lowPC (int64)
		var high uint64
		switch v := highPC.(type) {
		case uint64:
			high = v
		case int64:
			// Offset from lowPC
			high = low + uint64(v) // #nosec G115
		default:
			continue
		}

		// Check if address is in this function's range
		if addr >= low && addr < high {
			sym := Symbol{
				FunctionName: funcName,
			}

			// Try to get file:line info
			lineReader, err := s.dwarfData.LineReader(entry)
			if err == nil && lineReader != nil {
				var lineEntry dwarf.LineEntry
				if err := lineReader.SeekPC(addr, &lineEntry); err == nil {
					sym.FileName = lineEntry.File.Name
					sym.Line = lineEntry.Line
				}
			}

			return sym, nil
		}
	}

	return Symbol{}, fmt.Errorf("no DWARF entry found")
}

// resolveSymTab resolves an address using the symbol table.
func (s *Symbolizer) resolveSymTab(addr uint64) (Symbol, error) {
	// Find the symbol containing this address
	for _, sym := range s.symtab {
		if addr >= sym.Value && addr < sym.Value+sym.Size {
			return Symbol{
				FunctionName: sym.Name,
			}, nil
		}
	}

	return Symbol{}, fmt.Errorf("no symbol found")
}

// cacheSymbol stores a resolved symbol in the cache.
func (s *Symbolizer) cacheSymbol(addr uint64, sym Symbol) {
	s.mu.Lock()
	s.cache[addr] = sym
	s.mu.Unlock()
}

// Close closes the symbolizer and releases resources.
func (s *Symbolizer) Close() error {
	if s.elfFile != nil {
		return s.elfFile.Close()
	}
	return nil
}

// GetBinaryPath returns the path to the binary being symbolized.
func GetBinaryPath(pid int) (string, error) {
	// Read /proc/{pid}/exe symlink to get the binary path
	path := fmt.Sprintf("/proc/%d/exe", pid)
	target, err := os.Readlink(path)
	if err != nil {
		return "", fmt.Errorf("failed to read binary path: %w", err)
	}
	return target, nil
}

// getRuntimeLoadAddress reads /proc/PID/maps to find the runtime load address
// of the executable. This is needed for PIE (Position Independent Executable) binaries
// where the load address differs from the ELF file's base address.
func getRuntimeLoadAddress(pid int, binaryPath string) (uint64, error) {
	// Read /proc/PID/maps
	mapsPath := fmt.Sprintf("/proc/%d/maps", pid)
	data, err := os.ReadFile(mapsPath) // #nosec G304: pid is int so it's safe
	if err != nil {
		return 0, fmt.Errorf("failed to read maps: %w", err)
	}

	// Resolve the actual binary path from /proc/PID/exe if needed
	// This handles the case where binaryPath is "/proc/PID/exe"
	actualPath := binaryPath
	if strings.Contains(binaryPath, "/proc/") && strings.HasSuffix(binaryPath, "/exe") {
		resolved, err := os.Readlink(binaryPath)
		if err == nil {
			actualPath = resolved
		}
	}

	// Parse maps file to find the first executable mapping for the binary
	// Format: address           perms offset  dev   inode   pathname
	// Example: 555555554000-555555556000 r-xp 00000000 08:01 123456 /path/to/binary
	lines := string(data)
	for _, line := range strings.Split(lines, "\n") {
		if len(line) == 0 {
			continue
		}

		// Check if this is an executable mapping (r-xp)
		if !strings.Contains(line, "r-xp") {
			continue
		}

		// Check if this line contains the binary path (match against actualPath or /exe suffix)
		if !strings.Contains(line, actualPath) && !strings.HasSuffix(line, "/exe") {
			continue
		}

		// Parse the address range
		parts := strings.Fields(line)
		if len(parts) < 1 {
			continue
		}

		// Extract start address
		addrRange := parts[0]
		addrParts := strings.Split(addrRange, "-")
		if len(addrParts) != 2 {
			continue
		}

		// Parse hex address
		var addr uint64
		if _, err := fmt.Sscanf(addrParts[0], "%x", &addr); err != nil {
			continue
		}

		return addr, nil
	}

	return 0, fmt.Errorf("no executable mapping found for %s in /proc/%d/maps", actualPath, pid)
}

// FormatSymbol formats a symbol for display.
func FormatSymbol(sym Symbol) string {
	if sym.FileName != "" && sym.Line > 0 {
		return fmt.Sprintf("%s (%s:%d)", sym.FunctionName, sym.FileName, sym.Line)
	}
	return sym.FunctionName
}
