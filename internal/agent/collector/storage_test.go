package collector

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStorage_QueryMetrics_Attributes(t *testing.T) {
	// Use in-memory DuckDB for testing
	db, err := sql.Open("duckdb", "")
	require.NoError(t, err)
	defer db.Close()

	logger := zerolog.Nop()
	storage, err := NewStorage(db, logger)
	require.NoError(t, err)

	ctx := context.Background()

	// 1. Store a metric with attributes
	timestamp := time.Now().Truncate(time.Second)
	metric := Metric{
		Timestamp:  timestamp,
		Name:       "test_metric",
		Value:      123.45,
		Unit:       "bytes",
		MetricType: "gauge",
		Attributes: map[string]string{
			"key1": "value1",
			"key2": "value2",
		},
	}

	err = storage.StoreMetric(ctx, metric)
	require.NoError(t, err)

	// 2. Query it back
	results, err := storage.QueryMetrics(ctx, timestamp.Add(-1*time.Minute), timestamp.Add(1*time.Minute), nil)
	require.NoError(t, err)
	require.Len(t, results, 1)

	// 3. Verify attributes are correctly unmarshaled
	result := results[0]
	assert.Equal(t, metric.Name, result.Name)
	assert.Equal(t, metric.Value, result.Value)
	assert.Equal(t, "value1", result.Attributes["key1"])
	assert.Equal(t, "value2", result.Attributes["key2"])
}

func TestNewStorage(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	require.NoError(t, err)
	defer db.Close()

	logger := zerolog.Nop()
	storage, err := NewStorage(db, logger)

	require.NoError(t, err)
	assert.NotNil(t, storage)
	assert.NotNil(t, storage.db)
	assert.NotNil(t, storage.metricsTable)
}

