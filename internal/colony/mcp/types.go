package mcp

// Input types for MCP tools.
// Optional fields use pointers to allow nil values.

// ServiceHealthInput is the input for coral_get_service_health.
type ServiceHealthInput struct {
	ServiceFilter *string `json:"service_filter,omitempty" jsonschema:"description=Optional: Filter by service name pattern (e.g. 'api*' 'payment*')"`
}

// ServiceTopologyInput is the input for coral_get_service_topology.
type ServiceTopologyInput struct {
	Filter *string `json:"filter,omitempty" jsonschema:"description=Optional: Filter by service name tag or region"`
	Format *string `json:"format,omitempty" jsonschema:"description=Output format,enum=graph,enum=list,enum=json,default=graph"`
}

// QueryEventsInput is the input for coral_query_events.
type QueryEventsInput struct {
	EventType *string `json:"event_type,omitempty" jsonschema:"description=Event type filter,enum=deploy,enum=restart,enum=crash,enum=alert,enum=config_change,enum=connection,enum=error_spike"`
	TimeRange *string `json:"time_range,omitempty" jsonschema:"description=Time range to search,default=24h"`
	Service   *string `json:"service,omitempty" jsonschema:"description=Optional: Filter by service"`
}

// BeylaHTTPMetricsInput is the input for coral_query_beyla_http_metrics.
type BeylaHTTPMetricsInput struct {
	Service         string  `json:"service" jsonschema:"description=Service name (required)"`
	TimeRange       *string `json:"time_range,omitempty" jsonschema:"description=Time range (e.g. '1h' '30m' '24h'),default=1h"`
	HTTPRoute       *string `json:"http_route,omitempty" jsonschema:"description=Optional: Filter by HTTP route pattern (e.g. '/api/v1/users/:id')"`
	HTTPMethod      *string `json:"http_method,omitempty" jsonschema:"description=Optional: Filter by HTTP method,enum=GET,enum=POST,enum=PUT,enum=DELETE,enum=PATCH"`
	StatusCodeRange *string `json:"status_code_range,omitempty" jsonschema:"description=Optional: Filter by status code range,enum=2xx,enum=3xx,enum=4xx,enum=5xx"`
}

// BeylaGRPCMetricsInput is the input for coral_query_beyla_grpc_metrics.
type BeylaGRPCMetricsInput struct {
	Service    string  `json:"service" jsonschema:"description=Service name (required)"`
	TimeRange  *string `json:"time_range,omitempty" jsonschema:"description=Time range (e.g. '1h' '30m' '24h'),default=1h"`
	GRPCMethod *string `json:"grpc_method,omitempty" jsonschema:"description=Optional: Filter by gRPC method (e.g. '/payments.PaymentService/Charge')"`
	StatusCode *int    `json:"status_code,omitempty" jsonschema:"description=Optional: Filter by gRPC status code (0=OK 1=CANCELLED etc.)"`
}

// BeylaSQLMetricsInput is the input for coral_query_beyla_sql_metrics.
type BeylaSQLMetricsInput struct {
	Service      string  `json:"service" jsonschema:"description=Service name (required)"`
	TimeRange    *string `json:"time_range,omitempty" jsonschema:"description=Time range (e.g. '1h' '30m' '24h'),default=1h"`
	SQLOperation *string `json:"sql_operation,omitempty" jsonschema:"description=Optional: Filter by SQL operation,enum=SELECT,enum=INSERT,enum=UPDATE,enum=DELETE"`
	TableName    *string `json:"table_name,omitempty" jsonschema:"description=Optional: Filter by table name"`
}

// BeylaTracesInput is the input for coral_query_beyla_traces.
type BeylaTracesInput struct {
	TraceID       *string `json:"trace_id,omitempty" jsonschema:"description=Specific trace ID (32-char hex string)"`
	Service       *string `json:"service,omitempty" jsonschema:"description=Filter traces involving this service"`
	TimeRange     *string `json:"time_range,omitempty" jsonschema:"description=Time range (e.g. '1h' '30m' '24h'),default=1h"`
	MinDurationMs *int    `json:"min_duration_ms,omitempty" jsonschema:"description=Optional: Only return traces longer than this duration (milliseconds)"`
	MaxTraces     *int    `json:"max_traces,omitempty" jsonschema:"description=Maximum number of traces to return,default=10"`
}

