//go:build darwin

package wireguard

import (
	"fmt"
	"net"
	"os/exec"
	"strings"

	"github.com/rs/zerolog"
	"golang.zx2c4.com/wireguard/tun"
)

// CreateTUN creates a new TUN device with the given name.
// On macOS, interface names must be utun[0-9]*, so we use "utun" as the name.
func CreateTUN(name string, mtu int, logger zerolog.Logger) (*Interface, error) {
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

	// For point-to-point interfaces like WireGuard on macOS, we use:
	// ifconfig <interface> inet <local_ip> <dest_ip> netmask <mask>
	//
	// IMPORTANT: We use a /32 netmask (255.255.255.255) to create only a host route.
	// This prevents subnet-wide routing that would conflict when multiple WireGuard
	// instances run on the same host. Peer-specific routes are added separately
	// via AddRoutesForPeer.
	//
	// We still need a peer address for ifconfig's point-to-point syntax.
	// We use the network address, then delete the automatic route it creates.
	networkAddr := subnet.IP.String()
	args := []string{
		i.name,
		"inet",
		ip.String(),
		networkAddr, // destination IP (network address)
		"netmask",
		"255.255.255.255", // /32 - only create host route for this IP
	}

	cmd := exec.Command("ifconfig", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to assign IP %s to interface %s: %w (output: %s)",
			ip.String(), i.name, err, string(output))
	}

	// Delete the automatic route to the network address that ifconfig creates.
	// This route (e.g., "100.64.0.0 -> 100.64.0.1 via utun11") causes routing conflicts
	// when multiple WireGuard instances run on the same host.
	deleteNetRoute := exec.Command("route", "-n", "delete", "-host", networkAddr)
	_ = deleteNetRoute.Run() // Ignore errors - route might not exist

	// Note: We do NOT add a blanket subnet route here.
	// WireGuard peers will be configured with specific AllowedIPs, and routes for those
	// specific IPs should be added when peers are configured (via AddPeer).
	// This avoids routing conflicts when multiple WireGuard instances run on the same host.

	return nil
}

// AddRoutesForPeerPlatform adds routes for a peer's AllowedIPs on macOS.
func (i *Interface) AddRoutesForPeerPlatform(allowedIPs []string) error {
	i.logger.Debug().
		Strs("allowed_ips", allowedIPs).
		Msg("Adding routes for peer")

	for _, allowedIP := range allowedIPs {
		// Parse the CIDR to determine if it's a host (/32) or subnet
		_, ipNet, err := net.ParseCIDR(allowedIP)
		if err != nil {
			// Try as plain IP and convert to /32
			ip := net.ParseIP(allowedIP)
			if ip == nil {
				return fmt.Errorf("invalid allowed IP %q", allowedIP)
			}
			// Convert to /32 or /128
			mask := net.CIDRMask(32, 32)
			if ip.To4() == nil {
				mask = net.CIDRMask(128, 128)
			}
			ipNet = &net.IPNet{IP: ip, Mask: mask}
		}

		// Add route for this specific IP/subnet through our interface
		var routeCmd *exec.Cmd
		ones, _ := ipNet.Mask.Size()
		if ones == 32 || ones == 128 {
			// Host route: route add -host <ip> -interface <iface>
			routeCmd = exec.Command("route", "-n", "add", "-host", ipNet.IP.String(), "-interface", i.name)
		} else {
			// Network route: route add -net <cidr> -interface <iface>
			routeCmd = exec.Command("route", "-n", "add", "-net", ipNet.String(), "-interface", i.name)
		}

		i.logger.Debug().
			Strs("command", routeCmd.Args).
			Msg("Running route command")

		output, err := routeCmd.CombinedOutput()

		i.logger.Debug().
			Str("output", string(output)).
			Err(err).
			Msg("Route command result")

		if err != nil {
			// Ignore "File exists" errors (route already present)
			if !strings.Contains(string(output), "File exists") {
				return fmt.Errorf("failed to add route for %s on %s: %w (output: %s)",
					allowedIP, i.name, err, string(output))
			}
		}
	}

	return nil
}

// DeleteRoutePlatform deletes a specific route for an IP on macOS.
func (i *Interface) DeleteRoutePlatform(ip net.IP) error {
	i.logger.Debug().
		Str("ip", ip.String()).
		Msg("Deleting route")

	deleteCmd := exec.Command("route", "-n", "delete", "-host", ip.String())
	output, err := deleteCmd.CombinedOutput()

	if err != nil {
		// Ignore "not in table" errors (route doesn't exist).
		if !strings.Contains(string(output), "not in table") {
			i.logger.Debug().
				Err(err).
				Str("output", string(output)).
				Msg("Failed to delete route")
			return fmt.Errorf("failed to delete route for %s: %w (output: %s)",
				ip.String(), err, string(output))
		}
		i.logger.Debug().
			Str("ip", ip.String()).
			Msg("Route not in table (already deleted or never existed)")
	} else {
		i.logger.Debug().
			Str("ip", ip.String()).
			Msg("Route deleted successfully")
	}

	return nil
}