func TestStoreMetrics_Batch(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	require.NoError(t, err)
	defer db.Close()

	logger := zerolog.Nop()
	storage, err := NewStorage(db, logger)
	require.NoError(t, err)

	ctx := context.Background()
	now := time.Now()

	tests := []struct {
		name    string
		metrics []Metric
		wantErr bool
	}{
		{
			name:    "empty batch",
			metrics: []Metric{},
			wantErr: false,
		},
		{
			name: "single metric",
			metrics: []Metric{
				{
					Timestamp:  now,
					Name:       "cpu.usage",
					Value:      50.0,
					Unit:       "percent",
					MetricType: "gauge",
					Attributes: map[string]string{},
				},
			},
			wantErr: false,
		},
		{
			name: "multiple metrics",
			metrics: []Metric{
				{
					Timestamp:  now,
					Name:       "cpu.usage",
					Value:      50.0,
					Unit:       "percent",
					MetricType: "gauge",
					Attributes: map[string]string{},
				},
				{
					Timestamp:  now,
					Name:       "memory.usage",
					Value:      1024,
					Unit:       "bytes",
					MetricType: "gauge",
					Attributes: map[string]string{"type": "heap"},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := storage.StoreMetrics(ctx, tt.metrics)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestQueryMetrics_TimeRange(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	require.NoError(t, err)
	defer db.Close()

	logger := zerolog.Nop()
	storage, err := NewStorage(db, logger)
	require.NoError(t, err)

	ctx := context.Background()
	now := time.Now()

	// Insert test metrics at different times.
	metrics := []Metric{
		{
			Timestamp:  now.Add(-20 * time.Minute),
			Name:       "metric.old",
			Value:      1.0,
			Unit:       "count",
			MetricType: "counter",
			Attributes: map[string]string{},
		},
		{
			Timestamp:  now.Add(-10 * time.Minute),
			Name:       "metric.middle",
			Value:      2.0,
			Unit:       "count",
			MetricType: "counter",
			Attributes: map[string]string{},
		},
		{
			Timestamp:  now,
			Name:       "metric.new",
			Value:      3.0,
			Unit:       "count",
			MetricType: "counter",
			Attributes: map[string]string{},
		},
	}

	err = storage.StoreMetrics(ctx, metrics)
	require.NoError(t, err)

	tests := []struct {
		name      string
		startTime time.Time
		endTime   time.Time
		wantCount int
	}{
		{
			name:      "query all",
			startTime: now.Add(-30 * time.Minute),
			endTime:   now.Add(1 * time.Minute),
			wantCount: 3,
		},
		{
			name:      "query recent",
			startTime: now.Add(-15 * time.Minute),
			endTime:   now.Add(1 * time.Minute),
			wantCount: 2,
		},
		{
			name:      "query old only",
			startTime: now.Add(-30 * time.Minute),
			endTime:   now.Add(-15 * time.Minute),
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := storage.QueryMetrics(ctx, tt.startTime, tt.endTime, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.wantCount, len(results))
		})
	}
}

func TestQueryMetrics_FilterByName(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	require.NoError(t, err)
	defer db.Close()

	logger := zerolog.Nop()
	storage, err := NewStorage(db, logger)
	require.NoError(t, err)

	ctx := context.Background()
	now := time.Now()

	// Insert metrics with different names.
	metrics := []Metric{
		{
			Timestamp:  now,
			Name:       "cpu.usage",
			Value:      50.0,
			Unit:       "percent",
			MetricType: "gauge",
			Attributes: map[string]string{},
		},
		{
			Timestamp:  now,
			Name:       "memory.usage",
			Value:      1024,
			Unit:       "bytes",
			MetricType: "gauge",
			Attributes: map[string]string{},
		},
		{
			Timestamp:  now,
			Name:       "disk.usage",
			Value:      2048,
			Unit:       "bytes",
			MetricType: "gauge",
			Attributes: map[string]string{},
		},
	}

	err = storage.StoreMetrics(ctx, metrics)
	require.NoError(t, err)

	tests := []struct {
		name        string
		metricNames []string
		wantCount   int
	}{
		{
			name:        "query all metrics",
			metricNames: nil,
			wantCount:   3,
		},
		{
			name:        "query single metric",
			metricNames: []string{"cpu.usage"},
			wantCount:   1,
		},
		{
			name:        "query multiple metrics",
			metricNames: []string{"cpu.usage", "memory.usage"},
			wantCount:   2,
		},
		{
			name:        "query non-existent metric",
			metricNames: []string{"nonexistent"},
			wantCount:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := storage.QueryMetrics(ctx, now.Add(-1*time.Minute), now.Add(1*time.Minute), tt.metricNames)
			require.NoError(t, err)
			assert.Equal(t, tt.wantCount, len(results))
		})
	}
}

func TestCleanupOldMetrics(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	require.NoError(t, err)
	defer db.Close()

	logger := zerolog.Nop()
	storage, err := NewStorage(db, logger)
	require.NoError(t, err)

	ctx := context.Background()
	now := time.Now()

	// Insert metrics at different ages.
	metrics := []Metric{
		{
			Timestamp:  now.Add(-2 * time.Hour),
			Name:       "very.old",
			Value:      1.0,
			Unit:       "count",
			MetricType: "counter",
			Attributes: map[string]string{},
		},
		{
			Timestamp:  now.Add(-30 * time.Minute),
			Name:       "medium.old",
			Value:      2.0,
			Unit:       "count",
			MetricType: "counter",
			Attributes: map[string]string{},
		},
		{
			Timestamp:  now,
			Name:       "recent",
			Value:      3.0,
			Unit:       "count",
			MetricType: "counter",
			Attributes: map[string]string{},
		},
	}

	err = storage.StoreMetrics(ctx, metrics)
	require.NoError(t, err)

	// Cleanup metrics older than 1 hour.
	err = storage.CleanupOldMetrics(ctx, 1*time.Hour)
	require.NoError(t, err)

	// Query remaining metrics.
	results, err := storage.QueryMetrics(ctx, now.Add(-3*time.Hour), now.Add(1*time.Minute), nil)
	require.NoError(t, err)

	// Should only have the recent and medium.old metrics.
	assert.LessOrEqual(t, len(results), 2, "Cleanup should remove old metrics")
}

func TestQueryMetricsBySeqID(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	require.NoError(t, err)
	defer db.Close()

	logger := zerolog.Nop()
	storage, err := NewStorage(db, logger)
	require.NoError(t, err)

	ctx := context.Background()
	now := time.Now()

	// Insert 5 metrics.
	metrics := make([]Metric, 5)
	for i := range metrics {
		metrics[i] = Metric{
			Timestamp:  now.Add(-time.Duration(5-i) * time.Minute),
			Name:       "cpu.usage",
			Value:      float64(i * 10),
			Unit:       "percent",
			MetricType: "gauge",
			Attributes: map[string]string{},
		}
	}
	require.NoError(t, storage.StoreMetrics(ctx, metrics))

	// Query all (seq_id > 0).
	results, maxSeqID, err := storage.QueryMetricsBySeqID(ctx, 0, 100, nil)
	require.NoError(t, err)
	assert.Len(t, results, 5)
	assert.Greater(t, maxSeqID, uint64(0))

	// Query from max should return empty.
	results2, maxSeqID2, err := storage.QueryMetricsBySeqID(ctx, maxSeqID, 100, nil)
	require.NoError(t, err)
	assert.Len(t, results2, 0)
	assert.Equal(t, uint64(0), maxSeqID2)

	// Insert 3 more and query from previous max.
	more := make([]Metric, 3)
	for i := range more {
		more[i] = Metric{
			Timestamp:  now,
			Name:       "mem.usage",
			Value:      float64(i * 100),
			Unit:       "bytes",
			MetricType: "gauge",
			Attributes: map[string]string{},
		}
	}
	require.NoError(t, storage.StoreMetrics(ctx, more))

	results3, maxSeqID3, err := storage.QueryMetricsBySeqID(ctx, maxSeqID, 100, nil)
	require.NoError(t, err)
	assert.Len(t, results3, 3)
	assert.Greater(t, maxSeqID3, maxSeqID)
}

func TestSeqID_MetricsMonotonicallyIncreasing(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	require.NoError(t, err)
	defer db.Close()

	logger := zerolog.Nop()
	storage, err := NewStorage(db, logger)
	require.NoError(t, err)

	ctx := context.Background()
	now := time.Now()

	// Insert metrics in multiple batches.
	for batch := 0; batch < 3; batch++ {
		metrics := make([]Metric, 5)
		for i := range metrics {
			metrics[i] = Metric{
				Timestamp:  now.Add(-time.Duration(batch*5+i) * time.Second),
				Name:       "cpu.usage",
				Value:      float64(batch*5 + i),
				Unit:       "percent",
				MetricType: "gauge",
				Attributes: map[string]string{},
			}
		}
		require.NoError(t, storage.StoreMetrics(ctx, metrics))
	}

	results, _, err := storage.QueryMetricsBySeqID(ctx, 0, 100, nil)
	require.NoError(t, err)
	assert.Len(t, results, 15)

	var prevSeqID uint64
	for _, m := range results {
		assert.Greater(t, m.SeqID, prevSeqID, "seq_ids must be monotonically increasing")
		prevSeqID = m.SeqID
	}
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	assert.True(t, config.Enabled)
	assert.Equal(t, 15*time.Second, config.Interval)
	assert.True(t, config.CPUEnabled)
	assert.True(t, config.MemoryEnabled)
	assert.True(t, config.DiskEnabled)
	assert.True(t, config.NetworkEnabled)
}

func TestNewSystemCollector(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	require.NoError(t, err)
	defer db.Close()

	logger := zerolog.Nop()
	storage, err := NewStorage(db, logger)
	require.NoError(t, err)

	config := DefaultConfig()
	collector := NewSystemCollector(storage, config, logger)

	assert.NotNil(t, collector)
	assert.Equal(t, storage, collector.storage)
	assert.Equal(t, config.Interval, collector.interval)
}
