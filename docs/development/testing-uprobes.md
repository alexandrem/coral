# Testing Live Debugging with Uprobes

This guide explains how to test the uprobe-based live debugging functionality.

## Overview

RFD 059 enables on-demand debugging of Go applications by attaching eBPF uprobes
to specific functions. The implementation consists of:

1. **SDK** - Embedded in applications to expose function metadata
2. **Agent** - Manages eBPF uprobe collectors
3. **Colony** - Orchestrates debug sessions
4. **CLI** - User interface for debugging

## Prerequisites

### System Requirements

- **Linux**: Full eBPF support (kernel 5.8+)
- **macOS**: Limited support (eBPF code compiles but won't run)

### Build Requirements

```bash
# Install LLVM tools (for eBPF compilation)
# macOS
brew install llvm

# Linux
sudo apt-get install clang llvm

# Verify installation
clang --version
llvm-strip --version
```

### Build the Project

```bash
# Generate eBPF code and build
make build

# Verify eBPF code generation
ls -la internal/agent/ebpf/uprobe_bpf*.go
# Should see: uprobe_bpfel.go, uprobe_bpfeb.go
```

## Testing Phase 1 & 2: SDK and Uprobe Collector

### Step 1: Run the SDK Demo Application

The SDK demo application demonstrates function instrumentation:

```bash
# Build with debug symbols (required for DWARF parsing)
cd examples/sdk-demo
go build -gcflags="all=-N -l" -o sdk-demo .

# Run the demo app
./sdk-demo
```

**Expected output:**

```
{"level":"info","time":"...","message":"Application started with Coral SDK","debug_addr":"127.0.0.1:xxxxx"}
Processing payment: 99.99 USD
Validated card: ****9012
...
```

**Note the `debug_addr`** - this is the SDK debug server address (e.g.,
`127.0.0.1:50051`).

### Step 2: Query SDK Function Metadata

In another terminal, use `curl` to query the SDK (using Connect-RPC JSON API):

```bash
# No extra tools needed, just curl

# List available functions
curl -X POST \
  http://127.0.0.1:50051/coral.sdk.v1.SDKDebugService/ListFunctions \
  -H "Content-Type: application/json" \
  -d '{"package_pattern": "main"}'

# Get metadata for a specific function
curl -X POST \
  http://127.0.0.1:50051/coral.sdk.v1.SDKDebugService/GetFunctionMetadata \
  -H "Content-Type: application/json" \
  -d '{"function_name": "main.ProcessPayment"}'
```

**Expected output:**

```json
{
    "found": true,
    "metadata": {
        "name": "main.ProcessPayment",
        "binaryPath": "/path/to/sdk-demo",
        "offset": "0x4a2b40",
        "pid": 12345,
        "arguments": [
            ...
        ]
    }
}
```

### Step 3: Test Uprobe Collector Directly

Create a test program to exercise the uprobe collector:

```go
// test/uprobe_test.go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/coral-mesh/coral/internal/agent/ebpf"
    "github.com/rs/zerolog"
    "os"
)

func main() {
    logger := zerolog.New(os.Stdout).With().Timestamp().Logger()

    // Create uprobe collector
    config := &ebpf.UprobeConfig{
        ServiceName: "sdk-demo",
        FunctionName: "main.ProcessPayment",
        SDKAddr: "127.0.0.1:50051", // From Step 1
        MaxEvents: 1000,
    }

    collector, err := ebpf.NewUprobeCollector(logger, config)
    if err != nil {
        logger.Fatal().Err(err).Msg("Failed to create collector")
    }

    // Start collecting
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    if err := collector.Start(ctx); err != nil {
        logger.Fatal().Err(err).Msg("Failed to start collector")
    }
    defer collector.Stop()

    logger.Info().Msg("Collector started, collecting for 30 seconds...")

    // Wait for events
    time.Sleep(30 * time.Second)

    // Get collected events
    events, err := collector.GetEvents()
    if err != nil {
        logger.Fatal().Err(err).Msg("Failed to get events")
    }

    logger.Info().Int("event_count", len(events)).Msg("Collection complete")

    // Print event summary
    entryCount := 0
    returnCount := 0
    var totalDuration uint64

    for _, event := range events {
        if uprobeEvent := event.GetUprobeEvent(); uprobeEvent != nil {
            if uprobeEvent.EventType == "entry" {
                entryCount++
            } else {
                returnCount++
                totalDuration += uprobeEvent.DurationNs
            }
        }
    }

    if returnCount > 0 {
        avgDuration := totalDuration / uint64(returnCount)
        fmt.Printf("\nSummary:\n")
        fmt.Printf("  Entry events: %d\n", entryCount)
        fmt.Printf("  Return events: %d\n", returnCount)
        fmt.Printf("  Avg duration: %d ns (%.2f ms)\n", avgDuration, float64(avgDuration)/1e6)
    }
}
```

**Run the test:**

```bash
# Make sure sdk-demo is still running
go run test/uprobe_test.go
```

**Expected output:**

```
{"level":"info","message":"Collector started, collecting for 30 seconds..."}
{"level":"info","message":"Querying SDK for function metadata"}
{"level":"info","message":"Got function metadata from SDK","binary":"/path/to/sdk-demo","offset":4861760,"pid":12345}
{"level":"info","message":"Loaded eBPF objects"}
{"level":"info","message":"Attached uprobe to function entry"}
{"level":"info","message":"Attached uretprobe to function return"}
{"level":"info","message":"Uprobe collector started successfully"}
{"level":"info","event_count":40,"message":"Collection complete"}

Summary:
  Entry events: 20
  Return events: 20
  Avg duration: 52000000 ns (52.00 ms)
```

## Testing Phase 3: Agent Integration

### Step 1: Start the Agent with eBPF Support

```bash
# On Linux (with eBPF support)
sudo ./bin/coral agent start

# The agent will report eBPF capabilities
```

### Step 2: Test Agent Debug RPC

Use the agent's debug service to start a uprobe collector:

```bash
# Create a test request
cat > /tmp/start_uprobe.json <<EOF
{
  "agent_id": "test-agent",
  "service_name": "sdk-demo",
  "function_name": "main.ProcessPayment",
  "sdk_addr": "127.0.0.1:50051",
  "duration": "30s",
  "config": {
    "capture_args": false,
    "capture_return": false,
    "max_events": 1000
  }
}
EOF

# Send RPC to agent (assuming agent is running on port 8080)
curl -X POST \
  http://localhost:8080/coral.mesh.v1.DebugService/StartUprobeCollector \
  -H "Content-Type: application/json" \
  -d @/tmp/start_uprobe.json
```

**Expected response:**

```json
{
    "collectorId": "550e8400-e29b-41d4-a716-446655440000",
    "expiresAt": "2025-11-28T20:45:00Z",
    "supported": true
}
```

### Step 3: Query Collected Events

```bash
# Query events from the collector
cat > /tmp/query_events.json <<EOF
{
  "collector_id": "550e8400-e29b-41d4-a716-446655440000",
  "max_events": 100
}
EOF

curl -X POST \
  http://localhost:8080/coral.mesh.v1.DebugService/QueryUprobeEvents \
  -H "Content-Type: application/json" \
  -d @/tmp/query_events.json
```

## Testing Phase 3 & 4: Colony Orchestration & CLI

This section explains how to test the full end-to-end flow using the CLI and Colony.

### Step 1: Start Infrastructure

Start the Colony and Agent (on Linux):

```bash
# Terminal 1: Start Colony
./bin/coral colony start

# Terminal 2: Start Agent (sudo required for eBPF)
sudo ./bin/coral agent start
```

### Step 2: Start SDK Demo

Ensure the target application is running with the SDK:

```bash
# Terminal 3: Start SDK Demo
cd examples/sdk-demo
./sdk-demo
```

### Step 3: Start Debug Session (CLI)

Use the CLI to attach a uprobe. Note that you need to manually specify the agent ID and SDK address until service discovery is fully integrated.

```bash
# Terminal 4: CLI
./bin/coral debug attach sdk-demo \
  --function main.ProcessPayment \
  --agent-id <AGENT_ID> \
  --sdk-addr 127.0.0.1:50051
```

**Expected Output:**
```
Attaching uprobe to sdk-demo/main.ProcessPayment...
✓ Debug session started
  Session ID: <SESSION_ID>
  Expires at: 2025-11-28T...
```

### Step 4: List Sessions

Verify the session is active:

```bash
./bin/coral debug sessions
```

**Expected Output:**
```
SESSION ID      SERVICE   FUNCTION             STATUS   EXPIRES
<SESSION_ID>    sdk-demo  main.ProcessPayment  active   58s
```

### Step 5: Stream Events

Stream events in real-time:

```bash
./bin/coral debug events <SESSION_ID> --follow
```

Trigger some activity in the `sdk-demo` app (it runs in a loop), and you should see events appearing in the CLI.

### Step 6: Stop Session

Detach the uprobe session:

```bash
./bin/coral debug detach <SESSION_ID>
```

**Expected Output:**
```
Detaching session <SESSION_ID>...
✓ Debug session detached
```

## Troubleshooting

### Issue: "eBPF not supported on this system"

**Cause:** Running on macOS or Linux without eBPF support.

**Solution:**

- On macOS: This is expected. eBPF code compiles but won't run. Use Linux for
  testing.
- On Linux: Check kernel version (`uname -r`). Requires 5.8+.

### Issue: "failed to open executable: permission denied"

**Cause:** Agent doesn't have permission to attach to the process.

**Solution:**

```bash
# Run agent with sudo
sudo ./bin/coral agent start

# Or grant CAP_SYS_PTRACE capability
sudo setcap cap_sys_ptrace+ep ./bin/coral
```

### Issue: "function not found" from SDK

**Cause:** Binary not compiled with debug symbols, or function name incorrect.

**Solution:**

```bash
# Rebuild with debug symbols
go build -gcflags="all=-N -l" -o sdk-demo .

# List available functions to verify name
curl -X POST \
  http://127.0.0.1:50051/coral.sdk.v1.SDKDebugService/ListFunctions \
  -H "Content-Type: application/json" \
  -d '{"package_pattern": "main"}'
```

### Issue: "llvm-strip: executable file not found"

**Cause:** LLVM tools not in PATH.

**Solution:**

```bash
# macOS
export PATH="/usr/local/homebrew/opt/llvm/bin:$PATH"

# Linux
sudo apt-get install llvm

# Verify
which llvm-strip
```

## Verification Checklist

- [ ] SDK demo application starts and exposes debug server
- [ ] SDK responds to ListFunctions RPC
- [ ] SDK responds to GetFunctionMetadata RPC with correct offset
- [ ] Uprobe collector can attach to running process
- [ ] Events are collected (entry and return)
- [ ] Duration calculation works (return events have non-zero duration)
- [ ] Collector cleanup works (no leaked eBPF programs)
- [ ] Agent debug service accepts StartUprobeCollector RPC
- [ ] Agent debug service returns events via QueryUprobeEvents RPC
- [ ] CLI `debug attach` starts a session via Colony
- [ ] CLI `debug sessions` lists active sessions
- [ ] CLI `debug events` streams events from Colony
- [ ] CLI `debug detach` stops the session

## Next Steps

Phase 5 (Hardening) will address:

1. **Persistence**: Store debug sessions in DuckDB to survive Colony restarts.
2. **Service Discovery**: Automatically resolve `agent_id` and `sdk_addr` from the service name.
3. **Agent Pool**: Robust connection management between Colony and Agents.
4. **Security**: Audit logging and access control.

## Reference

- **RFD 059
  **: [Live Debugging Architecture](file:///Users/alex/workspace/perso/coral-io/RFDs/059-live-debugging-architecture.md)
- **SDK Example
  **: [examples/sdk-demo](file:///Users/alex/workspace/perso/coral-io/examples/sdk-demo)
- **Uprobe Collector
  **: [internal/agent/ebpf/uprobe_collector.go](file:///Users/alex/workspace/perso/coral-io/internal/agent/ebpf/uprobe_collector.go)
