package privilege

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsRoot(t *testing.T) {
	// Test returns a boolean (can't predict value in test environment)
	result := IsRoot()

	// Verify it matches expected behavior based on effective UID
	expected := os.Geteuid() == 0
	if result != expected {
		t.Errorf("IsRoot() = %v, expected %v (euid=%d)", result, expected, os.Geteuid())
	}
}

func TestIsRunningUnderSudo(t *testing.T) {
	// Save original SUDO_USER value
	originalSudoUser := os.Getenv("SUDO_USER")
	defer func() {
		if originalSudoUser != "" {
			os.Setenv("SUDO_USER", originalSudoUser)
		} else {
			os.Unsetenv("SUDO_USER")
		}
	}()

	tests := []struct {
		name     string
		sudoUser string
		setSudo  bool
		wantSudo bool
	}{
		{
			name:     "not running under sudo",
			setSudo:  false,
			wantSudo: false,
		},
		{
			name:     "running under sudo",
			sudoUser: "testuser",
			setSudo:  true,
			wantSudo: true,
		},
		{
			name:     "sudo user set to empty",
			sudoUser: "",
			setSudo:  true,
			wantSudo: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setSudo {
				os.Setenv("SUDO_USER", tt.sudoUser)
			} else {
				os.Unsetenv("SUDO_USER")
			}

			got := IsRunningUnderSudo()
			if got != tt.wantSudo {
				t.Errorf("IsRunningUnderSudo() = %v, want %v", got, tt.wantSudo)
			}
		})
	}
}

func TestDetectOriginalUser(t *testing.T) {
	// Save original environment
	originalSudoUser := os.Getenv("SUDO_USER")
	originalSudoUID := os.Getenv("SUDO_UID")
	originalSudoGID := os.Getenv("SUDO_GID")
	defer func() {
		restoreEnv("SUDO_USER", originalSudoUser)
		restoreEnv("SUDO_UID", originalSudoUID)
		restoreEnv("SUDO_GID", originalSudoGID)
	}()

	tests := []struct {
		name      string
		sudoUser  string
		sudoUID   string
		sudoGID   string
		wantErr   bool
		checkUser bool
	}{
		{
			name:      "not running under sudo",
			sudoUser:  "",
			wantErr:   false,
			checkUser: true,
		},
		{
			name:     "valid sudo environment",
			sudoUser: os.Getenv("USER"), // Use current user to ensure lookup succeeds
			sudoUID:  "1000",
			sudoGID:  "1000",
			wantErr:  false,
		},
		{
			name:     "sudo user without UID",
			sudoUser: "testuser",
			sudoUID:  "",
			sudoGID:  "1000",
			wantErr:  true,
		},
		{
			name:     "sudo user without GID",
			sudoUser: "testuser",
			sudoUID:  "1000",
			sudoGID:  "",
			wantErr:  true,
		},
		{
			name:     "invalid UID format",
			sudoUser: "testuser",
			sudoUID:  "invalid",
			sudoGID:  "1000",
			wantErr:  true,
		},
		{
			name:     "invalid GID format",
			sudoUser: "testuser",
			sudoUID:  "1000",
			sudoGID:  "invalid",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment
			if tt.sudoUser != "" {
				os.Setenv("SUDO_USER", tt.sudoUser)
			} else {
				os.Unsetenv("SUDO_USER")
			}
			if tt.sudoUID != "" {
				os.Setenv("SUDO_UID", tt.sudoUID)
			} else {
				os.Unsetenv("SUDO_UID")
			}
			if tt.sudoGID != "" {
				os.Setenv("SUDO_GID", tt.sudoGID)
			} else {
				os.Unsetenv("SUDO_GID")
			}

			userCtx, err := DetectOriginalUser()

			if (err != nil) != tt.wantErr {
				t.Errorf("DetectOriginalUser() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && userCtx == nil {
				t.Error("DetectOriginalUser() returned nil context without error")
				return
			}

			if tt.checkUser && userCtx != nil {
				// When not running under sudo, should return current user
				if userCtx.Username == "" {
					t.Error("DetectOriginalUser() returned empty username")
				}
				if userCtx.HomeDir == "" {
					t.Error("DetectOriginalUser() returned empty home directory")
				}
			}
		})
	}
}

