package beyla

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/rs/zerolog"
	"gopkg.in/yaml.v3"

	"github.com/coral-mesh/coral/internal/agent/beyla/discovery"
	"github.com/coral-mesh/coral/internal/agent/telemetry"
)

func TestNewManager(t *testing.T) {
	tests := []struct {
		name        string
		config      *Config
		withDB      bool
		wantErr     bool
		wantEnabled bool
	}{
		{
			name:    "nil config",
			config:  nil,
			wantErr: true,
		},
		{
			name: "disabled Beyla",
			config: &Config{
				Enabled: false,
			},
			wantErr:     false,
			wantEnabled: false,
		},
		{
			name: "enabled Beyla without DB",
			config: &Config{
				Enabled:      true,
				OTLPEndpoint: "localhost:4318",
				SamplingRate: 1.0,
				Discovery: DiscoveryConfig{
					OpenPorts: []int{8080},
				},
				Protocols: ProtocolsConfig{
					HTTPEnabled: true,
					GRPCEnabled: true,
				},
				Attributes: map[string]string{
					"colony.id": "test-colony",
				},
			},
			withDB:      false,
			wantErr:     false,
			wantEnabled: true,
		},
		{
			name: "enabled Beyla with DB",
			config: &Config{
				Enabled:      true,
				OTLPEndpoint: "localhost:4318",
				SamplingRate: 1.0,
				Discovery: DiscoveryConfig{
					OpenPorts: []int{8080},
				},
				Protocols: ProtocolsConfig{
					HTTPEnabled: true,
					GRPCEnabled: true,
				},
				Attributes: map[string]string{
					"colony.id": "test-colony",
				},
			},
			withDB:      true,
			wantErr:     false,
			wantEnabled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			logger := zerolog.Nop()

			// Setup database if needed.
			if tt.withDB && tt.config != nil {
				db, err := sql.Open("duckdb", ":memory:")
				if err != nil {
					t.Fatalf("Failed to create test database: %v", err)
				}
				defer func() { _ = db.Close() }()
				tt.config.DB = db
			}

			mgr, err := NewManager(ctx, tt.config, logger)

			if (err != nil) != tt.wantErr {
				t.Errorf("NewManager() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if err == nil && mgr != nil {
				if mgr.config.Enabled != tt.wantEnabled {
					t.Errorf("NewManager() enabled = %v, want %v", mgr.config.Enabled, tt.wantEnabled)
				}
			}
		})
	}
}

func TestManagerStartStop(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	config := &Config{
		Enabled:      true,
		OTLPEndpoint: "localhost:4318",
		SamplingRate: 1.0,
		Discovery: DiscoveryConfig{
			OpenPorts: []int{8080, 9090},
		},
		Protocols: ProtocolsConfig{
			HTTPEnabled: true,
			GRPCEnabled: true,
			SQLEnabled:  true,
		},
	}

	// Note: Starting without DB will work but OTLP receiver won't be available.
	// This tests the graceful degradation.
	mgr, err := NewManager(ctx, config, logger)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	// Test Start (without OTLP receiver due to no DB).
	if err := mgr.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if !mgr.IsRunning() {
		t.Error("Manager should be running after Start()")
	}

	// Test double start.
	if err := mgr.Start(); err == nil {
		t.Error("Second Start() should return error")
	}

	// Test Stop.
	if err := mgr.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if mgr.IsRunning() {
		t.Error("Manager should not be running after Stop()")
	}

	// Test double stop.
	if err := mgr.Stop(); err != nil {
		t.Error("Second Stop() should not return error")
	}
}

