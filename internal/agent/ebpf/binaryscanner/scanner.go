// Package binaryscanner provides agentless binary scanning for uprobe discovery.
// It implements RFD 065 - Agentless Binary Scanning for Uprobe Discovery.
package binaryscanner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coral-mesh/coral/pkg/sdk/debug"
)

// AccessMethod defines how the agent accesses container binaries.
type AccessMethod string

const (
	// AccessMethodNsenter uses nsenter to enter container namespace.
	AccessMethodNsenter AccessMethod = "nsenter"
	// AccessMethodCRI uses container runtime interface.
	AccessMethodCRI AccessMethod = "cri"
	// AccessMethodDirect directly reads from /proc (works in shared PID namespace).
	AccessMethodDirect AccessMethod = "direct"
)

// Config contains configuration for binary scanner.
type Config struct {
	// AccessMethod specifies how to access container binaries.
	AccessMethod AccessMethod

	// CacheEnabled enables caching of parsed function metadata.
	CacheEnabled bool

	// CacheTTL is the time-to-live for cached entries.
	CacheTTL time.Duration

	// MaxCachedBinaries limits the number of cached binaries.
	MaxCachedBinaries int

	// TempDir is the directory for storing temporary binary copies.
	TempDir string

	// Logger for debug/error messages.
	Logger *slog.Logger
}

// DefaultConfig returns a default configuration.
func DefaultConfig() *Config {
	return &Config{
		AccessMethod:      AccessMethodDirect,
		CacheEnabled:      true,
		CacheTTL:          1 * time.Hour,
		MaxCachedBinaries: 100,
		TempDir:           "/tmp/coral-binaries",
		Logger:            slog.Default(),
	}
}

// cacheEntry stores cached function metadata for a binary.
type cacheEntry struct {
	provider  *debug.FunctionMetadataProvider
	hash      string
	timestamp time.Time
}

// Scanner scans process binaries to extract function metadata without SDK integration.
type Scanner struct {
	cfg   *Config
	mu    sync.RWMutex
	cache map[string]*cacheEntry // keyed by binary hash
}

// NewScanner creates a new binary scanner.
func NewScanner(cfg *Config) (*Scanner, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	// Ensure temp directory exists.
	if err := os.MkdirAll(cfg.TempDir, 0750); err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}

	return &Scanner{
		cfg:   cfg,
		cache: make(map[string]*cacheEntry),
	}, nil
}

// GetFunctionMetadata retrieves function metadata for a specific function in a process.
// It discovers the binary, parses DWARF, and caches results.
func (s *Scanner) GetFunctionMetadata(ctx context.Context, pid uint32, functionName string) (*FunctionMetadata, error) {
	s.cfg.Logger.Debug("Scanning binary for function metadata",
		"pid", pid,
		"function", functionName,
		"access_method", s.cfg.AccessMethod)

	// 1. Discover binary path for PID.
	binaryPath, err := s.discoverBinary(pid)
	if err != nil {
		return nil, fmt.Errorf("discover binary for PID %d: %w", pid, err)
	}

	s.cfg.Logger.Debug("Discovered binary path",
		"pid", pid,
		"binary_path", binaryPath)

	// 2. Copy binary from container namespace if needed.
	localPath, err := s.copyBinary(ctx, pid, binaryPath)
	if err != nil {
		return nil, fmt.Errorf("copy binary: %w", err)
	}

	// Clean up temporary file when done if it's different from original.
	if localPath != binaryPath {
		defer func() {
			if err := os.Remove(localPath); err != nil {
				s.cfg.Logger.Warn("Failed to remove temporary binary", "path", localPath, "error", err)
			}
		}()
	}

	s.cfg.Logger.Debug("Binary available for scanning",
		"local_path", localPath)

	// 3. Get or create metadata provider (with caching).
	provider, err := s.getOrCreateProvider(localPath, int(pid))
	if err != nil {
		return nil, fmt.Errorf("create metadata provider: %w", err)
	}

	// 4. Query function metadata.
	meta, err := provider.GetFunctionMetadata(functionName)
	if err != nil {
		return nil, fmt.Errorf("get function metadata: %w", err)
	}

	// 5. Convert to our FunctionMetadata type.
	return &FunctionMetadata{
		Name:       meta.Name,
		BinaryPath: binaryPath, // Use original path, not temporary
		Offset:     meta.Offset,
		PID:        uint32(meta.PID),
	}, nil
}

