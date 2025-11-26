// Package storage provides colony data storage and monitoring.
package storage

import (
	"os"
	"path/filepath"
)

// CalculateSize returns the total size in bytes of all files in the given directory.
// Returns 0 if the directory doesn't exist or is empty.
func CalculateSize(path string) (int64, error) {
	if path == "" {
		return 0, nil
	}

	// Check if directory exists.
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	// If it's a file, return its size.
	if !info.IsDir() {
		return info.Size(), nil
	}

	// Walk the directory tree and sum file sizes.
	var totalSize int64
	err = filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			// Skip files we can't access.
			return nil
		}
		if !info.IsDir() {
			totalSize += info.Size()
		}
		return nil
	})

	if err != nil {
		return 0, err
	}

	return totalSize, nil
}
