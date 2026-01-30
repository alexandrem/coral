// cpu_profile.bpf.c
// eBPF program for CPU profiling via perf_event sampling.

#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>

#define MAX_STACK_DEPTH 127
#define STACK_STORAGE_SIZE 16384

// Stack trace storage.
// BPF_MAP_TYPE_STACK_TRACE stores arrays of instruction pointers.
// Note: key_size and value_size must be explicit for stack trace maps.
struct {
    __uint(type, BPF_MAP_TYPE_STACK_TRACE);
    __uint(key_size, sizeof(__u32));
    __uint(value_size, MAX_STACK_DEPTH * sizeof(__u64));
    __uint(max_entries, STACK_STORAGE_SIZE);
} stack_traces SEC(".maps");

// Key for stack_counts map.
// Combines PID, user stack ID, and kernel stack ID.
struct stack_key {
    __u32 pid;
    __s32 user_stack_id;
    __s32 kernel_stack_id;
};

// Stack sample counts.
// Tracks how many times each unique stack combination was sampled.
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, struct stack_key);
    __type(value, __u64);
    __uint(max_entries, 10240);
} stack_counts SEC(".maps");

// Perf event handler.
// Called at sampling frequency (e.g., 99Hz) when CPU is running.
SEC("perf_event")
int profile_cpu(void *ctx) {
    __u64 pid_tgid = bpf_get_current_pid_tgid();
    __u32 pid = pid_tgid >> 32;

    // No PID filtering needed: perf events are attached to specific threads of
    // the target process, so only those threads trigger this BPF program.
    // Note: bpf_get_current_pid_tgid() returns init-namespace PIDs which differ
    // from container-namespace PIDs, making PID filtering unreliable in containers.

    // Capture user and kernel stack traces.
    struct stack_key key = {};
    key.pid = pid;
    key.user_stack_id = bpf_get_stackid(ctx, &stack_traces, BPF_F_USER_STACK);
    key.kernel_stack_id = bpf_get_stackid(ctx, &stack_traces, 0);

    // Increment count for this stack combination.
    __u64 *count = bpf_map_lookup_elem(&stack_counts, &key);
    if (count) {
        __sync_fetch_and_add(count, 1);
    } else {
        __u64 init_val = 1;
        bpf_map_update_elem(&stack_counts, &key, &init_val, BPF_NOEXIST);
    }

    return 0;
}

char LICENSE[] SEC("license") = "GPL";
