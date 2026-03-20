package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	networkv1 "github.com/coral-mesh/coral/coral/network/v1"
	"github.com/coral-mesh/coral/internal/colony"
	"github.com/coral-mesh/coral/internal/colony/ca"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/registry"
	"github.com/coral-mesh/coral/internal/colony/storage"
	"github.com/coral-mesh/coral/internal/constants"
	"github.com/coral-mesh/coral/internal/wireguard"
)

// MeshInfoProvider is a callback that fetches live WireGuard/mesh statistics.
type MeshInfoProvider func() map[string]interface{}

// WGStatsProvider returns the WireGuard device's live peer stats and configured peer list.
// Returns (nil, nil) if the device is not available.
type WGStatsProvider func() (*wireguard.DeviceStats, []*wireguard.PeerConfig)

// Config contains configuration for the colony server.
type Config struct {
	ColonyID           string
	ApplicationName    string
	Environment        string
	DashboardPort      int
	StoragePath        string
	WireGuardPort      int
	WireGuardPublicKey string
	WireGuardEndpoints []string
	ConnectPort        int
	MeshIPv4           string
	MeshIPv6           string
	PublicEndpointURL  string // RFD 031 - public endpoint URL if enabled.
}

// Server implements the ColonyService.
type Server struct {
	registry         *registry.Registry
	database         *database.Database
	caManager        *ca.Manager // RFD 047 - certificate authority manager.
	ebpfService      interface{} // EbpfQueryService - using interface to avoid import cycle
	config           Config
	startTime        time.Time
	logger           zerolog.Logger
	meshInfoProvider MeshInfoProvider
	wgStatsProvider  WGStatsProvider
}

// New creates a new colony server.
func New(reg *registry.Registry, db *database.Database, caManager *ca.Manager, config Config, logger zerolog.Logger) *Server {
	return &Server{
		registry:  reg,
		database:  db,
		caManager: caManager,
		config:    config,
		startTime: time.Now(),
		logger:    logger,
	}
}

// SetMeshInfoProvider sets the callback used for providing mesh metrics dynamically.
func (s *Server) SetMeshInfoProvider(provider MeshInfoProvider) {
	s.meshInfoProvider = provider
}

// SetWGStatsProvider sets the callback for accessing WireGuard device stats and peer configs.
func (s *Server) SetWGStatsProvider(provider WGStatsProvider) {
	s.wgStatsProvider = provider
}

// SetEbpfService sets the eBPF query service instance.
func (s *Server) SetEbpfService(ebpfService interface{}) {
	s.ebpfService = ebpfService
	s.logger.Info().Msg("eBPF query service attached to colony server")
}

// Ensure Server implements the ColonyServiceHandler interface.
var _ colonyv1connect.ColonyServiceHandler = (*Server)(nil)

// GetStatus handles colony status requests.
func (s *Server) GetStatus(
	ctx context.Context,
	req *connect.Request[colonyv1.GetStatusRequest],
) (*connect.Response[colonyv1.GetStatusResponse], error) {
	resp := s.GetStatusResponse()
	return connect.NewResponse(resp), nil
}

