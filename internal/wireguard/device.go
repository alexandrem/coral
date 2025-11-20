package wireguard

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"

	"github.com/rs/zerolog"

	"github.com/coral-io/coral/internal/config"
	"github.com/coral-io/coral/internal/constants"
)

// Device represents a WireGuard device instance.
type Device struct {
	cfg         *config.WireGuardConfig
	wgDevice    *device.Device
	tunDevice   tun.Device
	iface       *Interface
	ipAllocator Allocator              // IP allocator interface (can be IPAllocator or PersistentIPAllocator).
	peers       map[string]*PeerConfig // publicKey -> PeerConfig
	mu          sync.RWMutex
	wgLogger    *device.Logger // WireGuard internal logger.
	logger      zerolog.Logger // Application logger.
	actualPort  int            // Actual bound UDP port (for ephemeral ports).
}

// NewDevice creates a new WireGuard device with the given configuration.
func NewDevice(cfg *config.WireGuardConfig, logger zerolog.Logger) (*Device, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}

	if cfg.PrivateKey == "" {
		return nil, fmt.Errorf("private key is required")
	}

	if cfg.PublicKey == "" {
		return nil, fmt.Errorf("public key is required")
	}

	// Set defaults
	if cfg.Port < 0 {
		// Negative value signals that the caller wants an ephemeral port.
		cfg.Port = 0
	} else if cfg.Port == 0 {
		cfg.Port = constants.DefaultWireGuardPort
	}

	if cfg.MTU == 0 {
		cfg.MTU = constants.DefaultWireGuardMTU
	}

	// Parse mesh network CIDR for IP allocation
	var subnet *net.IPNet
	if cfg.MeshNetworkIPv4 != "" {
		_, ipNet, err := net.ParseCIDR(cfg.MeshNetworkIPv4)
		if err != nil {
			return nil, fmt.Errorf("invalid mesh network IPv4: %w", err)
		}
		subnet = ipNet
	} else {
		_, subnet, _ = net.ParseCIDR(constants.DefaultColonyMeshIPv4Subnet)
	}

	// Create IP allocator
	allocator, err := NewIPAllocator(subnet)
	if err != nil {
		return nil, fmt.Errorf("failed to create IP allocator: %w", err)
	}

	// Create WireGuard internal logger.
	wgLogger := device.NewLogger(
		device.LogLevelError,
		fmt.Sprintf("(%s) ", "wg0"),
	)

	return &Device{
		cfg:         cfg,
		ipAllocator: allocator,
		peers:       make(map[string]*PeerConfig),
		wgLogger:    wgLogger,
		logger:      logger.With().Str("component", "wireguard").Logger(),
	}, nil
}

// Start brings up the WireGuard device and starts packet routing.
func (d *Device) Start() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.wgDevice != nil {
		return fmt.Errorf("device already started")
	}

	// Create TUN interface.
	iface, err := CreateTUN("wg0", d.cfg.MTU, d.logger)
	if err != nil {
		// Check if this is a permission error.
		if isPermissionError(err) {
			return d.wrapPermissionError(err)
		}
		return fmt.Errorf("failed to create TUN interface: %w", err)
	}
	d.iface = iface
	d.tunDevice = iface.Device()

	// Create WireGuard device with default bind.
	// STUN discovery runs before WireGuard starts to avoid port conflicts.
	bind := conn.NewDefaultBind()
	d.wgDevice = device.NewDevice(d.tunDevice, bind, d.wgLogger)

	// Configure device via UAPI
	if err := d.configure(); err != nil {
		_ = d.tunDevice.Close() // TODO: errcheck
		return fmt.Errorf("failed to configure device: %w", err)
	}

	// Bring up the device
	_ = d.wgDevice.Up() // TODO: errcheck

	// Query the actual listen port from the device.
	// This is important for ephemeral ports (when cfg.Port was 0 or negative).
	actualPort, err := d.queryListenPort()
	if err != nil {
		d.logger.Warn().Err(err).Msg("Failed to query actual listen port, using configured port")
		d.actualPort = d.cfg.Port
	} else {
		d.actualPort = actualPort
		// Update the config with the actual port for consistency.
		d.cfg.Port = actualPort
		d.logger.Debug().Int("actual_port", actualPort).Msg("Queried actual WireGuard listen port")
	}

	return nil
}

