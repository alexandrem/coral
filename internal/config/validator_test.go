package config

import (
	"strings"
	"testing"
)

func TestGlobalConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *GlobalConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid config",
			cfg:     DefaultGlobalConfig(),
			wantErr: false,
		},
		{
			name: "missing version",
			cfg: &GlobalConfig{
				Discovery: DiscoveryGlobal{
					Endpoint: "http://localhost:8080",
					Timeout:  10,
				},
			},
			wantErr: true,
			errMsg:  "version is required",
		},
		{
			name: "missing discovery endpoint",
			cfg: &GlobalConfig{
				Version: "1",
				Discovery: DiscoveryGlobal{
					Timeout: 10,
				},
			},
			wantErr: true,
			errMsg:  "discovery endpoint is required",
		},
		{
			name: "invalid discovery timeout",
			cfg: &GlobalConfig{
				Version: "1",
				Discovery: DiscoveryGlobal{
					Endpoint: "http://localhost:8080",
					Timeout:  0,
				},
			},
			wantErr: true,
			errMsg:  "discovery timeout must be positive",
		},
		{
			name: "invalid AI provider",
			cfg: &GlobalConfig{
				Version: "1",
				Discovery: DiscoveryGlobal{
					Endpoint: "http://localhost:8080",
					Timeout:  10,
				},
				AI: AIConfig{
					Provider: "invalid",
				},
			},
			wantErr: true,
			errMsg:  "ai provider must be 'anthropic' or 'openai'",
		},
		{
			name: "invalid API key source",
			cfg: &GlobalConfig{
				Version: "1",
				Discovery: DiscoveryGlobal{
					Endpoint: "http://localhost:8080",
					Timeout:  10,
				},
				AI: AIConfig{
					APIKeySource: "invalid",
				},
			},
			wantErr: true,
			errMsg:  "api key source must be 'env', 'keychain', or 'file'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("GlobalConfig.Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("GlobalConfig.Validate() error = %v, expected to contain %q", err, tt.errMsg)
			}
		})
	}
}

func TestColonyConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *ColonyConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config",
			cfg: func() *ColonyConfig {
				cfg := DefaultColonyConfig("test-colony", "test-app", "dev")
				cfg.WireGuard.PrivateKey = "test-private-key"
				cfg.WireGuard.PublicKey = "test-public-key"
				cfg.Services.ConnectPort = 9001
				cfg.Services.DashboardPort = 3000
				return cfg
			}(),
			wantErr: false,
		},
		{
			name: "missing version",
			cfg: &ColonyConfig{
				ColonyID:        "test",
				ApplicationName: "app",
				Environment:     "dev",
				WireGuard: WireGuardConfig{
					PrivateKey: "key",
					PublicKey:  "pub",
					Port:       51820,
				},
				Services: ServicesConfig{
					ConnectPort:   9001,
					DashboardPort: 3000,
				},
			},
			wantErr: true,
			errMsg:  "version is required",
		},
		{
			name: "missing colony ID",
			cfg: &ColonyConfig{
				Version:         "1",
				ApplicationName: "app",
				Environment:     "dev",
				WireGuard: WireGuardConfig{
					PrivateKey: "key",
					PublicKey:  "pub",
					Port:       51820,
				},
				Services: ServicesConfig{
					ConnectPort:   9001,
					DashboardPort: 3000,
				},
			},
			wantErr: true,
			errMsg:  "colony ID is required",
		},
		{
			name: "missing application name",
			cfg: &ColonyConfig{
				Version:     "1",
				ColonyID:    "test",
				Environment: "dev",
				WireGuard: WireGuardConfig{
					PrivateKey: "key",
					PublicKey:  "pub",
					Port:       51820,
				},
				Services: ServicesConfig{
					ConnectPort:   9001,
					DashboardPort: 3000,
				},
			},
			wantErr: true,
			errMsg:  "application name is required",
		},
		{
			name: "missing environment",
			cfg: &ColonyConfig{
				Version:         "1",
				ColonyID:        "test",
				ApplicationName: "app",
				WireGuard: WireGuardConfig{
					PrivateKey: "key",
					PublicKey:  "pub",
					Port:       51820,
				},
				Services: ServicesConfig{
					ConnectPort:   9001,
					DashboardPort: 3000,
				},
			},
			wantErr: true,
			errMsg:  "environment is required",
		},
		{
			name: "invalid connect port",
			cfg: &ColonyConfig{
				Version:         "1",
				ColonyID:        "test",
				ApplicationName: "app",
				Environment:     "dev",
				WireGuard: WireGuardConfig{
					PrivateKey: "key",
					PublicKey:  "pub",
					Port:       51820,
				},
				Services: ServicesConfig{
					ConnectPort:   0,
					DashboardPort: 3000,
				},
			},
			wantErr: true,
			errMsg:  "connect port must be between 1 and 65535",
		},
		{
			name: "invalid dashboard port",
			cfg: &ColonyConfig{
				Version:         "1",
				ColonyID:        "test",
				ApplicationName: "app",
				Environment:     "dev",
				WireGuard: WireGuardConfig{
					PrivateKey: "key",
					PublicKey:  "pub",
					Port:       51820,
				},
				Services: ServicesConfig{
					ConnectPort:   9001,
					DashboardPort: 70000,
				},
			},
			wantErr: true,
			errMsg:  "dashboard port must be between 1 and 65535",
		},
		{
			name: "mismatched discovery mesh ID",
			cfg: &ColonyConfig{
				Version:         "1",
				ColonyID:        "test-colony",
				ApplicationName: "app",
				Environment:     "dev",
				WireGuard: WireGuardConfig{
					PrivateKey: "key",
					PublicKey:  "pub",
					Port:       51820,
				},
				Services: ServicesConfig{
					ConnectPort:   9001,
					DashboardPort: 3000,
				},
				Discovery: DiscoveryColony{
					Enabled: true,
					MeshID:  "different-id",
				},
			},
			wantErr: true,
			errMsg:  "mesh ID must match colony ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("ColonyConfig.Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("ColonyConfig.Validate() error = %v, expected to contain %q", err, tt.errMsg)
			}
		})
	}
}

func TestWireGuardConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *WireGuardConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config",
			cfg: &WireGuardConfig{
				PrivateKey: "key",
				PublicKey:  "pub",
				Port:       51820,
				MeshIPv4:   "100.64.0.1",
				MeshIPv6:   "fd42::1",
			},
			wantErr: false,
		},
		{
			name: "missing private key",
			cfg: &WireGuardConfig{
				PublicKey: "pub",
				Port:      51820,
			},
			wantErr: true,
			errMsg:  "private key is required",
		},
		{
			name: "missing public key",
			cfg: &WireGuardConfig{
				PrivateKey: "key",
				Port:       51820,
			},
			wantErr: true,
			errMsg:  "public key is required",
		},
		{
			name: "invalid port (too low)",
			cfg: &WireGuardConfig{
				PrivateKey: "key",
				PublicKey:  "pub",
				Port:       0,
			},
			wantErr: true,
			errMsg:  "port must be between 1 and 65535",
		},
		{
			name: "invalid port (too high)",
			cfg: &WireGuardConfig{
				PrivateKey: "key",
				PublicKey:  "pub",
				Port:       70000,
			},
			wantErr: true,
			errMsg:  "port must be between 1 and 65535",
		},
		{
			name: "invalid mesh IPv4",
			cfg: &WireGuardConfig{
				PrivateKey: "key",
				PublicKey:  "pub",
				Port:       51820,
				MeshIPv4:   "invalid",
			},
			wantErr: true,
			errMsg:  "invalid IPv4 address",
		},
		{
			name: "invalid mesh IPv6",
			cfg: &WireGuardConfig{
				PrivateKey: "key",
				PublicKey:  "pub",
				Port:       51820,
				MeshIPv6:   "invalid",
			},
			wantErr: true,
			errMsg:  "invalid IPv6 address",
		},
		{
			name: "invalid MTU (too low)",
			cfg: &WireGuardConfig{
				PrivateKey: "key",
				PublicKey:  "pub",
				Port:       51820,
				MTU:        500,
			},
			wantErr: true,
			errMsg:  "MTU must be between 576 and 9000",
		},
		{
			name: "invalid MTU (too high)",
			cfg: &WireGuardConfig{
				PrivateKey: "key",
				PublicKey:  "pub",
				Port:       51820,
				MTU:        10000,
			},
			wantErr: true,
			errMsg:  "MTU must be between 576 and 9000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("WireGuardConfig.Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("WireGuardConfig.Validate() error = %v, expected to contain %q", err, tt.errMsg)
			}
		})
	}
}

func TestAgentConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *AgentConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid config",
			cfg:     DefaultAgentConfig(),
			wantErr: false,
		},
		{
			name: "invalid runtime",
			cfg: func() *AgentConfig {
				cfg := DefaultAgentConfig()
				cfg.Agent.Runtime = "invalid"
				return cfg
			}(),
			wantErr: true,
			errMsg:  "runtime must be one of: auto, native, docker, kubernetes",
		},
		{
			name: "missing colony ID when auto-discover is false",
			cfg: func() *AgentConfig {
				cfg := DefaultAgentConfig()
				cfg.Agent.Colony.AutoDiscover = false
				cfg.Agent.Colony.ID = ""
				return cfg
			}(),
			wantErr: true,
			errMsg:  "colony ID is required when auto_discover is false",
		},
		{
			name: "invalid sample rate (negative)",
			cfg: func() *AgentConfig {
				cfg := DefaultAgentConfig()
				cfg.Telemetry.Filters.SampleRate = -0.1
				return cfg
			}(),
			wantErr: true,
			errMsg:  "sample rate must be between 0.0 and 1.0",
		},
		{
			name: "invalid sample rate (too high)",
			cfg: func() *AgentConfig {
				cfg := DefaultAgentConfig()
				cfg.Telemetry.Filters.SampleRate = 1.5
				return cfg
			}(),
			wantErr: true,
			errMsg:  "sample rate must be between 0.0 and 1.0",
		},
		{
			name: "invalid Beyla sample rate",
			cfg: func() *AgentConfig {
				cfg := DefaultAgentConfig()
				cfg.Beyla.Sampling.Rate = 2.0
				return cfg
			}(),
			wantErr: true,
			errMsg:  "sample rate must be between 0.0 and 1.0",
		},
		{
			name: "invalid max concurrent sessions",
			cfg: func() *AgentConfig {
				cfg := DefaultAgentConfig()
				cfg.Debug.Limits.MaxConcurrentSessions = 0
				return cfg
			}(),
			wantErr: true,
			errMsg:  "max concurrent sessions must be positive",
		},
		{
			name: "invalid CPU profiling frequency",
			cfg: func() *AgentConfig {
				cfg := DefaultAgentConfig()
				cfg.ContinuousProfiling.CPU.FrequencyHz = -1
				return cfg
			}(),
			wantErr: true,
			errMsg:  "CPU profiling frequency must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("AgentConfig.Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("AgentConfig.Validate() error = %v, expected to contain %q", err, tt.errMsg)
			}
		})
	}
}

func TestProjectConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *ProjectConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid config",
			cfg:     DefaultProjectConfig("test-colony"),
			wantErr: false,
		},
		{
			name: "missing version",
			cfg: &ProjectConfig{
				ColonyID: "test",
			},
			wantErr: true,
			errMsg:  "version is required",
		},
		{
			name: "missing colony ID",
			cfg: &ProjectConfig{
				Version: "1",
			},
			wantErr: true,
			errMsg:  "colony ID is required",
		},
		{
			name: "invalid dashboard port",
			cfg: &ProjectConfig{
				Version:  "1",
				ColonyID: "test",
				Dashboard: DashboardConfig{
					Enabled: true,
					Port:    0,
				},
			},
			wantErr: true,
			errMsg:  "dashboard port must be between 1 and 65535",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("ProjectConfig.Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("ProjectConfig.Validate() error = %v, expected to contain %q", err, tt.errMsg)
			}
		})
	}
}

func TestMultiValidationError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *MultiValidationError
		expected string
	}{
		{
			name: "single error",
			err: &MultiValidationError{
				Errors: []ValidationError{
					{Field: "field1", Message: "is required"},
				},
			},
			expected: "field1: is required",
		},
		{
			name: "multiple errors",
			err: &MultiValidationError{
				Errors: []ValidationError{
					{Field: "field1", Message: "is required"},
					{Field: "field2", Message: "is invalid"},
				},
			},
			expected: "validation failed with 2 errors",
		},
		{
			name:     "no errors",
			err:      &MultiValidationError{Errors: []ValidationError{}},
			expected: "no validation errors",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.err.Error()
			if !strings.Contains(result, tt.expected) {
				t.Errorf("MultiValidationError.Error() = %v, expected to contain %q", result, tt.expected)
			}
		})
	}
}
