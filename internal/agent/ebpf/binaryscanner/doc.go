// Package binaryscanner implements agentless binary scanning for uprobe discovery.
//
// This package provides functionality to extract function metadata from process binaries
// without requiring SDK integration. It implements RFD 065 - Agentless Binary Scanning
// for Uprobe Discovery.
//
// # Overview
//
// The scanner discovers process binaries, parses DWARF debug information, and caches
// the results for efficient repeated access. It handles container namespace issues by
// supporting multiple access methods (direct, nsenter, CRI).
//
// # Usage
//
//	cfg := binaryscanner.DefaultConfig()
//	cfg.AccessMethod = binaryscanner.AccessMethodDirect
//
//	scanner, err := binaryscanner.NewScanner(cfg)
//	if err != nil {
//		return err
//	}
//	defer scanner.Close()
//
//	// Get metadata for a specific function.
//	meta, err := scanner.GetFunctionMetadata(ctx, pid, "main.myFunction")
//	if err != nil {
//		return err
//	}
//
//	fmt.Printf("Function %s at offset 0x%x\n", meta.Name, meta.Offset)
//
// # Access Methods
//
// - AccessMethodDirect: Read /proc/<pid>/exe directly (works in shared PID namespace)
// - AccessMethodNsenter: Use nsenter to enter container namespace (requires CAP_SYS_ADMIN)
// - AccessMethodCRI: Use container runtime (not yet implemented)
//
// # Caching
//
// The scanner caches parsed function metadata by binary hash to avoid repeated parsing.
// Cache entries have a TTL and the cache size is limited.
//
// # Limitations
//
// - Requires DWARF debug info in binaries (fails on stripped binaries)
// - nsenter method requires CAP_SYS_ADMIN capability
// - Only works with compiled languages (Go, Rust, C/C++)
package binaryscanner
