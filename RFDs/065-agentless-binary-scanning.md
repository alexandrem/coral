---
rfd: "065"
title: "Agentless Binary Scanning for Uprobe Discovery"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "059", "061", "064" ]
database_migrations: [ ]
areas: [ "agent", "ebpf", "discovery" ]
---

# RFD 065 - Agentless Binary Scanning for Uprobe Discovery

**Status:** 🎉 Implemented

## Summary

Enable uprobe debugging without SDK integration by having the Coral Agent
directly
scan target process binaries to extract function offsets. This provides a
fallback mechanism for applications that cannot or will not integrate the Coral
SDK, leveraging DWARF debug information and runtime pprof endpoints.

## Problem

### Current behavior/limitations

The initial approach to function discovery (RFD 060) requires:

- Application to integrate `coral-go` SDK.
- SDK to parse its own binary and expose a gRPC API.
- Agent to query the SDK for function offsets.

### Why this matters

This approach has several critical limitations:

- **Code modifications**: Users must import the SDK and call
  `EnableRuntimeMonitoring()`, which is a barrier to adoption.
- **Legacy applications**: Rebuilding and redeploying older applications is
  often impossible or undesirable.
- **Binary bloat**: The SDK adds ~2-5MB to the binary size.
- **Runtime overhead**: Adds a background goroutine for metadata serving.

### Use cases affected

- **Third-party applications**: Debugging binaries where source code is
  unavailable.
- **Minimalist deployments**: Environments where additional dependencies are
  strictly controlled.
- **Multi-language support**: Providing immediate value to non-Go applications (
  Rust, C/C++) before specific SDKs are ready.

## Solution

The Coral Agent will directly discover and scan target binaries to extract
function offsets as a fallback or alternative to the SDK.

**Key Design Decisions:**

- **Tiered Discovery**: Use SDK discovery first, falling back to pprof
  endpoints, then binary scanning.
- **Namespace Awareness**: Use `nsenter` to traverse container boundaries and
  access binaries.
- **Hashing & Cache**: Use content hashes for caching results to avoid redundant
  scanning overhead.

**Benefits:**

- Zero-config debugging for compiled languages.
- Works with legacy or unmodifiable binaries.
- No runtime overhead within the target application.

### Discovery Strategy Priority

| Priority | Method                     | Pros                                     | Cons                            |
| :------- | :------------------------- | :--------------------------------------- | :------------------------------ |
| 1        | **SDK** (RFD 060)          | Precise, works with stripped binaries    | Requires code changes           |
| 2        | **pprof** (This RFD)       | Works with stripped binaries, HTTP-based | Only discovery called functions |
| 3        | **Binary Scan** (This RFD) | Complete coverage, no network dependency | Requires DWARF symbols          |

### Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│ Kubernetes Node                                             │
│                                                             │
│  ┌────────────────────────────────────────────────────────┐ │
│  │ Coral Agent (DaemonSet)                                │ │
│  │                                                        │ │
│  │  1. Discover target pods via K8s API                  │ │
│  │  2. For each pod:                                     │ │
│  │     - Get PID via CRI                                 │ │
│  │     - Read /proc/<pid>/exe (via nsenter)              │ │
│  │     - Parse DWARF/ELF → extract offsets               │ │
│  │     - Cache results by Binary Hash                    │ │
│  │  3. Attach uprobe to /proc/<pid>/exe:<offset>         │ │
│  └────────────────────────────────────────────────────────┘ │
│                           ▲                                 │
│                           │                                 │
│  ┌────────────────────────┼────────────────────────────────┐ │
│  │ Target Pod             │                                │ │
│  │                        │                                │ │
│  │  Container             │                                │ │
│  │  ├─ PID: 1234          │                                │ │
│  │  ├─ Binary: /app/myapp ◄──────── Agent reads via procfs  │ │
│  └────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
```

### Component Changes

1. **Coral Agent**:

   - **Discovery Service**: Refactored to coordinate between SDK, pprof, and
     binary scanning providers.
   - **ProcFS Tracker**: Monitors container namespaces to locate binaries.
   - **DWARF Parser**: Shared logic to extract `STT_FUNC` symbols and DWARF
     low_pc/high_pc ranges.

2. **SDK (shared pkg)**:
   - `pkg/sdk/debug`: Extracted parser logic from the SDK to be reusable by the
     Agent.

## Implementation Plan

### Phase 1: Foundation & Shared Logic (COMPLETED)

- [x] Extract DWARF/ELF parsing logic to `pkg/sdk/debug`.
- [x] Implement Mach-O support for macOS dev environments.
- [x] Add `nsenter` wrappers for namespace traversal.

### Phase 2: Agent Integration (COMPLETED)

- [x] Implement `BinaryDiscoveryProvider` in the Agent.
- [x] Add in-memory LRU cache for function metadata.
- [x] Integrate with existing uprobe attachment flow.

## API Changes

### Protobuf Updates

No new protobuf messages were required for the initial implementation as the
metadata is consumed internally by the Agent.

### Configuration Changes

```yaml
agent:
    discovery:
        method: "auto" # sdk | binary | pprof | auto
        binary_scanning:
            enabled: true
            access_method: "nsenter" # nsenter | cri
            cache_ttl: 1h
```

## Testing Strategy

### Unit Tests

- `pkg/sdk/debug`: Validate correct offset extraction from sample Go and C
  binaries (stripped vs non-stripped).
- `internal/agent/discovery`: Mock ProcFS to test PID-to-binary resolution.

### Integration Tests

- Verify `nsenter` capability in a privileged container environment.
- Test fallback flow: disable SDK and verify agent correctly attaches uprobe via
  binary scan.

## Security Considerations

- **Permissions**: Agent requires `CAP_SYS_ADMIN` to use `nsenter` and read
  `/proc/<pid>/exe`.
- **Isolation**: Agent only accesses binaries of processes it is authorized to
  monitor via K8s RBAC.
- **Sensitive Data**: Binary scanning avoids reading application memory, only
  static code sections.

## Implementation Status

**Core Capability:** ✅ Complete

Scanning of compiled binaries (Go, Rust, C++) for function offsets is fully
functional and integrated as a fallback in the `DiscoveryService`.

**Operational Components:**

- ✅ `/proc/<pid>/exe` discovery and resolution
- ✅ `nsenter` mount namespace traversal
- ✅ DWARF/ELF symbol extraction
- ✅ Offset metadata caching

**What Works Now:**

- Automatic uprobe attachment for Go binaries without the Coral SDK.
- Fallback from SDK to Binary Scanning if gRPC port is not found.
- Support for both Linux ELF and macOS Mach-O binaries.

## Future Work

**pprof Discovery** (Deferred)

- Implement `PProfDiscoveryProvider` to query `:6060/debug/pprof/profile`.
- Add auto-detection for common pprof ports.

**Enhanced Scanning** (Future - RFD 088)

- Support for split-debug symbols (separate `.debug` files).
- Support for compressed DWARF (.zdebug).

**Dynamic Languages** (Deferred)

- Python/Node.js support via runtime-specific introspection tools.
