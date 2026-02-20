//go:build !linux

package ebpf

import (
	"context"
	"fmt"

	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/rs/zerolog"
)

// UprobeCollector is a stub for non-Linux platforms.
// eBPF uprobe collection requires Linux.
type UprobeCollector struct{}

// Start implements Collector. Always returns an error on non-Linux.
func (c *UprobeCollector) Start(_ context.Context) error {
	return fmt.Errorf("uprobe collection requires Linux")
}

// Stop implements Collector. Always returns an error on non-Linux.
func (c *UprobeCollector) Stop() error {
	return fmt.Errorf("uprobe collection requires Linux")
}

// GetEvents implements Collector. Always returns an error on non-Linux.
func (c *UprobeCollector) GetEvents() ([]*meshv1.EbpfEvent, error) {
	return nil, fmt.Errorf("uprobe collection requires Linux")
}

// UpdateFilter is a stub for non-Linux platforms.
func (c *UprobeCollector) UpdateFilter(_ UprobeFilter) error {
	return fmt.Errorf("uprobe collection requires Linux")
}

// NewUprobeCollector is a stub for non-Linux platforms.
func NewUprobeCollector(_ zerolog.Logger, _ *UprobeConfig) (*UprobeCollector, error) {
	return nil, fmt.Errorf("uprobe collection requires Linux")
}
