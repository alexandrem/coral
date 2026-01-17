package safe

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyFile(t *testing.T) {
	t.Run("copies regular file", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "source.txt")
		dst := filepath.Join(tmpDir, "dest.txt")
		content := []byte("test content")

		if err := os.WriteFile(src, content, 0o644); err != nil {
			t.Fatal(err)
		}

		if err := CopyFile(src, dst, nil); err != nil {
			t.Fatalf("CopyFile failed: %v", err)
		}

		got, err := os.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}

		if string(got) != string(content) {
			t.Errorf("got %q, want %q", got, content)
		}
	})

	t.Run("rejects symlink by default", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "source.txt")
		link := filepath.Join(tmpDir, "link.txt")
		dst := filepath.Join(tmpDir, "dest.txt")

		if err := os.WriteFile(src, []byte("test"), 0o644); err != nil {
			t.Fatal(err)
		}

		if err := os.Symlink(src, link); err != nil {
			t.Fatal(err)
		}

		err := CopyFile(link, dst, nil)
		if err == nil {
			t.Fatal("expected error for symlink, got nil")
		}
	})

	t.Run("allows symlink when enabled", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "source.txt")
		link := filepath.Join(tmpDir, "link.txt")
		dst := filepath.Join(tmpDir, "dest.txt")
		content := []byte("test content")

		if err := os.WriteFile(src, content, 0o644); err != nil {
			t.Fatal(err)
		}

		if err := os.Symlink(src, link); err != nil {
			t.Fatal(err)
		}

		if err := CopyFile(link, dst, &CopyFileOptions{AllowSymlinks: true}); err != nil {
			t.Fatalf("CopyFile failed: %v", err)
		}

		got, err := os.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}

		if string(got) != string(content) {
			t.Errorf("got %q, want %q", got, content)
		}
	})

	t.Run("rejects file exceeding max size", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "source.txt")
		dst := filepath.Join(tmpDir, "dest.txt")

		// Create a file larger than the custom max size.
		content := make([]byte, 1024)
		if err := os.WriteFile(src, content, 0o644); err != nil {
			t.Fatal(err)
		}

		err := CopyFile(src, dst, &CopyFileOptions{MaxSize: 512})
		if err == nil {
			t.Fatal("expected error for oversized file, got nil")
		}
	})

	t.Run("rejects directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		subDir := filepath.Join(tmpDir, "subdir")
		dst := filepath.Join(tmpDir, "dest.txt")

		if err := os.Mkdir(subDir, 0o755); err != nil {
			t.Fatal(err)
		}

		err := CopyFile(subDir, dst, nil)
		if err == nil {
			t.Fatal("expected error for directory, got nil")
		}
	})

	t.Run("sets correct destination permissions", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "source.txt")
		dst := filepath.Join(tmpDir, "dest.txt")

		if err := os.WriteFile(src, []byte("test"), 0o644); err != nil {
			t.Fatal(err)
		}

		if err := CopyFile(src, dst, &CopyFileOptions{DestPerm: 0o600}); err != nil {
			t.Fatal(err)
		}

		info, err := os.Stat(dst)
		if err != nil {
			t.Fatal(err)
		}

		// Check that the file has the expected permissions.
		perm := info.Mode().Perm()
		if perm != 0o600 {
			t.Errorf("got permissions %o, want %o", perm, 0o600)
		}
	})
}

func TestReadFile(t *testing.T) {
	t.Run("reads regular file", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "source.txt")
		content := []byte("test content")

		if err := os.WriteFile(src, content, 0o644); err != nil {
			t.Fatal(err)
		}

		got, err := ReadFile(src, nil)
		if err != nil {
			t.Fatalf("ReadFile failed: %v", err)
		}

		if string(got) != string(content) {
			t.Errorf("got %q, want %q", got, content)
		}
	})

	t.Run("rejects symlink by default", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "source.txt")
		link := filepath.Join(tmpDir, "link.txt")

		if err := os.WriteFile(src, []byte("test"), 0o644); err != nil {
			t.Fatal(err)
		}

		if err := os.Symlink(src, link); err != nil {
			t.Fatal(err)
		}

		_, err := ReadFile(link, nil)
		if err == nil {
			t.Fatal("expected error for symlink, got nil")
		}
	})

	t.Run("rejects file exceeding max size", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "source.txt")

		content := make([]byte, 1024)
		if err := os.WriteFile(src, content, 0o644); err != nil {
			t.Fatal(err)
		}

		_, err := ReadFile(src, &CopyFileOptions{MaxSize: 512})
		if err == nil {
			t.Fatal("expected error for oversized file, got nil")
		}
	})
}