// TraceByIDInput is the input for coral_get_trace_by_id.
type TraceByIDInput struct {
	TraceID string  `json:"trace_id" jsonschema:"description=Trace ID (32-char hex string)"`
	Format  *string `json:"format,omitempty" jsonschema:"description=Output format,enum=tree,enum=flat,enum=json,default=tree"`
}

// TelemetrySpansInput is the input for coral_query_telemetry_spans.
type TelemetrySpansInput struct {
	Service   string  `json:"service" jsonschema:"description=Service name"`
	TimeRange *string `json:"time_range,omitempty" jsonschema:"description=Time range (e.g. '1h' '30m' '24h'),default=1h"`
	Operation *string `json:"operation,omitempty" jsonschema:"description=Optional: Filter by operation name"`
}

// TelemetryMetricsInput is the input for coral_query_telemetry_metrics.
type TelemetryMetricsInput struct {
	MetricName *string `json:"metric_name,omitempty" jsonschema:"description=Metric name (e.g. 'http.server.duration' 'custom.orders.count')"`
	Service    *string `json:"service,omitempty" jsonschema:"description=Optional: Filter by service"`
	TimeRange  *string `json:"time_range,omitempty" jsonschema:"description=Time range,default=1h"`
}

// TelemetryLogsInput is the input for coral_query_telemetry_logs.
type TelemetryLogsInput struct {
	Query     *string `json:"query,omitempty" jsonschema:"description=Search query (full-text search)"`
	Service   *string `json:"service,omitempty" jsonschema:"description=Optional: Filter by service"`
	Level     *string `json:"level,omitempty" jsonschema:"description=Optional: Filter by log level,enum=DEBUG,enum=INFO,enum=WARN,enum=ERROR,enum=FATAL"`
	TimeRange *string `json:"time_range,omitempty" jsonschema:"description=Time range,default=1h"`
}

// Phase 3: Live Debugging Tool Inputs

// StartEBPFCollectorInput is the input for coral_start_ebpf_collector.
type StartEBPFCollectorInput struct {
	CollectorType   string  `json:"collector_type" jsonschema:"description=Type of eBPF collector to start,enum=cpu_profile,enum=syscall_stats,enum=http_latency,enum=tcp_metrics"`
	Service         string  `json:"service" jsonschema:"description=Target service name (use agent_id for disambiguation)"`
	AgentID         *string `json:"agent_id,omitempty" jsonschema:"description=Target agent ID (overrides service lookup, recommended for unambiguous targeting)"`
	DurationSeconds *int    `json:"duration_seconds,omitempty" jsonschema:"description=How long to run collector (max 300s),default=30"`
	ConfigJSON      *string `json:"config_json,omitempty" jsonschema:"description=Optional collector-specific configuration as JSON string"`
}

// StopEBPFCollectorInput is the input for coral_stop_ebpf_collector.
type StopEBPFCollectorInput struct {
	CollectorID string `json:"collector_id" jsonschema:"description=Collector ID returned from start_ebpf_collector"`
}

// ListEBPFCollectorsInput is the input for coral_list_ebpf_collectors.
type ListEBPFCollectorsInput struct {
	Service *string `json:"service,omitempty" jsonschema:"description=Optional: Filter by service (use agent_id for disambiguation)"`
	AgentID *string `json:"agent_id,omitempty" jsonschema:"description=Optional: Filter by agent ID"`
}

// ShellExecInput is the input for coral_shell_exec (RFD 045).
type ShellExecInput struct {
	Service        string            `json:"service" jsonschema:"description=Service whose agent to execute command on (use agent_id for disambiguation)"`
	AgentID        *string           `json:"agent_id,omitempty" jsonschema:"description=Target agent ID (overrides service lookup)"`
	Command        []string          `json:"command" jsonschema:"description=Command as array (e.g. [\"ps\" \"aux\"]),minItems=1"`
	TimeoutSeconds *uint32           `json:"timeout_seconds,omitempty" jsonschema:"description=Timeout in seconds,default=30,maximum=300"`
	WorkingDir     *string           `json:"working_dir,omitempty" jsonschema:"description=Working directory for command execution"`
	Env            map[string]string `json:"env,omitempty" jsonschema:"description=Additional environment variables"`
}

