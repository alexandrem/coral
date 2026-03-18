package colony

import (
	"context"
	"time"

	"github.com/coral-mesh/coral/internal/colony/database"
)

// ebpfDatabase defines the interface for database operations needed by the service.
type ebpfDatabase interface {
	QueryBeylaHTTPMetrics(ctx context.Context, serviceName string, startTime, endTime time.Time, filters map[string]string) ([]*database.BeylaHTTPMetricResult, error)
	QueryBeylaGRPCMetrics(ctx context.Context, serviceName string, startTime, endTime time.Time, filters map[string]string) ([]*database.BeylaGRPCMetricResult, error)
	QueryBeylaSQLMetrics(ctx context.Context, serviceName string, startTime, endTime time.Time, filters map[string]string) ([]*database.BeylaSQLMetricResult, error)
	QueryBeylaTraces(ctx context.Context, traceID, serviceName string, startTime, endTime time.Time, minDurationUs int64, maxTraces int) ([]*database.BeylaTraceResult, error)
	QueryTelemetrySummaries(ctx context.Context, agentID string, startTime, endTime time.Time) ([]database.TelemetrySummary, error)
	QuerySystemMetricsSummaries(ctx context.Context, agentID string, startTime, endTime time.Time) ([]database.SystemMetricsSummary, error)
	GetServiceByName(ctx context.Context, serviceName string) (*database.Service, error)
	QueryAllServiceNames(ctx context.Context) ([]string, error)
	// RFD 074: Profiling-enriched summary.
	GetTopKHotspots(ctx context.Context, serviceName string, startTime, endTime time.Time, topK int) (*database.ProfilingSummaryResult, error)
	GetLatestBinaryMetadata(ctx context.Context, serviceName string) (*database.BinaryMetadata, error)
	GetPreviousBinaryMetadata(ctx context.Context, serviceName, currentBuildID string) (*database.BinaryMetadata, error)
	CompareHotspotsWithBaseline(ctx context.Context, serviceName, currentBuildID, baselineBuildID string, startTime, endTime time.Time, topK int) ([]database.RegressionIndicatorResult, error)
	// RFD 077: Memory profiling.
	GetTopKMemoryHotspots(ctx context.Context, serviceName string, startTime, endTime time.Time, topK int) (*database.MemoryProfilingSummaryResult, error)
}

// ProfilingEnrichmentConfig controls profiling enrichment in query summaries (RFD 074).
type ProfilingEnrichmentConfig struct {
	// Disabled controls whether profiling data is excluded from summaries.
	// Zero value (false) means profiling is enabled by default.
	Disabled bool
	// TopKHotspots is the default number of top hotspots. Default: 5, max: 20.
	TopKHotspots int
}

// ServiceStatus represents the health status of a service.
type ServiceStatus int

const (
	// ServiceStatusHealthy indicates the service is operating normally.
	ServiceStatusHealthy ServiceStatus = iota
	// ServiceStatusDegraded indicates the service has issues but is still operational.
	ServiceStatusDegraded
	// ServiceStatusCritical indicates the service has severe issues.
	ServiceStatusCritical
	// ServiceStatusIdle indicates the service is registered but not receiving traffic.
	ServiceStatusIdle
)

// String returns the string representation of ServiceStatus.
func (s ServiceStatus) String() string {
	switch s {
	case ServiceStatusHealthy:
		return "healthy"
	case ServiceStatusDegraded:
		return "degraded"
	case ServiceStatusCritical:
		return "critical"
	case ServiceStatusIdle:
		return "idle"
	default:
		return "unknown"
	}
}

// UnifiedSummaryResult represents the health summary of a service.
type UnifiedSummaryResult struct {
	ServiceName  string
	Status       ServiceStatus // healthy, degraded, critical, idle
	RequestCount int64         // Total requests/spans
	ErrorRate    float64       // Error rate as percentage
	AvgLatencyMs float64       // Average latency in milliseconds
	Source       string        // eBPF, OTLP, or eBPF+OTLP
	Issues       []string
	// Host resources (RFD 071).
	HostCPUUtilization    float64 // CPU utilization percentage (max in time window)
	HostCPUUtilizationAvg float64 // CPU utilization percentage (average in time window)
	HostMemoryUsageGB     float64 // Memory usage in GB (max in time window)
	HostMemoryLimitGB     float64 // Memory limit in GB
	HostMemoryUtilization float64 // Memory utilization percentage (max in time window)
	AgentID               string  // Agent ID for correlation
	// RFD 074: Profiling-enriched data.
	ProfilingSummary     *ProfilingSummaryData
	Deployment           *DeploymentData
	RegressionIndicators []RegressionIndicatorData
}

// ProfilingSummaryData contains top-K CPU and memory hotspots (RFD 074, RFD 077).
type ProfilingSummaryData struct {
	Hotspots       []HotspotData
	TotalSamples   uint64
	SamplingPeriod string
	BuildID        string

	// Compact representation computed from Hotspots for client consumption.
	// HotPath is the call chain from the hottest stack in caller→callee order.
	HotPath []string
	// SamplesByFunction maps each unique leaf function to its aggregated percentage.
	SamplesByFunction []FunctionSample

	// Memory profiling data (RFD 077).
	MemoryHotspots    []MemoryHotspotData
	TotalAllocBytes   int64
	TotalAllocObjects int64
	MemoryHotPath     []string
	MemoryByFunction  []FunctionMemorySample
}

// HotspotData represents a single CPU hotspot (RFD 074).
type HotspotData struct {
	Rank        int32
	Frames      []string
	Percentage  float64
	SampleCount uint64
}

// FunctionSample pairs a function name with its CPU percentage.
type FunctionSample struct {
	Function   string
	Percentage float64
}

// MemoryHotspotData represents a single memory allocation hotspot (RFD 077).
type MemoryHotspotData struct {
	Rank         int32
	Frames       []string
	Percentage   float64
	AllocBytes   int64
	AllocObjects int64
}

// FunctionMemorySample pairs a function name with its memory allocation data.
type FunctionMemorySample struct {
	Function   string
	Percentage float64
	AllocBytes int64
}

// DeploymentData contains deployment context (RFD 074).
type DeploymentData struct {
	BuildID    string
	DeployedAt time.Time
	Age        string
}

// RegressionIndicatorData contains a regression indicator (RFD 074).
type RegressionIndicatorData struct {
	Type               string
	Message            string
	BaselinePercentage float64
	CurrentPercentage  float64
	Delta              float64
}
