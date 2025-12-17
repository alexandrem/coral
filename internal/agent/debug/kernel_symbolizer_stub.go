//go:build !linux
// +build !linux

package debug

import (
	"fmt"

	"github.com/rs/zerolog"
)

// KernelSymbolizer stub for non-Linux platforms.
type KernelSymbolizer struct{}

// NewKernelSymbolizer returns an error on non-Linux platforms.
func NewKernelSymbolizer(logger zerolog.Logger) (*KernelSymbolizer, error) {
	return nil, fmt.Errorf("kernel symbolization is only supported on Linux")
}

// Resolve returns empty string on non-Linux platforms.
func (k *KernelSymbolizer) Resolve(addr uint64) string {
	return ""
}

// SymbolCount returns 0 on non-Linux platforms.
func (k *KernelSymbolizer) SymbolCount() int {
	return 0
}
