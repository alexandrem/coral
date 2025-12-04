# Coral SDK Demo

This example demonstrates how to integrate the Coral SDK into a Go application
to enable live debugging with uprobes.

## Features

- **SDK Integration**: Shows how to initialize the Coral SDK
- **Debug Server**: Embedded gRPC server that exposes function metadata
- **DWARF Symbols**: Extracts function offsets from debug symbols
- **Example Functions**: `ProcessPayment`, `ValidateCard`, `CalculateTotal` can
  be traced

## SDK Integration

Initialize the SDK with `RegisterService` and `EnableRuntimeMonitoring`:

```go
// Register with local Agent
err := sdk.RegisterService("payment-service", sdk.Options{
Port:      8080,
AgentAddr: "localhost:9001",
})

// Enable runtime monitoring (background registration)
if err := sdk.EnableRuntimeMonitoring(); err != nil {
log.Fatal(err)
}
```

## Building

### Option 1: With Debug Symbols (Recommended)

For full debugging support (including function arguments and return values),
build with debug symbols:

```bash
go build -gcflags="all=-N -l" -o sdk-demo main.go
```

### Option 2: Stripped Binaries (Reflection Fallback)

The SDK supports stripped binaries using Go's runtime reflection. This allows
function discovery (entry points) but **cannot** capture arguments or return
values.

```bash
# Build stripped binary (no DWARF)
go build -ldflags="-w" -o sdk-demo main.go
```

## Running the Example

```bash
```bash
./sdk-demo
```

## Running with Docker (Recommended for macOS/Windows)

To run the example in a Linux environment (required for eBPF tracing) on macOS or Windows, use Docker Compose:

```bash
docker-compose up --build
```

This will start:
1. **sdk-demo**: The example application
2. **coral-agent**: The Coral agent (privileged container for eBPF)

Once running, you can attach to the debug session from your host machine using the CLI (if connected to the colony) or by executing into the agent container.

**Expected output:**

```
{"level":"INFO","time":"...","msg":"Application started with Coral SDK (Runtime Monitoring Enabled)","component":"coral-sdk","service":"payment-service"}
{"level":"INFO","time":"...","msg":"Attempting to register with Agent...","component":"coral-sdk","service":"payment-service","agent":"localhost:9091"}
{"level":"INFO","time":"...","msg":"Successfully registered with Agent","component":"coral-sdk","service":"payment-service"}
```

## Testing Function Metadata Queries

While the app is running, you can query the SDK debug server directly using
`curl` (Connect-RPC JSON API):

```bash
# List all available functions
curl -X POST \
  http://127.0.0.1:<port>/coral.sdk.v1.SDKDebugService/ListFunctions \
  -H "Content-Type: application/json" \
  -d '{"package_pattern": "main"}'

# Get metadata for a specific function
curl -X POST \
  http://127.0.0.1:<port>/coral.sdk.v1.SDKDebugService/GetFunctionMetadata \
  -H "Content-Type: application/json" \
  -d '{"function_name": "main.ProcessPayment"}'
```

## Live Debugging with CLI

Now that the live debugging infrastructure is complete, you can use the `coral`
CLI to attach to this application.

### 1. Start Infrastructure

```bash
# Start Colony
./bin/coral colony start

# Start Agent (requires sudo for eBPF)
sudo ./bin/coral agent start
```

### 2. Attach Debug Session

Use the `coral debug` command to attach a uprobe to `ProcessPayment`.

# Attach uprobe
./bin/coral debug attach payment-service \
  --function main.ProcessPayment
```

### 3. View Events

Stream the captured events in real-time:

```bash
./bin/coral debug events <SESSION_ID> --follow
```

You should see events like:

```json
{
    "timestamp": "...",
    "event_type": "entry",
    "function_name": "main.ProcessPayment",
    ...
}
{
    "timestamp": "...",
    "event_type": "return",
    "function_name": "main.ProcessPayment",
    "duration_ns": 123456,
    ...
}
```

## Functions Available for Tracing

The example includes these instrumentable functions:

- `main.ProcessPayment` - Process payment with amount and currency
- `main.ValidateCard` - Validate credit card number
- `main.CalculateTotal` - Calculate total with tax

## Security Considerations

- Debug server listens only on **localhost** (127.0.0.1)
- Only accessible by local agents (not exposed to network)
- Read-only access to function metadata
- No ability to modify application state

## Next Steps

- **Persistence**: Store debug sessions in DuckDB (Phase 5)
- **Service Discovery**: Automatic resolution of agent/SDK addresses (Phase 5)

See [RFD 059 Live Debugging Architecture](../../RFDs/059-live-debugging-architecture.md)
for details.
