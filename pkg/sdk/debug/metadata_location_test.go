package debug

import (
	"testing"
)

// TestParseLocationExpr_Register tests DW_OP_reg0-31 opcodes.
func TestParseLocationExpr_Register(t *testing.T) {
	tests := []struct {
		name     string
		expr     []byte
		wantReg  int
		wantType LocationType
	}{
		{"reg0", []byte{0x50}, 0, LocationRegister},
		{"reg1", []byte{0x51}, 1, LocationRegister},
		{"reg7", []byte{0x57}, 7, LocationRegister},
		{"reg15", []byte{0x5f}, 15, LocationRegister},
		{"reg31", []byte{0x6f}, 31, LocationRegister},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc, err := parseLocationExpr(tt.expr)
			if err != nil {
				t.Fatalf("parseLocationExpr() error = %v", err)
			}
			if loc.Type != tt.wantType {
				t.Errorf("Type = %v, want %v", loc.Type, tt.wantType)
			}
			if loc.Register != tt.wantReg {
				t.Errorf("Register = %d, want %d", loc.Register, tt.wantReg)
			}
			if loc.Offset != 0 {
				t.Errorf("Offset = %d, want 0", loc.Offset)
			}
		})
	}
}

// TestParseLocationExpr_Regx tests DW_OP_regx opcode with ULEB128.
func TestParseLocationExpr_Regx(t *testing.T) {
	tests := []struct {
		name    string
		expr    []byte
		wantReg int
	}{
		{"regx 0", []byte{0x90, 0x00}, 0},
		{"regx 5", []byte{0x90, 0x05}, 5},
		{"regx 127", []byte{0x90, 0x7f}, 127},
		{"regx 128", []byte{0x90, 0x80, 0x01}, 128}, // ULEB128: 0x80 0x01 = 128
		{"regx 255", []byte{0x90, 0xff, 0x01}, 255}, // ULEB128: 0xff 0x01 = 255
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc, err := parseLocationExpr(tt.expr)
			if err != nil {
				t.Fatalf("parseLocationExpr() error = %v", err)
			}
			if loc.Type != LocationRegister {
				t.Errorf("Type = %v, want LocationRegister", loc.Type)
			}
			if loc.Register != tt.wantReg {
				t.Errorf("Register = %d, want %d", loc.Register, tt.wantReg)
			}
		})
	}
}

// TestParseLocationExpr_Fbreg tests DW_OP_fbreg opcode.
func TestParseLocationExpr_Fbreg(t *testing.T) {
	tests := []struct {
		name       string
		expr       []byte
		wantOffset int64
	}{
		{"fbreg 0", []byte{0x91, 0x00}, 0},
		{"fbreg 8", []byte{0x91, 0x08}, 8},
		{"fbreg 63", []byte{0x91, 0x3f}, 63},                      // SLEB128: 0x3f = 63 (max positive single byte)
		{"fbreg -1", []byte{0x91, 0x7f}, -1},                      // SLEB128: 0x7f = -1
		{"fbreg -8", []byte{0x91, 0x78}, -8},                      // SLEB128: 0x78 = -8
		{"fbreg -16", []byte{0x91, 0x70}, -16},                    // SLEB128: 0x70 = -16
		{"fbreg 127", []byte{0x91, 0xff, 0x00}, 127},              // SLEB128: 0xff 0x00 = 127
		{"fbreg 128", []byte{0x91, 0x80, 0x01}, 128},              // SLEB128: 0x80 0x01 = 128
		{"fbreg -128", []byte{0x91, 0x80, 0x7f}, -128},            // SLEB128: 0x80 0x7f = -128
		{"fbreg large", []byte{0x91, 0xff, 0xff, 0x03}, 0xffff},   // SLEB128
		{"fbreg -large", []byte{0x91, 0x81, 0x80, 0x7c}, -0xffff}, // SLEB128
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc, err := parseLocationExpr(tt.expr)
			if err != nil {
				t.Fatalf("parseLocationExpr() error = %v", err)
			}
			if loc.Type != LocationFrameBase {
				t.Errorf("Type = %v, want LocationFrameBase", loc.Type)
			}
			if loc.Offset != tt.wantOffset {
				t.Errorf("Offset = %d, want %d", loc.Offset, tt.wantOffset)
			}
		})
	}
}

