package ebpf

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/cilium/ebpf/ringbuf"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/timestamppb"

	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/agent/ebpf/uprobe"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go uprobe ./bpf/uprobe.c -- -I./bpf/headers

// uprobeEvent matches the C struct uprobe_event in bpf/uprobe.c
type uprobeEvent struct {
	TimestampNs uint64
	Pid         uint32
	Tid         uint32
	EventType   uint8
	_           [7]byte // padding
	DurationNs  uint64
}

// UprobeCollector implements the Collector interface for uprobe-based debugging.
type UprobeCollector struct {
	logger       zerolog.Logger
	config       *UprobeConfig
	functionName string

	// Function discovery
	discoveryService *DiscoveryService
	funcOffset       uint64
	binaryPath       string
	pid              uint32

	// eBPF resources
	objs         *uprobeObjects
	attachResult *uprobe.AttachResult
	reader       *ringbuf.Reader

	// Event collection
	ctx    context.Context
	cancel context.CancelFunc
	events []*meshv1.UprobeEvent
	mu     sync.Mutex
}

// UprobeConfig contains configuration for an uprobe collector.
type UprobeConfig struct {
	ServiceName   string
	FunctionName  string
	SDKAddr       string
	PID           uint32 // Target process PID (required for agentless discovery)
	CaptureArgs   bool
	CaptureReturn bool
	SampleRate    uint32
	MaxEvents     uint32
	Duration      time.Duration

	// Discovery configuration (optional, uses defaults if nil).
	DiscoveryConfig *DiscoveryConfig
}

// NewUprobeCollector creates a new uprobe collector.
func NewUprobeCollector(logger zerolog.Logger, config *UprobeConfig) (*UprobeCollector, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	if config.FunctionName == "" {
		return nil, fmt.Errorf("function name is required")
	}

	// PID is now required for agentless discovery (if SDK is not available).
	// If SDK address is provided, PID might be discovered from SDK.
	if config.PID == 0 && config.SDKAddr == "" {
		return nil, fmt.Errorf("either PID or SDK address is required")
	}

	// Create discovery service.
	discoveryCfg := config.DiscoveryConfig
	if discoveryCfg == nil {
		// Use default config with slog logger converted from zerolog.
		slogLogger := convertZerologToSlog(logger)
		discoveryCfg = DefaultDiscoveryConfig(slogLogger)
	}

	discoveryService, err := NewDiscoveryService(discoveryCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery service: %w", err)
	}

	return &UprobeCollector{
		logger:           logger.With().Str("collector", "uprobe").Str("function", config.FunctionName).Logger(),
		config:           config,
		functionName:     config.FunctionName,
		discoveryService: discoveryService,
		events:           make([]*meshv1.UprobeEvent, 0),
	}, nil
}

// convertZerologToSlog is a temporary helper to convert zerolog to slog.
// TODO: standardize on one logging library across the codebase.
func convertZerologToSlog(zlog zerolog.Logger) *slog.Logger {
	// For now, just create a default slog logger.
	// In the future, we should create a proper adapter or standardize on one library.
	return slog.Default()
}

// Start begins collecting uprobe events.
func (c *UprobeCollector) Start(ctx context.Context) error {
	c.ctx, c.cancel = context.WithCancel(ctx)

	// Step 1: Discover function metadata using fallback chain.
	c.logger.Info().Msg("Discovering function metadata")

	// Determine PID: use config PID if set, otherwise will be discovered from SDK.
	pid := c.config.PID

	result, err := c.discoveryService.DiscoverFunction(ctx, c.config.SDKAddr, pid, c.functionName)
	if err != nil {
		return fmt.Errorf("failed to discover function metadata: %w", err)
	}

	c.funcOffset = result.Metadata.Offset
	c.binaryPath = result.Metadata.BinaryPath
	c.pid = result.Metadata.Pid

	c.logger.Info().
		Str("binary", c.binaryPath).
		Uint64("offset", c.funcOffset).
		Uint32("pid", c.pid).
		Str("discovery_method", string(result.Method)).
		Msg("Successfully discovered function metadata")

	// Step 2: Load eBPF program
	c.objs = &uprobeObjects{}
	if err := loadUprobeObjects(c.objs, nil); err != nil {
		return fmt.Errorf("failed to load eBPF objects: %w", err)
	}

	c.logger.Debug().Msg("Loaded eBPF objects")

	// Step 3: Attach uprobe using shared attacher.
	// NOTE: Uretprobes are disabled for Go programs due to runtime incompatibility.
	// Go uses a custom calling convention and stack management that conflicts with
	// uretprobe's return address manipulation, causing "unexpected return pc" crashes.
	// We only attach to function entry and won't capture return/duration information.
	// TODO: disassemble function and attach many uprobes for RET (Return-Instruction Uprobes).
	c.attachResult, err = uprobe.AttachUprobe(
		uprobe.AttachConfig{
			PID:          c.pid,
			Offset:       c.funcOffset,
			BinaryPath:   c.binaryPath,
			AttachReturn: false, // Disabled for Go programs
			PIDFilter:    0,     // Trace all processes using this binary (inode). Avoids PID namespace issues.
			Logger:       c.logger,
		},
		c.objs.UprobeEntry,
		nil, // No return probe
		c.objs.Events,
	)
	if err != nil {
		c.objs.Close() // nolint:errcheck
		return fmt.Errorf("failed to attach uprobe: %w", err)
	}

	// Store reader reference for easy access.
	c.reader = c.attachResult.Reader

	go c.readEvents()

	c.logger.Info().Msg("Uprobe collector started successfully")
	return nil
}

