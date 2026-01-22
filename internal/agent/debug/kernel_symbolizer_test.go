//go:build linux
// +build linux

package debug

import (
	"sync"
	"testing"

	"github.com/rs/zerolog"

	"github.com/coral-mesh/coral/internal/sys/proc"
)

func TestKernelSymbolizer_Resolve_Race(t *testing.T) {
	// Initialize with some mock symbols
	symbols := []proc.KernelSymbol{
		{Address: 0xffffffff81000000, Name: "start_kernel"},
		{Address: 0xffffffff81001000, Name: "secondary_startup_64"},
		{Address: 0xffffffffb0000000, Name: "module_func", Module: "test_module"},
	}

	ks := &KernelSymbolizer{
		symbols: symbols,
		cache:   make(map[uint64]string),
		logger:  zerolog.Nop(),
	}

	const (
		numGoroutines = 10
		numIterations = 100
	)

	addresses := []uint64{
		0xffffffff81000000, // Exact match
		0xffffffff81000500, // Inside start_kernel
		0xffffffff81001000, // Exact match
		0xffffffffb0000000, // Module match
		0xffffffffb0000100, // Inside module_func
	}

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < numIterations; j++ {
				for _, addr := range addresses {
					_ = ks.Resolve(addr)
				}
			}
		}()
	}

	wg.Wait()
}
