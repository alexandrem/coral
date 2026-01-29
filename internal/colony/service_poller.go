package colony

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/colony/database"
	"github.com/coral-mesh/coral/internal/colony/poller"
	"github.com/coral-mesh/coral/internal/colony/registry"
)

// ServicePoller periodically queries agents for their connected services.
// This ensures the colony's service registry stays in sync with agent state.
type ServicePoller struct {
	*poller.BasePoller
	registry     *registry.Registry
	db           *database.Database
	pollInterval time.Duration
	logger       zerolog.Logger
}

// NewServicePoller creates a new service poller.
func NewServicePoller(
	ctx context.Context,
	registry *registry.Registry,
	db *database.Database,
	pollInterval time.Duration,
	logger zerolog.Logger,
) *ServicePoller {
	componentLogger := logger.With().Str("component", "service_poller").Logger()

	base := poller.NewBasePoller(ctx, poller.Config{
		Name:         "service_poller",
		PollInterval: pollInterval,
		Logger:       componentLogger,
	})

	return &ServicePoller{
		BasePoller:   base,
		registry:     registry,
		db:           db,
		pollInterval: pollInterval,
		logger:       componentLogger,
	}
}

// Start begins the service polling loop.
func (p *ServicePoller) Start() error {
	return p.BasePoller.Start(p)
}

// Stop stops the service polling loop.
func (p *ServicePoller) Stop() error {
	return p.BasePoller.Stop()
}

// PollOnce performs a single polling cycle.
// Implements the poller.Poller interface.
func (p *ServicePoller) PollOnce(ctx context.Context) error {
	totalServices := 0

	successCount, errorCount := poller.ForEachHealthyAgent(p.registry, p.logger, func(agent *registry.Entry) error {
		services, err := p.queryAgent(ctx, agent)
		if err != nil {
			return err
		}

		// Register/update each service in the colony database.
		for _, svc := range services {
			serviceID := fmt.Sprintf("%s-%s", agent.AgentID, svc.Name)

			now := time.Now()
			dbService := &database.Service{
				ID:           serviceID,
				Name:         svc.Name,
				AppID:        svc.Name, // Use service name as app ID for now.
				Version:      "",       // Version not available from ListServices.
				AgentID:      agent.AgentID,
				Labels:       "", // Convert labels map to JSON if needed.
				Status:       "active",
				RegisteredAt: now,
				LastSeen:     now,
			}

			if err := p.db.UpsertService(ctx, dbService); err != nil {
				p.logger.Error().
					Err(err).
					Str("service_id", serviceID).
					Str("service_name", svc.Name).
					Str("agent_id", agent.AgentID).
					Msg("Failed to upsert service")
				continue
			}

			totalServices++
		}

		return nil
	})

	p.logger.Info().
		Int("agents_queried", successCount).
		Int("agents_failed", errorCount).
		Int("total_services", totalServices).
		Msg("Service poll cycle complete")

	return nil
}

// RunCleanup performs cleanup operations.
// For services, we don't delete old entries - they're managed by active/inactive status.
func (p *ServicePoller) RunCleanup(ctx context.Context) error {
	// No cleanup needed for services.
	// Services are marked inactive rather than deleted.
	return nil
}

// queryAgent queries a specific agent for its connected services.
func (p *ServicePoller) queryAgent(
	ctx context.Context,
	agent *registry.Entry,
) ([]*agentv1.ServiceStatus, error) {
	// Get agent client using mesh IP.
	client := GetAgentClient(agent)

	// Query agent for services with timeout.
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := client.ListServices(queryCtx, connect.NewRequest(&agentv1.ListServicesRequest{}))
	if err != nil {
		return nil, fmt.Errorf("failed to call ListServices: %w", err)
	}

	return resp.Msg.Services, nil
}
