//go:build darwin

package wireguard

import (
	"fmt"
	"net"
	"os/exec"

	"golang.zx2c4.com/wireguard/tun"
)

// CreateTUN creates a new TUN device with the given name.
// On macOS, interface names must be utun[0-9]*, so we use "utun" as the name.
func CreateTUN(name string, mtu int) (*Interface, error) {
	// On macOS, we must use "utun" as the name prefix
	// The system will automatically assign utun0, utun1, etc.
	tunName := "utun"

	if mtu <= 0 {
		mtu = 1420 // Default MTU for WireGuard (1500 - 80 overhead)
	}

	// Create TUN device
	tunDevice, err := tun.CreateTUN(tunName, mtu)
	if err != nil {
		return nil, fmt.Errorf("failed to create TUN device: %w", err)
	}

	realName, err := tunDevice.Name()
	if err != nil {
		tunDevice.Close()
		return nil, fmt.Errorf("failed to get TUN device name: %w", err)
	}

	return &Interface{
		device: tunDevice,
		name:   realName,
		mtu:    mtu,
	}, nil
}

// AssignIPPlatform assigns an IP address to the interface on macOS using ifconfig.
// If the interface already has an IP address, it will be replaced.
func (i *Interface) AssignIPPlatform(ip net.IP, subnet *net.IPNet) error {
	if i.name == "" {
		return fmt.Errorf("interface name is empty")
	}

	// First, try to delete any existing IPv4 address on the interface.
	// This allows us to replace an existing IP (e.g., temporary IP with assigned IP).
	// We ignore errors here because the interface might not have an IP yet.
	deleteCmd := exec.Command("ifconfig", i.name, "inet", "delete")
	_ = deleteCmd.Run() // Ignore error - interface might not have an IP

	// Convert subnet mask to netmask format
	mask := fmt.Sprintf("%d.%d.%d.%d", subnet.Mask[0], subnet.Mask[1], subnet.Mask[2], subnet.Mask[3])

	// For point-to-point interfaces like WireGuard on macOS, we use:
	// ifconfig <interface> inet <local_ip> <dest_ip> netmask <mask>
	// We set both local and dest to the same IP for simplicity.
	args := []string{
		i.name,
		"inet",
		ip.String(),
		ip.String(), // destination IP (same as local for p2p)
		"netmask",
		mask,
	}

	cmd := exec.Command("ifconfig", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to assign IP %s to interface %s: %w (output: %s)",
			ip.String(), i.name, err, string(output))
	}

	return nil
}