func TestDropPrivileges(t *testing.T) {
	// This test can only run as root, skip otherwise
	if !IsRoot() {
		t.Skip("Skipping DropPrivileges test: requires root privileges")
	}

	tests := []struct {
		name    string
		uid     int
		gid     int
		wantErr bool
	}{
		{
			name:    "drop to nobody (if exists)",
			uid:     65534, // nobody user (common on Unix systems)
			gid:     65534, // nobody group
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Note: We can't actually test privilege dropping in tests
			// because it's irreversible and would break subsequent tests.
			// This test is here for documentation and can be run manually.
			t.Skip("Skipping actual privilege drop to avoid breaking test suite")

			err := DropPrivileges(tt.uid, tt.gid)
			if (err != nil) != tt.wantErr {
				t.Errorf("DropPrivileges() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDropPrivilegesNotRoot(t *testing.T) {
	// Skip if actually running as root
	if IsRoot() {
		t.Skip("Skipping non-root test: running as root")
	}

	// Attempting to drop privileges when not root should error
	err := DropPrivileges(1000, 1000)
	if err == nil {
		t.Error("DropPrivileges() should error when not running as root")
	}
}

func TestFixFileOwnership(t *testing.T) {
	// Create a temporary file
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")

	err := os.WriteFile(tmpFile, []byte("test"), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Test fixing ownership
	err = FixFileOwnership(tmpFile)

	if IsRoot() {
		// If running as root, should attempt to change ownership
		// We can't predict if it will succeed without knowing SUDO_USER
		// Just verify it doesn't panic
		t.Logf("FixFileOwnership() returned: %v", err)
	} else {
		// If not root, should be a no-op and return nil
		if err != nil {
			t.Errorf("FixFileOwnership() error = %v, want nil (should be no-op when not root)", err)
		}
	}
}

func TestFixFileOwnershipNonExistentFile(t *testing.T) {
	if !IsRoot() {
		t.Skip("Skipping root-only test")
	}

	// Save original environment
	originalSudoUser := os.Getenv("SUDO_USER")
	defer restoreEnv("SUDO_USER", originalSudoUser)

	// Set up sudo environment
	os.Setenv("SUDO_USER", os.Getenv("USER"))
	os.Setenv("SUDO_UID", "1000")
	os.Setenv("SUDO_GID", "1000")

	err := FixFileOwnership("/nonexistent/file.txt")
	if err == nil {
		t.Error("FixFileOwnership() should error for non-existent file when running as root")
	}
}

func TestDropToOriginalUser(t *testing.T) {
	if IsRoot() {
		t.Skip("Skipping test: would drop privileges irreversibly")
	}

	// When not root, should be a no-op
	err := DropToOriginalUser()
	if err != nil {
		t.Errorf("DropToOriginalUser() error = %v, want nil (should be no-op when not root)", err)
	}
}

func TestUserContext(t *testing.T) {
	// Test UserContext structure
	ctx := &UserContext{
		Username: "testuser",
		UID:      1000,
		GID:      1000,
		HomeDir:  "/home/testuser",
	}

	if ctx.Username != "testuser" {
		t.Errorf("UserContext.Username = %q, want %q", ctx.Username, "testuser")
	}
	if ctx.UID != 1000 {
		t.Errorf("UserContext.UID = %d, want %d", ctx.UID, 1000)
	}
	if ctx.GID != 1000 {
		t.Errorf("UserContext.GID = %d, want %d", ctx.GID, 1000)
	}
	if ctx.HomeDir != "/home/testuser" {
		t.Errorf("UserContext.HomeDir = %q, want %q", ctx.HomeDir, "/home/testuser")
	}
}

// Helper function to restore environment variable
func restoreEnv(key, value string) {
	if value != "" {
		os.Setenv(key, value)
	} else {
		os.Unsetenv(key)
	}
}
