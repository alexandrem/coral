package registration

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/coral-io/coral/internal/discovery/client"
)

// Config contains configuration for the registration manager.
type Config struct {
	// Enabled controls whether registration is enabled.
	Enabled bool

	// AutoRegister controls whether to automatically register on start.
	AutoRegister bool

	// RegisterInterval is how often to re-register (heartbeat).
	RegisterInterval time.Duration

	// MeshID is the colony's unique identifier.
	MeshID string

	// PublicKey is the WireGuard public key.
	PublicKey string

	// Endpoints are the WireGuard endpoints.
	Endpoints []string

	// MeshIPv4 is the colony's mesh IPv4 address.
	MeshIPv4 string

	// MeshIPv6 is the colony's mesh IPv6 address.
	MeshIPv6 string

	// ConnectPort is the Buf Connect HTTP/2 port.
	ConnectPort uint32

	// Metadata contains additional colony information.
	Metadata map[string]string

	// DiscoveryEndpoint is the discovery service URL.
	DiscoveryEndpoint string

	// DiscoveryTimeout is the timeout for discovery requests.
	DiscoveryTimeout time.Duration

	// ObservedEndpoint is the colony's observed public endpoint from STUN.
	ObservedEndpoint interface{} // *discoveryv1.Endpoint (avoiding import cycle)
}

// Manager handles continuous registration and reconnection.
type Manager struct {
	config Config
	client *client.Client
	logger zerolog.Logger

	// State tracking.
	mu              sync.RWMutex
	registered      bool
	currentTTL      time.Duration
	expiresAt       time.Time
	lastRegisterErr error

	// Lifecycle management.
	stopCh chan struct{}
	doneCh chan struct{}
	wg     sync.WaitGroup
}

// NewManager creates a new registration manager.
func NewManager(cfg Config, logger zerolog.Logger) *Manager {
	return &Manager{
		config: cfg,
		client: client.New(cfg.DiscoveryEndpoint, cfg.DiscoveryTimeout),
		logger: logger,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

// Start begins the registration manager lifecycle.
// It performs initial registration and starts the heartbeat loop.
func (m *Manager) Start(ctx context.Context) error {
	if !m.config.Enabled {
		m.logger.Debug().Msg("Discovery registration disabled")
		return nil
	}

	if !m.config.AutoRegister {
		m.logger.Debug().Msg("Auto-register disabled")
		return nil
	}

	m.logger.Info().
		Str("mesh_id", m.config.MeshID).
		Dur("interval", m.config.RegisterInterval).
		Msg("Starting registration manager")

	// Perform initial registration with retries.
	if err := m.registerWithRetry(ctx); err != nil {
		m.logger.Warn().
			Err(err).
			Msg("Initial registration failed, will retry in background")
		// Don't fail startup - continue with background retries.
	}

	// Start heartbeat goroutine.
	m.wg.Add(1)
	go m.heartbeatLoop()

	return nil
}

// Stop gracefully shuts down the registration manager.
func (m *Manager) Stop() error {
	m.logger.Info().Msg("Stopping registration manager")
	close(m.stopCh)

	// Wait for heartbeat loop to exit with timeout.
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		m.logger.Info().Msg("Registration manager stopped")
	case <-time.After(5 * time.Second):
		m.logger.Warn().Msg("Registration manager stop timeout")
	}

	close(m.doneCh)
	return nil
}

// Done returns a channel that is closed when the manager is fully stopped.
func (m *Manager) Done() <-chan struct{} {
	return m.doneCh
}

// IsRegistered returns whether the colony is currently registered.
func (m *Manager) IsRegistered() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.registered && time.Now().Before(m.expiresAt)
}

// Status returns the current registration status.
func (m *Manager) Status() (registered bool, expiresAt time.Time, lastErr error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.registered, m.expiresAt, m.lastRegisterErr
}

