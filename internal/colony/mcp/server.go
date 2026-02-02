// Package mcp implements the Model Context Protocol server for AI tool integration.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/registry"
	"github.com/coral-mesh/coral/internal/logging"
)

// Server wraps the MCP server and provides Colony-specific tools.
type Server struct {
	mcpServer    *server.MCPServer
	registry     *registry.Registry
	db           *database.Database
	config       Config
	logger       logging.Logger
	startedAt    time.Time
	debugService colonyv1connect.ColonyDebugServiceHandler
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

	// Profiling enrichment settings (RFD 074).
	ProfilingEnrichmentDisabled bool // When true, profiling data is excluded from summaries.
	ProfilingTopKHotspots       int  // Default: 5, max: 20.
}

// New creates a new MCP server instance.
func New(
	registry *registry.Registry,
	db *database.Database,
	debugService colonyv1connect.ColonyDebugServiceHandler,
	config Config,
	logger logging.Logger,
) (*Server, error) {
	if config.Disabled {
		return nil, fmt.Errorf("MCP server is disabled in configuration")
	}

	logger.Info().
		Str("colony_id", config.ColonyID).
		Bool("audit_enabled", config.AuditEnabled).
		Msg("Initializing MCP server")

	// Create MCP server with tool capabilities.
	mcpServer := server.NewMCPServer(
		fmt.Sprintf("coral-%s", config.ColonyID),
		"1.0.0",
		server.WithToolCapabilities(true),
	)

	// Create Server instance.
	s := &Server{
		mcpServer:    mcpServer,
		registry:     registry,
		db:           db,
		debugService: debugService,
		config:       config,
		logger:       logger,
		startedAt:    time.Now(),
	}

	// Register all tools with the MCP server.
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
func (s *Server) ServeStdio(_ context.Context) error {
	s.logger.Info().Msg("Starting MCP server on stdio")

	// Use mark3labs/mcp-go stdio transport.
	// Note: ServeStdio creates its own context and handles signals internally.
	return server.ServeStdio(s.mcpServer)
}

// Close stops the MCP server and releases resources.
func (s *Server) Close() error {
	s.logger.Info().Msg("Stopping MCP server")
	return nil
}

// ExecuteTool executes an MCP tool by name with JSON-encoded arguments.
// This is called by the colony server's RPC handler and the test-tool CLI command.
func (s *Server) ExecuteTool(ctx context.Context, toolName string, argumentsJSON string) (string, error) {
	// Validate tool exists.
	if !s.isToolEnabled(toolName) {
		return "", fmt.Errorf("tool not found or not enabled: %s", toolName)
	}

	// Execute the appropriate tool based on name.
	// Each tool parses its own arguments and executes its logic.
	switch toolName {
	// Observability tools
	// Unified Observability Tools (RFD 067)
	case "coral_query_summary":
		return s.executeUnifiedSummaryTool(ctx, argumentsJSON)
	case "coral_query_traces":
		return s.executeUnifiedTracesTool(ctx, argumentsJSON)
	case "coral_query_metrics":
		return s.executeUnifiedMetricsTool(ctx, argumentsJSON)
	case "coral_query_logs":
		return s.executeUnifiedLogsTool(ctx, argumentsJSON)

	case "coral_shell_exec":
		return s.executeShellExecTool(ctx, argumentsJSON)
	case "coral_container_exec":
		return s.executeContainerExecTool(ctx, argumentsJSON)

	// Service discovery (RFD 054)
	case "coral_list_services":
		return s.executeListServicesTool(ctx, argumentsJSON)

	// Live debugging tools (RFD 062)
	case "coral_attach_uprobe":
		return s.executeAttachUprobeTool(ctx, argumentsJSON)
	case "coral_trace_request_path":
		return s.executeTraceRequestPathTool(ctx, argumentsJSON)
	case "coral_list_debug_sessions":
		return s.executeListDebugSessionsTool(ctx, argumentsJSON)
	case "coral_detach_uprobe":
		return s.executeDetachUprobeTool(ctx, argumentsJSON)
	case "coral_get_debug_results":
		return s.executeGetDebugResultsTool(ctx, argumentsJSON)

	// Function discovery and profiling tools (RFD 069)
	case "coral_discover_functions":
		return s.executeDiscoverFunctionsTool(ctx, argumentsJSON)
	case "coral_profile_functions":
		return s.executeProfileFunctionsTool(ctx, argumentsJSON)

	// Profiling-enriched debugging tools (RFD 074)
	case "coral_debug_cpu_profile":
		return s.executeDebugCPUProfileTool(ctx, argumentsJSON)

	default:
		return "", fmt.Errorf("unknown tool: %s", toolName)
	}
}

// ListToolNames returns the list of all registered tool names (public wrapper).
func (s *Server) ListToolNames() []string {
	return s.listToolNames()
}

// IsToolEnabled checks if a tool is enabled based on configuration (public wrapper).
func (s *Server) IsToolEnabled(toolName string) bool {
	return s.isToolEnabled(toolName)
}

// ToolMetadata contains metadata about an MCP tool including its schema.
type ToolMetadata struct {
	Name            string
	Description     string
	InputSchemaJSON string
}

// GetToolMetadata returns metadata for all registered tools including their input schemas.
// This is used by the Colony server's ListTools RPC to populate tool information.
func (s *Server) GetToolMetadata() ([]ToolMetadata, error) {
	// Get tool names, descriptions, and schemas.
	// Schemas are generated from typed input structs using jsonschema reflection.
	// This provides type-safe tool definitions that are consistent across MCP and RPC APIs.
	toolDescriptions := s.getToolDescriptions()
	toolSchemas := s.getToolSchemas()

	metadata := make([]ToolMetadata, 0, len(toolDescriptions))
	for name, description := range toolDescriptions {
		// Only include enabled tools.
		if !s.isToolEnabled(name) {
			continue
		}

		// Get schema for this tool (or default to empty object).
		schemaJSON, ok := toolSchemas[name]
		if !ok {
			schemaJSON = "{\"type\": \"object\", \"properties\": {}}"
		}

		metadata = append(metadata, ToolMetadata{
			Name:            name,
			Description:     description,
			InputSchemaJSON: schemaJSON,
		})
	}

	return metadata, nil
}

// getToolSchemas returns a map of tool names to their JSON Schema definitions.
// Schemas are generated from the typed input structs using reflection.
// NOTE: This uses the same generateInputSchema() function as tool registration
// to ensure consistency between MCP stdio and RPC APIs.
func (s *Server) getToolSchemas() map[string]string {
	schemas := make(map[string]string)

	// Generate schema for each tool's input type.
	// Use the same input types as tool registration.
	toolInputTypes := map[string]interface{}{
		"coral_query_summary":       UnifiedSummaryInput{},
		"coral_query_traces":        UnifiedTracesInput{},
		"coral_query_metrics":       UnifiedMetricsInput{},
		"coral_query_logs":          UnifiedLogsInput{},
		"coral_shell_exec":          ShellExecInput{},
		"coral_container_exec":      ContainerExecInput{},
		"coral_list_services":       ListServicesInput{},
		"coral_attach_uprobe":       AttachUprobeInput{},
		"coral_trace_request_path":  TraceRequestPathInput{},
		"coral_list_debug_sessions": ListDebugSessionsInput{},
		"coral_detach_uprobe":       DetachUprobeInput{},
		"coral_get_debug_results":   GetDebugResultsInput{},
		// RFD 069: New unified function discovery and profiling tools
		"coral_discover_functions": DiscoverFunctionsInput{},
		"coral_profile_functions":  ProfileFunctionsInput{},
		// RFD 074: Profiling-enriched debugging
		"coral_debug_cpu_profile": DebugCPUProfileInput{},
	}

	for toolName, inputType := range toolInputTypes {
		// Use the same generateInputSchema() function to ensure consistency.
		// This removes $schema and $id fields and uses DoNotReference.
		schemaMap, err := generateInputSchema(inputType)
		if err != nil {
			s.logger.Warn().
				Err(err).
				Str("tool", toolName).
				Msg("Failed to generate tool schema")
			schemas[toolName] = "{\"type\": \"object\", \"properties\": {}}"
			continue
		}

		schemaBytes, err := json.Marshal(schemaMap)
		if err != nil {
			s.logger.Warn().
				Err(err).
				Str("tool", toolName).
				Msg("Failed to marshal tool schema")
			schemas[toolName] = "{\"type\": \"object\", \"properties\": {}}"
			continue
		}

		schemas[toolName] = string(schemaBytes)
	}

	return schemas
}

// getToolDescriptions returns a map of tool names to their descriptions.
// These descriptions are used when serving tools via both MCP and RPC APIs.
func (s *Server) getToolDescriptions() map[string]string {
	return map[string]string{
		"coral_query_summary":       "Get an enriched health summary for a service including system metrics, CPU profiling hotspots, deployment context, and regression indicators. Use this as the FIRST tool when diagnosing performance issues.",
		"coral_query_traces":        "Query distributed traces from all sources (eBPF + OTLP).",
		"coral_query_metrics":       "Query metrics from all sources (eBPF + OTLP).",
		"coral_query_logs":          "Query logs from OTLP.",
		"coral_shell_exec":          "Execute a one-off command in the agent's host environment. Returns stdout, stderr, and exit code. Command runs with 30s timeout (max 300s). Use for diagnostic commands like 'ps aux', 'ss -tlnp', 'tcpdump -c 10'.",
		"coral_container_exec":      "Execute a command in a container's namespace using nsenter. Access container-mounted configs, logs, and volumes that are not visible from the agent's host filesystem. Works in sidecar and node agent deployments. Returns stdout, stderr, exit code, and container PID. Use for commands like 'cat /app/config.yaml', 'ls /data'.",
		"coral_list_services":       "List all services known to the colony - includes both currently connected services and historical services from observability data. Returns service names, ports, and types. Useful for discovering available services before querying metrics or traces.",
		"coral_attach_uprobe":       "Attach eBPF uprobe to application function for live debugging. Captures entry/exit events, measures duration. Time-limited and production-safe.",
		"coral_trace_request_path":  "Trace all functions called during HTTP request execution. Auto-discovers call chain and builds execution tree.",
		"coral_list_debug_sessions": "List active and recent debug sessions across services.",
		"coral_detach_uprobe":       "Stop debug session early and detach eBPF probes. Returns collected data summary.",
		"coral_get_debug_results":   "Get aggregated results from debug session: call counts, duration percentiles, slow outliers.",
		// RFD 069: New unified function discovery and profiling tools
		"coral_discover_functions": "Unified function discovery with semantic search. Returns functions with embedded metrics, instrumentation info, and actionable suggestions. Use this for all function discovery needs.",
		"coral_profile_functions":  "Intelligent batch profiling with automatic analysis. Discovers functions via semantic search, applies selection strategy, attaches probes to multiple functions simultaneously, waits and collects data, analyzes bottlenecks automatically, and returns actionable recommendations. Use this for performance investigation.",
		// RFD 074: Profiling-enriched debugging
		"coral_debug_cpu_profile": "Collect a high-frequency CPU profile (99Hz) for detailed analysis of specific functions. Use this AFTER coral_query_summary identifies a CPU hotspot that needs line-level investigation. Returns top stacks with sample counts.",
	}
}

// registerTools registers all MCP tools with the server.
func (s *Server) registerTools() error {
	// Register unified observability tools.
	s.registerUnifiedSummaryTool()
	s.registerUnifiedTracesTool()
	s.registerUnifiedMetricsTool()
	s.registerUnifiedLogsTool()

	s.registerShellExecTool()

	// Register service discovery tools (RFD 054).
	s.registerQueryServicesTool()

	// Register live debugging tools (RFD 062).
	s.registerAttachUprobeTool()
	s.registerTraceRequestPathTool()
	s.registerListDebugSessionsTool()
	s.registerDetachUprobeTool()
	s.registerGetDebugResultsTool()

	// Register function discovery and profiling tools (RFD 069).
	// These replace coral_search_functions, coral_get_function_context, and coral_list_probeable_functions.
	s.registerDiscoverFunctionsTool()
	s.registerProfileFunctionsTool()

	// Register profiling-enriched debugging tools (RFD 074, RFD 077).
	s.registerDebugCPUProfileTool()
	s.registerDebugMemoryProfileTool()

	// TODO: Register analysis tools.
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
		"coral_query_summary",
		"coral_query_traces",
		"coral_query_metrics",
		"coral_query_logs",
		"coral_shell_exec",
		"coral_container_exec",
		"coral_list_services",
		"coral_attach_uprobe",
		"coral_trace_request_path",
		"coral_list_debug_sessions",
		"coral_detach_uprobe",
		"coral_get_debug_results",
		// RFD 069: New unified function discovery and profiling tools
		"coral_discover_functions",
		"coral_profile_functions",
		// RFD 074: Profiling-enriched debugging
		"coral_debug_cpu_profile",
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
func (s *Server) auditToolCall(toolName string, args interface{}) {
	if !s.config.AuditEnabled {
		return
	}

	argsJSON, _ := json.Marshal(args)
	s.logger.Info().
		Str("tool", toolName).
		RawJSON("args", argsJSON).
		Msg("MCP tool called")
}

// runInteractive runs the MCP server in interactive mode for testing.
// nolint: unused
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
	// For stdio server (proxy mode), we might not have a full debug service.
	// Pass nil for now, tools checking for it should handle nil gracefully.
	server, err := New(registry, db, nil, config, logger)
	if err != nil {
		return fmt.Errorf("failed to create MCP server: %w", err)
	}
	defer func() { _ = server.Close() }() // TODO: errcheck

	ctx := context.Background()
	return server.ServeStdio(ctx)
}
