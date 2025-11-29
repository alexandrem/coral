//go:build ignore

#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>


// Event structure matching Go struct
struct uprobe_event {
    __u64 timestamp_ns;
    __u32 pid;
    __u32 tid;
    __u8  event_type;  // 0=entry, 1=return
    __u64 duration_ns;
};

// Ring buffer for streaming events to userspace
struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 256 * 1024);  // 256KB ring buffer
} events SEC(".maps");

// Hash map to track function entry timestamps per thread
// Key: thread ID (TID), Value: entry timestamp
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u64);    // tid
    __type(value, __u64);  // entry timestamp
    __uint(max_entries, 1024);
} entry_times SEC(".maps");

// Uprobe handler - called on function entry
SEC("uprobe/function_entry")
int uprobe_entry(struct pt_regs *ctx) {
    __u64 pid_tid = bpf_get_current_pid_tgid();
    __u32 pid = pid_tid >> 32;
    __u32 tid = (__u32)pid_tid;

    __u64 ts = bpf_ktime_get_ns();

    // Store entry timestamp for duration calculation
    bpf_map_update_elem(&entry_times, &pid_tid, &ts, BPF_ANY);

    // Reserve space in ring buffer
    struct uprobe_event *event;
    event = bpf_ringbuf_reserve(&events, sizeof(*event), 0);
    if (!event) {
        return 0;  // Ring buffer full, drop event
    }

    // Populate event
    event->timestamp_ns = ts;
    event->pid = pid;
    event->tid = tid;
    event->event_type = 0;  // entry
    event->duration_ns = 0;

    // Submit event to ring buffer
    bpf_ringbuf_submit(event, 0);
    return 0;
}

// Uretprobe handler - called on function return
SEC("uretprobe/function_return")
int uprobe_return(struct pt_regs *ctx) {
    __u64 pid_tid = bpf_get_current_pid_tgid();
    __u32 pid = pid_tid >> 32;
    __u32 tid = (__u32)pid_tid;

    __u64 ts = bpf_ktime_get_ns();

    // Calculate duration
    __u64 *entry_ts = bpf_map_lookup_elem(&entry_times, &pid_tid);
    __u64 duration = 0;
    if (entry_ts) {
        duration = ts - *entry_ts;
        bpf_map_delete_elem(&entry_times, &pid_tid);
    }

    // Reserve space in ring buffer
    struct uprobe_event *event;
    event = bpf_ringbuf_reserve(&events, sizeof(*event), 0);
    if (!event) {
        return 0;  // Ring buffer full, drop event
    }

    // Populate event
    event->timestamp_ns = ts;
    event->pid = pid;
    event->tid = tid;
    event->event_type = 1;  // return
    event->duration_ns = duration;

    // Submit event to ring buffer
    bpf_ringbuf_submit(event, 0);
    return 0;
}

char LICENSE[] SEC("license") = "GPL";
