package safe

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/rs/zerolog"
)

// DefaultMaxFileSize is the default maximum file size for safe file operations (1MB).
const DefaultMaxFileSize = 1 << 20

// CopyFileOptions configures the behavior of CopyFile.
type CopyFileOptions struct {
	// MaxSize is the maximum allowed file size in bytes. Zero means DefaultMaxFileSize.
	MaxSize int64
	// DestPerm is the permission mode for the destination file. Zero means 0600.
	DestPerm os.FileMode
	// AllowSymlinks allows copying from symlink sources. Default is false for security.
	AllowSymlinks bool
}

// CopyFile copies a file from src to dst with security validations.
// It rejects symlinks by default to prevent file inclusion attacks,
// validates file size, and ensures only regular files are copied.
func CopyFile(src, dst string, opts *CopyFileOptions) error {
	if opts == nil {
		opts = &CopyFileOptions{}
	}
	maxSize := opts.MaxSize
	if maxSize == 0 {
		maxSize = DefaultMaxFileSize
	}
	destPerm := opts.DestPerm
	if destPerm == 0 {
		destPerm = 0o600
	}

	// Clean and validate the source path.
	cleanSrc := filepath.Clean(src)

	// Check file info without following symlinks.
	info, err := os.Lstat(cleanSrc)
	if err != nil {
		return err
	}

	// Reject symlinks unless explicitly allowed.
	if info.Mode()&os.ModeSymlink != 0 && !opts.AllowSymlinks {
		return fmt.Errorf("source file %q is a symlink, which is not allowed for security reasons", src)
	}

	// If it's a symlink and allowed, follow it to get the real file info.
	if info.Mode()&os.ModeSymlink != 0 {
		info, err = os.Stat(cleanSrc)
		if err != nil {
			return err
		}
	}

	// Reject non-regular files.
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source %q is not a regular file", src)
	}

	// Check file size to prevent resource exhaustion.
	if info.Size() > maxSize {
		return fmt.Errorf("file exceeds maximum allowed size of %d bytes", maxSize)
	}

	srcFile, err := os.Open(cleanSrc)
	if err != nil {
		return err
	}
	defer func(srcFile *os.File) {
		_ = srcFile.Close()
	}(srcFile)

	// #nosec G304 - we have validated the file prior to this.
	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, destPerm)
	if err != nil {
		return err
	}
	defer func(dstFile *os.File) {
		_ = dstFile.Close()
	}(dstFile)

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// ReadFile reads a file with security validations.
// It rejects symlinks by default to prevent file inclusion attacks,
// validates file size, and ensures only regular files are read.
func ReadFile(path string, opts *CopyFileOptions) ([]byte, error) {
	if opts == nil {
		opts = &CopyFileOptions{}
	}
	maxSize := opts.MaxSize
	if maxSize == 0 {
		maxSize = DefaultMaxFileSize
	}

	// Clean and validate the path.
	cleanPath := filepath.Clean(path)

	// Check file info without following symlinks.
	info, err := os.Lstat(cleanPath)
	if err != nil {
		return nil, err
	}

	// Reject symlinks unless explicitly allowed.
	if info.Mode()&os.ModeSymlink != 0 && !opts.AllowSymlinks {
		return nil, fmt.Errorf("file %q is a symlink, which is not allowed for security reasons", path)
	}

	// If it's a symlink and allowed, follow it to get the real file info.
	if info.Mode()&os.ModeSymlink != 0 {
		info, err = os.Stat(cleanPath)
		if err != nil {
			return nil, err
		}
	}

	// Reject non-regular files.
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("path %q is not a regular file", path)
	}

	// Check file size to prevent resource exhaustion.
	if info.Size() > maxSize {
		return nil, fmt.Errorf("file exceeds maximum allowed size of %d bytes", maxSize)
	}

	return os.ReadFile(cleanPath)
}

// Close closes gracefully a Closer interface, handling and logging the error.
func Close(c io.Closer, logger zerolog.Logger, msg string) {
	if err := c.Close(); err != nil {
		logger.Error().Err(err).Msg(msg)
	}
}

// RemoveFile removes gracefully a file, handling and logging the error.
func RemoveFile(f *os.File, logger zerolog.Logger) {
	if f == nil {
		return
	}
	if err := os.Remove(f.Name()); err != nil {
		logger.Error().Err(err).Msg("failed to remove file")
	}
}
