// Package disasm provides binary disassembly to locate RET instructions
// for Return-Instruction Uprobes (RFD 073).
package disasm

import (
	"fmt"
	"os"

	"golang.org/x/arch/x86/x86asm"
)

// Disassembler finds RET instruction offsets within a function.
type Disassembler interface {
	// FindRETOffsets reads function bytes from the binary at the given offset
	// and returns the offsets (relative to function start) of all RET instructions.
	FindRETOffsets(binaryPath string, funcOffset, funcSize uint64) ([]uint64, error)
}

// x86Disassembler implements Disassembler for x86-64 binaries.
type x86Disassembler struct{}

// NewX86Disassembler creates a new x86-64 disassembler.
func NewX86Disassembler() Disassembler {
	return &x86Disassembler{}
}

// FindRETOffsets disassembles the function at the given offset in the binary
// and returns relative offsets of all RET instructions.
func (d *x86Disassembler) FindRETOffsets(binaryPath string, funcOffset, funcSize uint64) ([]uint64, error) {
	if funcSize == 0 {
		return nil, fmt.Errorf("function size is zero")
	}

	// Read function bytes from the binary.
	f, err := os.Open(binaryPath) // #nosec G304: provided path is vetted by parent for /proc pid
	if err != nil {
		return nil, fmt.Errorf("open binary: %w", err)
	}
	defer f.Close() //nolint:errcheck

	buf := make([]byte, funcSize)
	n, err := f.ReadAt(buf, int64(funcOffset))
	if err != nil {
		return nil, fmt.Errorf("read function bytes at offset 0x%x: %w", funcOffset, err)
	}
	buf = buf[:n]

	return findRETInstructions(buf)
}

// findRETInstructions scans x86-64 instruction bytes for RET opcodes.
func findRETInstructions(code []byte) ([]uint64, error) {
	var retOffsets []uint64
	pos := 0

	for pos < len(code) {
		inst, err := x86asm.Decode(code[pos:], 64)
		if err != nil {
			// Skip invalid byte and continue. Some padding/data bytes
			// may not decode as valid instructions.
			pos++
			continue
		}

		if isRETInstruction(inst) {
			retOffsets = append(retOffsets, uint64(pos))
		}

		pos += inst.Len
	}

	return retOffsets, nil
}

// isRETInstruction returns true if the instruction is a RET variant.
// Go uses only near RET (0xC3) in practice, but we detect all variants.
func isRETInstruction(inst x86asm.Inst) bool {
	return inst.Op == x86asm.RET
}