// Stop tears down the WireGuard device gracefully.
func (d *Device) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.wgDevice == nil {
		return nil
	}

	// Bring down the device
	_ = d.wgDevice.Down() // TODO: errcheck

	// Close the device
	d.wgDevice.Close()
	d.wgDevice = nil

	// Close TUN interface
	if d.tunDevice != nil {
		_ = d.tunDevice.Close() // TODO: errcheck
		d.tunDevice = nil
	}

	d.iface = nil

	return nil
}

// AddPeer adds or updates a WireGuard peer.
func (d *Device) AddPeer(peerConfig *PeerConfig) error {
	if err := ParsePeerConfig(peerConfig); err != nil {
		return fmt.Errorf("invalid peer config: %w", err)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.wgDevice == nil {
		return fmt.Errorf("device not started")
	}

	// Store peer config
	d.peers[peerConfig.PublicKey] = peerConfig

	// Build UAPI configuration for the peer
	uapiConfig := d.buildPeerUAPI(peerConfig)

	d.logger.Info().Msg("calling ipcset")

	// Apply peer configuration
	if err := d.wgDevice.IpcSet(uapiConfig); err != nil {
		return fmt.Errorf("failed to add peer: %w", err)
	}

	// Add routes for the peer's AllowedIPs.
	// Userspace WireGuard doesn't automatically create routes, so we must do it manually.

	d.logger.Debug().
		Bool("iface_exists", d.iface != nil).
		Strs("allowed_ips", peerConfig.AllowedIPs).
		Msg("Adding routes for peer")

	if d.iface != nil {
		if err := d.iface.AddRoutesForPeer(peerConfig.AllowedIPs); err != nil {
			// Log warning but don't fail - routes might already exist.
			d.logger.Debug().
				Err(err).
				Msg("AddRoutesForPeer returned error")
			_ = err
		}
	} else {
		d.logger.Warn().Msg("Interface is nil, cannot add routes")
	}

	return nil
}

// RemovePeer removes a WireGuard peer by public key.
func (d *Device) RemovePeer(publicKey string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.wgDevice == nil {
		return fmt.Errorf("device not started")
	}

	if _, ok := d.peers[publicKey]; !ok {
		return fmt.Errorf("peer not found: %s", publicKey)
	}

	// Remove from peers map
	delete(d.peers, publicKey)

	// Build UAPI configuration to remove peer
	uapiConfig := fmt.Sprintf("public_key=%s\nremove=true\n", publicKey)

	// Apply configuration
	if err := d.wgDevice.IpcSet(uapiConfig); err != nil {
		return fmt.Errorf("failed to remove peer: %w", err)
	}

	return nil
}

// GetPeer returns the configuration for a peer by public key.
func (d *Device) GetPeer(publicKey string) (*PeerConfig, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	peer, ok := d.peers[publicKey]
	return peer, ok
}

// ListPeers returns all configured peers.
func (d *Device) ListPeers() []*PeerConfig {
	d.mu.RLock()
	defer d.mu.RUnlock()

	peers := make([]*PeerConfig, 0, len(d.peers))
	for _, p := range d.peers {
		peers = append(peers, p)
	}

	return peers
}

// FlushAllPeerRoutes deletes all routes for all peers.
// This is useful when IP addresses change to clear cached source IPs from the kernel.
func (d *Device) FlushAllPeerRoutes() error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.iface == nil {
		return fmt.Errorf("interface not available")
	}

	d.logger.Debug().
		Int("peer_count", len(d.peers)).
		Msg("Flushing routes for all peers")

	for pubkey, peer := range d.peers {
		for _, allowedIP := range peer.AllowedIPs {
			// Parse IP from CIDR.
			ip, _, err := net.ParseCIDR(allowedIP)
			if err != nil {
				d.logger.Debug().
					Err(err).
					Str("allowed_ip", allowedIP).
					Msg("Failed to parse CIDR")
				continue
			}

			// Delete the route.
			if err := d.iface.DeleteRoute(ip); err != nil {
				// Log but don't fail - route might not exist.
				d.logger.Debug().
					Err(err).
					Str("peer_pubkey", pubkey[:8]+"...").
					Str("ip", ip.String()).
					Msg("Error deleting route")
			}
		}
	}

	return nil
}

