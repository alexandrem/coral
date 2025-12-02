// Package debug provides SDK-integrated debugging capabilities for Go applications.
//
// The debug package enables runtime introspection and instrumentation of Go
// applications through DWARF debug information and symbol table analysis.
// It extracts function metadata including offsets, arguments, and return values
// needed for eBPF uprobe attachment without requiring code modifications.
//
// Core functionality:
//   - Function metadata extraction from DWARF debug information
//   - Symbol table fallback for stripped binaries (when only -w is used)
//   - Support for ELF (Linux) and Mach-O (macOS) binary formats
//   - gRPC server exposing debug metadata to agents and the Colony
//   - Caching layer for efficient repeated function lookups
//
// The package serves as the bridge between the Coral SDK and the debugging
// infrastructure, allowing agents to attach uprobes to functions in running
// SDK-integrated applications with full type information for arguments and
// return values.
//
// Example usage in an SDK-integrated application:
//
//	provider, _ := debug.NewFunctionMetadataProvider(logger, binaryPath, pid)
//	server, _ := debug.NewServer(logger, provider)
//	server.Start()
//
// The server exposes function metadata that agents can query to enable
// precise debugging without source code access or application restarts.
package debug
