package colony

import (
	"math"
	"testing"
	"time"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/colony/database"
)

// TestCalculatePercentile tests the percentile calculation function.
func TestCalculatePercentile(t *testing.T) {
	tests := []struct {
		name     string
		values   []float64
		p        float64
		expected float64
	}{
		{
			name:     "single value",
			values:   []float64{10.0},
			p:        0.95,
			expected: 10.0,
		},
		{
			name:     "two values - p50",
			values:   []float64{10.0, 20.0},
			p:        0.50,
			expected: 15.0, // Linear interpolation between 10 and 20
		},
		{
			name:     "two values - p95",
			values:   []float64{10.0, 20.0},
			p:        0.95,
			expected: 19.5, // 95% between 10 and 20
		},
		{
			name:     "five values - p95",
			values:   []float64{10.0, 20.0, 30.0, 40.0, 50.0},
			p:        0.95,
			expected: 48.0, // 95% of 4 steps from 10
		},
		{
			name:     "five values sorted - p95",
			values:   []float64{10.0, 20.0, 30.0, 40.0, 50.0},
			p:        0.95,
			expected: 48.0, // 95% of range
		},
		{
			name:     "cpu utilization example - p95 (sorted)",
			values:   []float64{23.5, 45.2, 89.1, 91.3},
			p:        0.95,
			expected: 90.97, // 95th percentile: 89.1 + (91.3-89.1)*0.95
		},
		{
			name:     "all same values",
			values:   []float64{42.0, 42.0, 42.0, 42.0},
			p:        0.95,
			expected: 42.0,
		},
		{
			name:     "p0 (minimum)",
			values:   []float64{10.0, 20.0, 30.0, 40.0, 50.0},
			p:        0.0,
			expected: 10.0,
		},
		{
			name:     "p100 (maximum)",
			values:   []float64{10.0, 20.0, 30.0, 40.0, 50.0},
			p:        1.0,
			expected: 50.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculatePercentile(tt.values, tt.p)
			if math.Abs(result-tt.expected) > 0.01 {
				t.Errorf("calculatePercentile(%v, %.2f) = %.2f, expected %.2f",
					tt.values, tt.p, result, tt.expected)
			}
		})
	}
}

