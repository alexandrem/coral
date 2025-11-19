package agent

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	discoverypb "github.com/coral-io/coral/coral/discovery/v1"
	meshv1 "github.com/coral-io/coral/coral/mesh/v1"
	"github.com/coral-io/coral/coral/mesh/v1/meshv1connect"
	"github.com/coral-io/coral/internal/config"
	"github.com/coral-io/coral/internal/logging"
	wg "github.com/coral-io/coral/internal/wireguard"
)

// ConnectionState represents the current state of the agent's connection to the colony.
type ConnectionState int

const (
	// StateUnregistered indicates the agent has never registered or registration was lost.
	StateUnregistered ConnectionState = iota
	// StateRegistering indicates the agent is currently attempting registration.
	StateRegistering
	// StateRegistered indicates the agent successfully registered with the colony.
	StateRegistered
	// StateHealthy indicates the agent is registered and heartbeats are succeeding.
	StateHealthy
)

// String returns a human-readable representation of the connection state.
func (s ConnectionState) String() string {
	switch s {
	case StateUnregistered:
		return "unregistered"
	case StateRegistering:
		return "registering"
	case StateRegistered:
		return "registered"
	case StateHealthy:
		return "healthy"
	default:
		return "unknown"
	}
}

// ConnectionManager manages the agent's connection lifecycle to the colony.
// It handles initial registration, heartbeats, and automatic reconnection.
type ConnectionManager struct {
	// Configuration
	agentID      string
	colonyInfo   *discoverypb.LookupColonyResponse
	config       *config.ResolvedConfig
	serviceSpecs []*ServiceSpec
	agentPubKey  string
	wgDevice     *wg.Device
	logger       logging.Logger

	// State tracking
	state   ConnectionState
	stateMu sync.RWMutex

	// Connection tracking
	lastSuccessfulHeartbeat time.Time
	consecutiveFailures     int
	assignedIP              string
	assignedSubnet          string

	// Reconnection control
	reconnectTrigger chan struct{}
	backoff          *ExponentialBackoff
}

// ExponentialBackoff implements exponential backoff with jitter for reconnection attempts.
type ExponentialBackoff struct {
	InitialInterval time.Duration
	MaxInterval     time.Duration
	Multiplier      float64
	Jitter          float64
	currentAttempt  int
}

// NextDelay calculates the next backoff delay with exponential growth and jitter.
func (b *ExponentialBackoff) NextDelay() time.Duration {
	baseDelay := float64(b.InitialInterval) * math.Pow(b.Multiplier, float64(b.currentAttempt))
	maxDelay := float64(b.MaxInterval)

	if baseDelay > maxDelay {
		baseDelay = maxDelay
	}

	// Add jitter: randomize between (1-jitter)*baseDelay and (1+jitter)*baseDelay
	jitterAmount := baseDelay * b.Jitter
	delay := baseDelay + (rand.Float64()*2-1)*jitterAmount

	b.currentAttempt++
	return time.Duration(delay)
}

// Reset resets the backoff to initial state.
func (b *ExponentialBackoff) Reset() {
	b.currentAttempt = 0
}

// NewConnectionManager creates a new connection manager for agent-colony communication.
func NewConnectionManager(
	agentID string,
	colonyInfo *discoverypb.LookupColonyResponse,
	cfg *config.ResolvedConfig,
	serviceSpecs []*ServiceSpec,
	agentPubKey string,
	wgDevice *wg.Device,
	logger logging.Logger,
) *ConnectionManager {
	return &ConnectionManager{
		agentID:          agentID,
		colonyInfo:       colonyInfo,
		config:           cfg,
		serviceSpecs:     serviceSpecs,
		agentPubKey:      agentPubKey,
		wgDevice:         wgDevice,
		logger:           logger,
		state:            StateUnregistered,
		reconnectTrigger: make(chan struct{}, 1),
		backoff: &ExponentialBackoff{
			InitialInterval: 1 * time.Second,
			MaxInterval:     5 * time.Minute,
			Multiplier:      2.0,
			Jitter:          0.1,
		},
	}
}

