package startup

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/coral-mesh/coral/internal/cli/agent/types"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/logging"
	"github.com/coral-mesh/coral/internal/privilege"
	pkgruntime "github.com/coral-mesh/coral/internal/runtime"
)

// PreflightValidator handles agent prerequisite validation.
type PreflightValidator struct {
	logger logging.Logger
}

// NewPreflightValidator creates a new preflight validator.
func NewPreflightValidator(logger logging.Logger) *PreflightValidator {
	return &PreflightValidator{
		logger: logger,
	}
}

// Validate performs agent preflight checks with graceful degradation.
// On Linux, missing capabilities result in warnings allowing reduced functionality.
// On macOS, root is required (returns error if missing).
func (v *PreflightValidator) Validate() error {
	v.logger.Info().Msg("Running agent preflight checks...")

	var warnings []string
	hasFullCapabilities := true

	// Check if running as root or with sudo.
	isRoot := privilege.IsRoot()

	// On macOS, we must run as root (no capability system, no graceful degradation).
	if runtime.GOOS == "darwin" && !isRoot {
		return fmt.Errorf("agent must be run with sudo on macOS:\n  sudo coral agent start\n\n" +
			"macOS requires root privileges for TUN device creation and configuration")
	}

	if !isRoot {
		warnings = append(warnings, "Not running as root - TUN device creation may fail")
		hasFullCapabilities = false
		v.logger.Warn().Msg("⚠️  Not running with elevated privileges")
	} else {
		v.logger.Debug().Msg("✓ Running with elevated privileges")

		// Detect original user for privilege context.
		if privilege.IsRunningUnderSudo() {
			userCtx, err := privilege.DetectOriginalUser()
			if err != nil {
				v.logger.Debug().Err(err).Msg("Could not detect original user from sudo")
			} else {
				v.logger.Debug().
					Str("user", userCtx.Username).
					Int("uid", userCtx.UID).
					Msg("Detected original user from sudo")
			}
		}
	}

	// Detect Linux capabilities (Linux-specific).
	if runtime.GOOS == "linux" {
		caps, err := pkgruntime.DetectLinuxCapabilities()
		if err != nil {
			v.logger.Warn().Err(err).Msg("Failed to detect Linux capabilities")
			warnings = append(warnings, "Could not detect capabilities - assuming degraded mode")
			hasFullCapabilities = false
		} else {
			v.logger.Info().Msg("Detected Linux capabilities:")

			// Check required capabilities and report status.
			checkCap := func(name string, has bool, required bool, purpose string) {
				status := "✓"
				if !has {
					status = "✗"
					if required {
						warnings = append(warnings, fmt.Sprintf("Missing %s - %s unavailable", name, purpose))
						hasFullCapabilities = false
					}
				}
				v.logger.Info().Msgf("  %s %s: %s", status, name, purpose)
			}

			checkCap("CAP_NET_ADMIN", caps.CapNetAdmin, true, "TUN device, network config")
			checkCap("CAP_SYS_PTRACE", caps.CapSysPtrace, true, "Process tracing")
			checkCap("CAP_SYS_RESOURCE", caps.CapSysResource, true, "Memory locking for eBPF")
			checkCap("CAP_BPF", caps.CapBpf, false, "eBPF operations (Linux 5.8+)")
			checkCap("CAP_PERFMON", caps.CapPerfmon, false, "CPU profiling (Linux 5.8+)")
			checkCap("CAP_SYSLOG", caps.CapSyslog, false, "Kernel symbols (CPU profiling)")
			checkCap("CAP_SYS_ADMIN", caps.CapSysAdmin, false, "nsenter exec mode + fallback for older kernels")

			// Verify we have eBPF capabilities (either CAP_BPF or CAP_SYS_ADMIN).
			if !caps.CapBpf && !caps.CapSysAdmin {
				warnings = append(warnings, "eBPF requires CAP_BPF or CAP_SYS_ADMIN")
				hasFullCapabilities = false
			}

			// Verify we have perf capabilities (either CAP_PERFMON or CAP_SYS_ADMIN).
			if !caps.CapPerfmon && !caps.CapSysAdmin {
				warnings = append(warnings, "CPU profiling requires CAP_PERFMON or CAP_SYS_ADMIN")
				hasFullCapabilities = false
			}
		}
	} else if isRoot {
		// Non-Linux: just check root.
		v.logger.Info().Msg("Running as root (non-Linux platform)")
		v.logger.Info().Msg("  ✓ Full privileges available")
	}

	// Report overall status.
	if len(warnings) > 0 {
		v.logger.Warn().Msg("Agent will start with reduced functionality:")
		for _, w := range warnings {
			v.logger.Warn().Msg("  ⚠️  " + w)
		}
		if runtime.GOOS == "linux" {
			v.logger.Info().Msg("To enable all capabilities:")
			v.logger.Info().Msg("  sudo setcap 'cap_net_admin,cap_sys_admin,cap_sys_ptrace,cap_sys_resource,cap_bpf+ep' $(which coral)")
		} else {
			v.logger.Info().Msg("  Run with: sudo coral agent start")
		}
	}

	if hasFullCapabilities {
		v.logger.Info().Msg("✓ All required capabilities available")
	} else {
		v.logger.Info().Msg("⚠️  Starting in degraded mode with available capabilities")
	}

	return nil
}