// GetStatusResponse builds the status response.
// This is used internally by both the gRPC handler and HTTP handler.
func (s *Server) GetStatusResponse() *colonyv1.GetStatusResponse {
	// Calculate uptime.
	uptime := time.Since(s.startTime)
	uptimeSeconds := int64(uptime.Seconds())

	// Get agent count.
	agentCount := int32(s.registry.Count())
	activeCount, degradedCount := s.registry.CountByStatus()

	// Determine colony status based on agent health.
	status := s.determineColonyStatus()

	// Calculate storage size.
	storageBytes, err := storage.CalculateSize(s.config.StoragePath)
	if err != nil {
		s.logger.Warn().Err(err).Msg("Failed to calculate storage size")
		storageBytes = 0
	}

	// Build dashboard URL.
	dashboardURL := ""
	if s.config.DashboardPort > 0 {
		dashboardURL = fmt.Sprintf("http://localhost:%d", s.config.DashboardPort)
	}

	s.logger.Debug().
		Str("status", status).
		Int32("agent_count", agentCount).
		Int32("active_count", activeCount).
		Int32("degraded_count", degradedCount).
		Int64("uptime_seconds", uptimeSeconds).
		Msg("Colony status response prepared")

	// Fetch dynamic mesh telemetry and map to strictly typed Protobuf struct
	var meshTelemetry *networkv1.MeshTelemetry
	if s.meshInfoProvider != nil {
		if meshInfo := s.meshInfoProvider(); meshInfo != nil {
			meshTelemetry = wireguard.MapToMeshTelemetryProto(meshInfo)
		}
	}

	// Build response.
	return &colonyv1.GetStatusResponse{
		ColonyId:           s.config.ColonyID,
		AppName:            s.config.ApplicationName,
		Environment:        s.config.Environment,
		Status:             status,
		StartedAt:          timestamppb.New(s.startTime),
		UptimeSeconds:      uptimeSeconds,
		AgentCount:         agentCount,
		ActiveAgentCount:   activeCount,
		DegradedAgentCount: degradedCount,
		DashboardUrl:       dashboardURL,
		StorageBytes:       storageBytes,
		WireguardPort:      int32(s.config.WireGuardPort),
		WireguardPublicKey: s.config.WireGuardPublicKey,
		WireguardEndpoints: s.config.WireGuardEndpoints,
		ConnectPort:        int32(s.config.ConnectPort),
		MeshIpv4:           s.config.MeshIPv4,
		MeshIpv6:           s.config.MeshIPv6,
		PublicEndpointUrl:  s.config.PublicEndpointURL,
		Wireguard:          meshTelemetry,
	}
}

// ListAgents handles agent list requests.
func (s *Server) ListAgents(
	ctx context.Context,
	req *connect.Request[colonyv1.ListAgentsRequest],
) (*connect.Response[colonyv1.ListAgentsResponse], error) {
	s.logger.Debug().Msg("List agents request received")

	// Get all registered agents.
	entries := s.registry.ListAll()

	// Convert registry entries to protobuf agents.
	agents := make([]*colonyv1.Agent, len(entries))
	now := time.Now()

	// Use a WaitGroup to query agents concurrently.
	var wg sync.WaitGroup
	wg.Add(len(entries))

	for i, entry := range entries {
		// Capture loop variables.
		index := i
		e := entry

		go func() {
			defer wg.Done()

			status := registry.DetermineStatus(e.LastSeen, now)

			// Initialize agent with registry data.
			agent := &colonyv1.Agent{
				AgentId:        e.AgentID,
				ComponentName:  e.Name,
				MeshIpv4:       e.MeshIPv4,
				MeshIpv6:       e.MeshIPv6,
				LastSeen:       timestamppb.New(e.LastSeen),
				Status:         string(status),
				Services:       e.Services,       // Default to registry data
				RuntimeContext: e.RuntimeContext, // RFD 018: Runtime context
			}

			// If agent is healthy/degraded, try to query real-time services.
			if status == registry.StatusHealthy || status == registry.StatusDegraded {
				// Create agent client.
				// Agent listens on DefaultAgentPort for mesh traffic.
				client := colony.GetAgentClient(e)

				// Short timeout for real-time query.
				queryCtx, cancel := context.WithTimeout(ctx, constants.DefaultColonyRealtimeQueryTimeout)
				defer cancel()

				resp, err := client.ListServices(queryCtx, connect.NewRequest(&agentv1.ListServicesRequest{}))
				if err == nil {
					// Update services from real-time response.
					realTimeServices := make([]*meshv1.ServiceInfo, 0, len(resp.Msg.Services))
					for _, svcStatus := range resp.Msg.Services {
						realTimeServices = append(realTimeServices, &meshv1.ServiceInfo{
							Name:           svcStatus.Name,
							Port:           svcStatus.Port,
							HealthEndpoint: svcStatus.HealthEndpoint,
							ServiceType:    svcStatus.ServiceType,
							Labels:         svcStatus.Labels,
							ProcessId:      svcStatus.ProcessId,
							BinaryPath:     svcStatus.BinaryPath,
							BinaryHash:     svcStatus.BinaryHash,
						})
					}
					agent.Services = realTimeServices
				} else {
					s.logger.Debug().
						Err(err).
						Str("agent_id", e.AgentID).
						Msg("Failed to query agent services in real-time, using registry fallback")
				}
			}

			agents[index] = agent
		}()
	}

	// Wait for all queries to complete.
	wg.Wait()

	resp := &colonyv1.ListAgentsResponse{
		Agents: agents,
	}

	s.logger.Debug().
		Int("agent_count", len(agents)).
		Msg("List agents response prepared")

	return connect.NewResponse(resp), nil
}

