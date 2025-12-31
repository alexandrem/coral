package config

import (
	"fmt"
	"net"
	"strings"
)

// Validator is the interface for validating configuration.
type Validator interface {
	Validate() error
}

// ValidationError represents a single validation error.
type ValidationError struct {
	Field   string
	Message string
}

// Error implements the error interface.
func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// MultiValidationError represents multiple validation errors.
type MultiValidationError struct {
	Errors []ValidationError
}

// Error implements the error interface.
func (e *MultiValidationError) Error() string {
	if len(e.Errors) == 0 {
		return "no validation errors"
	}

	if len(e.Errors) == 1 {
		return e.Errors[0].Error()
	}

	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("validation failed with %d errors:\n", len(e.Errors)))
	for i, err := range e.Errors {
		builder.WriteString(fmt.Sprintf("  %d. %s\n", i+1, err.Error()))
	}
	return builder.String()
}

// Validate validates GlobalConfig.
func (c *GlobalConfig) Validate() error {
	var errors []ValidationError

	// Validate version
	if c.Version == "" {
		errors = append(errors, ValidationError{
			Field:   "version",
			Message: "version is required",
		})
	}

	// Validate discovery endpoint
	if c.Discovery.Endpoint == "" {
		errors = append(errors, ValidationError{
			Field:   "discovery.endpoint",
			Message: "discovery endpoint is required",
		})
	}

	// Validate discovery timeout
	if c.Discovery.Timeout <= 0 {
		errors = append(errors, ValidationError{
			Field:   "discovery.timeout",
			Message: "discovery timeout must be positive",
		})
	}

	// Validate AI provider
	if c.AI.Provider != "" && c.AI.Provider != "anthropic" && c.AI.Provider != "openai" {
		errors = append(errors, ValidationError{
			Field:   "ai.provider",
			Message: "ai provider must be 'anthropic' or 'openai'",
		})
	}

	// Validate API key source
	if c.AI.APIKeySource != "" && c.AI.APIKeySource != "env" && c.AI.APIKeySource != "keychain" && c.AI.APIKeySource != "file" {
		errors = append(errors, ValidationError{
			Field:   "ai.api_key_source",
			Message: "api key source must be 'env', 'keychain', or 'file'",
		})
	}

	if len(errors) > 0 {
		return &MultiValidationError{Errors: errors}
	}
	return nil
}

// Validate validates ColonyConfig.
func (c *ColonyConfig) Validate() error {
	var errors []ValidationError

	// Validate version
	if c.Version == "" {
		errors = append(errors, ValidationError{
			Field:   "version",
			Message: "version is required",
		})
	}

	// Validate colony ID
	if c.ColonyID == "" {
		errors = append(errors, ValidationError{
			Field:   "colony_id",
			Message: "colony ID is required",
		})
	}

	// Validate application name
	if c.ApplicationName == "" {
		errors = append(errors, ValidationError{
			Field:   "application_name",
			Message: "application name is required",
		})
	}

	// Validate environment
	if c.Environment == "" {
		errors = append(errors, ValidationError{
			Field:   "environment",
			Message: "environment is required",
		})
	}

	// Validate WireGuard configuration
	if err := c.WireGuard.Validate(); err != nil {
		if multiErr, ok := err.(*MultiValidationError); ok {
			for _, e := range multiErr.Errors {
				e.Field = "wireguard." + e.Field
				errors = append(errors, e)
			}
		}
	}

	// Validate ports
	if c.Services.ConnectPort <= 0 || c.Services.ConnectPort > 65535 {
		errors = append(errors, ValidationError{
			Field:   "services.connect_port",
			Message: "connect port must be between 1 and 65535",
		})
	}

	if c.Services.DashboardPort <= 0 || c.Services.DashboardPort > 65535 {
		errors = append(errors, ValidationError{
			Field:   "services.dashboard_port",
			Message: "dashboard port must be between 1 and 65535",
		})
	}

	// Validate discovery mesh ID matches colony ID
	if c.Discovery.Enabled && c.Discovery.MeshID != c.ColonyID {
		errors = append(errors, ValidationError{
			Field:   "discovery.mesh_id",
			Message: "mesh ID must match colony ID",
		})
	}

	if len(errors) > 0 {
		return &MultiValidationError{Errors: errors}
	}
	return nil
}

