package beyla

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	ebpfpb "github.com/coral-io/coral/coral/mesh/v1"
	"github.com/coral-io/coral/internal/agent/telemetry"
	"github.com/rs/zerolog"
)

// Manager handles Beyla lifecycle within Coral agent (RFD 032).
type Manager struct {
	ctx     context.Context
	cancel  context.CancelFunc
	config  *Config
	logger  zerolog.Logger
	mu      sync.RWMutex
	running bool

	// OTLP receiver from RFD 025 (receives Beyla's trace and metrics output).
	otlpReceiver *telemetry.OTLPReceiver
	storage      *telemetry.Storage
	transformer  *Transformer

	// Beyla metrics storage (local DuckDB for pull-based queries).
	beylaStorage *BeylaStorage

	// Beyla process management.
	beylaCmd        *exec.Cmd
	beylaBinaryPath string // Path to extracted binary (cleanup on stop)

	// Channels for processed Beyla data.
	tracesCh  chan *BeylaTrace       // Beyla traces ready for Colony
	metricsCh chan *ebpfpb.EbpfEvent // Beyla metrics ready for Colony
}

// BeylaTrace represents a processed Beyla trace ready for Colony.
type BeylaTrace struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
	ServiceName  string
	SpanName     string
	SpanKind     string
	StartTime    string // ISO 8601
	DurationUs   int64
	StatusCode   int
	Attributes   map[string]string
}

// Config contains Beyla manager configuration.
type Config struct {
	Enabled bool

	// Discovery configuration.
	Discovery DiscoveryConfig

	// Protocol filters.
	Protocols ProtocolsConfig

	// Performance tuning.
	SamplingRate float64 // 0.0-1.0

	// OTLP export configuration.
	OTLPEndpoint string // e.g., "localhost:4318"

	// Resource attributes for enrichment.
	Attributes map[string]string

	// Database for local storage (required for OTLP receiver).
	DB *sql.DB

	// Database file path (optional, for HTTP serving via RFD 039).
	// If empty, the database cannot be served over HTTP.
	DBPath string

	// Local storage retention for Beyla metrics in hours (default: 1 hour).
	// This controls how long metrics are kept in agent's local DuckDB before cleanup.
	// Colony queries metrics within this window, so this should be >= colony poll interval.
	StorageRetentionHours int
}

// DiscoveryConfig specifies which processes to instrument.
type DiscoveryConfig struct {
	// Instrument processes listening on these ports.
	OpenPorts []int

	// Kubernetes-based discovery (for node agents).
	K8sNamespaces []string
	K8sPodLabels  map[string]string

	// Process name patterns.
	ProcessNames []string
}

// ProtocolsConfig enables/disables specific protocols.
type ProtocolsConfig struct {
	HTTPEnabled  bool
	GRPCEnabled  bool
	SQLEnabled   bool
	KafkaEnabled bool
	RedisEnabled bool
}

// NewManager creates a new Beyla manager.
func NewManager(ctx context.Context, config *Config, logger zerolog.Logger) (*Manager, error) {
	if config == nil {
		return nil, fmt.Errorf("config is required")
	}

	if !config.Enabled {
		logger.Info().Msg("Beyla integration is disabled")
		return &Manager{
			logger: logger.With().Str("component", "beyla_manager").Logger(),
			config: config,
		}, nil
	}

	ctx, cancel := context.WithCancel(ctx)

	m := &Manager{
		ctx:         ctx,
		cancel:      cancel,
		config:      config,
		logger:      logger.With().Str("component", "beyla_manager").Logger(),
		transformer: NewTransformer(logger),
		tracesCh:    make(chan *BeylaTrace, 100),
		metricsCh:   make(chan *ebpfpb.EbpfEvent, 100),
	}

	// Initialize OTLP receiver storage (RFD 025).
	if config.DB != nil {
		storage, err := telemetry.NewStorage(config.DB, logger)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("failed to create telemetry storage: %w", err)
		}
		m.storage = storage

		// Initialize Beyla metrics storage (RFD 032 Phase 4).
		beylaStorage, err := NewBeylaStorage(config.DB, config.DBPath, logger)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("failed to create Beyla storage: %w", err)
		}
		m.beylaStorage = beylaStorage
	} else {
		m.logger.Warn().Msg("No database provided; OTLP receiver will not be available")
	}

	return m, nil
}

