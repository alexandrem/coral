// Package tun_helper implements the internal _tun-helper command for
// privileged TUN device creation.
//
// This command is not intended to be called directly by users. It is invoked
// by the main coral process when TUN device creation requires elevated
// privileges.
package tun_helper

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/coral-io/coral/internal/wireguard"
)

// New creates the internal _tun-helper command.
// This command is hidden from help output and is only used internally for
// privilege separation.
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "_tun-helper <device-name> <mtu> <socket-path>",
		Short:  "Internal command for privileged TUN device creation",
		Hidden: true, // Don't show in help output.
		Args:   cobra.ExactArgs(3),
		RunE:   runTUNHelper,
		// Disable default behavior that might interfere with FD passing.
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	return cmd
}

// runTUNHelper implements the _tun-helper command logic.
func runTUNHelper(cmd *cobra.Command, args []string) error {
	deviceName := args[0]
	mtuStr := args[1]
	socketPath := args[2]

	// Parse MTU.
	mtu, err := strconv.Atoi(mtuStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[tun-helper] Error: Invalid MTU: %v\n", err)
		return fmt.Errorf("invalid MTU: %w", err)
	}

	// Log what we're doing (to stderr, since stdout might interfere with FD
	// passing).
	fmt.Fprintf(os.Stderr, "[tun-helper] Creating TUN device: %s (MTU: %d)\n", deviceName, mtu)

	// Create the TUN device.
	tunDevice, err := wireguard.CreateTUN(deviceName, mtu)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[tun-helper] Error: Failed to create TUN device: %v\n", err)
		return fmt.Errorf("failed to create TUN device: %w", err)
	}
	defer tunDevice.Close()

	// Get the file descriptor from the underlying TUN device.
	file := tunDevice.Device().File()
	if file == nil {
		fmt.Fprintf(os.Stderr, "[tun-helper] Error: TUN device file is nil\n")
		return fmt.Errorf("TUN device file is nil")
	}

	fd := int(file.Fd())
	fmt.Fprintf(os.Stderr, "[tun-helper] TUN device created successfully (FD: %d)\n", fd)

	// Send FD to parent via Unix socket.
	fmt.Fprintf(os.Stderr, "[tun-helper] Sending FD to parent via %s\n", socketPath)
	if err := wireguard.SendFDOverSocket(fd, socketPath); err != nil {
		fmt.Fprintf(os.Stderr, "[tun-helper] Error: Failed to send FD: %v\n", err)
		return fmt.Errorf("failed to send FD to parent: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[tun-helper] FD sent successfully, exiting\n")
	return nil
}
