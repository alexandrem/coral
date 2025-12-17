# CPU Profiling E2E Test

This directory contains an end-to-end test for the CPU profiling feature using
Docker Compose.

## Overview

The test validates the complete CPU profiling workflow:

1. Agent running in Linux container with required capabilities
2. Colony orchestrating the profiling request
3. BPF-based stack trace collection
4. Profile data returned in folded format

## Prerequisites

- Docker and Docker Compose installed
- Coral binary built (`make build`)
- Colony credentials configured (CORAL_COLONY_ID and CORAL_COLONY_SECRET)

## Running the Test

### Quick Start

**Prerequisites**:
- Colony and discovery running on host
- export `CORAL_COLONY_ID` and `CORAL_COLONY_SECRET` env vars

```bash
# Start services
docker compose up -d

# Run the E2E test (default: tests 'cpu-app' service)
./test_cpu_profile.sh

# Test specific service
./test_cpu_profile.sh cpu-app
```

### Manual Testing

```bash
# Start services
docker compose up -d

# Wait for agent to be ready
docker compose logs -f coral-agent-cpu

# Run CPU profiling (from repository root)
bin/coral debug cpu-profile -s cpu-app -d 5 --frequency 99
```

## Test Architecture

```
┌─────────────┐         ┌─────────────┐         ┌─────────────┐
│   CLI       │────────▶│   Colony    │────────▶│   Agent     │
│  (macOS)    │  gRPC   │  (remote)   │  gRPC   │  (Linux)    │
└─────────────┘         └─────────────┘         └─────────────┘
                                                        │
                                                        ▼
                                                   ┌─────────┐
                                                   │   BPF   │
                                                   │ Profiler│
                                                   └─────────┘
                                                        │
                                                        ▼
                                                  ┌──────────┐
                                                  │  Target  │
                                                  │  Process │
                                                  │ (nginx)  │
                                                  └──────────┘
```

## Docker Compose Setup

The test uses the existing docker-compose.yml with:

- **cpu-app**: cpu intensive app
- **coral-agent**: Coral agent with required capabilities:
    - `SYS_ADMIN`: Required for eBPF operations
    - `SYS_PTRACE`: Required for process attachment
    - `SYS_RESOURCE`: Required for memlock rlimit
    - `BPF`: Required for BPF operations
    - `NET_ADMIN`: Required for WireGuard mesh
    - `CAP_SYSLOG`: Required to read kernel symbols for cpu-profile
- **PID namespace sharing**: Agent shares PID namespace with demo-app

## Expected Output

```
Profiling CPU for service 'cpu-app' (5s at 99Hz)...
Total samples: 370
Unique stacks: 207

[kernel] 0xffffbeb59d881648;[kernel] 0xffffbeb59eece080;[kernel] 0xffffbeb59eecce40;[kernel] 0xffffbeb59d8958e4;[kernel] 0xffffbeb59dc31940;[kernel] 0xffffbeb59d8b4934;[kernel] 0xffffbeb59d8b65a0;runtime.goexit;net/http.(*Server).Serve.gowrap3;net/http.(*conn).serve;net/http.(*response).finishRequest;bufio.(*Writer).Flush;net/http.(*chunkWriter).Write;net/http.Header.writeSubset 1
[kernel] 0xffffbeb59d881648;[kernel] 0xffffbeb59eece110;[kernel] 0xffffbeb59eecd93c;[kernel] 0xffffbeb59d8a2bb8;[kernel] 0xffffbeb59d8a2b64;[kernel] 0xffffbeb59d8a299c;[kernel] 0xffffbeb59eb8ae34;[kernel] 0xffffbeb59eb8ad44;[kernel] 0xffffbeb59eb8ac3c;[kernel] 0xffffbeb59ee67098;[kernel] 0xffffbeb59ecb6380;[kernel] 0xffffbeb59dd64ad4;[kernel] 0xffffbeb59dd6150c;runtime.goexit;runtime.main;main.main;net/http.(*Server).ListenAndServe;net/http.(*Server).Serve;net/http.(*onceCloseListener).Accept;net.(*TCPListener).Accept;net.(*TCPListener).accept;net.(*netFD).accept;internal/poll.(*FD).Accept;internal/poll.accept;syscall.Accept4;syscall.accept4;syscall.Syscall6;internal/runtime/syscall.Syscall6 1
...
```

## Troubleshooting

### Agent Not Starting

Check logs:

```bash
docker compose logs coral-agent-cpu
```

Common issues:

- Missing CORAL_COLONY_ID or CORAL_COLONY_SECRET
- Insufficient capabilities
- Port conflicts

### BPF Errors

If you see "invalid argument" or BPF loading errors:

- Ensure running on Linux kernel 5.8+
- Check that capabilities are properly set
- Verify BPF is enabled: `cat /proc/sys/kernel/unprivileged_bpf_disabled`

### No Samples Captured

This can be normal if the target process (nginx) is idle. To generate load:

```bash
# Generate HTTP requests
for i in {1..5000}; do
  curl http://localhost:8081 &
done
wait
```

## Integration with CI

The test can be run in CI using Docker:

```yaml
# Example GitHub Actions workflow
-   name: Run CPU Profile E2E Test
    run: |
        cd examples/docker-compose
        docker compose up -d
        ./test_cpu_profile.sh
    env:
        CORAL_COLONY_ID: ${{ secrets.CORAL_COLONY_ID }}
        CORAL_COLONY_SECRET: ${{ secrets.CORAL_COLONY_SECRET }}
```

## Generating Flame Graphs

To visualize the CPU profile:

```bash
# Generate flame graph
bin/coral debug cpu-profile -s cpu-app -d 30 | scripts/flamegraph.pl > cpu.svg

# Open in browser
open cpu.svg
```