// ListFunctions returns all discoverable functions in a process binary.
func (s *Scanner) ListFunctions(ctx context.Context, pid uint32, pattern string, limit, offset int) ([]*BasicInfo, int, error) {
	// Discover and copy binary.
	binaryPath, err := s.discoverBinary(pid)
	if err != nil {
		return nil, 0, fmt.Errorf("discover binary: %w", err)
	}

	localPath, err := s.copyBinary(ctx, pid, binaryPath)
	if err != nil {
		return nil, 0, fmt.Errorf("copy binary: %w", err)
	}

	if localPath != binaryPath {
		defer func() {
			if err := os.Remove(localPath); err != nil {
				s.cfg.Logger.Warn("Failed to remove temporary binary", "path", localPath, "error", err)
			}
		}()
	}

	// Get provider.
	provider, err := s.getOrCreateProvider(localPath, int(pid))
	if err != nil {
		return nil, 0, fmt.Errorf("create metadata provider: %w", err)
	}

	// List functions.
	functions, total := provider.ListFunctions(pattern, limit, offset)

	// Convert to our BasicInfo type.
	result := make([]*BasicInfo, len(functions))
	for i, fn := range functions {
		result[i] = &BasicInfo{
			Name:   fn.Name,
			Offset: fn.Offset,
			File:   fn.File,
			Line:   fn.Line,
		}
	}

	return result, total, nil
}

// discoverBinary finds the binary path for a given PID by reading /proc/<pid>/exe.
func (s *Scanner) discoverBinary(pid uint32) (string, error) {
	// Read /proc/<pid>/exe symlink to get binary path.
	exePath := fmt.Sprintf("/proc/%d/exe", pid)

	target, err := os.Readlink(exePath)
	if err != nil {
		return "", fmt.Errorf("read /proc/%d/exe: %w", pid, err)
	}

	// Handle deleted binaries (common in containers).
	// /proc/<pid>/exe might show as "/path/to/binary (deleted)"
	target = strings.TrimSuffix(target, " (deleted)")

	s.cfg.Logger.Debug("Discovered binary from /proc",
		"pid", pid,
		"binary", target)

	return target, nil
}

// copyBinary copies the binary from the target process's mount namespace to a local path.
// This handles the case where the agent and target process are in different namespaces.
func (s *Scanner) copyBinary(ctx context.Context, pid uint32, binaryPath string) (string, error) {
	switch s.cfg.AccessMethod {
	case AccessMethodDirect:
		// In direct mode, we can read /proc/<pid>/exe directly.
		// This works in shared PID namespace (sidecar deployment).
		procPath := fmt.Sprintf("/proc/%d/exe", pid)

		// Verify the binary is accessible.
		if _, err := os.Stat(procPath); err != nil {
			return "", fmt.Errorf("binary not accessible at %s: %w", procPath, err)
		}

		return procPath, nil

	case AccessMethodNsenter:
		// Use nsenter to copy binary from container's mount namespace.
		return s.copyBinaryWithNsenter(ctx, pid, binaryPath)

	case AccessMethodCRI:
		return "", fmt.Errorf("CRI access method not yet implemented")

	default:
		return "", fmt.Errorf("unknown access method: %s", s.cfg.AccessMethod)
	}
}

