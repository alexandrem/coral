package ebpf_test

import (
	"context"
	"fmt"
	"time"

	agentv1 "github.com/coral-io/coral/coral/agent/v1"
	meshv1 "github.com/coral-io/coral/coral/mesh/v1"
	"github.com/coral-io/coral/internal/agent/ebpf"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Example demonstrates basic eBPF collector usage.
func Example() {
	// Create logger.
	logger := zerolog.New(zerolog.NewConsoleWriter()).With().Timestamp().Logger()

	// Create eBPF manager.
	manager := ebpf.NewManager(ebpf.Config{
		Logger: logger,
	})
	defer manager.Stop()

	// Check capabilities.
	caps := manager.GetCapabilities()
	fmt.Printf("eBPF supported: %v\n", caps.Supported)
	fmt.Printf("Kernel: %s\n", caps.KernelVersion)

	// Only proceed if eBPF is supported (Linux only).
	if !caps.Supported {
		fmt.Println("eBPF not supported on this system (requires Linux)")
		return
	}

	// Start a syscall stats collector.
	ctx := context.Background()
	req := &meshv1.StartEbpfCollectorRequest{
		AgentId:     "example-agent",
		ServiceName: "example-service",
		Kind:        agentv1.EbpfCollectorKind_EBPF_COLLECTOR_KIND_SYSCALL_STATS,
		Config:      map[string]string{},
		Duration:    durationpb.New(5 * time.Second),
	}

	resp, err := manager.StartCollector(ctx, req)
	if err != nil {
		fmt.Printf("Error starting collector: %v\n", err)
		return
	}

	if !resp.Supported {
		fmt.Printf("Collector not supported: %s\n", resp.Error)
		return
	}

	fmt.Printf("Started collector: %s\n", resp.CollectorId)
	fmt.Printf("Expires at: %v\n", resp.ExpiresAt.AsTime())

	// Wait for some events to be collected.
	time.Sleep(2 * time.Second)

	// Get collected events.
	events, err := manager.GetEvents(resp.CollectorId)
	if err != nil {
		fmt.Printf("Error getting events: %v\n", err)
		return
	}

	fmt.Printf("Collected %d events\n", len(events))

	// Display some events.
	for i, event := range events[:min(5, len(events))] {
		if stats, ok := event.Payload.(*meshv1.EbpfEvent_SyscallStats); ok {
			fmt.Printf("  [%d] %s: %d calls, %d errors, %dus total\n",
				i,
				stats.SyscallStats.SyscallName,
				stats.SyscallStats.CallCount,
				stats.SyscallStats.ErrorCount,
				stats.SyscallStats.TotalDurationUs,
			)
		}
	}

	// Stop collector.
	err = manager.StopCollector(resp.CollectorId)
	if err != nil {
		fmt.Printf("Error stopping collector: %v\n", err)
		return
	}

	fmt.Println("Collector stopped")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