// TestAggregateMetrics tests the metric aggregation logic.
func TestAggregateMetrics(t *testing.T) {
	poller := &SystemMetricsPoller{
		pollInterval: 1 * time.Minute,
	}

	agentID := "test-agent-1"
	startTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	endTime := startTime.Add(1 * time.Minute)

	tests := []struct {
		name     string
		metrics  []*agentv1.SystemMetric
		expected []database.SystemMetricsSummary
	}{
		{
			name: "single gauge metric",
			metrics: []*agentv1.SystemMetric{
				{
					Timestamp:  startTime.UnixMilli(),
					Name:       "system.cpu.utilization",
					Value:      75.5,
					Unit:       "percent",
					MetricType: "gauge",
					Attributes: "{}",
				},
			},
			expected: []database.SystemMetricsSummary{
				{
					BucketTime:  startTime.Truncate(time.Minute),
					AgentID:     agentID,
					MetricName:  "system.cpu.utilization",
					MinValue:    75.5,
					MaxValue:    75.5,
					AvgValue:    75.5,
					P95Value:    75.5,
					DeltaValue:  0,
					SampleCount: 1,
					Unit:        "percent",
					MetricType:  "gauge",
					Attributes:  "{}",
				},
			},
		},
		{
			name: "multiple samples - gauge",
			metrics: []*agentv1.SystemMetric{
				{
					Timestamp:  startTime.UnixMilli(),
					Name:       "system.cpu.utilization",
					Value:      45.2,
					Unit:       "percent",
					MetricType: "gauge",
					Attributes: "{}",
				},
				{
					Timestamp:  startTime.Add(15 * time.Second).UnixMilli(),
					Name:       "system.cpu.utilization",
					Value:      89.1,
					Unit:       "percent",
					MetricType: "gauge",
					Attributes: "{}",
				},
				{
					Timestamp:  startTime.Add(30 * time.Second).UnixMilli(),
					Name:       "system.cpu.utilization",
					Value:      91.3,
					Unit:       "percent",
					MetricType: "gauge",
					Attributes: "{}",
				},
				{
					Timestamp:  startTime.Add(45 * time.Second).UnixMilli(),
					Name:       "system.cpu.utilization",
					Value:      67.8,
					Unit:       "percent",
					MetricType: "gauge",
					Attributes: "{}",
				},
			},
			expected: []database.SystemMetricsSummary{
				{
					BucketTime:  startTime.Truncate(time.Minute),
					AgentID:     agentID,
					MetricName:  "system.cpu.utilization",
					MinValue:    45.2,
					MaxValue:    91.3,
					AvgValue:    73.35, // (45.2 + 89.1 + 91.3 + 67.8) / 4
					P95Value:    90.97, // 95th percentile: 89.1 + (91.3-89.1)*0.85
					DeltaValue:  0,     // Gauges don't have delta value
					SampleCount: 4,
					Unit:        "percent",
					MetricType:  "gauge",
					Attributes:  "{}",
				},
			},
		},
		{
			name: "counter metric",
			metrics: []*agentv1.SystemMetric{
				{
					Timestamp:  startTime.UnixMilli(),
					Name:       "system.disk.io.read",
					Value:      1000000,
					Unit:       "bytes",
					MetricType: "counter",
					Attributes: "{}",
				},
				{
					Timestamp:  startTime.Add(15 * time.Second).UnixMilli(),
					Name:       "system.disk.io.read",
					Value:      1500000,
					Unit:       "bytes",
					MetricType: "counter",
					Attributes: "{}",
				},
				{
					Timestamp:  startTime.Add(30 * time.Second).UnixMilli(),
					Name:       "system.disk.io.read",
					Value:      2000000,
					Unit:       "bytes",
					MetricType: "counter",
					Attributes: "{}",
				},
				{
					Timestamp:  startTime.Add(45 * time.Second).UnixMilli(),
					Name:       "system.disk.io.read",
					Value:      2500000,
					Unit:       "bytes",
					MetricType: "counter",
					Attributes: "{}",
				},
			},
			expected: []database.SystemMetricsSummary{
				{
					BucketTime:  startTime.Truncate(time.Minute),
					AgentID:     agentID,
					MetricName:  "system.disk.io.read",
					MinValue:    1000000,
					MaxValue:    2500000,
					AvgValue:    1750000, // Average of counter values
					P95Value:    2425000, // 95th percentile
					DeltaValue:  1500000, // 2500000 - 1000000 (total increase)
					SampleCount: 4,
					Unit:        "bytes",
					MetricType:  "counter",
					Attributes:  "{}",
				},
			},
		},
		{
			name: "multiple metrics with different names",
			metrics: []*agentv1.SystemMetric{
				{
					Timestamp:  startTime.UnixMilli(),
					Name:       "system.cpu.utilization",
					Value:      50.0,
					Unit:       "percent",
					MetricType: "gauge",
					Attributes: "{}",
				},
				{
					Timestamp:  startTime.UnixMilli(),
					Name:       "system.memory.usage",
					Value:      8000000000,
					Unit:       "bytes",
					MetricType: "gauge",
					Attributes: "{}",
				},
				{
					Timestamp:  startTime.Add(15 * time.Second).UnixMilli(),
					Name:       "system.cpu.utilization",
					Value:      60.0,
					Unit:       "percent",
					MetricType: "gauge",
					Attributes: "{}",
				},
				{
					Timestamp:  startTime.Add(15 * time.Second).UnixMilli(),
					Name:       "system.memory.usage",
					Value:      8500000000,
					Unit:       "bytes",
					MetricType: "gauge",
					Attributes: "{}",
				},
			},
			expected: []database.SystemMetricsSummary{
				{
					BucketTime:  startTime.Truncate(time.Minute),
					AgentID:     agentID,
					MetricName:  "system.cpu.utilization",
					MinValue:    50.0,
					MaxValue:    60.0,
					AvgValue:    55.0,
					P95Value:    59.5,
					DeltaValue:  0, // Gauges don't have delta value
					SampleCount: 2,
					Unit:        "percent",
					MetricType:  "gauge",
					Attributes:  "{}",
				},
				{
					BucketTime:  startTime.Truncate(time.Minute),
					AgentID:     agentID,
					MetricName:  "system.memory.usage",
					MinValue:    8000000000,
					MaxValue:    8500000000,
					AvgValue:    8250000000,
					P95Value:    8475000000,
					DeltaValue:  0, // Gauges don't have delta value
					SampleCount: 2,
					Unit:        "bytes",
					MetricType:  "gauge",
					Attributes:  "{}",
				},
			},
		},
		{
			name: "metrics with different attributes",
			metrics: []*agentv1.SystemMetric{
				{
					Timestamp:  startTime.UnixMilli(),
					Name:       "system.disk.usage",
					Value:      100000000,
					Unit:       "bytes",
					MetricType: "gauge",
					Attributes: `{"mount":"/"}`,
				},
				{
					Timestamp:  startTime.UnixMilli(),
					Name:       "system.disk.usage",
					Value:      50000000,
					Unit:       "bytes",
					MetricType: "gauge",
					Attributes: `{"mount":"/data"}`,
				},
			},
			expected: []database.SystemMetricsSummary{
				{
					BucketTime:  startTime.Truncate(time.Minute),
					AgentID:     agentID,
					MetricName:  "system.disk.usage",
					MinValue:    100000000,
					MaxValue:    100000000,
					AvgValue:    100000000,
					P95Value:    100000000,
					DeltaValue:  0,
					SampleCount: 1,
					Unit:        "bytes",
					MetricType:  "gauge",
					Attributes:  `{"mount":"/"}`,
				},
				{
					BucketTime:  startTime.Truncate(time.Minute),
					AgentID:     agentID,
					MetricName:  "system.disk.usage",
					MinValue:    50000000,
					MaxValue:    50000000,
					AvgValue:    50000000,
					P95Value:    50000000,
					DeltaValue:  0,
					SampleCount: 1,
					Unit:        "bytes",
					MetricType:  "gauge",
					Attributes:  `{"mount":"/data"}`,
				},
			},
		},
		{
			name:     "empty metrics",
			metrics:  []*agentv1.SystemMetric{},
			expected: []database.SystemMetricsSummary{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := poller.aggregateMetrics(agentID, tt.metrics, startTime, endTime)

			if len(result) != len(tt.expected) {
				t.Fatalf("aggregateMetrics returned %d summaries, expected %d",
					len(result), len(tt.expected))
			}

			// Sort both slices by metric name and attributes for comparison.
			sortSummaries := func(summaries []database.SystemMetricsSummary) {
				for i := 0; i < len(summaries); i++ {
					for j := i + 1; j < len(summaries); j++ {
						if summaries[i].MetricName > summaries[j].MetricName ||
							(summaries[i].MetricName == summaries[j].MetricName &&
								summaries[i].Attributes > summaries[j].Attributes) {
							summaries[i], summaries[j] = summaries[j], summaries[i]
						}
					}
				}
			}

			sortSummaries(result)
			sortSummaries(tt.expected)

			for i, expected := range tt.expected {
				actual := result[i]

				if actual.MetricName != expected.MetricName {
					t.Errorf("Summary %d: metric_name = %s, expected %s",
						i, actual.MetricName, expected.MetricName)
				}
				if actual.AgentID != expected.AgentID {
					t.Errorf("Summary %d: agent_id = %s, expected %s",
						i, actual.AgentID, expected.AgentID)
				}
				if !actual.BucketTime.Equal(expected.BucketTime) {
					t.Errorf("Summary %d: bucket_time = %v, expected %v",
						i, actual.BucketTime, expected.BucketTime)
				}
				if math.Abs(actual.MinValue-expected.MinValue) > 0.01 {
					t.Errorf("Summary %d (%s): min_value = %.2f, expected %.2f",
						i, actual.MetricName, actual.MinValue, expected.MinValue)
				}
				if math.Abs(actual.MaxValue-expected.MaxValue) > 0.01 {
					t.Errorf("Summary %d (%s): max_value = %.2f, expected %.2f",
						i, actual.MetricName, actual.MaxValue, expected.MaxValue)
				}
				if math.Abs(actual.AvgValue-expected.AvgValue) > 0.01 {
					t.Errorf("Summary %d (%s): avg_value = %.2f, expected %.2f",
						i, actual.MetricName, actual.AvgValue, expected.AvgValue)
				}
				if math.Abs(actual.P95Value-expected.P95Value) > 0.01 {
					t.Errorf("Summary %d (%s): p95_value = %.2f, expected %.2f",
						i, actual.MetricName, actual.P95Value, expected.P95Value)
				}
				if math.Abs(actual.DeltaValue-expected.DeltaValue) > 0.01 {
					t.Errorf("Summary %d (%s): delta_value = %.2f, expected %.2f",
						i, actual.MetricName, actual.DeltaValue, expected.DeltaValue)
				}
				if actual.SampleCount != expected.SampleCount {
					t.Errorf("Summary %d (%s): sample_count = %d, expected %d",
						i, actual.MetricName, actual.SampleCount, expected.SampleCount)
				}
				if actual.Unit != expected.Unit {
					t.Errorf("Summary %d (%s): unit = %s, expected %s",
						i, actual.MetricName, actual.Unit, expected.Unit)
				}
				if actual.MetricType != expected.MetricType {
					t.Errorf("Summary %d (%s): metric_type = %s, expected %s",
						i, actual.MetricName, actual.MetricType, expected.MetricType)
				}
				if actual.Attributes != expected.Attributes {
					t.Errorf("Summary %d (%s): attributes = %s, expected %s",
						i, actual.MetricName, actual.Attributes, expected.Attributes)
				}
			}
		})
	}
}

