package server

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/coral/agent/v1/agentv1connect"
	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/colony/ca"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/registry"
	"github.com/coral-mesh/coral/internal/colony/storage"
)

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
	registry    *registry.Registry
	database    *database.Database
	caManager   *ca.Manager // RFD 047 - certificate authority manager.
	mcpServer   interface{} // *mcp.Server - using interface to avoid import cycle
	ebpfService interface{} // EbpfQueryService - using interface to avoid import cycle
	config      Config
	startTime   time.Time
	logger      zerolog.Logger
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
				// Agent listens on port 9001 for mesh traffic (see internal/cli/agent/start.go).
				agentURL := fmt.Sprintf("http://%s:9001", e.MeshIPv4)
				client := agentv1connect.NewAgentServiceClient(http.DefaultClient, agentURL)

				// Short timeout for real-time query.
				queryCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
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

// GetTopology handles topology request.
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

	// Return empty connections list for now (topology discovery is a future enhancement).
	resp := &colonyv1.GetTopologyResponse{
		ColonyId:    s.config.ColonyID,
		Agents:      agents,
		Connections: []*colonyv1.Connection{},
	}

	s.logger.Debug().
		Int("agent_count", len(agents)).
		Msg("Get topology response prepared")

	return connect.NewResponse(resp), nil
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

// CallTool executes an MCP tool and returns the result (RFD 004).
func (s *Server) CallTool(
	ctx context.Context,
	req *connect.Request[colonyv1.CallToolRequest],
) (*connect.Response[colonyv1.CallToolResponse], error) {
	s.logger.Info().
		Str("tool", req.Msg.ToolName).
		Msg("MCP tool call received via RPC")

	// Execute the tool.
	result, err := s.ExecuteTool(ctx, req.Msg.ToolName, req.Msg.ArgumentsJson)
	if err != nil {
		return connect.NewResponse(&colonyv1.CallToolResponse{
			Result:  "",
			Error:   err.Error(),
			Success: false,
		}), nil
	}

	return connect.NewResponse(&colonyv1.CallToolResponse{
		Result:  result,
		Error:   "",
		Success: true,
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

// ListTools returns the list of available MCP tools (RFD 004).
func (s *Server) ListTools(
	ctx context.Context,
	req *connect.Request[colonyv1.ListToolsRequest],
) (*connect.Response[colonyv1.ListToolsResponse], error) {
	// Get tool metadata including schemas from the MCP server.
	metadata, err := s.GetToolMetadata()
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to get tool metadata")
		// Fallback to simple tool list without schemas.
		toolNames := s.ListToolNames()
		tools := make([]*colonyv1.ToolInfo, 0, len(toolNames))
		for _, name := range toolNames {
			enabled := s.IsToolEnabled(name)
			tools = append(tools, &colonyv1.ToolInfo{
				Name:            name,
				Description:     "",
				Enabled:         enabled,
				InputSchemaJson: "{\"type\": \"object\", \"properties\": {}}",
			})
		}
		return connect.NewResponse(&colonyv1.ListToolsResponse{
			Tools: tools,
		}), nil
	}

	// Convert metadata to ToolInfo proto messages.
	tools := make([]*colonyv1.ToolInfo, 0, len(metadata))
	for _, meta := range metadata {
		tools = append(tools, &colonyv1.ToolInfo{
			Name:            meta.Name,
			Description:     meta.Description,
			Enabled:         true, // Already filtered by GetToolMetadata
			InputSchemaJson: meta.InputSchemaJSON,
		})
	}

	return connect.NewResponse(&colonyv1.ListToolsResponse{
		Tools: tools,
	}), nil
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

	s.logger.Info().
		Str("agent_id", claims.AgentID).
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
