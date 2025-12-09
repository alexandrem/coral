package wireguard

import (
	"fmt"
	"net"

	"github.com/rs/zerolog"
	"golang.zx2c4.com/wireguard/tun"
)

// Interface represents a WireGuard network interface.
type Interface struct {
	device tun.Device
	name   string
	mtu    int
	logger zerolog.Logger
}

// CreateTUN is implemented in platform-specific files:
// - interface_darwin.go for macOS
// - interface_linux.go for Linux
// This allows for platform-specific TUN device naming conventions.

// CreateTUNFromFD is implemented in platform-specific files, similar to CreateTUN.
// This allows for platform-specific logic for creating a TUN device from a
// file descriptor.

// Name returns the interface name.
func (i *Interface) Name() string {
	return i.name
}

// MTU returns the interface MTU.
func (i *Interface) MTU() int {
	return i.mtu
}

// Device returns the underlying TUN device.
func (i *Interface) Device() tun.Device {
	return i.device
}

// Close closes the interface.
func (i *Interface) Close() error {
	if i.device != nil {
		return i.device.Close()
	}
	return nil
}

// AssignIP assigns an IP address to the interface using the platform-specific method.
func (i *Interface) AssignIP(ip net.IP, subnet *net.IPNet) error {
	if ip == nil {
		return fmt.Errorf("IP is nil")
	}
	if subnet == nil {
		return fmt.Errorf("subnet is nil")
	}

	// Call platform-specific implementation
	return i.AssignIPPlatform(ip, subnet)
}

// AssignIPPlatform is implemented in platform-specific files:
// - interface_darwin.go for macOS (using ifconfig)
// - interface_linux.go for Linux (using netlink or ip command)
// This allows for platform-specific IP assignment methods.

// SetMTU updates the interface MTU.
func (i *Interface) SetMTU(mtu int) error {
	if mtu <= 0 {
		return fmt.Errorf("invalid MTU: %d", mtu)
	}

	// Note: TUN device MTU is set at creation time.
	// Changing it requires recreating the device or using platform-specific APIs.
	// For simplicity, we store the requested MTU but don't change the device.
	i.mtu = mtu

	return nil
}

// AddRoutesForPeer adds routes for a peer's AllowedIPs.
// This is necessary for userspace WireGuard since it doesn't automatically manage routes.
func (i *Interface) AddRoutesForPeer(allowedIPs []string) error {
	if i.name == "" {
		return fmt.Errorf("interface name is empty")
	}

	// Call platform-specific implementation
	return i.AddRoutesForPeerPlatform(allowedIPs)
}

// AddRoutesForPeerPlatform is implemented in platform-specific files:
// - interface_darwin.go for macOS (using route command)
// - interface_linux.go for Linux (using ip route or netlink)

// DeleteRoute deletes a specific route for an IP through the interface.
// This is useful for clearing cached routes when IP addresses change.
func (i *Interface) DeleteRoute(ip net.IP) error {
	if ip == nil {
		return fmt.Errorf("IP is nil")
	}

	// Call platform-specific implementation.
	return i.DeleteRoutePlatform(ip)
}

// DeleteRoutePlatform is implemented in platform-specific files:
// - interface_darwin.go for macOS (using route command)
// - interface_linux.go for Linux (using ip route or netlink)
