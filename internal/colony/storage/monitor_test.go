package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCalculateSize(t *testing.T) {
	t.Run("empty path", func(t *testing.T) {
		size, err := CalculateSize("")
		require.NoError(t, err)
		assert.Equal(t, int64(0), size)
	})

	t.Run("nonexistent directory", func(t *testing.T) {
		size, err := CalculateSize("/nonexistent/path/to/nowhere")
		require.NoError(t, err)
		assert.Equal(t, int64(0), size)
	})

	t.Run("empty directory", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "coral-storage-test-*")
		require.NoError(t, err)
		defer func() { _ = os.RemoveAll(tmpDir) }() // TODO: errcheck

		size, err := CalculateSize(tmpDir)
		require.NoError(t, err)
		assert.Equal(t, int64(0), size)
	})

	t.Run("directory with single file", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "coral-storage-test-*")
		require.NoError(t, err)
		defer func() { _ = os.RemoveAll(tmpDir) }() // TODO: errcheck

		// Create a file with known content.
		testFile := filepath.Join(tmpDir, "test.txt")
		testContent := []byte("Hello, Coral!")
		err = os.WriteFile(testFile, testContent, 0644)
		require.NoError(t, err)

		size, err := CalculateSize(tmpDir)
		require.NoError(t, err)
		assert.Equal(t, int64(len(testContent)), size)
	})

	t.Run("directory with multiple files", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "coral-storage-test-*")
		require.NoError(t, err)
		defer func() { _ = os.RemoveAll(tmpDir) }() // TODO: errcheck

		// Create multiple files.
		file1 := filepath.Join(tmpDir, "file1.txt")
		file2 := filepath.Join(tmpDir, "file2.txt")
		file3 := filepath.Join(tmpDir, "file3.txt")

		content1 := []byte("File 1")
		content2 := []byte("File 2 is longer")
		content3 := []byte("File 3 is the longest of all")

		err = os.WriteFile(file1, content1, 0644)
		require.NoError(t, err)
		err = os.WriteFile(file2, content2, 0644)
		require.NoError(t, err)
		err = os.WriteFile(file3, content3, 0644)
		require.NoError(t, err)

		expectedSize := int64(len(content1) + len(content2) + len(content3))
		size, err := CalculateSize(tmpDir)
		require.NoError(t, err)
		assert.Equal(t, expectedSize, size)
	})

	t.Run("nested directory structure", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "coral-storage-test-*")
		require.NoError(t, err)
		defer func() { _ = os.RemoveAll(tmpDir) }() // TODO: errcheck

		// Create nested directories.
		subDir1 := filepath.Join(tmpDir, "subdir1")
		subDir2 := filepath.Join(tmpDir, "subdir2")
		subSubDir := filepath.Join(subDir1, "subsubdir")

		err = os.MkdirAll(subSubDir, 0755)
		require.NoError(t, err)
		err = os.MkdirAll(subDir2, 0755)
		require.NoError(t, err)

		// Create files in different directories.
		file1 := filepath.Join(tmpDir, "root.txt")
		file2 := filepath.Join(subDir1, "sub1.txt")
		file3 := filepath.Join(subDir2, "sub2.txt")
		file4 := filepath.Join(subSubDir, "subsub.txt")

		content1 := []byte("Root file")
		content2 := []byte("Sub1 file")
		content3 := []byte("Sub2 file")
		content4 := []byte("SubSub file")

		err = os.WriteFile(file1, content1, 0644)
		require.NoError(t, err)
		err = os.WriteFile(file2, content2, 0644)
		require.NoError(t, err)
		err = os.WriteFile(file3, content3, 0644)
		require.NoError(t, err)
		err = os.WriteFile(file4, content4, 0644)
		require.NoError(t, err)

		expectedSize := int64(len(content1) + len(content2) + len(content3) + len(content4))
		size, err := CalculateSize(tmpDir)
		require.NoError(t, err)
		assert.Equal(t, expectedSize, size)
	})

	t.Run("single file path", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "coral-storage-test-*")
		require.NoError(t, err)
		defer func() { _ = os.RemoveAll(tmpDir) }() // TODO: errcheck

		// Create a single file.
		testFile := filepath.Join(tmpDir, "single.txt")
		testContent := []byte("Single file content")
		err = os.WriteFile(testFile, testContent, 0644)
		require.NoError(t, err)

		// Calculate size for the file directly (not directory).
		size, err := CalculateSize(testFile)
		require.NoError(t, err)
		assert.Equal(t, int64(len(testContent)), size)
	})
}
