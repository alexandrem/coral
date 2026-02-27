package wireguard

import (
	networkv1 "github.com/coral-mesh/coral/coral/network/v1"
)

// MapToMeshTelemetryProto converts the dynamically gathered WireGuard mesh JSON dictionary to strictly typed Protobuf.
func MapToMeshTelemetryProto(info map[string]interface{}) *networkv1.MeshTelemetry {
	if info == nil {
		return nil
	}

	telemetry := &networkv1.MeshTelemetry{}

	if ip, ok := info["mesh_ip"].(string); ok {
		telemetry.MeshIp = ip
	}
	if subnet, ok := info["mesh_subnet"].(string); ok {
		telemetry.MeshSubnet = subnet
	}

	if wgInfo, ok := info["wireguard"].(map[string]interface{}); ok {
		wgTelemetry := &networkv1.WireGuardTelemetry{}
		if exists, ok := wgInfo["interface_exists"].(bool); ok {
			wgTelemetry.InterfaceExists = exists
		}
		if status, ok := wgInfo["link_status"].(string); ok {
			wgTelemetry.LinkStatus = status
		}

		if peersList, ok := wgInfo["peers"].([]interface{}); ok {
			for _, p := range peersList {
				if peerMap, ok := p.(map[string]interface{}); ok {
					peer := &networkv1.WireGuardPeer{}
					if pk, ok := peerMap["public_key"].(string); ok {
						peer.PublicKey = pk
					}
					if ep, ok := peerMap["endpoint"].(string); ok {
						peer.Endpoint = ep
					}
					if rx, ok := peerMap["rx_bytes"].(float64); ok {
						peer.RxBytes = int64(rx)
					}
					if tx, ok := peerMap["tx_bytes"].(float64); ok {
						peer.TxBytes = int64(tx)
					}
					if hs, ok := peerMap["last_handshake_time"].(string); ok {
						peer.LastHandshakeTime = hs
					}
					wgTelemetry.Peers = append(wgTelemetry.Peers, peer)
				}
			}
		}
		telemetry.Wireguard = wgTelemetry
	}

	return telemetry
}