// Start begins Beyla instrumentation.
func (m *Manager) Start() error {
	if !m.config.Enabled {
		m.logger.Info().Msg("Beyla is disabled, skipping start")
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return fmt.Errorf("Beyla manager already running")
	}

	m.logger.Info().
		Str("otlp_endpoint", m.config.OTLPEndpoint).
		Float64("sampling_rate", m.config.SamplingRate).
		Msg("Starting Beyla manager")

	// Start OTLP receiver to consume Beyla output (RFD 025).
	// Only start if database was provided; otherwise skip gracefully.
	if m.storage != nil {
		if err := m.startOTLPReceiver(); err != nil {
			return fmt.Errorf("failed to start OTLP receiver: %w", err)
		}
	} else {
		m.logger.Warn().Msg("Skipping OTLP receiver start (no database provided)")
	}

	// TODO(RFD 032): Start Beyla instrumentation goroutine.
	// This will use Beyla's Go library API when integrated.
	if err := m.startBeyla(); err != nil {
		return fmt.Errorf("failed to start Beyla: %w", err)
	}

	m.running = true
	m.logger.Info().Msg("Beyla manager started successfully")
	return nil
}

// Stop gracefully shuts down Beyla.
func (m *Manager) Stop() error {
	if !m.config.Enabled {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return nil
	}

	m.logger.Info().Msg("Stopping Beyla manager")

	// Stop Beyla process.
	if m.beylaCmd != nil && m.beylaCmd.Process != nil {
		m.logger.Info().Msg("Stopping Beyla process")
		if err := m.beylaCmd.Process.Kill(); err != nil {
			m.logger.Error().Err(err).Msg("Failed to kill Beyla process")
		}
		// Wait for process to exit.
		_ = m.beylaCmd.Wait()
	}

	// Cleanup extracted binary if it was in a temp directory.
	if m.beylaBinaryPath != "" && filepath.HasPrefix(m.beylaBinaryPath, os.TempDir()) {
		tmpDir := filepath.Dir(m.beylaBinaryPath)
		if err := os.RemoveAll(tmpDir); err != nil {
			m.logger.Error().Err(err).Str("path", tmpDir).Msg("Failed to cleanup temp Beyla binary")
		} else {
			m.logger.Debug().Str("path", tmpDir).Msg("Cleaned up temp Beyla binary")
		}
	}

	// Stop OTLP receiver.
	if m.otlpReceiver != nil {
		if err := m.otlpReceiver.Stop(); err != nil {
			m.logger.Error().Err(err).Msg("Failed to stop OTLP receiver")
		}
	}

	// Cancel context to stop all goroutines.
	m.cancel()

	// Close channels.
	close(m.tracesCh)
	close(m.metricsCh)

	m.running = false
	m.logger.Info().Msg("Beyla manager stopped")
	return nil
}

// GetTraces returns a channel receiving Beyla traces ready for Colony.
func (m *Manager) GetTraces() <-chan *BeylaTrace {
	return m.tracesCh
}

// GetMetrics returns a channel receiving Beyla metrics ready for Colony.
func (m *Manager) GetMetrics() <-chan *ebpfpb.EbpfEvent {
	return m.metricsCh
}

// QueryHTTPMetrics queries Beyla HTTP metrics from local storage (RFD 032 Phase 4).
// This is called by the QueryBeylaMetrics RPC handler (colony → agent pull-based).
func (m *Manager) QueryHTTPMetrics(ctx context.Context, startTime, endTime time.Time, serviceNames []string) ([]*ebpfpb.BeylaHttpMetrics, error) {
	if m.beylaStorage == nil {
		return nil, fmt.Errorf("Beyla storage not initialized")
	}
	return m.beylaStorage.QueryHTTPMetrics(ctx, startTime, endTime, serviceNames)
}

// QueryGRPCMetrics queries Beyla gRPC metrics from local storage (RFD 032 Phase 4).
// This is called by the QueryBeylaMetrics RPC handler (colony → agent pull-based).
func (m *Manager) QueryGRPCMetrics(ctx context.Context, startTime, endTime time.Time, serviceNames []string) ([]*ebpfpb.BeylaGrpcMetrics, error) {
	if m.beylaStorage == nil {
		return nil, fmt.Errorf("Beyla storage not initialized")
	}
	return m.beylaStorage.QueryGRPCMetrics(ctx, startTime, endTime, serviceNames)
}

