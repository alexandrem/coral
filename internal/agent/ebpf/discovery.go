package ebpf

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/coral-mesh/coral/internal/agent/ebpf/binaryscanner"
	"github.com/rs/zerolog"
)

// DiscoveryMethod represents the method used to discover function metadata.
type DiscoveryMethod string

const (
	// DiscoveryMethodSDK uses the SDK's HTTP API.
	DiscoveryMethodSDK DiscoveryMethod = "sdk"
	// DiscoveryMethodPprof uses pprof endpoints (not yet implemented).
	DiscoveryMethodPprof DiscoveryMethod = "pprof"
	// DiscoveryMethodBinary scans the binary directly.
	DiscoveryMethodBinary DiscoveryMethod = "binary"
)

// DiscoveryConfig configures the function discovery service.
type DiscoveryConfig struct {
	// EnableSDK enables SDK-based discovery (Priority 1).
	EnableSDK bool

	// EnablePprof enables pprof-based discovery (Priority 2).
	// Not yet implemented.
	EnablePprof bool

	// EnableBinaryScanning enables binary scanning (Priority 3).
	EnableBinaryScanning bool

	// BinaryScannerConfig configures the binary scanner.
	BinaryScannerConfig *binaryscanner.Config

	// Logger for debug/error messages.
	Logger *slog.Logger
}

// DefaultDiscoveryConfig returns a default discovery configuration.
func DefaultDiscoveryConfig(logger *slog.Logger) *DiscoveryConfig {
	return &DiscoveryConfig{
		EnableSDK:            true,
		EnablePprof:          false, // Not yet implemented
		EnableBinaryScanning: true,
		BinaryScannerConfig:  binaryscanner.DefaultConfig(),
		Logger:               logger,
	}
}

// DiscoveryService implements the function discovery fallback chain.
// It tries multiple methods in priority order:
// 1. SDK (if available)
// 2. pprof endpoints (if available, not yet implemented)
// 3. Binary DWARF scanning (if enabled)
// 4. Fail with helpful error message
type DiscoveryService struct {
	cfg           *DiscoveryConfig
	binaryScanner *binaryscanner.Scanner
	logger        *slog.Logger
}

// DiscoveryResult contains the result of function discovery.
type DiscoveryResult struct {
	// Method is the discovery method that succeeded.
	Method DiscoveryMethod

	// Metadata is the discovered function metadata.
	Metadata *FunctionMetadata
}

// NewDiscoveryService creates a new function discovery service.
func NewDiscoveryService(cfg *DiscoveryConfig) (*DiscoveryService, error) {
	if cfg == nil {
		cfg = DefaultDiscoveryConfig(slog.Default())
	}

	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	svc := &DiscoveryService{
		cfg:    cfg,
		logger: cfg.Logger,
	}

	// Initialize binary scanner if enabled.
	if cfg.EnableBinaryScanning {
		scanner, err := binaryscanner.NewScanner(cfg.BinaryScannerConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create binary scanner: %w", err)
		}
		svc.binaryScanner = scanner
	}

	return svc, nil
}

// DiscoverFunction discovers function metadata using the priority fallback chain.
func (s *DiscoveryService) DiscoverFunction(
	ctx context.Context,
	sdkAddr string,
	pid uint32,
	functionName string,
) (*DiscoveryResult, error) {
	s.logger.Debug("Starting function discovery",
		"function", functionName,
		"pid", pid,
		"sdk_addr", sdkAddr)

	var lastErr error

	// Priority 1: Try SDK if enabled and SDK address is provided.
	if s.cfg.EnableSDK && sdkAddr != "" {
		s.logger.Debug("Attempting SDK discovery", "sdk_addr", sdkAddr)

		// Create SDK client (we could cache this, but for now create on demand).
		logger := zerolog.New(nil) // Create a minimal zerolog for SDK client
		sdkClient := NewSDKClient(logger, sdkAddr)

		metadata, err := sdkClient.GetFunctionMetadata(ctx, functionName)
		if err == nil {
			s.logger.Info("Successfully discovered function via SDK",
				"function", functionName,
				"method", DiscoveryMethodSDK)

			return &DiscoveryResult{
				Method:   DiscoveryMethodSDK,
				Metadata: metadata,
			}, nil
		}

		s.logger.Debug("SDK discovery failed, trying next method", "error", err)
		lastErr = fmt.Errorf("SDK discovery failed: %w", err)
	}

	// Priority 2: Try pprof if enabled (not yet implemented).
	if s.cfg.EnablePprof {
		s.logger.Debug("pprof discovery not yet implemented, skipping")
	}

	// Priority 3: Try binary scanning if enabled.
	if s.cfg.EnableBinaryScanning && s.binaryScanner != nil {
		s.logger.Debug("Attempting binary scanning discovery", "pid", pid)

		scannerMeta, err := s.binaryScanner.GetFunctionMetadata(ctx, pid, functionName)
		if err == nil {
			s.logger.Info("Successfully discovered function via binary scanning",
				"function", functionName,
				"method", DiscoveryMethodBinary)

			// Convert binaryscanner.FunctionMetadata to ebpf.FunctionMetadata.
			metadata := &FunctionMetadata{
				Name:       scannerMeta.Name,
				BinaryPath: scannerMeta.BinaryPath,
				Offset:     scannerMeta.Offset,
				Pid:        scannerMeta.PID,
			}

			return &DiscoveryResult{
				Method:   DiscoveryMethodBinary,
				Metadata: metadata,
			}, nil
		}

		s.logger.Debug("Binary scanning discovery failed", "error", err)
		lastErr = fmt.Errorf("binary scanning failed: %w", err)
	}

	// Priority 4: All methods failed - return helpful error.
	return nil, s.buildHelpfulError(functionName, lastErr)
}

// buildHelpfulError creates a helpful error message when all discovery methods fail.
func (s *DiscoveryService) buildHelpfulError(functionName string, lastErr error) error {
	msg := fmt.Sprintf(`Failed to discover function %s using all available methods.

Attempted methods:
`, functionName)

	if s.cfg.EnableSDK {
		msg += "  ✗ SDK discovery (requires SDK integration in application)\n"
	}

	if s.cfg.EnablePprof {
		msg += "  ✗ pprof discovery (not yet implemented)\n"
	}

	if s.cfg.EnableBinaryScanning {
		msg += "  ✗ Binary DWARF scanning\n"
	}

	msg += "\nRecommendations:\n"
	msg += "  1. Integrate Coral SDK in your application (best option):\n"
	msg += "     import \"github.com/coral-mesh/coral/pkg/sdk/debug\"\n"
	msg += "     debug.EnableRuntimeMonitoring()\n\n"

	msg += "  2. Ensure binary has DWARF debug symbols:\n"
	msg += "     Build with: go build (without -ldflags=\"-w\")\n\n"

	if lastErr != nil {
		msg += fmt.Sprintf("\nLast error: %v\n", lastErr)
	}

	return fmt.Errorf("%s", msg)
}

// Close cleans up resources.
func (s *DiscoveryService) Close() error {
	if s.binaryScanner != nil {
		return s.binaryScanner.Close()
	}
	return nil
}