// GetTopology handles topology request (RFD 092).
// Returns all registered agents and the live service dependency graph derived
// from observed trace data.
func (s *Server) GetTopology(
	ctx context.Context,
	req *connect.Request[colonyv1.GetTopologyRequest],
) (*connect.Response[colonyv1.GetTopologyResponse], error) {
	s.logger.Debug().Msg("Get topology request received")

	// Get all registered agents.
	entries := s.registry.ListAll()

	// Convert registry entries to protobuf agents.
	agents := make([]*colonyv1.Agent, 0, len(entries))
	now := time.Now()

	for _, entry := range entries {
		status := registry.DetermineStatus(entry.LastSeen, now)

		agent := &colonyv1.Agent{
			AgentId:        entry.AgentID,
			ComponentName:  entry.Name,
			MeshIpv4:       entry.MeshIPv4,
			MeshIpv6:       entry.MeshIPv6,
			LastSeen:       timestamppb.New(entry.LastSeen),
			Status:         string(status),
			Services:       entry.Services,       // RFD 011: Multi-service support
			RuntimeContext: entry.RuntimeContext, // RFD 018: Runtime context
		}
		agents = append(agents, agent)
	}

	// Derive service connections from trace data (default 1h window).
	since := time.Now().Add(-time.Hour)
	serviceConns, err := s.database.GetServiceConnections(ctx, since)
	if err != nil {
		// Non-fatal: return agents without connections rather than failing.
		s.logger.Warn().Err(err).Msg("Failed to fetch service connections for topology")
		serviceConns = nil
	}

	// Build a set of L7 edges keyed by (source, target) for overlap detection.
	type edgeKey struct{ src, dst string }
	l7Edges := make(map[edgeKey]bool, len(serviceConns))

	connections := make([]*colonyv1.Connection, 0, len(serviceConns))
	for _, sc := range serviceConns {
		connections = append(connections, &colonyv1.Connection{
			SourceId:       sc.FromService,
			TargetId:       sc.ToService,
			ConnectionType: sc.Protocol,
			EvidenceLayer:  colonyv1.EvidenceLayer_EVIDENCE_LAYER_L7_TRACE,
		})
		l7Edges[edgeKey{sc.FromService, sc.ToService}] = true
	}

	// Fetch L4 network connections and merge (RFD 033).
	l4Conns, err := s.database.GetL4Connections(ctx, since)
	if err != nil {
		s.logger.Warn().Err(err).Msg("Failed to fetch L4 topology connections")
		l4Conns = nil
	}

	for _, lc := range l4Conns {
		// Determine the target identity: prefer agent ID, fall back to dest_ip:port.
		target := lc.DestIP
		if lc.DestAgentID != "" {
			target = lc.DestAgentID
		}

		key := edgeKey{lc.SourceAgentID, target}
		if l7Edges[key] {
			// Edge already present from L7 data — promote to BOTH.
			for _, c := range connections {
				if c.SourceId == lc.SourceAgentID && c.TargetId == target {
					c.EvidenceLayer = colonyv1.EvidenceLayer_EVIDENCE_LAYER_BOTH
					break
				}
			}
			continue
		}

		connections = append(connections, &colonyv1.Connection{
			SourceId:       lc.SourceAgentID,
			TargetId:       target,
			ConnectionType: lc.Protocol,
			EvidenceLayer:  colonyv1.EvidenceLayer_EVIDENCE_LAYER_L4_NETWORK,
		})
	}

	resp := &colonyv1.GetTopologyResponse{
		ColonyId:    s.config.ColonyID,
		Agents:      agents,
		Connections: connections,
	}

	s.logger.Debug().
		Int("agent_count", len(agents)).
		Int("connection_count", len(connections)).
		Msg("Get topology response prepared")

	return connect.NewResponse(resp), nil
}

