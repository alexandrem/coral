package helpers

// Response schemas for MCP tools.
// These match the output structures from internal/colony/mcp/tools_*.go

// ===== coral_list_services =====

// ListServicesResponse is the response from coral_list_services.
type ListServicesResponse struct {
	Services []ServiceInfo `json:"services"`
}

// ServiceInfo contains information about a service.
type ServiceInfo struct {
	Name          string            `json:"name"`
	Port          int32             `json:"port,omitempty"`
	ServiceType   string            `json:"service_type,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	Source        string            `json:"source"`           // REGISTERED, OBSERVED, or VERIFIED
	Status        string            `json:"status,omitempty"` // ACTIVE, UNHEALTHY, DISCONNECTED, or OBSERVED_ONLY
	InstanceCount int32             `json:"instance_count,omitempty"`
	AgentID       string            `json:"agent_id,omitempty"`
}

// ===== coral_discover_functions =====

// DiscoverFunctionsResponse is the response from coral_discover_functions.
type DiscoverFunctionsResponse struct {
	Functions       []FunctionResult `json:"functions"`
	DataCoveragePct int32            `json:"data_coverage_pct"`
	Suggestion      string           `json:"suggestion,omitempty"`
}

// FunctionResult contains information about a discovered function.
type FunctionResult struct {
	Function        FunctionInfo         `json:"function"`
	Search          *SearchInfo          `json:"search,omitempty"`
	Instrumentation *InstrumentationInfo `json:"instrumentation,omitempty"`
	Metrics         *FunctionMetrics     `json:"metrics,omitempty"`
	Suggestion      string               `json:"suggestion,omitempty"`
}

// FunctionInfo contains basic function metadata.
type FunctionInfo struct {
	Name    string `json:"name"`
	Package string `json:"package,omitempty"`
	File    string `json:"file,omitempty"`
	Line    int32  `json:"line,omitempty"`
}

// SearchInfo contains search relevance information.
type SearchInfo struct {
	Score  float64 `json:"score"`
	Reason string  `json:"reason,omitempty"`
}

// InstrumentationInfo contains information about function instrumentation.
type InstrumentationInfo struct {
	IsProbeable bool `json:"is_probeable"`
	HasDWARF    bool `json:"has_dwarf"`
}

// FunctionMetrics contains performance metrics for a function.
type FunctionMetrics struct {
	Source      string   `json:"source"`        // e.g., "OTLP", "eBPF"
	P95         *string  `json:"p95,omitempty"` // Duration string like "150ms"
	CallsPerMin *float64 `json:"calls_per_min,omitempty"`
}

// ===== coral_query_traces =====

// QueryTracesResponse is the response from coral_query_traces.
// Note: The actual response is text-based, but we can parse it for validation.
type QueryTracesResponse struct {
	Traces []Trace `json:"traces"`
}

// Trace represents a distributed trace.
type Trace struct {
	TraceID string `json:"trace_id"`
	Spans   []Span `json:"spans"`
}

// Span represents a single span in a trace.
type Span struct {
	TraceID      string                 `json:"trace_id"`
	SpanID       string                 `json:"span_id"`
	ParentSpanID string                 `json:"parent_span_id,omitempty"`
	ServiceName  string                 `json:"service_name"`
	SpanName     string                 `json:"span_name"`
	DurationUs   int64                  `json:"duration_us"`
	Attributes   map[string]interface{} `json:"attributes,omitempty"`
}

// ===== coral_query_metrics =====

// QueryMetricsResponse is the response from coral_query_metrics.
type QueryMetricsResponse struct {
	HTTPMetrics []HTTPMetric `json:"http_metrics,omitempty"`
	GRPCMetrics []GRPCMetric `json:"grpc_metrics,omitempty"`
	SQLMetrics  []SQLMetric  `json:"sql_metrics,omitempty"`
}

// HTTPMetric contains HTTP request metrics.
type HTTPMetric struct {
	ServiceName    string    `json:"service_name"`
	HTTPMethod     string    `json:"http_method"`
	HTTPRoute      string    `json:"http_route"`
	RequestCount   int64     `json:"request_count"`
	LatencyBuckets []float64 `json:"latency_buckets,omitempty"` // P50, P95, P99
}

// GRPCMetric contains gRPC request metrics.
type GRPCMetric struct {
	ServiceName  string  `json:"service_name"`
	GRPCMethod   string  `json:"grpc_method"`
	RequestCount int64   `json:"request_count"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
}

// SQLMetric contains SQL query metrics.
type SQLMetric struct {
	ServiceName  string  `json:"service_name"`
	SQLOperation string  `json:"sql_operation"`
	TableName    string  `json:"table_name,omitempty"`
	QueryCount   int64   `json:"query_count"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
}

// ===== coral_query_summary =====

// QuerySummaryResponse is the response from coral_query_summary.
// Note: This is text-based, but we can validate structure.
type QuerySummaryResponse struct {
	Services []ServiceSummary `json:"services"`
}

// ServiceSummary contains health summary for a service.
type ServiceSummary struct {
	ServiceName           string                `json:"service_name"`
	Source                string                `json:"source"` // REGISTERED, OBSERVED, VERIFIED
	Status                string                `json:"status"` // OK, DEGRADED, CRITICAL, IDLE
	RequestCount          int64                 `json:"request_count"`
	ErrorRate             float64               `json:"error_rate"` // 0-100
	AvgLatencyMs          float64               `json:"avg_latency_ms"`
	HostCPUUtilization    float64               `json:"host_cpu_utilization,omitempty"`    // 0-100
	HostMemoryUtilization float64               `json:"host_memory_utilization,omitempty"` // 0-100
	ProfilingSummary      *ProfilingSummary     `json:"profiling_summary,omitempty"`
	Deployment            *DeploymentInfo       `json:"deployment,omitempty"`
	RegressionIndicators  []RegressionIndicator `json:"regression_indicators,omitempty"`
}

// ProfilingSummary contains CPU/memory profiling hotspots.
type ProfilingSummary struct {
	SamplingPeriod string             `json:"sampling_period"`
	TotalSamples   int64              `json:"total_samples"`
	Hotspots       []ProfilingHotspot `json:"hotspots,omitempty"`
	MemoryHotspots []MemoryHotspot    `json:"memory_hotspots,omitempty"`
}

// ProfilingHotspot represents a CPU hotspot.
type ProfilingHotspot struct {
	Rank        int      `json:"rank"`
	Frames      []string `json:"frames"`
	Percentage  float64  `json:"percentage"` // 0-100
	SampleCount int64    `json:"sample_count"`
}

// MemoryHotspot represents a memory allocation hotspot.
type MemoryHotspot struct {
	Rank         int      `json:"rank"`
	Frames       []string `json:"frames"`
	Percentage   float64  `json:"percentage"` // 0-100
	AllocBytes   int64    `json:"alloc_bytes"`
	AllocObjects int64    `json:"alloc_objects"`
}

// DeploymentInfo contains deployment context.
type DeploymentInfo struct {
	BuildID string `json:"build_id"`
	Age     string `json:"age"` // Human-readable like "2h ago"
}

// RegressionIndicator indicates a performance regression.
type RegressionIndicator struct {
	Metric  string `json:"metric"` // e.g., "latency", "error_rate"
	Message string `json:"message"`
}