// GetState returns the current connection state.
func (cm *ConnectionManager) GetState() ConnectionState {
	cm.stateMu.RLock()
	defer cm.stateMu.RUnlock()
	return cm.state
}

// setState updates the connection state and logs the transition.
func (cm *ConnectionManager) setState(newState ConnectionState) {
	cm.stateMu.Lock()
	oldState := cm.state
	cm.state = newState
	cm.stateMu.Unlock()

	if oldState != newState {
		cm.logger.Info().
			Str("old_state", oldState.String()).
			Str("new_state", newState.String()).
			Msg("Connection state changed")
	}
}

// AttemptRegistration attempts to register with the colony.
// Returns the assigned IP and subnet on success, or an error on failure.
func (cm *ConnectionManager) AttemptRegistration() (string, string, error) {
	cm.setState(StateRegistering)

	cm.logger.Info().
		Str("agent_id", cm.agentID).
		Int("service_count", len(cm.serviceSpecs)).
		Msg("Attempting registration with colony")

	result, err := registerWithColony(
		cm.config,
		cm.agentID,
		cm.serviceSpecs,
		cm.agentPubKey,
		cm.colonyInfo,
		cm.logger,
	)

	if err != nil {
		cm.setState(StateUnregistered)
		return "", "", err
	}

	// Parse registration result (format: "IP|SUBNET")
	parts := strings.Split(result, "|")
	if len(parts) != 2 {
		cm.setState(StateUnregistered)
		return "", "", fmt.Errorf("invalid registration response format")
	}

	cm.assignedIP = parts[0]
	cm.assignedSubnet = parts[1]
	cm.setState(StateRegistered)

	cm.logger.Info().
		Str("assigned_ip", cm.assignedIP).
		Str("mesh_subnet", cm.assignedSubnet).
		Msg("Successfully registered with colony")

	return cm.assignedIP, cm.assignedSubnet, nil
}

// StartHeartbeatLoop sends periodic heartbeats to the colony and monitors connection health.
func (cm *ConnectionManager) StartHeartbeatLoop(ctx context.Context, interval time.Duration) {
	connectPort := cm.colonyInfo.ConnectPort
	if connectPort == 0 {
		connectPort = 9000
	}

	colonyURL := fmt.Sprintf("http://%s", net.JoinHostPort(cm.colonyInfo.MeshIpv4, fmt.Sprintf("%d", connectPort)))
	client := meshv1connect.NewMeshServiceClient(http.DefaultClient, colonyURL)

	cm.logger.Info().
		Str("agent_id", cm.agentID).
		Str("colony_url", colonyURL).
		Dur("interval", interval).
		Msg("Starting heartbeat loop")

	sendHeartbeat := func() bool {
		heartbeatCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		resp, err := client.Heartbeat(heartbeatCtx, connect.NewRequest(&meshv1.HeartbeatRequest{
			AgentId: cm.agentID,
			Status:  "healthy",
		}))

		if err != nil {
			cm.consecutiveFailures++
			cm.logger.Warn().
				Err(err).
				Str("agent_id", cm.agentID).
				Int("consecutive_failures", cm.consecutiveFailures).
				Msg("Failed to send heartbeat")
			return false
		}

		if !resp.Msg.Ok {
			cm.consecutiveFailures++
			cm.logger.Warn().
				Str("agent_id", cm.agentID).
				Int("consecutive_failures", cm.consecutiveFailures).
				Msg("Heartbeat rejected by colony")
			return false
		}

		// Success - reset failure counter
		cm.consecutiveFailures = 0
		cm.lastSuccessfulHeartbeat = time.Now()
		cm.setState(StateHealthy)
		cm.backoff.Reset()

		cm.logger.Debug().
			Str("agent_id", cm.agentID).
			Msg("Heartbeat sent successfully")
		return true
	}

	// Send first heartbeat immediately.
	sendHeartbeat()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			cm.logger.Info().Msg("Heartbeat loop stopping")
			return
		case <-ticker.C:
			success := sendHeartbeat()
			if !success && cm.consecutiveFailures >= 3 {
				// After 3 consecutive failures (~45 seconds with 15s interval),
				// assume connection is lost and trigger reconnection.
				cm.logger.Warn().
					Int("consecutive_failures", cm.consecutiveFailures).
					Msg("Multiple heartbeat failures detected - triggering reconnection")
				cm.setState(StateUnregistered)
				cm.triggerReconnection()
			}
		}
	}
}

