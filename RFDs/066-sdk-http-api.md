---
rfd: "066"
title: "SDK HTTP API - Pull-Based Discovery"
state: "implemented"
breaking_changes: true
testing_required: true
database_changes: false
api_changes: true
dependencies: [ "060", "064" ]
database_migrations: [ ]
areas: [ "sdk", "agent", "debugging" ]
---

# RFD 066 - SDK HTTP API - Pull-Based Discovery

**Status:** 🎉 Implemented

## Summary

Refactor the SDK-Agent communication from push-based gRPC registration to
pull-based HTTP/JSON discovery, making the SDK dependency-free and establishing
a convention that could be adopted beyond Coral for uprobe-based debugging.

## Problem

The current SDK implementation (RFD 060) has several issues:

**Tight Coupling:**

- SDK must know agent address (`localhost:9091`)
- SDK actively initiates registration with retry logic
- Adds complexity and failure modes to application code

**Heavy Dependencies:**

- Requires Connect-RPC and protobuf
- Forces applications to include gRPC tooling
- Increases binary size and complexity

**UX Misalignment:**

- SDK auto-registers on startup (implicit)
- Doesn't align with explicit `coral connect` UX
- Users can't control when monitoring starts

**Not a Convention:**

- Coral-specific implementation
- Can't be adopted by other uprobe tools
- Requires tight integration with Coral ecosystem

## Solution

Transform the SDK into a **simple HTTP server** that exposes debug metadata on a
standard port, following the Prometheus `/metrics` pattern.

### Key Design Principles

1. **Zero Dependencies**: SDK uses only Go stdlib (`net/http`, `encoding/json`)
2. **Pull-Based**: Agent discovers SDK, not vice versa
3. **Standard Port**: Bind to `localhost:9002` (configurable)
4. **Convention Over Framework**: Could become industry-standard for uprobe
   debugging
5. **Explicit Control**: Discovery happens when `coral connect` is called

### Architecture Overview

```
┌─────────────────┐                    ┌──────────────┐
│  Application    │                    │  Coral Agent │
│                 │                    │              │
│  ┌───────────┐  │                    │              │
│  │ Coral SDK │  │                    │              │
│  │           │  │                    │              │
│  │ HTTP :9002│◄─┼────GET /debug/*────┤  Discovery   │
│  └───────────┘  │                    │              │
│                 │                    │              │
│  App :8080 ◄────┼────Health Checks───┤  Monitor     │
└─────────────────┘                    └──────────────┘

User: coral connect myapp:8080
      ↓
Agent: 1. Monitor app on :8080
       2. Probe localhost:9002 for SDK
       3. Store capabilities if found
```

**Flow:**

1. Application imports SDK, starts HTTP server on `:9002`
2. User runs `coral connect myapp:8080`
3. Agent probes `localhost:9002/debug/capabilities`
4. If found, agent queries function metadata and enables live debugging
5. If not found, service runs normally (SDK optional)

## API Specification

### HTTP Endpoints

All endpoints are **GET requests** returning **JSON** responses.

#### `GET /debug/capabilities`

Returns SDK version and DWARF availability.

**Response:**

```json
{
    "process_id": "1234",
    "sdk_version": "v0.2.0",
    "has_dwarf_symbols": true,
    "function_count": 127,
    "binary_path": "/usr/local/bin/payment-api",
    "binary_hash": "sha256:abc123..."
}
```

**Note:** Service name is not included because it's determined by the `coral connect` command on the agent side. The agent associates the SDK capabilities with the service name provided by the user in `coral connect`.

**HTTP Status:**

