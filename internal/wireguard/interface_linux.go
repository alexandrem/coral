//go:build linux

package wireguard

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"

	"github.com/rs/zerolog"
	"golang.zx2c4.com/wireguard/tun"
)

// validInterfaceName validates that an interface name is safe for use in commands.
// Linux interface names must be alphanumeric with optional hyphens/underscores, max 15 chars.
var validInterfaceName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,15}$`)

// validateInterfaceName checks if an interface name is safe for use in shell commands.
func validateInterfaceName(name string) error {
	if !validInterfaceName.MatchString(name) {
		return fmt.Errorf("invalid interface name %q: must be alphanumeric with optional hyphens/underscores, max 15 chars", name)
	}
	return nil
}

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
		_ = tunDevice.Close() // TODO: errcheck
		return nil, fmt.Errorf("failed to get TUN device name: %w", err)
	}

	return &Interface{
		device: tunDevice,
		name:   realName,
		mtu:    mtu,
		logger: logger.With().Str("component", "wireguard.interface").Str("name", realName).Logger(),
	}, nil
}

// CreateTUNFromFD creates a new TUN device from an existing file descriptor.
func CreateTUNFromFD(name string, fd int, mtu int, logger zerolog.Logger) (*Interface, error) {
	if name == "" {
		name = "wg0"
	}

	if mtu <= 0 {
		mtu = 1420 // Default MTU for WireGuard (1500 - 80 overhead)
	}

	// Create an os.File from the file descriptor.
	file := os.NewFile(uintptr(fd), "")
	if file == nil {
		return nil, fmt.Errorf("failed to create os.File from file descriptor")
	}

	// Create TUN device from os.File.
	// Do NOT close the file here. The TUN device takes ownership of the file descriptor.
	tunDevice, err := tun.CreateTUNFromFile(file, mtu)
	if err != nil {
		return nil, fmt.Errorf("failed to create TUN device from FD: %w", err)
	}

	realName, err := tunDevice.Name()
	if err != nil {
		_ = tunDevice.Close() // TODO: errcheck
		return nil, fmt.Errorf("failed to get TUN device name: %w", err)
	}

	// Keep the file object alive until the tunDevice is returned,
	// preventing the garbage collector from finalizing it and closing the FD.
	runtime.KeepAlive(file)

	return &Interface{
		device: tunDevice,
		name:   realName,
		mtu:    mtu,
		logger: logger.With().Str("component", "wireguard.interface").Str("name", realName).Logger(),
	}, nil
}

// AssignIPPlatform assigns an IP address to the interface on Linux using ip command.
// If the interface already has an IP address, it will be replaced.
func (i *Interface) AssignIPPlatform(ip net.IP, subnet *net.IPNet) error {
	if i.name == "" {
		return fmt.Errorf("interface name is empty")
	}

	// Validate interface name before using in commands.
	if err := validateInterfaceName(i.name); err != nil {
		return err
	}

	// First, bring the interface up.
	// #nosec G204 -- interface name is validated by validateInterfaceName
	upCmd := exec.Command("ip", "link", "set", "dev", i.name, "up")
	if output, err := upCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to bring up interface %s: %w (output: %s)",
			i.name, err, string(output))
	}

	// Flush any existing IPv4 addresses on the interface.
	// This allows us to replace an existing IP (e.g., temporary IP with assigned IP).
	// We ignore errors here because the interface might not have an IP yet.
	// #nosec G204 -- interface name is validated by validateInterfaceName
	flushCmd := exec.Command("ip", "addr", "flush", "dev", i.name)
	_ = flushCmd.Run() // Ignore error - interface might not have an IP.

	// Calculate the prefix length from the subnet mask.
	ones, _ := subnet.Mask.Size()

	// Assign the IP address with /32 netmask to create only a host route.
	// This prevents subnet-wide routing that would conflict when multiple WireGuard
	// instances run on the same host. Peer-specific routes are added separately
	// via AddRoutesForPeer.
	//
	// On Linux we use: ip addr add <ip>/32 dev <interface>
	ipWithPrefix := fmt.Sprintf("%s/32", ip.String())
	args := []string{"addr", "add", ipWithPrefix, "dev", i.name}

	// #nosec G204 -- interface name is validated, IP is from net.IP type
	cmd := exec.Command("ip", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Ignore "File exists" errors (IP already assigned).
		if !strings.Contains(string(output), "File exists") {
			return fmt.Errorf("failed to assign IP %s to interface %s: %w (output: %s)",
				ip.String(), i.name, err, string(output))
		}
	}

	i.logger.Debug().
		Str("ip", ip.String()).
		Int("prefix", ones).
		Str("interface", i.name).
		Msg("Assigned IP address to interface")

	// Note: We do NOT add a blanket subnet route here.
	// WireGuard peers will be configured with specific AllowedIPs, and routes for those
	// specific IPs should be added when peers are configured (via AddPeer).
	// This avoids routing conflicts when multiple WireGuard instances run on the same host.

	return nil
}

// AddRoutesForPeerPlatform adds routes for a peer's AllowedIPs on Linux.
func (i *Interface) AddRoutesForPeerPlatform(allowedIPs []string) error {
	// Validate interface name before using in commands.
	if err := validateInterfaceName(i.name); err != nil {
		return err
	}

	i.logger.Debug().
		Strs("allowed_ips", allowedIPs).
		Msg("Adding routes for peer")

	for _, allowedIP := range allowedIPs {
		// Parse the CIDR to determine if it's a host (/32) or subnet.
		_, ipNet, err := net.ParseCIDR(allowedIP)
		if err != nil {
			// Try as plain IP and convert to /32.
			ip := net.ParseIP(allowedIP)
			if ip == nil {
				return fmt.Errorf("invalid allowed IP %q", allowedIP)
			}
			// Convert to /32 or /128.
			mask := net.CIDRMask(32, 32)
			if ip.To4() == nil {
				mask = net.CIDRMask(128, 128)
			}
			ipNet = &net.IPNet{IP: ip, Mask: mask}
		}

		// Add route for this specific IP/subnet through our interface.
		// On Linux: ip route add <cidr> dev <interface>
		// #nosec G204 -- interface name is validated, ipNet is from net.ParseCIDR
		routeCmd := exec.Command("ip", "route", "add", ipNet.String(), "dev", i.name)

		i.logger.Debug().
			Strs("command", routeCmd.Args).
			Msg("Running route command")

		output, err := routeCmd.CombinedOutput()

		i.logger.Debug().
			Str("output", string(output)).
			Err(err).
			Msg("Route command result")

		if err != nil {
			// Ignore "File exists" errors (route already present).
			if !strings.Contains(string(output), "File exists") {
				return fmt.Errorf("failed to add route for %s on %s: %w (output: %s)",
					allowedIP, i.name, err, string(output))
			}
		}
	}

	return nil
}

// DeleteRoutePlatform deletes a specific route for an IP on Linux.
func (i *Interface) DeleteRoutePlatform(ip net.IP) error {
	i.logger.Debug().
		Str("ip", ip.String()).
		Msg("Deleting route")

	// On Linux: ip route del <ip>
	// #nosec G204 -- IP is from net.IP type which is validated
	deleteCmd := exec.Command("ip", "route", "del", ip.String())
	output, err := deleteCmd.CombinedOutput()

	if err != nil {
		// Ignore "No such process" or "not found" errors (route doesn't exist).
		outputStr := string(output)
		if !strings.Contains(outputStr, "No such process") &&
			!strings.Contains(outputStr, "not found") {
			i.logger.Debug().
				Err(err).
				Str("output", outputStr).
				Msg("Failed to delete route")
			return fmt.Errorf("failed to delete route for %s: %w (output: %s)",
				ip.String(), err, outputStr)
		}
		i.logger.Debug().
			Str("ip", ip.String()).
			Msg("Route not found (already deleted or never existed)")
	} else {
		i.logger.Debug().
			Str("ip", ip.String()).
			Msg("Route deleted successfully")
	}

	return nil
}
