// Package collector implements system metrics collection.
package collector

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

// SystemCollector collects host system metrics (CPU, Memory, Disk, Network) using gopsutil.
// Metrics are sampled at a configurable interval (default: 15s) and stored locally.
type SystemCollector struct {
	storage  *Storage
	logger   zerolog.Logger
	interval time.Duration
	config   Config
}

// Config configures which collectors are enabled.
type Config struct {
	Enabled        bool
	Interval       time.Duration
	CPUEnabled     bool
	MemoryEnabled  bool
	DiskEnabled    bool
	NetworkEnabled bool
}

// DefaultConfig returns the default collector configuration.
func DefaultConfig() Config {
	return Config{
		Enabled:        true,
		Interval:       15 * time.Second,
		CPUEnabled:     true,
		MemoryEnabled:  true,
		DiskEnabled:    true,
		NetworkEnabled: true,
	}
}

// NewSystemCollector creates a new system metrics collector.
func NewSystemCollector(storage *Storage, config Config, logger zerolog.Logger) *SystemCollector {
	return &SystemCollector{
		storage:  storage,
		logger:   logger.With().Str("component", "system_collector").Logger(),
		interval: config.Interval,
		config:   config,
	}
}

// Start begins collecting system metrics at the configured interval.
// It runs until the context is cancelled.
func (c *SystemCollector) Start(ctx context.Context) error {
	if !c.config.Enabled {
		c.logger.Info().Msg("System metrics collector is disabled")
		return nil
	}

	c.logger.Info().
		Dur("interval", c.interval).
		Bool("cpu", c.config.CPUEnabled).
		Bool("memory", c.config.MemoryEnabled).
		Bool("disk", c.config.DiskEnabled).
		Bool("network", c.config.NetworkEnabled).
		Msg("Starting system metrics collector")

	// Collect immediately on start.
	if err := c.collectAndStore(ctx); err != nil {
		c.logger.Error().Err(err).Msg("Initial system metrics collection failed")
	}

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.logger.Info().Msg("Stopping system metrics collector")
			return ctx.Err()
		case <-ticker.C:
			if err := c.collectAndStore(ctx); err != nil {
				c.logger.Error().Err(err).Msg("Failed to collect system metrics")
			}
		}
	}
}

// collectAndStore collects all enabled system metrics and stores them.
func (c *SystemCollector) collectAndStore(ctx context.Context) error {
	timestamp := time.Now()
	var metrics []Metric

	// Collect CPU metrics.
	if c.config.CPUEnabled {
		cpuMetrics, err := c.collectCPU(timestamp)
		if err != nil {
			c.logger.Warn().Err(err).Msg("Failed to collect CPU metrics")
		} else {
			metrics = append(metrics, cpuMetrics...)
		}
	}

	// Collect memory metrics.
	if c.config.MemoryEnabled {
		memMetrics, err := c.collectMemory(timestamp)
		if err != nil {
			c.logger.Warn().Err(err).Msg("Failed to collect memory metrics")
		} else {
			metrics = append(metrics, memMetrics...)
		}
	}

	// Collect disk metrics.
	if c.config.DiskEnabled {
		diskMetrics, err := c.collectDisk(timestamp)
		if err != nil {
			c.logger.Warn().Err(err).Msg("Failed to collect disk metrics")
		} else {
			metrics = append(metrics, diskMetrics...)
		}
	}

	// Collect network metrics.
	if c.config.NetworkEnabled {
		netMetrics, err := c.collectNetwork(timestamp)
		if err != nil {
			c.logger.Warn().Err(err).Msg("Failed to collect network metrics")
		} else {
			metrics = append(metrics, netMetrics...)
		}
	}

	// Store all metrics in a single transaction.
	if len(metrics) > 0 {
		if err := c.storage.StoreMetrics(ctx, metrics); err != nil {
			return fmt.Errorf("failed to store metrics: %w", err)
		}

		c.logger.Debug().
			Int("count", len(metrics)).
			Msg("Stored system metrics")
	}

	return nil
}

// collectCPU collects CPU utilization metrics.
func (c *SystemCollector) collectCPU(timestamp time.Time) ([]Metric, error) {
	// Get aggregate CPU utilization (all cores).
	percentages, err := cpu.Percent(0, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get CPU percent: %w", err)
	}

	if len(percentages) == 0 {
		return nil, fmt.Errorf("no CPU percentages returned")
	}

	metrics := []Metric{
		{
			Timestamp:  timestamp,
			Name:       "system.cpu.utilization",
			Value:      percentages[0],
			Unit:       "percent",
			MetricType: "gauge",
			Attributes: make(map[string]string),
		},
	}

	// Get CPU times for rate calculations.
	times, err := cpu.Times(false)
	if err == nil && len(times) > 0 {
		// Total CPU time (cumulative seconds).
		totalTime := times[0].User + times[0].System + times[0].Idle + times[0].Nice +
			times[0].Iowait + times[0].Irq + times[0].Softirq + times[0].Steal

		metrics = append(metrics, Metric{
			Timestamp:  timestamp,
			Name:       "system.cpu.time",
			Value:      totalTime,
			Unit:       "seconds",
			MetricType: "counter",
			Attributes: make(map[string]string),
		})
	}

	return metrics, nil
}

