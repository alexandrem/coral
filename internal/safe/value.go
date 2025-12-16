package safe

import (
	"math"
)

// Uint64ToInt64 safely converts an uint64 value to int64, clamping to math.MaxInt64 if overflow
// would occur.
// Returns the converted value and a boolean indicating whether clamping occurred.
func Uint64ToInt64(val uint64) (int64, bool) {
	if val > math.MaxInt64 {
		return math.MaxInt64, true
	}
	return int64(val), false
}

// IntToInt32 safely converts an int value to int32, clamping to math.MaxInt32 or math.MinInt32
// if overflow would occur.
// On 64-bit systems, int is 64 bits and can exceed int32 range.
// Returns the converted value and a boolean indicating whether clamping occurred.
func IntToInt32(val int) (int32, bool) {
	if val > math.MaxInt32 {
		return math.MaxInt32, true
	}
	if val < math.MinInt32 {
		return math.MinInt32, true
	}
	return int32(val), false
}