// RefreshPeerRoutes re-adds routes for all peers.
// This is useful after IP address changes, which may delete existing routes.
func (d *Device) RefreshPeerRoutes() error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.iface == nil {
		return fmt.Errorf("interface not available")
	}

	d.logger.Debug().
		Int("peer_count", len(d.peers)).
		Msg("Refreshing routes for all peers")

	for pubkey, peer := range d.peers {
		d.logger.Debug().
			Str("peer_pubkey", pubkey[:8]+"...").
			Strs("allowed_ips", peer.AllowedIPs).
			Msg("Re-adding routes for peer")

		if err := d.iface.AddRoutesForPeer(peer.AllowedIPs); err != nil {
			// Log but don't fail - routes might already exist.
			d.logger.Debug().
				Err(err).
				Str("peer_pubkey", pubkey[:8]+"...").
				Msg("Error adding routes for peer")
		}
	}

	return nil
}

// Allocator returns the IP allocator for this device.
func (d *Device) Allocator() Allocator {
	return d.ipAllocator
}

// SetAllocator replaces the IP allocator for this device.
// This allows injecting a PersistentIPAllocator after device creation.
// Must be called before Start().
func (d *Device) SetAllocator(allocator Allocator) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.wgDevice != nil {
		return fmt.Errorf("cannot change allocator after device is started")
	}

	d.ipAllocator = allocator
	return nil
}

// Config returns the device configuration.
func (d *Device) Config() *config.WireGuardConfig {
	return d.cfg
}

// InterfaceName returns the name of the network interface.
func (d *Device) InterfaceName() string {
	if d.iface != nil {
		return d.iface.Name()
	}
	return ""
}

// Interface returns the Interface object for this device.
func (d *Device) Interface() *Interface {
	return d.iface
}

// ListenPort returns the UDP port the WireGuard device is listening on.
// Returns 0 if the device is not started or port is unknown.
func (d *Device) ListenPort() int {
	d.mu.RLock()
	defer d.mu.RUnlock()

	// Return the actual bound port (which may differ from cfg.Port for ephemeral ports).
	if d.actualPort > 0 {
		return d.actualPort
	}

	// Fall back to configured port if actual port is not available.
	if d.cfg == nil {
		return 0
	}

	return d.cfg.Port
}

// queryListenPort queries the actual listen port from the WireGuard device via UAPI.
func (d *Device) queryListenPort() (int, error) {
	if d.wgDevice == nil {
		return 0, fmt.Errorf("device not started")
	}

	// Query device configuration via UAPI.
	uapiResponse, err := d.wgDevice.IpcGet()
	if err != nil {
		return 0, fmt.Errorf("IpcGet failed: %w", err)
	}

	// Parse the response to find listen_port.
	// Format: "listen_port=12345\n..."
	lines := strings.Split(uapiResponse, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "listen_port=") {
			portStr := strings.TrimPrefix(line, "listen_port=")
			port := 0
			if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
				return 0, fmt.Errorf("failed to parse listen_port: %w", err)
			}
			return port, nil
		}
	}

	return 0, fmt.Errorf("listen_port not found in UAPI response")
}

// configure applies the initial device configuration via UAPI.
func (d *Device) configure() error {
	// Decode private key from base64
	privateKeyBytes, err := base64.StdEncoding.DecodeString(d.cfg.PrivateKey)
	if err != nil {
		return fmt.Errorf("invalid private key encoding: %w", err)
	}

	// Convert to hex for UAPI
	privateKeyHex := fmt.Sprintf("%x", privateKeyBytes)

	// Build UAPI configuration
	uapiConfig := strings.Builder{}
	uapiConfig.WriteString(fmt.Sprintf("private_key=%s\n", privateKeyHex))

	if d.cfg.Port > 0 {
		uapiConfig.WriteString(fmt.Sprintf("listen_port=%d\n", d.cfg.Port))
	}

	// Apply configuration
	if err := d.wgDevice.IpcSet(uapiConfig.String()); err != nil {
		return fmt.Errorf("failed to configure device: %w", err)
	}

	return nil
}