// Stop stops the collector and cleans up resources.
func (c *UprobeCollector) Stop() error {
	c.logger.Info().Msg("Stopping uprobe collector")

	if c.cancel != nil {
		c.cancel()
	}

	// Clean up uprobe attachment resources (links and reader).
	if c.attachResult != nil {
		if err := c.attachResult.Close(); err != nil {
			c.logger.Error().Err(err).Msg("Error closing uprobe attachment")
		}
	}

	// Clean up BPF objects (programs and maps).
	if c.objs != nil {
		if err := c.objs.Close(); err != nil {
			c.logger.Error().Err(err).Msg("Error closing eBPF objects")
		}
	}

	// Clean up discovery service.
	if c.discoveryService != nil {
		if err := c.discoveryService.Close(); err != nil {
			c.logger.Error().Err(err).Msg("Error closing discovery service")
		}
	}

	c.logger.Info().Msg("Uprobe collector stopped")
	return nil
}

// GetEvents retrieves collected events since last call.
func (c *UprobeCollector) GetEvents() ([]*meshv1.EbpfEvent, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Convert UprobeEvents to generic EbpfEvents
	events := make([]*meshv1.EbpfEvent, len(c.events))
	for i, uprobeEvent := range c.events {
		events[i] = &meshv1.EbpfEvent{
			Timestamp:   uprobeEvent.Timestamp,
			CollectorId: "uprobe-" + c.functionName,
			AgentId:     "", // Will be set by manager
			ServiceName: c.config.ServiceName,
			Payload: &meshv1.EbpfEvent_UprobeEvent{
				UprobeEvent: uprobeEvent,
			},
		}
	}

	// Don't clear buffer - keep events for historical queries (RFD 062).
	// Events are kept until collector stops or max buffer is reached.

	return events, nil
}

// readEvents reads events from the ring buffer in a goroutine.
func (c *UprobeCollector) readEvents() {
	c.logger.Info().
		Str("function", c.functionName).
		Str("service", c.config.ServiceName).
		Msg("Event reader goroutine started, waiting for events...")

	for {
		select {
		case <-c.ctx.Done():
			c.logger.Info().Msg("Event reader stopped")
			return
		default:
		}

		// Set a read deadline to avoid blocking forever
		// This allows us to periodically log that we're still waiting
		c.reader.SetDeadline(time.Now().Add(5 * time.Second))

		record, err := c.reader.Read()
		if err != nil {
			if errors.Is(err, ringbuf.ErrClosed) {
				c.logger.Info().Msg("Ring buffer closed, exiting event reader")
				return
			}
			// Check if it's a timeout error (os.ErrDeadlineExceeded)
			if errors.Is(err, os.ErrDeadlineExceeded) {
				c.logger.Debug().Msg("No events in last 5s, still waiting...")
				continue
			}
			c.logger.Error().Err(err).Msg("Failed to read event from ring buffer")
			continue
		}

		c.logger.Debug().
			Int("size", len(record.RawSample)).
			Msg("✓ Read record from ring buffer")

		// Parse event from raw bytes
		var rawEvent uprobeEvent
		if err := binary.Read(bytes.NewBuffer(record.RawSample), binary.LittleEndian, &rawEvent); err != nil {
			c.logger.Error().Err(err).Msg("Failed to parse event")
			continue
		}

		// Convert to protobuf
		event := &meshv1.UprobeEvent{
			Timestamp:    timestamppb.New(time.Unix(0, int64(rawEvent.TimestampNs))),
			FunctionName: c.functionName,
			ServiceName:  c.config.ServiceName,
			EventType:    eventTypeString(rawEvent.EventType),
			DurationNs:   rawEvent.DurationNs,
			Pid:          int32(rawEvent.Pid), //nolint:gosec // G115: PID conversion is safe
			Tid:          int32(rawEvent.Tid), //nolint:gosec // G115: TID conversion is safe
		}

		// Store event
		c.mu.Lock()
		c.events = append(c.events, event)

		// Enforce max events limit
		if c.config.MaxEvents > 0 && len(c.events) > int(c.config.MaxEvents) {
			c.events = c.events[1:] // Drop oldest
		}
		c.mu.Unlock()

		c.logger.Debug().
			Str("event_type", event.EventType).
			Uint64("duration_ns", event.DurationNs).
			Msg("Collected uprobe event")
	}
}

// eventTypeString converts event type byte to string.
func eventTypeString(eventType uint8) string {
	if eventType == 0 {
		return "entry"
	}
	return "return"
}
