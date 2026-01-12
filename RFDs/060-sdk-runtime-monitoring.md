---
rfd: "060"
title: "SDK Runtime Monitoring"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "059" ]
database_migrations: [ ]
areas: [ "sdk", "go", "debugging" ]
---

# RFD 060 - SDK Runtime Monitoring

**Status:** 🎉 Implemented

## Summary

This RFD details the **Application-side** implementation of the live debugging
system. It defines the `coral-go` SDK responsibilities, including function
offset discovery, DWARF parsing, and the gRPC interface exposed to the Agent.

## Problem

To attach eBPF uprobes safely and accurately, the Agent needs to know the exact
memory offset of target functions within the application binary. This
information is dynamic (changes with every build) and often stripped from
production binaries. We need a way for the application to self-report this
metadata at runtime.

## Solution

The `coral-go` SDK will provide a lightweight runtime monitoring capability
that:

1. Parses its own executable's debug info (DWARF) or uses runtime reflection to
   find function offsets.
2. Exposes a gRPC server for the local Agent to query these offsets.
3. Handles the "handshake" with the Agent to register the service.

### SDK API

The SDK is designed to be minimally invasive.

```go
package coral

// RegisterService registers the application with Coral agent.
func RegisterService(name string, opts Options) error

// EnableRuntimeMonitoring starts background goroutine that:
// - Discovers function offsets
// - Serves gRPC API for agent queries
func EnableRuntimeMonitoring() error

type Options struct {
    Port           int    // Application listen port
    HealthEndpoint string // Health check endpoint
    AgentAddr      string // Agent gRPC address (default: localhost:9091)
    SdkListenAddr  string // Address for SDK gRPC server (default: :9092)
}
```

### Function Offset Discovery

The SDK employs two strategies to find function offsets:

#### 1. DWARF Debug Info (Preferred)

If the binary is built with DWARF symbols (default in Go, unless `-w` is used),
the SDK parses the `.debug_info` section.

* **Pros**: Full metadata (file, line number, params), works for unexported
  functions.
* **Cons**: Increases binary size (~20%).

#### 2. Runtime Reflection (Fallback)

If DWARF is missing, the SDK uses Go's `runtime` package and `reflect` to
iterate over the symbol table.

* **Pros**: Works on stripped binaries (if `-s` is used but symbol table
  remains, or just standard Go symbol table).
* **Cons**: Only exported functions (usually), no file/line metadata.

### SDK gRPC API

The SDK exposes a gRPC service that the Agent calls to resolve function names to
offsets.

```protobuf
// proto/coral/sdk/v1/runtime.proto

service RuntimeMonitoring {
    // Query available functions for uprobe attachment
    rpc ListFunctions(ListFunctionsRequest) returns (ListFunctionsResponse);

    // Get offset for specific function
    rpc GetFunctionOffset(GetFunctionOffsetRequest) returns (GetFunctionOffsetResponse);
}

message ListFunctionsRequest {
    string pattern = 1; // Regex pattern (e.g., "handle.*")
}

message ListFunctionsResponse {
    repeated FunctionInfo functions = 1;
}

message FunctionInfo {
    string name = 1;           // Full function name (e.g., "main.handleCheckout")
    uint64 offset = 2;         // Offset from executable base
    string file = 3;           // Source file path
    uint32 line = 4;           // Source line number
}

message GetFunctionOffsetRequest {
    string function_name = 1;
}

message GetFunctionOffsetResponse {
    uint64 offset = 1;
    bool found = 2;
}
```

### Build Recommendations

To maximize debugging capability, we recommend the following build flags:

| Build Flags                 | Binary Size | Uprobe Support | Function Discovery         | Recommendation   |
|:----------------------------|:------------|:---------------|:---------------------------|:-----------------|
| `go build`                  | 100%        | ✅ Full         | All functions + metadata   | Development      |
| `go build -ldflags="-s"`    | ~95%        | ✅ Full         | All functions + metadata   | **Production**   |
| `go build -ldflags="-w"`    | ~70%        | ⚠️ Limited     | Exported only, no metadata | Size-constrained |
| `go build -ldflags="-s -w"` | ~65%        | ⚠️ Limited     | Exported only, no metadata | Size-constrained |

**Note**: `-s` strips the symbol table but leaves DWARF. `-w` strips DWARF.

## Implementation Details

### DWARF Parsing

The SDK will use `debug/dwarf` and `debug/elf` to open `/proc/self/exe`.

```go
func discoverFunctionOffsets() (map[string]uint64, error) {
    exePath, _ := os.Executable()
    elfFile, _ := elf.Open(exePath)
    dwarfData, _ := elfFile.DWARF()

    // Iterate over subprograms in DWARF
    // ...
}
```

### Security

* The SDK gRPC server should only accept connections from the local Agent (or
  authorized subnets).
* It is a read-only API; it cannot modify application state.

## Configuration Changes

### Application Integration

Applications must import and initialize the SDK:

```go
// In application code
import "github.com/coral-io/coral-go"

func main() {
    // Register service with Coral
    coral.RegisterService("api", coral.Options{
        Port:           8080,
        HealthEndpoint: "/health",
        AgentAddr:      "localhost:9091", // Default
        SdkListenAddr:  ":9092",          // SDK gRPC server
    })

    // Enable runtime monitoring (starts background goroutine)
    coral.EnableRuntimeMonitoring()

    // ... application code
}
```

### Build Recommendations

To maximize debugging capability, applications should be built with DWARF debug
information.

| Build Flags                 | Binary Size | Uprobe Support | Function Discovery         | Recommendation   |
|:----------------------------|:------------|:---------------|:---------------------------|:-----------------|
| `go build`                  | 100%        | ✅ Full         | All functions + metadata   | Development      |
| `go build -ldflags="-s"`    | ~95%        | ✅ Full         | All functions + metadata   | **Production**   |
| `go build -ldflags="-w"`    | ~70%        | ⚠️ Limited     | Exported only, no metadata | Size-constrained |
| `go build -ldflags="-s -w"` | ~65%        | ⚠️ Limited     | Exported only, no metadata | Size-constrained |

**Note**: `-s` strips the symbol table but leaves DWARF. `-w` strips DWARF.

**Recommended Production Build:**

```bash
# Best balance: optimized, DWARF included, slightly smaller
go build -ldflags="-s" -trimpath -o myapp

# Alternative: keep both symbols and DWARF (debugging-friendly)
go build -trimpath -o myapp
```

**Docker/Kubernetes Deployment:**

```dockerfile
FROM golang:1.25 AS builder

WORKDIR /app
COPY . .

# Build with DWARF (avoid -w flag)
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s" \
    -trimpath \
    -o myapp

FROM scratch
COPY --from=builder /app/myapp /myapp

# Binary has DWARF, uprobes will work
ENTRYPOINT ["/myapp"]
```

**Verifying Debug Info:**

```bash
# Linux
readelf --debug-dump=info myapp | head -20

# macOS
dwarfdump --debug-info myapp | head -20

# Cross-platform (using go tool)
go tool nm myapp | grep -i 'main.handleCheckout'
```

### SDK Behavior

* **Startup check**: SDK checks for DWARF and falls back to runtime reflection
  if unavailable.
* **Warning message**: If DWARF missing, logs warning:
  ```
  [Coral SDK] WARN: No DWARF debug info found in binary
  [Coral SDK] Using runtime reflection (exported functions only)
  [Coral SDK] For full uprobe support, rebuild without -ldflags="-w"
  ```
* **Graceful degradation**: Application continues normally in both cases.

## Implementation Plan

### Phase 1: Core SDK Infrastructure

- [x] Create `coral-go` SDK repository
- [x] Implement `RegisterService` and `EnableRuntimeMonitoring` API
- [x] Set up gRPC server for agent communication
- [x] Implement service registration with local agent

### Phase 2: Function Discovery

- [x] DWARF debug info parser (using `debug/dwarf`, `debug/elf`)
- [x] Runtime reflection fallback for stripped binaries
- [x] Symbol table builder and cache
- [x] Handle inlined functions and optimized builds

### Phase 3: Agent Integration

- [x] Implement `ListFunctions` RPC handler
- [x] Implement `GetFunctionOffset` RPC handler
- [x] Add connection management for agent queries
- [x] Implement binary hash calculation for cache invalidation

### Phase 4: Testing & Documentation

- [x] Unit tests for DWARF parsing
- [x] Unit tests for runtime reflection
- [x] Integration tests with sample application
- [x] SDK documentation and usage examples
- [x] Build guide for different optimization levels

## Implementation Status

**Core Capability:** ✅ Complete

The SDK Runtime Monitoring system is fully implemented, enabling Go applications
to self-register with the Coral Agent and expose function metadata for live
debugging.

**Operational Components:**

- ✅ **SDK Core**: `RegisterService` and `EnableRuntimeMonitoring` APIs.
- ✅ **Function Discovery**: DWARF parsing (primary) and Reflection fallback (
  stripped binaries).
- ✅ **Agent Integration**: `ServiceSdkCapabilities` exchange and
  `ConnectService` RPC.
- ✅ **Metadata Provider**: Efficient caching and lookup of function offsets.

**What Works Now:**

- Applications automatically register with the local Agent on startup.
- Agent receives full metadata: SDK version, DWARF availability, binary path,
  function count, and binary hash.
- Reflection fallback allows debugging even on stripped binaries (entry points
  only).
- `sdk-demo` example demonstrates full end-to-end flow.

## Testing Strategy

### Unit Tests

* **DWARF Parsing**: Test with binaries built with various flags (`-s`, `-w`,
  `-s -w`).
* **Runtime Reflection**: Test function discovery on stripped binaries.
* **Offset Calculation**: Verify offsets match expected values.
* **Symbol Table**: Test caching and invalidation.

### Integration Tests

