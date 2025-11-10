# eBPF Introspection - Minimal Implementation

This is the minimal working implementation of RFD 013 (eBPF-Based Application
Introspection).

## What's Implemented

### 1. Protobuf Definitions

- Created `proto/coral/mesh/v1/ebpf.proto` with:
    - Collector types (syscall stats, HTTP latency, CPU profile, TCP metrics)
    - Start/Stop collector RPCs
    - Event streaming support
    - Capability detection

### 2. eBPF Manager (`manager.go`)

- Lifecycle management for eBPF collectors
- Automatic expiration and cleanup
- Capability detection
- Thread-safe collector tracking

### 3. Collector Interface (`collector.go`)

- Simple interface for implementing collectors
- Start/Stop lifecycle
- Event retrieval

### 4. Syscall Stats Collector (`syscall_stats.go`)

- **STUB IMPLEMENTATION**: Generates synthetic syscall data for testing
- Demonstrates the collector interface
- Real implementation would use actual eBPF programs

### 5. Capability Detection (`capabilities.go`)

- Detects Linux kernel version
- Checks for BTF support
- Verifies CAP_BPF capability
- Returns available collectors based on system capabilities

### 6. Agent Integration

- eBPF manager integrated into `internal/agent/agent.go`
- Automatic initialization and cleanup
- Exposed via `GetEbpfManager()` method

## Testing

All tests pass:

```bash
make test
```

Run eBPF-specific tests:

```bash
go test ./internal/agent/ebpf/... -v
```

**Note**: On non-Linux systems (macOS, Windows), tests will skip as eBPF
requires Linux.

## Usage Example

```go
import (
"context"
"time"

"github.com/coral-io/coral/internal/agent/ebpf"
meshv1 "github.com/coral-io/coral/coral/mesh/v1"
"google.golang.org/protobuf/types/known/durationpb"
)

// Create manager
manager := ebpf.NewManager(ebpf.Config{Logger: logger})
defer manager.Stop()

// Check capabilities
caps := manager.GetCapabilities()
if !caps.Supported {
// eBPF not available
return
}

// Start collector
resp, err := manager.StartCollector(ctx, &meshv1.StartEbpfCollectorRequest{
AgentId:     "my-agent",
ServiceName: "my-service",
Kind:        meshv1.EbpfCollectorKind_EBPF_COLLECTOR_KIND_SYSCALL_STATS,
Duration:    durationpb.New(60 * time.Second),
})

// Get events
events, err := manager.GetEvents(resp.CollectorId)

// Stop collector
manager.StopCollector(resp.CollectorId)
```

## What's NOT Implemented (Future Work)

This is a **minimal** implementation. The following are NOT implemented:

1. **Actual eBPF Programs**: The syscall stats collector is a stub that
   generates synthetic data. Real implementation needs:
    - CO-RE BPF programs written in C
    - libbpf integration via cgo
    - Proper symbolization
    - Real kernel instrumentation

2. **Colony Integration**: No RPC handlers in colony to receive eBPF events

3. **DuckDB Storage**: No persistence of eBPF data

4. **CLI Commands**: No `coral tap` integration or eBPF-specific commands

5. **Other Collectors**:
    - HTTP latency collector
    - CPU profiler
    - TCP metrics

6. **Advanced Features**:
    - Event streaming (gRPC streaming)
    - AI-driven collector selection
    - Resource limits enforcement
    - Continuous vs on-demand modes

## Platform Support

- **Linux**: Supported (requires kernel 4.1+ for basic features, 5.8+
  recommended)
- **macOS**: Not supported (eBPF is Linux-only)
- **Windows**: Not supported

On non-Linux platforms, the manager will report `Supported: false` in
capabilities.

## Next Steps

To complete RFD 013 implementation:

1. **Phase 1**: Implement real eBPF programs using libbpf
2. **Phase 2**: Add colony RPC handlers and storage
3. **Phase 3**: Integrate with CLI (`coral tap`)
4. **Phase 4**: Add remaining collector types
5. **Phase 5**: Implement AI-driven collector selection

## Files Structure

```
internal/agent/ebpf/
├── README.md              # This file
├── manager.go             # eBPF manager (lifecycle, tracking)
├── manager_test.go        # Manager tests
├── collector.go           # Collector interface
├── syscall_stats.go       # Syscall stats collector (stub)
├── capabilities.go        # System capability detection
└── example_test.go        # Usage examples

proto/coral/mesh/v1/
└── ebpf.proto            # Protobuf definitions

coral/mesh/v1/
└── ebpf.pb.go            # Generated Go code
```

## Testing on Linux

To test on a Linux system:

```bash
# Build
make build

# Run tests (should not skip)
go test ./internal/agent/ebpf/... -v

# The syscall stats collector will generate synthetic data
# even on Linux (since it's a stub implementation)
```

To implement real eBPF collection:

1. Install libbpf-dev
2. Write BPF programs in C
3. Use `go generate` with bpf2go or similar
4. Replace stub collector with real implementation
