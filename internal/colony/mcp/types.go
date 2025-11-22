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
	CollectorType   string                 `json:"collector_type" jsonschema:"description=Type of eBPF collector to start,enum=cpu_profile,enum=syscall_stats,enum=http_latency,enum=tcp_metrics"`
	Service         string                 `json:"service" jsonschema:"description=Target service name (use agent_id for disambiguation)"`
	AgentID         *string                `json:"agent_id,omitempty" jsonschema:"description=Target agent ID (overrides service lookup, recommended for unambiguous targeting)"`
	DurationSeconds *int                   `json:"duration_seconds,omitempty" jsonschema:"description=How long to run collector (max 300s),default=30"`
	Config          map[string]interface{} `json:"config,omitempty" jsonschema:"description=Optional collector-specific configuration (sample rate filters etc.)"`
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

// ExecCommandInput is the input for coral_exec_command.
type ExecCommandInput struct {
	Service        string   `json:"service" jsonschema:"description=Target service name (deprecated in multi-agent scenarios, use agent_id)"`
	AgentID        *string  `json:"agent_id,omitempty" jsonschema:"description=Target agent ID (overrides service lookup, recommended for unambiguous targeting)"`
	Command        []string `json:"command" jsonschema:"description=Command and arguments to execute (e.g. ['ls' '-la' '/app'])"`
	TimeoutSeconds *int     `json:"timeout_seconds,omitempty" jsonschema:"description=Command timeout,default=30"`
	WorkingDir     *string  `json:"working_dir,omitempty" jsonschema:"description=Optional: Working directory"`
}

// ShellStartInput is the input for coral_shell_start.
type ShellStartInput struct {
	Service string  `json:"service" jsonschema:"description=Service whose agent to connect to (use agent_id for disambiguation)"`
	AgentID *string `json:"agent_id,omitempty" jsonschema:"description=Target agent ID (overrides service lookup)"`
	Shell   *string `json:"shell,omitempty" jsonschema:"description=Shell to use,enum=/bin/bash,enum=/bin/sh,default=/bin/bash"`
}
