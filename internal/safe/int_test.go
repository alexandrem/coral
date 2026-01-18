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

func TestIntToInt32(t *testing.T) {
	tests := []struct {
		name            string
		input           int
		expectedValue   int32
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
			name:            "small negative value",
			input:           -12345,
			expectedValue:   -12345,
			expectedClamped: false,
		},
		{
			name:            "max int32 value",
			input:           math.MaxInt32,
			expectedValue:   math.MaxInt32,
			expectedClamped: false,
		},
		{
			name:            "min int32 value",
			input:           math.MinInt32,
			expectedValue:   math.MinInt32,
			expectedClamped: false,
		},
		{
			name:            "max int32 plus one (overflow)",
			input:           math.MaxInt32 + 1,
			expectedValue:   math.MaxInt32,
			expectedClamped: true,
		},
		{
			name:            "min int32 minus one (underflow)",
			input:           math.MinInt32 - 1,
			expectedValue:   math.MinInt32,
			expectedClamped: true,
		},
		{
			name:            "large positive value (overflow)",
			input:           math.MaxInt32 + 1000000,
			expectedValue:   math.MaxInt32,
			expectedClamped: true,
		},
		{
			name:            "large negative value (underflow)",
			input:           math.MinInt32 - 1000000,
			expectedValue:   math.MinInt32,
			expectedClamped: true,
		},
		{
			name:            "value just below max int32",
			input:           math.MaxInt32 - 1,
			expectedValue:   math.MaxInt32 - 1,
			expectedClamped: false,
		},
		{
			name:            "value just above min int32",
			input:           math.MinInt32 + 1,
			expectedValue:   math.MinInt32 + 1,
			expectedClamped: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, clamped := IntToInt32(tt.input)
			if value != tt.expectedValue {
				t.Errorf("IntToInt32(%d) value = %d, expected %d", tt.input, value, tt.expectedValue)
			}
			if clamped != tt.expectedClamped {
				t.Errorf("IntToInt32(%d) clamped = %v, expected %v", tt.input, clamped, tt.expectedClamped)
			}
		})
	}
}

func TestUint64ToUint32(t *testing.T) {
	tests := []struct {
		name            string
		input           uint64
		expectedValue   uint32
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
			name:            "max uint32 value",
			input:           math.MaxUint32,
			expectedValue:   math.MaxUint32,
			expectedClamped: false,
		},
		{
			name:            "max uint32 plus one (overflow)",
			input:           math.MaxUint32 + 1,
			expectedValue:   math.MaxUint32,
			expectedClamped: true,
		},
		{
			name:            "max uint64 value (overflow)",
			input:           math.MaxUint64,
			expectedValue:   math.MaxUint32,
			expectedClamped: true,
		},
		{
			name:            "large value below max uint32",
			input:           math.MaxUint32 - 1000,
			expectedValue:   math.MaxUint32 - 1000,
			expectedClamped: false,
		},
		{
			name:            "realistic CPU sample count",
			input:           300000, // 1000Hz × 300sec
			expectedValue:   300000,
			expectedClamped: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, clamped := Uint64ToUint32(tt.input)
			if value != tt.expectedValue {
				t.Errorf("Uint64ToUint32(%d) value = %d, expected %d", tt.input, value, tt.expectedValue)
			}
			if clamped != tt.expectedClamped {
				t.Errorf("Uint64ToUint32(%d) clamped = %v, expected %v", tt.input, clamped, tt.expectedClamped)
			}
		})
	}
}
