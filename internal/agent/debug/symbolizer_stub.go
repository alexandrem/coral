//go:build !linux
// +build !linux

package debug

import (
	"fmt"

	"github.com/rs/zerolog"
)

// Symbol represents a resolved symbol (stub for non-Linux).
type Symbol struct {
	FunctionName string
	FileName     string
	Line         int
}

// Symbolizer stub for non-Linux platforms.
type Symbolizer struct{}

// NewSymbolizer returns an error on non-Linux platforms.
func NewSymbolizer(binaryPath string, pid int, logger zerolog.Logger) (*Symbolizer, error) {
	return nil, fmt.Errorf("symbolization is only supported on Linux")
}

// Resolve returns an error on non-Linux platforms.
func (s *Symbolizer) Resolve(addr uint64) (Symbol, error) {
	return Symbol{}, fmt.Errorf("symbolization is only supported on Linux")
}

// Close does nothing on non-Linux platforms.
func (s *Symbolizer) Close() error {
	return nil
}

// GetBinaryPath returns an error on non-Linux platforms.
func GetBinaryPath(pid int) (string, error) {
	return "", fmt.Errorf("binary path detection is only supported on Linux")
}

// FormatSymbol formats a symbol for display.
func FormatSymbol(sym Symbol) string {
	if sym.FileName != "" && sym.Line > 0 {
		return fmt.Sprintf("%s (%s:%d)", sym.FunctionName, sym.FileName, sym.Line)
	}
	return sym.FunctionName
}
