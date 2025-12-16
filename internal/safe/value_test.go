package safe

import (
	"math"
	"testing"
)

func TestSafeUint64ToInt64(t *testing.T) {
	tests := []struct {
		name            string
		input           uint64
		expectedValue   int64
		expectedClamped bool
	}{
		{
			name:            "zero value",
			input:           0,
			expectedValue:   0,
			expectedClamped: false,
		},
		{
			name:            "small positive value",
			input:           12345,
			expectedValue:   12345,
			expectedClamped: false,
		},
		{
			name:            "max int64 value",
			input:           math.MaxInt64,
			expectedValue:   math.MaxInt64,
			expectedClamped: false,
		},
		{
			name:            "max int64 plus one (overflow)",
			input:           math.MaxInt64 + 1,
			expectedValue:   math.MaxInt64,
			expectedClamped: true,
		},
		{
			name:            "max uint64 value (overflow)",
			input:           math.MaxUint64,
			expectedValue:   math.MaxInt64,
			expectedClamped: true,
		},
		{
			name:            "large value below max int64",
			input:           math.MaxInt64 - 1000,
			expectedValue:   math.MaxInt64 - 1000,
			expectedClamped: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, clamped := Uint64ToInt64(tt.input)
			if value != tt.expectedValue {
				t.Errorf("Uint64ToInt64(%d) value = %d, expected %d", tt.input, value, tt.expectedValue)
			}
			if clamped != tt.expectedClamped {
				t.Errorf("Uint64ToInt64(%d) clamped = %v, expected %v", tt.input, clamped, tt.expectedClamped)
			}
		})
	}
}
