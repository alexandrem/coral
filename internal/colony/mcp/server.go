package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/coral-io/coral/internal/colony/database"
	"github.com/coral-io/coral/internal/colony/registry"
	"github.com/coral-io/coral/internal/logging"
	"github.com/firebase/genkit/go/plugins/mcp"
)

// Server wraps the Genkit MCP server and provides Colony-specific tools.
type Server struct {
	mcpServer   *mcp.MCPServer
	registry    *registry.Registry
	db          *database.Database
	config      Config
	logger      logging.Logger
	startedAt   time.Time
}

// Config contains configuration for the MCP server.
type Config struct {
	// ColonyID is the unique identifier for this colony.
	ColonyID string

	// ApplicationName is the name of the application.
	ApplicationName string

	// Environment is the deployment environment (production, staging, etc).
	Environment string

	// Disabled controls whether the MCP server is enabled.
	Disabled bool

	// EnabledTools optionally restricts which tools are available.
	// If empty, all tools are enabled.
	EnabledTools []string

	// Security settings.
	RequireRBACForActions bool
	AuditEnabled          bool
}

// New creates a new MCP server instance.
func New(registry *registry.Registry, db *database.Database, config Config, logger logging.Logger) (*Server, error) {
	if config.Disabled {
		return nil, fmt.Errorf("MCP server is disabled in configuration")
	}

	logger.Info().
		Str("colony_id", config.ColonyID).
		Bool("audit_enabled", config.AuditEnabled).
		Msg("Initializing MCP server")

	// Create Genkit MCP server.
	mcpServer := mcp.NewMCPServer(mcp.MCPServerOptions{
		Name:    fmt.Sprintf("coral-%s", config.ColonyID),
		Version: "1.0.0",
	})

	s := &Server{
		mcpServer:   mcpServer,
		registry:    registry,
		db:          db,
		config:      config,
		logger:      logger,
		startedAt:   time.Now(),
	}

	// Register all tools.
	if err := s.registerTools(); err != nil {
		return nil, fmt.Errorf("failed to register tools: %w", err)
	}

	logger.Info().
		Int("tool_count", len(s.listToolNames())).
		Msg("MCP server initialized successfully")

	return s, nil
}

// ServeStdio starts the MCP server and serves over stdio.
// This blocks until the context is cancelled or an error occurs.
func (s *Server) ServeStdio(ctx context.Context) error {
	s.logger.Info().Msg("Starting MCP server on stdio")

	// Use Genkit's stdio transport.
	return s.mcpServer.ServeStdio(ctx)
}

// Close stops the MCP server and releases resources.
func (s *Server) Close() error {
	s.logger.Info().Msg("Stopping MCP server")
	return nil
}

// registerTools registers all MCP tools with the server.
func (s *Server) registerTools() error {
	// Register observability query tools.
	s.registerServiceHealthTool()
	s.registerServiceTopologyTool()
	s.registerQueryEventsTool()
	s.registerBeylaHTTPMetricsTool()
	s.registerBeylaGRPCMetricsTool()
	s.registerBeylaSQLMetricsTool()
	s.registerBeylaTracesTool()
	s.registerTraceByIDTool()
	s.registerTelemetrySpansTool()
	s.registerTelemetryMetricsTool()
	s.registerTelemetryLogsTool()

	// TODO: Register live debugging tools (Phase 3).
	// s.registerEBPFCollectorTools()
	// s.registerExecCommandTool()
	// s.registerShellStartTool()

	// TODO: Register analysis tools (Phase 4).
	// s.registerCorrelateEventsTool()
	// s.registerCompareEnvironmentsTool()
	// s.registerDeploymentTimelineTool()

	s.logger.Debug().
		Int("registered_tools", len(s.listToolNames())).
		Msg("Tools registered")

	return nil
}

// listToolNames returns the names of all registered tools.
func (s *Server) listToolNames() []string {
	// This will be populated by Genkit's tool registry.
	// For now, return a placeholder.
	return []string{
		"coral_get_service_health",
		"coral_get_service_topology",
		"coral_query_events",
		"coral_query_beyla_http_metrics",
		"coral_query_beyla_grpc_metrics",
		"coral_query_beyla_sql_metrics",
		"coral_query_beyla_traces",
		"coral_get_trace_by_id",
		"coral_query_telemetry_spans",
		"coral_query_telemetry_metrics",
		"coral_query_telemetry_logs",
	}
}

// isToolEnabled checks if a tool is enabled based on configuration.
func (s *Server) isToolEnabled(toolName string) bool {
	if len(s.config.EnabledTools) == 0 {
		// All tools enabled by default.
		return true
	}

	for _, enabled := range s.config.EnabledTools {
		if enabled == toolName {
			return true
		}
	}
	return false
}

// auditToolCall logs a tool invocation if auditing is enabled.
func (s *Server) auditToolCall(toolName string, args map[string]interface{}) {
	if !s.config.AuditEnabled {
		return
	}

	argsJSON, _ := json.Marshal(args)
	s.logger.Info().
		Str("tool", toolName).
		RawJSON("args", argsJSON).
		Msg("MCP tool called")
}

// writeJSONResponse writes a JSON response to the writer.
func writeJSONResponse(w io.Writer, data interface{}) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(data)
}

// writeTextResponse writes a text response to the writer.
func writeTextResponse(w io.Writer, text string) error {
	_, err := fmt.Fprintln(w, text)
	return err
}

// runInteractive runs the MCP server in interactive mode for testing.
func (s *Server) runInteractive() error {
	s.logger.Info().Msg("Running MCP server in interactive mode")
	fmt.Println("MCP Server Interactive Mode")
	fmt.Println("Type 'help' for available commands")
	fmt.Println()

	// Simple REPL for testing.
	// In production, this is replaced by ServeStdio().
	for {
		fmt.Print("> ")
		var input string
		if _, err := fmt.Scanln(&input); err != nil {
			if err == io.EOF {
				break
			}
			continue
		}

		switch input {
		case "help":
			fmt.Println("Available commands:")
			fmt.Println("  list-tools  - List all available tools")
			fmt.Println("  quit        - Exit interactive mode")
		case "list-tools":
			for _, tool := range s.listToolNames() {
				fmt.Printf("  - %s\n", tool)
			}
		case "quit":
			return nil
		default:
			fmt.Println("Unknown command. Type 'help' for available commands.")
		}
	}

	return nil
}

// StartStdioServer is a convenience function to start an MCP server on stdio.
// This is used by the 'coral colony proxy mcp' command.
func StartStdioServer(registry *registry.Registry, db *database.Database, config Config, logger logging.Logger) error {
	server, err := New(registry, db, config, logger)
	if err != nil {
		return fmt.Errorf("failed to create MCP server: %w", err)
	}
	defer server.Close()

	ctx := context.Background()
	return server.ServeStdio(ctx)
}