func TestManagerStartWithBeylaBinaryDoesNotDeadlock(t *testing.T) {
	// A real executable makes startBeyla reach generateBeylaConfig. This
	// regresses the RFD-102 bug where Start held m.mu and config generation
	// attempted to take m.mu.RLock(), permanently blocking startup.
	t.Setenv("BEYLA_PATH", "/bin/true")

	mgr, err := NewManager(context.Background(), &Config{
		Enabled:      true,
		OTLPEndpoint: "localhost:4318",
		SamplingRate: 1.0,
		Discovery: DiscoveryConfig{
			OpenPorts: []int{8080},
		},
		Protocols: ProtocolsConfig{HTTPEnabled: true},
	}, zerolog.Nop())
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- mgr.Start() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Start() blocked while generating the Beyla configuration")
	}

	if err := mgr.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestManagerDisabled(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	config := &Config{
		Enabled: false,
	}

	mgr, err := NewManager(ctx, config, logger)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	// Start should succeed but not actually start.
	if err := mgr.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if mgr.IsRunning() {
		t.Error("Disabled manager should not be running")
	}

	// Stop should succeed.
	if err := mgr.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestGetCapabilities(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	tests := []struct {
		name               string
		config             *Config
		wantProtocols      []string
		wantTracingEnabled bool
	}{
		{
			name: "disabled Beyla",
			config: &Config{
				Enabled: false,
			},
			wantProtocols:      []string{},
			wantTracingEnabled: false,
		},
		{
			name: "HTTP only",
			config: &Config{
				Enabled: true,
				Protocols: ProtocolsConfig{
					HTTPEnabled: true,
				},
			},
			wantProtocols:      []string{"http", "http2"},
			wantTracingEnabled: true,
		},
		{
			name: "all protocols",
			config: &Config{
				Enabled: true,
				Protocols: ProtocolsConfig{
					HTTPEnabled:  true,
					GRPCEnabled:  true,
					SQLEnabled:   true,
					KafkaEnabled: true,
					RedisEnabled: true,
				},
			},
			wantProtocols:      []string{"http", "http2", "grpc", "postgresql", "mysql", "kafka", "redis"},
			wantTracingEnabled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr, err := NewManager(ctx, tt.config, logger)
			if err != nil {
				t.Fatalf("NewManager() error = %v", err)
			}

			caps := mgr.GetCapabilities()

			if caps.TracingEnabled != tt.wantTracingEnabled {
				t.Errorf("GetCapabilities() TracingEnabled = %v, want %v",
					caps.TracingEnabled, tt.wantTracingEnabled)
			}

			if len(caps.SupportedProtocols) != len(tt.wantProtocols) {
				t.Errorf("GetCapabilities() SupportedProtocols count = %d, want %d",
					len(caps.SupportedProtocols), len(tt.wantProtocols))
			}
		})
	}
}

func TestManagerChannels(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	config := &Config{
		Enabled:      true,
		OTLPEndpoint: "localhost:4318",
	}

	mgr, err := NewManager(ctx, config, logger)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	// Channels should be available before Start.
	tracesCh := mgr.GetTraces()
	if tracesCh == nil {
		t.Error("GetTraces() should return non-nil channel")
	}

	// Start manager.
	if err := mgr.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Channels should still work after Start.
	select {
	case <-tracesCh:
		// No data expected without OTLP receiver running
	case <-time.After(10 * time.Millisecond):
		// Expected - no data
	}

	// Stop manager.
	if err := mgr.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	// Channels should be closed after Stop.
	select {
	case _, ok := <-tracesCh:
		if ok {
			t.Error("Traces channel should be closed after Stop()")
		}
	case <-time.After(10 * time.Millisecond):
		t.Error("Should receive from closed traces channel immediately")
	}
}

