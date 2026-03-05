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
