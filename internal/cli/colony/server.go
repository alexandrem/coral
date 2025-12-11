package colony

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	discoverypb "github.com/coral-mesh/coral/coral/discovery/v1"
	"github.com/coral-mesh/coral/coral/discovery/v1/discoveryv1connect"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/coral/mesh/v1/meshv1connect"
	"github.com/coral-mesh/coral/internal/colony"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/debug"
	"github.com/coral-mesh/coral/internal/colony/mcp"
	"github.com/coral-mesh/coral/internal/colony/registry"
	"github.com/coral-mesh/coral/internal/colony/server"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/constants"
	"github.com/coral-mesh/coral/internal/duckdb"
	"github.com/coral-mesh/coral/internal/logging"
	"github.com/coral-mesh/coral/internal/wireguard"
)

// startServers starts the HTTP/Connect servers for agent registration and colony management.
func startServers(cfg *config.ResolvedConfig, wgDevice *wireguard.Device, agentRegistry *registry.Registry, db *database.Database, endpoints []string, logger logging.Logger) (*http.Server, error) {
	// Get connect port from config or use default
	loader, err := config.NewLoader()
	if err != nil {
		return nil, fmt.Errorf("failed to create config loader: %w", err)
	}

	colonyConfig, err := loader.LoadColonyConfig(cfg.ColonyID)
	if err != nil {
		return nil, fmt.Errorf("failed to load colony config: %w", err)
	}

	globalConfig, err := loader.LoadGlobalConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load global config: %w", err)
	}

	connectPort := colonyConfig.Services.ConnectPort
	if connectPort == 0 {
		connectPort = 9000 // Default Buf Connect port
	}

	dashboardPort := colonyConfig.Services.DashboardPort
	if dashboardPort == 0 {
		dashboardPort = constants.DefaultDashboardPort
	}

	// Create discovery client for agent endpoint lookup
	var discoveryClient discoveryv1connect.DiscoveryServiceClient
	if globalConfig.Discovery.Endpoint != "" {
		discoveryClient = discoveryv1connect.NewDiscoveryServiceClient(
			http.DefaultClient,
			globalConfig.Discovery.Endpoint,
		)
		logger.Debug().
			Str("discovery_endpoint", globalConfig.Discovery.Endpoint).
			Msg("Discovery client configured for agent endpoint lookup")
	}

	// Create mesh service handler
	meshSvc := &meshServiceHandler{
		cfg:             cfg,
		wgDevice:        wgDevice,
		registry:        agentRegistry,
		logger:          logger,
		discoveryClient: discoveryClient,
	}

	// Initialize CA manager (RFD 047 - Colony CA Infrastructure).
	// Use CA from colony config directory (generated during init).
	jwtSigningKey := []byte(cfg.ColonySecret) // Use colony secret as JWT signing key for now.
	caDir := filepath.Join(loader.ColonyDir(cfg.ColonyID), "ca")
	caManager, err := colony.InitializeCA(db.DB(), cfg.ColonyID, caDir, jwtSigningKey)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize CA manager: %w", err)
	}

	// Create colony service handler
	colonyServerConfig := server.Config{
		ColonyID:           cfg.ColonyID,
		ApplicationName:    cfg.ApplicationName,
		Environment:        cfg.Environment,
		DashboardPort:      dashboardPort,
		StoragePath:        cfg.StoragePath,
		WireGuardPort:      cfg.WireGuard.Port,
		WireGuardPublicKey: cfg.WireGuard.PublicKey,
		WireGuardEndpoints: endpoints,
		ConnectPort:        connectPort,
		MeshIPv4:           cfg.WireGuard.MeshIPv4,
		MeshIPv6:           cfg.WireGuard.MeshIPv6,
	}
	colonySvc := server.New(agentRegistry, db, caManager, colonyServerConfig, logger.With().Str("component", "colony-server").Logger())

	// Load colony config early (needed by function registry, MCP, and other components).
	mcpLoader, mcpErr := config.NewLoader()
	if mcpErr != nil {
		return nil, fmt.Errorf("failed to create config loader: %w", mcpErr)
	}
	colonyConfig, mcpErr = mcpLoader.LoadColonyConfig(cfg.ColonyID)
	if mcpErr != nil {
		return nil, fmt.Errorf("failed to load colony config: %w", mcpErr)
	}

	// Initialize function registry early (needed by debug orchestrator).
	var functionReg *colony.FunctionRegistry
	if !colonyConfig.FunctionRegistry.Disabled {
		functionReg = colony.NewFunctionRegistry(db, logger)
	}

	// Initialize Debug Orchestrator (RFD 059 - Live Debugging, RFD 069 - Function Discovery).
	debugOrchestrator := debug.NewOrchestrator(logger, agentRegistry, db, functionReg)

	// Create MCP server if not disabled.
	if !colonyConfig.MCP.Disabled {
		mcpConfig := mcp.Config{
			ColonyID:              cfg.ColonyID,
			ApplicationName:       cfg.ApplicationName,
			Environment:           cfg.Environment,
			Disabled:              colonyConfig.MCP.Disabled,
			EnabledTools:          colonyConfig.MCP.EnabledTools,
			RequireRBACForActions: colonyConfig.MCP.Security.RequireRBACForActions,
			AuditEnabled:          colonyConfig.MCP.Security.AuditEnabled,
		}

		mcpServer, err := mcp.New(agentRegistry, db, debugOrchestrator, mcpConfig, logger.With().Str("component", "mcp-server").Logger())
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to initialize MCP server, continuing without MCP support")
		} else {
			colonySvc.SetMCPServer(mcpServer)
			logger.Info().
				Int("tool_count", len(mcpServer.ListToolNames())).
				Msg("MCP server initialized and attached to colony")

			// Log all registered MCP tools.
			toolNames := mcpServer.ListToolNames()
			if len(toolNames) > 0 {
				logger.Info().
					Strs("tools", toolNames).
					Msg("Registered MCP tools")
			}
		}
	} else {
		logger.Info().Msg("MCP server is disabled in configuration")
	}

	// Initialize eBPF query service (RFD 035).
	ebpfService := colony.NewEbpfQueryService(db)
	colonySvc.SetEbpfService(ebpfService)
	logger.Info().Msg("eBPF query service initialized and attached to colony")

	// Start function registry poller if registry was created (RFD 063).
	if functionReg != nil {
		// Configure poll interval from config.
		pollInterval := constants.DefaultColonyFunctionsPollInterval
		if colonyConfig.FunctionRegistry.PollInterval > 0 {
			pollInterval = time.Duration(colonyConfig.FunctionRegistry.PollInterval) * time.Second
		}

		functionPoller := colony.NewFunctionPoller(colony.FunctionPollerConfig{
			Registry:         agentRegistry,
			FunctionRegistry: functionReg,
			PollInterval:     pollInterval,
			Logger:           logger,
		})
		functionPoller.Start()
		logger.Info().
			Dur("poll_interval", pollInterval).
			Msg("Function discovery poller started")
	} else {
		logger.Info().Msg("Function discovery is disabled in configuration")
	}

	// Register the handlers
	meshPath, meshHandler := meshv1connect.NewMeshServiceHandler(meshSvc)
	colonyPath, colonyHandler := colonyv1connect.NewColonyServiceHandler(colonySvc)
	debugPath, debugHandler := colonyv1connect.NewDebugServiceHandler(debugOrchestrator)

	// Create HTTP server
	mux := http.NewServeMux()
	mux.Handle(meshPath, meshHandler)
	mux.Handle(colonyPath, colonyHandler)
	mux.Handle(debugPath, debugHandler)

	// Add DuckDB HTTP handler for remote query (RFD 046).
	duckdbHandler := duckdb.NewDuckDBHandler(logger.With().Str("component", "duckdb-handler").Logger())
	if err := duckdbHandler.RegisterDatabase(filepath.Base(db.Path()), db.Path()); err != nil {
		logger.Warn().Err(err).Msg("Failed to register colony database for HTTP serving")
	} else {
		mux.Handle("/duckdb/", duckdbHandler)
		logger.Info().
			Str("path", db.Path()).
			Str("db_name", filepath.Base(db.Path())).
			Msg("Colony database registered for remote query")
	}

	// Add simple HTTP /status endpoint (similar to agent).
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		// Call the colony service's internal GetStatusResponse method directly.
		// This avoids the Connect protocol overhead and potential auth middleware issues.
		resp := colonySvc.GetStatusResponse()

		// Gather mesh network information for debugging.
		meshInfo := gatherColonyMeshInfo(wgDevice, cfg.WireGuard.MeshIPv4, cfg.WireGuard.MeshNetworkIPv4, cfg.ColonyID, logger)

		// Gather platform information.
		platformInfo := gatherPlatformInfo()

		// Group related fields for better organization.
		status := map[string]interface{}{
			"colony": map[string]interface{}{
				"id":          resp.ColonyId,
				"app_name":    resp.AppName,
				"environment": resp.Environment,
			},
			"runtime": map[string]interface{}{
				"status":         resp.Status,
				"started_at":     resp.StartedAt.AsTime(),
				"uptime_seconds": resp.UptimeSeconds,
				"agent_count":    resp.AgentCount,
				"storage_bytes":  resp.StorageBytes,
				"dashboard_url":  resp.DashboardUrl,
				"platform":       platformInfo,
			},
			"network": map[string]interface{}{
				"wireguard_port":       resp.WireguardPort,
				"wireguard_public_key": resp.WireguardPublicKey,
				"wireguard_endpoints":  resp.WireguardEndpoints,
				"connect_port":         resp.ConnectPort,
				"mesh_ipv4":            resp.MeshIpv4,
				"mesh_ipv6":            resp.MeshIpv6,
			},
			"mesh": meshInfo,
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(status); err != nil {
			logger.Error().Err(err).Msg("Failed to encode status response")
		}
	})

	addr := fmt.Sprintf(":%d", connectPort)
	httpServer := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// Start server in background
	go func() {
		logger.Info().
			Int("port", connectPort).
			Str("status_endpoint", fmt.Sprintf("http://localhost:%d/status", connectPort)).
			Msg("Mesh and Colony services listening")

		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error().
				Err(err).
				Msg("Server error")
		}
	}()

	return httpServer, nil
}