// registerWithRetry attempts registration with exponential backoff.
func (m *Manager) registerWithRetry(ctx context.Context) error {
	const (
		maxRetries     = 5
		initialBackoff = 1 * time.Second
		maxBackoff     = 30 * time.Second
	)

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(math.Pow(2, float64(attempt-1))) * initialBackoff
			if backoff > maxBackoff {
				backoff = maxBackoff
			}

			m.logger.Debug().
				Int("attempt", attempt+1).
				Dur("backoff", backoff).
				Msg("Retrying registration after backoff")

			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			case <-m.stopCh:
				return fmt.Errorf("manager stopped during retry")
			}
		}

		if err := m.register(ctx); err != nil {
			lastErr = err
			m.logger.Warn().
				Err(err).
				Int("attempt", attempt+1).
				Int("max_retries", maxRetries).
				Msg("Registration attempt failed")
			continue
		}

		// Success!
		return nil
	}

	return fmt.Errorf("registration failed after %d attempts: %w", maxRetries, lastErr)
}

// register performs a single registration attempt.
func (m *Manager) register(ctx context.Context) error {
	// Check health first.
	healthCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := m.client.Health(healthCtx); err != nil {
		m.mu.Lock()
		m.lastRegisterErr = fmt.Errorf("health check failed: %w", err)
		m.mu.Unlock()
		return m.lastRegisterErr
	}

	// Perform registration.
	regCtx, regCancel := context.WithTimeout(ctx, 10*time.Second)
	defer regCancel()

	// Convert observed endpoint (avoid import cycle by using interface{})
	var observedEndpoint interface{}
	if m.config.ObservedEndpoint != nil {
		observedEndpoint = m.config.ObservedEndpoint
	}

	req := &client.RegisterColonyRequest{
		MeshID:           m.config.MeshID,
		PublicKey:        m.config.PublicKey,
		Endpoints:        m.config.Endpoints,
		MeshIPv4:         m.config.MeshIPv4,
		MeshIPv6:         m.config.MeshIPv6,
		ConnectPort:      m.config.ConnectPort,
		Metadata:         m.config.Metadata,
		ObservedEndpoint: observedEndpoint,
	}

	m.logger.Debug().
		Str("mesh_id", req.MeshID).
		Str("pubkey", req.PublicKey).
		Strs("endpoints", req.Endpoints).
		Str("mesh_ipv4", req.MeshIPv4).
		Str("mesh_ipv6", req.MeshIPv6).
		Uint32("connect_port", req.ConnectPort).
		Interface("metadata", req.Metadata).
		Msg("Sending registration request")

	resp, err := m.client.RegisterColony(regCtx, req)
	if err != nil {
		m.mu.Lock()
		m.lastRegisterErr = err
		m.mu.Unlock()
		return err
	}

	if !resp.Success {
		err := fmt.Errorf("registration returned success=false")
		m.mu.Lock()
		m.lastRegisterErr = err
		m.mu.Unlock()
		return err
	}

	// Update state.
	m.mu.Lock()
	m.registered = true
	m.currentTTL = time.Duration(resp.TTL) * time.Second
	m.expiresAt = resp.ExpiresAt
	m.lastRegisterErr = nil
	m.mu.Unlock()

	m.logger.Info().
		Int32("ttl_seconds", resp.TTL).
		Time("expires_at", resp.ExpiresAt).
		Str("mesh_id", m.config.MeshID).
		Msg("Successfully registered with discovery service")

	return nil
}

// heartbeatLoop runs the continuous registration loop.
func (m *Manager) heartbeatLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.config.RegisterInterval)
	defer ticker.Stop()

	m.logger.Debug().
		Dur("interval", m.config.RegisterInterval).
		Msg("Heartbeat loop started")

	for {
		select {
		case <-m.stopCh:
			m.logger.Debug().Msg("Heartbeat loop stopped")
			return

		case <-ticker.C:
			m.logger.Debug().Msg("Heartbeat tick: re-registering")

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			err := m.registerWithRetry(ctx)
			cancel()

			if err != nil {
				m.logger.Error().
					Err(err).
					Msg("Heartbeat registration failed")
			} else {
				m.logger.Debug().Msg("Heartbeat registration succeeded")
			}
		}
	}
}
