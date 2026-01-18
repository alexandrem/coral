// Package bootstrap implements agent certificate bootstrap for mTLS.
// This file provides telemetry metrics for bootstrap operations (RFD 048).
package bootstrap

import (
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// MetricResult represents the outcome of a bootstrap/renewal operation.
type MetricResult string

const (
	// MetricResultSuccess indicates the operation succeeded.
	MetricResultSuccess MetricResult = "success"

	// MetricResultFailure indicates the operation failed.
	MetricResultFailure MetricResult = "failure"

	// MetricResultTimeout indicates the operation timed out.
	MetricResultTimeout MetricResult = "timeout"

	// MetricResultFallback indicates fallback to colony_secret was used.
	MetricResultFallback MetricResult = "fallback"
)

// Metrics provides telemetry tracking for bootstrap and renewal operations.
// Implements RFD 048 observability requirements:
// - coral_agent_bootstrap_attempts_total{result="success|failure|timeout"}
// - coral_agent_bootstrap_duration_seconds
// - coral_agent_renewal_attempts_total{result="success|failure"}
// - coral_agent_renewal_duration_seconds
type Metrics struct {
	logger zerolog.Logger

	mu sync.RWMutex

	// Bootstrap metrics.
	bootstrapTotal     map[MetricResult]int64
	bootstrapDurations []float64

	// Renewal metrics.
	renewalTotal     map[MetricResult]int64
	renewalDurations []float64
}

// NewMetrics creates a new Metrics instance.
func NewMetrics(logger zerolog.Logger) *Metrics {
	return &Metrics{
		logger:         logger,
		bootstrapTotal: make(map[MetricResult]int64),
		renewalTotal:   make(map[MetricResult]int64),
	}
}

// RecordBootstrapAttempt records a bootstrap attempt with its result and duration.
func (m *Metrics) RecordBootstrapAttempt(result MetricResult, duration time.Duration, agentID, colonyID string, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.bootstrapTotal[result]++
	durationSec := duration.Seconds()
	m.bootstrapDurations = append(m.bootstrapDurations, durationSec)

	// Log structured telemetry event.
	event := m.logger.Info().
		Str("metric", "coral_agent_bootstrap_attempt").
		Str("result", string(result)).
		Float64("duration_seconds", durationSec).
		Str("agent_id", agentID).
		Str("colony_id", colonyID)

	if errMsg != "" {
		event = event.Str("error", errMsg)
	}

	event.Msg("Bootstrap attempt recorded")
}

// RecordRenewalAttempt records a renewal attempt with its result and duration.
func (m *Metrics) RecordRenewalAttempt(result MetricResult, duration time.Duration, agentID, colonyID string, usedMTLS bool, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.renewalTotal[result]++
	durationSec := duration.Seconds()
	m.renewalDurations = append(m.renewalDurations, durationSec)

	// Log structured telemetry event.
	event := m.logger.Info().
		Str("metric", "coral_agent_renewal_attempt").
		Str("result", string(result)).
		Float64("duration_seconds", durationSec).
		Str("agent_id", agentID).
		Str("colony_id", colonyID).
		Bool("mtls_auth", usedMTLS)

	if errMsg != "" {
		event = event.Str("error", errMsg)
	}

	event.Msg("Renewal attempt recorded")
}

// GetBootstrapStats returns current bootstrap statistics.
func (m *Metrics) GetBootstrapStats() BootstrapStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	total := int64(0)
	for _, count := range m.bootstrapTotal {
		total += count
	}

	var avgDuration float64
	if len(m.bootstrapDurations) > 0 {
		sum := 0.0
		for _, d := range m.bootstrapDurations {
			sum += d
		}
		avgDuration = sum / float64(len(m.bootstrapDurations))
	}

	return BootstrapStats{
		TotalAttempts:          total,
		SuccessCount:           m.bootstrapTotal[MetricResultSuccess],
		FailureCount:           m.bootstrapTotal[MetricResultFailure],
		TimeoutCount:           m.bootstrapTotal[MetricResultTimeout],
		FallbackCount:          m.bootstrapTotal[MetricResultFallback],
		AverageDurationSeconds: avgDuration,
	}
}

// GetRenewalStats returns current renewal statistics.
func (m *Metrics) GetRenewalStats() RenewalStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	total := int64(0)
	for _, count := range m.renewalTotal {
		total += count
	}

	var avgDuration float64
	if len(m.renewalDurations) > 0 {
		sum := 0.0
		for _, d := range m.renewalDurations {
			sum += d
		}
		avgDuration = sum / float64(len(m.renewalDurations))
	}

	return RenewalStats{
		TotalAttempts:          total,
		SuccessCount:           m.renewalTotal[MetricResultSuccess],
		FailureCount:           m.renewalTotal[MetricResultFailure],
		AverageDurationSeconds: avgDuration,
	}
}

// BootstrapStats contains aggregated bootstrap statistics.
type BootstrapStats struct {
	TotalAttempts          int64   `json:"total_attempts"`
	SuccessCount           int64   `json:"success_count"`
	FailureCount           int64   `json:"failure_count"`
	TimeoutCount           int64   `json:"timeout_count"`
	FallbackCount          int64   `json:"fallback_count"`
	AverageDurationSeconds float64 `json:"average_duration_seconds"`
}

// RenewalStats contains aggregated renewal statistics.
type RenewalStats struct {
	TotalAttempts          int64   `json:"total_attempts"`
	SuccessCount           int64   `json:"success_count"`
	FailureCount           int64   `json:"failure_count"`
	AverageDurationSeconds float64 `json:"average_duration_seconds"`
}

// Global metrics instance for convenience.
var globalMetrics *Metrics
var metricsOnce sync.Once

// GetGlobalMetrics returns the global metrics instance.
func GetGlobalMetrics() *Metrics {
	metricsOnce.Do(func() {
		globalMetrics = NewMetrics(zerolog.Nop())
	})
	return globalMetrics
}

// InitGlobalMetrics initializes the global metrics with a logger.
func InitGlobalMetrics(logger zerolog.Logger) {
	metricsOnce.Do(func() {
		globalMetrics = NewMetrics(logger)
	})
}
