//go:build linux

// Package profiler implements continuous CPU profiling for the agent.
package profiler

import (
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/coral-mesh/coral/internal/sys/proc"
)

// ExtractBuildID extracts the NT_GNU_BUILD_ID from an ELF binary.
// Falls back to SHA-256 hash of the binary if build ID is not present.
func ExtractBuildID(binaryPath string) (string, error) {
	f, err := elf.Open(binaryPath)
	if err != nil {
		return "", fmt.Errorf("failed to open ELF file: %w", err)
	}
	defer func() { _ = f.Close() }()

	// Try to find .note.gnu.build-id section.
	section := f.Section(".note.gnu.build-id")
	if section != nil {
		data, err := section.Data()
		if err == nil && len(data) >= 16 {
			// Parse ELF note structure.
			// Format: namesz(4) + descsz(4) + type(4) + name(namesz) + desc(descsz)
			// The build ID typically starts at offset 16 (after header + "GNU\0" name)
			if len(data) >= 36 {
				buildID := data[16:36] // 20 bytes for SHA-1
				return hex.EncodeToString(buildID), nil
			}
		}
	}

	// Fallback: compute SHA-256 hash of the binary.
	// #nosec G304 - binaryPath is a trusted path to a system binary being profiled.
	file, err := os.Open(binaryPath)
	if err != nil {
		return "", fmt.Errorf("failed to open binary for hashing: %w", err)
	}
	defer func() { _ = file.Close() }()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("failed to hash binary: %w", err)
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// ExtractBuildIDFromPID extracts the build ID from a running process.
func ExtractBuildIDFromPID(pid int) (string, error) {
	binaryPath, err := proc.GetBinaryPath(pid)
	if err != nil {
		return "", err
	}
	return ExtractBuildID(binaryPath)
}
