package wireguard

import (
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"

	"github.com/coral-mesh/coral/internal/privilege"
)

// HelperTimeout is the maximum time to wait for the helper subprocess to
// respond.
const HelperTimeout = 5 * time.Second

// generateSocketPath creates a unique socket path for IPC with the helper
// subprocess.
// nolint: unused
func generateSocketPath() (string, error) {
	// Generate random bytes for unique socket name.
	randBytes := make([]byte, 8)
	if _, err := rand.Read(randBytes); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}

	// Use hex encoding for filename-safe string.
	socketName := fmt.Sprintf("coral-tun-%x.sock", randBytes)
	socketPath := filepath.Join(os.TempDir(), socketName)

	return socketPath, nil
}

// validateDeviceName checks that the TUN device name is safe to use.
// nolint: unused
func validateDeviceName(name string) error {
	if name == "" {
		return fmt.Errorf("device name cannot be empty")
	}

	// Allow only alphanumeric characters, hyphens, and underscores.
	for _, c := range name {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') &&
			(c < '0' || c > '9') && c != '-' && c != '_' {
			return fmt.Errorf("invalid device name: %s (only alphanumeric, -, _ allowed)", name)
		}
	}

	return nil
}

// validateMTU checks that the MTU value is within a reasonable range.
// nolint: unused
func validateMTU(mtu int) error {
	if mtu < 68 || mtu > 65535 {
		return fmt.Errorf("invalid MTU: %d (must be between 68 and 65535)", mtu)
	}
	return nil
}

// createTUNWithHelper spawns a privileged subprocess to create a TUN device
// and returns the file descriptor. The subprocess must be running with elevated
// privileges (root or CAP_NET_ADMIN).
// nolint: unused // is it really used though?
func createTUNWithHelper(deviceName string, mtu int) (int, error) {
	// Validate inputs to prevent injection attacks.
	if err := validateDeviceName(deviceName); err != nil {
		return -1, err
	}
	if err := validateMTU(mtu); err != nil {
		return -1, err
	}

	// Check if helper subprocess is disabled.
	if os.Getenv("CORAL_SKIP_TUN_HELPER") != "" {
		return -1, fmt.Errorf("helper subprocess disabled via CORAL_SKIP_TUN_HELPER")
	}

	// Generate unique socket path.
	socketPath, err := generateSocketPath()
	if err != nil {
		return -1, fmt.Errorf("failed to generate socket path: %w", err)
	}

	// Ensure socket is cleaned up.
	defer func() { _ = os.Remove(socketPath) }() // TODO: errcheck

	// Create Unix listener for receiving FD from subprocess.
	listener, err := createUnixListener(socketPath)
	if err != nil {
		return -1, fmt.Errorf("failed to create Unix listener: %w", err)
	}
	defer func() { _ = listener.Close() }() // TODO: errcheck

	// Spawn the helper subprocess.
	if err := spawnHelperSubprocess(deviceName, mtu, socketPath); err != nil {
		return -1, fmt.Errorf("failed to spawn helper subprocess: %w", err)
	}

	// Wait for subprocess to connect and send FD.
	fd, err := receiveFDFromSocket(listener)
	if err != nil {
		return -1, fmt.Errorf("failed to receive FD from helper: %w", err)
	}

	return fd, nil
}

// createUnixListener creates a Unix domain socket listener at the specified
// path.
// nolint: unused
func createUnixListener(socketPath string) (*net.UnixListener, error) {
	// Remove existing socket if present.
	_ = os.Remove(socketPath) // TODO: errcheck

	addr, err := net.ResolveUnixAddr("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve Unix address: %w", err)
	}

	listener, err := net.ListenUnix("unix", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on Unix socket: %w", err)
	}

	// Set socket permissions to owner-only (0600).
	if err := os.Chmod(socketPath, 0600); err != nil {
		_ = listener.Close() // TODO: errcheck
		return nil, fmt.Errorf("failed to set socket permissions: %w", err)
	}

	return listener, nil
}