// TestAggregateMetricsEdgeCases tests edge cases in aggregation.
func TestAggregateMetricsEdgeCases(t *testing.T) {
	poller := &SystemMetricsPoller{
		pollInterval: 1 * time.Minute,
	}

	agentID := "test-agent-1"
	startTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	endTime := startTime.Add(1 * time.Minute)

	t.Run("nil metrics", func(t *testing.T) {
		result := poller.aggregateMetrics(agentID, nil, startTime, endTime)
		if len(result) != 0 {
			t.Errorf("Expected empty result for nil metrics, got %d summaries", len(result))
		}
	})

	t.Run("metric with zero value", func(t *testing.T) {
		metrics := []*agentv1.SystemMetric{
			{
				Timestamp:  startTime.UnixMilli(),
				Name:       "system.cpu.utilization",
				Value:      0.0,
				Unit:       "percent",
				MetricType: "gauge",
				Attributes: "{}",
			},
		}

		result := poller.aggregateMetrics(agentID, metrics, startTime, endTime)
		if len(result) != 1 {
			t.Fatalf("Expected 1 summary, got %d", len(result))
		}

		if result[0].MinValue != 0.0 || result[0].MaxValue != 0.0 || result[0].AvgValue != 0.0 {
			t.Errorf("Zero value not handled correctly: min=%.2f, max=%.2f, avg=%.2f",
				result[0].MinValue, result[0].MaxValue, result[0].AvgValue)
		}
	})

	t.Run("very large values", func(t *testing.T) {
		metrics := []*agentv1.SystemMetric{
			{
				Timestamp:  startTime.UnixMilli(),
				Name:       "system.memory.usage",
				Value:      1099511627776, // 1TB in bytes
				Unit:       "bytes",
				MetricType: "gauge",
				Attributes: "{}",
			},
		}

		result := poller.aggregateMetrics(agentID, metrics, startTime, endTime)
		if len(result) != 1 {
			t.Fatalf("Expected 1 summary, got %d", len(result))
		}

		if result[0].AvgValue != 1099511627776 {
			t.Errorf("Large value not handled correctly: %.0f", result[0].AvgValue)
		}
	})

	t.Run("negative values (for delta counters)", func(t *testing.T) {
		metrics := []*agentv1.SystemMetric{
			{
				Timestamp:  startTime.UnixMilli(),
				Name:       "system.network.errors.receive",
				Value:      -5,
				Unit:       "count",
				MetricType: "counter",
				Attributes: "{}",
			},
		}

		result := poller.aggregateMetrics(agentID, metrics, startTime, endTime)
		if len(result) != 1 {
			t.Fatalf("Expected 1 summary, got %d", len(result))
		}

		if result[0].MinValue != -5 {
			t.Errorf("Negative value not handled correctly: %.0f", result[0].MinValue)
		}
	})
}
