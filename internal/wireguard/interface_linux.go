//go:build linux

package wireguard

import (
	"fmt"
	"net"

	"github.com/rs/zerolog"
	"golang.zx2c4.com/wireguard/tun"
)

// CreateTUN creates a new TUN device with the given name.
// On Linux, we can use custom names like "wg0", "wg1", etc.
func CreateTUN(name string, mtu int, logger zerolog.Logger) (*Interface, error) {
	if name == "" {
		name = "wg0"
	}

	if mtu <= 0 {
		mtu = 1420 // Default MTU for WireGuard (1500 - 80 overhead)
	}

	// Create TUN device
	tunDevice, err := tun.CreateTUN(name, mtu)
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
		logger: logger.With().Str("component", "wireguard.interface").Str("name", realName).Logger(),
	}, nil
}

// AssignIPPlatform assigns an IP address to the interface on Linux.
// TODO: Implement using netlink or ip command.
func (i *Interface) AssignIPPlatform(ip net.IP, subnet *net.IPNet) error {
	return fmt.Errorf("IP assignment not yet implemented for Linux (interface: %s, IP: %s)",
		i.name, ip.String())
}

// AddRoutesForPeerPlatform adds routes for a peer's AllowedIPs on Linux.
// TODO: Implement using netlink or ip route command.
func (i *Interface) AddRoutesForPeerPlatform(allowedIPs []string) error {
	return fmt.Errorf("route management not yet implemented for Linux (interface: %s)", i.name)
}

// DeleteRoutePlatform deletes a specific route for an IP on Linux.
// TODO: Implement using netlink or ip route command.
func (i *Interface) DeleteRoutePlatform(ip net.IP) error {
	return fmt.Errorf("route deletion not yet implemented for Linux (interface: %s)", i.name)
}
