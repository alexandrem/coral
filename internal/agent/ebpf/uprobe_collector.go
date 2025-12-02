package ebpf

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/timestamppb"

	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go uprobe ./bpf/uprobe.c -- -I./bpf/headers

// uprobeEvent matches the C struct uprobe_event in bpf/uprobe.c
type uprobeEvent struct {
	TimestampNs uint64
	Pid         uint32
	Tid         uint32
	EventType   uint8
	_           [3]byte // padding
	DurationNs  uint64
}

// UprobeCollector implements the Collector interface for uprobe-based debugging.
type UprobeCollector struct {
	logger       zerolog.Logger
	config       *UprobeConfig
	functionName string

	// SDK metadata
	sdkClient  *SDKClient
	funcOffset uint64
	binaryPath string
	pid        uint32

	// eBPF resources
	objs       *uprobeObjects
	entryLink  link.Link
	returnLink link.Link
	reader     *ringbuf.Reader

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
	CaptureArgs   bool
	CaptureReturn bool
	SampleRate    uint32
	MaxEvents     uint32
	Duration      time.Duration
}

// NewUprobeCollector creates a new uprobe collector.
func NewUprobeCollector(logger zerolog.Logger, config *UprobeConfig) (*UprobeCollector, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	if config.FunctionName == "" {
		return nil, fmt.Errorf("function name is required")
	}

	if config.SDKAddr == "" {
		return nil, fmt.Errorf("SDK address is required")
	}

	sdkClient := NewSDKClient(logger, config.SDKAddr)

	return &UprobeCollector{
		logger:       logger.With().Str("collector", "uprobe").Str("function", config.FunctionName).Logger(),
		config:       config,
		functionName: config.FunctionName,
		sdkClient:    sdkClient,
		events:       make([]*meshv1.UprobeEvent, 0),
	}, nil
}

// Start begins collecting uprobe events.
func (c *UprobeCollector) Start(ctx context.Context) error {
	c.ctx, c.cancel = context.WithCancel(ctx)

	// Step 1: Query SDK for function metadata
	c.logger.Info().Msg("Querying SDK for function metadata")
	metadata, err := c.sdkClient.GetFunctionMetadata(ctx, c.functionName)
	if err != nil {
		return fmt.Errorf("failed to get function metadata: %w", err)
	}

	c.funcOffset = metadata.Offset
	c.binaryPath = metadata.BinaryPath
	c.pid = metadata.Pid

	c.logger.Info().
		Str("binary", c.binaryPath).
		Uint64("offset", c.funcOffset).
		Uint32("pid", c.pid).
		Msg("Got function metadata from SDK")

	// Step 2: Load eBPF program
	c.objs = &uprobeObjects{}
	if err := loadUprobeObjects(c.objs, nil); err != nil {
		return fmt.Errorf("failed to load eBPF objects: %w", err)
	}

	c.logger.Debug().Msg("Loaded eBPF objects")

	// Step 3: Open executable for uprobe attachment
	exe, err := link.OpenExecutable(c.binaryPath)
	if err != nil {
		c.objs.Close() // nolint:errcheck
		return fmt.Errorf("failed to open executable: %w", err)
	}

	// Step 4: Attach uprobe (function entry)
	c.entryLink, err = exe.Uprobe("", c.objs.UprobeEntry, &link.UprobeOptions{
		Offset: c.funcOffset,
		PID:    int(c.pid),
	})
	if err != nil {
		c.objs.Close() // nolint:errcheck
		return fmt.Errorf("failed to attach uprobe: %w", err)
	}

	c.logger.Debug().Msg("Attached uprobe to function entry")

	// Step 5: Attach uretprobe (function return)
	c.returnLink, err = exe.Uretprobe("", c.objs.UprobeReturn, &link.UprobeOptions{
		Offset: c.funcOffset,
		PID:    int(c.pid),
	})
	if err != nil {
		c.entryLink.Close() // nolint:errcheck
		c.objs.Close()      // nolint:errcheck
		return fmt.Errorf("failed to attach uretprobe: %w", err)
	}

	c.logger.Debug().Msg("Attached uretprobe to function return")

	// Step 6: Start reading events from ring buffer
	c.reader, err = ringbuf.NewReader(c.objs.Events)
	if err != nil {
		c.returnLink.Close() // nolint:errcheck
		c.entryLink.Close()  // nolint:errcheck
		c.objs.Close()       // nolint:errcheck
		return fmt.Errorf("failed to create ringbuf reader: %w", err)
	}

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

	if c.reader != nil {
		if err := c.reader.Close(); err != nil {
			c.logger.Error().Err(err).Msg("Error closing ring buffer reader")
		}
	}

	if c.returnLink != nil {
		if err := c.returnLink.Close(); err != nil {
			c.logger.Error().Err(err).Msg("Error closing uretprobe link")
		}
	}

	if c.entryLink != nil {
		if err := c.entryLink.Close(); err != nil {
			c.logger.Error().Err(err).Msg("Error closing uprobe link")
		}
	}

	if c.objs != nil {
		if err := c.objs.Close(); err != nil {
			c.logger.Error().Err(err).Msg("Error closing eBPF objects")
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

	// Clear buffer
	c.events = make([]*meshv1.UprobeEvent, 0)

	return events, nil
}

// readEvents reads events from the ring buffer in a goroutine.
func (c *UprobeCollector) readEvents() {
	c.logger.Info().Msg("Started event reader goroutine")

	for {
		select {
		case <-c.ctx.Done():
			c.logger.Info().Msg("Event reader stopped")
			return
		default:
		}

		record, err := c.reader.Read()
		if err != nil {
			if errors.Is(err, ringbuf.ErrClosed) {
				return
			}
			c.logger.Error().Err(err).Msg("Failed to read event from ring buffer")
			continue
		}

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
			Pid:          int32(rawEvent.Pid),
			Tid:          int32(rawEvent.Tid),
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