* **SDK + Agent**: Full request/response cycle for function queries.
* **Multiple Applications**: Test with different Go versions and build
  configurations.
* **Error Handling**: Test behavior when DWARF missing, when binary changes,
  when agent disconnects.

### E2E Tests

* **Sample Application**: Go web service with SDK integration.
* **Function Discovery**: Verify all expected functions are discoverable.
* **Offset Accuracy**: Verify uprobes attach to correct locations.
* **Build Variations**: Test with different build flags.

## Security Considerations

### Network Exposure

* **Local-Only by Default**: SDK gRPC server listens on `localhost:9092` by
  default.
* **Container Networking**: In Kubernetes, SDK is reachable only by the local
  agent in the same pod/node.
* **No Authentication**: V1 relies on network segmentation. Future: mTLS for
  agent-SDK communication.

### Data Privacy

* **Read-Only**: SDK only exposes function metadata, not application state.
* **No PII**: Function names, offsets, and source locations are not sensitive
  data.
* **Binary Inspection**: SDK reads its own executable, not other processes.

### Resource Usage

* **Low Overhead**: Symbol table built once at startup (<100ms, <10MB memory).
* **Lazy Loading**: DWARF parsing deferred until first query (optional
  optimization).
* **No Runtime Impact**: Background goroutine is idle until agent queries.

## Appendix

### Example: DWARF Parsing Implementation

```go
// SDK internal implementation
package internal

import (
    "debug/dwarf"
    "debug/elf"
    "fmt"
    "os"
)

// discoverFunctionOffsets parses DWARF debug info to find function offsets.
func discoverFunctionOffsets() (map[string]uint64, error) {
    // 1. Get executable path
    exePath, err := os.Executable()
    if err != nil {
        return nil, err
    }

    // 2. Open ELF file
    elfFile, err := elf.Open(exePath)
    if err != nil {
        return nil, err
    }
    defer elfFile.Close()

    // 3. Parse DWARF debug info
    dwarfData, err := elfFile.DWARF()
    if err != nil {
        return nil, fmt.Errorf("no debug symbols: %w", err)
    }

    offsets := make(map[string]uint64)

    // 4. Iterate over compilation units
    reader := dwarfData.Reader()
    for {
        entry, err := reader.Next()
        if entry == nil || err != nil {
            break
        }

        // 5. Find function entries (subprograms)
        if entry.Tag == dwarf.TagSubprogram {
            nameAttr := entry.Val(dwarf.AttrName)
            lowPCAttr := entry.Val(dwarf.AttrLowpc)

            if nameAttr != nil && lowPCAttr != nil {
                name := nameAttr.(string)
                lowPC := lowPCAttr.(uint64)
                offsets[name] = lowPC
            }
        }
    }

    return offsets, nil
}

// discoverFunctionsViaReflection uses Go runtime for stripped binaries.
func discoverFunctionsViaReflection() (map[string]uint64, error) {
    // Fallback when DWARF is unavailable
    // Uses runtime.FuncForPC and reflect package
    // See: https://pkg.go.dev/runtime#FuncForPC

    // Note: This has limitations:
    // - Only exported functions (usually)
    // - No file/line information
    // - May miss inlined functions

    return nil, fmt.Errorf("reflection-based discovery not yet implemented")
}
```

### Example: SDK Usage in Application

```go
// main.go - Sample application with Coral SDK
package main

import (
    "context"
    "log"
    "net/http"
    "time"

    "github.com/coral-io/coral-go"
)

func main() {
    // Register with Coral
    coral.RegisterService("checkout-api", coral.Options{
        Port:           8080,
        HealthEndpoint: "/health",
    })

    // Enable runtime monitoring (starts background goroutine)
    coral.EnableRuntimeMonitoring()

    // Set up HTTP handlers
    http.HandleFunc("/api/checkout", handleCheckout)
    http.HandleFunc("/health", healthCheck)

    // Start server
    log.Println("Starting server on :8080")
    log.Fatal(http.ListenAndServe(":8080", nil))
}

// Business logic - no instrumentation needed!
func handleCheckout(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()

    // Validate cart
    if err := validateCart(ctx); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    // Process payment
    if err := processPayment(ctx); err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    w.WriteHeader(http.StatusOK)
    w.Write([]byte("OK"))
}

func validateCart(ctx context.Context) error {
    time.Sleep(10 * time.Millisecond) // Simulated work
    return nil
}

func processPayment(ctx context.Context) error {
    time.Sleep(50 * time.Millisecond) // Simulated external API call
    return nil
}

func healthCheck(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusOK)
}
```

**Build and run:**

```bash
# Build with debug symbols (required for uprobes)
$ go build -ldflags="-s" -o checkout-api main.go

# Run application
$ ./checkout-api
Starting server on :8080
[Coral SDK] Runtime monitoring enabled
[Coral SDK] Discovered 3 functions: handleCheckout, validateCart, processPayment
[Coral SDK] DWARF symbols: ✓ present
[Coral SDK] Listening for agent queries on :9092
```
