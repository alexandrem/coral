//go:build linux
// +build linux

package debug

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/rs/zerolog"
)

// KernelSymbol represents a kernel symbol from /proc/kallsyms.
type KernelSymbol struct {
	Address uint64
	Type    byte
	Name    string
	Module  string // Empty for core kernel, module name for loadable modules
}

// KernelSymbolizer resolves kernel addresses to symbol names.
type KernelSymbolizer struct {
	symbols []KernelSymbol    // Sorted by address for binary search
	cache   map[uint64]string // Address -> symbol name cache
	logger  zerolog.Logger
}

// NewKernelSymbolizer creates a new kernel symbolizer by reading /proc/kallsyms.
// This should be called once at agent startup and reused for all profiling sessions.
func NewKernelSymbolizer(logger zerolog.Logger) (*KernelSymbolizer, error) {
	logger = logger.With().Str("component", "kernel_symbolizer").Logger()

	// Read /proc/kallsyms
	file, err := os.Open("/proc/kallsyms")
	if err != nil {
		return nil, fmt.Errorf("failed to open /proc/kallsyms: %w (requires root or CAP_SYSLOG)", err)
	}
	defer file.Close() // nolint:errcheck

	var symbols []KernelSymbol
	scanner := bufio.NewScanner(file)
	zeroAddresses := 0

	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}

		// Parse address
		var addr uint64
		if _, err := fmt.Sscanf(parts[0], "%x", &addr); err != nil {
			continue
		}

		// Check for zero addresses (means insufficient permissions)
		if addr == 0 {
			zeroAddresses++
			continue
		}

		// Parse symbol type and name
		symType := parts[1][0]
		symName := parts[2]

		// Parse optional module name [module_name]
		var module string
		if len(parts) > 3 && strings.HasPrefix(parts[3], "[") && strings.HasSuffix(parts[3], "]") {
			module = strings.Trim(parts[3], "[]")
		}

		symbols = append(symbols, KernelSymbol{
			Address: addr,
			Type:    symType,
			Name:    symName,
			Module:  module,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read /proc/kallsyms: %w", err)
	}

	// Check if we have permission to see addresses
	if len(symbols) == 0 && zeroAddresses > 0 {
		return nil, fmt.Errorf("all kallsyms addresses are 0 (insufficient permissions - need root or CAP_SYSLOG)")
	}

	if len(symbols) == 0 {
		return nil, fmt.Errorf("no kernel symbols found in /proc/kallsyms")
	}

	// Sort symbols by address for binary search
	sort.Slice(symbols, func(i, j int) bool {
		return symbols[i].Address < symbols[j].Address
	})

	k := &KernelSymbolizer{
		symbols: symbols,
		cache:   make(map[uint64]string),
		logger:  logger,
	}

	logger.Info().
		Int("symbol_count", len(symbols)).
		Int("zero_addresses", zeroAddresses).
		Msg("Kernel symbolizer initialized")

	return k, nil
}

// Resolve resolves a kernel address to a symbol name.
// Uses binary search on the sorted symbol list.
func (k *KernelSymbolizer) Resolve(addr uint64) string {
	// Check cache first
	if sym, ok := k.cache[addr]; ok {
		return sym
	}

	// Binary search to find the symbol containing this address
	// Find the largest symbol address <= addr
	idx := sort.Search(len(k.symbols), func(i int) bool {
		return k.symbols[i].Address > addr
	})

	if idx == 0 {
		// Address is before the first symbol
		return ""
	}

	// Use the previous symbol (largest address <= addr)
	sym := k.symbols[idx-1]

	// Format symbol name
	result := sym.Name
	if sym.Module != "" {
		result = fmt.Sprintf("%s [%s]", sym.Name, sym.Module)
	}

	// Cache the result
	k.cache[addr] = result

	return result
}

// SymbolCount returns the number of kernel symbols loaded.
func (k *KernelSymbolizer) SymbolCount() int {
	return len(k.symbols)
}
