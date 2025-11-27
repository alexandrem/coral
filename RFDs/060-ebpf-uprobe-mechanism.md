---
rfd: "060"
title: "eBPF Uprobe Mechanism"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "058", "059" ]
database_migrations: [ ]
areas: [ "agent", "ebpf", "linux" ]
---

# RFD 060 - eBPF Uprobe Mechanism

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