// collectMemory collects memory usage metrics.
func (c *SystemCollector) collectMemory(timestamp time.Time) ([]Metric, error) {
	vmStat, err := mem.VirtualMemory()
	if err != nil {
		return nil, fmt.Errorf("failed to get memory stats: %w", err)
	}

	metrics := []Metric{
		{
			Timestamp:  timestamp,
			Name:       "system.memory.usage",
			Value:      float64(vmStat.Used),
			Unit:       "bytes",
			MetricType: "gauge",
			Attributes: make(map[string]string),
		},
		{
			Timestamp:  timestamp,
			Name:       "system.memory.limit",
			Value:      float64(vmStat.Total),
			Unit:       "bytes",
			MetricType: "gauge",
			Attributes: make(map[string]string),
		},
		{
			Timestamp:  timestamp,
			Name:       "system.memory.utilization",
			Value:      vmStat.UsedPercent,
			Unit:       "percent",
			MetricType: "gauge",
			Attributes: make(map[string]string),
		},
	}

	return metrics, nil
}

// collectDisk collects disk I/O and usage metrics.
func (c *SystemCollector) collectDisk(timestamp time.Time) ([]Metric, error) {
	var metrics []Metric

	// Disk I/O counters (aggregate across all devices).
	ioCounters, err := disk.IOCounters()
	if err == nil {
		var totalReadBytes, totalWriteBytes uint64
		for _, counter := range ioCounters {
			totalReadBytes += counter.ReadBytes
			totalWriteBytes += counter.WriteBytes
		}

		metrics = append(metrics,
			Metric{
				Timestamp:  timestamp,
				Name:       "system.disk.io.read",
				Value:      float64(totalReadBytes),
				Unit:       "bytes",
				MetricType: "counter",
				Attributes: make(map[string]string),
			},
			Metric{
				Timestamp:  timestamp,
				Name:       "system.disk.io.write",
				Value:      float64(totalWriteBytes),
				Unit:       "bytes",
				MetricType: "counter",
				Attributes: make(map[string]string),
			},
		)
	}

	// Disk usage (root partition).
	usage, err := disk.Usage("/")
	if err == nil {
		metrics = append(metrics,
			Metric{
				Timestamp:  timestamp,
				Name:       "system.disk.usage",
				Value:      float64(usage.Used),
				Unit:       "bytes",
				MetricType: "gauge",
				Attributes: map[string]string{"mount": "/"},
			},
			Metric{
				Timestamp:  timestamp,
				Name:       "system.disk.utilization",
				Value:      usage.UsedPercent,
				Unit:       "percent",
				MetricType: "gauge",
				Attributes: map[string]string{"mount": "/"},
			},
		)
	}

	return metrics, nil
}

// collectNetwork collects network I/O metrics.
func (c *SystemCollector) collectNetwork(timestamp time.Time) ([]Metric, error) {
	// Get network I/O counters (aggregate across all interfaces except loopback).
	ioCounters, err := net.IOCounters(false)
	if err != nil {
		return nil, fmt.Errorf("failed to get network stats: %w", err)
	}

	if len(ioCounters) == 0 {
		return nil, nil
	}

	// First counter contains aggregate stats.
	counter := ioCounters[0]

	metrics := []Metric{
		{
			Timestamp:  timestamp,
			Name:       "system.network.io.receive",
			Value:      float64(counter.BytesRecv),
			Unit:       "bytes",
			MetricType: "counter",
			Attributes: make(map[string]string),
		},
		{
			Timestamp:  timestamp,
			Name:       "system.network.io.transmit",
			Value:      float64(counter.BytesSent),
			Unit:       "bytes",
			MetricType: "counter",
			Attributes: make(map[string]string),
		},
		{
			Timestamp:  timestamp,
			Name:       "system.network.errors.receive",
			Value:      float64(counter.Errin),
			Unit:       "count",
			MetricType: "counter",
			Attributes: make(map[string]string),
		},
		{
			Timestamp:  timestamp,
			Name:       "system.network.errors.transmit",
			Value:      float64(counter.Errout),
			Unit:       "count",
			MetricType: "counter",
			Attributes: make(map[string]string),
		},
	}

	return metrics, nil
}
