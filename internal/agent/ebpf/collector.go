package ebpf

import (
	"context"

	meshv1 "github.com/coral-io/coral/coral/mesh/v1"
)

// Collector defines the interface for all eBPF collectors.
type Collector interface {
	// Start begins collecting eBPF data.
	Start(ctx context.Context) error

	// Stop stops collecting and cleans up resources.
	Stop() error

	// GetEvents retrieves collected events since last call.
	GetEvents() ([]*meshv1.EbpfEvent, error)
}
