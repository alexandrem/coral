package debug

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/cilium/ebpf/ringbuf"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	"github.com/coral-mesh/coral/internal/agent/ebpf/uprobe"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go uprobe_monitor ./bpf/uprobe_monitor.bpf.c -- -I../ebpf/bpf/headers

// attachUprobeLocked attaches eBPF uprobe to target function.
// Caller must hold m.mu.
func (m *SessionManager) attachUprobeLocked(
	session *DebugSession,
	pid int,
	binaryPath string,
	offset uint64,
) error {
	// 1. Load BPF program.
	objs := &uprobe_monitorObjects{}
	if err := loadUprobe_monitorObjects(objs, nil); err != nil {
		return fmt.Errorf("load BPF objects: %w", err)
	}

	// 2. Attach uprobe using shared attacher.
	attachResult, err := uprobe.AttachUprobe(
		uprobe.AttachConfig{
			PID:          uint32(pid),
			Offset:       offset,
			BinaryPath:   binaryPath,
			AttachReturn: true, // Debug sessions attach both entry and return probes
			PIDFilter:    pid,  // Trace specific PID for debug sessions
			Logger:       m.logger,
		},
		objs.ProbeEntry,
		objs.ProbeExit,
		objs.Events,
	)
	if err != nil {
		objs.Close() // nolint:errcheck
		return fmt.Errorf("attach uprobe: %w", err)
	}

	// 3. Store session resources.
	session.BPFObjects = objs
	session.AttachResult = attachResult
	session.Reader = attachResult.Reader

	// 4. Start event reader goroutine.
	go m.readEvents(session.ID, attachResult.Reader)

	return nil
}

// DetachUprobe detaches eBPF probe and cleans up.
func (m *SessionManager) DetachUprobe(sessionID string) error {
	// This is called from CloseSession which holds the lock.
	session, ok := m.sessions[sessionID]
	if !ok {
		return nil // Already gone
	}

	var errs []error

	// Clean up uprobe attachment resources (links and reader).
	if session.AttachResult != nil {
		if err := session.AttachResult.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close attach result: %w", err))
		}
	}

	// Clean up BPF objects (programs and maps).
	if session.BPFObjects != nil {
		if err := session.BPFObjects.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close BPF objects: %w", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing session: %v", errs)
	}

	return nil
}

func (m *SessionManager) readEvents(sessionID string, reader *ringbuf.Reader) {
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
