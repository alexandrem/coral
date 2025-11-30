// uprobe_monitor.bpf.c
// eBPF program for function entry/exit tracing

#include "vmlinux.h"
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

// BPF map: Ring buffer for sending events to userspace
struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 256 * 1024); // 256KB
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

    // Reserve space in ring buffer
    struct uprobe_event *event;
    event = bpf_ringbuf_reserve(&events, sizeof(*event), 0);
    if (!event) {
        return 0;
    }

    // Build event
    event->timestamp = end_ts;
    event->pid = pid_tgid >> 32;
    event->tid = (__u32)pid_tgid;
    event->duration_ns = duration;

    // Submit event to userspace
    bpf_ringbuf_submit(event, 0);

    // Clean up entry timestamp
    bpf_map_delete_elem(&start_times, &pid_tgid);

    return 0;
}

char LICENSE[] SEC("license") = "GPL";