// QuerySQLMetrics queries Beyla SQL metrics from local storage (RFD 032 Phase 4).
// This is called by the QueryBeylaMetrics RPC handler (colony → agent pull-based).
func (m *Manager) QuerySQLMetrics(ctx context.Context, startTime, endTime time.Time, serviceNames []string) ([]*ebpfpb.BeylaSqlMetrics, error) {
	if m.beylaStorage == nil {
		return nil, fmt.Errorf("Beyla storage not initialized")
	}
	return m.beylaStorage.QuerySQLMetrics(ctx, startTime, endTime, serviceNames)
}

// IsRunning returns whether Beyla is currently running.
func (m *Manager) IsRunning() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.running
}

// GetCapabilities returns Beyla capabilities for this system.
func (m *Manager) GetCapabilities() *Capabilities {
	caps := &Capabilities{
		Enabled:            m.config.Enabled,
		Version:            "stub", // Will be actual Beyla version when integrated
		SupportedProtocols: []string{},
		SupportedRuntimes:  []string{},
		TracingEnabled:     false,
	}

	if !m.config.Enabled {
		return caps
	}

	// Report enabled protocols.
	if m.config.Protocols.HTTPEnabled {
		caps.SupportedProtocols = append(caps.SupportedProtocols, "http", "http2")
	}
	if m.config.Protocols.GRPCEnabled {
		caps.SupportedProtocols = append(caps.SupportedProtocols, "grpc")
	}
	if m.config.Protocols.SQLEnabled {
		caps.SupportedProtocols = append(caps.SupportedProtocols, "postgresql", "mysql")
	}
	if m.config.Protocols.KafkaEnabled {
		caps.SupportedProtocols = append(caps.SupportedProtocols, "kafka")
	}
	if m.config.Protocols.RedisEnabled {
		caps.SupportedProtocols = append(caps.SupportedProtocols, "redis")
	}

	// Beyla supports multiple runtimes.
	caps.SupportedRuntimes = []string{"go", "java", "python", "nodejs", "dotnet", "ruby", "rust"}
	caps.TracingEnabled = true

	return caps
}

// Capabilities describes Beyla integration features.
type Capabilities struct {
	Enabled            bool
	Version            string
	SupportedProtocols []string
	SupportedRuntimes  []string
	TracingEnabled     bool
}

// GetDatabasePath returns the file path to the Beyla DuckDB database (RFD 039).
// Returns empty string if Beyla storage is not initialized or database is in-memory.
func (m *Manager) GetDatabasePath() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.beylaStorage == nil {
		return ""
	}

	return m.beylaStorage.GetDatabasePath()
}

// startOTLPReceiver starts an embedded OTLP receiver using RFD 025 infrastructure.
func (m *Manager) startOTLPReceiver() error {
	if m.storage == nil {
		return fmt.Errorf("storage not initialized; database required for OTLP receiver")
	}

	m.logger.Info().
		Str("endpoint", m.config.OTLPEndpoint).
		Msg("Starting OTLP receiver (RFD 025)")

	// Configure OTLP receiver to use Beyla's endpoint.
	// Parse the endpoint to get gRPC and HTTP endpoints.
	// Default: localhost:4318 for HTTP (Beyla default)
	otlpConfig := telemetry.Config{
		Disabled:              false,
		GRPCEndpoint:          "127.0.0.1:4317", // Standard OTLP gRPC port
		HTTPEndpoint:          m.config.OTLPEndpoint,
		StorageRetentionHours: 1,
		Filters: telemetry.FilterConfig{
			AlwaysCaptureErrors:    true,
			HighLatencyThresholdMs: 500.0,
			SampleRate:             m.config.SamplingRate,
		},
	}

	// Create OTLP receiver.
	receiver, err := telemetry.NewOTLPReceiver(otlpConfig, m.storage, m.logger)
	if err != nil {
		return fmt.Errorf("failed to create OTLP receiver: %w", err)
	}

	// Start receiver.
	if err := receiver.Start(m.ctx); err != nil {
		return fmt.Errorf("failed to start OTLP receiver: %w", err)
	}

	m.otlpReceiver = receiver

	// Start trace and metrics consumer goroutines.
	go m.consumeTraces()
	go m.consumeMetrics()

	// Start Beyla metrics cleanup loop (RFD 032 Phase 4).
	if m.beylaStorage != nil {
		// Use configured retention or default to 1 hour.
		retention := 1 * time.Hour
		if m.config.StorageRetentionHours > 0 {
			retention = time.Duration(m.config.StorageRetentionHours) * time.Hour
		}
		m.logger.Info().
			Dur("retention", retention).
			Msg("Starting Beyla metrics cleanup loop")
		go m.beylaStorage.RunCleanupLoop(m.ctx, retention)
	}

	m.logger.Info().Msg("OTLP receiver started successfully")
	return nil
}