// Validate validates WireGuardConfig.
func (c *WireGuardConfig) Validate() error {
	var errors []ValidationError

	// Validate private key
	if c.PrivateKey == "" {
		errors = append(errors, ValidationError{
			Field:   "private_key",
			Message: "private key is required",
		})
	}

	// Validate public key
	if c.PublicKey == "" {
		errors = append(errors, ValidationError{
			Field:   "public_key",
			Message: "public key is required",
		})
	}

	// Validate port
	if c.Port <= 0 || c.Port > 65535 {
		errors = append(errors, ValidationError{
			Field:   "port",
			Message: "port must be between 1 and 65535",
		})
	}

	// Validate mesh IPv4
	if c.MeshIPv4 != "" {
		if ip := net.ParseIP(c.MeshIPv4); ip == nil {
			errors = append(errors, ValidationError{
				Field:   "mesh_ipv4",
				Message: "invalid IPv4 address",
			})
		}
	}

	// Validate mesh IPv6
	if c.MeshIPv6 != "" {
		if ip := net.ParseIP(c.MeshIPv6); ip == nil {
			errors = append(errors, ValidationError{
				Field:   "mesh_ipv6",
				Message: "invalid IPv6 address",
			})
		}
	}

	// Validate mesh network IPv4
	if c.MeshNetworkIPv4 != "" {
		if _, _, err := net.ParseCIDR(c.MeshNetworkIPv4); err != nil {
			errors = append(errors, ValidationError{
				Field:   "mesh_network_ipv4",
				Message: "invalid IPv4 CIDR",
			})
		}
	}

	// Validate mesh network IPv6
	if c.MeshNetworkIPv6 != "" {
		if _, _, err := net.ParseCIDR(c.MeshNetworkIPv6); err != nil {
			errors = append(errors, ValidationError{
				Field:   "mesh_network_ipv6",
				Message: "invalid IPv6 CIDR",
			})
		}
	}

	// Validate MTU
	if c.MTU != 0 && (c.MTU < 576 || c.MTU > 9000) {
		errors = append(errors, ValidationError{
			Field:   "mtu",
			Message: "MTU must be between 576 and 9000",
		})
	}

	if len(errors) > 0 {
		return &MultiValidationError{Errors: errors}
	}
	return nil
}

// Validate validates ProjectConfig.
func (c *ProjectConfig) Validate() error {
	var errors []ValidationError

	// Validate version
	if c.Version == "" {
		errors = append(errors, ValidationError{
			Field:   "version",
			Message: "version is required",
		})
	}

	// Validate colony ID
	if c.ColonyID == "" {
		errors = append(errors, ValidationError{
			Field:   "colony_id",
			Message: "colony ID is required",
		})
	}

	// Validate dashboard port if enabled
	if c.Dashboard.Enabled {
		if c.Dashboard.Port <= 0 || c.Dashboard.Port > 65535 {
			errors = append(errors, ValidationError{
				Field:   "dashboard.port",
				Message: "dashboard port must be between 1 and 65535",
			})
		}
	}

	if len(errors) > 0 {
		return &MultiValidationError{Errors: errors}
	}
	return nil
}

// Validate validates AgentConfig.
func (c *AgentConfig) Validate() error {
	var errors []ValidationError

	// Validate runtime
	validRuntimes := map[string]bool{
		"auto":       true,
		"native":     true,
		"docker":     true,
		"kubernetes": true,
	}
	if !validRuntimes[c.Agent.Runtime] {
		errors = append(errors, ValidationError{
			Field:   "agent.runtime",
			Message: "runtime must be one of: auto, native, docker, kubernetes",
		})
	}

	// Validate colony ID if not auto-discover
	if !c.Agent.Colony.AutoDiscover && c.Agent.Colony.ID == "" {
		errors = append(errors, ValidationError{
			Field:   "agent.colony.id",
			Message: "colony ID is required when auto_discover is false",
		})
	}

	// Validate telemetry endpoints
	if !c.Telemetry.Disabled {
		if c.Telemetry.GRPCEndpoint == "" {
			errors = append(errors, ValidationError{
				Field:   "telemetry.grpc_endpoint",
				Message: "gRPC endpoint is required when telemetry is enabled",
			})
		}
		if c.Telemetry.HTTPEndpoint == "" {
			errors = append(errors, ValidationError{
				Field:   "telemetry.http_endpoint",
				Message: "HTTP endpoint is required when telemetry is enabled",
			})
		}
	}

	// Validate sample rate
	if c.Telemetry.Filters.SampleRate < 0 || c.Telemetry.Filters.SampleRate > 1.0 {
		errors = append(errors, ValidationError{
			Field:   "telemetry.filters.sample_rate",
			Message: "sample rate must be between 0.0 and 1.0",
		})
	}

	// Validate Beyla sample rate
	if c.Beyla.Sampling.Rate < 0 || c.Beyla.Sampling.Rate > 1.0 {
		errors = append(errors, ValidationError{
			Field:   "beyla.sampling.rate",
			Message: "sample rate must be between 0.0 and 1.0",
		})
	}

	// Validate debug session limits
	if c.Debug.Limits.MaxConcurrentSessions <= 0 {
		errors = append(errors, ValidationError{
			Field:   "debug.limits.max_concurrent_sessions",
			Message: "max concurrent sessions must be positive",
		})
	}

	if c.Debug.Limits.MaxSessionDuration <= 0 {
		errors = append(errors, ValidationError{
			Field:   "debug.limits.max_session_duration",
			Message: "max session duration must be positive",
		})
	}

	// Validate CPU profiling frequency
	if c.ContinuousProfiling.CPU.FrequencyHz <= 0 {
		errors = append(errors, ValidationError{
			Field:   "continuous_profiling.cpu.frequency_hz",
			Message: "CPU profiling frequency must be positive",
		})
	}

	if len(errors) > 0 {
		return &MultiValidationError{Errors: errors}
	}
	return nil
}
