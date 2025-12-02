package debug

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go uprobe_monitor ./bpf/uprobe_monitor.bpf.c -- -I../ebpf/bpf/headers

// attachUprobeLocked attaches eBPF uprobe to target function.
// Caller must hold m.mu.
func (m *DebugSessionManager) attachUprobeLocked(
	session *DebugSession,
	pid int,
	binaryPath string,
	offset uint64,
) error {
	// 1. Load BPF program
	// We use the generated LoadUprobeMonitorObjects function
	objs := uprobe_monitorObjects{}
	if err := loadUprobe_monitorObjects(&objs, nil); err != nil {
		return fmt.Errorf("load BPF objects: %w", err)
	}

	// Ensure cleanup on error
	// We create a collection from the objects to manage them easily
	// But here we have individual programs in objs
	// We'll manage them manually or put them in the session

	// 2. Open executable
	exe, err := link.OpenExecutable(binaryPath)
	if err != nil {
		objs.Close() // nolint:errcheck
		return fmt.Errorf("open executable: %w", err)
	}

	// 3. Attach uprobe (entry)
	entryLink, err := exe.Uprobe(
		"", // Symbol name empty because we use offset
		objs.ProbeEntry,
		&link.UprobeOptions{
			Offset: offset,
			PID:    pid,
		},
	)
	if err != nil {
		objs.Close() // nolint:errcheck
		return fmt.Errorf("attach uprobe entry: %w", err)
	}

	// 4. Attach uretprobe (exit)
	exitLink, err := exe.Uretprobe(
		"", // Symbol name empty because we use offset
		objs.ProbeExit,
		&link.UprobeOptions{
			Offset: offset,
			PID:    pid,
		},
	)
	if err != nil {
		entryLink.Close() // nolint:errcheck
		objs.Close()      // nolint:errcheck
		return fmt.Errorf("attach uretprobe exit: %w", err)
	}

	// 4. Open ring buffer reader
	reader, err := ringbuf.NewReader(objs.Events)
	if err != nil {
		exitLink.Close()  // nolint:errcheck
		entryLink.Close() // nolint:errcheck
		objs.Close()      // nolint:errcheck
		return fmt.Errorf("create ringbuf reader: %w", err)
	}

	// 5. Store session resources
	session.EntryLink = entryLink
	session.ExitLink = exitLink
	session.Reader = reader

	// 6. Start event reader goroutine
	go m.readEvents(session.ID, reader)

	return nil
}

// DetachUprobe detaches eBPF probe and cleans up.
func (m *DebugSessionManager) DetachUprobe(sessionID string) error {
	// This is called from CloseSession which holds the lock.
	// So we don't need to lock here if we assume it's internal.
	// But CloseSession calls it.

	session, ok := m.sessions[sessionID]
	if !ok {
		return nil // Already gone
	}

	var errs []error

	if session.Reader != nil {
		if err := session.Reader.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if session.ExitLink != nil {
		if err := session.ExitLink.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if session.EntryLink != nil {
		if err := session.EntryLink.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	// We also need to close the objects (Maps/Programs)
	// But we didn't store them in session.
	// We should update DebugSession to store *uprobe_monitorObjects

	if len(errs) > 0 {
		return fmt.Errorf("errors closing session: %v", errs)
	}

	return nil
}

func (m *DebugSessionManager) readEvents(sessionID string, reader *ringbuf.Reader) {
	for {
		record, err := reader.Read()
		if err != nil {
			if errors.Is(err, ringbuf.ErrClosed) {
				return
			}
			m.logger.Error().Err(err).Str("session_id", sessionID).Msg("Error reading ringbuf event")
			continue
		}

		// Parse event
		// struct uprobe_event {
		//     __u64 timestamp;
		//     __u32 pid;
		//     __u32 tid;
		//     __u64 duration_ns;
		// };

		if len(record.RawSample) < 24 { // 8 + 4 + 4 + 8 = 24 bytes
			m.logger.Warn().Int("len", len(record.RawSample)).Msg("Invalid event size")
			continue
		}

		timestamp := binary.LittleEndian.Uint64(record.RawSample[0:8])
		pid := binary.LittleEndian.Uint32(record.RawSample[8:12])
		tid := binary.LittleEndian.Uint32(record.RawSample[12:16])
		duration := binary.LittleEndian.Uint64(record.RawSample[16:24])

		event := &agentv1.DebugEvent{
			SessionId:  sessionID,
			Timestamp:  int64(timestamp),
			Pid:        int32(pid),
			Tid:        int32(tid),
			DurationNs: int64(duration),
		}

		// Non-blocking send to avoid blocking the reader
		select {
		case m.eventCh <- event:
		default:
			m.logger.Warn().Str("session_id", sessionID).Msg("Event buffer full, dropping event")
		}
	}
}
