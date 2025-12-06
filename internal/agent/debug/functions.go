// Package debug implements function discovery via DWARF introspection.
// It reuses the SDK's FunctionMetadataProvider for cross-platform DWARF extraction.
package debug

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/pkg/sdk/debug"
)

// FunctionDiscoverer discovers functions from binary DWARF debug info.
type FunctionDiscoverer struct {
	logger zerolog.Logger
}

// NewFunctionDiscoverer creates a new function discoverer.
func NewFunctionDiscoverer(logger zerolog.Logger) *FunctionDiscoverer {
	return &FunctionDiscoverer{
		logger: logger,
	}
}

// DiscoverFunctions extracts function metadata from a binary using DWARF debug info.
// Returns a list of FunctionInfo messages suitable for gRPC responses.
// This uses the SDK's FunctionMetadataProvider for robust cross-platform extraction.
func (d *FunctionDiscoverer) DiscoverFunctions(binaryPath, serviceName string) ([]*agentv1.FunctionInfo, error) {
	d.logger.Debug().
		Str("binary", binaryPath).
		Str("service", serviceName).
		Msg("Discovering functions from binary")

	// Create a temporary provider to extract metadata.
	// We need to create a custom slog.Logger from zerolog for SDK compatibility.
	slogger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn, // Reduce verbosity for discovery
	}))

	// Since the SDK provider expects the current process binary,
	// we'll need to create a custom provider for an external binary.
	// For now, if the binary is the current process, use the SDK directly.
	// Otherwise, we'll use a simpler approach.

	// Check if this is the current process.
	currentBinary, err := os.Executable()
	if err == nil && binaryPath == currentBinary {
		// Use SDK provider for current process.
		provider, err := debug.NewFunctionMetadataProvider(slogger)
		if err != nil {
			return nil, fmt.Errorf("failed to create function metadata provider: %w", err)
		}
		defer func() {
			if closeErr := provider.Close(); closeErr != nil {
				d.logger.Warn().Err(closeErr).Msg("Failed to close function metadata provider")
			}
		}()

		// Get all functions from the index.
		basicFunctions := provider.ListAllFunctions()

		// Convert to protobuf format.
		functions := make([]*agentv1.FunctionInfo, 0, len(basicFunctions))
		for _, fn := range basicFunctions {
			functions = append(functions, &agentv1.FunctionInfo{
				Name:        fn.Name,
				Package:     extractPackageName(fn.Name),
				FilePath:    fn.File,
				LineNumber:  int32(fn.Line),
				Offset:      int64(fn.Offset),
				HasDwarf:    provider.HasDWARF(),
				ServiceName: serviceName,
			})
		}

		d.logger.Info().
			Int("function_count", len(functions)).
			Str("binary", binaryPath).
			Str("service", serviceName).
			Msg("Function discovery completed using SDK provider")

		return functions, nil
	}

	// For external binaries, create a custom DWARF extractor.
	// This is a simplified version that just extracts basic info.
	functions, err := d.extractFunctionsFromExternalBinary(binaryPath, serviceName)
	if err != nil {
		return nil, fmt.Errorf("failed to extract functions from external binary: %w", err)
	}

	d.logger.Info().
		Int("function_count", len(functions)).
		Str("binary", binaryPath).
		Str("service", serviceName).
		Msg("Function discovery completed from external binary")

	return functions, nil
}

// extractFunctionsFromExternalBinary extracts functions from an external binary.
// This is a simplified extractor for binaries that are not the current process.
// For production use, this should be enhanced with proper DWARF/symbol table parsing.
func (d *FunctionDiscoverer) extractFunctionsFromExternalBinary(binaryPath, serviceName string) ([]*agentv1.FunctionInfo, error) {
	// For now, return empty list with a warning.
	// TODO: Implement full external binary parsing using debug/elf and debug/dwarf.
	d.logger.Warn().
		Str("binary", binaryPath).
		Msg("External binary function discovery not yet fully implemented")

	return []*agentv1.FunctionInfo{}, nil
}

// extractPackageName extracts the package name from a fully-qualified Go function name.
// Examples:
//   - "main.handleCheckout" → "main"
//   - "github.com/foo/bar.ProcessPayment" → "github.com/foo/bar"
//   - "(*Handler).ServeHTTP" → "" (method)
func extractPackageName(functionName string) string {
	// Handle methods (e.g., "(*Type).Method" or "Type.Method").
	if strings.HasPrefix(functionName, "(") {
		return ""
	}

	// Find the last dot before the function name.
	lastDot := strings.LastIndex(functionName, ".")
	if lastDot == -1 {
		return ""
	}

	return functionName[:lastDot]
}

// GetBinaryPathForService returns the binary path for a monitored service.
// This is a helper function that can be extended to support different discovery methods.
func GetBinaryPathForService(serviceName string, pid int32) (string, error) {
	// For now, we'll use /proc/<pid>/exe to find the binary path.
	// This works on Linux and is the most straightforward approach.
	if pid <= 0 {
		return "", fmt.Errorf("invalid PID: %d", pid)
	}

	binaryPath := fmt.Sprintf("/proc/%d/exe", pid)

	// Resolve symlink to get actual binary path.
	resolvedPath, err := filepath.EvalSymlinks(binaryPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve binary path: %w", err)
	}

	return resolvedPath, nil
}
