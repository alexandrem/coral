package server

import (
	"context"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/colony"
)

// mockEbpfService implements the eBPF query service interface for testing.
type mockEbpfService struct {
	summaryResults    []colony.UnifiedSummaryResult
	traceSpans        []*agentv1.EbpfTraceSpan
	metricsResponse   *agentv1.QueryEbpfMetricsResponse
	logs              []string
	shouldReturnErr   bool
	capturedService   string
	capturedStartTime time.Time
	capturedEndTime   time.Time
}

func (m *mockEbpfService) QueryUnifiedSummary(ctx context.Context, serviceName string, startTime, endTime time.Time) ([]colony.UnifiedSummaryResult, error) {
	m.capturedService = serviceName
	m.capturedStartTime = startTime
	m.capturedEndTime = endTime
	if m.shouldReturnErr {
		return nil, fmt.Errorf("mock error")
	}
	return m.summaryResults, nil
}

func (m *mockEbpfService) QueryUnifiedTraces(ctx context.Context, traceID, serviceName string, startTime, endTime time.Time, minDurationUs int64, maxTraces int) ([]*agentv1.EbpfTraceSpan, error) {
	m.capturedService = serviceName
	m.capturedStartTime = startTime
	m.capturedEndTime = endTime
	if m.shouldReturnErr {
		return nil, fmt.Errorf("mock error")
	}
	return m.traceSpans, nil
}

func (m *mockEbpfService) QueryUnifiedMetrics(ctx context.Context, serviceName string, startTime, endTime time.Time) (*agentv1.QueryEbpfMetricsResponse, error) {
	m.capturedService = serviceName
	m.capturedStartTime = startTime
	m.capturedEndTime = endTime
	if m.shouldReturnErr {
		return nil, fmt.Errorf("mock error")
	}
	return m.metricsResponse, nil
}

func (m *mockEbpfService) QueryUnifiedLogs(ctx context.Context, serviceName string, startTime, endTime time.Time, level string, search string) ([]string, error) {
	m.capturedService = serviceName
	m.capturedStartTime = startTime
	m.capturedEndTime = endTime
	if m.shouldReturnErr {
		return nil, fmt.Errorf("mock error")
	}
	return m.logs, nil
}

// TestQueryUnifiedSummaryHandler tests the QueryUnifiedSummary RPC handler.
func TestQueryUnifiedSummaryHandler(t *testing.T) {
	t.Run("successful summary query", func(t *testing.T) {
		mockSvc := &mockEbpfService{
			summaryResults: []colony.UnifiedSummaryResult{
				{ServiceName: "api-service", Status: "healthy"},
				{ServiceName: "payment-service", Status: "degraded"},
			},
		}

		server := &Server{
			ebpfService: mockSvc,
		}

		req := connect.NewRequest(&colonyv1.QueryUnifiedSummaryRequest{
			Service:   "api-service",
			TimeRange: "5m",
		})

		resp, err := server.QueryUnifiedSummary(context.Background(), req)
		require.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Contains(t, resp.Msg.Result, "api-service")
		assert.Contains(t, resp.Msg.Result, "healthy")
		assert.Equal(t, "api-service", mockSvc.capturedService)
	})

	t.Run("default time range", func(t *testing.T) {
		mockSvc := &mockEbpfService{
			summaryResults: []colony.UnifiedSummaryResult{},
		}

		server := &Server{
			ebpfService: mockSvc,
		}

		req := connect.NewRequest(&colonyv1.QueryUnifiedSummaryRequest{
			Service:   "",
			TimeRange: "", // Should default to 1h
		})

		resp, err := server.QueryUnifiedSummary(context.Background(), req)
		require.NoError(t, err)
		assert.NotNil(t, resp)

		// Verify time range is approximately 1 hour
		duration := mockSvc.capturedEndTime.Sub(mockSvc.capturedStartTime)
		assert.InDelta(t, time.Hour, duration, float64(time.Second))
	})

	t.Run("invalid time range", func(t *testing.T) {
		mockSvc := &mockEbpfService{}

		server := &Server{
			ebpfService: mockSvc,
		}

		req := connect.NewRequest(&colonyv1.QueryUnifiedSummaryRequest{
			Service:   "",
			TimeRange: "invalid",
		})

		resp, err := server.QueryUnifiedSummary(context.Background(), req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "invalid time_range")
	})

	t.Run("service error", func(t *testing.T) {
		mockSvc := &mockEbpfService{
			shouldReturnErr: true,
		}

		server := &Server{
			ebpfService: mockSvc,
		}

		req := connect.NewRequest(&colonyv1.QueryUnifiedSummaryRequest{
			Service:   "api-service",
			TimeRange: "5m",
		})

		resp, err := server.QueryUnifiedSummary(context.Background(), req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "failed to query summary")
	})

	t.Run("nil service", func(t *testing.T) {
		server := &Server{
			ebpfService: nil,
		}

		req := connect.NewRequest(&colonyv1.QueryUnifiedSummaryRequest{
			Service:   "api-service",
			TimeRange: "5m",
		})

		resp, err := server.QueryUnifiedSummary(context.Background(), req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "eBPF service not available")
	})
}

