// Package tunhelper implements the internal _tun-helper command for
// privileged TUN device creation.
//
// This command is not intended to be called directly by users. It is invoked
// by the main coral process when TUN device creation requires elevated
// privileges.
package tunhelper

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/coral-io/coral/internal/logging"
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

	// Create a logger for the TUN helper.
	logger := logging.New(logging.Config{
		Level:  "info",
		Pretty: false,
		Output: os.Stderr,
	})

	// Log what we're doing (to stderr, since stdout might interfere with FD
	// passing).
	logger.Info().
		Str("device", deviceName).
		Int("mtu", mtu).
		Msg("Creating TUN device")

	// Create the TUN device.
	tunDevice, err := wireguard.CreateTUN(deviceName, mtu, logger)
	if err != nil {
		logger.Error().Err(err).Msg("Failed to create TUN device")
		return fmt.Errorf("failed to create TUN device: %w", err)
	}
	defer func() { _ = tunDevice.Close() }() // TODO: errcheck

	// Get the file descriptor from the underlying TUN device.
	file := tunDevice.Device().File()
	if file == nil {
		logger.Error().Msg("TUN device file is nil")
		return fmt.Errorf("TUN device file is nil")
	}

	fd := int(file.Fd())
	logger.Info().Int("fd", fd).Msg("TUN device created successfully")

	// Send FD to parent via Unix socket.
	logger.Info().Str("socket", socketPath).Msg("Sending FD to parent")
	if err := wireguard.SendFDOverSocket(fd, socketPath); err != nil {
		logger.Error().Err(err).Msg("Failed to send FD")
		return fmt.Errorf("failed to send FD to parent: %w", err)
	}

	logger.Info().Msg("FD sent successfully, exiting")
	return nil
}
