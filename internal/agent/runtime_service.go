package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/coral-io/coral/coral/agent/v1"
	"github.com/coral-io/coral/internal/runtime"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// RuntimeService implements the AgentService RPC interface.
type RuntimeService struct {
	detector        *runtime.Detector
	runtimeContext  *agentv1.RuntimeContextResponse
	logger          zerolog.Logger
	mu              sync.RWMutex
	refreshInterval time.Duration
	ctx             context.Context
	cancel          context.CancelFunc
}

// RuntimeServiceConfig contains configuration for the runtime service.
type RuntimeServiceConfig struct {
	Logger          zerolog.Logger
	Version         string
	RefreshInterval time.Duration
}

// NewRuntimeService creates a new runtime service.
func NewRuntimeService(config RuntimeServiceConfig) (*RuntimeService, error) {
	if config.RefreshInterval == 0 {
		config.RefreshInterval = 5 * time.Minute // Default 5 minutes
	}

	ctx, cancel := context.WithCancel(context.Background())

	detector := runtime.NewDetector(config.Logger, config.Version)

	return &RuntimeService{
		detector:        detector,
		logger:          config.Logger.With().Str("component", "runtime_service").Logger(),
		refreshInterval: config.RefreshInterval,
		ctx:             ctx,
		cancel:          cancel,
	}, nil
}

// Start begins the runtime service and performs initial detection.
func (s *RuntimeService) Start() error {
	s.logger.Info().Msg("Starting runtime service")

	// Perform initial detection.
	if err := s.refreshContext(); err != nil {
		return fmt.Errorf("initial runtime detection failed: %w", err)
	}

	// Start periodic refresh goroutine.
	go s.periodicRefresh()

	return nil
}

// Stop stops the runtime service.
func (s *RuntimeService) Stop() error {
	s.logger.Info().Msg("Stopping runtime service")
	s.cancel()
	return nil
}

// GetRuntimeContext implements the GetRuntimeContext RPC.
func (s *RuntimeService) GetRuntimeContext(
	ctx context.Context,
	req *agentv1.GetRuntimeContextRequest,
) (*agentv1.RuntimeContextResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.runtimeContext == nil {
		return nil, fmt.Errorf("runtime context not yet detected")
	}

	return s.runtimeContext, nil
}

// GetCachedContext returns the cached runtime context.
func (s *RuntimeService) GetCachedContext() *agentv1.RuntimeContextResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.runtimeContext
}

// RefreshContext manually triggers a runtime context refresh.
func (s *RuntimeService) RefreshContext() error {
	return s.refreshContext()
}

// refreshContext performs runtime context detection and updates the cache.
func (s *RuntimeService) refreshContext() error {
	s.logger.Debug().Msg("Refreshing runtime context")

	newContext, err := s.detector.Detect(s.ctx)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to detect runtime context")
		return err
	}

	s.mu.Lock()
	oldContext := s.runtimeContext
	s.runtimeContext = newContext
	s.mu.Unlock()

	// Check if context changed.
	if oldContext != nil && s.hasContextChanged(oldContext, newContext) {
		s.logger.Info().Msg("Runtime context changed - re-registration required")
		// TODO: Trigger re-registration with colony
	}

	return nil
}

// periodicRefresh periodically refreshes the runtime context.
func (s *RuntimeService) periodicRefresh() {
	ticker := time.NewTicker(s.refreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			if err := s.refreshContext(); err != nil {
				s.logger.Warn().Err(err).Msg("Periodic runtime context refresh failed")
			}
		}
	}
}

// hasContextChanged checks if the runtime context has changed significantly.
func (s *RuntimeService) hasContextChanged(
	old *agentv1.RuntimeContextResponse,
	new *agentv1.RuntimeContextResponse,
) bool {
	// Check runtime type.
	if old.RuntimeType != new.RuntimeType {
		return true
	}

	// Check sidecar mode.
	if old.SidecarMode != new.SidecarMode {
		return true
	}

	// Check CRI socket availability.
	oldHasCRI := old.CriSocket != nil
	newHasCRI := new.CriSocket != nil
	if oldHasCRI != newHasCRI {
		return true
	}

	// Check CRI version if both have CRI.
	if oldHasCRI && newHasCRI {
		if old.CriSocket.Version != new.CriSocket.Version {
			return true
		}
	}

	// Check capabilities.
	if old.Capabilities.CanRun != new.Capabilities.CanRun ||
		old.Capabilities.CanExec != new.Capabilities.CanExec ||
		old.Capabilities.CanShell != new.Capabilities.CanShell {
		return true
	}

	// Check visibility scope.
	if len(old.Visibility.ContainerIds) != len(new.Visibility.ContainerIds) {
		return true
	}

	return false
}

// GetDetectedAt returns when the runtime context was last detected.
func (s *RuntimeService) GetDetectedAt() *timestamppb.Timestamp {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.runtimeContext == nil {
		return nil
	}

	return s.runtimeContext.DetectedAt
}

// RuntimeServiceAdapter adapts RuntimeService to the Connect RPC AgentServiceHandler interface.
type RuntimeServiceAdapter struct {
	service *RuntimeService
}

// NewRuntimeServiceAdapter creates a new adapter for the runtime service.
func NewRuntimeServiceAdapter(service *RuntimeService) *RuntimeServiceAdapter {
	return &RuntimeServiceAdapter{service: service}
}

// GetRuntimeContext implements the Connect RPC handler interface.
func (a *RuntimeServiceAdapter) GetRuntimeContext(
	ctx context.Context,
	req *connect.Request[agentv1.GetRuntimeContextRequest],
) (*connect.Response[agentv1.RuntimeContextResponse], error) {
	// Call the underlying service.
	resp, err := a.service.GetRuntimeContext(ctx, req.Msg)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(resp), nil
}