// TestSetStaticCandidates tests the dynamic discovery update functionality
// (RFD 053/102). SetStaticCandidates always receives the FULL current set of
// connected services (mirroring how agent.go's ConnectService/
// DisconnectService handlers recompute it on every call), so each call fully
// replaces the dynamic candidate set.
func TestSetStaticCandidates(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	tests := []struct {
		name        string
		updatePorts []int
	}{
		{name: "add services", updatePorts: []int{8080, 9090}},
		{name: "shrink to one service", updatePorts: []int{8080}},
		{name: "replace all services", updatePorts: []int{3000, 4000}},
		{name: "clear all services", updatePorts: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &Config{Enabled: true}
			mgr, err := NewManager(ctx, config, logger)
			if err != nil {
				t.Fatalf("NewManager() error = %v", err)
			}

			// No candidates configured initially.
			if got := mgr.GetDiscoveryPorts(); len(got) != 0 {
				t.Fatalf("initial ports = %v, want empty", got)
			}

			// Each connected service has a unique name, as agent.go's
			// ConnectService guarantees (service names key a.monitors).
			candidates := make([]discovery.ProcessCandidate, 0, len(tt.updatePorts))
			for _, port := range tt.updatePorts {
				candidates = append(candidates, discovery.ProcessCandidate{
					Ports: []int{port},
					Name:  fmt.Sprintf("test-service-%d", port),
				})
			}
			if err := mgr.SetStaticCandidates(candidates); err != nil {
				t.Fatalf("SetStaticCandidates() error = %v", err)
			}

			updatedPorts := mgr.GetDiscoveryPorts()
			if len(updatedPorts) != len(tt.updatePorts) {
				t.Errorf("Updated ports count = %d, want %d", len(updatedPorts), len(tt.updatePorts))
			}

			portMap := make(map[int]bool)
			for _, port := range updatedPorts {
				portMap[port] = true
			}
			for _, port := range tt.updatePorts {
				if !portMap[port] {
					t.Errorf("Expected port %d not found in updated ports", port)
				}
			}
		})
	}
}

// TestConfigStaticCandidatesPersistAcrossSetStaticCandidates verifies that
// named ports from the initial config file's ServiceMap (RFD 110) survive
// every SetStaticCandidates call, since a `coral connect`/`coral disconnect`
// call only ever supplies the currently-connected-via-RPC services, not the
// config file's permanent entries.
func TestConfigStaticCandidatesPersistAcrossSetStaticCandidates(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	config := &Config{
		Enabled: true,
		Discovery: DiscoveryConfig{
			OpenPorts:  []int{8080},
			ServiceMap: map[int]string{8080: "config-service"},
		},
	}

	mgr, err := NewManager(ctx, config, logger)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	if got := mgr.GetDiscoveryPorts(); len(got) != 1 || got[0] != 8080 {
		t.Fatalf("initial ports = %v, want [8080] from config ServiceMap", got)
	}

	// A `coral connect` for an unrelated service must not evict the
	// config-file entry.
	if err := mgr.SetStaticCandidates([]discovery.ProcessCandidate{
		{Ports: []int{9090}, Name: "connected-service"},
	}); err != nil {
		t.Fatalf("SetStaticCandidates() error = %v", err)
	}

	ports := mgr.GetDiscoveryPorts()
	portMap := make(map[int]bool, len(ports))
	for _, p := range ports {
		portMap[p] = true
	}
	if !portMap[8080] {
		t.Errorf("config ServiceMap port 8080 was evicted by SetStaticCandidates, ports = %v", ports)
	}
	if !portMap[9090] {
		t.Errorf("dynamically connected port 9090 missing, ports = %v", ports)
	}

	// Even an empty dynamic set (all RPC-connected services disconnected)
	// must not evict the config-file entry.
	if err := mgr.SetStaticCandidates(nil); err != nil {
		t.Fatalf("SetStaticCandidates(nil) error = %v", err)
	}
	if got := mgr.GetDiscoveryPorts(); len(got) != 1 || got[0] != 8080 {
		t.Errorf("config ServiceMap port did not survive clearing dynamic candidates, ports = %v", got)
	}
}

// TestSetStaticCandidatesDisabled tests that SetStaticCandidates is a no-op
// when Beyla is disabled.
func TestSetStaticCandidatesDisabled(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	config := &Config{
		Enabled: false,
	}

	mgr, err := NewManager(ctx, config, logger)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	// SetStaticCandidates should not fail when Beyla is disabled.
	err = mgr.SetStaticCandidates([]discovery.ProcessCandidate{
		{Ports: []int{8080}, Name: "service-1"},
		{Ports: []int{9090}, Name: "service-2"},
	})
	if err != nil {
		t.Errorf("SetStaticCandidates() on disabled manager should not error, got: %v", err)
	}
}

