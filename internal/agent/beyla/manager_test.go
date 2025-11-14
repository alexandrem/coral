package beyla

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestNewManager(t *testing.T) {
	tests := []struct {
		name        string
		config      *Config
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
			name: "enabled Beyla",
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
			wantErr:     false,
			wantEnabled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			logger := zerolog.Nop()

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

	mgr, err := NewManager(ctx, config, logger)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	// Test Start.
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
		name              string
		config            *Config
		wantProtocols     []string
		wantTracingEnabled bool
	}{
		{
			name: "disabled Beyla",
			config: &Config{
				Enabled: false,
			},
			wantProtocols:     []string{},
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
			wantProtocols:     []string{"http", "http2"},
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
			wantProtocols:     []string{"http", "http2", "grpc", "postgresql", "mysql", "kafka", "redis"},
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
	metricsCh := mgr.GetMetrics()
	if metricsCh == nil {
		t.Error("GetMetrics() should return non-nil channel")
	}

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
	case <-metricsCh:
		// No data expected in stub implementation
	case <-time.After(10 * time.Millisecond):
		// Expected - no data
	}

	// Stop manager.
	if err := mgr.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	// Channels should be closed after Stop.
	select {
	case _, ok := <-metricsCh:
		if ok {
			t.Error("Metrics channel should be closed after Stop()")
		}
	case <-time.After(10 * time.Millisecond):
		t.Error("Should receive from closed metrics channel immediately")
	}
}
