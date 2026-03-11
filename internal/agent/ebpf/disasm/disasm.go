// Package disasm provides binary disassembly to locate RET instructions
// for Return-Instruction Uprobes (RFD 073).
package disasm

import (
	"debug/elf"
	"encoding/binary"
	"fmt"
	"os"

	"golang.org/x/arch/arm64/arm64asm"
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

func (d *x86Disassembler) FindRETOffsets(binaryPath string, funcOffset, funcSize uint64) ([]uint64, error) {
	buf, err := readFunctionBytes(binaryPath, funcOffset, funcSize)
	if err != nil {
		return nil, err
	}
	return findRETInstructions(buf)
}

// arm64Disassembler implements Disassembler for ARM64 binaries.
type arm64Disassembler struct{}

func (d *arm64Disassembler) FindRETOffsets(binaryPath string, funcOffset, funcSize uint64) ([]uint64, error) {
	buf, err := readFunctionBytes(binaryPath, funcOffset, funcSize)
	if err != nil {
		return nil, err
	}
	return findRETInstructionsARM64(buf)
}

// nativeDisassembler auto-detects the ELF machine type and delegates to the
// appropriate architecture-specific implementation.
type nativeDisassembler struct{}

// NewNativeDisassembler creates a disassembler that auto-detects the target
// binary's architecture from its ELF header.
func NewNativeDisassembler() Disassembler {
	return &nativeDisassembler{}
}

// NewX86Disassembler creates a disassembler for x86-64 binaries.
// Deprecated: Use NewNativeDisassembler for automatic architecture detection.
func NewX86Disassembler() Disassembler {
	return &nativeDisassembler{}
}

func (d *nativeDisassembler) FindRETOffsets(binaryPath string, funcOffset, funcSize uint64) ([]uint64, error) {
	if funcSize == 0 {
		return nil, fmt.Errorf("function size is zero")
	}

	machine, err := readELFMachine(binaryPath)
	if err != nil {
		return nil, fmt.Errorf("detect ELF architecture: %w", err)
	}

	switch machine {
	case elf.EM_AARCH64:
		return (&arm64Disassembler{}).FindRETOffsets(binaryPath, funcOffset, funcSize)
	default:
		return (&x86Disassembler{}).FindRETOffsets(binaryPath, funcOffset, funcSize)
	}
}

// readELFMachine reads the ELF machine type from the first 20 bytes of the binary.
// e_machine is at byte offset 18 (2 bytes, little-endian for LE ELFs).
func readELFMachine(binaryPath string) (elf.Machine, error) {
	f, err := os.Open(binaryPath) // #nosec G304: provided path is vetted by parent for /proc pid
	if err != nil {
		return 0, fmt.Errorf("open binary: %w", err)
	}
	defer f.Close() //nolint:errcheck

	var header [20]byte
	if _, err := f.ReadAt(header[:], 0); err != nil {
		return 0, fmt.Errorf("read ELF header: %w", err)
	}

	return elf.Machine(binary.LittleEndian.Uint16(header[18:20])), nil
}

// readFunctionBytes reads funcSize bytes from binaryPath at the given offset.
func readFunctionBytes(binaryPath string, funcOffset, funcSize uint64) ([]byte, error) {
	if funcSize == 0 {
		return nil, fmt.Errorf("function size is zero")
	}

	f, err := os.Open(binaryPath) // #nosec G304: provided path is vetted by parent for /proc pid
	if err != nil {
		return nil, fmt.Errorf("open binary: %w", err)
	}
	defer f.Close() //nolint:errcheck

	buf := make([]byte, funcSize)
	n, err := f.ReadAt(buf, int64(funcOffset)) // #nosec:G115
	if err != nil {
		return nil, fmt.Errorf("read function bytes at offset 0x%x: %w", funcOffset, err)
	}

	return buf[:n], nil
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

		if inst.Op == x86asm.RET {
			retOffsets = append(retOffsets, uint64(pos))
		}

		pos += inst.Len
	}

	return retOffsets, nil
}

// findRETInstructionsARM64 scans ARM64 instruction bytes for RET opcodes.
// ARM64 instructions are always 4 bytes wide and must be 4-byte aligned.
func findRETInstructionsARM64(code []byte) ([]uint64, error) {
	const instrSize = 4
	var retOffsets []uint64

	for pos := 0; pos+instrSize <= len(code); pos += instrSize {
		inst, err := arm64asm.Decode(code[pos:])
		if err != nil {
			continue
		}

		if inst.Op == arm64asm.RET {
			retOffsets = append(retOffsets, uint64(pos))
		}
	}

	return retOffsets, nil
}
