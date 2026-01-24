package startup

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

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/coral/mesh/v1/meshv1connect"
	"github.com/coral-mesh/coral/internal/agent"
	"github.com/coral-mesh/coral/internal/cli/agent/types"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/constants"
	discoveryclient "github.com/coral-mesh/coral/internal/discovery/client"
	"github.com/coral-mesh/coral/internal/logging"
	wg "github.com/coral-mesh/coral/internal/wireguard"
)

// ConnectionState represents the current state of the agent's connection to the colony.
type ConnectionState int

const (
	// StateWaitingDiscovery indicates the agent is waiting for discovery service to become available.
	StateWaitingDiscovery ConnectionState = iota
	// StateUnregistered indicates the agent has never registered or registration was lost.
	StateUnregistered
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
	case StateWaitingDiscovery:
		return "waiting_discovery"
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
// It handles discovery, initial registration, heartbeats, and automatic reconnection.
type ConnectionManager struct {
	// Configuration
	agentID        string
	colonyInfo     *discoveryclient.LookupColonyResponse // May be nil if discovery hasn't succeeded yet
	config         *config.ResolvedConfig
	serviceSpecs   []*types.ServiceSpec
	agentPubKey    string
	wgDevice       *wg.Device
	runtimeService *agent.RuntimeService // RFD 018: Runtime context for registration
	logger         logging.Logger

	// State tracking
	state   ConnectionState
	stateMu sync.RWMutex

	// Connection tracking
	lastSuccessfulHeartbeat time.Time
	consecutiveFailures     int
	assignedIP              string
	assignedSubnet          string
	currentEndpoint         string // Tracks the currently configured WireGuard endpoint
	lastSuccessfulEndpoint  string // Tracks the last WireGuard endpoint that successfully connected
	lastSuccessfulRegURL    string // Tracks the last HTTP registration URL that succeeded

	// Reconnection control
	reconnectTrigger chan struct{}
	discoveryTrigger chan struct{}
	backoff          *ExponentialBackoff
	discoveryBackoff *ExponentialBackoff
	colonyInfoMu     sync.RWMutex // Protects colonyInfo updates
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
	//nolint:gosec // G404: Weak random is acceptable for backoff jitter.
	delay := baseDelay + (rand.Float64()*2-1)*jitterAmount

	b.currentAttempt++
	return time.Duration(delay)
}

// Reset resets the backoff to initial state.
func (b *ExponentialBackoff) Reset() {
	b.currentAttempt = 0
}

// NewConnectionManager creates a new connection manager for agent-colony communication.
// colonyInfo may be nil if discovery service is unavailable at startup.
// runtimeService may be nil if runtime detection is not available yet.
func NewConnectionManager(
	agentID string,
	colonyInfo *discoveryclient.LookupColonyResponse,
	cfg *config.ResolvedConfig,
	serviceSpecs []*types.ServiceSpec,
	agentPubKey string,
	wgDevice *wg.Device,
	runtimeService *agent.RuntimeService,
	logger logging.Logger,
) *ConnectionManager {
	// Determine initial state based on whether we have colony info.
	initialState := StateUnregistered
	if colonyInfo == nil {
		initialState = StateWaitingDiscovery
	}

	return &ConnectionManager{
		agentID:          agentID,
		colonyInfo:       colonyInfo,
		config:           cfg,
		serviceSpecs:     serviceSpecs,
		agentPubKey:      agentPubKey,
		wgDevice:         wgDevice,
		runtimeService:   runtimeService,
		logger:           logger,
		state:            initialState,
		reconnectTrigger: make(chan struct{}, 1),
		discoveryTrigger: make(chan struct{}, 1),
		backoff: &ExponentialBackoff{
			InitialInterval: 1 * time.Second,
			MaxInterval:     5 * time.Minute,
			Multiplier:      2.0,
			Jitter:          0.1,
		},
		discoveryBackoff: &ExponentialBackoff{
			InitialInterval: 2 * time.Second,
			MaxInterval:     2 * time.Minute,
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

// AttemptDiscovery attempts to query the discovery service for colony information.
// Returns the colony info on success, or an error on failure.
func (cm *ConnectionManager) AttemptDiscovery() (*discoveryclient.LookupColonyResponse, error) {
	cm.logger.Info().
		Str("colony_id", cm.config.ColonyID).
		Str("discovery_url", cm.config.DiscoveryURL).
		Msg("Attempting discovery service query")

	colonyInfo, err := QueryDiscoveryForColony(cm.config, cm.logger)
	if err != nil {
		return nil, fmt.Errorf("discovery lookup failed: %w", err)
	}

	// Update colony info with lock.
	cm.colonyInfoMu.Lock()
	cm.colonyInfo = colonyInfo
	cm.colonyInfoMu.Unlock()

	cm.logger.Info().
		Str("colony_pubkey", colonyInfo.Pubkey).
		Strs("endpoints", colonyInfo.Endpoints).
		Msg("Successfully retrieved colony information from discovery")

	// Transition from waiting_discovery to unregistered state.
	if cm.GetState() == StateWaitingDiscovery {
		cm.setState(StateUnregistered)
	}

	return colonyInfo, nil
}

// GetColonyInfo safely returns the current colony info.
func (cm *ConnectionManager) GetColonyInfo() *discoveryclient.LookupColonyResponse {
	cm.colonyInfoMu.RLock()
	defer cm.colonyInfoMu.RUnlock()
	return cm.colonyInfo
}

// GetLastSuccessfulEndpoint returns the last WireGuard endpoint that successfully connected.
func (cm *ConnectionManager) GetLastSuccessfulEndpoint() string {
	cm.stateMu.RLock()
	defer cm.stateMu.RUnlock()
	return cm.lastSuccessfulEndpoint
}

// SetLastSuccessfulEndpoint updates the last successful WireGuard endpoint.
func (cm *ConnectionManager) SetLastSuccessfulEndpoint(endpoint string) {
	cm.stateMu.Lock()
	defer cm.stateMu.Unlock()
	cm.lastSuccessfulEndpoint = endpoint
	cm.logger.Info().
		Str("endpoint", endpoint).
		Msg("Updated last successful WireGuard endpoint")
}

// SetCurrentEndpoint updates the currently configured WireGuard endpoint.
func (cm *ConnectionManager) SetCurrentEndpoint(endpoint string) {
	cm.stateMu.Lock()
	defer cm.stateMu.Unlock()
	cm.currentEndpoint = endpoint
}

// GetCurrentEndpoint returns the currently configured WireGuard endpoint.
func (cm *ConnectionManager) GetCurrentEndpoint() string {
	cm.stateMu.RLock()
	defer cm.stateMu.RUnlock()
	return cm.currentEndpoint
}

// GetLastSuccessfulRegURL returns the last HTTP registration URL that succeeded.
func (cm *ConnectionManager) GetLastSuccessfulRegURL() string {
	cm.stateMu.RLock()
	defer cm.stateMu.RUnlock()
	return cm.lastSuccessfulRegURL
}

// SetLastSuccessfulRegURL updates the last successful HTTP registration URL.
func (cm *ConnectionManager) SetLastSuccessfulRegURL(url string) {
	cm.stateMu.Lock()
	defer cm.stateMu.Unlock()
	cm.lastSuccessfulRegURL = url
	cm.logger.Info().
		Str("registration_url", url).
		Msg("Updated last successful registration URL")
}

// AttemptRegistration attempts to register with the colony.
// Returns the assigned IP and subnet on success, or an error on failure.
// Returns an error if colony info is not available (discovery hasn't succeeded yet).
func (cm *ConnectionManager) AttemptRegistration() (string, string, error) {
	// Check if we have colony info.
	colonyInfo := cm.GetColonyInfo()
	if colonyInfo == nil {
		return "", "", fmt.Errorf("colony information not available - discovery service not reached")
	}

	cm.setState(StateRegistering)

	cm.logger.Info().
		Str("agent_id", cm.agentID).
		Int("service_count", len(cm.serviceSpecs)).
		Msg("Attempting registration with colony")

	// Get preferred registration URL from last successful attempt
	preferredURL := cm.GetLastSuccessfulRegURL()

	// Get runtime context from runtime service (RFD 018).
	var runtimeContext = cm.getRuntimeContext()

	result, successfulURL, err := registerWithColony(
		cm.config,
		cm.agentID,
		cm.serviceSpecs,
		cm.agentPubKey,
		colonyInfo,
		runtimeContext,
		preferredURL,
		cm.logger,
	)

	if err != nil {
		cm.setState(StateUnregistered)
		return "", "", err
	}

	// Record successful registration URL for future reconnections
	if successfulURL != "" {
		cm.SetLastSuccessfulRegURL(successfulURL)
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
// This loop will wait if colony info is not available and start heartbeats once available.
func (cm *ConnectionManager) StartHeartbeatLoop(ctx context.Context, interval time.Duration) {
	cm.logger.Info().
		Dur("interval", interval).
		Msg("Starting heartbeat loop")

	sendHeartbeat := func() bool {
		// Check if we have colony info.
		colonyInfo := cm.GetColonyInfo()
		if colonyInfo == nil {
			// Can't send heartbeat without colony info - silently skip.
			return false
		}

		connectPort := colonyInfo.ConnectPort
		if connectPort == 0 {
			connectPort = constants.DefaultColonyPort
		}

		colonyURL := fmt.Sprintf("http://%s", net.JoinHostPort(colonyInfo.MeshIPv4, fmt.Sprintf("%d",
			connectPort)))
		client := meshv1connect.NewMeshServiceClient(http.DefaultClient, colonyURL)

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

		// Success - reset failure counter and record successful endpoint.
		cm.consecutiveFailures = 0
		cm.lastSuccessfulHeartbeat = time.Now()
		cm.setState(StateHealthy)
		cm.backoff.Reset()

		// Record the current endpoint as successful since heartbeats are working.
		// This means the WireGuard tunnel is established and functional.
		currentEndpoint := cm.GetCurrentEndpoint()
		if currentEndpoint != "" && currentEndpoint != cm.GetLastSuccessfulEndpoint() {
			cm.SetLastSuccessfulEndpoint(currentEndpoint)
		}

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

// StartDiscoveryLoop runs a background loop that attempts discovery when in waiting_discovery state.
func (cm *ConnectionManager) StartDiscoveryLoop(
	ctx context.Context,
	onDiscoverySuccess func(*discoveryclient.LookupColonyResponse),
) {
	cm.logger.Info().Msg("Starting discovery loop")

	for {
		select {
		case <-ctx.Done():
			cm.logger.Info().Msg("Discovery loop stopping")
			return
		case <-cm.discoveryTrigger:
			// Triggered discovery - attempt immediately.
			cm.attemptDiscovery(ctx, onDiscoverySuccess)
		case <-time.After(5 * time.Second):
			// Periodic check - only query discovery if waiting for it.
			if cm.GetState() == StateWaitingDiscovery {
				cm.attemptDiscovery(ctx, onDiscoverySuccess)
			}
		}
	}
}

// attemptDiscovery performs a discovery attempt with exponential backoff.
func (cm *ConnectionManager) attemptDiscovery(
	ctx context.Context,
	onSuccess func(*discoveryclient.LookupColonyResponse),
) {
	state := cm.GetState()
	if state != StateWaitingDiscovery {
		// Already have discovery info, skip.
		return
	}

	cm.logger.Info().
		Str("colony_id", cm.config.ColonyID).
		Msg("Attempting to query discovery service")

	colonyInfo, err := cm.AttemptDiscovery()
	if err != nil {
		delay := cm.discoveryBackoff.NextDelay()
		cm.logger.Warn().
			Err(err).
			Dur("retry_in", delay).
			Msg("Discovery attempt failed - will retry")

		// Wait with backoff before next attempt.
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
			// Continue to next attempt.
		}
		return
	}

	// Discovery successful - reset backoff and call success callback.
	cm.discoveryBackoff.Reset()
	cm.logger.Info().
		Str("colony_pubkey", colonyInfo.Pubkey).
		Msg("Successfully discovered colony")

	// Call success callback to handle post-discovery setup (e.g., WireGuard configuration).
	if onSuccess != nil {
		onSuccess(colonyInfo)
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

	// Registration successful - configure mesh network.
	// IMPORTANT: Must call ConfigureAgentMesh to:
	// - Assign mesh IP to wg0 interface
	// - Add colony as WireGuard peer
	// - Bring wg0 interface UP
	// - Establish WireGuard tunnel
	if err := cm.configureMesh(ip, subnet); err != nil {
		cm.logger.Warn().
			Err(err).
			Msg("Failed to configure mesh after reconnection - will retry")
		cm.setState(StateUnregistered)
		return
	}

	cm.logger.Info().
		Str("assigned_ip", ip).
		Msg("Successfully reconnected to colony and configured mesh")
}

/*
// updateMeshIP updates the WireGuard interface IP if it has changed.
// nolint:unused // Used in linux platform.
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
*/

// configureMesh configures the mesh network after registration or reconnection.
// This method handles the complete mesh setup including:
// - Assigning mesh IP to wg0 interface
// - Adding colony as WireGuard peer
// - Bringing wg0 interface UP
// - Starting mesh server on mesh IP.
func (cm *ConnectionManager) configureMesh(meshIPStr, meshSubnetStr string) error {
	// Get colony info.
	colonyInfo := cm.GetColonyInfo()
	if colonyInfo == nil {
		return fmt.Errorf("colony information not available")
	}

	// Get colony endpoint for WireGuard peer.
	colonyEndpoint := cm.GetColonyEndpoint()
	if colonyEndpoint == "" {
		return fmt.Errorf("no colony endpoint available for mesh configuration")
	}

	// Parse IP and subnet.
	parsedMeshIP := net.ParseIP(meshIPStr)
	if parsedMeshIP == nil {
		return fmt.Errorf("invalid mesh IP from colony: %s", meshIPStr)
	}

	_, parsedMeshSubnet, err := net.ParseCIDR(meshSubnetStr)
	if err != nil {
		return fmt.Errorf("invalid mesh subnet from colony: %w", err)
	}

	cm.logger.Info().
		Str("mesh_ip", meshIPStr).
		Str("subnet", meshSubnetStr).
		Str("colony_endpoint", colonyEndpoint).
		Msg("Configuring agent mesh network")

	// Call ConfigureAgentMesh to set up the complete mesh network.
	if err := ConfigureAgentMesh(cm.wgDevice, parsedMeshIP, parsedMeshSubnet, colonyInfo, colonyEndpoint, cm.logger); err != nil {
		return fmt.Errorf("failed to configure agent mesh: %w", err)
	}

	cm.logger.Info().
		Str("mesh_ip", meshIPStr).
		Msg("Agent mesh configured successfully - tunnel ready")

	// Update assigned IP tracking.
	cm.assignedIP = meshIPStr
	cm.assignedSubnet = meshSubnetStr

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

// getRuntimeContext returns the cached runtime context from the runtime service.
// Returns nil if runtime service is not available or context is not yet detected.
func (cm *ConnectionManager) getRuntimeContext() *agentv1.RuntimeContextResponse {
	if cm.runtimeService == nil {
		cm.logger.Debug().Msg("Runtime service not available - registration will proceed without runtime context")
		return nil
	}

	ctx := cm.runtimeService.GetCachedContext()
	if ctx == nil {
		cm.logger.Debug().Msg("Runtime context not yet detected - registration will proceed without it")
		return nil
	}

	cm.logger.Debug().
		Str("runtime_type", ctx.RuntimeType.String()).
		Str("sidecar_mode", ctx.SidecarMode.String()).
		Msg("Including runtime context in registration")
	return ctx
}

// GetColonyEndpoint returns the best colony endpoint for Wire Guard peer configuration.
// Returns empty string if colony info is not available.
func (cm *ConnectionManager) GetColonyEndpoint() string {
	colonyInfo := cm.GetColonyInfo()
	if colonyInfo == nil {
		return ""
	}

	// Get last successful endpoint for prioritization.
	lastSuccessful := cm.GetLastSuccessfulEndpoint()

	// Try observed endpoints first (NAT traversal).
	// These take highest priority as they're discovered via STUN for NAT traversal.
	for _, observedEp := range colonyInfo.ObservedEndpoints {
		if observedEp.IP == "" {
			continue
		}

		// Skip invalid endpoints (port 0 means STUN failed or returned invalid data).
		if observedEp.Port == 0 {
			cm.logger.Debug().
				Str("ip", observedEp.IP).
				Msg("Skipping observed endpoint with port 0 (invalid STUN result)")
			continue
		}

		ip := net.ParseIP(observedEp.IP)
		// Skip IPv6 and loopback.
		if ip != nil && (ip.To4() == nil || ip.IsLoopback()) {
			continue
		}

		endpoint := net.JoinHostPort(observedEp.IP, fmt.Sprintf("%d", observedEp.Port))
		cm.SetCurrentEndpoint(endpoint)
		cm.logger.Debug().
			Str("endpoint", endpoint).
			Msg("Selected observed endpoint for WireGuard connection")
		return endpoint
	}

	// Helper function to determine WireGuard port.
	getWireGuardPort := func() uint32 {
		wgPort := uint32(51820) // Default
		if len(colonyInfo.ObservedEndpoints) > 0 && colonyInfo.ObservedEndpoints[0].Port > 0 {
			wgPort = colonyInfo.ObservedEndpoints[0].Port
		} else if colonyInfo.Metadata != nil {
			if portStr, ok := colonyInfo.Metadata["wireguard_port"]; ok && portStr != "" {
				_, _ = fmt.Sscanf(portStr, "%d", &wgPort)
			}
		}
		return wgPort
	}

	wgPort := getWireGuardPort()

	// Fall back to regular endpoints.
	// Strategy: Try last successful endpoint first if available, then try remaining endpoints.
	// This provides automatic failover while remembering what worked before.
	//
	// IMPORTANT: If we registered via localhost, prefer localhost for WireGuard too.
	// This ensures consistency - same-host deployments use localhost for both HTTP and WireGuard.
	preferLocalhost := false
	if cm.lastSuccessfulRegURL != "" {
		if host, _, err := net.SplitHostPort(strings.TrimPrefix(strings.TrimPrefix(cm.lastSuccessfulRegURL, "http://"), "https://")); err == nil {
			if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
				preferLocalhost = true
				cm.logger.Debug().
					Str("registration_url", cm.lastSuccessfulRegURL).
					Msg("Registered via localhost - will prefer localhost for WireGuard peer")
			}
		}
	}

	// First pass: Try last successful endpoint if it's still in the list.
	// Note: We honor last successful even if it's localhost (proven to work before).
	if lastSuccessful != "" {
		for _, ep := range colonyInfo.Endpoints {
			if ep == "" {
				continue
			}

			host, _, err := net.SplitHostPort(ep)
			if err != nil || host == "" {
				continue
			}

			endpoint := net.JoinHostPort(host, fmt.Sprintf("%d", wgPort))
			if endpoint == lastSuccessful {
				cm.SetCurrentEndpoint(endpoint)

				// Log whether we're reusing localhost or non-localhost.
				ip := net.ParseIP(host)
				if ip != nil && ip.IsLoopback() {
					cm.logger.Info().
						Str("endpoint", endpoint).
						Msg("Reusing last successful localhost endpoint (same-host deployment)")
				} else {
					cm.logger.Info().
						Str("endpoint", endpoint).
						Msg("Reusing last successful WireGuard endpoint")
				}
				return endpoint
			}
		}
	}

	// Second pass: If we prefer localhost (registered via localhost), try localhost endpoints.
	// This ensures consistency for same-host deployments.
	if preferLocalhost {
		for _, ep := range colonyInfo.Endpoints {
			if ep == "" {
				continue
			}

			host, _, err := net.SplitHostPort(ep)
			if err != nil || host == "" {
				continue
			}

			endpoint := net.JoinHostPort(host, fmt.Sprintf("%d", wgPort))

			// Skip if already tried.
			if endpoint == lastSuccessful {
				continue
			}

			// Only consider localhost in this pass.
			ip := net.ParseIP(host)
			if ip != nil && ip.IsLoopback() {
				cm.SetCurrentEndpoint(endpoint)
				cm.logger.Info().
					Str("endpoint", endpoint).
					Msg("Selected localhost endpoint (same-host deployment, registered via localhost)")
				return endpoint
			}
		}
	}

	// Third pass: Try non-localhost endpoints.
	for _, ep := range colonyInfo.Endpoints {
		if ep == "" {
			continue
		}

		host, _, err := net.SplitHostPort(ep)
		if err != nil || host == "" {
			continue
		}

		endpoint := net.JoinHostPort(host, fmt.Sprintf("%d", wgPort))

		// Skip if this was the last successful (already tried above).
		if endpoint == lastSuccessful {
			continue
		}

		// Skip localhost in third pass - will try as last resort in fourth pass.
		ip := net.ParseIP(host)
		if ip != nil && ip.IsLoopback() {
			continue
		}

		cm.SetCurrentEndpoint(endpoint)
		cm.logger.Info().
			Str("endpoint", endpoint).
			Msg("Selected new WireGuard endpoint")
		return endpoint
	}

	// Fourth pass: Try localhost as last resort (fallback for same-host deployment).
	for _, ep := range colonyInfo.Endpoints {
		if ep == "" {
			continue
		}

		host, _, err := net.SplitHostPort(ep)
		if err != nil || host == "" {
			continue
		}

		endpoint := net.JoinHostPort(host, fmt.Sprintf("%d", wgPort))

		// Skip if already tried.
		if endpoint == lastSuccessful {
			continue
		}

		// Only consider localhost in this pass.
		ip := net.ParseIP(host)
		if ip != nil && ip.IsLoopback() {
			cm.SetCurrentEndpoint(endpoint)
			cm.logger.Info().
				Str("endpoint", endpoint).
				Msg("Selected localhost endpoint (same-host deployment)")
			return endpoint
		}
	}

	return ""
}
