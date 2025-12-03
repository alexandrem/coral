// Package main provides a sample binary for testing DWARF file/line extraction.
//
// This binary is built with full DWARF debug symbols to test the metadata
// provider's ability to extract source file and line information from DWARF
// line tables.
//
// Build: go generate (or manually: go build -o sample_with_dwarf sample.go)
// nolint:errcheck
package main

import (
	"fmt"
)

// SampleFunction is a test function with debug info.
func SampleFunction(a int, b string) (int, error) {
	fmt.Println("Hello from SampleFunction")
	return a * 2, nil
}

// AnotherFunction is another test function.
func AnotherFunction() {
	fmt.Println("Hello from AnotherFunction")
}

func main() {
	SampleFunction(42, "test")
	AnotherFunction()
}
