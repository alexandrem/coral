// Package wireguard provides colony-specific WireGuard device setup and orchestration.
// This package handles WireGuard device creation, network configuration, persistent IP allocation,
// and endpoint management for the colony mesh network. It builds on the low-level primitives
// in internal/wireguard to provide colony-specific initialization and configuration logic.
package wireguard

import (
	"fmt"
	"net"

	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/constants"
	"github.com/coral-mesh/coral/internal/logging"
	"github.com/coral-mesh/coral/internal/wireguard"
)

// CreateDevice creates a WireGuard device but doesn't start it yet.
// This allows the persistent IP allocator to be configured before the device starts.
func CreateDevice(cfg *config.ResolvedConfig, logger logging.Logger) (*wireguard.Device, error) {
	logger.Info().
		Str("mesh_ipv4", cfg.WireGuard.MeshIPv4).
		Int("port", cfg.WireGuard.Port).
		Msg("Creating WireGuard device")

	wgDevice, err := wireguard.NewDevice(&cfg.WireGuard, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create WireGuard device: %w", err)
	}

	return wgDevice, nil
}

// StartDevice starts the WireGuard device and assigns the mesh IP.
func StartDevice(wgDevice *wireguard.Device, cfg *config.ResolvedConfig, logger logging.Logger) error {
	if err := wgDevice.Start(); err != nil {
		return fmt.Errorf("failed to start WireGuard device: %w", err)
	}

	logger.Info().
		Str("interface", wgDevice.InterfaceName()).
		Str("mesh_ip", cfg.WireGuard.MeshIPv4).
		Msg("WireGuard device started successfully")

	// Assign mesh IP to the interface
	if cfg.WireGuard.MeshIPv4 != "" && cfg.WireGuard.MeshNetworkIPv4 != "" {
		meshIP := net.ParseIP(cfg.WireGuard.MeshIPv4)
		if meshIP == nil {
			return fmt.Errorf("invalid mesh IPv4 address: %s", cfg.WireGuard.MeshIPv4)
		}

		_, meshNet, err := net.ParseCIDR(cfg.WireGuard.MeshNetworkIPv4)
		if err != nil {
			return fmt.Errorf("invalid mesh network CIDR: %w", err)
		}

		logger.Info().
			Str("interface", wgDevice.InterfaceName()).
			Str("ip", meshIP.String()).
			Str("subnet", meshNet.String()).
			Msg("Assigning IP address to WireGuard interface")

		iface := wgDevice.Interface()
		if iface == nil {
			return fmt.Errorf("WireGuard device has no interface")
		}

		if err := iface.AssignIP(meshIP, meshNet); err != nil {
			return fmt.Errorf("failed to assign IP to interface: %w", err)
		}

		logger.Info().
			Str("interface", wgDevice.InterfaceName()).
			Str("ip", meshIP.String()).
			Msg("Successfully assigned IP to WireGuard interface")
	}

	// Save the assigned interface name to config for future reference
	interfaceName := wgDevice.InterfaceName()
	if interfaceName != "" {
		loader, err := config.NewLoader()
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to create config loader to save interface name")
		} else {
			colonyConfig, err := loader.LoadColonyConfig(cfg.ColonyID)
			if err != nil {
				logger.Warn().Err(err).Msg("Failed to load colony config to save interface name")
			} else {
				colonyConfig.WireGuard.InterfaceName = interfaceName
				if err := loader.SaveColonyConfig(colonyConfig); err != nil {
					logger.Warn().Err(err).Msg("Failed to save interface name to config")
				} else {
					logger.Debug().
						Str("interface", interfaceName).
						Msg("Saved interface name to colony config")
				}
			}
		}
	}

	return nil
}

// InitializePersistentIPAllocator creates and injects a persistent IP allocator (RFD 019).
func InitializePersistentIPAllocator(wgDevice *wireguard.Device, db *database.Database, logger logging.Logger) error {
	// Get the mesh network subnet from WireGuard config.
	cfg := wgDevice.Config()
	if cfg.MeshNetworkIPv4 == "" {
		cfg.MeshNetworkIPv4 = constants.DefaultColonyMeshIPv4Subnet
	}

	_, subnet, err := net.ParseCIDR(cfg.MeshNetworkIPv4)
	if err != nil {
		return fmt.Errorf("invalid mesh network CIDR: %w", err)
	}

	// Create database adapter for IP allocation store.
	store := database.NewIPAllocationStore(db)

	// Create persistent allocator with database store.
	allocator, err := wireguard.NewPersistentIPAllocator(subnet, store)
	if err != nil {
		return fmt.Errorf("failed to create persistent allocator: %w", err)
	}

	// Inject the persistent allocator into the WireGuard device.
	if err := wgDevice.SetAllocator(allocator); err != nil {
		return fmt.Errorf("failed to set allocator: %w", err)
	}

	logger.Info().
		Int("loaded_allocations", allocator.AllocatedCount()).
		Msg("Persistent IP allocator loaded from database")

	return nil
}

// BuildEndpoints builds the list of WireGuard endpoints to be advertised.
// CORAL_PUBLIC_ENDPOINT env var is merged into wireguardConfig.PublicEndpoints via struct tags.
func BuildEndpoints(port int, wireguardConfig config.WireGuardConfig) []string {
	var endpoints []string
	var rawEndpoints []string

	// Get endpoints from config (CORAL_PUBLIC_ENDPOINT env var already merged).
	if len(wireguardConfig.PublicEndpoints) > 0 {
		rawEndpoints = wireguardConfig.PublicEndpoints
	}

	// Process raw endpoints: extract host and ALWAYS use the WireGuard port.
	// We extract the host and ALWAYS use the WireGuard port, not the port from the env var.
	// This is because CORAL_PUBLIC_ENDPOINT typically contains the gRPC/Connect service address,
	// but we need to advertise the WireGuard UDP port for peer connections.
	for _, endpoint := range rawEndpoints {
		if endpoint == "" {
			continue
		}

		var host string
		// Try to extract host from endpoint (may have port)
		if h, _, err := net.SplitHostPort(endpoint); err == nil {
			host = h
		} else {
			// No port in the endpoint, use as-is
			host = endpoint
		}

		// Build WireGuard endpoint with the configured WireGuard port
		if host != "" {
			endpoints = append(endpoints, net.JoinHostPort(host, fmt.Sprintf("%d", port)))
		}
	}

	// If we found any endpoints, return them
	if len(endpoints) > 0 {
		return endpoints
	}

	// For local development: use localhost.
	// Agents on the same machine can connect via 127.0.0.1.
	//
	// For production deployments:
	// - Set CORAL_PUBLIC_ENDPOINT to your public IP or hostname (comma-separated for multiple)
	// - Or configure public_endpoints in the colony YAML config
	// - Or use NAT traversal/STUN (future feature)
	if port > 0 {
		endpoints = append(endpoints, net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port)))
	}

	return endpoints
}
