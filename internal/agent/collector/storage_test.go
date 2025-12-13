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