// buildPeerUAPI builds UAPI configuration string for a peer.
func (d *Device) buildPeerUAPI(peerConfig *PeerConfig) string {
	uapiConfig := strings.Builder{}

	// Decode public key from base64 to hex
	pubKeyBytes, err := base64.StdEncoding.DecodeString(peerConfig.PublicKey)
	if err != nil {
		// Should not happen as we validated in ParsePeerConfig
		return ""
	}
	pubKeyHex := fmt.Sprintf("%x", pubKeyBytes)

	uapiConfig.WriteString(fmt.Sprintf("public_key=%s\n", pubKeyHex))

	// Add endpoint if provided
	if peerConfig.Endpoint != "" {
		uapiConfig.WriteString(fmt.Sprintf("endpoint=%s\n", peerConfig.Endpoint))
	}

	// Add allowed IPs
	for _, allowedIP := range peerConfig.AllowedIPs {
		uapiConfig.WriteString(fmt.Sprintf("allowed_ip=%s\n", allowedIP))
	}

	// Add persistent keepalive
	if peerConfig.PersistentKeepalive > 0 {
		uapiConfig.WriteString(fmt.Sprintf("persistent_keepalive_interval=%d\n", peerConfig.PersistentKeepalive))
	}

	return uapiConfig.String()
}

// isPermissionError checks if an error is related to insufficient privileges.
func isPermissionError(err error) bool {
	if err == nil {
		return false
	}

	// Define known permission errors.
	known := []error{os.ErrPermission, syscall.EPERM, syscall.EACCES}
	for _, e := range known {
		if errors.Is(err, e) {
			return true
		}
	}

	// Fallback to message (case-insensitive).
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "permission denied") ||
		strings.Contains(msg, "operation not permitted") ||
		strings.Contains(msg, "access denied")
}

// wrapPermissionError wraps a permission error with a helpful message.
func (d *Device) wrapPermissionError(err error) error {
	binaryPath, _ := os.Executable()
	if binaryPath == "" {
		binaryPath = constants.DefaultBinaryPath
	}

	var helpMsg strings.Builder
	helpMsg.WriteString(fmt.Sprintf("Failed to create TUN device: %v\n\n", err))
	helpMsg.WriteString("TUN device creation requires elevated privileges. Choose one of:\n\n")

	if runtime.GOOS == "linux" {
		helpMsg.WriteString("  1. Install capabilities (Linux only, recommended):\n")
		helpMsg.WriteString(fmt.Sprintf("     sudo setcap cap_net_admin+ep %s\n\n", binaryPath))
		helpMsg.WriteString("  2. Run with sudo:\n")
		helpMsg.WriteString("     sudo coral colony start\n\n")
		helpMsg.WriteString("  3. Make binary setuid (use with caution):\n")
		helpMsg.WriteString(fmt.Sprintf("     sudo chown root:root %s\n", binaryPath))
		helpMsg.WriteString(fmt.Sprintf("     sudo chmod u+s %s\n\n", binaryPath))
	} else {
		// macOS and other platforms.
		helpMsg.WriteString("  1. Run with sudo:\n")
		helpMsg.WriteString("     sudo coral colony start\n\n")
		helpMsg.WriteString("  2. Make binary setuid (use with caution):\n")
		helpMsg.WriteString(fmt.Sprintf("     sudo chown root:root %s\n", binaryPath))
		helpMsg.WriteString(fmt.Sprintf("     sudo chmod u+s %s\n\n", binaryPath))
	}

	helpMsg.WriteString("For more information, see: docs/INSTALLATION.md")

	return fmt.Errorf("%s", helpMsg.String())
}
