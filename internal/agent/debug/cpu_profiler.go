//go:build linux
// +build linux

package debug

import (
	"bytes"
	"fmt"
	"time"
	"unsafe"

	"github.com/cilium/ebpf"
	"github.com/rs/zerolog"
	"golang.org/x/sys/unix"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/safe"
	"github.com/coral-mesh/coral/internal/sys/proc"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -tags linux cpu_profile ./bpf/cpu_profile.bpf.c -- -I../ebpf/bpf/headers

const (
	defaultSampleFrequency = 99   // 99Hz sampling
	maxSampleFrequency     = 1000 // 1000Hz max
	maxStackDepth          = 127  // Max stack depth from BPF
)

// CPUProfileSession represents an active CPU profiling session.
type CPUProfileSession struct {
	PID              int
	StartTime        time.Time
	Duration         time.Duration
	Frequency        int
	Logger           zerolog.Logger
	BPFObjects       *cpu_profileObjects
	PerfEventFDs     []int
	StackTraces      *ebpf.Map         // Reference to stack_traces map
	StackCounts      *ebpf.Map         // Reference to stack_counts map
	Symbolizer       *Symbolizer       // Symbol resolver for address -> function name
	KernelSymbolizer *KernelSymbolizer // Kernel symbol resolver (shared across sessions)
}

// CPUProfileResult contains the results of a CPU profiling session.
type CPUProfileResult struct {
	Samples      []*agentv1.StackSample
	TotalSamples uint64
	LostSamples  uint32
}

// stackKey matches the struct in cpu_profile.bpf.c.
type stackKey struct {
	PID           uint32
	UserStackID   int32
	KernelStackID int32
}

// StartCPUProfile starts a CPU profiling session.
func StartCPUProfile(pid int, durationSeconds int, frequencyHz int, kernelSymbolizer *KernelSymbolizer, logger zerolog.Logger) (*CPUProfileSession, error) {
	if frequencyHz <= 0 {
		frequencyHz = defaultSampleFrequency
	}
	if frequencyHz > maxSampleFrequency {
		return nil, fmt.Errorf("frequency %dHz exceeds maximum %dHz", frequencyHz, maxSampleFrequency)
	}

	if durationSeconds <= 0 {
		durationSeconds = 30 // Default 30 seconds
	}

	// Load BPF program.
	objs := &cpu_profileObjects{}
	if err := loadCpu_profileObjects(objs, nil); err != nil {
		return nil, fmt.Errorf("load BPF objects: %w", err)
	}

	sample, clamp := safe.IntToUint64(frequencyHz)
	if clamp {
		objs.Close() // nolint:errcheck
		return nil, fmt.Errorf("invalid frequency %dHz being clamped", frequencyHz)
	}

	// Open perf events for all threads in the target process.
	// Use PERF_COUNT_SW_TASK_CLOCK for per-task CPU profiling. This measures CPU time
	// consumed by each task and works reliably across environments including
	// Docker Desktop VMs where PERF_COUNT_SW_CPU_CLOCK may not fire.
	// PerfBitInherit ensures new threads spawned after we start are also profiled.
	attr := &unix.PerfEventAttr{
		Type:   unix.PERF_TYPE_SOFTWARE,
		Config: unix.PERF_COUNT_SW_TASK_CLOCK,
		Size:   uint32(unsafe.Sizeof(unix.PerfEventAttr{})),
		Sample: sample,                                 // Sample frequency in Hz
		Bits:   unix.PerfBitFreq | unix.PerfBitInherit, // Frequency mode + inherit to child threads
	}

	// Enumerate all threads to attach perf events to each one.
	// This is necessary because Go programs run goroutines across multiple OS threads,
	// and a single perf event only monitors one thread.
	tids, err := proc.ListThreads(pid)
	if err != nil {
		logger.Warn().Err(err).Int("pid", pid).Msg("Failed to list threads, falling back to main PID only")
		tids = []int{pid}
	}

	var perfEventFDs []int
	for _, tid := range tids {
		fd, err := unix.PerfEventOpen(attr, tid, -1, -1, unix.PERF_FLAG_FD_CLOEXEC)
		if err != nil {
			logger.Warn().Err(err).Int("tid", tid).Msg("Failed to open perf event for thread, skipping")
			continue
		}

		// Attach BPF program to perf event.
		if err := unix.IoctlSetInt(fd, unix.PERF_EVENT_IOC_SET_BPF, objs.ProfileCpu.FD()); err != nil {
			unix.Close(fd) // nolint:errcheck
			logger.Warn().Err(err).Int("tid", tid).Msg("Failed to attach BPF to perf event, skipping")
			continue
		}

		// Enable the perf event.
		if err := unix.IoctlSetInt(fd, unix.PERF_EVENT_IOC_ENABLE, 0); err != nil {
			unix.Close(fd) // nolint:errcheck
			logger.Warn().Err(err).Int("tid", tid).Msg("Failed to enable perf event, skipping")
			continue
		}

		perfEventFDs = append(perfEventFDs, fd)
	}

	if len(perfEventFDs) == 0 {
		objs.Close() // nolint:errcheck
		return nil, fmt.Errorf("failed to open perf events for any thread of pid %d", pid)
	}

	logger.Info().Int("thread_count", len(perfEventFDs)).Int("total_threads", len(tids)).Msg("Perf events attached to threads")

	// Create symbolizer for address resolution
	// Use /proc/PID/exe directly - it works across container boundaries in shared PID namespace
	var symbolizer *Symbolizer
	binaryPath, err := proc.GetBinaryPath(pid)
	if err == nil {
		symbolizer, err = NewSymbolizer(binaryPath, pid, logger)
	}
	if err != nil {
		logger.Warn().Err(err).Str("binary", binaryPath).Msg("Failed to create symbolizer, outputting raw addresses")
		symbolizer = nil // Continue without symbolization
	} else {
		logger.Info().Str("binary", binaryPath).Msg("Symbolizer initialized")
	}

	session := &CPUProfileSession{
		PID:              pid,
		StartTime:        time.Now(),
		Duration:         time.Duration(durationSeconds) * time.Second,
		Frequency:        frequencyHz,
		Logger:           logger,
		BPFObjects:       objs,
		PerfEventFDs:     perfEventFDs,
		StackTraces:      objs.StackTraces,
		StackCounts:      objs.StackCounts,
		Symbolizer:       symbolizer,
		KernelSymbolizer: kernelSymbolizer,
	}

	logger.Info().
		Int("pid", pid).
		Int("duration_seconds", durationSeconds).
		Int("frequency_hz", frequencyHz).
		Msg("CPU profiling session started")

	return session, nil
}

// CollectProfile waits for the duration and collects the CPU profile.
func (s *CPUProfileSession) CollectProfile() (*CPUProfileResult, error) {
	// Wait for the profiling duration.
	time.Sleep(s.Duration)

	// Read stack counts from the BPF map.
	samples, totalSamples, err := s.readStackCounts()
	if err != nil {
		return nil, fmt.Errorf("read stack counts: %w", err)
	}

	result := &CPUProfileResult{
		Samples:      samples,
		TotalSamples: totalSamples,
		LostSamples:  0, // TODO: Track lost samples if map overflows
	}

	s.Logger.Info().
		Uint64("total_samples", totalSamples).
		Int("unique_stacks", len(samples)).
		Msg("CPU profile collected")

	return result, nil
}

// DrainStackCounts reads and clears accumulated samples from the BPF maps without
// sleeping. Used by the continuous profiler which keeps a persistent BPF session.
func (s *CPUProfileSession) DrainStackCounts() (*CPUProfileResult, error) {
	samples, totalSamples, err := s.readStackCounts()
	if err != nil {
		return nil, fmt.Errorf("read stack counts: %w", err)
	}

	return &CPUProfileResult{
		Samples:      samples,
		TotalSamples: totalSamples,
	}, nil
}

// readStackCounts reads and symbolizes stack traces from the BPF maps.
func (s *CPUProfileSession) readStackCounts() ([]*agentv1.StackSample, uint64, error) {
	var samples []*agentv1.StackSample
	var totalSamples uint64

	// Iterate over stack_counts map.
	var key stackKey
	var value uint64
	iter := s.StackCounts.Iterate()

	for iter.Next(&key, &value) {
		totalSamples += value

		// Resolve stack trace.
		frames, err := s.resolveStack(key)
		if err != nil {
			s.Logger.Warn().
				Err(err).
				Int32("user_stack_id", key.UserStackID).
				Int32("kernel_stack_id", key.KernelStackID).
				Msg("Failed to resolve stack")
			continue
		}

		if len(frames) == 0 {
			continue
		}

		sample := &agentv1.StackSample{
			FrameNames: frames,
			Count:      value,
		}
		samples = append(samples, sample)
	}

	if err := iter.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate stack counts: %w", err)
	}

	// Clear maps after reading to prevent unbounded accumulation across collection windows.
	var delKey stackKey
	delIter := s.StackCounts.Iterate()
	for delIter.Next(&delKey, &value) {
		if err := s.StackCounts.Delete(&delKey); err != nil {
			s.Logger.Warn().Err(err).Msg("Failed to delete stack count entry")
		}
	}

	return samples, totalSamples, nil
}

