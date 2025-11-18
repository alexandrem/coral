package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/coral-io/coral/internal/colony/database"
	"github.com/coral-io/coral/internal/colony/registry"
	"github.com/coral-io/coral/internal/logging"
	"github.com/firebase/genkit/go/genkit"
	"github.com/firebase/genkit/go/plugins/mcp"
	"github.com/invopop/jsonschema"
)

// Server wraps the Genkit MCP server and provides Colony-specific tools.
type Server struct {
	genkit    *genkit.Genkit
	mcpServer *mcp.GenkitMCPServer
	registry  *registry.Registry
	db        *database.Database
	config    Config
	logger    logging.Logger
	startedAt time.Time
	// toolFuncs maps tool names to their execution functions (for RPC calls).
	toolFuncs map[string]interface{}
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

	// Create Genkit instance.
	ctx := context.Background()
	g := genkit.Init(ctx)

	// Create Server instance first so we can register tools.
	s := &Server{
		genkit:    g,
		registry:  registry,
		db:        db,
		config:    config,
		logger:    logger,
		startedAt: time.Now(),
	}

	// Register all tools with Genkit.
	if err := s.registerTools(); err != nil {
		return nil, fmt.Errorf("failed to register tools: %w", err)
	}

	// Create Genkit MCP server (exposes registered tools).
	mcpServer := mcp.NewMCPServer(g, mcp.MCPServerOptions{
		Name:    fmt.Sprintf("coral-%s", config.ColonyID),
		Version: "1.0.0",
	})

	s.mcpServer = mcpServer

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
	return s.mcpServer.ServeStdio()
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
	case "coral_get_service_health":
		return s.executeServiceHealthTool(ctx, argumentsJSON)
	case "coral_get_service_topology":
		return s.executeServiceTopologyTool(ctx, argumentsJSON)
	case "coral_query_events":
		return s.executeQueryEventsTool(ctx, argumentsJSON)
	case "coral_query_beyla_http_metrics":
		return s.executeBeylaHTTPMetricsTool(ctx, argumentsJSON)
	case "coral_query_beyla_grpc_metrics":
		return s.executeBeylaGRPCMetricsTool(ctx, argumentsJSON)
	case "coral_query_beyla_sql_metrics":
		return s.executeBeylaSQLMetricsTool(ctx, argumentsJSON)
	case "coral_query_beyla_traces":
		return s.executeBeylaTracesTool(ctx, argumentsJSON)
	case "coral_get_trace_by_id":
		return s.executeTraceByIDTool(ctx, argumentsJSON)
	case "coral_query_telemetry_spans":
		return s.executeTelemetrySpansTool(ctx, argumentsJSON)
	case "coral_query_telemetry_metrics":
		return s.executeTelemetryMetricsTool(ctx, argumentsJSON)
	case "coral_query_telemetry_logs":
		return s.executeTelemetryLogsTool(ctx, argumentsJSON)

	// Live debugging tools (Phase 3)
	case "coral_start_ebpf_collector":
		return s.executeStartEBPFCollectorTool(ctx, argumentsJSON)
	case "coral_stop_ebpf_collector":
		return s.executeStopEBPFCollectorTool(ctx, argumentsJSON)
	case "coral_list_ebpf_collectors":
		return s.executeListEBPFCollectorsTool(ctx, argumentsJSON)
	case "coral_exec_command":
		return s.executeExecCommandTool(ctx, argumentsJSON)
	case "agent_shell_exec":
		return s.executeAgentShellExecTool(ctx, argumentsJSON)

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
	// Get tool names and descriptions.
	// Note: Full schema extraction from Genkit tools requires accessing the internal
	// tool registry, which is not currently exposed by the genkit/mcp library.
	// We generate schemas manually from our typed input structs using jsonschema reflection.
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
func (s *Server) getToolSchemas() map[string]string {
	reflector := jsonschema.Reflector{}

	schemas := make(map[string]string)

	// Generate schema for each tool's input type.
	toolInputTypes := map[string]interface{}{
		"coral_get_service_health":       ServiceHealthInput{},
		"coral_get_service_topology":     ServiceTopologyInput{},
		"coral_query_events":             QueryEventsInput{},
		"coral_query_beyla_http_metrics": BeylaHTTPMetricsInput{},
		"coral_query_beyla_grpc_metrics": BeylaGRPCMetricsInput{},
		"coral_query_beyla_sql_metrics":  BeylaSQLMetricsInput{},
		"coral_query_beyla_traces":       BeylaTracesInput{},
		"coral_get_trace_by_id":          TraceByIDInput{},
		"coral_query_telemetry_spans":    TelemetrySpansInput{},
		"coral_query_telemetry_metrics":  TelemetryMetricsInput{},
		"coral_query_telemetry_logs":     TelemetryLogsInput{},
		"coral_start_ebpf_collector":     StartEBPFCollectorInput{},
		"coral_stop_ebpf_collector":      StopEBPFCollectorInput{},
		"coral_list_ebpf_collectors":     ListEBPFCollectorsInput{},
		"coral_exec_command":             ExecCommandInput{},
		"coral_shell_start":              ShellStartInput{},
	}

	for toolName, inputType := range toolInputTypes {
		schema := reflector.Reflect(inputType)
		schemaBytes, err := json.Marshal(schema)
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
// This mirrors the descriptions used when registering tools via genkit.DefineTool.
func (s *Server) getToolDescriptions() map[string]string {
	return map[string]string{
		"coral_get_service_health":       "Get current health status of services. Returns health state, resource usage (CPU, memory), uptime, and recent issues.",
		"coral_get_service_topology":     "Get service dependency graph discovered from distributed traces. Shows which services communicate and call frequency.",
		"coral_query_events":             "Query operational events tracked by Coral (deployments, restarts, crashes, alerts, configuration changes).",
		"coral_query_beyla_http_metrics": "Query HTTP RED metrics collected by Beyla (request rate, error rate, latency distributions). Returns percentiles, status code breakdown, and route-level metrics.",
		"coral_query_beyla_grpc_metrics": "Query gRPC method-level RED metrics collected by Beyla. Returns RPC rate, latency distributions, and status code breakdown.",
		"coral_query_beyla_sql_metrics":  "Query SQL operation metrics collected by Beyla. Returns query latencies, operation types, and table-level statistics.",
		"coral_query_beyla_traces":       "Query distributed traces collected by Beyla. Can search by trace ID, service, time range, or duration threshold.",
		"coral_get_trace_by_id":          "Get a specific distributed trace by ID with full span tree showing parent-child relationships and timing.",
		"coral_query_telemetry_spans":    "Query generic OTLP spans (from instrumented applications using OpenTelemetry SDKs). Returns aggregated telemetry summaries. For detailed raw spans, see RFD 041.",
		"coral_query_telemetry_metrics":  "Query generic OTLP metrics (from instrumented applications). Returns time-series data for custom application metrics.",
		"coral_query_telemetry_logs":     "Query generic OTLP logs (from instrumented applications). Search application logs with full-text search and filters.",
		"coral_start_ebpf_collector":     "Start an on-demand eBPF collector for live debugging (CPU profiling, syscall tracing, network analysis). Collector runs for specified duration.",
		"coral_stop_ebpf_collector":      "Stop a running eBPF collector before its duration expires.",
		"coral_list_ebpf_collectors":     "List currently active eBPF collectors with their status and remaining duration.",
		"coral_exec_command":             "Execute a command in an application container (kubectl/docker exec semantics). Useful for checking configuration, running diagnostic commands, or inspecting container state.",
		"coral_shell_start":              "Start an interactive debug shell in the agent's environment (not the application container). Provides access to debugging tools (tcpdump, netcat, curl) and agent's data. Returns session ID for audit.",
	}
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

	// Register live debugging tools (Phase 3).
	s.registerStartEBPFCollectorTool()
	s.registerStopEBPFCollectorTool()
	s.registerListEBPFCollectorsTool()
	s.registerExecCommandTool()
	s.registerAgentShellExecTool()

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
		"coral_start_ebpf_collector",
		"coral_stop_ebpf_collector",
		"coral_list_ebpf_collectors",
		"coral_exec_command",
		"coral_shell_start",
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
