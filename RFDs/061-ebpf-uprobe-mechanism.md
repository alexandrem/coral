---
rfd: "061"
title: "eBPF Uprobe Mechanism"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "059", "060" ]
database_migrations: [ ]
areas: [ "agent", "ebpf", "linux" ]
---

# RFD 061 - eBPF Uprobe Mechanism

**Status:** 🚧 Draft

## Summary

This RFD details the **Agent-side** implementation of live debugging. It covers
the `DebugSessionManager`, the eBPF programs for uprobes, and the communication
protocol between the Agent and the Application SDK.

## Problem

The Agent is responsible for the "heavy lifting": injecting eBPF programs into
the kernel to trace a target process. It must do this safely, respecting
resource limits, and correctly mapping high-level function names (from the SDK)
to low-level memory addresses.

## Solution

The Agent will implement a `DebugSessionManager` that:

1. Receives `AttachUprobe` commands from the Colony.
2. Queries the target service's SDK to get the function offset.
3. Uses `libbpf` to attach a `uprobe` (entry) and `uretprobe` (exit) to the
   target PID.
4. Reads events from a BPF perf buffer and streams them back to the Colony.

### Agent-SDK Communication

For the Agent to query the SDK, it must be able to reach the application's
network namespace.

* **Sidecar Mode**: Agent and App share `localhost`. Communication is trivial.
* **Node Agent Mode**: Agent runs on the host (or separate namespace). App runs
  in a Pod.
    * **Discovery**: The Agent discovers the Pod IP via the Container Runtime (
      CRI) or Kubernetes API (RFD 012).
    * **Connection**: The SDK must listen on `0.0.0.0` (or the Pod IP). The
      Agent connects to `PodIP:SdkPort`.
    * **Security**: The SDK should verify that the connection comes from the
      Agent (e.g., via mTLS or network policy, though for V1 we may rely on
      network segmentation).

### eBPF Implementation

We will use `libbpf` and CO-RE (Compile Once – Run Everywhere).

#### BPF Maps

* `start_times`: Hash map (`pid_tgid` -> `timestamp`) to track function entry
  time.
* `events`: Perf event array to send data to userspace.

#### Uprobe (Entry)

```c
SEC("uprobe")
int probe_entry(struct pt_regs *ctx) {
    __u64 pid_tgid = bpf_get_current_pid_tgid();
    __u64 ts = bpf_ktime_get_ns();
    bpf_map_update_elem(&start_times, &pid_tgid, &ts, BPF_ANY);
    return 0;
}
```

#### Uretprobe (Exit)

```c
SEC("uretprobe")
int probe_exit(struct pt_regs *ctx) {
    __u64 pid_tgid = bpf_get_current_pid_tgid();
    __u64 end_ts = bpf_ktime_get_ns();
    __u64 *start_ts = bpf_map_lookup_elem(&start_times, &pid_tgid);

    if (start_ts) {
        // Calculate duration and submit event
        struct uprobe_event event = { ... };
        bpf_perf_event_output(ctx, &events, ...);
        bpf_map_delete_elem(&start_times, &pid_tgid);
    }
    return 0;
}
```

### Safety & Resource Limits

To prevent performance degradation:

1. **Max Concurrent Sessions**: Limit to 5 active sessions per Agent.
2. **Max Event Rate**: Rate-limit events in BPF or userspace (e.g., 10,000
   events/sec).
3. **Auto-Detach**: The Agent sets a timer for every session. If the Colony
   doesn't request a detach, the Agent forces a detach after the duration
   expires.
4. **Memory Limit**: BPF maps have fixed sizes (`max_entries`).

### Capability Detection

The Agent must check for kernel support:

* Kernel version >= 4.7 (for uprobes).
* `CAP_BPF` or `CAP_SYS_ADMIN` capability.
* Mount point for `/sys/kernel/debug/tracing`.

This status is reported in the `RuntimeContext` (RFD 018).

## Configuration Changes

### Agent Configuration