// ReportConnections receives a stream of L4 connection batches from an agent,
// correlates destination IPs against the agent registry, and upserts the results
// into topology_connections (RFD 033).
func (s *Server) ReportConnections(
	ctx context.Context,
	stream *connect.ClientStream[colonyv1.ReportConnectionsRequest],
) (*connect.Response[colonyv1.ReportConnectionsResponse], error) {
	var totalBatches, totalEntries int

	for stream.Receive() {
		msg := stream.Msg()

		if msg.AgentId == "" {
			s.logger.Warn().Msg("ReportConnections: received batch with empty agent_id, skipping")
			continue
		}

		if len(msg.Connections) == 0 {
			continue
		}

		entries := make([]database.TopologyConnection, 0, len(msg.Connections))
		now := time.Now()

		for _, lc := range msg.Connections {
			if lc.RemoteIp == "" || lc.RemotePort == 0 {
				continue
			}

			// Correlate dest IP to an agent ID if possible.
			destAgentID := ""
			if peer := s.registry.FindAgentByIP(lc.RemoteIp); peer != nil {
				destAgentID = peer.AgentID
			}

			lastObserved := now
			if lc.LastObserved != nil {
				lastObserved = lc.LastObserved.AsTime()
			}

			entries = append(entries, database.TopologyConnection{
				SourceAgentID: msg.AgentId,
				DestAgentID:   destAgentID,
				DestIP:        lc.RemoteIp,
				DestPort:      int(lc.RemotePort),
				Protocol:      lc.Protocol,
				BytesSent:     int64(lc.BytesSent),
				BytesReceived: int64(lc.BytesReceived),
				Retransmits:   int(lc.Retransmits),
				RTTUS:         int(lc.RttUs),
				FirstObserved: lastObserved,
				LastObserved:  lastObserved,
			})
		}

		if err := s.database.UpsertTopologyConnections(ctx, entries); err != nil {
			s.logger.Error().
				Err(err).
				Str("agent_id", msg.AgentId).
				Msg("Failed to upsert topology connections")
			// Continue processing subsequent batches rather than aborting the stream.
			continue
		}

		totalBatches++
		totalEntries += len(entries)
	}

	if err := stream.Err(); err != nil {
		s.logger.Warn().
			Err(err).
			Int("batches", totalBatches).
			Msg("ReportConnections stream closed with error")
		return nil, fmt.Errorf("stream error: %w", err)
	}

	s.logger.Debug().
		Int("batches", totalBatches).
		Int("entries", totalEntries).
		Msg("ReportConnections stream completed")

	return connect.NewResponse(&colonyv1.ReportConnectionsResponse{}), nil
}

// determineColonyStatus calculates overall colony status based on agent health.
// determineColonyStatus calculates overall colony status.
// Since agents are now decoupled from colony status, this simply returns "running"
// as long as the server itself is operational.
func (s *Server) determineColonyStatus() string {
	return "running"
}

// Note: QueryTelemetry (RFD 025) and QueryEbpfMetrics (RFD 035) were removed.
// Use the unified query interface (RFD 067) instead:
// - QueryUnifiedSummary for service health overview
// - QueryUnifiedTraces for distributed traces
// - QueryUnifiedMetrics for HTTP/gRPC/SQL metrics
// - QueryUnifiedLogs for application logs

