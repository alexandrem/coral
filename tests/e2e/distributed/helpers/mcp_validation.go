package helpers

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// ValidateJSONResponse validates that a tool response:
// 1. Is valid JSON
// 2. Can be unmarshaled into the expected schema
// 3. Contains expected structure
//
// Returns the unmarshaled result for further validation.
func ValidateJSONResponse(t *testing.T, response string, schema interface{}) interface{} {
	t.Helper()

	// First validate it's valid JSON.
	var rawJSON interface{}
	err := json.Unmarshal([]byte(response), &rawJSON)
	require.NoError(t, err, "Response should be valid JSON")

	// Then validate it matches the expected schema.
	err = json.Unmarshal([]byte(response), schema)
	require.NoError(t, err, "Response should match expected schema")

	return schema
}

// ValidateServiceInfo validates a single service entry from coral_list_services.
func ValidateServiceInfo(t *testing.T, svc ServiceInfo) {
	t.Helper()

	require.NotEmpty(t, svc.Name, "Service must have name")

	// Validate source enum.
	if svc.Source != "" {
		validSources := []string{"REGISTERED", "OBSERVED", "VERIFIED"}
		require.Contains(t, validSources, svc.Source, "Invalid source value: %s", svc.Source)
	}

	// Validate status enum.
	if svc.Status != "" {
		validStatuses := []string{"ACTIVE", "UNHEALTHY", "DISCONNECTED", "OBSERVED_ONLY"}
		require.Contains(t, validStatuses, svc.Status, "Invalid status value: %s", svc.Status)
	}

	// Validate instance count is non-negative.
	if svc.InstanceCount != 0 {
		require.GreaterOrEqual(t, svc.InstanceCount, int32(0), "Instance count cannot be negative")
	}

	// Validate port if present.
	if svc.Port != 0 {
		require.Greater(t, svc.Port, int32(0), "Port must be positive")
		require.LessOrEqual(t, svc.Port, int32(65535), "Port must be <= 65535")
	}
}

// ValidateFunctionResult validates a function discovery result.
func ValidateFunctionResult(t *testing.T, fn FunctionResult) {
	t.Helper()

	require.NotEmpty(t, fn.Function.Name, "Function must have name")

	// If search score is present, validate range.
	if fn.Search != nil {
		require.GreaterOrEqual(t, fn.Search.Score, float64(0), "Search score cannot be negative")
		require.LessOrEqual(t, fn.Search.Score, float64(1), "Search score must be <= 1.0")
	}

	// If metrics are present, validate they're non-negative.
	if fn.Metrics != nil {
		if fn.Metrics.CallsPerMin != nil {
			require.GreaterOrEqual(t, *fn.Metrics.CallsPerMin, float64(0), "Calls per minute cannot be negative")
		}
	}
}

// ValidateSpan validates a trace span.
func ValidateSpan(t *testing.T, span Span) {
	t.Helper()

	require.NotEmpty(t, span.TraceID, "Span must have trace ID")
	require.NotEmpty(t, span.SpanID, "Span must have span ID")
	require.NotEmpty(t, span.ServiceName, "Span must have service name")

	// Validate duration is non-negative.
	if span.DurationUs != 0 {
		require.GreaterOrEqual(t, span.DurationUs, int64(0), "Duration cannot be negative")
	}
}

// ValidateMetric validates a metric entry.
func ValidateMetric(t *testing.T, metric interface{}) {
	t.Helper()

	// This is a generic validator - specific metrics can add more validation.
	metricMap, ok := metric.(map[string]interface{})
	require.True(t, ok, "Metric should be a map")

	// Common validations for metrics.
	if serviceName, exists := metricMap["service_name"]; exists {
		require.NotEmpty(t, serviceName, "Service name cannot be empty")
	}

	if requestCount, exists := metricMap["request_count"]; exists {
		count, ok := requestCount.(float64)
		require.True(t, ok, "Request count should be numeric")
		require.GreaterOrEqual(t, count, float64(0), "Request count cannot be negative")
	}
}