// TestPortsEqual tests the portsEqual helper function.
func TestPortsEqual(t *testing.T) {
	tests := []struct {
		name string
		a    []int
		b    []int
		want bool
	}{
		{
			name: "empty slices",
			a:    []int{},
			b:    []int{},
			want: true,
		},
		{
			name: "same ports same order",
			a:    []int{8080, 9090},
			b:    []int{8080, 9090},
			want: true,
		},
		{
			name: "same ports different order",
			a:    []int{8080, 9090},
			b:    []int{9090, 8080},
			want: true,
		},
		{
			name: "different ports",
			a:    []int{8080, 9090},
			b:    []int{8080, 3000},
			want: false,
		},
		{
			name: "different length",
			a:    []int{8080},
			b:    []int{8080, 9090},
			want: false,
		},
		{
			name: "nil vs empty",
			a:    nil,
			b:    []int{},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := portsEqual(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("portsEqual(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestGenerateBeylaConfigNoCatchAllWithNamedRules(t *testing.T) {
	// Regression test: when named per-service rules exist, the MonitorAll
	// catch-all (open_ports: "1-65535") must NOT be added. When both named
	// rules and the catch-all are present, Beyla attaches eBPF probes to the
	// same processes twice, causing conflicts that result in zero spans captured.
	ctx := context.Background()
	logger := zerolog.Nop()

	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("failed to open duckdb: %v", err)
	}
	defer func() { _ = db.Close() }()

	mgr, err := NewManager(ctx, &Config{
		Enabled:    true,
		DB:         db,
		MonitorAll: true,
		Discovery: DiscoveryConfig{
			OpenPorts:  []int{8080, 8090},
			ServiceMap: map[int]string{8080: "cpu-app", 8090: "otel-app"},
		},
	}, logger)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	configPath, err := mgr.generateBeylaConfig()
	if err != nil {
		t.Fatalf("generateBeylaConfig() error = %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read generated config: %v", err)
	}
	_ = os.Remove(configPath)

	content := string(data)
	if strings.Contains(content, "1-65535") {
		t.Errorf("generated Beyla config contains catch-all rule (1-65535) alongside named rules; "+
			"this causes eBPF probe conflicts and zero spans.\nConfig:\n%s", content)
	}
	if !strings.Contains(content, "cpu-app") || !strings.Contains(content, "otel-app") {
		t.Errorf("generated Beyla config missing named service rules.\nConfig:\n%s", content)
	}
}

func TestGenerateBeylaConfigMonitorAllFallback(t *testing.T) {
	// When MonitorAll is set but no named rules exist, the catch-all must still
	// be added so all processes are instrumented.
	ctx := context.Background()
	logger := zerolog.Nop()

	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("failed to open duckdb: %v", err)
	}
	defer func() { _ = db.Close() }()

	mgr, err := NewManager(ctx, &Config{
		Enabled:    true,
		DB:         db,
		MonitorAll: true,
	}, logger)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	configPath, err := mgr.generateBeylaConfig()
	if err != nil {
		t.Fatalf("generateBeylaConfig() error = %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read generated config: %v", err)
	}
	_ = os.Remove(configPath)

	if !strings.Contains(string(data), "1-65535") {
		t.Errorf("generated Beyla config missing catch-all rule (1-65535) for MonitorAll mode.\nConfig:\n%s", string(data))
	}
}

// TestGenerateBeylaConfigFromDiscoveryCandidates verifies the RFD 102
// candidate-to-rule mapping directly: server candidates (with ports) become
// open_ports rules, client-only candidates become exe_path-only rules, and
// no residual catch-all is emitted since every candidate resolved to a
// named rule.
func TestGenerateBeylaConfigFromDiscoveryCandidates(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("failed to open duckdb: %v", err)
	}
	defer func() { _ = db.Close() }()

	mgr, err := NewManager(ctx, &Config{
		Enabled:    true,
		DB:         db,
		MonitorAll: true,
	}, logger)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	// Simulate DiscoveryManager having merged a server candidate (e.g. from
	// ProcFSProvider) and a client-only candidate (e.g. a Kafka consumer
	// with no listening socket) — the two categories called out by RFD 102.
	mgr.mu.Lock()
	mgr.discoveryCandidates = []discovery.ProcessCandidate{
		{PID: 100, Name: "otel-app", Ports: []int{8090}},
		{PID: 200, Name: "kafka-consumer", IsClientOnly: true},
	}
	mgr.mu.Unlock()

	configPath, err := mgr.generateBeylaConfig()
	if err != nil {
		t.Fatalf("generateBeylaConfig() error = %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read generated config: %v", err)
	}
	_ = os.Remove(configPath)
	content := string(data)

	if strings.Contains(content, "1-65535") {
		t.Errorf("generated config must not fall back to the catch-all when every candidate resolved.\nConfig:\n%s", content)
	}
	if !strings.Contains(content, "open_ports: \"8090\"") {
		t.Errorf("expected an open_ports rule for the server candidate.\nConfig:\n%s", content)
	}
	if !strings.Contains(content, "otel-app") {
		t.Errorf("expected the server candidate's name in the config.\nConfig:\n%s", content)
	}
	if !strings.Contains(content, "kafka-consumer") {
		t.Errorf("expected the client-only candidate's name in the config.\nConfig:\n%s", content)
	}
	if !strings.Contains(content, "exe_path: .*kafka-consumer.*") {
		t.Errorf("expected an exe_path rule for the client-only candidate.\nConfig:\n%s", content)
	}

	// The client-only candidate must not have picked up an open_ports rule.
	var cfg BeylaConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("failed to parse generated config: %v", err)
	}
	for _, svc := range cfg.Discovery.Services {
		if svc.Name == "kafka-consumer" && svc.OpenPorts != "" {
			t.Errorf("client-only candidate must not get an open_ports rule, got %+v", svc)
		}
		if svc.Name == "otel-app" && svc.OpenPorts == "" {
			t.Errorf("server candidate must get an open_ports rule, got %+v", svc)
		}
	}
}