// spawnHelperSubprocess spawns the coral _tun-helper subprocess with the
// specified parameters.
// nolint: unused
func spawnHelperSubprocess(deviceName string, mtu int, socketPath string) error {
	// Get path to current binary.
	binaryPath := os.Getenv("CORAL_TUN_HELPER_PATH")
	if binaryPath == "" {
		// Default to current executable path.
		var err error
		binaryPath, err = os.Executable()
		if err != nil {
			return fmt.Errorf("failed to get executable path: %w", err)
		}
	}

	// Build command arguments.
	args := []string{
		"_tun-helper",
		deviceName,
		fmt.Sprintf("%d", mtu),
		socketPath,
	}

	// Check if we need sudo for privilege escalation.
	needsSudo := !privilege.IsRoot()

	var cmd *exec.Cmd
	if needsSudo {
		// Prepend sudo to command.
		sudoArgs := append([]string{binaryPath}, args...)
		cmd = exec.Command("sudo", sudoArgs...)
	} else {
		// Already running as root or with capabilities.
		cmd = exec.Command(binaryPath, args...)
	}

	// Start the subprocess.
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start subprocess: %w", err)
	}

	// Don't wait for subprocess - it will send FD and exit on its own.
	// The parent process will receive the FD via the socket.
	return nil
}

// receiveFDFromSocket waits for a connection on the Unix listener and receives
// a file descriptor via SCM_RIGHTS.
// nolint: unused
func receiveFDFromSocket(listener *net.UnixListener) (int, error) {
	// Set timeout for receiving FD.
	_ = listener.SetDeadline(time.Now().Add(HelperTimeout)) // TODO: errcheck

	// Accept connection from subprocess.
	conn, err := listener.AcceptUnix()
	if err != nil {
		return -1, fmt.Errorf("failed to accept connection: %w", err)
	}
	defer func() { _ = conn.Close() }() // TODO: errcheck

	// Receive file descriptor via SCM_RIGHTS.
	// We need to provide a buffer for receiving the FD.
	oob := make([]byte, unix.CmsgSpace(4)) // Space for one int (FD).
	buf := make([]byte, 1)                 // Dummy buffer for data.

	// Get underlying file from connection.
	connFile, err := conn.File()
	if err != nil {
		return -1, fmt.Errorf("failed to get connection file: %w", err)
	}
	defer func() { _ = connFile.Close() }() // TODO: errcheck

	// Receive message with FD.
	_, oobn, _, _, err := unix.Recvmsg(int(connFile.Fd()), buf, oob, 0)
	if err != nil {
		return -1, fmt.Errorf("failed to receive message: %w", err)
	}

	if oobn == 0 {
		return -1, fmt.Errorf("no ancillary data received")
	}

	// Parse control messages.
	scm, err := unix.ParseSocketControlMessage(oob[:oobn])
	if err != nil {
		return -1, fmt.Errorf("failed to parse socket control message: %w", err)
	}

	if len(scm) == 0 {
		return -1, fmt.Errorf("no socket control messages received")
	}

	// Parse Unix rights (file descriptors).
	fds, err := unix.ParseUnixRights(&scm[0])
	if err != nil {
		return -1, fmt.Errorf("failed to parse Unix rights: %w", err)
	}

	if len(fds) == 0 {
		return -1, fmt.Errorf("no file descriptors received")
	}

	return fds[0], nil
}

// SendFDOverSocket sends a file descriptor to the parent process via a Unix
// domain socket. This is called by the helper subprocess.
func SendFDOverSocket(fd int, socketPath string) error {
	// Connect to parent's Unix socket.
	addr, err := net.ResolveUnixAddr("unix", socketPath)
	if err != nil {
		return fmt.Errorf("failed to resolve Unix address: %w", err)
	}

	conn, err := net.DialUnix("unix", nil, addr)
	if err != nil {
		return fmt.Errorf("failed to connect to parent socket: %w", err)
	}
	defer func() { _ = conn.Close() }() // TODO: errcheck

	// Get underlying file from connection.
	connFile, err := conn.File()
	if err != nil {
		return fmt.Errorf("failed to get connection file: %w", err)
	}
	defer func() { _ = connFile.Close() }() // TODO: errcheck

	// Prepare ancillary data with FD.
	rights := unix.UnixRights(fd)
	buf := []byte{0} // Dummy data.

	// Send message with FD via SCM_RIGHTS.
	err = unix.Sendmsg(int(connFile.Fd()), buf, rights, nil, 0)
	if err != nil {
		return fmt.Errorf("failed to send file descriptor: %w", err)
	}

	return nil
}