// StartReconnectionLoop runs a background loop that attempts to reconnect when in unregistered state.
func (cm *ConnectionManager) StartReconnectionLoop(ctx context.Context) {
	cm.logger.Info().Msg("Starting reconnection loop")

	for {
		select {
		case <-ctx.Done():
			cm.logger.Info().Msg("Reconnection loop stopping")
			return
		case <-cm.reconnectTrigger:
			// Triggered reconnection - attempt immediately
			cm.attemptReconnection(ctx)
		case <-time.After(5 * time.Second):
			// Periodic check - only reconnect if in unregistered state
			if cm.GetState() == StateUnregistered {
				cm.attemptReconnection(ctx)
			}
		}
	}
}

// attemptReconnection performs a reconnection attempt with exponential backoff.
func (cm *ConnectionManager) attemptReconnection(ctx context.Context) {
	state := cm.GetState()
	if state != StateUnregistered {
		// Already registered or registering, skip
		return
	}

	cm.logger.Info().
		Str("agent_id", cm.agentID).
		Msg("Attempting to reconnect to colony")

	ip, subnet, err := cm.AttemptRegistration()
	if err != nil {
		delay := cm.backoff.NextDelay()
		cm.logger.Warn().
			Err(err).
			Dur("retry_in", delay).
			Msg("Reconnection attempt failed - will retry")

		// Wait with backoff before next attempt
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
			// Continue to next attempt
		}
		return
	}

	// Registration successful - update IP if changed
	if err := cm.updateMeshIP(ip, subnet); err != nil {
		cm.logger.Warn().
			Err(err).
			Msg("Failed to update mesh IP after reconnection")
	}

	cm.logger.Info().
		Str("assigned_ip", ip).
		Msg("Successfully reconnected to colony")
}

// updateMeshIP updates the WireGuard interface IP if it has changed.
func (cm *ConnectionManager) updateMeshIP(newIP, subnet string) error {
	if cm.assignedIP == newIP {
		// IP hasn't changed, no update needed
		return nil
	}

	cm.logger.Info().
		Str("old_ip", cm.assignedIP).
		Str("new_ip", newIP).
		Msg("Updating mesh IP address")

	// Parse IP and subnet
	meshIP := net.ParseIP(newIP)
	if meshIP == nil {
		return fmt.Errorf("invalid IP address: %s", newIP)
	}

	_, meshSubnet, err := net.ParseCIDR(subnet)
	if err != nil {
		return fmt.Errorf("invalid mesh subnet: %w", err)
	}

	iface := cm.wgDevice.Interface()
	if iface == nil {
		return fmt.Errorf("WireGuard device has no interface")
	}

	// Assign new IP to interface
	if err := iface.AssignIP(meshIP, meshSubnet); err != nil {
		return fmt.Errorf("failed to assign IP to interface: %w", err)
	}

	// Flush and refresh routes to clear old IP caching
	if err := cm.wgDevice.FlushAllPeerRoutes(); err != nil {
		cm.logger.Warn().Err(err).Msg("Failed to flush peer routes")
	}

	time.Sleep(200 * time.Millisecond)

	if err := cm.wgDevice.RefreshPeerRoutes(); err != nil {
		cm.logger.Warn().Err(err).Msg("Failed to refresh peer routes after IP change")
	}

	cm.assignedIP = newIP
	cm.assignedSubnet = subnet

	return nil
}

// triggerReconnection signals the reconnection loop to attempt reconnection immediately.
func (cm *ConnectionManager) triggerReconnection() {
	select {
	case cm.reconnectTrigger <- struct{}{}:
		// Trigger sent
	default:
		// Channel already has a pending trigger, skip
	}
}

// GetAssignedIP returns the currently assigned mesh IP and subnet.
func (cm *ConnectionManager) GetAssignedIP() (string, string) {
	cm.stateMu.RLock()
	defer cm.stateMu.RUnlock()
	return cm.assignedIP, cm.assignedSubnet
}
