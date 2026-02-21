//go:build ignore

#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>

#define bpf_printk(fmt, ...) \
    ({ \
        char ____fmt[] = fmt; \
        bpf_trace_printk(____fmt, sizeof(____fmt), \
                         ##__VA_ARGS__); \
    })

static long (*bpf_trace_printk)(const char *fmt, __u32 fmt_size, ...) = (void *) 6;


// Event structure matching Go struct
struct uprobe_event {
    __u64 timestamp_ns;
    __u32 pid;
    __u32 tid;
    __u8  event_type;  // 0=entry, 1=return
    __u64 duration_ns;
};

// filter_config holds runtime-configurable filter criteria (RFD 090).
// All fields default to zero = no filter applied, preserving backward compatibility.
struct filter_config {
    __u64 min_duration_ns;  // Drop return events shorter than this. 0 = no minimum.
    __u64 max_duration_ns;  // Drop return events longer than this. 0 = no maximum.
    __u32 sample_rate;      // Emit 1 in every N events. 0 or 1 = emit all.
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

// filter_config BPF ARRAY map (single entry, key 0).
// Userspace writes filter parameters here; eBPF reads on each event.
// No reload or reattach required for updates (RFD 090).
struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __type(key, __u32);
    __type(value, struct filter_config);
    __uint(max_entries, 1);
} filter_config_map SEC(".maps");

// sample_counter per-CPU ARRAY map for rate sampling.
// Each CPU independently counts and emits every Nth event,
// avoiding lock contention on high-rate paths (RFD 090).
struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
    __type(key, __u32);
    __type(value, __u64);
    __uint(max_entries, 1);
} sample_counter SEC(".maps");

// Uprobe handler - called on function entry
SEC("uprobe/function_entry")
int uprobe_entry(struct pt_regs *ctx) {
    __u64 pid_tid = bpf_get_current_pid_tgid();
    __u32 pid = pid_tid >> 32;
    __u32 tid = (__u32)pid_tid;

    __u64 ts = bpf_ktime_get_ns();

    // Store entry timestamp for duration calculation
    bpf_map_update_elem(&entry_times, &pid_tid, &ts, BPF_ANY);

    bpf_printk("uprobe_entry: pid=%d tid=%d\n", pid, tid);

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

    // Calculate duration (RFD 090: fix — was always emitting zero before)
    __u64 *entry_ts = bpf_map_lookup_elem(&entry_times, &pid_tid);
    __u64 duration = 0;
    if (entry_ts) {
        duration = ts - *entry_ts;
        bpf_map_delete_elem(&entry_times, &pid_tid);
    }

    // Read filter config (key 0)
    __u32 filter_key = 0;
    struct filter_config *cfg = bpf_map_lookup_elem(&filter_config_map, &filter_key);

    if (cfg) {
        // Apply duration filter: drop events that don't fall in [min, max]
        if (cfg->min_duration_ns > 0 && duration < cfg->min_duration_ns) {
            return 0;  // Too fast — drop before ring buffer copy
        }
        if (cfg->max_duration_ns > 0 && duration > cfg->max_duration_ns) {
            return 0;  // Too slow — drop before ring buffer copy
        }

        // Apply sample rate filter: emit 1 in every N events
        __u32 rate = cfg->sample_rate;
        if (rate > 1) {
            __u64 *counter = bpf_map_lookup_elem(&sample_counter, &filter_key);
            if (counter) {
                __u64 count = *counter + 1;
                *counter = count;
                if (count % rate != 0) {
                    return 0;  // Not our turn — drop
                }
            }
        }
    }

    bpf_printk("uprobe_return: pid=%d tid=%d duration=%llu\n", pid, tid, duration);

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