The Agent requires configuration for debug session management:

```yaml
# agent-config.yaml
agent:
    debug:
        enabled: true

        # SDK communication
        sdk_api:
            timeout: 5s
            retry_attempts: 3

        # Uprobe limits (safety)
        limits:
            max_concurrent_sessions: 5
            max_session_duration: 600s      # 10 minutes hard limit
            max_events_per_second: 10000
            max_memory_mb: 256

        # BPF program settings
        bpf:
            map_size: 10240                 # BPF map max entries
            perf_buffer_pages: 64           # Perf buffer size
```

### Resource Limits Explanation

| Limit                     | Default | Purpose                                      |
|:--------------------------|:--------|:---------------------------------------------|
| `max_concurrent_sessions` | 5       | Prevent too many active probes               |
| `max_session_duration`    | 600s    | Force detach after 10 minutes                |
| `max_events_per_second`   | 10,000  | Rate limit to prevent performance impact     |
| `max_memory_mb`           | 256     | Limit BPF map and perf buffer memory usage   |
| `map_size`                | 10,240  | Max concurrent function calls being tracked |
| `perf_buffer_pages`       | 64      | Perf buffer size (64 pages = 256KB)          |

## Implementation Plan

### Phase 1: Debug Session Manager

- [ ] Create `DebugSessionManager` component
- [ ] Implement session lifecycle (create, track, expire, cleanup)
- [ ] Add resource limit enforcement
- [ ] Implement auto-detach on expiry

### Phase 2: eBPF Programs

- [ ] Write BPF programs in C (uprobe entry, uretprobe exit)
- [ ] Set up libbpf build infrastructure
- [ ] Implement CO-RE (Compile Once - Run Everywhere) support
- [ ] Create BPF map definitions (start_times, events)

### Phase 3: Uprobe Attachment

- [ ] Implement uprobe attach logic using libbpf
- [ ] Query SDK for function offsets
- [ ] Resolve PID from service name
- [ ] Attach to `/proc/<pid>/exe` at calculated offset
- [ ] Handle attachment failures gracefully

### Phase 4: Event Collection

- [ ] Set up perf event buffer reader
- [ ] Parse events from BPF perf buffer
- [ ] Stream events to Colony via gRPC
- [ ] Implement event rate limiting
- [ ] Add event aggregation (optional optimization)

### Phase 5: Testing & Validation

- [ ] Unit tests for session management
- [ ] Integration tests with sample application (from RFD 060)
- [ ] Performance overhead measurement
- [ ] Resource limit validation
- [ ] Kernel compatibility tests (different kernel versions)

## Testing Strategy

### Unit Tests

* **Session Management**: Test session creation, expiry, cleanup.
* **Resource Limits**: Verify limits are enforced (max sessions, duration,
  memory).
* **Offset Resolution**: Test querying SDK for function offsets.
* **PID Resolution**: Test resolving service name to PID.

### Integration Tests

* **Agent + SDK**: Full cycle: query offset, attach probe, collect events.
* **Agent + Colony**: Stream events to Colony, verify data flow.
* **Multiple Sessions**: Concurrent debug sessions on different functions.
* **Error Handling**: SDK unreachable, invalid function name, PID changes.

### Performance Tests

**Overhead measurement:**

| Scenario                  | Target CPU Overhead | Target Memory Overhead | Target Latency Impact |
|:--------------------------|:--------------------|:-----------------------|:----------------------|
| No active probes          | < 0.1%              | < 5 MB                 | None                  |
| 1 active probe            | < 0.5%              | < 20 MB                | < 1%                  |
| 5 active probes (max)     | < 2%                | < 100 MB               | < 5%                  |
| High event rate (10k/sec) | < 3%                | < 150 MB               | < 10%                 |

**Measurement methodology:**

1. Baseline: Application without agent monitoring.
2. Agent idle: Agent running, no active debug sessions.
3. Active probes: 1, 3, 5 concurrent sessions on high-traffic functions.
4. Metrics: CPU usage, memory (RSS), request latency (P50, P95, P99).