// ConfigValidator handles configuration loading and validation.
type ConfigValidator struct {
	logger           logging.Logger
	configFile       string
	colonyIDOverride string
	connectServices  []string
	monitorAll       bool
}

// NewConfigValidator creates a new config validator.
func NewConfigValidator(logger logging.Logger, configFile, colonyIDOverride string, connectServices []string, monitorAll bool) *ConfigValidator {
	return &ConfigValidator{
		logger:           logger,
		configFile:       configFile,
		colonyIDOverride: colonyIDOverride,
		connectServices:  connectServices,
		monitorAll:       monitorAll,
	}
}

// ConfigResult contains the results of config validation.
type ConfigResult struct {
	Config       *config.ResolvedConfig
	ServiceSpecs []*types.ServiceSpec
	AgentConfig  *config.AgentConfig
	AgentMode    string
}

// Validate loads and validates agent configuration.
func (v *ConfigValidator) Validate() (*ConfigResult, error) {
	// Load configuration.
	cfg, serviceSpecs, agentCfg, err := v.loadAgentConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load agent configuration: %w", err)
	}

	// Parse services from --connect flag (RFD 053).
	if len(v.connectServices) > 0 {
		connectSpecs, err := types.ParseMultipleServiceSpecs(v.connectServices)
		if err != nil {
			return nil, fmt.Errorf("failed to parse --connect services: %w", err)
		}
		// Merge with config file services (--connect takes precedence).
		serviceSpecs = append(serviceSpecs, connectSpecs...)
	}

	// Validate service specs (if any provided).
	if len(serviceSpecs) > 0 {
		if err := types.ValidateServiceSpecs(serviceSpecs); err != nil {
			return nil, fmt.Errorf("invalid service configuration: %w", err)
		}
	}

	// Determine agent mode.
	agentMode := "passive"
	if v.monitorAll {
		agentMode = "monitor-all"
	} else if len(serviceSpecs) > 0 {
		agentMode = "active"
	}

	v.logger.Info().
		Str("colony_id", cfg.ColonyID).
		Int("service_count", len(serviceSpecs)).
		Str("runtime", "auto-detect").
		Str("mode", agentMode).
		Msg("Starting Coral agent")

	switch agentMode {
	case "passive":
		v.logger.Info().Msg("Agent running in passive mode - use 'coral connect' to attach services")
	case "monitor-all":
		v.logger.Info().Msg("Agent running in monitor-all mode - auto-discovering processes")
	}

	// Log service configuration.
	for _, spec := range serviceSpecs {
		v.logger.Info().
			Str("service", spec.Name).
			Int32("port", spec.Port).
			Str("health_endpoint", spec.HealthEndpoint).
			Str("type", spec.ServiceType).
			Msg("Configured service")
	}

	return &ConfigResult{
		Config:       cfg,
		ServiceSpecs: serviceSpecs,
		AgentConfig:  agentCfg,
		AgentMode:    agentMode,
	}, nil
}

