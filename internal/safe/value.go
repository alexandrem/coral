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

// Uint32ToInt32 safely converts an uint32 value to int32, clamping to math.MaxInt32 if overflow
// would occur.
// Returns the converted value and a boolean indicating whether clamping occurred.
func Uint32ToInt32(val uint32) (int32, bool) {
	if val > math.MaxInt32 {
		return math.MaxInt32, true
	}
	return int32(val), false
}
