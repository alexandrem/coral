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

	if wgInfo, ok := info["status"].(map[string]interface{}); ok {
		if pk, ok := wgInfo["public_key"].(string); ok {
			telemetry.PublicKey = pk
		}
		if port, ok := wgInfo["listen_port"].(int); ok {
			telemetry.ListenPort = int32(port)
		} else if port, ok := wgInfo["listen_port"].(float64); ok {
			telemetry.ListenPort = int32(port)
		} else if port, ok := wgInfo["listen_port"].(int32); ok {
			telemetry.ListenPort = port
		}

		if endpoints, ok := wgInfo["endpoints"].([]string); ok {
			telemetry.Endpoints = endpoints
		} else if eps, ok := wgInfo["endpoints"].([]interface{}); ok {
			for _, ep := range eps {
				if s, ok := ep.(string); ok {
					telemetry.Endpoints = append(telemetry.Endpoints, s)
				}
			}
		}

		wgTelemetry := &networkv1.WireGuardTelemetry{}
		if exists, ok := wgInfo["interface_exists"].(bool); ok {
			wgTelemetry.InterfaceExists = exists
		}
		if status, ok := wgInfo["link_status"].(string); ok {
			wgTelemetry.LinkStatus = status
		}

		// Handle peers list which could be []interface{} (from JSON) or []map[string]interface{} (direct)
		var peersRaw []interface{}
		if pList, ok := wgInfo["peers"].([]interface{}); ok {
			peersRaw = pList
		} else if pList, ok := wgInfo["peers"].([]map[string]interface{}); ok {
			for _, p := range pList {
				peersRaw = append(peersRaw, p)
			}
		}

		for _, p := range peersRaw {
			if peerMap, ok := p.(map[string]interface{}); ok {
				peer := &networkv1.WireGuardPeer{}
				if pk, ok := peerMap["public_key"].(string); ok {
					peer.PublicKey = pk
				}
				if ep, ok := peerMap["endpoint"].(string); ok {
					peer.Endpoint = ep
				}

				// Handle numeric types robustly (int64 from direct, float64 from JSON)
				peer.RxBytes = getInt64(peerMap, "rx_bytes")
				peer.TxBytes = getInt64(peerMap, "tx_bytes")

				if hs, ok := peerMap["last_handshake_time"].(string); ok {
					peer.LastHandshakeTime = hs
				}
				wgTelemetry.Peers = append(wgTelemetry.Peers, peer)
			}
		}
		telemetry.Status = wgTelemetry
	}

	return telemetry
}

func getInt64(m map[string]interface{}, key string) int64 {
	val, ok := m[key]
	if !ok {
		return 0
	}
	switch v := val.(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	case int:
		return int64(v)
	}
	return 0
}
