// Package startup provides agent server initialization and lifecycle management.
package startup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/coral-mesh/coral/internal/agent/bootstrap"
	"github.com/coral-mesh/coral/internal/agent/certs"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/logging"
)

// BootstrapPhase handles certificate bootstrap during agent startup.
// Implements RFD 048 - Agent Certificate Bootstrap.
type BootstrapPhase struct {
	logger      logging.Logger
	agentConfig *config.AgentConfig
	colonyID    string
	agentID     string
}

// BootstrapResult contains the result of the bootstrap phase.
type BootstrapResult struct {
	// CertManager is the certificate manager with loaded credentials.
	CertManager *certs.Manager

	// Bootstrapped indicates whether a new certificate was obtained.
	Bootstrapped bool
}

// NewBootstrapPhase creates a new bootstrap phase handler.
func NewBootstrapPhase(
	logger logging.Logger,
	agentConfig *config.AgentConfig,
	colonyID string,
	agentID string,
) *BootstrapPhase {
	return &BootstrapPhase{
		logger:      logger,
		agentConfig: agentConfig,
		colonyID:    colonyID,
		agentID:     agentID,
	}
}

// Execute runs the bootstrap phase.
// Returns a BootstrapResult indicating the state of certificate credentials.
func (bp *BootstrapPhase) Execute(ctx context.Context) (*BootstrapResult, error) {
	// Get bootstrap config.
	bootstrapCfg := bp.agentConfig.Agent.Bootstrap

	// Check if bootstrap is enabled.
	// Default is enabled unless explicitly disabled.
	// Environment variables are loaded via config.MergeFromEnv
	if !bootstrapCfg.Enabled && bootstrapCfg.CAFingerprint == "" {
		return nil, fmt.Errorf("certificate bootstrap required: set ca_fingerprint or CORAL_CA_FINGERPRINT")
	}

	// Create certificate manager.
	certManager := certs.NewManager(certs.Config{
		CertsDir: bootstrapCfg.CertsDir,
		Logger:   bp.logger,
	})

	// Check if we already have a valid certificate.
	if certManager.CertificateExists() {
		if err := certManager.Load(); err == nil {
			info := certManager.GetCertificateInfo()
			switch info.Status {
			case certs.CertStatusValid:
				bp.logger.Info().
					Str("agent_id", info.AgentID).
					Int("days_remaining", info.DaysRemaining).
					Msg("Using existing valid certificate")
				return &BootstrapResult{
					CertManager:  certManager,
					Bootstrapped: false,
				}, nil

			case certs.CertStatusRenewalNeeded:
				bp.logger.Info().
					Str("agent_id", info.AgentID).
					Int("days_remaining", info.DaysRemaining).
					Msg("Certificate valid but renewal recommended")
				// Continue using existing certificate, renewal will happen in background.
				return &BootstrapResult{
					CertManager:  certManager,
					Bootstrapped: false,
				}, nil

			case certs.CertStatusExpiringSoon:
				bp.logger.Warn().
					Str("agent_id", info.AgentID).
					Int("days_remaining", info.DaysRemaining).
					Msg("Certificate expiring soon, attempting renewal")
				// Try to renew, but continue if it fails.

			case certs.CertStatusExpired:
				bp.logger.Warn().Msg("Certificate expired, need to bootstrap")
				// Fall through to bootstrap.
			}
		} else {
			bp.logger.Warn().Err(err).Msg("Failed to load existing certificate")
		}
	}

	// CA fingerprint is loaded from config (env var override via MergeFromEnv)
	fingerprint := bootstrapCfg.CAFingerprint

	if fingerprint == "" {
		return nil, fmt.Errorf("certificate bootstrap required but no CA fingerprint configured")
	}

	// Discovery endpoint is loaded from config (env var override via MergeFromEnv)
	discoveryURL := ""
	// Try to load from global config (which will merge CORAL_DISCOVERY_ENDPOINT if set).
	loader, err := config.NewLoader()
	if err == nil {
		globalCfg, err := loader.LoadGlobalConfig()
		if err == nil && globalCfg.Discovery.Endpoint != "" {
			discoveryURL = globalCfg.Discovery.Endpoint
		}
	}

	// Fallback: In containerized environments without global config file,
	// check CORAL_DISCOVERY_ENDPOINT directly
	if discoveryURL == "" {
		discoveryURL = os.Getenv("CORAL_DISCOVERY_ENDPOINT")
	}

	if discoveryURL == "" {
		return nil, fmt.Errorf("certificate bootstrap required but no discovery endpoint configured")
	}

	// Perform bootstrap.
	bp.logger.Info().
		Str("colony_id", bp.colonyID).
		Str("agent_id", bp.agentID).
		Msg("Starting certificate bootstrap")

	// Bootstrap PSK is loaded from config (env var override via MergeFromEnv)
	bootstrapPSK := bootstrapCfg.BootstrapPSK

	client := bootstrap.NewClient(bootstrap.Config{
		AgentID:           bp.agentID,
		ColonyID:          bp.colonyID,
		CAFingerprint:     fingerprint,
		BootstrapPSK:      bootstrapPSK,
		DiscoveryEndpoint: discoveryURL,
		ColonyEndpoint:    bootstrapCfg.ColonyEndpoint,
		Logger:            bp.logger,
	})

	// Apply timeout from config.
	timeout := bootstrapCfg.TotalTimeout
	if timeout == 0 {
		timeout = 5 * time.Minute // Default timeout.
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Initialize metrics for telemetry (RFD 048).
	metrics := bootstrap.NewMetrics(bp.logger)
	startTime := time.Now()

	result, err := client.Bootstrap(ctx)
	duration := time.Since(startTime)

	if err != nil {
		bp.logger.Error().Err(err).Msg("Certificate bootstrap failed")

		// Record metrics based on error type.
		metricResult := bootstrap.MetricResultFailure
		if errors.Is(err, context.DeadlineExceeded) {
			metricResult = bootstrap.MetricResultTimeout
		}

		metrics.RecordBootstrapAttempt(metricResult, duration, bp.agentID, bp.colonyID, err.Error())
		return nil, fmt.Errorf("certificate bootstrap failed: %w", err)
	}

	// Record success metric.
	metrics.RecordBootstrapAttempt(bootstrap.MetricResultSuccess, duration, bp.agentID, bp.colonyID, "")

	// Save the certificate.
	if err := certManager.Save(result); err != nil {
		bp.logger.Error().Err(err).Msg("Failed to save bootstrap certificate")
		return nil, fmt.Errorf("failed to save certificate: %w", err)
	}

	// Save agent ID for persistence.
	if err := certManager.SaveAgentID(bp.agentID); err != nil {
		bp.logger.Warn().Err(err).Msg("Failed to persist agent ID")
	}

	// Reload the certificate.
	if err := certManager.Load(); err != nil {
		bp.logger.Error().Err(err).Msg("Failed to load bootstrap certificate")
		return nil, fmt.Errorf("failed to load certificate: %w", err)
	}

	bp.logger.Info().
		Str("spiffe_id", result.AgentSPIFFEID).
		Time("expires_at", result.ExpiresAt).
		Msg("Certificate bootstrap completed successfully")

	return &BootstrapResult{
		CertManager:  certManager,
		Bootstrapped: true,
	}, nil
}

// ShouldBootstrap checks if bootstrap is needed based on configuration.
func (bp *BootstrapPhase) ShouldBootstrap() bool {
	bootstrapCfg := bp.agentConfig.Agent.Bootstrap

	// Bootstrap is enabled if:
	// 1. Explicitly enabled in config, or
	// 2. CA fingerprint is configured (implies bootstrap intent).
	if bootstrapCfg.Enabled {
		return true
	}

	// CA fingerprint check handled by config loader
	if bootstrapCfg.CAFingerprint != "" {
		return true
	}

	return false
}
