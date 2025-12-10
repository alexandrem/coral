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
	"github.com/coral-mesh/coral/pkg/embedding"
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

	// Create slog.Logger from zerolog for SDK compatibility.
	slogger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn, // Reduce verbosity for discovery
	}))

	// Determine if this is the current process or an external binary.
	currentBinary, _ := os.Executable()
	isCurrentProcess := (binaryPath == currentBinary)

	// Use appropriate PID (current process PID or 0 for external binaries).
	pid := 0
	if isCurrentProcess {
		pid = os.Getpid()
	}

	// Create provider for the binary.
	provider, err := debug.NewFunctionMetadataProviderForBinary(slogger, binaryPath, pid)
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
		// Generate embedding with enrichment.
		emb := embedding.GenerateFunctionEmbedding(embedding.FunctionMetadata{
			Name:       fn.Name,
			Package:    extractPackageName(fn.Name),
			FilePath:   fn.File,
			Parameters: nil, // TODO: Extract parameters
		})

		// Convert []float64 to []float32 for protobuf.
		emb32 := make([]float32, len(emb))
		for i, v := range emb {
			emb32[i] = float32(v)
		}

		functions = append(functions, &agentv1.FunctionInfo{
			Name:        fn.Name,
			Package:     extractPackageName(fn.Name),
			FilePath:    fn.File,
			LineNumber:  int32(fn.Line),
			Offset:      int64(fn.Offset),
			HasDwarf:    provider.HasDWARF(),
			ServiceName: serviceName,
			Embedding:   emb32,
		})
	}

	d.logger.Info().
		Int("function_count", len(functions)).
		Str("binary", binaryPath).
		Str("service", serviceName).
		Bool("is_current_process", isCurrentProcess).
		Bool("has_dwarf", provider.HasDWARF()).
		Msg("Function discovery completed")

	return functions, nil
}

// extractFunctionsFromExternalBinary extracts functions from an external binary.
// This uses the SDK's FunctionMetadataProvider to extract DWARF debug info.
//
//nolint:unused
func (d *FunctionDiscoverer) extractFunctionsFromExternalBinary(binaryPath, serviceName string) ([]*agentv1.FunctionInfo, error) {
	// Create SDK logger from zerolog.
	slogger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn, // Reduce verbosity for discovery
	}))

	// Create provider for external binary (pid=0 since it's not the current process).
	provider, err := debug.NewFunctionMetadataProviderForBinary(slogger, binaryPath, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to create function metadata provider for %s: %w", binaryPath, err)
	}
	defer func() {
		if closeErr := provider.Close(); closeErr != nil {
			d.logger.Warn().Err(closeErr).Msg("Failed to close function metadata provider")
		}
	}()

	// Get all functions from the index.
	basicFunctions := provider.ListAllFunctions()

	d.logger.Debug().
		Int("function_count", len(basicFunctions)).
		Str("binary", binaryPath).
		Bool("has_dwarf", provider.HasDWARF()).
		Msg("Extracted functions from external binary")

	// Convert to protobuf format.
	functions := make([]*agentv1.FunctionInfo, 0, len(basicFunctions))
	for _, fn := range basicFunctions {
		// Generate embedding with enrichment.
		emb := embedding.GenerateFunctionEmbedding(embedding.FunctionMetadata{
			Name:       fn.Name,
			Package:    extractPackageName(fn.Name),
			FilePath:   fn.File,
			Parameters: nil, // TODO: Extract parameters
		})

		// Convert []float64 to []float32 for protobuf.
		emb32 := make([]float32, len(emb))
		for i, v := range emb {
			emb32[i] = float32(v)
		}

		functions = append(functions, &agentv1.FunctionInfo{
			Name:        fn.Name,
			Package:     extractPackageName(fn.Name),
			FilePath:    fn.File,
			LineNumber:  int32(fn.Line),
			Offset:      int64(fn.Offset),
			HasDwarf:    provider.HasDWARF(),
			ServiceName: serviceName,
			Embedding:   emb32,
		})
	}

	return functions, nil
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
