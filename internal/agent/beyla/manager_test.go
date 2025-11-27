package beyla

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/rs/zerolog"
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

// TestUpdateDiscovery tests the dynamic discovery update functionality (RFD 053).
func TestUpdateDiscovery(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	tests := []struct {
		name         string
		initialPorts []int
		updatePorts  []int
		wantChanged  bool
	}{
		{
			name:         "add new port",
			initialPorts: []int{8080},
			updatePorts:  []int{8080, 9090},
			wantChanged:  true,
		},
		{
			name:         "remove port",
			initialPorts: []int{8080, 9090},
			updatePorts:  []int{8080},
			wantChanged:  true,
		},
		{
			name:         "no change",
			initialPorts: []int{8080, 9090},
			updatePorts:  []int{8080, 9090},
			wantChanged:  false,
		},
		{
			name:         "different order (no change)",
			initialPorts: []int{8080, 9090},
			updatePorts:  []int{9090, 8080},
			wantChanged:  false,
		},
		{
			name:         "replace all ports",
			initialPorts: []int{8080, 9090},
			updatePorts:  []int{3000, 4000},
			wantChanged:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &Config{
				Enabled: true,
				Discovery: DiscoveryConfig{
					OpenPorts: tt.initialPorts,
				},
			}

			mgr, err := NewManager(ctx, config, logger)
			if err != nil {
				t.Fatalf("NewManager() error = %v", err)
			}

			// Verify initial ports.
			initialConfigPorts := mgr.GetDiscoveryPorts()
			if len(initialConfigPorts) != len(tt.initialPorts) {
				t.Errorf("Initial ports count = %d, want %d", len(initialConfigPorts), len(tt.initialPorts))
			}

			// Update discovery.
			err = mgr.UpdateDiscovery(tt.updatePorts)
			if err != nil {
				t.Fatalf("UpdateDiscovery() error = %v", err)
			}

			// Verify ports were updated.
			updatedPorts := mgr.GetDiscoveryPorts()
			if len(updatedPorts) != len(tt.updatePorts) {
				t.Errorf("Updated ports count = %d, want %d", len(updatedPorts), len(tt.updatePorts))
			}

			// Verify all expected ports are present.
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

// TestUpdateDiscoveryDisabled tests that UpdateDiscovery is a no-op when Beyla is disabled.
func TestUpdateDiscoveryDisabled(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()

	config := &Config{
		Enabled: false,
	}

	mgr, err := NewManager(ctx, config, logger)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	// UpdateDiscovery should not fail when Beyla is disabled.
	err = mgr.UpdateDiscovery([]int{8080, 9090})
	if err != nil {
		t.Errorf("UpdateDiscovery() on disabled manager should not error, got: %v", err)
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
