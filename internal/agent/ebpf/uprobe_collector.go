//go:build linux

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

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/agent/ebpf/bpfgen"
	"github.com/coral-mesh/coral/internal/agent/ebpf/disasm"
	"github.com/coral-mesh/coral/internal/agent/ebpf/uprobe"
)

// uprobeEvent matches the C struct uprobe_event in bpf/uprobe.c
type uprobeEvent struct {
	TimestampNs uint64
	Pid         uint32
	Tid         uint32
	EventType   uint8
	_           [7]byte // padding
	DurationNs  uint64
}

// uprobeFilterConfig matches the C struct filter_config in bpf/uprobe.c (RFD 090).
// Field layout must be kept in sync with the C definition.
type uprobeFilterConfig struct {
	MinDurationNs uint64
	MaxDurationNs uint64
	SampleRate    uint32
	_             [4]byte // padding to 8-byte alignment
}

// UprobeCollector implements the Collector interface for uprobe-based debugging.
type UprobeCollector struct {
	logger       zerolog.Logger
	config       *UprobeConfig
	functionName string

	// Function discovery
	discoveryService *DiscoveryService
	funcOffset       uint64
	funcSizeBytes    uint64
	hasFuncSize      bool
	binaryPath       string
	pid              uint32

	// eBPF resources
	objs         *bpfgen.Objects
	attachResult *uprobe.AttachResult
	reader       *ringbuf.Reader

	// Event collection
	ctx    context.Context
	cancel context.CancelFunc
	events []*agentv1.UprobeEvent
	mu     sync.Mutex
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
		events:           make([]*agentv1.UprobeEvent, 0),
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
	c.funcSizeBytes = result.Metadata.SizeBytes
	c.hasFuncSize = result.Metadata.HasSize

	c.logger.Info().
		Str("binary", c.binaryPath).
		Uint64("offset", c.funcOffset).
		Uint32("pid", c.pid).
		Uint64("size_bytes", c.funcSizeBytes).
		Bool("has_size", c.hasFuncSize).
		Str("discovery_method", string(result.Method)).
		Msg("Successfully discovered function metadata")

	// Step 2: Load eBPF program
	c.objs = &bpfgen.Objects{}
	if err := bpfgen.LoadObjects(c.objs, nil); err != nil {
		return fmt.Errorf("failed to load eBPF objects: %w", err)
	}

	c.logger.Debug().Msg("Loaded eBPF objects")

	// Step 2b: Write initial filter config if one was specified (RFD 090).
	// The filter_config_map is optional; if the compiled .o lacks it the field is nil.
	if err := c.writeFilterConfig(c.config.Filter); err != nil {
		c.objs.Close() //nolint:errcheck
		return fmt.Errorf("failed to write initial filter config: %w", err)
	}

	// Step 3: Attach uprobe to function entry.
	attachCfg := uprobe.AttachConfig{
		PID:          c.pid,
		Offset:       c.funcOffset,
		BinaryPath:   c.binaryPath,
		AttachReturn: false, // Uretprobes disabled for Go; we use RET-instruction uprobes instead.
		PIDFilter:    0,     // Trace all processes using this binary (inode). Avoids PID namespace issues.
		Logger:       c.logger,
	}

	c.attachResult, err = uprobe.AttachUprobe(
		attachCfg,
		c.objs.UprobeEntry,
		nil, // No uretprobe (incompatible with Go)
		c.objs.Events,
	)
	if err != nil {
		c.objs.Close() //nolint:errcheck
		return fmt.Errorf("failed to attach uprobe: %w", err)
	}

	// Step 4: Attach return probes to RET instructions (RFD 073).
	// Disassemble function to find all RET instruction offsets, then attach
	// uprobes to each one. Falls back to entry-only if disassembly fails.
	if c.hasFuncSize && c.funcSizeBytes > 0 {
		resolvedPath := fmt.Sprintf("/proc/%d/exe", c.pid)
		d := disasm.NewX86Disassembler()
		retOffsets, disasmErr := d.FindRETOffsets(resolvedPath, c.funcOffset, c.funcSizeBytes)
		if disasmErr != nil {
			c.logger.Warn().Err(disasmErr).
				Msg("Could not disassemble function, duration metrics unavailable")
		} else if len(retOffsets) == 0 {
			c.logger.Warn().
				Msg("No RET instructions found (tail-call optimized?), duration metrics unavailable")
		} else {
			c.logger.Info().
				Int("ret_count", len(retOffsets)).
				Msg("Found RET instructions, attaching return probes")

			returnLinks, retErr := uprobe.AttachReturnProbes(attachCfg, c.objs.UprobeReturn, retOffsets)
			if retErr != nil {
				c.logger.Warn().Err(retErr).
					Msg("Failed to attach return probes, continuing with entry-only")
			} else {
				c.attachResult.ReturnLinks = returnLinks
				c.logger.Info().
					Int("return_probes", len(returnLinks)).
					Msg("Successfully attached return probes to RET instructions")
			}
		}
	} else {
		c.logger.Info().
			Msg("Function size not available, entry-only probe (no duration metrics)")
	}

	// Store reader reference for easy access.
	c.reader = c.attachResult.Reader

	go c.readEvents()

	// Start periodic cleanup of orphaned BPF map entries (RFD 073).
	// Entries older than 60s are removed (caused by panics, SIGKILL, etc.).
	if len(c.attachResult.ReturnLinks) > 0 {
		go c.cleanupOrphanedEntries()
	}

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
		event := &agentv1.UprobeEvent{
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

// cleanupOrphanedEntries periodically removes stale entries from the BPF entry_times map.
// Orphaned entries occur when functions panic, are killed, or enter infinite loops (RFD 073).
func (c *UprobeCollector) cleanupOrphanedEntries() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.doCleanup()
		}
	}
}