// consumeTraces consumes traces from the OTLP receiver and transforms them for Beyla.
func (m *Manager) consumeTraces() {
	m.logger.Info().Msg("Starting trace consumer")

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			m.logger.Info().Msg("Trace consumer stopped")
			return

		case <-ticker.C:
			// Query recent spans from storage.
			endTime := time.Now()
			startTime := endTime.Add(-10 * time.Second)

			spans, err := m.otlpReceiver.QuerySpans(m.ctx, startTime, endTime, nil)
			if err != nil {
				m.logger.Error().Err(err).Msg("Failed to query spans")
				continue
			}

			// Transform and forward spans.
			for i := range spans {
				trace := m.transformSpanToBeylaTrace(&spans[i])
				select {
				case m.tracesCh <- trace:
				case <-m.ctx.Done():
					return
				default:
					// Channel full, drop span.
					m.logger.Warn().Msg("Trace channel full, dropping span")
				}
			}

			if len(spans) > 0 {
				m.logger.Debug().Int("count", len(spans)).Msg("Processed Beyla traces")
			}
		}
	}
}

// transformSpanToBeylaTrace transforms an OTLP span to Beyla trace format.
func (m *Manager) transformSpanToBeylaTrace(span *telemetry.Span) *BeylaTrace {
	return &BeylaTrace{
		TraceID:      span.TraceID,
		SpanID:       span.SpanID,
		ParentSpanID: "", // TODO: Extract from span if available
		ServiceName:  span.ServiceName,
		SpanName:     span.HTTPRoute, // Use HTTP route as span name
		SpanKind:     span.SpanKind,
		StartTime:    span.Timestamp.Format(time.RFC3339),
		DurationUs:   int64(span.DurationMs * 1000), // Convert ms to microseconds
		StatusCode:   span.HTTPStatus,
		Attributes:   span.Attributes,
	}
}

// consumeMetrics consumes metrics from the OTLP receiver and transforms them for Beyla.
func (m *Manager) consumeMetrics() {
	m.logger.Info().Msg("Starting metrics consumer")

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			m.logger.Info().Msg("Metrics consumer stopped")
			return

		case <-ticker.C:
			// Query recent metrics from OTLP receiver.
			metrics := m.otlpReceiver.QueryMetrics(m.ctx)

			if len(metrics) == 0 {
				continue
			}

			// Transform each metric batch.
			for _, metricBatch := range metrics {
				events, err := m.transformer.TransformMetrics(metricBatch)
				if err != nil {
					m.logger.Error().Err(err).Msg("Failed to transform metrics")
					continue
				}

				// Store events in local DuckDB for pull-based queries (RFD 032 Phase 4).
				for _, event := range events {
					if m.beylaStorage != nil {
						if err := m.beylaStorage.StoreEvent(m.ctx, event); err != nil {
							m.logger.Error().Err(err).Msg("Failed to store Beyla metric")
							continue
						}
					}

					// Also send to channel for backwards compatibility.
					select {
					case m.metricsCh <- event:
					case <-m.ctx.Done():
						return
					default:
						// Channel full, drop metric (already stored in DB).
						m.logger.Debug().Msg("Metrics channel full (metric already stored in DB)")
					}
				}
			}

			// Clear processed metrics from buffer.
			m.otlpReceiver.ClearMetrics()

			m.logger.Debug().Int("batch_count", len(metrics)).Msg("Processed Beyla metrics")
		}
	}
}

