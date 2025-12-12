package ebpf

import (
	"context"
	"testing"
	"time"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestManager_GetCapabilities(t *testing.T) {
	logger := zerolog.New(zerolog.NewTestWriter(t))
	manager := NewManager(Config{Logger: logger})

	caps := manager.GetCapabilities()

	if caps == nil {
		t.Fatal("capabilities should not be nil")
	}

	// On non-Linux systems, eBPF should not be supported.
	// On Linux, it might be supported depending on kernel.
	t.Logf("eBPF supported: %v", caps.Supported)
	t.Logf("Kernel version: %s", caps.KernelVersion)
	t.Logf("BTF available: %v", caps.BtfAvailable)
	t.Logf("CAP_BPF: %v", caps.CapBpf)
	t.Logf("Available collectors: %v", caps.AvailableCollectors)
}

func TestManager_StartStopCollector(t *testing.T) {
	logger := zerolog.New(zerolog.NewTestWriter(t))
	manager := NewManager(Config{Logger: logger})
	defer func() { _ = manager.Stop() }()

	// Skip if eBPF not supported.
	if !manager.GetCapabilities().Supported {
		t.Skip("eBPF not supported on this system")
	}

	ctx := context.Background()
	req := &meshv1.StartEbpfCollectorRequest{
		AgentId:     "test-agent",
		ServiceName: "test-service",
		Kind:        agentv1.EbpfCollectorKind_EBPF_COLLECTOR_KIND_SYSCALL_STATS,
		Config:      map[string]string{},
		Duration:    durationpb.New(2 * time.Second),
	}

	// Start collector.
	resp, err := manager.StartCollector(ctx, req)
	if err != nil {
		t.Fatalf("failed to start collector: %v", err)
	}

	if !resp.Supported {
		t.Fatalf("expected supported=true, got error: %s", resp.Error)
	}

	if resp.CollectorId == "" {
		t.Fatal("expected non-empty collector ID")
	}

	t.Logf("Started collector: %s", resp.CollectorId)

	// Wait a bit for events to be collected.
	time.Sleep(1500 * time.Millisecond)

	// Get events.
	events, err := manager.GetEvents(resp.CollectorId)
	if err != nil {
		t.Fatalf("failed to get events: %v", err)
	}

	t.Logf("Collected %d events", len(events))

	if len(events) == 0 {
		t.Error("expected some events to be collected")
	}

	// Verify event structure.
	for i, event := range events {
		if event.Timestamp == nil {
			t.Errorf("event %d: timestamp is nil", i)
		}

		if event.Payload == nil {
			t.Errorf("event %d: payload is nil", i)
			continue
		}

		// Check if it's syscall stats.
		if stats, ok := event.Payload.(*meshv1.EbpfEvent_SyscallStats); ok {
			if stats.SyscallStats.SyscallName == "" {
				t.Errorf("event %d: syscall name is empty", i)
			}
			t.Logf("Event %d: %s called %d times", i, stats.SyscallStats.SyscallName, stats.SyscallStats.CallCount)
		} else {
			t.Errorf("event %d: unexpected payload type", i)
		}
	}

	// Stop collector.
	err = manager.StopCollector(resp.CollectorId)
	if err != nil {
		t.Fatalf("failed to stop collector: %v", err)
	}

	// Try to get events from stopped collector (should fail).
	_, err = manager.GetEvents(resp.CollectorId)
	if err == nil {
		t.Error("expected error when getting events from stopped collector")
	}
}

func TestManager_AutoStop(t *testing.T) {
	logger := zerolog.New(zerolog.NewTestWriter(t))
	manager := NewManager(Config{Logger: logger})
	defer func() { _ = manager.Stop() }()

	// Skip if eBPF not supported.
	if !manager.GetCapabilities().Supported {
		t.Skip("eBPF not supported on this system")
	}

	ctx := context.Background()
	req := &meshv1.StartEbpfCollectorRequest{
		AgentId:     "test-agent",
		ServiceName: "test-service",
		Kind:        agentv1.EbpfCollectorKind_EBPF_COLLECTOR_KIND_SYSCALL_STATS,
		Config:      map[string]string{},
		Duration:    durationpb.New(1 * time.Second), // Short duration.
	}

	// Start collector.
	resp, err := manager.StartCollector(ctx, req)
	if err != nil {
		t.Fatalf("failed to start collector: %v", err)
	}

	if !resp.Supported {
		t.Fatalf("expected supported=true, got error: %s", resp.Error)
	}

	collectorID := resp.CollectorId
	t.Logf("Started collector: %s (expires in 1s)", collectorID)

	// Verify collector is running.
	_, err = manager.GetEvents(collectorID)
	if err != nil {
		t.Errorf("expected collector to be running: %v", err)
	}

	// Wait for expiration (1s duration + small buffer).
	time.Sleep(1500 * time.Millisecond)

	// Verify collector events are still available after expiration (grace period).
	// The collector is marked as expired but events remain available for 1 hour.
	_, err = manager.GetEvents(collectorID)
	if err != nil {
		t.Errorf("expected collector events to still be available after expiration: %v", err)
	}

	t.Logf("Collector expired but events still available as expected")
}
