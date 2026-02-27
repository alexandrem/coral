package wireguard

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// PeerStats contains real-time statistics for a WireGuard peer retrieved via UAPI.
type PeerStats struct {
	PublicKey           string
	Endpoint            string
	LastHandshakeTime   time.Time
	RxBytes             int64
	TxBytes             int64
	AllowedIPs          []string
	PersistentKeepalive int
}

// DeviceStats contains the real-time statistics for the entire WireGuard device.
type DeviceStats struct {
	PrivateKey string
	ListenPort int
	Peers      map[string]*PeerStats // map of base64 public key to stats
}

// GetStats queries the WireGuard device via UAPI and parses the real-time statistics.
func (d *Device) GetStats() (*DeviceStats, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.wgDevice == nil {
		return nil, fmt.Errorf("device not started")
	}

	uapiResponse, err := d.wgDevice.IpcGet()
	if err != nil {
		return nil, fmt.Errorf("IpcGet failed: %w", err)
	}

	return ParseUAPI(uapiResponse)
}

// ParseUAPI parses the raw string response from wireguard-go IpcGet()
// into a structured DeviceStats format containing metrics and configurations.
func ParseUAPI(uapi string) (*DeviceStats, error) {
	stats := &DeviceStats{
		Peers: make(map[string]*PeerStats),
	}

	var currentPeer *PeerStats

	lines := strings.Split(uapi, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		switch key {
		case "private_key":
			stats.PrivateKey = val
		case "listen_port":
			port, _ := strconv.Atoi(val)
			stats.ListenPort = port
		case "public_key":
			// This starts a new peer block

			// Try to convert pubkey from hex back to base64, as wireguard-go UAPI uses hex
			// whereas we store and configure them as base64.
			decodedHex, err := hex.DecodeString(val)
			base64Key := val
			if err == nil {
				base64Key = base64.StdEncoding.EncodeToString(decodedHex)
			}

			currentPeer = &PeerStats{
				PublicKey: base64Key,
			}
			stats.Peers[base64Key] = currentPeer

		// The following keys are context-dependent (belong to the current peer)
		case "endpoint":
			if currentPeer != nil {
				currentPeer.Endpoint = val
			}
		case "last_handshake_time_sec":
			if currentPeer != nil {
				sec, _ := strconv.ParseInt(val, 10, 64)
				// We'll update the time object. Nanoseconds might come later.
				currentPeer.LastHandshakeTime = time.Unix(sec, int64(currentPeer.LastHandshakeTime.Nanosecond()))
			}
		case "last_handshake_time_nsec":
			if currentPeer != nil {
				nsec, _ := strconv.ParseInt(val, 10, 64)
				currentPeer.LastHandshakeTime = time.Unix(currentPeer.LastHandshakeTime.Unix(), nsec)
			}
		case "rx_bytes":
			if currentPeer != nil {
				rx, _ := strconv.ParseInt(val, 10, 64)
				currentPeer.RxBytes = rx
			}
		case "tx_bytes":
			if currentPeer != nil {
				tx, _ := strconv.ParseInt(val, 10, 64)
				currentPeer.TxBytes = tx
			}
		case "allowed_ip":
			if currentPeer != nil {
				currentPeer.AllowedIPs = append(currentPeer.AllowedIPs, val)
			}
		case "persistent_keepalive_interval":
			if currentPeer != nil {
				keepalive, _ := strconv.Atoi(val)
				currentPeer.PersistentKeepalive = keepalive
			}
		}
	}

	return stats, nil
}
