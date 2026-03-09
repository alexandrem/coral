package disasm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindRETInstructions_SimpleRET(t *testing.T) {
	// Simple sequence: NOP, NOP, RET (0xC3)
	code := []byte{0x90, 0x90, 0xC3}
	offsets, err := findRETInstructions(code)
	require.NoError(t, err)
	assert.Equal(t, []uint64{2}, offsets)
}

func TestFindRETInstructions_MultipleRET(t *testing.T) {
	// Two return paths: NOP, RET, NOP, NOP, RET
	code := []byte{0x90, 0xC3, 0x90, 0x90, 0xC3}
	offsets, err := findRETInstructions(code)
	require.NoError(t, err)
	assert.Equal(t, []uint64{1, 4}, offsets)
}

func TestFindRETInstructions_NoRET(t *testing.T) {
	// No RET: just NOPs (tail-call optimized function)
	code := []byte{0x90, 0x90, 0x90, 0x90}
	offsets, err := findRETInstructions(code)
	require.NoError(t, err)
	assert.Empty(t, offsets)
}

func TestFindRETInstructions_RETWithImmediate(t *testing.T) {
	// RET imm16 (0xC2 xx xx): near return with stack pop
	code := []byte{0x90, 0xC2, 0x08, 0x00}
	offsets, err := findRETInstructions(code)
	require.NoError(t, err)
	assert.Equal(t, []uint64{1}, offsets)
}

func TestFindRETInstructions_RealisticGoFunction(t *testing.T) {
	// Simulate a Go function with multiple return paths:
	// push rbp          (0x55)
	// mov rbp, rsp      (0x48 0x89 0xe5)
	// ...nops as body...
	// pop rbp           (0x5d)
	// ret               (0xc3)  -- return path #1
	// nop padding
	// pop rbp           (0x5d)
	// ret               (0xc3)  -- return path #2
	code := []byte{
		0x55,             // push rbp
		0x48, 0x89, 0xe5, // mov rbp, rsp
		0x90, 0x90, 0x90, 0x90, // nops (body)
		0x5d, // pop rbp
		0xc3, // ret #1 at offset 9
		0x90, // nop padding
		0x5d, // pop rbp
		0xc3, // ret #2 at offset 12
	}
	offsets, err := findRETInstructions(code)
	require.NoError(t, err)
	assert.Equal(t, []uint64{9, 12}, offsets)
}

func TestFindRETInstructions_EmptyCode(t *testing.T) {
	offsets, err := findRETInstructions([]byte{})
	require.NoError(t, err)
	assert.Empty(t, offsets)
}

func TestFindRETInstructions_OnlyRET(t *testing.T) {
	code := []byte{0xC3}
	offsets, err := findRETInstructions(code)
	require.NoError(t, err)
	assert.Equal(t, []uint64{0}, offsets)
}

func TestFindRETOffsets_ZeroSize(t *testing.T) {
	d := NewX86Disassembler()
	_, err := d.FindRETOffsets("/dev/null", 0, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "function size is zero")
}

func TestFindRETOffsets_BadPath(t *testing.T) {
	d := NewX86Disassembler()
	_, err := d.FindRETOffsets("/nonexistent/binary", 0, 100)
	assert.Error(t, err)
}

// ARM64 RET instruction: 0xD65F03C0 stored little-endian as [0xC0, 0x03, 0x5F, 0xD6].

func TestFindRETInstructionsARM64_SimpleRET(t *testing.T) {
	// MOV x0, xzr (NOP equivalent) followed by RET X30.
	// MOV x0, xzr = 0xAA1F03E0 → [E0 03 1F AA]
	// RET X30     = 0xD65F03C0 → [C0 03 5F D6]
	code := []byte{
		0xE0, 0x03, 0x1F, 0xAA, // mov x0, xzr
		0xC0, 0x03, 0x5F, 0xD6, // ret
	}
	offsets, err := findRETInstructionsARM64(code)
	require.NoError(t, err)
	assert.Equal(t, []uint64{4}, offsets)
}

func TestFindRETInstructionsARM64_MultipleRET(t *testing.T) {
	// Two return paths: NOP-equiv, RET, NOP-equiv, NOP-equiv, RET
	nop := []byte{0xE0, 0x03, 0x1F, 0xAA} // mov x0, xzr
	ret := []byte{0xC0, 0x03, 0x5F, 0xD6} // ret
	code := append(append(append(append(nop, ret...), nop...), nop...), ret...)
	offsets, err := findRETInstructionsARM64(code)
	require.NoError(t, err)
	assert.Equal(t, []uint64{4, 16}, offsets)
}

func TestFindRETInstructionsARM64_NoRET(t *testing.T) {
	// Tail call: B (branch) instead of RET
	// B #0 = 0x14000000 → [00 00 00 14]
	code := []byte{
		0xE0, 0x03, 0x1F, 0xAA, // mov x0, xzr
		0x00, 0x00, 0x00, 0x14, // b
	}
	offsets, err := findRETInstructionsARM64(code)
	require.NoError(t, err)
	assert.Empty(t, offsets)
}

func TestFindRETInstructionsARM64_EmptyCode(t *testing.T) {
	offsets, err := findRETInstructionsARM64([]byte{})
	require.NoError(t, err)
	assert.Empty(t, offsets)
}

func TestFindRETInstructionsARM64_OnlyRET(t *testing.T) {
	code := []byte{0xC0, 0x03, 0x5F, 0xD6} // ret
	offsets, err := findRETInstructionsARM64(code)
	require.NoError(t, err)
	assert.Equal(t, []uint64{0}, offsets)
}
