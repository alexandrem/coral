package wireguard

import (
	"fmt"
	"net"

	"golang.zx2c4.com/wireguard/tun"
)

// Interface represents a WireGuard network interface.
type Interface struct {
	device tun.Device
	name   string
	mtu    int
}

// CreateTUN is implemented in platform-specific files:
// - interface_darwin.go for macOS
// - interface_linux.go for Linux
// This allows for platform-specific TUN device naming conventions.

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
// Note: This is a placeholder. Actual IP assignment depends on the platform
// and typically requires additional system calls or external commands.
func (i *Interface) AssignIP(ip net.IP, subnet *net.IPNet) error {
	if ip == nil {
		return fmt.Errorf("IP is nil")
	}
	if subnet == nil {
		return fmt.Errorf("subnet is nil")
	}

	// TODO: Implement platform-specific IP assignment
	// On Linux: use netlink (github.com/vishvananda/netlink)
	// On macOS: use ifconfig system call
	// On Windows: use netsh or Windows API
	//
	// For now, this is a no-op as wireguard-go handles most of the networking
	// and the actual IP assignment can be handled by the calling code using
	// platform-specific utilities.

	return nil
}

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
