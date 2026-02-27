package wireguard

import (
	"github.com/coral-mesh/coral/internal/logging"
	"github.com/coral-mesh/coral/internal/wireguard"
)

// GatherMeshInfo gathers WireGuard mesh network information for the colony status endpoint.
func GatherMeshInfo(
	wgDevice *wireguard.Device,
	meshIP, meshSubnet string,
	colonyID string,
	logger logging.Logger,
) map[string]interface{} {
	info := make(map[string]interface{})

	// Basic mesh info.
	info["colony_id"] = colonyID
	info["mesh_ip"] = meshIP
	info["mesh_subnet"] = meshSubnet

	// WireGuard interface info.
	if wgDevice != nil {
		wgInfo := make(map[string]interface{})
		wgInfo["interface_name"] = wgDevice.InterfaceName()
		wgInfo["listen_port"] = wgDevice.ListenPort()
		wgInfo["public_key"] = wgDevice.Config().PublicKey
		wgInfo["endpoints"] = wgDevice.Config().PublicEndpoints

		// Get interface status.
		iface := wgDevice.Interface()
		if iface != nil {
			wgInfo["interface_exists"] = true

			// Get peer information.
			peers := wgDevice.ListPeers()

			// Try to get dynamic stats from UAPI.
			stats, err := wgDevice.GetStats()
			if err != nil {
				logger.Warn().Err(err).Msg("Failed to gather dynamic WireGuard stats via UAPI")
			}

			peerInfos := make([]map[string]interface{}, 0, len(peers))
			for _, peer := range peers {
				peerInfo := make(map[string]interface{})
				peerInfo["public_key"] = peer.PublicKey[:16] + "..."

				// Base properties from internal configuration.
				peerInfo["configured_endpoint"] = peer.Endpoint
				peerInfo["allowed_ips"] = peer.AllowedIPs
				peerInfo["persistent_keepalive"] = peer.PersistentKeepalive

				// Dynamic properties from UAPI.
				if stats != nil {
					pStats, ok := stats.Peers[peer.PublicKey]
					// Wireguard UAPI might surface endpoints we haven't seen explicitly configured.
					if ok {
						if pStats.Endpoint != "" {
							peerInfo["endpoint"] = pStats.Endpoint
						}
						peerInfo["rx_bytes"] = pStats.RxBytes
						peerInfo["tx_bytes"] = pStats.TxBytes

						if !pStats.LastHandshakeTime.IsZero() {
							// Return ISO8601 formatted string.
							peerInfo["last_handshake_time"] = pStats.LastHandshakeTime.UTC().Format("2006-01-02T15:04:05Z")
						}
					}
				}

				// Fallback to configured endpoint if dynamic endpoint wasn't discovered.
				if peerInfo["endpoint"] == nil {
					peerInfo["endpoint"] = peer.Endpoint
				}

				peerInfos = append(peerInfos, peerInfo)
			}
			wgInfo["peers"] = peerInfos
			wgInfo["peer_count"] = len(peers)
		} else {
			wgInfo["interface_exists"] = false
		}

		info["status"] = wgInfo
	}

	return info
}
