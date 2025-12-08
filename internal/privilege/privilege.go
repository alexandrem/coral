// Package privilege provides utilities for handling privilege separation and user
// context detection when running with elevated privileges.
package privilege

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
	"syscall"
)

// UserContext represents the identity of the original user when running under
// privilege escalation.
type UserContext struct {
	Username string
	UID      int
	GID      int
	HomeDir  string
}

// DetectOriginalUser extracts user identity, accounting for sudo execution.
// When running under sudo, it returns the original user's context from
// SUDO_USER/SUDO_UID/SUDO_GID environment variables. Otherwise, returns the
// current user's context.
func DetectOriginalUser() (*UserContext, error) {
	// Check if running under sudo.
	sudoUser := os.Getenv("SUDO_USER")
	if sudoUser != "" {
		// We're running under sudo - get original user info.
		uidStr := os.Getenv("SUDO_UID")
		gidStr := os.Getenv("SUDO_GID")

		if uidStr == "" || gidStr == "" {
			return nil, fmt.Errorf("SUDO_USER set but SUDO_UID or SUDO_GID missing")
		}

		uid, err := strconv.Atoi(uidStr)
		if err != nil {
			return nil, fmt.Errorf("invalid SUDO_UID: %w", err)
		}

		gid, err := strconv.Atoi(gidStr)
		if err != nil {
			return nil, fmt.Errorf("invalid SUDO_GID: %w", err)
		}

		// Get home directory for the sudo user.
		u, err := user.Lookup(sudoUser)
		if err != nil {
			return nil, fmt.Errorf("failed to lookup user %s: %w", sudoUser, err)
		}

		return &UserContext{
			Username: sudoUser,
			UID:      uid,
			GID:      gid,
			HomeDir:  u.HomeDir,
		}, nil
	}

	// Not running under sudo - use current user.
	return getCurrentUser()
}

// getCurrentUser returns the context for the current user.
func getCurrentUser() (*UserContext, error) {
	u, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("failed to get current user: %w", err)
	}

	uid := os.Getuid()
	gid := os.Getgid()

	return &UserContext{
		Username: u.Username,
		UID:      uid,
		GID:      gid,
		HomeDir:  u.HomeDir,
	}, nil
}

// IsRoot checks if the current process is running with root privileges (euid
// == 0).
func IsRoot() bool {
	return os.Geteuid() == 0
}

// IsRunningUnderSudo checks if the process is running under sudo by checking
// for the SUDO_USER environment variable.
func IsRunningUnderSudo() bool {
	return os.Getenv("SUDO_USER") != ""
}

// DropPrivileges permanently drops root privileges to the specified UID/GID.
// This is irreversible and should only be called when root privileges are no
// longer needed.
func DropPrivileges(uid, gid int) error {
	if !IsRoot() {
		return fmt.Errorf("not running as root, cannot drop privileges")
	}

	// Set GID first (must be done before dropping UID).
	if err := syscall.Setgid(gid); err != nil {
		return fmt.Errorf("failed to set GID to %d: %w", gid, err)
	}

	// Set UID (irreversible).
	if err := syscall.Setuid(uid); err != nil {
		return fmt.Errorf("failed to set UID to %d: %w", uid, err)
	}

	// Verify we actually dropped privileges.
	if os.Geteuid() == 0 {
		return fmt.Errorf("failed to drop privileges: still running as root")
	}

	return nil
}

// FixFileOwnership changes the ownership of a file to the original user when
// running under sudo. If not running as root or under sudo, this is a no-op.
func FixFileOwnership(path string) error {
	if !IsRoot() {
		// Not running as root, no need to fix ownership.
		return nil
	}

	userCtx, err := DetectOriginalUser()
	if err != nil {
		return fmt.Errorf("failed to detect original user: %w", err)
	}

	// Change ownership to the original user.
	if err := os.Chown(path, userCtx.UID, userCtx.GID); err != nil {
		return fmt.Errorf("failed to chown %s to %d:%d: %w", path, userCtx.UID, userCtx.GID, err)
	}

	return nil
}

// DropToOriginalUser attempts to drop root privileges to the original user
// who invoked sudo. If not running via sudo, it does nothing.
// This should be called after all privileged operations are complete.
func DropToOriginalUser() error {
	if !IsRoot() {
		// Not running as root, nothing to do.
		return nil
	}

	userCtx, err := DetectOriginalUser()
	if err != nil {
		// If we can't determine the original user, continue as root with a warning.
		// This handles the case where someone runs directly as root (not via sudo).
		return nil
	}

	if err := DropPrivileges(userCtx.UID, userCtx.GID); err != nil {
		return fmt.Errorf("failed to drop privileges to user %s (uid:%d, gid:%d): %w", userCtx.Username, userCtx.UID, userCtx.GID, err)
	}

	return nil
}