// startBeyla configures and starts Beyla instrumentation as a process.
func (m *Manager) startBeyla() error {
	// Get Beyla binary path (embedded, system, or env var).
	binaryPath, err := getBeylaBinaryPath()
	if err != nil {
		m.logger.Warn().
			Err(err).
			Msg("Beyla binary not found - skipping Beyla instrumentation. " +
				"To enable: set BEYLA_PATH env var, run 'go generate ./internal/agent/beyla', or install Beyla in PATH")
		return nil // Don't fail if Beyla not available, just skip instrumentation
	}

	m.beylaBinaryPath = binaryPath
	m.logger.Info().
		Str("binary_path", binaryPath).
		Msg("Found Beyla binary")

	// Build Beyla command line arguments.
	args := m.buildBeylaArgs()

	// Create command.
	cmd := exec.CommandContext(m.ctx, binaryPath, args...)

	// Configure logging (Beyla logs to stderr).
	cmd.Stdout = &beylaLogWriter{logger: m.logger, level: "info"}
	cmd.Stderr = &beylaLogWriter{logger: m.logger, level: "error"}

	// Start Beyla process.
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start Beyla process: %w", err)
	}

	m.beylaCmd = cmd

	m.logger.Info().
		Int("pid", cmd.Process.Pid).
		Ints("open_ports", m.config.Discovery.OpenPorts).
		Bool("http", m.config.Protocols.HTTPEnabled).
		Bool("grpc", m.config.Protocols.GRPCEnabled).
		Bool("sql", m.config.Protocols.SQLEnabled).
		Msg("Beyla process started")

	// Monitor process in background.
	go m.monitorBeylaProcess(cmd)

	return nil
}

// buildBeylaArgs constructs command line arguments for Beyla binary.
func (m *Manager) buildBeylaArgs() []string {
	args := []string{}

	// OTLP export endpoint.
	if m.config.OTLPEndpoint != "" {
		args = append(args, "--otel-metrics-export", "otlp")
		args = append(args, "--otel-metrics-endpoint", "http://"+m.config.OTLPEndpoint)
		args = append(args, "--otel-traces-export", "otlp")
		args = append(args, "--otel-traces-endpoint", "http://"+m.config.OTLPEndpoint)
	}

	// Discovery: open ports.
	for _, port := range m.config.Discovery.OpenPorts {
		args = append(args, "--open-port", fmt.Sprintf("%d", port))
	}

	// Discovery: process names.
	for _, name := range m.config.Discovery.ProcessNames {
		args = append(args, "--service-name", name)
	}

	// Protocol filters.
	if !m.config.Protocols.HTTPEnabled {
		args = append(args, "--disable-http")
	}
	if !m.config.Protocols.GRPCEnabled {
		args = append(args, "--disable-grpc")
	}

	// Sampling rate.
	if m.config.SamplingRate > 0 && m.config.SamplingRate < 1.0 {
		args = append(args, "--sampling-rate", fmt.Sprintf("%.2f", m.config.SamplingRate))
	}

	// Resource attributes.
	for k, v := range m.config.Attributes {
		args = append(args, "--otel-resource-attributes", fmt.Sprintf("%s=%s", k, v))
	}

	return args
}

// monitorBeylaProcess monitors the Beyla process and logs when it exits.
func (m *Manager) monitorBeylaProcess(cmd *exec.Cmd) {
	err := cmd.Wait()
	if err != nil && m.ctx.Err() == nil {
		// Process exited unexpectedly (not due to context cancellation).
		m.logger.Error().
			Err(err).
			Msg("Beyla process exited unexpectedly")
	} else {
		m.logger.Info().Msg("Beyla process exited")
	}
}

// beylaLogWriter adapts Beyla's stdout/stderr to zerolog.
type beylaLogWriter struct {
	logger zerolog.Logger
	level  string
}

func (w *beylaLogWriter) Write(p []byte) (n int, err error) {
	msg := string(p)
	if len(msg) == 0 {
		return 0, nil
	}

	// Strip trailing newline.
	if msg[len(msg)-1] == '\n' {
		msg = msg[:len(msg)-1]
	}

	// Log based on level.
	if w.level == "error" {
		w.logger.Error().Str("source", "beyla").Msg(msg)
	} else {
		w.logger.Info().Str("source", "beyla").Msg(msg)
	}

	return len(p), nil
}