// TestQueryUnifiedTracesHandler tests the QueryUnifiedTraces RPC handler.
func TestQueryUnifiedTracesHandler(t *testing.T) {
	t.Run("successful traces query", func(t *testing.T) {
		mockSvc := &mockEbpfService{
			traceSpans: []*agentv1.EbpfTraceSpan{
				{TraceId: "trace-123", SpanId: "span-456"},
				{TraceId: "trace-123", SpanId: "span-789"},
			},
		}

		server := &Server{
			ebpfService: mockSvc,
		}

		req := connect.NewRequest(&colonyv1.QueryUnifiedTracesRequest{
			Service:       "api-service",
			TimeRange:     "1h",
			TraceId:       "trace-123",
			MinDurationMs: 500,
			MaxTraces:     10,
		})

		resp, err := server.QueryUnifiedTraces(context.Background(), req)
		require.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Contains(t, resp.Msg.Result, "2 spans")
	})

	t.Run("default time range", func(t *testing.T) {
		mockSvc := &mockEbpfService{
			traceSpans: []*agentv1.EbpfTraceSpan{},
		}

		server := &Server{
			ebpfService: mockSvc,
		}

		req := connect.NewRequest(&colonyv1.QueryUnifiedTracesRequest{
			Service:   "",
			TimeRange: "", // Should default to 1h
		})

		resp, err := server.QueryUnifiedTraces(context.Background(), req)
		require.NoError(t, err)
		assert.NotNil(t, resp)

		// Verify time range is approximately 1 hour
		duration := mockSvc.capturedEndTime.Sub(mockSvc.capturedStartTime)
		assert.InDelta(t, time.Hour, duration, float64(time.Second))
	})

	t.Run("min duration conversion", func(t *testing.T) {
		mockSvc := &mockEbpfService{
			traceSpans: []*agentv1.EbpfTraceSpan{},
		}

		server := &Server{
			ebpfService: mockSvc,
		}

		req := connect.NewRequest(&colonyv1.QueryUnifiedTracesRequest{
			Service:       "api-service",
			TimeRange:     "1h",
			MinDurationMs: 500, // Should convert to 500000 microseconds
			MaxTraces:     10,
		})

		_, err := server.QueryUnifiedTraces(context.Background(), req)
		require.NoError(t, err)
		// Verification of microsecond conversion happens in the service layer
	})

	t.Run("invalid time range", func(t *testing.T) {
		mockSvc := &mockEbpfService{}

		server := &Server{
			ebpfService: mockSvc,
		}

		req := connect.NewRequest(&colonyv1.QueryUnifiedTracesRequest{
			Service:   "",
			TimeRange: "invalid",
		})

		resp, err := server.QueryUnifiedTraces(context.Background(), req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "invalid time_range")
	})

	t.Run("service error", func(t *testing.T) {
		mockSvc := &mockEbpfService{
			shouldReturnErr: true,
		}

		server := &Server{
			ebpfService: mockSvc,
		}

		req := connect.NewRequest(&colonyv1.QueryUnifiedTracesRequest{
			Service:   "api-service",
			TimeRange: "1h",
		})

		resp, err := server.QueryUnifiedTraces(context.Background(), req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "failed to query traces")
	})
}

