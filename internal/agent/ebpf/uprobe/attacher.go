// Package uprobe provides shared uprobe attachment functionality.
package uprobe

import (
	"fmt"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
	"github.com/rs/zerolog"
)

// AttachConfig contains configuration for uprobe attachment.
type AttachConfig struct {
	// PID of the target process.
	PID uint32

	// Offset is the absolute address/offset from the binary start.
	Offset uint64

	// BinaryPath is the original binary path (used for logging only).
	BinaryPath string

	// AttachReturn specifies whether to attach uretprobe.
	// Note: uretprobes are incompatible with Go programs.
	AttachReturn bool

	// PIDFilter is the PID value for UprobeOptions.
	// Use the target process PID (recommended) so perf_event_open uses
	// pid=PIDFilter, cpu=-1, which fires on all CPUs for that process.
	// Use 0 (pid=-1, cpu=0) only as a last resort: it fires on CPU 0 only
	// and will miss goroutines that migrate CPUs during blocking calls.
	PIDFilter int

	// Logger for debug/error messages.
	Logger zerolog.Logger
}

// AttachResult contains resources created during uprobe attachment.
type AttachResult struct {
	// EntryLink is the link for the uprobe (function entry).
	EntryLink link.Link

	// ReturnLink is the link for the uretprobe (function return).
	// Will be nil if AttachReturn is false.
	ReturnLink link.Link

	// ReturnLinks holds multiple return probe links for RET-instruction uprobes (RFD 073).
	// Each link is attached to a RET instruction offset within the function.
	ReturnLinks []link.Link

	// Reader is the ring buffer reader for events.
	Reader *ringbuf.Reader
}

// Close cleans up all resources.
func (r *AttachResult) Close() error {
	var errs []error

	if r.Reader != nil {
		if err := r.Reader.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close reader: %w", err))
		}
	}

	if r.ReturnLink != nil {
		if err := r.ReturnLink.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close return link: %w", err))
		}
	}

	// Close all RET-instruction return links (RFD 073).
	for i, l := range r.ReturnLinks {
		if l != nil {
			if err := l.Close(); err != nil {
				errs = append(errs, fmt.Errorf("close return link %d: %w", i, err))
			}
		}
	}

	if r.EntryLink != nil {
		if err := r.EntryLink.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close entry link: %w", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors during cleanup: %v", errs)
	}

	return nil
}

// AttachReturnProbes attaches uprobes to RET instruction offsets within a function (RFD 073).
// Each offset is relative to the binary start (funcOffset + retRelativeOffset).
// Returns the attached links. Caller must close them.
func AttachReturnProbes(
	cfg AttachConfig,
	returnProg *ebpf.Program,
	retOffsets []uint64,
) ([]link.Link, error) {
	if returnProg == nil {
		return nil, fmt.Errorf("return program is required")
	}

	if len(retOffsets) == 0 {
		return nil, nil
	}

	resolvedPath := fmt.Sprintf("/proc/%d/exe", cfg.PID)
	exe, err := link.OpenExecutable(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("open executable: %w", err)
	}

	var links []link.Link
	for _, retOffset := range retOffsets {
		absOffset := cfg.Offset + retOffset
		l, err := exe.Uprobe("", returnProg, &link.UprobeOptions{
			Address: absOffset,
			PID:     cfg.PIDFilter,
		})
		if err != nil {
			// Clean up previously attached links on error.
			for _, prev := range links {
				prev.Close() //nolint:errcheck
			}
			return nil, fmt.Errorf("attach return uprobe at offset 0x%x: %w", absOffset, err)
		}

		cfg.Logger.Debug().
			Uint64("offset", absOffset).
			Uint64("ret_offset", retOffset).
			Msg("Attached return uprobe to RET instruction")

		links = append(links, l)
	}

	return links, nil
}

