package bootstrap

import (
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
)

func TestMetrics_RecordBootstrapAttempt(t *testing.T) {
	logger := zerolog.Nop()
	metrics := NewMetrics(logger)

	// Record some bootstrap attempts.
	metrics.RecordBootstrapAttempt(MetricResultSuccess, 5*time.Second, "agent-1", "colony-1", "")
	metrics.RecordBootstrapAttempt(MetricResultSuccess, 3*time.Second, "agent-2", "colony-1", "")
	metrics.RecordBootstrapAttempt(MetricResultFailure, 10*time.Second, "agent-3", "colony-1", "connection failed")
	metrics.RecordBootstrapAttempt(MetricResultTimeout, 30*time.Second, "agent-4", "colony-1", "context deadline exceeded")
	metrics.RecordBootstrapAttempt(MetricResultFallback, 2*time.Second, "agent-5", "colony-1", "using colony_secret")

	stats := metrics.GetBootstrapStats()

	assert.Equal(t, int64(5), stats.TotalAttempts)
	assert.Equal(t, int64(2), stats.SuccessCount)
	assert.Equal(t, int64(1), stats.FailureCount)
	assert.Equal(t, int64(1), stats.TimeoutCount)
	assert.Equal(t, int64(1), stats.FallbackCount)
	assert.InDelta(t, 10.0, stats.AverageDurationSeconds, 0.1) // (5+3+10+30+2)/5 = 10
}

func TestMetrics_RecordRenewalAttempt(t *testing.T) {
	logger := zerolog.Nop()
	metrics := NewMetrics(logger)

	// Record some renewal attempts.
	metrics.RecordRenewalAttempt(MetricResultSuccess, 2*time.Second, "agent-1", "colony-1", true, "")
	metrics.RecordRenewalAttempt(MetricResultSuccess, 4*time.Second, "agent-2", "colony-1", true, "")
	metrics.RecordRenewalAttempt(MetricResultFailure, 1*time.Second, "agent-3", "colony-1", false, "certificate expired")

	stats := metrics.GetRenewalStats()

	assert.Equal(t, int64(3), stats.TotalAttempts)
	assert.Equal(t, int64(2), stats.SuccessCount)
	assert.Equal(t, int64(1), stats.FailureCount)
	assert.InDelta(t, 2.33, stats.AverageDurationSeconds, 0.1) // (2+4+1)/3 = 2.33
}

func TestMetrics_EmptyStats(t *testing.T) {
	logger := zerolog.Nop()
	metrics := NewMetrics(logger)

	bootstrapStats := metrics.GetBootstrapStats()
	assert.Equal(t, int64(0), bootstrapStats.TotalAttempts)
	assert.Equal(t, float64(0), bootstrapStats.AverageDurationSeconds)

	renewalStats := metrics.GetRenewalStats()
	assert.Equal(t, int64(0), renewalStats.TotalAttempts)
	assert.Equal(t, float64(0), renewalStats.AverageDurationSeconds)
}

func TestMetrics_ConcurrentAccess(t *testing.T) {
	logger := zerolog.Nop()
	metrics := NewMetrics(logger)

	// Run concurrent recordings.
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				if j%2 == 0 {
					metrics.RecordBootstrapAttempt(MetricResultSuccess, time.Second, "agent", "colony", "")
				} else {
					metrics.RecordRenewalAttempt(MetricResultSuccess, time.Second, "agent", "colony", true, "")
				}
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines.
	for i := 0; i < 10; i++ {
		<-done
	}

	// Check totals.
	bootstrapStats := metrics.GetBootstrapStats()
	renewalStats := metrics.GetRenewalStats()

	// Each goroutine does 50 bootstrap and 50 renewal attempts.
	assert.Equal(t, int64(500), bootstrapStats.TotalAttempts)
	assert.Equal(t, int64(500), renewalStats.TotalAttempts)
}

func TestNewMetrics(t *testing.T) {
	logger := zerolog.Nop()
	metrics := NewMetrics(logger)

	assert.NotNil(t, metrics)
	assert.NotNil(t, metrics.bootstrapTotal)
	assert.NotNil(t, metrics.renewalTotal)
}