// doCleanup iterates the entry_times map and removes entries older than 60s.
func (c *UprobeCollector) doCleanup() {
	if c.objs == nil || c.objs.EntryTimes == nil {
		return
	}

	const maxAgeNs = 60_000_000_000 // 60 seconds
	now := uint64(time.Now().UnixNano())

	var key bpfgen.UprobeEntryKey
	var val bpfgen.UprobeEntryValue
	var keysToDelete []bpfgen.UprobeEntryKey

	iter := c.objs.EntryTimes.Iterate()
	for iter.Next(&key, &val) {
		if now-val.CreatedAt > maxAgeNs {
			keysToDelete = append(keysToDelete, key)
		}
	}

	for _, k := range keysToDelete {
		if err := c.objs.EntryTimes.Delete(k); err != nil {
			c.logger.Debug().Err(err).Msg("Failed to delete orphaned entry")
		}
	}

	if len(keysToDelete) > 0 {
		c.logger.Info().
			Int("cleaned", len(keysToDelete)).
			Msg("Cleaned orphaned entry_times entries")
	}
}

// UpdateFilter updates the kernel-level event filter for an active collector session
// without detaching or interrupting event collection (RFD 090).
// Returns nil when filter maps are unavailable (old compiled .o without filter support).
func (c *UprobeCollector) UpdateFilter(f UprobeFilter) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writeFilterConfig(f)
}

// writeFilterConfig writes f into the filter_config BPF map.
// Must be called with c.mu held (or before the reader goroutine is started).
func (c *UprobeCollector) writeFilterConfig(f UprobeFilter) error {
	if c.objs == nil || c.objs.FilterConfigMap == nil {
		// Filter maps absent (old compiled .o); silently skip.
		return nil
	}

	cfg := uprobeFilterConfig{
		MinDurationNs: f.MinDurationNs,
		MaxDurationNs: f.MaxDurationNs,
		SampleRate:    f.SampleRate,
	}

	const filterKey uint32 = 0
	if err := c.objs.FilterConfigMap.Put(filterKey, cfg); err != nil {
		return fmt.Errorf("update filter_config_map: %w", err)
	}

	c.logger.Debug().
		Uint64("min_duration_ns", f.MinDurationNs).
		Uint64("max_duration_ns", f.MaxDurationNs).
		Uint32("sample_rate", f.SampleRate).
		Msg("Updated eBPF filter config")

	return nil
}

// eventTypeString converts event type byte to string.
func eventTypeString(eventType uint8) string {
	if eventType == 0 {
		return "entry"
	}
	return "return"
}
