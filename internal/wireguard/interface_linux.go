//go:build linux

package wireguard

import (
	"fmt"

	"golang.zx2c4.com/wireguard/tun"
)

// CreateTUN creates a new TUN device with the given name.
// On Linux, we can use custom names like "wg0", "wg1", etc.
func CreateTUN(name string, mtu int) (*Interface, error) {
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
	}, nil
}