// resolveStack resolves a stack trace from stack IDs.
func (s *CPUProfileSession) resolveStack(key stackKey) ([]string, error) {
	var frames []string

	// Resolve user stack if available.
	if key.UserStackID >= 0 {
		userFrames, err := s.getStackTrace(key.UserStackID)
		if err != nil {
			return nil, fmt.Errorf("get user stack: %w", err)
		}
		for _, addr := range userFrames {
			if addr == 0 {
				break
			}

			// Try to symbolize if symbolizer is available
			if s.Symbolizer != nil {
				if sym, err := s.Symbolizer.Resolve(addr); err == nil {
					frames = append(frames, FormatSymbol(sym))
					continue
				}
			}

			// Fallback to raw address if symbolization fails or unavailable
			frames = append(frames, fmt.Sprintf("0x%x", addr))
		}
	}

	// Resolve kernel stack if available.
	if key.KernelStackID >= 0 {
		kernelFrames, err := s.getStackTrace(key.KernelStackID)
		if err != nil {
			s.Logger.Warn().Err(err).Msg("Failed to get kernel stack")
			// Continue with user frames only
		} else {
			for _, addr := range kernelFrames {
				if addr == 0 {
					break
				}

				// Try to symbolize kernel address
				if s.KernelSymbolizer != nil {
					if sym := s.KernelSymbolizer.Resolve(addr); sym != "" {
						frames = append(frames, fmt.Sprintf("[kernel] %s", sym))
						continue
					}
				}

				// Fallback to raw address
				frames = append(frames, fmt.Sprintf("[kernel] 0x%x", addr))
			}
		}
	}

	return frames, nil
}