// AttachUprobe attaches eBPF uprobe to a function in a binary.
// It resolves the binary path using /proc/{pid}/exe (works in sidecar mode with
// shared PID namespace), attaches uprobe/uretprobe, and creates a ring buffer reader.
//
// The caller is responsible for closing the BPF programs and maps, but must call
// AttachResult.Close() to clean up links and reader.
func AttachUprobe(
	cfg AttachConfig,
	entryProg *ebpf.Program,
	returnProg *ebpf.Program,
	eventsMap *ebpf.Map,
) (*AttachResult, error) {
	if entryProg == nil {
		return nil, fmt.Errorf("entry program is required")
	}

	if eventsMap == nil {
		return nil, fmt.Errorf("events map is required")
	}

	if cfg.AttachReturn && returnProg == nil {
		return nil, fmt.Errorf("return program is required when AttachReturn is true")
	}

	// 1. Resolve binary path using /proc/{pid}/exe.
	// In sidecar mode with shared PID namespace, use /proc/{pid}/exe which is a
	// symlink to the actual binary. This works for uprobe attachment even when
	// the binary path is only visible in the container's mount namespace.
	resolvedPath := fmt.Sprintf("/proc/%d/exe", cfg.PID)

	cfg.Logger.Debug().
		Str("container_path", cfg.BinaryPath).
		Str("proc_exe_path", resolvedPath).
		Uint32("pid", cfg.PID).
		Uint64("offset", cfg.Offset).
		Msg("Using /proc/{pid}/exe for uprobe attachment")

	// 2. Open executable for uprobe attachment.
	exe, err := link.OpenExecutable(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("open executable (path=%s): %w", resolvedPath, err)
	}

	cfg.Logger.Debug().
		Str("exe_path", resolvedPath).
		Msg("Successfully opened executable for uprobe attachment")

	result := &AttachResult{}

	// 3. Attach uprobe (function entry).
	// Use Address field for absolute address from SDK (not Offset which is relative).
	// Pass empty symbol since we're using absolute addressing.
	cfg.Logger.Debug().
		Uint64("address", cfg.Offset).
		Int("pid_filter", cfg.PIDFilter).
		Msg("Attaching uprobe to function entry")

	result.EntryLink, err = exe.Uprobe("", entryProg, &link.UprobeOptions{
		Address: cfg.Offset,
		PID:     cfg.PIDFilter,
	})
	if err != nil {
		return nil, fmt.Errorf("attach uprobe entry: %w", err)
	}

	cfg.Logger.Debug().Msg("Successfully attached uprobe to function entry")

	// 4. Attach uretprobe (function return) if requested.
	if cfg.AttachReturn {
		cfg.Logger.Debug().
			Uint64("address", cfg.Offset).
			Int("pid_filter", cfg.PIDFilter).
			Msg("Attaching uretprobe to function return")

		result.ReturnLink, err = exe.Uretprobe("", returnProg, &link.UprobeOptions{
			Address: cfg.Offset,
			PID:     cfg.PIDFilter,
		})
		if err != nil {
			// Clean up entry link on error.
			result.EntryLink.Close() // nolint:errcheck
			return nil, fmt.Errorf("attach uretprobe exit: %w", err)
		}

		cfg.Logger.Debug().Msg("Successfully attached uretprobe to function return")
	}

	// 5. Create ring buffer reader for events.
	result.Reader, err = ringbuf.NewReader(eventsMap)
	if err != nil {
		// Clean up links on error.
		if result.ReturnLink != nil {
			result.ReturnLink.Close() // nolint:errcheck
		}
		result.EntryLink.Close() // nolint:errcheck
		return nil, fmt.Errorf("create ringbuf reader: %w", err)
	}

	cfg.Logger.Info().
		Str("binary_path", resolvedPath).
		Uint64("offset", cfg.Offset).
		Uint32("pid", cfg.PID).
		Bool("attach_return", cfg.AttachReturn).
		Msg("Successfully attached uprobe")

	return result, nil
}
