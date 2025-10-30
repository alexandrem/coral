package wireguard

import (
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"sync"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"

	"github.com/coral-io/coral/internal/config"
)

// Device represents a WireGuard device instance.
type Device struct {
	cfg         *config.WireGuardConfig
	wgDevice    *device.Device
	tunDevice   tun.Device
	iface       *Interface
	ipAllocator *IPAllocator
	peers       map[string]*PeerConfig // publicKey -> PeerConfig
	mu          sync.RWMutex
	logger      *device.Logger
}

// NewDevice creates a new WireGuard device with the given configuration.
func NewDevice(cfg *config.WireGuardConfig) (*Device, error) {
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
	if cfg.Port == 0 {
		cfg.Port = 41580
	}

	if cfg.MTU == 0 {
		cfg.MTU = 1420
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
		// Default to 10.42.0.0/16
		_, subnet, _ = net.ParseCIDR("10.42.0.0/16")
	}

	// Create IP allocator
	allocator, err := NewIPAllocator(subnet)
	if err != nil {
		return nil, fmt.Errorf("failed to create IP allocator: %w", err)
	}

	// Create logger
	logger := device.NewLogger(
		device.LogLevelError,
		fmt.Sprintf("(%s) ", "wg0"),
	)

	return &Device{
		cfg:         cfg,
		ipAllocator: allocator,
		peers:       make(map[string]*PeerConfig),
		logger:      logger,
	}, nil
}

// Start brings up the WireGuard device and starts packet routing.
func (d *Device) Start() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.wgDevice != nil {
		return fmt.Errorf("device already started")
	}

	// Create TUN interface
	iface, err := CreateTUN("wg0", d.cfg.MTU)
	if err != nil {
		return fmt.Errorf("failed to create TUN interface: %w", err)
	}
	d.iface = iface
	d.tunDevice = iface.Device()

	// Create WireGuard device
	bind := conn.NewDefaultBind()
	d.wgDevice = device.NewDevice(d.tunDevice, bind, d.logger)

	// Configure device via UAPI
	if err := d.configure(); err != nil {
		d.tunDevice.Close()
		return fmt.Errorf("failed to configure device: %w", err)
	}

	// Bring up the device
	d.wgDevice.Up()

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
	d.wgDevice.Down()

	// Close the device
	d.wgDevice.Close()
	d.wgDevice = nil

	// Close TUN interface
	if d.tunDevice != nil {
		d.tunDevice.Close()
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

	// Apply peer configuration
	if err := d.wgDevice.IpcSet(uapiConfig); err != nil {
		return fmt.Errorf("failed to add peer: %w", err)
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

// Allocator returns the IP allocator for this device.
func (d *Device) Allocator() *IPAllocator {
	return d.ipAllocator
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