### Security Tests

* **Capability Check**: Verify agent detects missing CAP_BPF.
* **Read-Only Verification**: Confirm probes cannot modify process state.
* **Auto-Detach**: Verify probes detach after session expiry.
* **Resource Exhaustion**: Test behavior when limits exceeded.

## Security Considerations

### Kernel-Level Safety

* **eBPF Verifier**: All BPF programs must pass the kernel verifier, which
  ensures:
    * No infinite loops
    * No out-of-bounds memory access
    * No kernel crashes
* **Read-Only Probes**: Uprobes can only observe; they cannot modify process
  memory or registers.
* **Bounded Execution**: BPF programs have instruction limits and must
  complete quickly.

### Resource Protection

* **Session Limits**: Max 5 concurrent sessions prevents resource exhaustion.
* **Duration Limits**: Sessions expire after 10 minutes (hard limit enforced
  by Agent).
* **Event Rate Limiting**: Limit to 10,000 events/sec prevents network
  saturation.
* **Memory Limits**: BPF maps and perf buffers capped at 256MB total.

### Capability Requirements

* **CAP_BPF** (Linux 5.8+): Preferred, least-privilege capability for BPF
  operations.
* **CAP_SYS_ADMIN** (< 5.8): Required on older kernels, broader permissions.
* **No Root Required**: Agent runs as unprivileged user with specific
  capabilities.

### Audit & Compliance

* **Session Logging**: All debug sessions logged with:
    * User identity (who requested)
    * Timestamp (when)
    * Target (service, function, PID)
    * Duration and event count
* **RBAC Integration**: Debug operations require `debug:attach` permission (
  enforced by Colony).

## Appendix

### Complete eBPF Program Implementation

```c
// uprobe_monitor.bpf.c
// eBPF program for function entry/exit tracing

#include <linux/bpf.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>

// Event structure sent to userspace
struct uprobe_event {
    __u64 timestamp;
    __u32 pid;
    __u32 tid;
    __u64 duration_ns;
};

// BPF map: Track function entry timestamps
// Key: pid_tgid (combined PID/TID)
// Value: entry timestamp (nanoseconds)
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u64);
    __type(value, __u64);
    __uint(max_entries, 10240);
} start_times SEC(".maps");

// BPF map: Perf event array for sending events to userspace
struct {
    __uint(type, BPF_MAP_TYPE_PERF_EVENT_ARRAY);
    __uint(key_size, sizeof(__u32));
    __uint(value_size, sizeof(__u32));
} events SEC(".maps");

// Uprobe: Called on function entry
SEC("uprobe")
int probe_entry(struct pt_regs *ctx) {
    __u64 pid_tgid = bpf_get_current_pid_tgid();
    __u64 ts = bpf_ktime_get_ns();

    // Store entry timestamp in BPF map
    bpf_map_update_elem(&start_times, &pid_tgid, &ts, BPF_ANY);

    return 0;
}

// Uretprobe: Called on function exit
SEC("uretprobe")
int probe_exit(struct pt_regs *ctx) {
    __u64 pid_tgid = bpf_get_current_pid_tgid();
    __u64 end_ts = bpf_ktime_get_ns();

    // Lookup entry timestamp
    __u64 *start_ts = bpf_map_lookup_elem(&start_times, &pid_tgid);
    if (!start_ts) {
        // Entry not found (possible if probe attached mid-execution)
        return 0;
    }

    // Calculate duration
    __u64 duration = end_ts - *start_ts;

    // Build event
    struct uprobe_event event = {
        .timestamp = end_ts,
        .pid = pid_tgid >> 32,
        .tid = (__u32)pid_tgid,
        .duration_ns = duration,
    };

    // Send event to userspace via perf buffer
    bpf_perf_event_output(ctx, &events, BPF_F_CURRENT_CPU,
                         &event, sizeof(event));

    // Clean up entry timestamp
    bpf_map_delete_elem(&start_times, &pid_tgid);

    return 0;
}

char LICENSE[] SEC("license") = "GPL";
```

