//go:build linux
// +build linux

package debug

import (
	"fmt"
	"sort"
	"sync"

	"github.com/rs/zerolog"

	"github.com/coral-mesh/coral/internal/sys/proc"
)

// KernelSymbolizer resolves kernel addresses to symbol names.
type KernelSymbolizer struct {
	symbols []proc.KernelSymbol // Sorted by address for binary search
	cache   map[uint64]string   // Address -> symbol name cache
	mu      sync.RWMutex
	logger  zerolog.Logger
}

// NewKernelSymbolizer creates a new kernel symbolizer by reading /proc/kallsyms.
// This should be called once at agent startup and reused for all profiling sessions.
func NewKernelSymbolizer(logger zerolog.Logger) (*KernelSymbolizer, error) {
	logger = logger.With().Str("component", "kernel_symbolizer").Logger()

	symbols, zeroAddresses, err := proc.ReadKallsyms()
	if err != nil {
		return nil, fmt.Errorf("failed to read kallsyms: %w", err)
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
	k.mu.RLock()
	if sym, ok := k.cache[addr]; ok {
		k.mu.RUnlock()
		return sym
	}
	k.mu.RUnlock()

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
	k.mu.Lock()
	k.cache[addr] = result
	k.mu.Unlock()

	return result
}

// SymbolCount returns the number of kernel symbols loaded.
func (k *KernelSymbolizer) SymbolCount() int {
	return len(k.symbols)
}