// copyBinaryWithNsenter uses nsenter to enter the container's mount namespace and copy the binary.
func (s *Scanner) copyBinaryWithNsenter(ctx context.Context, pid uint32, binaryPath string) (string, error) {
	// Create temporary file for the binary copy.
	hash := fmt.Sprintf("%d-%s", pid, filepath.Base(binaryPath))
	tempPath := filepath.Join(s.cfg.TempDir, hash)

	s.cfg.Logger.Debug("Copying binary using nsenter",
		"pid", pid,
		"source", binaryPath,
		"destination", tempPath)

	// Use nsenter to enter the container's mount namespace and cat the binary.
	// nsenter -t <pid> -m cat /proc/self/exe > /tmp/binary
	// #nosec G204 -- PID is validated as uint32, nsenter args are controlled
	cmd := exec.CommandContext(ctx, "nsenter",
		"-t", fmt.Sprintf("%d", pid),
		"-m",
		"cat", "/proc/self/exe")

	// Create output file.
	// #nosec G304 -- tempPath is constructed from controlled TempDir and hash
	outFile, err := os.Create(tempPath)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer func() {
		if err := outFile.Close(); err != nil {
			s.cfg.Logger.Warn("Failed to close temp file", "path", tempPath, "error", err)
		}
	}()

	cmd.Stdout = outFile
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("nsenter failed: %w", err)
	}

	s.cfg.Logger.Debug("Successfully copied binary using nsenter",
		"temp_path", tempPath)

	return tempPath, nil
}

// getOrCreateProvider gets a cached metadata provider or creates a new one.
func (s *Scanner) getOrCreateProvider(binaryPath string, pid int) (*debug.FunctionMetadataProvider, error) {
	// Compute binary hash.
	hash, err := computeFileHash(binaryPath)
	if err != nil {
		return nil, fmt.Errorf("compute binary hash: %w", err)
	}

	// Check cache if enabled.
	if s.cfg.CacheEnabled {
		s.mu.RLock()
		if entry, ok := s.cache[hash]; ok {
			// Check if entry is still valid (not expired).
			if time.Since(entry.timestamp) < s.cfg.CacheTTL {
				s.mu.RUnlock()
				s.cfg.Logger.Debug("Using cached metadata provider",
					"hash", hash[:8],
					"binary", binaryPath)
				return entry.provider, nil
			}
			// Entry expired, will be replaced.
			s.cfg.Logger.Debug("Cache entry expired",
				"hash", hash[:8],
				"age", time.Since(entry.timestamp))
		}
		s.mu.RUnlock()
	}

	// Create new provider.
	s.cfg.Logger.Debug("Creating new metadata provider",
		"binary", binaryPath,
		"pid", pid)

	provider, err := debug.NewFunctionMetadataProviderForBinary(s.cfg.Logger, binaryPath, pid)
	if err != nil {
		return nil, fmt.Errorf("create function metadata provider: %w", err)
	}

	// Cache the provider if enabled.
	if s.cfg.CacheEnabled {
		s.mu.Lock()
		defer s.mu.Unlock()

		// Enforce cache size limit.
		if len(s.cache) >= s.cfg.MaxCachedBinaries {
			// Simple eviction: remove oldest entry.
			var oldestHash string
			var oldestTime time.Time
			for h, entry := range s.cache {
				if oldestHash == "" || entry.timestamp.Before(oldestTime) {
					oldestHash = h
					oldestTime = entry.timestamp
				}
			}
			if oldestHash != "" {
				s.cfg.Logger.Debug("Evicting oldest cache entry",
					"hash", oldestHash[:8])
				if err := s.cache[oldestHash].provider.Close(); err != nil {
					s.cfg.Logger.Warn("Failed to close evicted provider", "error", err)
				}
				delete(s.cache, oldestHash)
			}
		}

		s.cache[hash] = &cacheEntry{
			provider:  provider,
			hash:      hash,
			timestamp: time.Now(),
		}

		s.cfg.Logger.Debug("Cached metadata provider",
			"hash", hash[:8],
			"cache_size", len(s.cache))
	}

	return provider, nil
}

// Close cleans up all cached providers.
func (s *Scanner) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var errs []error
	for hash, entry := range s.cache {
		if err := entry.provider.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close provider for hash %s: %w", hash[:8], err))
		}
	}

	s.cache = make(map[string]*cacheEntry)

	if len(errs) > 0 {
		return fmt.Errorf("errors closing providers: %v", errs)
	}

	return nil
}

// computeFileHash computes the SHA256 hash of a file.
func computeFileHash(path string) (string, error) {
	// #nosec G304 -- path is either from TempDir (controlled) or from discoverBinary (validated)
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		// Best effort close for hash computation.
		_ = f.Close()
	}()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
