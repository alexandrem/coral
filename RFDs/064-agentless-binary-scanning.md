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

### Comparison Table

| Aspect                    | SDK Integration (RFD 060)          | External Scanning (RFD 064)     |
|:--------------------------|:-----------------------------------|:--------------------------------|
| **Code changes**          | Required (import SDK)              | None                            |
| **Binary size**           | +2-5MB (SDK)                       | No change                       |
| **Runtime overhead**      | ~10MB RAM, 1 goroutine             | None                            |
| **Language support**      | Per-language SDK needed            | Works for any compiled language |
| **DWARF requirement**     | Optional (has reflection fallback) | **Required** (no fallback)      |
| **Container access**      | Works in all scenarios             | ⚠️ Challenges in K8s            |
| **Binary updates**        | Auto-detects                       | Needs re-scan logic             |
| **Interpreted languages** | Possible (language-specific)       | ❌ Not possible                  |
| **Security**              | Narrow boundary (metadata only)    | Requires binary read access     |

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

### 1. Binary Discovery

**Challenge: Find binary for a given PID**

```go
// internal/agent/discovery/binary.go
package discovery

import (
    "fmt"
    "os"
    "path/filepath"
)

// GetBinaryPath returns the path to the executable for a given PID.
func GetBinaryPath(pid int) (string, error) {
    // Method 1: Read /proc/<pid>/exe symlink
    exeLink := fmt.Sprintf("/proc/%d/exe", pid)
    binaryPath, err := os.Readlink(exeLink)
    if err != nil {
        return "", fmt.Errorf("failed to read exe symlink: %w", err)
    }

    // Handle "(deleted)" suffix (binary was deleted after process started)
    // Common in containers where binary is on ephemeral layer
    binaryPath = strings.TrimSuffix(binaryPath, " (deleted)")

    return binaryPath, nil
}
```

### 2. Container Namespace Handling

**Challenge: Binary path is in container's mount namespace, not host**

```go
// CopyBinaryFromContainer copies binary from container to host for parsing.
func CopyBinaryFromContainer(pid int) (string, error) {
// 1. Get binary path (in container namespace)
containerPath, err := GetBinaryPath(pid)
if err != nil {
return "", err
}

// 2. Use nsenter to access container's mount namespace
tmpFile := filepath.Join("/tmp", fmt.Sprintf("binary-%d", pid))
cmd := exec.Command(
"nsenter",
"-t", fmt.Sprintf("%d", pid),
"-m",  // Enter mount namespace
"cat", containerPath,
)

output, err := cmd.Output()
if err != nil {
return "", fmt.Errorf("failed to copy binary: %w", err)
}

// 3. Write to temporary file on host
if err := os.WriteFile(tmpFile, output, 0644); err != nil {
return "", err
}

return tmpFile, nil
}
```

**Security Note:** This requires `CAP_SYS_ADMIN` for the agent.

### 3. DWARF Parsing

**Same as SDK approach, but agent does it:**

```go
// internal/agent/discovery/dwarf.go
package discovery

import (
    "debug/dwarf"
    "debug/elf"
    "fmt"
)

type FunctionInfo struct {
    Name   string
    Offset uint64
    File   string
    Line   uint32
}

// ParseDWARF extracts function offsets from binary.
func ParseDWARF(binaryPath string) ([]FunctionInfo, error) {
    // 1. Open ELF file
    elfFile, err := elf.Open(binaryPath)
    if err != nil {
        return nil, fmt.Errorf("failed to open ELF: %w", err)
    }
    defer elfFile.Close()

    // 2. Parse DWARF
    dwarfData, err := elfFile.DWARF()
    if err != nil {
        return nil, fmt.Errorf("no DWARF debug info: %w", err)
    }

    // 3. Extract functions
    var functions []FunctionInfo
    reader := dwarfData.Reader()
    for {
        entry, err := reader.Next()
        if entry == nil || err != nil {
            break
        }

        if entry.Tag == dwarf.TagSubprogram {
            nameAttr := entry.Val(dwarf.AttrName)
            lowPCAttr := entry.Val(dwarf.AttrLowpc)

            if nameAttr != nil && lowPCAttr != nil {
                functions = append(functions, FunctionInfo{
                    Name:   nameAttr.(string),
                    Offset: lowPCAttr.(uint64),
                    // TODO: Extract file, line from DWARF
                })
            }
        }
    }

    return functions, nil
}
```

### 4. Binary Caching

**Challenge: Avoid re-parsing same binary repeatedly**

