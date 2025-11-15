package beyla

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

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

	// OTLP receiver from RFD 025 (receives Beyla's trace output).
	otlpReceiver *telemetry.OTLPReceiver
	storage      *telemetry.Storage

	// Channels for processed Beyla data.
	tracesCh chan *BeylaTrace // Beyla traces ready for Colony
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
	HTTPEnabled bool
	GRPCEnabled bool
	SQLEnabled  bool
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
		ctx:      ctx,
		cancel:   cancel,
		config:   config,
		logger:   logger.With().Str("component", "beyla_manager").Logger(),
		tracesCh: make(chan *BeylaTrace, 100),
	}

	// Initialize OTLP receiver storage (RFD 025).
	if config.DB != nil {
		storage, err := telemetry.NewStorage(config.DB, logger)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("failed to create telemetry storage: %w", err)
		}
		m.storage = storage
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

	// TODO(RFD 032): Start OTLP receiver to consume Beyla output.
	// This assumes RFD 025 (Basic OTLP Ingestion) is implemented.
	if err := m.startOTLPReceiver(); err != nil {
		return fmt.Errorf("failed to start OTLP receiver: %w", err)
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

	m.running = false
	m.logger.Info().Msg("Beyla manager stopped")
	return nil
}

// GetTraces returns a channel receiving Beyla traces ready for Colony.
func (m *Manager) GetTraces() <-chan *BeylaTrace {
	return m.tracesCh
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

	// Start trace consumer goroutine.
	go m.consumeTraces()

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

// startBeyla configures and starts Beyla instrumentation.
// This is a stub implementation. Full integration requires Beyla Go library.
func (m *Manager) startBeyla() error {
	m.logger.Info().Msg("Starting Beyla instrumentation (stub - requires Beyla library)")

	// NOTE: Metrics support (RFD 032).
	// Beyla also exports OTLP metrics (http.server.request.duration, rpc.server.duration, etc.).
	// The current RFD 025 implementation only supports traces.
	// Future work:
	// 1. Extend OTLPReceiver to support OTLP metrics
	// 2. Create metrics consumer similar to consumeTraces()
	// 3. Transform OTLP metrics to BeylaHttpMetrics, BeylaGrpcMetrics, BeylaSqlMetrics
	// 4. Forward to Colony for storage in beyla_*_metrics tables

	// TODO(RFD 032): Integrate Beyla Go library.
	// The integration should:
	// 1. Configure Beyla with discovery rules, protocol filters, sampling rate
	// 2. Start Beyla in a goroutine via beyla.Run(ctx, config)
	// 3. Beyla will export metrics/traces to the OTLP endpoint
	// 4. OTLP receiver consumes and forwards to Coral
	//
	// Example integration:
	// beylaConfig := &beyla.Config{
	//     Discovery: beyla.DiscoveryConfig{
	//         OpenPorts:    m.config.Discovery.OpenPorts,
	//         K8sNamespace: m.config.Discovery.K8sNamespaces,
	//         K8sPodLabels: m.config.Discovery.K8sPodLabels,
	//     },
	//     Protocols: beyla.ProtocolsConfig{
	//         HTTP: beyla.HTTPConfig{Enabled: m.config.Protocols.HTTPEnabled},
	//         GRPC: beyla.GRPCConfig{Enabled: m.config.Protocols.GRPCEnabled},
	//         SQL:  beyla.SQLConfig{Enabled: m.config.Protocols.SQLEnabled},
	//     },
	//     Sampling: beyla.SamplingConfig{
	//         Rate: m.config.SamplingRate,
	//     },
	//     OTLP: beyla.OTLPConfig{
	//         MetricsEndpoint: m.config.OTLPEndpoint,
	//         TracesEndpoint:  m.config.OTLPEndpoint,
	//     },
	//     Attributes: m.config.Attributes,
	// }
	//
	// go func() {
	//     if err := beyla.Run(m.ctx, beylaConfig); err != nil {
	//         m.logger.Error().Err(err).Msg("Beyla error")
	//     }
	// }()

	m.logger.Info().
		Ints("open_ports", m.config.Discovery.OpenPorts).
		Bool("http", m.config.Protocols.HTTPEnabled).
		Bool("grpc", m.config.Protocols.GRPCEnabled).
		Bool("sql", m.config.Protocols.SQLEnabled).
		Msg("Beyla configuration (stub)")

	return nil
}