// TestParseLocationExpr_Breg tests DW_OP_breg0-31 opcodes.
func TestParseLocationExpr_Breg(t *testing.T) {
	tests := []struct {
		name       string
		expr       []byte
		wantReg    int
		wantOffset int64
	}{
		{"breg0+0", []byte{0x70, 0x00}, 0, 0},
		{"breg1+8", []byte{0x71, 0x08}, 1, 8},
		{"breg7+16", []byte{0x77, 0x10}, 7, 16},
		{"breg31+0", []byte{0x8f, 0x00}, 31, 0},
		{"breg5-8", []byte{0x75, 0x78}, 5, -8},            // SLEB128: 0x78 = -8
		{"breg10+128", []byte{0x7a, 0x80, 0x01}, 10, 128}, // SLEB128: 0x80 0x01 = 128
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc, err := parseLocationExpr(tt.expr)
			if err != nil {
				t.Fatalf("parseLocationExpr() error = %v", err)
			}
			if loc.Type != LocationRegister {
				t.Errorf("Type = %v, want LocationRegister", loc.Type)
			}
			if loc.Register != tt.wantReg {
				t.Errorf("Register = %d, want %d", loc.Register, tt.wantReg)
			}
			if loc.Offset != tt.wantOffset {
				t.Errorf("Offset = %d, want %d", loc.Offset, tt.wantOffset)
			}
		})
	}
}

// TestParseLocationExpr_Addr tests DW_OP_addr opcode.
func TestParseLocationExpr_Addr(t *testing.T) {
	tests := []struct {
		name     string
		expr     []byte
		wantAddr uint64
	}{
		{"addr 0", []byte{0x03, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, 0},
		{"addr 0x1000", []byte{0x03, 0x00, 0x10, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, 0x1000},
		{"addr 0xdeadbeef", []byte{0x03, 0xef, 0xbe, 0xad, 0xde, 0x00, 0x00, 0x00, 0x00}, 0xdeadbeef},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc, err := parseLocationExpr(tt.expr)
			if err != nil {
				t.Fatalf("parseLocationExpr() error = %v", err)
			}
			if loc.Type != LocationMemory {
				t.Errorf("Type = %v, want LocationMemory", loc.Type)
			}
			if loc.Address != tt.wantAddr {
				t.Errorf("Address = 0x%x, want 0x%x", loc.Address, tt.wantAddr)
			}
		})
	}
}