- `200 OK`: SDK available
- `404 Not Found`: No SDK (endpoint doesn't exist)

#### `GET /debug/functions`

Returns list of discoverable functions with filtering and pagination.

**Query Parameters:**

- `pattern` (optional): Filter functions by pattern (e.g., `?pattern=Process*`)
- `package` (optional): Filter by package (e.g.,
  `?package=github.com/myapp/payments`)
- `limit` (optional): Maximum functions to return (default: 100, max: 1000)
- `offset` (optional): Skip first N functions for pagination (default: 0)

**Response Headers:**

- `Content-Encoding: gzip` - Always compressed for large payloads
- `X-Total-Count` - Total functions matching filter (for pagination)

**Response:**

```json
{
    "functions": [
        {
            "name": "github.com/myapp/payments.ProcessPayment",
            "offset": 12345,
            "file": "/app/payments/process.go",
            "line": 42
        },
        {
            "name": "github.com/myapp/payments.ValidateCard",
            "offset": 13456,
            "file": "/app/payments/validate.go",
            "line": 18
        }
    ],
    "total": 127,
    "returned": 2,
    "offset": 0,
    "has_more": false
}
```

**Pagination Example:**

```bash
# Get first 100 functions
curl http://localhost:9002/debug/functions?limit=100&offset=0

# Get next 100
curl http://localhost:9002/debug/functions?limit=100&offset=100
```

#### `GET /debug/functions/{name}`

Returns metadata for a specific function.

**URL Encoding:** Function names must be URL-encoded (e.g.,
`github.com%2Fmyapp%2Fpayments.ProcessPayment`)

**Response:**

```json
{
    "name": "github.com/myapp/payments.ProcessPayment",
    "offset": 12345,
    "file": "/app/payments/process.go",
    "line": 42,
    "arguments": [
        {
            "name": "ctx",
            "type": "context.Context",
            "offset": 8
        },
        {
            "name": "req",
            "type": "*PaymentRequest",
            "offset": 16
        }
    ],
    "returns": [
        {
            "type": "*PaymentResponse",
            "offset": 0
        },
        {
            "type": "error",
            "offset": 8
        }
    ]
}
```

**HTTP Status:**

- `200 OK`: Function found
- `404 Not Found`: Function not found

#### `GET /debug/functions/export`

Returns a compressed stream of all function metadata for bulk export.

**Primary Use Case:** Agent imports all functions into DuckDB with VSS extension
for semantic search via embeddings. This enables AI-driven function discovery
(e.g., "Find functions that handle payment validation").

**Query Parameters:**

- `format` (optional): Export format - `json` (default) or `ndjson`
  (newline-delimited JSON)

**Response Headers:**

- `Content-Type: application/gzip`
- `Content-Disposition: attachment; filename="functions-{hash}.json.gz"`
- `X-Total-Functions` - Total functions in export

**Response:** Gzip-compressed JSON or NDJSON stream

**NDJSON Format** (recommended for large datasets):

```
{"name":"main.ProcessPayment","offset":12345,"file":"/app/main.go","line":42}
{"name":"main.ValidateCard","offset":13456,"file":"/app/main.go","line":58}
...
```

**Compression Efficiency:**

- Typical function metadata: ~150 bytes/function
- 10,000 functions: ~1.5 MB uncompressed
- Gzip compression: ~80-90% reduction → ~200 KB
- 100,000 functions: ~15 MB uncompressed → ~2 MB compressed

**Example:**

```bash
# Download full function list (compressed)
curl http://localhost:9002/debug/functions/export \
  -o functions.json.gz

# Extract and process
gunzip functions.json.gz
jq '.[] | select(.name | contains("Process"))' functions.json
```

### Error Responses

All errors return JSON with `error` field:

```json
{
    "error": "function not found: invalid.Function"
}
```

## SDK Changes

### Simplified API

**Core Functions** (`pkg/sdk/sdk.go`):

- `EnableRuntimeMonitoring(opts)` - Start HTTP debug server on configured port

**Key Changes from RFD 060:**

- Remove `RegisterService()` - Service name comes from `coral connect`, not SDK
- Remove `Options.AgentAddr` - No longer needed (pull-based discovery)
- Remove `Options.Port` and `Options.HealthEndpoint` - Specified in `coral connect`
- Remove background registration goroutine and retry logic
- Remove all Connect-RPC and protobuf dependencies
- Use only Go stdlib: `net/http`, `encoding/json`, `debug/dwarf`, `debug/elf`

**Design Rationale:**

The service name is not part of the SDK API because:
1. It's already specified by the user in `coral connect myapp:8080`
2. The agent associates the SDK at `localhost:9002` with the service name from the CLI
3. This eliminates potential conflicts between SDK-declared and CLI-declared names
4. Makes the SDK even simpler - just enable monitoring, no identity management

### Example Usage

```go
package main

import (
    "log"
    "net/http"
    "github.com/coral-mesh/coral-go"
)

func main() {
    // Start debug server (no service name or agent address needed!)
    if err := coral.EnableRuntimeMonitoring(coral.Options{
        DebugAddr: ":9002", // Optional, defaults to :9002
    }); err != nil {
        log.Printf("Warning: SDK debug server failed: %v", err)
        // App continues normally - SDK is optional
    }

    // Start application server
    http.HandleFunc("/api/payment", ProcessPayment)
    http.ListenAndServe(":8080", nil)
}
```

**Service connection:**
```bash
# User provides service name via CLI, not SDK
coral connect payment-api:8080:/health
```

### Implementation Approach

**HTTP Server** (`pkg/sdk/debug/server.go`):

- Implements standard `http.Handler` interface
- Routes requests to `/debug/capabilities`, `/debug/functions`, `/debug/functions/{name}`, `/debug/functions/export`
- Returns JSON responses with appropriate headers
- Supports gzip compression for large payloads

**Memory Management Strategy:**

1. **Startup**: Parse DWARF to build minimal index (name → offset + location)
   - Memory: ~80 bytes/function
   - Time: ~100-200ms for 10k functions

2. **HTTP Requests**: Parse detailed metadata (args, returns) on-demand
   - LRU cache for last 100 functions
   - Cache hit: O(1) lookup
   - Cache miss: Parse DWARF (~1-2ms/function)

3. **Export Endpoint**: Stream from index, parse on-the-fly
   - No intermediate buffering
   - Memory stays constant regardless of function count

## Agent Changes

### Discovery Flow

**Service Connection** (`internal/agent/service_handler.go`):

When `coral connect <service>` is called:

1. Connect to service (health checks, monitoring)
2. Probe `localhost:9002/debug/capabilities` via HTTP GET
3. If SDK responds with 200 OK, parse capabilities JSON
4. Store SDK metadata (version, function count, binary hash)
5. If SDK not found (404 or connection refused), continue without SDK

**Key Implementation Points:**

- Use standard `net/http` client for discovery
- SDK discovery is non-blocking and optional
- Failed discovery is logged but not treated as error
- Capabilities include binary hash for cache invalidation

### Function Metadata Queries

**Uprobe Attachment:**

When attaching uprobes to specific functions:

1. Query `GET /debug/functions/{name}` with URL-encoded function name
2. Parse JSON response to extract function offset
3. Use offset for eBPF uprobe attachment
4. Cache function metadata to avoid repeated HTTP calls

## CLI Changes

No changes to user-facing commands, but better feedback:

```bash
$ coral connect payment-api:8080:/health

Connecting to service: payment-api
Port: 8080
Health endpoint: /health
Agent: localhost:9001

✓ Connected: payment-api

SDK Auto-Discovery:
  ✓ Found Coral SDK v0.2.0
  ✓ DWARF symbols available
  ✓ 127 functions discoverable

Service is ready for live debugging.

Use 'coral agent status' to view service health
```

**If SDK not found:**

```bash
✓ Connected: payment-api

SDK Auto-Discovery:
  • No SDK detected on :9002 (optional)

Service monitoring active (health checks only).
To enable live debugging, integrate Coral SDK:
  https://docs.coral.io/sdk/getting-started
```

## Migration Strategy

### Breaking Changes

**SDK API Changes:**

```diff
-// OLD: Service declares its own name
-coral.RegisterService("payment-api", coral.Options{
-    Port:           8080,        // ← REMOVED
-    HealthEndpoint: "/health",   // ← REMOVED
-    AgentAddr:      "localhost:9091",  // ← REMOVED
-})
-coral.EnableRuntimeMonitoring()

+// NEW: Just enable monitoring, service name comes from CLI
+coral.EnableRuntimeMonitoring(coral.Options{
+    DebugAddr: ":9002",     // ← NEW (optional, defaults to :9002)
+})
```

**Service Connection:**

```diff
-# OLD: SDK auto-registers on startup
-./payment-api  # SDK connects to agent automatically

+# NEW: Explicit service connection
+./payment-api  # SDK starts HTTP server on :9002
+coral connect payment-api:8080:/health  # Agent discovers SDK
```

### Migration Timeline

**Phase 1: Draft RFD** (This document)

- [ ] Review and approve architectural changes
- [ ] Finalize HTTP/JSON API specification

**Phase 2: Implementation** (Breaking changes)

- [ ] Remove Connect-RPC dependency from SDK
- [ ] Implement HTTP server in `pkg/sdk/debug/server.go`
- [ ] Update agent discovery in `internal/agent/service_handler.go`
- [ ] Update all examples (`examples/sdk-demo`)

**Phase 3: Documentation**

- [ ] Update RFD 060 status to "Superseded by RFD 065"
- [ ] Migration guide for existing SDK users
- [ ] Update all documentation and examples

**Phase 4: Testing**

- [ ] Update all SDK unit tests
- [ ] Update agent integration tests
- [ ] E2E testing with updated sdk-demo
- [ ] Test backward compat scenarios

## AI-Driven Function Discovery

The HTTP/JSON API enables **semantic search** for function discovery, a key
differentiator for Coral's AI-powered debugging.

### Traditional Approach (Exact Match)

```bash
# User must know exact function name
coral debug attach payment-api --function ProcessPayment
```

**Problem:** User must know the codebase intimately to know function names.

### Coral's Semantic Search Approach

```bash
# User describes intent in natural language
coral ask "Why is payment processing slow?"

# Behind the scenes:
# 1. LLM extracts intent: "payment processing"
# 2. Agent generates embedding for "payment processing"
# 3. DuckDB VSS finds similar functions:
#    - ProcessPayment (distance: 0.12)
#    - ValidateCard (distance: 0.18)
#    - AuthorizeTransaction (distance: 0.21)
# 4. Agent attaches uprobes to top 3 matches
# 5. Collect performance data
# 6. LLM analyzes results and responds
```

### DuckDB VSS Integration

**Why DuckDB with VSS extension?**

1. **Embedded database** - No external dependencies
2. **OLAP optimized** - Fast analytical queries
3. **VSS extension** - Native vector similarity search
4. **SQL interface** - Easy to query and join with telemetry data
5. **Parquet export** - Can share datasets with LLMs

**Example Queries:**

```sql
-- Find functions semantically similar to "validate credit card"
SELECT name, offset, array_distance(embedding, ?) as similarity
FROM sdk_functions
WHERE service = 'payment-api'
ORDER BY similarity ASC LIMIT 10;

-- Join with telemetry to find slow functions related to "checkout"
SELECT f.name, f.offset, AVG(t.duration_ms) as avg_duration
FROM sdk_functions f
         JOIN telemetry_spans t ON t.function_name = f.name
WHERE array_distance(f.embedding, ?) < 0.3 -- Similarity threshold
GROUP BY f.name, f.offset
ORDER BY avg_duration DESC;

-- Find functions in same package as a known slow function
SELECT f2.name, array_distance(f1.embedding, f2.embedding) as similarity
FROM sdk_functions f1,
     sdk_functions f2
WHERE f1.name = 'ProcessPayment'
  AND f2.service = f1.service
  AND f2.name != f1.name
ORDER BY similarity ASC
    LIMIT 5;
```

### Embedding Generation Strategy

**Option 1: OpenAI API** (default, most accurate)

```go
embedding := openai.CreateEmbedding(openai.EmbeddingRequest{
    Model: "text-embedding-3-small",
    Input: fmt.Sprintf("%s %s", functionName, filepath),
})
// Dimensions: 1536, Cost: $0.02 per 1M tokens
```

**Option 2: Local model** (privacy-focused)

```go
// Use all-MiniLM-L6-v2 via Ollama or sentence-transformers
embedding := localModel.Encode(functionName)
// Dimensions: 384, Cost: Free, Latency: ~5ms
```

**Trade-offs:**

| Model                    | Dimensions | Accuracy | Cost     | Privacy |
|--------------------------|------------|----------|----------|---------|
| text-embedding-3-small   | 1536       | High     | $0.02/1M | Cloud   |
| text-embedding-3-large   | 3072       | Highest  | $0.13/1M | Cloud   |
| all-MiniLM-L6-v2 (local) | 384        | Medium   | Free     | Local   |

**Recommended:** Start with local model, upgrade to OpenAI for complex queries.

### Full AI-Driven Debugging Flow

```
User: "Why is checkout slow?"
  ↓
LLM: Extract entities → ["checkout", "slow", "performance"]
  ↓
Agent: Generate embeddings for entities
  ↓
DuckDB VSS: Find top-k similar functions
  → ProcessCheckout (0.08)
  → ValidateCart (0.15)
  → ApplyDiscount (0.19)
  ↓
Agent: Attach uprobes to discovered functions
  ↓
eBPF: Collect execution time, args, returns
  ↓
Agent: Store spans in DuckDB
  ↓
LLM: Analyze telemetry + code context
  ↓
Response: "ProcessCheckout is slow because ValidateCart
          makes 5 database queries per item. Optimize to
          batch query all items at once."
```

## Future: Convention Over Framework

This HTTP/JSON API could become a **standard convention** for uprobe-based
debugging tools, not just Coral.

**Potential adoption:**

```
Any tool that needs function offsets for uprobes could:
1. Check if app exposes GET /debug/capabilities
2. Query GET /debug/functions to discover targets
3. Attach uprobes without app-specific integration

Examples:
- Performance profilers (continuous profiling)
- APM tools (transaction tracing)
- Security tools (runtime behavior analysis)
- Testing frameworks (coverage analysis)
```

**Benefits of standardization:**

- Apps instrument once, support multiple tools
- Lower friction for observability adoption
- Interoperability between debugging ecosystems
- Community-driven improvements

**Proposal:** Publish as an informal specification (similar to Prometheus
exposition format) that other tools can adopt.

## Testing Strategy

### Integration Tests

**Full Discovery Flow:**

1. Start agent
2. Start app with SDK
3. Run `coral connect app:8080`
4. Verify agent discovered SDK via HTTP
5. Verify capabilities stored
6. Run `coral debug attach app --function Handler`
7. Verify uprobe attached successfully

### E2E Tests

Update `examples/sdk-demo`:

```bash
# Start agent
coral agent start

# Start demo app (new SDK)
cd examples/sdk-demo
go build -o demo .
./demo
# Output: "SDK HTTP server listening on 127.0.0.1:9002"

# Connect service
coral connect payment-service:3001:/health
# Output: "✓ Found Coral SDK v0.2.0"

# Attach debugger
coral debug attach payment-service --function ProcessPayment
# Should work without errors
```

## Security Considerations

### Network Binding

**Localhost-only by default:**

```go
// SDK binds to 127.0.0.1:9002 (not 0.0.0.0)
listener, err := net.Listen("tcp", "127.0.0.1:9002")
```

**Rationale:**

- Agent and SDK always co-located on same host
- No need for remote access
- Prevents accidental internet exposure

### Deployment Patterns

**Pattern 1: Sidecar Deployment** (same network namespace)

```yaml
# Agent and app share network namespace
apiVersion: v1
kind: Pod
spec:
  containers:
  - name: app
    image: myapp:latest
    # SDK binds to 127.0.0.1:9002
  - name: coral-agent
    image: coral-agent:latest
    # Agent connects to 127.0.0.1:9002 directly
```

In this pattern, the agent directly accesses `http://127.0.0.1:9002` since both
containers share the same network namespace.

**Pattern 2: Host Agent (DaemonSet)** (requires nsenter)

```yaml
# Agent runs on host, app runs in containers
apiVersion: apps/v1
kind: DaemonSet
spec:
  template:
    spec:
      hostPID: true  # Required for nsenter
      containers:
      - name: coral-agent
        image: coral-agent:latest
        securityContext:
          privileged: true  # Required for nsenter
```

In this pattern, the agent must enter the target container's network namespace to reach `127.0.0.1:9002`.

**Implementation Options:**

**Option A: Use `setns()` syscall directly** (preferred for HTTP client)

**Option B: Shell out to `nsenter`** (simpler, for commands)

```go
// For running arbitrary commands (e.g., coral exec)
func (a *Agent) execInNamespace(pid int, command []string) error {
    args := []string{
        "-t", fmt.Sprintf("%d", pid),
        "-n", // network namespace
        "-p", // pid namespace
        "-m", // mount namespace
        "--", // end of nsenter args
    }
    args = append(args, command...)

    cmd := exec.Command("nsenter", args...)
    return cmd.Run()
}
```

**Recommendation:**

- Use **Option A (setns)** for SDK HTTP discovery (cleaner, no external deps)
- Use **Option B (nsenter)** for `coral exec` (supports all namespaces easily)
- Assume `nsenter` is available on host (part of util-linux, standard on most
  Linux distributions)
- If `nsenter` is missing, fallback to setns or fail with clear error message

**Recommended:** Start with sidecar for simplicity, migrate to DaemonSet for
production efficiency.

### Data Exposure

**What's exposed:**

- Function names (compile-time metadata)
- Memory offsets (non-exploitable without process access)
- Type information (DWARF symbols)

**What's NOT exposed:**

- Environment variables
- Configuration values
- Runtime data or memory contents
- Credentials or secrets

**Risk level:** Similar to Prometheus `/metrics` - structural metadata only.

### Authentication

**V1: None (localhost-only)**

- Network segmentation provides security
- Same trust model as Prometheus

**V2: Optional mTLS** (future enhancement)

- For high-security environments
- Agent and SDK share certificate
- Out of scope for initial implementation

## Implementation Plan

### Phase 1: SDK Core

- [x] Remove Connect-RPC and protobuf dependencies
- [x] Implement HTTP server with `/debug/*` endpoints
- [x] Remove `RegisterService()` - service name comes from CLI
- [x] Update `EnableRuntimeMonitoring()` API to accept options directly
- [x] Add HTTP handlers for capabilities, functions, and export
- [x] Unit tests for HTTP endpoints

### Phase 2: Agent Discovery

- [x] Implement SDK discovery via HTTP client
- [x] Update `ConnectService` handler to probe `:9002`
- [x] Update function metadata queries to use HTTP
- [x] Add agent unit tests for discovery

### Phase 3: Examples & Documentation

- [x] Update `examples/sdk-demo/main.go` with new API
- [ ] Update RFD 060 status to "Superseded by RFD 066"
- [ ] Write migration guide
- [x] Update all documentation
- [x] Update CLI help text and output messages

### Phase 4: Testing & Polish

- [x] Comprehensive test coverage
- [x] E2E testing with sdk-demo
- [x] Performance testing (discovery latency)
- [x] Error handling and edge cases
- [x] Security review

## Implementation Status

**Core Capability:** ✅ Complete

SDK-Agent communication has been successfully refactored from push-based gRPC to pull-based HTTP/JSON discovery. The SDK is now dependency-free (uses only Go stdlib) and follows the Prometheus `/metrics` pattern for debug metadata exposure.

**Operational Components:**

- ✅ HTTP debug server in SDK (`pkg/sdk/debug/server.go`)
- ✅ Zero-dependency SDK using only `net/http` and `encoding/json`
- ✅ Agent HTTP discovery (`internal/agent/service_handler.go`)
- ✅ Function metadata queries via HTTP (`internal/agent/ebpf/sdk_client.go`)
- ✅ Automatic SDK discovery on `coral connect`
- ✅ CLI integration with improved feedback
- ✅ Updated examples (`examples/sdk-demo`)

**What Works Now:**

- SDK starts HTTP server on `localhost:9002` (configurable)
- Agent discovers SDK capabilities via `GET /debug/capabilities`
- Agent queries function metadata via `GET /debug/functions/{name}`
- Pagination and filtering for large function lists
- Gzip compression for bulk exports
- Automatic SDK address resolution (no manual `--sdk-addr` flag needed)
- Debug attach command uses discovered SDK address
- Service monitoring works with or without SDK (optional)

**Breaking Changes Implemented:**

- Removed `RegisterService()` API - service name now comes from `coral connect`
- Removed `Options.AgentAddr` - no longer needed with pull-based discovery
- Removed `Options.Port` and `Options.HealthEndpoint` - specified in CLI
- Removed Connect-RPC and protobuf dependencies from SDK
- Updated `EnableRuntimeMonitoring()` to accept `Options` with `DebugAddr`

**Integration Status:**

- ✅ SDK HTTP server fully functional
- ✅ Agent discovery integrated into service connection flow
- ✅ CLI commands updated to remove obsolete `--sdk-addr` flag
- ✅ Examples updated to new API
- ⏳ Migration guide pending (deferred to documentation update)
- ⏳ RFD 060 status update pending

## Configuration Examples

### SDK Integration

**Minimal (defaults to :9002):**

```go
import "github.com/coral-mesh/coral-go"

func main() {
    // Enable monitoring with default port :9002
    coral.EnableRuntimeMonitoring(coral.Options{})

    // Start app server
    http.ListenAndServe(":8080", nil)
}
```

**Custom port:**

```go
coral.EnableRuntimeMonitoring(coral.Options{
    DebugAddr: ":9999", // Custom port
})
```

**Production deployment (Docker):**

```dockerfile
FROM golang:1.25 AS builder
WORKDIR /app
COPY . .

# Build with DWARF symbols (required for uprobes)
RUN go build -ldflags="-s" -o app .

FROM scratch
COPY --from=builder /app/app /app

# SDK will listen on :9002 (localhost-only)
ENTRYPOINT ["/app"]
```

### Agent Discovery

No configuration needed - automatic:

```bash
# Agent probes localhost:9002 when service connects
coral connect my-service:8080:/health
```

## Performance Considerations

### Handling Large Function Lists

**Problem:** Applications with tens of thousands of functions (e.g., large monoliths, heavily templated code).

**Solutions:**

1. **HTTP Compression (Gzip)**
   - Automatic compression with standard gzip
   - 80-90% size reduction
   - 10,000 functions: 1.5 MB → 200 KB

2. **Pagination**
   - Default: 100 functions per request
   - Agent can paginate through large lists
   - Prevents overwhelming network/memory

3. **Filtering**
   - `?pattern=Process*` - Only matching functions
   - `?package=github.com/myapp/payments` - Package-scoped
   - Reduces payload before transfer

4. **Bulk Export Endpoint**
   - `/debug/functions/export` - One-time download
   - Agent caches locally
   - Invalidated on binary hash change

5. **NDJSON Streaming**
   - Newline-delimited JSON for large exports
   - Can be processed line-by-line (streaming)
   - Lower memory footprint

### Agent Workflow for Semantic Search

The agent stores function metadata in DuckDB with the VSS extension for semantic search:

1. **Discovery**: Get capabilities (includes binary hash for cache invalidation)
2. **Cache Check**: Query DuckDB to see if functions already loaded for this binary version
3. **Bulk Import**: If not cached, download all functions as compressed NDJSON stream
4. **Streaming Insert**: Stream directly into DuckDB without intermediate storage
5. **Embedding Generation**: Generate embeddings for each function (OpenAI or local model)
6. **Semantic Search**: Use DuckDB VSS extension for ANN queries

**DuckDB Schema:**

```sql
CREATE TABLE sdk_functions (
    service       VARCHAR NOT NULL,
    binary_hash   VARCHAR NOT NULL,
    name          VARCHAR NOT NULL,
    offset        BIGINT  NOT NULL,
    file          VARCHAR,
    line          INTEGER,
    embedding     FLOAT[1536], -- OpenAI embedding dimension
    discovered_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (service, binary_hash, name)
);

-- VSS index for fast ANN search
CREATE INDEX idx_function_embeddings ON sdk_functions USING HNSW (embedding);
```

**Why NDJSON is Optimal:**

- **Streaming**: Parse and insert line-by-line, no full array in memory
- **Incremental**: Can start inserting before full download completes
- **Error recovery**: If connection drops, can resume from last line
- **Memory efficient**: O(1) memory usage regardless of function count

### Performance Benchmarks

| Function Count | Uncompressed | Gzip Compressed | Transfer Time (100 Mbps) |
|----------------|--------------|-----------------|-------------------------|
| 1,000          | 150 KB       | 20 KB           | < 10 ms                 |
| 10,000         | 1.5 MB       | 200 KB          | ~20 ms                  |
| 100,000        | 15 MB        | 2 MB            | ~200 ms                 |

### Memory Usage

**SDK (in instrumented application):**

- **Lazy parsing**: ~50 bytes/function (name + offset index only)
- **Eager parsing**: ~150 bytes/function (full metadata cached)
- **Hybrid (recommended)**: ~80 bytes/function + LRU cache
  - 10,000 functions: ~1 MB
  - 100,000 functions: ~8.5 MB

**Agent (on host with Coral):**

- **During import**: O(1) - streams NDJSON line-by-line, no buffering
- **Persistent storage**: O(n) - DuckDB with embeddings
  - 10,000 functions: ~60 MB (1.5 MB metadata + 60 MB embeddings)
  - 100,000 functions: ~600 MB (acceptable for local database)

## Appendix

### Implementation Details

This section contains detailed implementation examples for reference during development.

#### SDK HTTP Server Implementation

```go
// HTTP server in pkg/sdk/debug/server.go
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    switch {
    case r.URL.Path == "/debug/capabilities":
        s.handleCapabilities(w, r)
    case r.URL.Path == "/debug/functions":
        s.handleListFunctions(w, r)
    case r.URL.Path == "/debug/functions/export":
        s.handleExportFunctions(w, r)
    case strings.HasPrefix(r.URL.Path, "/debug/functions/"):
        s.handleGetFunction(w, r)
    default:
        http.NotFound(w, r)
    }
}

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
    caps := CapabilitiesResponse{
        ProcessID:       fmt.Sprintf("%d", os.Getpid()),
        SdkVersion:      "v0.2.0",
        HasDwarfSymbols: s.provider.HasDWARF(),
        FunctionCount:   s.provider.GetFunctionCount(),
        BinaryPath:      s.provider.BinaryPath(),
        BinaryHash:      s.provider.GetBinaryHash(),
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(caps)
}

func (s *Server) handleListFunctions(w http.ResponseWriter, r *http.Request) {
    // Parse pagination params
    limit := parseInt(r.URL.Query().Get("limit"), 100, 1000)
    offset := parseInt(r.URL.Query().Get("offset"), 0, math.MaxInt)
    pattern := r.URL.Query().Get("pattern")

    // Get filtered functions
    functions := s.provider.ListFunctions(pattern, limit, offset)
    total := s.provider.CountFunctions(pattern)

    // Enable gzip compression for large responses
    w.Header().Set("Content-Type", "application/json")
    w.Header().Set("X-Total-Count", strconv.Itoa(total))

    resp := ListFunctionsResponse{
        Functions: functions,
        Total:     total,
        Returned:  len(functions),
        Offset:    offset,
        HasMore:   offset+len(functions) < total,
    }

    json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleExportFunctions(w http.ResponseWriter, r *http.Request) {
    format := r.URL.Query().Get("format")
    if format == "" {
        format = "json"
    }

    // Set headers for compressed download
    w.Header().Set("Content-Type", "application/gzip")
    w.Header().Set("Content-Disposition",
        fmt.Sprintf(`attachment; filename="functions-%s.%s.gz"`,
            s.provider.GetBinaryHash()[:8], format))
    w.Header().Set("X-Total-Functions",
        strconv.Itoa(s.provider.GetFunctionCount()))

    // Create gzip writer
    gzWriter := gzip.NewWriter(w)
    defer gzWriter.Close()

    if format == "ndjson" {
        // Stream newline-delimited JSON (efficient for large datasets)
        for _, fn := range s.provider.ListAllFunctions() {
            json.NewEncoder(gzWriter).Encode(fn)
        }
    } else {
        // Standard JSON array
        json.NewEncoder(gzWriter).Encode(s.provider.ListAllFunctions())
    }
}
```

#### SDK Metadata Provider Implementation

```go
type MetadataProvider struct {
    elfFile      *elf.File
    dwarfData    *dwarf.Data

    // Minimal index built at startup (~80 bytes/function)
    basicIndex   map[string]*BasicInfo

    // LRU cache for detailed metadata (100 entries)
    detailCache  *lru.Cache
}

type BasicInfo struct {
    Name   string // Fully qualified function name
    Offset uint64 // Memory offset in binary
    File   string // Source file path
    Line   uint32 // Line number
}

func (p *MetadataProvider) GetFunction(name string) (*FunctionMetadata, error) {
    // Check cache first
    if cached, ok := p.detailCache.Get(name); ok {
        return cached.(*FunctionMetadata), nil
    }

    // Get basic info from index
    basic, ok := p.basicIndex[name]
    if !ok {
        return nil, ErrNotFound
    }

    // Parse detailed metadata from DWARF
    detailed := p.parseDWARFDetails(name)
    p.detailCache.Add(name, detailed)

    return detailed, nil
}
```

#### Agent SDK Discovery Implementation

```go
// internal/agent/service_handler.go
func (h *ServiceHandler) ConnectService(
    ctx context.Context,
    req *connect.Request[agentv1.ConnectServiceRequest],
) (*connect.Response[agentv1.ConnectServiceResponse], error) {
    // 1. Connect to service (health checks, etc.)
    h.agent.ConnectService(serviceInfo)

    // 2. Attempt SDK discovery
    if caps := h.discoverSDK(ctx, "localhost:9002"); caps != nil {
        monitor.SetSdkCapabilities(caps)
        h.logger.Info("SDK discovered",
            "service", serviceInfo.Name,
            "version", caps.SdkVersion,
            "functions", caps.FunctionCount)
    }

    return &agentv1.ConnectServiceResponse{Success: true}, nil
}

func (h *ServiceHandler) discoverSDK(ctx context.Context, addr string) *Capabilities {
    // Simple HTTP GET request
    resp, err := http.Get("http://" + addr + "/debug/capabilities")
    if err != nil {
        return nil // SDK not present (not an error)
    }
    defer resp.Body.Close()

    if resp.StatusCode != 200 {
        return nil
    }

    var caps CapabilitiesResponse
    if err := json.NewDecoder(resp.Body).Decode(&caps); err != nil {
        h.logger.Warn("Invalid SDK response", "error", err)
        return nil
    }

    return &caps
}
```

#### Agent DuckDB Integration for Semantic Search

```go
// Agent discovers SDK and imports ALL functions into DuckDB
func (a *Agent) discoverSDK(ctx context.Context, serviceName string) error {
    // 1. Get capabilities (includes binary hash for cache invalidation)
    caps, _ := http.Get("http://localhost:9002/debug/capabilities")

    // 2. Check if functions already loaded for this binary version
    exists := a.db.Query(`
        SELECT COUNT(*) FROM sdk_functions
        WHERE service = ? AND binary_hash = ?`,
        serviceName, caps.BinaryHash)

    if exists > 0 {
        return nil // Already loaded
    }

    // 3. Download ALL functions as compressed NDJSON stream
    resp, _ := http.Get("http://localhost:9002/debug/functions/export?format=ndjson")
    defer resp.Body.Close()

    // 4. Stream directly into DuckDB (no intermediate storage)
    gzReader := gzip.NewReader(resp.Body)
    scanner := bufio.NewScanner(gzReader)

    tx := a.db.Begin()
    for scanner.Scan() {
        var fn FunctionMetadata
        json.Unmarshal(scanner.Bytes(), &fn)

        // Insert into DuckDB with embedding for semantic search
        embedding := generateEmbedding(fn.Name, fn.File) // OpenAI/local model

        tx.Exec(`
            INSERT INTO sdk_functions
            (service, binary_hash, name, offset, file, line, embedding)
            VALUES (?, ?, ?, ?, ?, ?, ?)`,
            serviceName, caps.BinaryHash, fn.Name, fn.Offset,
            fn.File, fn.Line, embedding)
    }
    tx.Commit()

    return nil
}

// User asks: "Find functions that handle credit card payments"
func (a *Agent) findRelevantFunctions(query string) []FunctionMetadata {
    queryEmbedding := generateEmbedding(query)

    // VSS semantic search using DuckDB VSS extension
    results := a.db.Query(`
        SELECT name, offset, file, line,
               array_distance(embedding, ?::FLOAT[1536]) as distance
        FROM sdk_functions
        WHERE service = ?
        ORDER BY distance ASC
        LIMIT 10`,
        queryEmbedding, serviceName)

    return results
}

// Attach uprobe to discovered function
func (a *Agent) attachUprobe(functionName string, offset uint64) {
    // Use offset from DuckDB, no need to query SDK again
    attacheBPFUprobe(offset)
}
```

### Sample HTTP Requests


```bash
# Check if SDK is present
curl http://localhost:9002/debug/capabilities
# {
#   "process_id": "1234",
#   "sdk_version": "v0.2.0",
#   "has_dwarf_symbols": true,
#   "function_count": 127,
#   "binary_path": "/usr/local/bin/payment-api",
#   "binary_hash": "sha256:abc123..."
# }

# List all functions
curl http://localhost:9002/debug/functions
# {
#   "functions": [
#     {"name": "main.ProcessPayment", "offset": 12345, ...},
#     ...
#   ]
# }

# Get specific function
curl http://localhost:9002/debug/functions/main.ProcessPayment
# {
#   "name": "main.ProcessPayment",
#   "offset": 12345,
#   "arguments": [...],
#   "returns": [...]
# }

# Filter by pattern
curl 'http://localhost:9002/debug/functions?pattern=Process*'

# Paginate large function lists
curl 'http://localhost:9002/debug/functions?limit=100&offset=0'
curl 'http://localhost:9002/debug/functions?limit=100&offset=100'

# Export all functions (compressed)
curl http://localhost:9002/debug/functions/export -o functions.json.gz
gunzip functions.json.gz
```

### JSON Schema

```json
{
    "$schema": "http://json-schema.org/draft-07/schema#",
    "definitions": {
        "CapabilitiesResponse": {
            "type": "object",
            "required": [
                "sdk_version",
                "has_dwarf_symbols"
            ],
            "properties": {
                "process_id": {
                    "type": "string"
                },
                "sdk_version": {
                    "type": "string"
                },
                "has_dwarf_symbols": {
                    "type": "boolean"
                },
                "function_count": {
                    "type": "integer"
                },
                "binary_path": {
                    "type": "string"
                },
                "binary_hash": {
                    "type": "string"
                }
            }
        },
        "FunctionMetadata": {
            "type": "object",
            "required": [
                "name",
                "offset"
            ],
            "properties": {
                "name": {
                    "type": "string"
                },
                "offset": {
                    "type": "integer"
                },
                "file": {
                    "type": "string"
                },
                "line": {
                    "type": "integer"
                },
                "arguments": {
                    "type": "array",
                    "items": {
                        "$ref": "#/definitions/Argument"
                    }
                },
                "returns": {
                    "type": "array",
                    "items": {
                        "$ref": "#/definitions/ReturnValue"
                    }
                }
            }
        }
    }
}
```

---

## Files Requiring Changes

### Critical Implementation Files

1. **`pkg/sdk/sdk.go`**
    - Remove `registerWithAgent()` and background goroutine
    - Simplify `Options` struct
    - Remove agent address dependency

2. **`pkg/sdk/debug/server.go`**
    - Replace Connect-RPC with HTTP server
    - Implement `/debug/*` HTTP handlers
    - JSON encoding/decoding

3. **`internal/agent/service_handler.go`**
    - Implement `discoverSDK()` with HTTP client
    - Update `ConnectService` to probe `:9002`
    - Replace gRPC client with HTTP client

4. **`examples/sdk-demo/main.go`**
    - Update to new SDK API
    - Reference implementation
