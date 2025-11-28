---
rfd: "064"
title: "Agentless Binary Scanning for Uprobe Discovery"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: false
dependencies: [ "059", "061" ]
database_migrations: [ ]
areas: [ "agent", "ebpf", "discovery" ]
---

# RFD 064 - Agentless Binary Scanning for Uprobe Discovery

**Status:** 🚧 Draft

## Summary

Enable uprobe debugging without SDK integration by having the agent directly
scan target process binaries to extract function offsets. This provides a
fallback mechanism for applications that cannot or will not integrate the Coral
SDK.

## Problem

**Current approach (RFD 060):**

- Requires application to integrate coral-go SDK
- SDK parses its own binary and exposes gRPC API
- Agent queries SDK for function offsets

**Limitations:**

- ❌ Code changes required (import SDK, call EnableRuntimeMonitoring)
- ❌ Not viable for legacy applications
- ❌ Increases binary size (~2-5MB for SDK)
- ❌ Adds runtime overhead (background goroutine)

**Use cases blocked:**

- Debugging third-party applications (can't modify source)
- Legacy applications (no rebuild possible)
- Minimal-overhead deployments (no SDK dependencies)
- Multi-language support (SDK doesn't exist yet for Python/Node.js)

## Solution

**Agent directly scans target binary** to extract function offsets:

1. Agent discovers process PID (via container runtime, K8s API)
2. Agent reads binary from `/proc/<pid>/exe`
3. Agent parses DWARF debug info
4. Agent extracts function offsets
5. Agent attaches uprobes using discovered offsets

**This is a fallback mechanism** - SDK integration (RFD 060) is still preferred
when possible.

### Alternative: pprof-Based Discovery

Many Go applications already expose `net/http/pprof` for profiling. We can leverage this for zero-config function discovery:

```go
import _ "net/http/pprof"  // Many apps already have this!

http.ListenAndServe(":6060", nil)
```

**Agent can extract function information from pprof endpoints:**

1. **Discovery**: Agent finds pprof HTTP endpoint (port 6060, or via service annotation)
2. **Profile fetch**: GET `/debug/pprof/allocs` or `/debug/pprof/profile?seconds=1`
3. **Parse profile**: Extract function names + PC addresses from protobuf
4. **Attach uprobes**: Use extracted addresses

**Benefits over binary scanning:**
- ✅ **Works with stripped binaries** (uses runtime reflection, no DWARF needed)
- ✅ **No filesystem access** (HTTP-based)
- ✅ **No namespace complications** (network call, not filesystem)
- ✅ **Already enabled** in many apps (especially in dev/staging)

**Limitations:**
- ⚠️ **Incomplete coverage**: Only functions that have been called (CPU profile) or allocated (heap profile)
- ⚠️ **Security**: pprof exposes sensitive data (heap dumps, goroutine stacks)
- ⚠️ **Not always accessible**: Production apps often disable pprof or firewall it

**Use case:** Quick function discovery for apps that already expose pprof, without needing DWARF symbols or SDK integration.

### Discovery Strategy Priority

```
Priority 1: SDK (RFD 060)
  ├─ Best: Complete coverage, auto-updates, works everywhere
  └─ Requires: Code changes (import SDK)
       ↓ fallback
Priority 2: pprof endpoints (THIS RFD)
  ├─ Good: No code changes (if already enabled), works with stripped binaries
  ├─ Limitation: Incomplete coverage (only called functions)
  └─ Requires: pprof exposed, network access
       ↓ fallback
Priority 3: Binary DWARF scanning (THIS RFD)
  ├─ Good: Complete coverage, no network dependency
  ├─ Limitation: Requires DWARF symbols
  └─ Requires: Filesystem access, CAP_SYS_ADMIN
       ↓ fallback
Priority 4: Fail + suggest SDK integration
```

### Comparison Table

| Aspect | SDK (RFD 060) | pprof Discovery | Binary Scan (DWARF) |
|:-------|:--------------|:----------------|:--------------------|
| **Code changes** | Required | None (if already enabled) | None |
| **Binary size** | +2-5MB | No change | No change |
| **Runtime overhead** | ~10MB RAM | Profile fetch (~1s) | None |
| **Stripped binaries** | ✅ Works (reflection) | ✅ Works (runtime) | ❌ Fails (needs DWARF) |
| **Function coverage** | ✅ All functions | ⚠️ Only called functions | ✅ All functions (DWARF) |
| **Container access** | ✅ Always works | ✅ HTTP (network) | ⚠️ nsenter (filesystem) |
| **Binary updates** | ✅ Auto-detects | ⚠️ Needs re-profile | ⚠️ Needs re-scan |
| **Network access** | gRPC (agent→app) | HTTP (agent→app) | None needed |
| **Permissions** | None | None | CAP_SYS_ADMIN |
| **Security** | ✅ Narrow (metadata) | ⚠️ Sensitive (heap/stack) | ⚠️ Broad (binary access) |
| **Availability** | Rare (new apps) | Common (many apps) | Always (filesystem) |

## Architecture

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
│  │     - Read /proc/<pid>/exe (with nsenter if needed)   │ │
│  │     - Parse DWARF → extract function offsets          │ │
│  │     - Cache offsets for this binary                   │ │
│  │  3. On uprobe request:                                │ │
│  │     - Lookup cached offsets                           │ │
│  │     - Attach uprobe to /proc/<pid>/exe:offset         │ │
│  └────────────────────────────────────────────────────────┘ │
│                           ▲                                 │
│                           │                                 │
│  ┌────────────────────────┼────────────────────────────────┐ │
│  │ Target Pod             │                                │ │
│  │                        │                                │ │
│  │  Container             │                                │ │
│  │  ├─ PID: 1234          │                                │ │
│  │  ├─ Binary: /app/myapp │◄──────── Agent reads this      │ │
│  │  └─ No SDK required ✓  │                                │ │
│  └────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
```

## Implementation

The implementation involves several key components to enable agentless binary
scanning.

### 1. Binary Discovery

The agent needs to locate the executable for a given PID. This is achieved by
reading the `/proc/<pid>/exe` symlink. Special handling is required for deleted
binaries (common in containers).

### 2. Container Namespace Handling

Since the agent runs in a different mount namespace than the target container,
it cannot directly access the binary path found in step 1. We will use `nsenter`
to enter the target container's mount namespace and copy the binary to a
temporary location on the host for analysis.

### 3. DWARF Parsing

Once the binary is available on the host, the agent parses its ELF headers to
locate the DWARF debug information. It iterates through the DWARF entries to
extract function names and their memory offsets (LowPC).

### 4. Binary Caching

To avoid the overhead of copying and parsing the binary for every uprobe
attachment, we will implement a cache. The cache will use the binary's content
hash as a key and store the extracted function offsets.

### 5. Integration with Uprobe Attachment

The existing uprobe attachment logic will be extended. If SDK discovery fails (
or is disabled), the agent will fallback to this binary scanning method to find
the offset for the requested function, and then proceed with the standard eBPF
uprobe attachment.

(See **Appendix** for prototype code)

## Challenges & Solutions

### Challenge 1: Container Filesystem Access (K8s)

**Problem:** Agent (DaemonSet) and app (Pod) are in different mount namespaces.

**Solutions:**

**Option A: nsenter (Recommended)**

```bash
# Agent enters container's mount namespace to read binary
nsenter -t <pid> -m cat /proc/self/exe > /tmp/binary
```

- ✅ Works reliably
- ⚠️ Requires `CAP_SYS_ADMIN` for agent

**Option B: CRI (Container Runtime Interface)**

```bash
# Use CRI to copy file from container
crictl cp <container-id>:/app/myapp /tmp/binary
```

- ✅ Works without CAP_SYS_ADMIN
- ⚠️ Depends on CRI implementation (Docker, containerd, CRI-O)

**Option C: Shared HostPath volume**

```yaml
# Mount binaries to known host path
volumes:
    -   name: app-binaries
        hostPath:
            path: /var/lib/coral/binaries
```

- ✅ Simple, no special permissions
- ⚠️ Requires application deployment changes

### Challenge 2: Binary Updates (Rolling Deployments)

**Problem:** Binary changes during rolling update, cached offsets become stale.

**Solution: Watch for binary changes**

### Challenge 3: Stripped Binaries

**Problem:** Binary built with `-ldflags="-w"` (no DWARF).

**Solution: Fail gracefully, suggest SDK**

```go
functions, err := ParseDWARF(binaryPath)
if err != nil {
    return fmt.Errorf(`
    Binary has no DWARF debug symbols.

    To enable uprobe debugging:
      Option 1: Rebuild with debug symbols
        go build -ldflags="-s" (keep DWARF, strip symbol table)

      Option 2: Integrate Coral SDK
        import "github.com/coral-io/coral-go"
        coral.EnableRuntimeMonitoring()

    Error: %w`, err)
}
```

### Challenge 4: Interpreted Languages (Python, Node.js)

**Problem:** No binary to scan (e.g., `python app.py`).

**Solution: Not supported in V1**

For V1, external scanning only works for compiled languages:

- ✅ Go
- ✅ Rust
- ✅ C/C++
- ❌ Python (need SDK or different approach)
- ❌ Node.js (need SDK or different approach)

Future: Could use language-specific mechanisms (py-spy for Python, V8 API for
Node.js).

## When to Use Which Approach

### Use SDK Integration (RFD 060) when:

- ✅ You control the application source code
- ✅ You want best reliability (works in all scenarios)
- ✅ You need interpreted language support (Python, Node.js)
- ✅ Binary is stripped (SDK has reflection fallback)
- ✅ You want auto-update detection

### Use External Scanning (RFD 064) when:

- ✅ You can't modify application code (third-party app)
- ✅ You want zero-overhead (no SDK dependency)
- ✅ Application is compiled (Go, Rust, C++)
- ✅ Binary has DWARF symbols
- ✅ Agent has necessary permissions (CAP_SYS_ADMIN or CRI access)

## Configuration

### Agent Configuration

```yaml
# agent-config.yaml
agent:
    discovery:
        # Method: "sdk" (query SDK), "binary" (scan binary), "auto" (try SDK first, fallback to binary)
        method: "auto"

        binary_scanning:
            enabled: true

            # How to access container binaries
            access_method: "nsenter"  # "nsenter", "cri", "hostpath"

            # Caching
            cache:
                enabled: true
                ttl: 1h
                max_binaries: 100

            # Binary watching
            watch:
                enabled: true
                check_interval: 30s

            # Fallback to SDK if binary scan fails
            fallback_to_sdk: true
```

## Security Considerations

### Required Permissions

**For nsenter approach:**

- `CAP_SYS_ADMIN` - Required to enter container namespaces
- Read access to `/proc/<pid>/exe`

**For CRI approach:**

- No special capabilities
- CRI socket access (usually `/var/run/containerd/containerd.sock`)

### Security Risks

1. **Binary access**: Agent can read any container's binary
    - Mitigation: RBAC to limit which pods agent can scan

2. **Namespace escape**: `nsenter` is powerful, could be misused
    - Mitigation: Audit all nsenter usage, limit to specific namespaces

3. **Cache poisoning**: Attacker could replace binary, agent uses old cached
   offsets
    - Mitigation: Hash-based invalidation, periodic re-scan

## Testing Strategy

### Unit Tests

```go
func TestDWARFParsing(t *testing.T) {
    // Test with various Go binaries
    testCases := []string{
        "testdata/simple.bin",     // Basic Go binary
        "testdata/stripped-s.bin", // Built with -ldflags="-s"
        "testdata/stripped-w.bin", // Built with -ldflags="-w" (should fail)
        "testdata/optimized.bin", // Built with -gcflags="-N -l"
    }

    for _, binary := range testCases {
        functions, err := ParseDWARF(binary)

        if strings.Contains(binary, "stripped-w") {
            assert.Error(t, err, "Should fail on stripped DWARF")
        } else {
            assert.NoError(t, err)
            assert.Greater(t, len(functions), 0)
        }
    }
}
```

## Migration Path

**Phase 1: SDK-only (Current)**

- RFD 060 implementation
- Requires SDK integration

**Phase 2: External scanning (V1)**

- Implement binary discovery and DWARF parsing
- Add auto-discovery mode (SDK → binary fallback)
- Go-only support

**Phase 3: Multi-language (V2)**

- Add Python support (py-spy integration)
- Add Node.js support (V8 API integration)

## Recommendation

Implement **both approaches** with auto-fallback:

1. **Default: Try SDK first**
    - Best reliability, works everywhere
    - Handles stripped binaries, interpreted languages

2. **Fallback: Binary scanning**
    - Automatically used when SDK unavailable
    - Works for legacy/third-party apps

3. **User override: Force binary scanning**
    - For zero-overhead deployments
    - When SDK integration not desired

This provides maximum flexibility while maintaining a great default UX.

## Dependencies

- **RFD 059**: Live Debugging Architecture
- **RFD 061**: eBPF Uprobe Mechanism

## References

- Linux namespaces: https://man7.org/linux/man-pages/man7/namespaces.7.html
- nsenter: https://man7.org/linux/man-pages/man1/nsenter.1.html
- DWARF debugging format: http://dwarfstd.org/
- ELF format: https://en.wikipedia.org/wiki/Executable_and_Linkable_Format
