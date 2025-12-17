//go:build !linux
// +build !linux

package debug

import (
	"fmt"

	"github.com/rs/zerolog"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
)

// CPUProfileSession represents an active CPU profiling session (stub for non-Linux).
type CPUProfileSession struct{}

// CPUProfileResult contains the results of a CPU profiling session (stub for non-Linux).
type CPUProfileResult struct {
	Samples      []*colonypb.StackSample
	TotalSamples uint64
	LostSamples  uint32
}

// StartCPUProfile returns an error on non-Linux systems.
func StartCPUProfile(pid int, durationSeconds int, frequencyHz int, logger zerolog.Logger) (*CPUProfileSession, error) {
	return nil, fmt.Errorf("CPU profiling is only supported on Linux")
}

// CollectProfile returns an error on non-Linux systems.
func (s *CPUProfileSession) CollectProfile() (*CPUProfileResult, error) {
	return nil, fmt.Errorf("CPU profiling is only supported on Linux")
}

// Close returns an error on non-Linux systems.
func (s *CPUProfileSession) Close() error {
	return nil
}

// FormatFoldedStacks formats stack samples in the "folded" format (stub for non-Linux).
func FormatFoldedStacks(samples []*colonypb.StackSample) string {
	return ""
}