```go
// internal/agent/discovery/cache.go
package discovery

import (
    "crypto/sha256"
    "encoding/hex"
    "io"
    "os"
    "sync"
)

type BinaryCache struct {
    mu    sync.RWMutex
    cache map[string][]FunctionInfo // hash → functions
}

func NewBinaryCache() *BinaryCache {
    return &BinaryCache{
        cache: make(map[string][]FunctionInfo),
    }
}

// GetFunctions returns cached functions or parses binary if not cached.
func (c *BinaryCache) GetFunctions(binaryPath string) ([]FunctionInfo, error) {
    // 1. Compute hash
    hash, err := hashFile(binaryPath)
    if err != nil {
        return nil, err
    }

    // 2. Check cache
    c.mu.RLock()
    if functions, ok := c.cache[hash]; ok {
        c.mu.RUnlock()
        return functions, nil
    }
    c.mu.RUnlock()

    // 3. Parse DWARF (not cached)
    functions, err := ParseDWARF(binaryPath)
    if err != nil {
        return nil, err
    }

    // 4. Cache result
    c.mu.Lock()
    c.cache[hash] = functions
    c.mu.Unlock()

    return functions, nil
}

func hashFile(path string) (string, error) {
    f, err := os.Open(path)
    if err != nil {
        return "", err
    }
    defer f.Close()

    h := sha256.New()
    if _, err := io.Copy(h, f); err != nil {
        return "", err
    }

    return hex.EncodeToString(h.Sum(nil)), nil
}
```

### 5. Integration with Uprobe Attachment

```go
// internal/agent/debug/uprobe.go (modified)
package debug

// AttachUprobeWithoutSDK attaches uprobe by scanning binary directly.
func (m *DebugSessionManager) AttachUprobeWithoutSDK(
    pid int,
    functionName string,
    duration time.Duration,
) error {
    // 1. Copy binary from container
    binaryPath, err := discovery.CopyBinaryFromContainer(pid)
    if err != nil {
        return fmt.Errorf("failed to copy binary: %w", err)
    }
    defer os.Remove(binaryPath)

    // 2. Get functions from cache or parse DWARF
    functions, err := m.binaryCache.GetFunctions(binaryPath)
    if err != nil {
        return fmt.Errorf("failed to parse binary: %w", err)
    }

    // 3. Find target function
    var offset uint64
    for _, fn := range functions {
        if fn.Name == functionName {
            offset = fn.Offset
            break
        }
    }

    if offset == 0 {
        return fmt.Errorf("function %s not found", functionName)
    }

    // 4. Attach uprobe (same as SDK approach)
    return m.AttachUprobe(pid, binaryPath, offset, sessionID)
}
```

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

```go
// Watch for binary hash changes
func (c *BinaryCache) WatchBinary(pid int) {
ticker := time.NewTicker(30 * time.Second)
defer ticker.Stop()

lastHash := ""
for range ticker.C {
binaryPath, _ := GetBinaryPath(pid)
currentHash, _ := hashFile(binaryPath)

if currentHash != lastHash {
// Binary changed, invalidate cache
c.InvalidateHash(lastHash)
log.Printf("Binary changed for PID %d, re-scanning", pid)
lastHash = currentHash
}
}
}
```

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

### Discovery Flow (Auto Mode)

```go
func (a *Agent) DiscoverFunctions(serviceName string, pid int) ([]FunctionInfo, error) {
// 1. Try SDK first (if available)
if a.config.Discovery.Method == "auto" || a.config.Discovery.Method == "sdk" {
sdkAddr := a.discoverSDK(pid)
if sdkAddr != "" {
functions, err := a.querySDK(sdkAddr)
if err == nil {
log.Printf("Discovered %d functions via SDK", len(functions))
return functions, nil
}
log.Printf("SDK query failed: %v, falling back to binary scan", err)
}
}

// 2. Fallback to binary scanning
if a.config.Discovery.Method == "auto" || a.config.Discovery.Method == "binary" {
if !a.config.Discovery.BinaryScanning.Enabled {
return nil, fmt.Errorf("binary scanning disabled")
}

binaryPath, err := CopyBinaryFromContainer(pid)
if err != nil {
return nil, fmt.Errorf("failed to copy binary: %w", err)
}
defer os.Remove(binaryPath)

functions, err := a.binaryCache.GetFunctions(binaryPath)
if err != nil {
return nil, fmt.Errorf("failed to parse binary: %w", err)
}

log.Printf("Discovered %d functions via binary scan", len(functions))
return functions, nil
}

return nil, fmt.Errorf("no discovery method succeeded")
}
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

### Integration Tests

```go
func TestContainerBinaryAccess(t *testing.T) {
// Start test container
containerID := startTestContainer("test-app")
defer stopContainer(containerID)

// Get PID
pid := getContainerPID(containerID)

// Copy binary
binaryPath, err := CopyBinaryFromContainer(pid)
assert.NoError(t, err)
defer os.Remove(binaryPath)

// Verify binary is valid ELF
elfFile, err := elf.Open(binaryPath)
assert.NoError(t, err)
elfFile.Close()
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
