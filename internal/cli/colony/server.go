package colony

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/coral/mesh/v1/meshv1connect"
	"github.com/coral-mesh/coral/internal/auth"
	"github.com/coral-mesh/coral/internal/colony"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/debug"
	"github.com/coral-mesh/coral/internal/colony/httpapi"
	"github.com/coral-mesh/coral/internal/colony/jwks"
	"github.com/coral-mesh/coral/internal/colony/mcp"
	"github.com/coral-mesh/coral/internal/colony/mesh"
	"github.com/coral-mesh/coral/internal/colony/registry"
	"github.com/coral-mesh/coral/internal/colony/server"
	colonywg "github.com/coral-mesh/coral/internal/colony/wireguard"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/constants"
	discoveryclient "github.com/coral-mesh/coral/internal/discovery/client"
	"github.com/coral-mesh/coral/internal/duckdb"
	"github.com/coral-mesh/coral/internal/logging"
	"github.com/coral-mesh/coral/internal/wireguard"
)

// startServers starts the HTTP/Connect servers for agent registration and colony management.
func startServers(cfg *config.ResolvedConfig, wgDevice *wireguard.Device, agentRegistry *registry.Registry, db *database.Database, endpoints []string, logger logging.Logger) (*http.Server, error) {
	ctx := context.Background()
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
		connectPort = constants.DefaultColonyPort // Default Buf Connect port
	}

	dashboardPort := colonyConfig.Services.DashboardPort
	if dashboardPort == 0 {
		dashboardPort = constants.DefaultDashboardPort
	}

	// Create discovery client for agent endpoint lookup
	var discoveryClient *discoveryclient.Client
	if globalConfig.Discovery.Endpoint != "" {
		discoveryClient = discoveryclient.New(globalConfig.Discovery.Endpoint)
		logger.Debug().
			Str("discovery_endpoint", globalConfig.Discovery.Endpoint).
			Msg("Discovery client configured for agent endpoint lookup")
	}

	// Create mesh service handler
	meshSvc := mesh.NewHandler(cfg, wgDevice, agentRegistry, discoveryClient, logger)

	// Initialize CA manager (RFD 047 - Colony CA Infrastructure).
	// Use CA from colony config directory (generated during init).
	// RFD 049: Use JWKS client for referral ticket validation.
	jwksClient := jwks.NewClient(cfg.DiscoveryURL, logger.With().Str("component", "jwks-client").Logger())
	caDir := filepath.Join(loader.ColonyDir(cfg.ColonyID), "ca")
	caManager, err := colony.InitializeCA(db.DB(), cfg.ColonyID, caDir, jwksClient, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize CA manager: %w", err)
	}

	// Compute public endpoint URL if enabled (RFD 031).
	var publicEndpointURL string
	if colonyConfig.PublicEndpoint.Enabled {
		host := colonyConfig.PublicEndpoint.Host
		if host == "" {
			host = constants.DefaultPublicEndpointHost
		}
		port := colonyConfig.PublicEndpoint.Port
		if port == 0 {
			port = constants.DefaultPublicEndpointPort
		}
		scheme := "http"
		isLocalhost := host == "127.0.0.1" || host == "localhost" || host == "::1"
		if !isLocalhost || colonyConfig.PublicEndpoint.TLS.CertFile != "" {
			scheme = "https"
		}
		publicEndpointURL = fmt.Sprintf("%s://%s:%d", scheme, host, port)
	}

	// Create colony service handler.
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
		PublicEndpointURL:  publicEndpointURL,
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
	var mcpServer *mcp.Server
	if !colonyConfig.MCP.Disabled {
		// Resolve profiling enrichment config (RFD 074).
		profilingEnrichmentEnabled := true
		if colonyConfig.ContinuousProfiling.EnableSummaryEnrichment != nil {
			profilingEnrichmentEnabled = *colonyConfig.ContinuousProfiling.EnableSummaryEnrichment
		}
		profilingTopK := 5
		if colonyConfig.ContinuousProfiling.TopKHotspots > 0 {
			profilingTopK = colonyConfig.ContinuousProfiling.TopKHotspots
		}

		mcpConfig := mcp.Config{
			ColonyID:                   cfg.ColonyID,
			ApplicationName:            cfg.ApplicationName,
			Environment:                cfg.Environment,
			Disabled:                   colonyConfig.MCP.Disabled,
			EnabledTools:               colonyConfig.MCP.EnabledTools,
			RequireRBACForActions:      colonyConfig.MCP.Security.RequireRBACForActions,
			AuditEnabled:               colonyConfig.MCP.Security.AuditEnabled,
			ProfilingEnrichmentEnabled: profilingEnrichmentEnabled,
			ProfilingTopKHotspots:      profilingTopK,
		}

		var err error
		mcpServer, err = mcp.New(
			agentRegistry,
			db,
			debugOrchestrator,
			mcpConfig,
			logger.With().Str("component", "mcp-server").Logger(),
		)
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

		functionPoller := colony.NewFunctionPoller(ctx, colony.FunctionPollerConfig{
			Registry:         agentRegistry,
			FunctionRegistry: functionReg,
			PollInterval:     pollInterval,
			Logger:           logger,
		})
		if err := functionPoller.Start(); err != nil {
			logger.Warn().Err(err).Msg("Failed to start function discovery poller")
		} else {
			logger.Info().
				Dur("poll_interval", pollInterval).
				Msg("Function discovery poller started")
		}
	} else {
		logger.Info().Msg("Function discovery is disabled in configuration")
	}

	// Register the handlers
	meshPath, meshHandler := meshv1connect.NewMeshServiceHandler(meshSvc)
	colonyPath, colonyHandler := colonyv1connect.NewColonyServiceHandler(colonySvc)
	debugPath, debugHandler := colonyv1connect.NewColonyDebugServiceHandler(debugOrchestrator)

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

	// Create status handler (reused for public endpoint).
	statusHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Call the colony service's internal GetStatusResponse method directly.
		// This avoids the Connect protocol overhead and potential auth middleware issues.
		resp := colonySvc.GetStatusResponse()

		// Gather mesh network information for debugging.
		meshInfo := colonywg.GatherMeshInfo(wgDevice, cfg.WireGuard.MeshIPv4, cfg.WireGuard.MeshNetworkIPv4, cfg.ColonyID, logger)

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

	// Add simple HTTP /status endpoint (similar to agent).
	mux.Handle("/status", statusHandler)

	addr := fmt.Sprintf(":%d", connectPort)
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
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

	// Start public endpoint server if enabled (RFD 031).
	if colonyConfig.PublicEndpoint.Enabled {
		// Initialize token store with tokens from colony config directory.
		colonyDir := loader.ColonyDir(cfg.ColonyID)
		tokensFile := colonyConfig.PublicEndpoint.Auth.TokensFile
		if tokensFile == "" {
			tokensFile = filepath.Join(colonyDir, "tokens.yaml")
		}
		tokenStore := auth.NewTokenStore(tokensFile)

		// Issue server certificate from internal CA if no cert provided.
		var tlsCert *tls.Certificate
		if colonyConfig.PublicEndpoint.TLS.CertFile == "" && caManager != nil {
			// Include local and identity-based DNS names.
			dnsNames := []string{"localhost", cfg.ColonyID}
			if colonyConfig.PublicEndpoint.Host != "" && colonyConfig.PublicEndpoint.Host != "0.0.0.0" {
				dnsNames = append(dnsNames, colonyConfig.PublicEndpoint.Host)
			}

			certPEM, keyPEM, err := caManager.IssueServerCertificate(dnsNames)
			if err != nil {
				logger.Warn().Err(err).Msg("Failed to issue server certificate from CA")
			} else {
				// Build full certificate chain: leaf + server intermediate + root CA.
				// This allows agents to validate the CA fingerprint during bootstrap.
				serverIntPEM := caManager.GetServerIntermediateCertPEM()
				rootCAPEM := caManager.GetRootCertPEM()
				fullChainPEM := append(certPEM, serverIntPEM...)
				fullChainPEM = append(fullChainPEM, rootCAPEM...)

				cert, err := tls.X509KeyPair(fullChainPEM, keyPEM)
				if err != nil {
					logger.Warn().Err(err).Msg("Failed to load issued server certificate")
				} else {
					tlsCert = &cert
					logger.Info().Msg("Issued public endpoint server certificate from colony CA (RFD 047)")
				}
			}
		}

		// Create public endpoint server.
		publicConfig := httpapi.Config{
			PublicConfig:   colonyConfig.PublicEndpoint,
			ColonyPath:     colonyPath,
			ColonyHandler:  colonyHandler,
			DebugPath:      debugPath,
			DebugHandler:   debugHandler,
			MCPServer:      mcpServer,
			TokenStore:     tokenStore,
			ColonyDir:      colonyDir,
			TLSCertificate: tlsCert,
			StatusHandler:  statusHandler,
			Logger:         logger.With().Str("component", "public-endpoint").Logger(),
		}

		publicServer, err := httpapi.New(publicConfig)
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to create public endpoint server, continuing without it")
		} else {
			if err := publicServer.Start(); err != nil {
				logger.Error().Err(err).Msg("Failed to start public endpoint server")
			} else {
				logger.Info().
					Str("url", publicServer.URL()).
					Bool("mcp_enabled", colonyConfig.PublicEndpoint.MCP.Enabled).
					Msg("Public endpoint server started")
			}
		}
	}

	return httpServer, nil
}