// TestStaticAndProviderCandidatesCoexistWithoutDuplicateRules verifies that
// two independently `coral connect`-registered services produce exactly one
// Beyla rule apiece — never two rules claiming the same port, which would
// cause eBPF probe attachment conflicts (RFD 102).
func TestStaticAndProviderCandidatesCoexistWithoutDuplicateRules(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("failed to open duckdb: %v", err)
	}
	defer func() { _ = db.Close() }()

	mgr, err := NewManager(ctx, &Config{
		Enabled: true,
		DB:      db,
	}, logger)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	if err := mgr.SetStaticCandidates([]discovery.ProcessCandidate{
		{Ports: []int{8080}, Name: "cpu-app"},
		{Ports: []int{8090}, Name: "otel-app"},
	}); err != nil {
		t.Fatalf("SetStaticCandidates() error = %v", err)
	}

	configPath, err := mgr.generateBeylaConfig()
	if err != nil {
		t.Fatalf("generateBeylaConfig() error = %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read generated config: %v", err)
	}
	_ = os.Remove(configPath)

	var cfg BeylaConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("failed to parse generated config: %v", err)
	}

	portRuleCount := make(map[string]int)
	for _, svc := range cfg.Discovery.Services {
		if svc.OpenPorts != "" {
			portRuleCount[svc.OpenPorts]++
		}
	}
	for port, count := range portRuleCount {
		if count > 1 {
			t.Errorf("port %q claimed by %d rules, want exactly 1 (would cause eBPF probe conflicts): %+v",
				port, count, cfg.Discovery.Services)
		}
	}
	if len(cfg.Discovery.Services) != 2 {
		t.Errorf("expected exactly 2 service rules (one per connected service), got %d: %+v",
			len(cfg.Discovery.Services), cfg.Discovery.Services)
	}
}

func TestGenerateBeylaConfigContextPropagation(t *testing.T) {
	// Regression test: generated Beyla config must always include
	// context_propagation: all so that Beyla injects and extracts W3C
	// traceparent headers. Without this, parent_span_id is always empty and
	// topology materialisation produces no edges.
	ctx := context.Background()
	logger := zerolog.Nop()

	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("failed to open duckdb: %v", err)
	}
	defer func() { _ = db.Close() }()

	mgr, err := NewManager(ctx, &Config{
		Enabled: true,
		DB:      db,
		Discovery: DiscoveryConfig{
			OpenPorts: []int{8080},
		},
	}, logger)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	configPath, err := mgr.generateBeylaConfig()
	if err != nil {
		t.Fatalf("generateBeylaConfig() error = %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read generated config: %v", err)
	}
	_ = os.Remove(configPath)

	if !strings.Contains(string(data), "context_propagation: all") {
		t.Errorf("generated Beyla config missing context_propagation: all; topology will not work.\nConfig:\n%s", data)
	}
}