// meshServiceHandler implements the MeshService RPC handler.
type meshServiceHandler struct {
	cfg             *config.ResolvedConfig
	wgDevice        *wireguard.Device
	registry        *registry.Registry
	logger          logging.Logger
	discoveryClient discoveryv1connect.DiscoveryServiceClient
}

// Register handles agent registration requests.
func (h *meshServiceHandler) Register(
	ctx context.Context,
	req *connect.Request[meshv1.RegisterRequest],
) (*connect.Response[meshv1.RegisterResponse], error) {
	// Extract peer address from request headers for WireGuard endpoint detection.
	var peerAddr string
	if req.Peer().Addr != "" {
		peerAddr = req.Peer().Addr
	}

	h.logger.Info().
		Str("agent_id", req.Msg.AgentId).
		Str("component_name", req.Msg.ComponentName). //nolint:staticcheck // ComponentName is deprecated but kept for backward compatibility
		Str("peer_addr", peerAddr).
		Msg("Agent registration request received")

	// Validate colony_id and colony_secret (RFD 002)
	if req.Msg.ColonyId != h.cfg.ColonyID {
		h.logger.Warn().
			Str("agent_id", req.Msg.AgentId).
			Str("expected_colony_id", h.cfg.ColonyID).
			Str("received_colony_id", req.Msg.ColonyId).
			Msg("Agent registration rejected: wrong colony ID")

		return connect.NewResponse(&meshv1.RegisterResponse{
			Accepted: false,
			Reason:   "wrong_colony",
		}), nil
	}

	if req.Msg.ColonySecret != h.cfg.ColonySecret {
		h.logger.Warn().
			Str("agent_id", req.Msg.AgentId).
			Msg("Agent registration rejected: invalid colony secret")

		return connect.NewResponse(&meshv1.RegisterResponse{
			Accepted: false,
			Reason:   "invalid_secret",
		}), nil
	}

	// Validate WireGuard public key
	if req.Msg.WireguardPubkey == "" {
		h.logger.Warn().
			Str("agent_id", req.Msg.AgentId).
			Msg("Agent registration rejected: missing WireGuard public key")

		return connect.NewResponse(&meshv1.RegisterResponse{
			Accepted: false,
			Reason:   "missing_wireguard_pubkey",
		}), nil
	}

	// Allocate mesh IP for the agent
	allocator := h.wgDevice.Allocator()
	meshIP, err := allocator.Allocate(req.Msg.AgentId)
	if err != nil {
		h.logger.Error().
			Err(err).
			Str("agent_id", req.Msg.AgentId).
			Msg("Failed to allocate mesh IP")

		return connect.NewResponse(&meshv1.RegisterResponse{
			Accepted: false,
			Reason:   "ip_allocation_failed",
		}), nil
	}

	h.logger.Info().
		Str("agent_id", req.Msg.AgentId).
		Str("mesh_ip", meshIP.String()).
		Msg("Allocated mesh IP for agent")

	// Get agent's public endpoint from discovery service.
	// The agent registers its STUN-discovered public endpoint with discovery,
	// which we need for NAT traversal.
	var agentEndpoint string

	// Query discovery service for agent's observed endpoint
	if h.discoveryClient != nil {
		agentInfo, err := h.discoveryClient.LookupAgent(ctx, connect.NewRequest(&discoverypb.LookupAgentRequest{
			AgentId: req.Msg.AgentId,
		}))

		if err == nil && agentInfo.Msg != nil && len(agentInfo.Msg.ObservedEndpoints) > 0 {
			// Extract the peer's source IP from the HTTP connection to help select the right endpoint.
			var peerHost string
			if peerAddr != "" {
				if host, _, err := net.SplitHostPort(peerAddr); err == nil {
					peerHost = host
				}
			}

			// Select the best observed endpoint from the list.
			selectedEp, matchType := selectBestAgentEndpoint(agentInfo.Msg.ObservedEndpoints, peerHost, h.logger, req.Msg.AgentId)

			// Build endpoint string and log selection.
			if selectedEp != nil {
				agentEndpoint = net.JoinHostPort(selectedEp.Ip, fmt.Sprintf("%d", selectedEp.Port))
				if matchType == "matching" {
					h.logger.Info().
						Str("agent_id", req.Msg.AgentId).
						Str("endpoint", agentEndpoint).
						Str("peer_host", peerHost).
						Msg("Using agent's endpoint matching connection source")
				} else {
					h.logger.Info().
						Str("agent_id", req.Msg.AgentId).
						Str("endpoint", agentEndpoint).
						Msg("Using agent's observed endpoint from discovery service")
				}
			} else {
				h.logger.Warn().
					Str("agent_id", req.Msg.AgentId).
					Msg("All observed endpoints were localhost - agent may not be reachable via WireGuard")
			}
		} else {
			h.logger.Debug().
				Err(err).
				Str("agent_id", req.Msg.AgentId).
				Msg("Could not get agent endpoint from discovery service")
		}
	}

	// Fallback: extract agent's source address from HTTP connection.
	// This works for same-host testing but not for NAT traversal.
	// Note: peerAddr includes the HTTP port, not the WireGuard port.
	if agentEndpoint == "" && peerAddr != "" {
		if host, _, err := net.SplitHostPort(peerAddr); err == nil {
			// Use a default WireGuard port (this is just a guess and likely wrong for agents)
			// WireGuard will learn the correct endpoint from incoming packets
			h.logger.Debug().
				Str("agent_id", req.Msg.AgentId).
				Str("peer_addr", peerAddr).
				Msg("No discovery endpoint available, WireGuard will learn endpoint from incoming packets")
			_ = host
		}
	}

	// Add agent as WireGuard peer
	peerConfig := &wireguard.PeerConfig{
		PublicKey:           req.Msg.WireguardPubkey,
		Endpoint:            agentEndpoint, // Use detected endpoint
		AllowedIPs:          []string{meshIP.String() + "/32"},
		PersistentKeepalive: 25, // Keep NAT mappings alive
	}

	h.logger.Info().
		Str("agent_id", req.Msg.AgentId).
		Str("endpoint", agentEndpoint).
		Str("pubkey", req.Msg.WireguardPubkey[:8]+"...").
		Msg("Adding agent as WireGuard peer")

	if err := h.wgDevice.AddPeer(peerConfig); err != nil {
		h.logger.Error().
			Err(err).
			Str("agent_id", req.Msg.AgentId).
			Msg("Failed to add agent as WireGuard peer")

		// Release the allocated IP since we couldn't add the peer
		_ = allocator.Release(meshIP) // TODO: errcheck

		return connect.NewResponse(&meshv1.RegisterResponse{
			Accepted: false,
			Reason:   "peer_add_failed",
		}), nil
	}

	// Register agent in the registry for tracking.
	// Note: We don't have IPv6 mesh IP yet (future enhancement).
	//nolint:staticcheck // ComponentName is deprecated but kept for backward compatibility
	if _, err := h.registry.Register(req.Msg.AgentId, req.Msg.ComponentName, meshIP.String(), "", req.Msg.Services, req.Msg.RuntimeContext, req.Msg.ProtocolVersion); err != nil {
		h.logger.Warn().
			Err(err).
			Str("agent_id", req.Msg.AgentId).
			Msg("Failed to register agent in registry (non-fatal)")
	}

	// Log registration with service details
	logEvent := h.logger.Info().
		Str("agent_id", req.Msg.AgentId).
		Str("component_name", req.Msg.ComponentName). //nolint:staticcheck // ComponentName is deprecated but kept for backward compatibility
		Str("mesh_ip", meshIP.String())

	if len(req.Msg.Services) > 0 {
		logEvent.Int("service_count", len(req.Msg.Services))
	}

	logEvent.Msg("Agent registered successfully")

	// Build list of existing peers (excluding this agent)
	peers := []*meshv1.PeerInfo{}
	for _, peer := range h.wgDevice.ListPeers() {
		if peer.PublicKey != req.Msg.WireguardPubkey {
			// Get the IP from allowed IPs
			if len(peer.AllowedIPs) > 0 {
				peers = append(peers, &meshv1.PeerInfo{
					WireguardPubkey: peer.PublicKey,
					MeshIp:          peer.AllowedIPs[0],
				})
			}
		}
	}

	// Return successful registration response
	return connect.NewResponse(&meshv1.RegisterResponse{
		Accepted:     true,
		AssignedIp:   meshIP.String(),
		MeshSubnet:   h.cfg.WireGuard.MeshNetworkIPv4,
		Peers:        peers,
		RegisteredAt: timestamppb.Now(),
	}), nil
}