// ContainerExecInput is the input for coral_container_exec (RFD 056).
type ContainerExecInput struct {
	Service        string            `json:"service" jsonschema:"description=Service whose container to execute command in (use agent_id for disambiguation)"`
	AgentID        *string           `json:"agent_id,omitempty" jsonschema:"description=Target agent ID (overrides service lookup)"`
	ContainerName  *string           `json:"container_name,omitempty" jsonschema:"description=Container name (optional in sidecar mode)"`
	Command        []string          `json:"command" jsonschema:"description=Command as array (e.g. [\"cat\" \"/app/config.yaml\"]),minItems=1"`
	TimeoutSeconds *uint32           `json:"timeout_seconds,omitempty" jsonschema:"description=Timeout in seconds,default=30,maximum=300"`
	WorkingDir     *string           `json:"working_dir,omitempty" jsonschema:"description=Working directory in container namespace"`
	Env            map[string]string `json:"env,omitempty" jsonschema:"description=Additional environment variables"`
	Namespaces     []string          `json:"namespaces,omitempty" jsonschema:"description=Namespaces to enter (default: [\"mnt\"])"`
}

// AttachUprobeInput is the input for coral_attach_uprobe.
type AttachUprobeInput struct {
	Service    string  `json:"service" jsonschema:"description=Service name (required)"`
	Function   string  `json:"function" jsonschema:"description=Function name to probe (e.g., 'handleCheckout', 'main.processPayment')"`
	AgentID    *string `json:"agent_id,omitempty" jsonschema:"description=Target agent ID (optional, for direct targeting)"`
	SDKAddr    *string `json:"sdk_addr,omitempty" jsonschema:"description=SDK address (optional, for direct targeting)"`
	Duration   *string `json:"duration,omitempty" jsonschema:"description=Collection duration (e.g., '30s', '5m'). Default: 60s, max: 600s"`
	SampleRate *int    `json:"sample_rate,omitempty" jsonschema:"description=Sample every Nth call (1 = all calls). Default: 1"`
}

// TraceRequestPathInput is the input for coral_trace_request_path.
type TraceRequestPathInput struct {
	Service  string  `json:"service" jsonschema:"description=Service name"`
	Path     string  `json:"path" jsonschema:"description=HTTP path to trace (e.g., '/api/checkout')"`
	Duration *string `json:"duration,omitempty" jsonschema:"description=Trace duration. Default: 60s, max: 600s"`
}

// ListDebugSessionsInput is the input for coral_list_debug_sessions.
type ListDebugSessionsInput struct {
	Service *string `json:"service,omitempty" jsonschema:"description=Filter by service name (optional)"`
	Status  *string `json:"status,omitempty" jsonschema:"description=Filter by status (active, expired, all). Default: active"`
}

// DetachUprobeInput is the input for coral_detach_uprobe.
type DetachUprobeInput struct {
	SessionID string `json:"session_id" jsonschema:"description=Debug session ID to detach"`
}

// GetDebugResultsInput is the input for coral_get_debug_results.
type GetDebugResultsInput struct {
	SessionID string  `json:"session_id" jsonschema:"description=Debug session ID"`
	Format    *string `json:"format,omitempty" jsonschema:"description=Result format (summary, full, histogram). Default: summary"`
}

// SearchFunctionsInput is the input for coral_search_functions.
type SearchFunctionsInput struct {
	Service string `json:"service" jsonschema:"description=Service name"`
	Query   string `json:"query" jsonschema:"description=Natural language query (e.g., 'checkout payment processing', 'database query', 'authentication')"`
	Limit   *int   `json:"limit,omitempty" jsonschema:"description=Max results to return (default: 20, max: 50)"`
}

// GetFunctionContextInput is the input for coral_get_function_context.
type GetFunctionContextInput struct {
	Service        string `json:"service" jsonschema:"description=Service name"`
	Function       string `json:"function" jsonschema:"description=Function name (e.g., 'main.handleCheckout')"`
	IncludeCallers *bool  `json:"include_callers,omitempty" jsonschema:"description=Include functions that call this one. Default: true"`
	IncludeCallees *bool  `json:"include_callees,omitempty" jsonschema:"description=Include functions this one calls. Default: true"`
	IncludeMetrics *bool  `json:"include_metrics,omitempty" jsonschema:"description=Include performance metrics if available. Default: true"`
}

// ListProbeableFunctionsInput is the input for coral_list_probeable_functions.
type ListProbeableFunctionsInput struct {
	Service string  `json:"service" jsonschema:"description=Service name"`
	Pattern *string `json:"pattern,omitempty" jsonschema:"description=Regex filter for function names (e.g., 'handle.*', 'process.*')"`
}
