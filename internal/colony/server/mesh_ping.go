package server

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"connectrpc.com/connect"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/constants"
	"github.com/coral-mesh/coral/internal/safe"
)

// MeshPing handles mesh troubleshooting pings from colony to agents (RFD 097).
func (s *Server) MeshPing(
	ctx context.Context,
	req *connect.Request[colonyv1.MeshPingRequest],
) (*connect.Response[colonyv1.MeshPingResponse], error) {
	s.logger.Info().
		Str("agent_id", req.Msg.AgentId).
		Int32("count", req.Msg.Count).
		Int32("timeout_ms", req.Msg.TimeoutMs).
		Msg("Mesh ping request received")

	// 1. Identify target agents.
	type pingTarget struct {
		id string
		ip string
	}
	var targets []pingTarget

	if req.Msg.AgentId != "" {
		entry, err := s.registry.Get(req.Msg.AgentId)
		if err != nil {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("agent %s not found", req.Msg.AgentId))
		}
		targets = append(targets, pingTarget{id: entry.AgentID, ip: entry.MeshIPv4})
	} else {
		all := s.registry.ListAll()
		for _, e := range all {
			targets = append(targets, pingTarget{id: e.AgentID, ip: e.MeshIPv4})
		}
	}

	if len(targets) == 0 {
		return connect.NewResponse(&colonyv1.MeshPingResponse{}), nil
	}

	// 2. Set defaults.
	count := int(req.Msg.Count)
	if count <= 0 {
		count = 3
	}
	timeout := time.Duration(req.Msg.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 1 * time.Second
	}

	// 3. Perform pings concurrently.
	var wg sync.WaitGroup
	results := make([]*colonyv1.MeshPingResponse_AgentPingResult, len(targets))

	for i, target := range targets {
		wg.Add(1)
		go func(idx int, id, ip string) {
			defer wg.Done()
			results[idx] = s.pingAgent(ctx, id, ip, count, timeout)
		}(i, target.id, target.ip)
	}

	wg.Wait()

	return connect.NewResponse(&colonyv1.MeshPingResponse{
		Results: results,
	}), nil
}

func (s *Server) pingAgent(ctx context.Context, agentID, meshIP string, count int, timeout time.Duration) *colonyv1.MeshPingResponse_AgentPingResult {
	result := &colonyv1.MeshPingResponse_AgentPingResult{
		AgentId: agentID,
		MeshIp:  meshIP,
	}

	if meshIP == "" {
		result.Error = "No mesh IP assigned"
		return result
	}

	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", meshIP, constants.DefaultMeshPingPort))
	if err != nil {
		result.Error = fmt.Sprintf("Failed to resolve address: %v", err)
		return result
	}

	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		result.Error = fmt.Sprintf("Failed to open UDP socket: %v", err)
		return result
	}
	defer safe.Close(conn, s.logger, "failed to close socket")

	buffer := make([]byte, 1024)
	var minRTT = 100 * time.Second // sentinel; replaced on first received packet
	var maxRTT time.Duration
	var totalRTT time.Duration
	received := 0

	for i := 0; i < count; i++ {
		select {
		case <-ctx.Done():
			result.Error = "Context cancelled"
			return result
		default:
		}

		msg := fmt.Sprintf("PING %d %d", i, time.Now().UnixNano())
		_ = conn.SetDeadline(time.Now().Add(timeout))
		start := time.Now()
		_, err := conn.Write([]byte(msg))
		if err != nil {
			continue
		}

		_, err = conn.Read(buffer)
		if err != nil {
			continue
		}

		rtt := time.Since(start)
		received++
		totalRTT += rtt
		if rtt < minRTT {
			minRTT = rtt
		}
		if rtt > maxRTT {
			maxRTT = rtt
		}

		if i < count-1 {
			// Short inter-ping delay; respect context cancellation.
			select {
			case <-ctx.Done():
				result.Error = "Context cancelled"
				return result
			case <-time.After(100 * time.Millisecond):
			}
		}
	}

	result.Sent = int32(count)
	result.Received = int32(received)
	if count > 0 {
		result.PacketLossPercentage = 100.0 * float64(count-received) / float64(count)
	}

	if received > 0 {
		result.MinRttMs = float64(minRTT.Microseconds()) / 1000.0
		result.MaxRttMs = float64(maxRTT.Microseconds()) / 1000.0
		result.AvgRttMs = float64((totalRTT / time.Duration(received)).Microseconds()) / 1000.0
	}

	return result
}