// CallTool is no longer supported on the colony server (RFD 100).
// Tool dispatch is handled locally by the coral_cli proxy layer.
func (s *Server) CallTool(
	_ context.Context,
	req *connect.Request[colonyv1.CallToolRequest],
) (*connect.Response[colonyv1.CallToolResponse], error) {
	return connect.NewResponse(&colonyv1.CallToolResponse{
		Error:   "tool dispatch has moved to the proxy layer (RFD 100): use coral colony mcp proxy",
		Success: false,
	}), nil
}

// StreamTool executes an MCP tool with streaming (bidirectional) (RFD 004).
// This is for future streaming tools support.
func (s *Server) StreamTool(
	ctx context.Context,
	stream *connect.BidiStream[colonyv1.StreamToolRequest, colonyv1.StreamToolResponse],
) error {
	// For now, streaming is not implemented.
	// Future enhancement: support streaming tools that can return incremental results.
	return fmt.Errorf("streaming tools not yet implemented")
}

// ListTools is no longer supported on the colony server (RFD 100).
// Tool listing is handled locally by the coral_cli proxy layer.
func (s *Server) ListTools(
	_ context.Context,
	_ *connect.Request[colonyv1.ListToolsRequest],
) (*connect.Response[colonyv1.ListToolsResponse], error) {
	return connect.NewResponse(&colonyv1.ListToolsResponse{Tools: nil}), nil
}

// RequestCertificate handles certificate issuance requests (RFD 047).
func (s *Server) RequestCertificate(
	ctx context.Context,
	req *connect.Request[colonyv1.RequestCertificateRequest],
) (*connect.Response[colonyv1.RequestCertificateResponse], error) {
	// Validate request.
	if req.Msg.Jwt == "" {
		s.logger.Warn().Msg("Certificate request rejected: jwt is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("jwt is required"))
	}
	if len(req.Msg.Csr) == 0 {
		s.logger.Warn().Msg("Certificate request rejected: csr is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("csr is required"))
	}

	s.logger.Info().
		Int("csr_size", len(req.Msg.Csr)).
		Msg("Certificate request received")

	// Validate referral ticket (stateless).
	claims, err := s.caManager.ValidateReferralTicket(req.Msg.Jwt)
	if err != nil {
		s.logger.Warn().
			Err(err).
			Msg("Invalid referral ticket")
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("invalid referral ticket: %w", err))
	}

	// Verify colony match.
	if claims.ColonyID != s.config.ColonyID {
		s.logger.Warn().
			Str("ticket_colony_id", claims.ColonyID).
			Str("server_colony_id", s.config.ColonyID).
			Msg("Colony ID mismatch")
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("colony ID mismatch"))
	}

	// Validate Bootstrap PSK for non-renewal requests (RFD 088).
	if claims.Intent != "renew" {
		if req.Msg.BootstrapPsk == "" {
			s.logger.Warn().
				Str("agent_id", claims.AgentID).
				Msg("Certificate request rejected: bootstrap PSK required")
			return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("bootstrap PSK is required"))
		}
		if err := s.caManager.ValidateBootstrapPSK(ctx, req.Msg.BootstrapPsk); err != nil {
			s.logger.Warn().
				Err(err).
				Str("agent_id", claims.AgentID).
				Msg("Certificate request rejected: invalid bootstrap PSK")
			return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("invalid bootstrap PSK"))
		}
	}

	// Issue certificate.
	certPEM, caChain, expiresAt, err := s.caManager.IssueCertificate(claims.AgentID, claims.ColonyID, req.Msg.Csr)
	if err != nil {
		s.logger.Error().
			Err(err).
			Str("agent_id", claims.AgentID).
			Msg("Failed to issue certificate")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to issue certificate: %w", err))
	}

	resp := &colonyv1.RequestCertificateResponse{
		Certificate: certPEM,
		CaChain:     caChain,
		ExpiresAt:   expiresAt.Unix(),
	}

	authMethod := "psk"
	if claims.Intent == "renew" {
		authMethod = "mtls"
	}
	s.logger.Info().
		Str("agent_id", claims.AgentID).
		Str("auth_method", authMethod).
		Time("expires_at", expiresAt).
		Msg("Certificate issued successfully")

	return connect.NewResponse(resp), nil
}