// Heartbeat handles agent heartbeat requests to update last_seen timestamp.
func (h *meshServiceHandler) Heartbeat(
	ctx context.Context,
	req *connect.Request[meshv1.HeartbeatRequest],
) (*connect.Response[meshv1.HeartbeatResponse], error) {
	h.logger.Debug().
		Str("agent_id", req.Msg.AgentId).
		Str("status", req.Msg.Status).
		Msg("Agent heartbeat received")

	// Validate agent_id
	if req.Msg.AgentId == "" {
		h.logger.Warn().Msg("Heartbeat rejected: missing agent_id")
		return connect.NewResponse(&meshv1.HeartbeatResponse{
			Ok: false,
		}), nil
	}

	// Update heartbeat in registry
	if err := h.registry.UpdateHeartbeat(req.Msg.AgentId); err != nil {
		h.logger.Warn().
			Err(err).
			Str("agent_id", req.Msg.AgentId).
			Msg("Failed to update agent heartbeat")
		return connect.NewResponse(&meshv1.HeartbeatResponse{
			Ok: false,
		}), nil
	}

	h.logger.Debug().
		Str("agent_id", req.Msg.AgentId).
		Msg("Agent heartbeat updated successfully")

	return connect.NewResponse(&meshv1.HeartbeatResponse{
		Ok:       true,
		Commands: []string{}, // Future: colony can send commands to agent
	}), nil
}