// getStackTrace retrieves a stack trace from the stack_traces map.
func (s *CPUProfileSession) getStackTrace(stackID int32) ([]uint64, error) {
	var stack [maxStackDepth]uint64
	key, clamp := safe.Int32ToUint32(stackID)
	if clamp {
		return nil, fmt.Errorf("invalid stack ID number would overflow: %d", stackID)
	}

	if err := s.StackTraces.Lookup(&key, &stack); err != nil {
		return nil, fmt.Errorf("lookup stack %d: %w", stackID, err)
	}

	// Convert fixed array to slice.
	result := make([]uint64, 0, maxStackDepth)
	for _, addr := range stack {
		if addr == 0 {
			break
		}
		result = append(result, addr)
	}

	return result, nil
}

// FormatFoldedStacks formats stack samples in the "folded" format for flamegraph.pl.
func FormatFoldedStacks(samples []*agentv1.StackSample) string {
	var buf bytes.Buffer

	for _, sample := range samples {
		if len(sample.FrameNames) == 0 {
			continue
		}

		// Folded format: frame1;frame2;frame3 count
		// Stack frames should be from outermost (root) to innermost (leaf).
		// Reverse the order since BPF captures innermost first.
		for i := len(sample.FrameNames) - 1; i >= 0; i-- {
			buf.WriteString(sample.FrameNames[i])
			if i > 0 {
				buf.WriteString(";")
			}
		}
		buf.WriteString(fmt.Sprintf(" %d\n", sample.Count))
	}

	return buf.String()
}

// Close closes the CPU profiling session and cleans up resources.
func (s *CPUProfileSession) Close() error {
	var errs []error

	for _, fd := range s.PerfEventFDs {
		if fd > 0 {
			_ = unix.IoctlSetInt(fd, unix.PERF_EVENT_IOC_DISABLE, 0)
			if err := unix.Close(fd); err != nil {
				errs = append(errs, fmt.Errorf("close perf event fd %d: %w", fd, err))
			}
		}
	}

	if s.BPFObjects != nil {
		if err := s.BPFObjects.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close BPF objects: %w", err))
		}
	}

	if s.Symbolizer != nil {
		if err := s.Symbolizer.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close symbolizer: %w", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing CPU profile session: %v", errs)
	}

	s.Logger.Info().Msg("CPU profiling session closed")
	return nil
}