// observedService records one serviceObservedHandler invocation (RFD 103).
type observedService struct {
	port int32
	pid  int32
	name string
}

func TestHandleSpanServiceObservedCallbackFires(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("failed to open duckdb: %v", err)
	}
	defer func() { _ = db.Close() }()

	mgr, err := NewManager(ctx, &Config{Enabled: true, DB: db}, logger)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	var observed []observedService
	mgr.SetServiceObservedHandler(func(port int32, pid int32, serviceName string) {
		observed = append(observed, observedService{port: port, pid: pid, name: serviceName})
	})

	span := telemetry.Span{
		TraceID:     "abcd",
		SpanID:      "1234",
		ServiceName: "checkout",
		SpanKind:    "server",
		Timestamp:   time.Now(),
		ProcessPID:  4242,
		Attributes:  map[string]string{"server.port": "3000"},
	}

	if err := mgr.HandleSpan(ctx, span); err != nil {
		t.Fatalf("HandleSpan() error = %v", err)
	}

	if len(observed) != 1 {
		t.Fatalf("expected 1 observation, got %d: %+v", len(observed), observed)
	}
	if got := observed[0]; got.port != 3000 || got.pid != 4242 || got.name != "checkout" {
		t.Errorf("unexpected observation: %+v", got)
	}
}

func TestHandleSpanServiceObservedCallbackDeduplicatesPerPort(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("failed to open duckdb: %v", err)
	}
	defer func() { _ = db.Close() }()

	mgr, err := NewManager(ctx, &Config{Enabled: true, DB: db}, logger)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	var observed []observedService
	mgr.SetServiceObservedHandler(func(port int32, pid int32, serviceName string) {
		observed = append(observed, observedService{port: port, pid: pid, name: serviceName})
	})

	base := telemetry.Span{
		ServiceName: "checkout",
		SpanKind:    "server",
		Timestamp:   time.Now(),
		ProcessPID:  4242,
		Attributes:  map[string]string{"server.port": "3000"},
	}

	// Two spans on the same port: only the first should fire the callback.
	first, second := base, base
	first.SpanID, second.SpanID = "1111", "2222"
	if err := mgr.HandleSpan(ctx, first); err != nil {
		t.Fatalf("HandleSpan() error = %v", err)
	}
	if err := mgr.HandleSpan(ctx, second); err != nil {
		t.Fatalf("HandleSpan() error = %v", err)
	}
	if len(observed) != 1 {
		t.Fatalf("expected callback to fire once for repeated port, got %d calls: %+v", len(observed), observed)
	}

	// A span on a new port (via the older net.host.port key) fires again.
	third := base
	third.SpanID = "3333"
	third.Attributes = map[string]string{"net.host.port": "4000"}
	if err := mgr.HandleSpan(ctx, third); err != nil {
		t.Fatalf("HandleSpan() error = %v", err)
	}
	if len(observed) != 2 {
		t.Fatalf("expected 2 observations after new port, got %d: %+v", len(observed), observed)
	}
	if observed[1].port != 4000 {
		t.Errorf("expected port 4000 from net.host.port fallback, got %d", observed[1].port)
	}
}

func TestHandleSpanNoPortAttributeSkipsCallback(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("failed to open duckdb: %v", err)
	}
	defer func() { _ = db.Close() }()

	mgr, err := NewManager(ctx, &Config{Enabled: true, DB: db}, logger)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	called := false
	mgr.SetServiceObservedHandler(func(int32, int32, string) { called = true })

	span := telemetry.Span{
		ServiceName: "checkout",
		Timestamp:   time.Now(),
		Attributes:  map[string]string{},
	}
	if err := mgr.HandleSpan(ctx, span); err != nil {
		t.Fatalf("HandleSpan() error = %v", err)
	}
	if called {
		t.Error("callback should not fire when no port attribute is present")
	}
}