// RevokeCertificate handles certificate revocation requests (RFD 047).
func (s *Server) RevokeCertificate(
	ctx context.Context,
	req *connect.Request[colonyv1.RevokeCertificateRequest],
) (*connect.Response[colonyv1.RevokeCertificateResponse], error) {
	// Validate request.
	if req.Msg.SerialNumber == "" {
		s.logger.Warn().Msg("Revocation rejected: serial_number is required")
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("serial_number is required"))
	}

	s.logger.Info().
		Str("serial_number", req.Msg.SerialNumber).
		Str("reason", req.Msg.Reason).
		Msg("Certificate revocation request received")

	// TODO: Add authentication check to ensure only authorized entities can revoke.
	// For now, we'll accept all revocation requests (this should be restricted in production).

	// Revoke certificate.
	err := s.caManager.RevokeCertificate(req.Msg.SerialNumber, req.Msg.Reason, "admin")
	if err != nil {
		s.logger.Error().
			Err(err).
			Str("serial_number", req.Msg.SerialNumber).
			Msg("Failed to revoke certificate")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to revoke certificate: %w", err))
	}

	resp := &colonyv1.RevokeCertificateResponse{
		Success: true,
	}

	s.logger.Info().
		Str("serial_number", req.Msg.SerialNumber).
		Msg("Certificate revoked successfully")

	return connect.NewResponse(resp), nil
}

// GetCAStatus handles CA status requests (RFD 047).
func (s *Server) GetCAStatus(
	ctx context.Context,
	req *connect.Request[colonyv1.GetCAStatusRequest],
) (*connect.Response[colonyv1.GetCAStatusResponse], error) {
	if s.caManager == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("CA is not initialized"))
	}

	// Get status from manager.
	status := s.caManager.GetStatus()

	// Query stats from database.
	var totalCerts, activeCerts, revokedCerts int32
	err := s.database.DB().QueryRowContext(ctx, `
		SELECT
			COUNT(*) as total,
			SUM(CASE WHEN status = 'active' THEN 1 ELSE 0 END) as active,
			SUM(CASE WHEN status = 'revoked' THEN 1 ELSE 0 END) as revoked
		FROM issued_certificates
	`).Scan(&totalCerts, &activeCerts, &revokedCerts)
	if err != nil {
		// Table might not exist yet, treat as 0.
		totalCerts, activeCerts, revokedCerts = 0, 0, 0
	}

	resp := &colonyv1.GetCAStatusResponse{
		RootCa: &colonyv1.GetCAStatusResponse_CertStatus{
			Path:        status.RootCA.Path,
			Fingerprint: status.RootCA.Fingerprint,
			ExpiresAt:   timestamppb.New(status.RootCA.ExpiresAt),
		},
		ServerIntermediate: &colonyv1.GetCAStatusResponse_CertStatus{
			Path:      status.ServerIntermediate.Path,
			ExpiresAt: timestamppb.New(status.ServerIntermediate.ExpiresAt),
		},
		AgentIntermediate: &colonyv1.GetCAStatusResponse_CertStatus{
			Path:      status.AgentIntermediate.Path,
			ExpiresAt: timestamppb.New(status.AgentIntermediate.ExpiresAt),
		},
		PolicySigning: &colonyv1.GetCAStatusResponse_CertStatus{
			Path:      status.PolicySigning.Path,
			ExpiresAt: timestamppb.New(status.PolicySigning.ExpiresAt),
		},
		ColonySpiffeId: status.ColonySPIFFEID,
		Statistics: &colonyv1.GetCAStatusResponse_Stats{
			TotalIssued: totalCerts,
			Active:      activeCerts,
			Revoked:     revokedCerts,
		},
	}

	return connect.NewResponse(resp), nil
}
