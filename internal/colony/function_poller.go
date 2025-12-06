package colony

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/zerolog"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/colony/registry"
)

// FunctionPoller periodically polls agents for function metadata.
// It implements change detection to avoid unnecessary database updates.
type FunctionPoller struct {
	registry         *registry.Registry
	functionRegistry *FunctionRegistry
	logger           zerolog.Logger

	// Track last known hash for each service to detect changes.
	mu            sync.RWMutex
	serviceHashes map[string]string // service_name -> hash of function list

	// Polling configuration.
	pollInterval time.Duration
	ctx          context.Context
	cancel       context.CancelFunc
}

// FunctionPollerConfig contains configuration for the function poller.
type FunctionPollerConfig struct {
	Registry         *registry.Registry
	FunctionRegistry *FunctionRegistry
	PollInterval     time.Duration
	Logger           zerolog.Logger
}

// NewFunctionPoller creates a new function poller.
func NewFunctionPoller(config FunctionPollerConfig) *FunctionPoller {
	if config.PollInterval == 0 {
		config.PollInterval = 5 * time.Minute // Default: 5 minutes
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &FunctionPoller{
		registry:         config.Registry,
		functionRegistry: config.FunctionRegistry,
		logger:           config.Logger.With().Str("component", "function_poller").Logger(),
		serviceHashes:    make(map[string]string),
		pollInterval:     config.PollInterval,
		ctx:              ctx,
		cancel:           cancel,
	}
}

// Start begins the periodic polling loop.
func (p *FunctionPoller) Start() {
	p.logger.Info().
		Dur("interval", p.pollInterval).
		Msg("Starting function discovery poller")

	go p.pollLoop()
}

// Stop stops the polling loop.
func (p *FunctionPoller) Stop() {
	p.logger.Info().Msg("Stopping function discovery poller")
	p.cancel()
}

// pollLoop runs the periodic polling logic.
func (p *FunctionPoller) pollLoop() {
	// Perform initial poll immediately.
	p.pollAllAgents()

	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.pollAllAgents()
		}
	}
}

// pollAllAgents polls all registered agents for function metadata.
func (p *FunctionPoller) pollAllAgents() {
	agents := p.registry.ListAll()

	p.logger.Debug().
		Int("agent_count", len(agents)).
		Msg("Polling agents for function metadata")

	// Track statistics.
	var (
		totalPolled  int
		totalUpdated int
		totalSkipped int
		totalErrors  int
	)

	// Poll each agent.
	for _, agent := range agents {
		// Skip agents without services.
		if len(agent.Services) == 0 {
			continue
		}

		// Skip unhealthy agents.
		if time.Since(agent.LastSeen) > 5*time.Minute {
			p.logger.Debug().
				Str("agent_id", agent.AgentID).
				Msg("Skipping unhealthy agent")
			continue
		}

		// Poll each service on this agent.
		for _, service := range agent.Services {
			totalPolled++

			if err := p.pollService(agent, service.Name); err != nil {
				p.logger.Warn().
					Err(err).
					Str("agent_id", agent.AgentID).
					Str("service", service.Name).
					Msg("Failed to poll service for functions")
				totalErrors++
			} else {
				// Check if we updated or skipped.
				// This is tracked inside pollService via the hash check.
				totalUpdated++
			}
		}
	}

	p.logger.Info().
		Int("polled", totalPolled).
		Int("updated", totalUpdated).
		Int("skipped", totalSkipped).
		Int("errors", totalErrors).
		Msg("Function discovery poll completed")
}

// pollService polls a specific service for function metadata.
func (p *FunctionPoller) pollService(agent *registry.Entry, serviceName string) error {
	// Find the service in the agent's service list to get binary_hash.
	var binaryHash string
	for _, svc := range agent.Services {
		if svc.Name == serviceName {
			binaryHash = svc.BinaryHash
			break
		}
	}

	// If no binary hash is available, compute one from the function list as fallback.
	// This ensures backward compatibility with agents that don't report binary_hash.
	useFallbackHash := binaryHash == ""

	// Create agent client using helper utility.
	client := GetAgentClient(agent)

	// Call GetFunctions RPC.
	ctx, cancel := context.WithTimeout(p.ctx, 30*time.Second)
	defer cancel()

	resp, err := client.GetFunctions(ctx, connect.NewRequest(&agentv1.GetFunctionsRequest{
		ServiceName: serviceName,
	}))
	if err != nil {
		return fmt.Errorf("GetFunctions RPC failed: %w", err)
	}

	functions := resp.Msg.Functions
	if len(functions) == 0 {
		p.logger.Debug().
			Str("service", serviceName).
			Msg("No functions discovered")
		return nil
	}

	// If using fallback, compute hash from function list.
	if useFallbackHash {
		binaryHash = computeFunctionListHash(functions)
		p.logger.Debug().
			Str("service", serviceName).
			Msg("Using computed function list hash (binary_hash not available from agent)")
	}

	// Compute hash of function list to detect changes (for change detection only).
	currentHash := computeFunctionListHash(functions)

	// Check if functions have changed.
	p.mu.RLock()
	lastHash, exists := p.serviceHashes[serviceName]
	p.mu.RUnlock()

	if exists && lastHash == currentHash {
		p.logger.Debug().
			Str("service", serviceName).
			Int("function_count", len(functions)).
			Msg("Functions unchanged, skipping update")
		return nil
	}

	// Functions have changed - store them with the binary hash.
	if err := p.functionRegistry.StoreFunctions(ctx, agent.AgentID, serviceName, binaryHash, functions); err != nil {
		return fmt.Errorf("failed to store functions: %w", err)
	}

	// Update hash.
	p.mu.Lock()
	p.serviceHashes[serviceName] = currentHash
	p.mu.Unlock()

	p.logger.Info().
		Str("agent_id", agent.AgentID).
		Str("service", serviceName).
		Str("binary_hash", binaryHash).
		Int("function_count", len(functions)).
		Bool("first_discovery", !exists).
		Bool("using_fallback_hash", useFallbackHash).
		Msg("Stored functions in registry")

	return nil
}

// computeFunctionListHash computes a SHA256 hash of a function list.
// This is used for change detection - if the hash hasn't changed, we skip the update.
func computeFunctionListHash(functions []*agentv1.FunctionInfo) string {
	// Sort functions by name for deterministic hashing.
	sorted := make([]*agentv1.FunctionInfo, len(functions))
	copy(sorted, functions)

	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})

	// Build a string representation of all function metadata.
	var builder strings.Builder
	for _, fn := range sorted {
		builder.WriteString(fn.Name)
		builder.WriteString("|")
		builder.WriteString(fn.Package)
		builder.WriteString("|")
		builder.WriteString(fn.FilePath)
		builder.WriteString("|")
		builder.WriteString(fmt.Sprintf("%d|%d|%t\n", fn.LineNumber, fn.Offset, fn.HasDwarf))
	}

	// Compute SHA256 hash.
	hash := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(hash[:])
}
