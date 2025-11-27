---
rfd: "059"
title: "SDK Runtime Monitoring"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "058" ]
database_migrations: [ ]
areas: [ "sdk", "go", "debugging" ]
---

# RFD 059 - SDK Runtime Monitoring

**Status:** 🚧 Draft

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
