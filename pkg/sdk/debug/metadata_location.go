package debug

import (
	"encoding/binary"
	"fmt"
)

// LocationType describes the type of location (register, stack, etc.).
type LocationType int

const (
	LocationUnknown LocationType = iota
	LocationRegister
	LocationFrameBase // Frame base relative (stack)
	LocationMemory
)

// Location represents a parsed DWARF location expression.
type Location struct {
	Type     LocationType
	Register int    // Register number (for LocationRegister)
	Offset   int64  // Offset (for LocationFrameBase or register-relative)
	Address  uint64 // Absolute address (for LocationMemory)
}

// DWARF location expression opcodes.
const (
	opAddr   = 0x03 // Constant address
	opReg0   = 0x50 // Register 0
	opReg31  = 0x6f // Register 31
	opBreg0  = 0x70 // Base register 0 + offset
	opBreg31 = 0x8f // Base register 31 + offset
	opRegx   = 0x90 // Register with ULEB128 number
	opFbreg  = 0x91 // Frame base relative
)

// parseLocationExpr parses a DWARF location expression.
// This handles the most common cases for function parameters:
// - DW_OP_reg0-31: value in register
// - DW_OP_regx: value in register (extended)
// - DW_OP_fbreg: frame base relative (stack)
// - DW_OP_breg0-31: base register + offset
func parseLocationExpr(expr []byte) (*Location, error) {
	if len(expr) == 0 {
		return nil, fmt.Errorf("empty location expression")
	}

	op := expr[0]
	loc := &Location{}

	switch {
	case op >= opReg0 && op <= opReg31:
		// DW_OP_reg0-31: value is in register N
		loc.Type = LocationRegister
		loc.Register = int(op - opReg0)
		return loc, nil

	case op == opRegx:
		// DW_OP_regx: register number encoded as ULEB128
		if len(expr) < 2 {
			return nil, fmt.Errorf("DW_OP_regx: truncated expression")
		}
		regNum, n := decodeULEB128(expr[1:])
		if n == 0 {
			return nil, fmt.Errorf("DW_OP_regx: invalid ULEB128")
		}
		loc.Type = LocationRegister
		loc.Register = int(regNum)
		return loc, nil

	case op == opFbreg:
		// DW_OP_fbreg: frame base relative (stack offset)
		if len(expr) < 2 {
			return nil, fmt.Errorf("DW_OP_fbreg: truncated expression")
		}
		offset, n := decodeSLEB128(expr[1:])
		if n == 0 {
			return nil, fmt.Errorf("DW_OP_fbreg: invalid SLEB128")
		}
		loc.Type = LocationFrameBase
		loc.Offset = offset
		return loc, nil

	case op >= opBreg0 && op <= opBreg31:
		// DW_OP_breg0-31: base register + SLEB128 offset
		if len(expr) < 2 {
			return nil, fmt.Errorf("DW_OP_breg: truncated expression")
		}
		offset, n := decodeSLEB128(expr[1:])
		if n == 0 {
			return nil, fmt.Errorf("DW_OP_breg: invalid SLEB128")
		}
		loc.Type = LocationRegister // Register-relative
		loc.Register = int(op - opBreg0)
		loc.Offset = offset
		return loc, nil

	case op == opAddr:
		// DW_OP_addr: constant address (size depends on architecture)
		if len(expr) < 9 {
			return nil, fmt.Errorf("DW_OP_addr: truncated expression")
		}
		// Assume 64-bit address for now
		addr := binary.LittleEndian.Uint64(expr[1:9])
		loc.Type = LocationMemory
		loc.Address = addr
		return loc, nil

	default:
		// Unknown or unsupported opcode
		return nil, fmt.Errorf("unsupported location opcode: 0x%02x", op)
	}
}

// decodeULEB128 decodes an unsigned LEB128 value.
// Returns the value and number of bytes consumed.
func decodeULEB128(data []byte) (uint64, int) {
	var result uint64
	var shift uint
	var i int

	for i = 0; i < len(data) && i < 10; i++ {
		b := data[i]
		result |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return result, i + 1
		}
		shift += 7
	}

	return 0, 0 // Invalid or truncated
}

// decodeSLEB128 decodes a signed LEB128 value.
// Returns the value and number of bytes consumed.
func decodeSLEB128(data []byte) (int64, int) {
	var result int64
	var shift uint
	var i int
	var b byte

	for i = 0; i < len(data) && i < 10; i++ {
		b = data[i]
		result |= int64(b&0x7f) << shift
		shift += 7
		if b&0x80 == 0 {
			// Last byte - check for sign extension
			if shift < 64 && (b&0x40) != 0 {
				// Sign bit is set, extend with 1s
				result |= -(1 << shift)
			}
			return result, i + 1
		}
	}

	// Truncated or invalid
	return 0, 0
}

// String returns a human-readable description of the location.
func (l *Location) String() string {
	switch l.Type {
	case LocationRegister:
		if l.Offset != 0 {
			return fmt.Sprintf("reg%d%+d", l.Register, l.Offset)
		}
		return fmt.Sprintf("reg%d", l.Register)
	case LocationFrameBase:
		return fmt.Sprintf("fbreg%+d", l.Offset)
	case LocationMemory:
		return fmt.Sprintf("addr:0x%x", l.Address)
	default:
		return "<unknown>"
	}
}
