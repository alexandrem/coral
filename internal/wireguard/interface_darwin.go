//go:build darwin

package wireguard

import (
	"fmt"

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