// loadAgentConfig loads agent configuration from file and environment variables.
func (v *ConfigValidator) loadAgentConfig() (*config.ResolvedConfig, []*types.ServiceSpec, *config.AgentConfig, error) {
	agentCfg := config.DefaultAgentConfig()
	var serviceSpecs []*types.ServiceSpec

	// Try to load config file.
	configFile := v.configFile
	if configFile == "" {
		// Check default locations.
		defaultPaths := []string{
			"./agent.yaml",
			"/etc/coral/agent.yaml",
		}
		for _, path := range defaultPaths {
			if _, err := os.Stat(path); err == nil {
				configFile = path
				break
			}
		}
	}

	if configFile != "" {
		//nolint:gosec // G304: Config file path from command line argument.
		data, err := os.ReadFile(configFile)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to read config file %s: %w", configFile, err)
		}

		if err := yaml.Unmarshal(data, agentCfg); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to parse config file %s: %w", configFile, err)
		}

		// Parse services from config file.
		for _, svc := range agentCfg.Services {
			spec := &types.ServiceSpec{
				Name:           svc.Name,
				Port:           int32(svc.Port),
				HealthEndpoint: svc.HealthEndpoint,
				ServiceType:    svc.Type,
				Labels:         make(map[string]string),
			}
			serviceSpecs = append(serviceSpecs, spec)
		}
	}

	// Check environment variables (they take precedence).
	if envColonyID := os.Getenv("CORAL_COLONY_ID"); envColonyID != "" {
		agentCfg.Agent.Colony.ID = envColonyID
	}
	if heartbeat := os.Getenv("CORAL_HEARTBEAT_INTERVAL"); heartbeat != "" {
		interval, err := time.ParseDuration(heartbeat)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to parse CORAL_HEARTBEAT_INTERVAL: %w", err)
		}
		agentCfg.Agent.HeartbeatInterval = interval
	}
	if memInterval := os.Getenv("CORAL_MEMORY_PROFILING_INTERVAL"); memInterval != "" {
		interval, err := time.ParseDuration(memInterval)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to parse CORAL_MEMORY_PROFILING_INTERVAL: %w", err)
		}
		agentCfg.ContinuousProfiling.Memory.Interval = interval
	}
	if memDisabled := os.Getenv("CORAL_MEMORY_PROFILING_DISABLED"); memDisabled != "" {
		agentCfg.ContinuousProfiling.Memory.Disabled = memDisabled == "true" || memDisabled == "1"
	}

	// Apply colony ID override from flag.
	if v.colonyIDOverride != "" {
		agentCfg.Agent.Colony.ID = v.colonyIDOverride
	}

	// Parse CORAL_SERVICES environment variable.
	envServices := os.Getenv("CORAL_SERVICES")
	if envServices != "" {
		// Format: name:port[:health][:type],name:port[:health][:type],...
		envSpecs, err := types.ParseMultipleServiceSpecs(strings.Split(envServices, ","))
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to parse CORAL_SERVICES: %w", err)
		}
		// Environment services override config file services.
		serviceSpecs = envSpecs
	}

	// Resolve colony configuration.
	resolver, err := config.NewResolver()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create config resolver: %w", err)
	}

	// Determine colony ID.
	colonyID := agentCfg.Agent.Colony.ID
	if colonyID == "" {
		colonyID, err = resolver.ResolveColonyID()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to resolve colony ID: %w\n\nRun 'coral init <app-name>' or set CORAL_COLONY_ID", err)
		}
	}

	// Load resolved configuration.
	cfg, err := resolver.ResolveConfig(colonyID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to load colony config: %w", err)
	}

	return cfg, serviceSpecs, agentCfg, nil
}

// GenerateAgentID generates a stable agent ID based on hostname and service specs.
// The ID remains consistent across agent restarts to maintain identity in the colony.
func GenerateAgentID(serviceSpecs []*types.ServiceSpec) string {
	// Get hostname for stable identification.
	hostname, err := os.Hostname()
	if err != nil {
		// Fallback to "unknown" if hostname cannot be determined.
		hostname = "unknown"
	}

	// Sanitize hostname: replace dots and underscores with hyphens.
	hostname = strings.ReplaceAll(hostname, ".", "-")
	hostname = strings.ReplaceAll(hostname, "_", "-")
	hostname = strings.ToLower(hostname)

	if len(serviceSpecs) == 1 {
		// Single service: hostname-servicename.
		// Example: "myserver-frontend", "myserver-api".
		return fmt.Sprintf("%s-%s", hostname, serviceSpecs[0].Name)
	}

	if len(serviceSpecs) > 1 {
		// Multi-service: hostname-multi.
		// Example: "myserver-multi" for an agent monitoring multiple services.
		return fmt.Sprintf("%s-multi", hostname)
	}

	// No services (daemon mode): just hostname.
	// Example: "myserver" for a standalone agent.
	return hostname
}