// TestParseLocationExpr_Errors tests error cases.
func TestParseLocationExpr_Errors(t *testing.T) {
	tests := []struct {
		name    string
		expr    []byte
		wantErr bool
	}{
		{"empty", []byte{}, true},
		{"fbreg truncated", []byte{0x91}, true},
		{"breg truncated", []byte{0x70}, true},
		{"regx truncated", []byte{0x90}, true},
		{"addr truncated", []byte{0x03, 0x00}, true},
		{"unsupported opcode", []byte{0xff}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseLocationExpr(tt.expr)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseLocationExpr() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestDecodeULEB128 tests ULEB128 decoding.
func TestDecodeULEB128(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		wantValue uint64
		wantBytes int
	}{
		{"0", []byte{0x00}, 0, 1},
		{"1", []byte{0x01}, 1, 1},
		{"127", []byte{0x7f}, 127, 1},
		{"128", []byte{0x80, 0x01}, 128, 2},
		{"255", []byte{0xff, 0x01}, 255, 2},
		{"300", []byte{0xac, 0x02}, 300, 2},
		{"16384", []byte{0x80, 0x80, 0x01}, 16384, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, n := decodeULEB128(tt.data)
			if val != tt.wantValue {
				t.Errorf("value = %d, want %d", val, tt.wantValue)
			}
			if n != tt.wantBytes {
				t.Errorf("bytes = %d, want %d", n, tt.wantBytes)
			}
		})
	}
}

// TestDecodeSLEB128 tests SLEB128 decoding.
func TestDecodeSLEB128(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		wantValue int64
		wantBytes int
	}{
		{"0", []byte{0x00}, 0, 1},
		{"1", []byte{0x01}, 1, 1},
		{"63", []byte{0x3f}, 63, 1},                   // 0x3f = 63 (max positive single byte)
		{"-1", []byte{0x7f}, -1, 1},                   // 0x7f = -1 in SLEB128
		{"-2", []byte{0x7e}, -2, 1},                   // 0x7e = -2
		{"-8", []byte{0x78}, -8, 1},                   // 0x78 = -8
		{"-16", []byte{0x70}, -16, 1},                 // 0x70 = -16
		{"127", []byte{0xff, 0x00}, 127, 2},           // 0xff 0x00 = 127
		{"128", []byte{0x80, 0x01}, 128, 2},           // 0x80 0x01 = 128
		{"-128", []byte{0x80, 0x7f}, -128, 2},         // 0x80 0x7f = -128
		{"300", []byte{0xac, 0x02}, 300, 2},           // 0xac 0x02 = 300
		{"-300", []byte{0xd4, 0x7d}, -300, 2},         // 0xd4 0x7d = -300
		{"16384", []byte{0x80, 0x80, 0x01}, 16384, 3}, // Multi-byte positive
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, n := decodeSLEB128(tt.data)
			if val != tt.wantValue {
				t.Errorf("value = %d, want %d", val, tt.wantValue)
			}
			if n != tt.wantBytes {
				t.Errorf("bytes = %d, want %d", n, tt.wantBytes)
			}
		})
	}
}

// TestLocationString tests the String() method.
func TestLocationString(t *testing.T) {
	tests := []struct {
		name string
		loc  *Location
		want string
	}{
		{"reg0", &Location{Type: LocationRegister, Register: 0}, "reg0"},
		{"reg5", &Location{Type: LocationRegister, Register: 5}, "reg5"},
		{"reg7+8", &Location{Type: LocationRegister, Register: 7, Offset: 8}, "reg7+8"},
		{"fbreg-16", &Location{Type: LocationFrameBase, Offset: -16}, "fbreg-16"},
		{"fbreg+8", &Location{Type: LocationFrameBase, Offset: 8}, "fbreg+8"},
		{"addr", &Location{Type: LocationMemory, Address: 0x1000}, "addr:0x1000"},
		{"unknown", &Location{Type: LocationUnknown}, "<unknown>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.loc.String()
			if got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestParseLocationExpr_RealWorld tests with real-world DWARF expressions.
func TestParseLocationExpr_RealWorld(t *testing.T) {
	tests := []struct {
		name        string
		expr        []byte
		wantType    LocationType
		description string
	}{
		{
			"go arm64 arg0",
			[]byte{0x50}, // DW_OP_reg0 (X0 on ARM64)
			LocationRegister,
			"First argument in X0 register (ARM64)",
		},
		{
			"go arm64 arg1",
			[]byte{0x51}, // DW_OP_reg1 (X1 on ARM64)
			LocationRegister,
			"Second argument in X1 register (ARM64)",
		},
		{
			"stack arg",
			[]byte{0x91, 0x10}, // DW_OP_fbreg +16
			LocationFrameBase,
			"Stack argument at FP+16",
		},
		{
			"local var",
			[]byte{0x91, 0x70}, // DW_OP_fbreg -16 (0x70 = -16 in SLEB128)
			LocationFrameBase,
			"Local variable at FP-16",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc, err := parseLocationExpr(tt.expr)
			if err != nil {
				t.Fatalf("parseLocationExpr() error = %v", err)
			}
			if loc.Type != tt.wantType {
				t.Errorf("Type = %v, want %v", loc.Type, tt.wantType)
			}
			t.Logf("%s: %s", tt.description, loc.String())
		})
	}
}