### Agent Uprobe Attachment (Go)

```go
// internal/agent/debug/uprobe.go
package debug

import (
    "fmt"
    "github.com/cilium/ebpf"
    "github.com/cilium/ebpf/link"
    "github.com/cilium/ebpf/perf"
)

// AttachUprobe attaches eBPF uprobe to target function.
func (m *DebugSessionManager) AttachUprobe(
    pid int,
    binaryPath string,
    offset uint64,
    sessionID string,
) error {
    // 1. Load BPF program
    spec, err := ebpf.LoadCollectionSpec("uprobe_monitor.bpf.o")
    if err != nil {
        return fmt.Errorf("load BPF spec: %w", err)
    }

    coll, err := ebpf.NewCollection(spec)
    if err != nil {
        return fmt.Errorf("create BPF collection: %w", err)
    }

    // 2. Attach uprobe (entry)
    entryProbe := coll.Programs["probe_entry"]
    entryLink, err := link.Uprobe(
        binaryPath,
        entryProbe,
        &link.UprobeOptions{
            Offset: offset,
            PID:    pid,
        },
    )
    if err != nil {
        coll.Close()
        return fmt.Errorf("attach uprobe entry: %w", err)
    }

    // 3. Attach uretprobe (exit)
    exitProbe := coll.Programs["probe_exit"]
    exitLink, err := link.Uretprobe(
        binaryPath,
        exitProbe,
        &link.UprobeOptions{
            Offset: offset,
            PID:    pid,
        },
    )
    if err != nil {
        entryLink.Close()
        coll.Close()
        return fmt.Errorf("attach uretprobe exit: %w", err)
    }

    // 4. Open perf event reader
    eventsMap := coll.Maps["events"]
    reader, err := perf.NewReader(eventsMap, 4096) // 4KB per CPU
    if err != nil {
        exitLink.Close()
        entryLink.Close()
        coll.Close()
        return fmt.Errorf("create perf reader: %w", err)
    }

    // 5. Store session
    m.sessions[sessionID] = &DebugSession{
        ID:         sessionID,
        EntryLink:  entryLink,
        ExitLink:   exitLink,
        Collection: coll,
        Reader:     reader,
    }

    // 6. Start event reader goroutine
    go m.readEvents(sessionID, reader)

    return nil
}

// DetachUprobe detaches eBPF probe and cleans up.
func (m *DebugSessionManager) DetachUprobe(sessionID string) error {
    session, ok := m.sessions[sessionID]
    if !ok {
        return fmt.Errorf("session not found: %s", sessionID)
    }

    // Close in reverse order
    session.Reader.Close()
    session.ExitLink.Close()
    session.EntryLink.Close()
    session.Collection.Close()

    delete(m.sessions, sessionID)
    return nil
}
```

### Network Discovery (Node Agent Mode)

For node agents monitoring pods, the Agent must discover the SDK endpoint:

```go
// internal/agent/discovery/sdk.go
package discovery

import (
    "context"
    "fmt"
    "google.golang.org/grpc"
)

// DiscoverSDK finds SDK endpoint for a given PID.
func DiscoverSDK(ctx context.Context, pid int) (string, error) {
    // 1. Get pod IP via CRI/Kubernetes API
    podIP, err := getPodIPForPID(pid)
    if err != nil {
        return "", err
    }

    // 2. Try default SDK port
    sdkAddr := fmt.Sprintf("%s:9092", podIP)

    // 3. Test connection
    conn, err := grpc.DialContext(
        ctx,
        sdkAddr,
        grpc.WithInsecure(),
        grpc.WithBlock(),
        grpc.WithTimeout(5*time.Second),
    )
    if err != nil {
        return "", fmt.Errorf("SDK not reachable at %s: %w", sdkAddr, err)
    }
    conn.Close()

    return sdkAddr, nil
}
```
