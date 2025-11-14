package beyla

import (
	"context"
	"fmt"
	"sync"

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

	// Channels for OTLP data from Beyla.
	metricsCh chan interface{} // Will be pmetric.Metrics when OTLP receiver is implemented
	tracesCh  chan interface{} // Will be ptrace.Traces when OTLP receiver is implemented
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
		ctx:       ctx,
		cancel:    cancel,
		config:    config,
		logger:    logger.With().Str("component", "beyla_manager").Logger(),
		metricsCh: make(chan interface{}, 100),
		tracesCh:  make(chan interface{}, 100),
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

	// Cancel context to stop all goroutines.
	m.cancel()

	// Close channels.
	close(m.metricsCh)
	close(m.tracesCh)

	m.running = false
	m.logger.Info().Msg("Beyla manager stopped")
	return nil
}

// GetMetrics returns a channel receiving OTLP metrics from Beyla.
func (m *Manager) GetMetrics() <-chan interface{} {
	return m.metricsCh
}

// GetTraces returns a channel receiving OTLP traces from Beyla.
func (m *Manager) GetTraces() <-chan interface{} {
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

// startOTLPReceiver starts an embedded OTLP receiver.
// This is a stub implementation. RFD 025 provides the actual OTLP receiver infrastructure.
func (m *Manager) startOTLPReceiver() error {
	m.logger.Info().
		Str("endpoint", m.config.OTLPEndpoint).
		Msg("Starting OTLP receiver (stub - requires RFD 025)")

	// TODO(RFD 032): Integrate with RFD 025 OTLP receiver.
	// The receiver should:
	// 1. Listen on the configured endpoint (e.g., localhost:4318)
	// 2. Accept OTLP metrics and traces from Beyla
	// 3. Forward to m.metricsCh and m.tracesCh channels
	//
	// Example integration:
	// receiverConfig := &otlpreceiver.Config{
	//     Protocols: otlpreceiver.Protocols{
	//         HTTP: &otlpreceiver.HTTPConfig{
	//             Endpoint: m.config.OTLPEndpoint,
	//         },
	//     },
	// }
	// receiver := otlpreceiver.NewFactory().CreateMetricsReceiver(...)
	// receiver.Start(m.ctx, nil)

	return nil
}

// startBeyla configures and starts Beyla instrumentation.
// This is a stub implementation. Full integration requires Beyla Go library.
func (m *Manager) startBeyla() error {
	m.logger.Info().Msg("Starting Beyla instrumentation (stub - requires Beyla library)")

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