// TestQueryUnifiedMetricsHandler tests the QueryUnifiedMetrics RPC handler.
func TestQueryUnifiedMetricsHandler(t *testing.T) {
	t.Run("successful metrics query", func(t *testing.T) {
		mockSvc := &mockEbpfService{
			metricsResponse: &agentv1.QueryEbpfMetricsResponse{
				HttpMetrics: []*agentv1.EbpfHttpMetric{
					{ServiceName: "api-service", HttpRoute: "/api/users"},
				},
			},
		}

		server := &Server{
			ebpfService: mockSvc,
		}

		req := connect.NewRequest(&colonyv1.QueryUnifiedMetricsRequest{
			Service:   "api-service",
			TimeRange: "1h",
		})

		resp, err := server.QueryUnifiedMetrics(context.Background(), req)
		require.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Contains(t, resp.Msg.Result, "api-service")
		assert.Contains(t, resp.Msg.Result, "HTTP Metrics:")
	})

	t.Run("default time range", func(t *testing.T) {
		mockSvc := &mockEbpfService{
			metricsResponse: &agentv1.QueryEbpfMetricsResponse{},
		}

		server := &Server{
			ebpfService: mockSvc,
		}

		req := connect.NewRequest(&colonyv1.QueryUnifiedMetricsRequest{
			Service:   "",
			TimeRange: "", // Should default to 1h
		})

		resp, err := server.QueryUnifiedMetrics(context.Background(), req)
		require.NoError(t, err)
		assert.NotNil(t, resp)

		// Verify time range is approximately 1 hour
		duration := mockSvc.capturedEndTime.Sub(mockSvc.capturedStartTime)
		assert.InDelta(t, time.Hour, duration, float64(time.Second))
	})

	t.Run("empty metrics", func(t *testing.T) {
		mockSvc := &mockEbpfService{
			metricsResponse: &agentv1.QueryEbpfMetricsResponse{
				HttpMetrics: []*agentv1.EbpfHttpMetric{},
			},
		}

		server := &Server{
			ebpfService: mockSvc,
		}

		req := connect.NewRequest(&colonyv1.QueryUnifiedMetricsRequest{
			Service:   "nonexistent-service",
			TimeRange: "1h",
		})

		resp, err := server.QueryUnifiedMetrics(context.Background(), req)
		require.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("invalid time range", func(t *testing.T) {
		mockSvc := &mockEbpfService{}

		server := &Server{
			ebpfService: mockSvc,
		}

		req := connect.NewRequest(&colonyv1.QueryUnifiedMetricsRequest{
			Service:   "",
			TimeRange: "bad-range",
		})

		resp, err := server.QueryUnifiedMetrics(context.Background(), req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "invalid time_range")
	})

	t.Run("service error", func(t *testing.T) {
		mockSvc := &mockEbpfService{
			shouldReturnErr: true,
		}

		server := &Server{
			ebpfService: mockSvc,
		}

		req := connect.NewRequest(&colonyv1.QueryUnifiedMetricsRequest{
			Service:   "api-service",
			TimeRange: "1h",
		})

		resp, err := server.QueryUnifiedMetrics(context.Background(), req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "failed to query metrics")
	})
}

// TestQueryUnifiedLogsHandler tests the QueryUnifiedLogs RPC handler.
func TestQueryUnifiedLogsHandler(t *testing.T) {
	t.Run("successful logs query", func(t *testing.T) {
		mockSvc := &mockEbpfService{
			logs: []string{
				"[ERROR] Connection timeout",
				"[ERROR] Database unavailable",
			},
		}

		server := &Server{
			ebpfService: mockSvc,
		}

		req := connect.NewRequest(&colonyv1.QueryUnifiedLogsRequest{
			Service:   "api-service",
			TimeRange: "1h",
			Level:     "error",
			Search:    "timeout",
		})

		resp, err := server.QueryUnifiedLogs(context.Background(), req)
		require.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Contains(t, resp.Msg.Result, "2 logs")
	})

	t.Run("default time range", func(t *testing.T) {
		mockSvc := &mockEbpfService{
			logs: []string{},
		}

		server := &Server{
			ebpfService: mockSvc,
		}

		req := connect.NewRequest(&colonyv1.QueryUnifiedLogsRequest{
			Service:   "",
			TimeRange: "", // Should default to 1h
		})

		resp, err := server.QueryUnifiedLogs(context.Background(), req)
		require.NoError(t, err)
		assert.NotNil(t, resp)

		// Verify time range is approximately 1 hour
		duration := mockSvc.capturedEndTime.Sub(mockSvc.capturedStartTime)
		assert.InDelta(t, time.Hour, duration, float64(time.Second))
	})

	t.Run("empty logs", func(t *testing.T) {
		mockSvc := &mockEbpfService{
			logs: []string{},
		}

		server := &Server{
			ebpfService: mockSvc,
		}

		req := connect.NewRequest(&colonyv1.QueryUnifiedLogsRequest{
			Service:   "api-service",
			TimeRange: "1h",
		})

		resp, err := server.QueryUnifiedLogs(context.Background(), req)
		require.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Contains(t, resp.Msg.Result, "0 logs")
	})

	t.Run("invalid time range", func(t *testing.T) {
		mockSvc := &mockEbpfService{}

		server := &Server{
			ebpfService: mockSvc,
		}

		req := connect.NewRequest(&colonyv1.QueryUnifiedLogsRequest{
			Service:   "",
			TimeRange: "invalid-range",
		})

		resp, err := server.QueryUnifiedLogs(context.Background(), req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "invalid time_range")
	})

	t.Run("service error", func(t *testing.T) {
		mockSvc := &mockEbpfService{
			shouldReturnErr: true,
		}

		server := &Server{
			ebpfService: mockSvc,
		}

		req := connect.NewRequest(&colonyv1.QueryUnifiedLogsRequest{
			Service:   "api-service",
			TimeRange: "1h",
		})

		resp, err := server.QueryUnifiedLogs(context.Background(), req)
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "failed to query logs")
	})
}

// TestParseTimeRange tests the parseTimeRange helper function.
func TestParseTimeRange(t *testing.T) {
	t.Run("valid durations", func(t *testing.T) {
		tests := []struct {
			name     string
			input    string
			expected time.Duration
		}{
			{"5 minutes", "5m", 5 * time.Minute},
			{"1 hour", "1h", 1 * time.Hour},
			{"30 seconds", "30s", 30 * time.Second},
			{"2 hours", "2h", 2 * time.Hour},
			{"24 hours", "24h", 24 * time.Hour},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				start, end, err := parseTimeRange(tt.input)
				require.NoError(t, err)
				duration := end.Sub(start)
				assert.InDelta(t, tt.expected, duration, float64(time.Second))
			})
		}
	})

	t.Run("empty string defaults to 1h", func(t *testing.T) {
		start, end, err := parseTimeRange("")
		require.NoError(t, err)
		duration := end.Sub(start)
		assert.InDelta(t, time.Hour, duration, float64(time.Second))
	})

	t.Run("invalid duration format", func(t *testing.T) {
		_, _, err := parseTimeRange("invalid")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid duration format")
	})

	t.Run("end time is approximately now", func(t *testing.T) {
		before := time.Now()
		_, end, err := parseTimeRange("5m")
		after := time.Now()

		require.NoError(t, err)
		assert.True(t, end.After(before) || end.Equal(before))
		assert.True(t, end.Before(after) || end.Equal(after))
	})

	t.Run("start time is before end time", func(t *testing.T) {
		start, end, err := parseTimeRange("1h")
		require.NoError(t, err)
		assert.True(t, start.Before(end))
	})
}
