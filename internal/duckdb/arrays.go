// Package duckdb provides utilities for working with DuckDB data types and formats.
package duckdb

import (
	"fmt"
	"strings"
	"unsafe"
)

// Float64ArrayToString converts []float64 to DuckDB array string format.
// Example: [1.0, 2.5, 3.7] -> "[1.000000, 2.500000, 3.700000]"
// This format is required for casting to FLOAT[] types in DuckDB.
func Float64ArrayToString(vec []float64) string {
	if len(vec) == 0 {
		return "[]"
	}

	var sb strings.Builder
	sb.WriteString("[")
	for i, v := range vec {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(fmt.Sprintf("%f", v))
	}
	sb.WriteString("]")
	return sb.String()
}

// Float32ToFloat64 converts []float32 to []float64.
func Float32ToFloat64(arr []float32) []float64 {
	result := make([]float64, len(arr))
	for i, v := range arr {
		result[i] = float64(v)
	}
	return result
}

// BytesToFloat32Array converts DuckDB FLOAT array bytes to []float32.
// DuckDB stores FLOAT[N] arrays as raw bytes that need to be decoded.
func BytesToFloat32Array(data []byte) []float32 {
	if len(data) == 0 {
		return nil
	}

	// DuckDB FLOAT is 4 bytes per value.
	const floatSize = 4
	numFloats := len(data) / floatSize

	result := make([]float32, numFloats)
	for i := 0; i < numFloats; i++ {
		offset := i * floatSize
		// Convert 4 bytes to float32 (little-endian).
		bits := uint32(data[offset]) | uint32(data[offset+1])<<8 | uint32(data[offset+2])<<16 | uint32(data[offset+3])<<24
		//nolint:gosec // G103: unsafe required for binary float conversion
		result[i] = *(*float32)(unsafe.Pointer(&bits))
	}

	return result
}

// Float32ArrayToBytes converts []float32 to bytes for DuckDB storage.
func Float32ArrayToBytes(arr []float32) []byte {
	if len(arr) == 0 {
		return nil
	}

	const floatSize = 4
	data := make([]byte, len(arr)*floatSize)

	for i, f := range arr {
		offset := i * floatSize
		//nolint:gosec // G103: unsafe required for binary float conversion
		bits := *(*uint32)(unsafe.Pointer(&f))
		// Convert float32 to 4 bytes (little-endian).
		data[offset] = byte(bits)
		data[offset+1] = byte(bits >> 8)
		data[offset+2] = byte(bits >> 16)
		data[offset+3] = byte(bits >> 24)
	}

	return data
}
