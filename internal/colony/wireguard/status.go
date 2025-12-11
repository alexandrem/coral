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

		// Get interface status.
		iface := wgDevice.Interface()
		if iface != nil {
			wgInfo["interface_exists"] = true

			// Get peer information.
			peers := wgDevice.ListPeers()
			peerInfos := make([]map[string]interface{}, 0, len(peers))
			for _, peer := range peers {
				peerInfo := make(map[string]interface{})
				peerInfo["public_key"] = peer.PublicKey[:16] + "..."
				peerInfo["endpoint"] = peer.Endpoint
				peerInfo["allowed_ips"] = peer.AllowedIPs
				peerInfo["persistent_keepalive"] = peer.PersistentKeepalive
				peerInfos = append(peerInfos, peerInfo)
			}
			wgInfo["peers"] = peerInfos
			wgInfo["peer_count"] = len(peers)
		} else {
			wgInfo["interface_exists"] = false
		}

		info["wireguard"] = wgInfo
	}

	return info
}
