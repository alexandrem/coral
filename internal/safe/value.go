package safe

import (
	"math"
)

const (
	maxInt32 = int32(math.MaxInt32)
	minInt32 = int32(math.MinInt32)
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
	if val > int(maxInt32) {
		return maxInt32, true
	}
	if val < int(minInt32) {
		return minInt32, true
	}
	return int32(val), false
}

// Int32ToUint32 safely converts an int32 value to uint32, clamping to zero
// if overflow would occur.
func Int32ToUint32(val int32) (uint32, bool) {
	if val < 0 {
		return 0, true // clamped because negative
	}
	return uint32(val), false
}

// IntToUint64 safely converts an int value to uint64, clamping to zero
// if overflow would occur.
func IntToUint64(val int) (uint64, bool) {
	if val < 0 {
		return 0, true // clamped because negative
	}
	return uint64(val), false
}

// Uint64ToUint32 safely converts a uint64 value to uint32, clamping to math.MaxUint32
// if overflow would occur.
// Returns the converted value and a boolean indicating whether clamping occurred.
func Uint64ToUint32(val uint64) (uint32, bool) {
	if val > math.MaxUint32 {
		return math.MaxUint32, true
	}
	return uint32(val), false
}
