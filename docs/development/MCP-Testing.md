# MCP Server Testing Guide

This document describes how to test the Coral MCP server implementation (RFD 004, Phase 6).

## Unit and Integration Tests

### Running All Tests

```bash
# Run all MCP tests
make test

# Run only MCP package tests
go test -v ./internal/colony/mcp/...

# Run specific test
go test -v ./internal/colony/mcp/... -run TestServiceHealthTool
```

### Test Coverage

Current test coverage includes:

1. **Configuration Tests** (`server_test.go`):
   - MCP server configuration validation
   - Tool filtering (enabled/disabled tools)
   - Pattern matching for service filters

2. **Integration Tests** (`tools_integration_test.go`):
   - Service health tool with mock registry data
   - Service topology tool
   - Beyla metrics placeholder responses
   - Audit logging (enabled/disabled)
   - Server creation and initialization
   - Tool registration and discovery

3. **Helper Function Tests**:
   - Duration formatting
   - Pattern matching (wildcard support)

## End-to-End (E2E) Testing

### Prerequisites

1. **Running Colony**: Start a colony with MCP enabled:
   ```bash
   # Build coral binary
   make build

   # Start a colony (in one terminal)
   ./bin/coral colony start
   ```

2. **Test Agents**: Register some test agents to verify health and topology tools:
   ```bash
   # In another terminal, connect an agent
   ./bin/coral connect
   ```

### E2E Test 1: Claude Desktop Integration

This test verifies the full MCP integration with Claude Desktop.

#### Setup

1. **Generate Claude Desktop Configuration**:
   ```bash
   ./bin/coral colony mcp generate-config
   ```

   Copy the output to `~/.config/claude/claude_desktop_config.json`:
   ```json
   {
     "mcpServers": {
       "coral": {
         "command": "/absolute/path/to/coral",
         "args": ["colony", "proxy", "mcp"]
       }
     }
   }
   ```

2. **Restart Claude Desktop**: Close and reopen Claude Desktop to load the new MCP server configuration.

#### Test Steps

1. **Verify MCP Server is Connected**:
   - Open Claude Desktop
   - Look for the MCP server indicator (should show "coral" is connected)
   - If there's an error, check Claude Desktop logs at `~/Library/Logs/Claude/mcp*.log`

2. **Test Service Health Tool**:
   - In Claude Desktop, ask: "What's the health status of my services?"
   - Expected: Claude should call `coral_get_service_health` and return a health report
   - Verify the response includes:
     - Overall status (healthy/degraded/unhealthy)
     - List of connected services
     - Last seen timestamps
     - Uptime information

3. **Test Service Topology Tool**:
   - Ask: "Show me the service topology"
   - Expected: Claude calls `coral_get_service_topology`
   - Verify the response includes:
     - List of connected services
     - Mesh IP addresses
     - Note about trace-based topology (not yet implemented)

4. **Test Placeholder Tools**:
   - Ask: "Show me HTTP metrics for my API service"
   - Expected: Claude calls `coral_query_beyla_http_metrics`
   - Verify the response includes:
     - Placeholder message "No metrics available yet"
     - Reference to RFD 032 for Beyla integration

5. **Test Tool Filtering** (if configured):
   - If you've configured `enabled_tools` in colony config, verify only those tools are available
   - Ask Claude to list available tools
   - Verify only enabled tools are shown

#### Expected Results

✅ **Success Criteria**:
- Claude Desktop shows "coral" MCP server as connected
- All tool calls complete without errors
- Service health tool returns real data from registry
- Placeholder tools return appropriate "not yet implemented" messages with RFD references
- Audit logs show tool calls (if `audit_enabled: true`)

❌ **Common Issues**:
- **MCP server not connecting**: Check that `coral` binary path is absolute in config
- **"Tool not found" errors**: Verify colony is running and MCP is enabled
- **Empty health report**: Check that agents are connected to the colony
- **Permission errors**: Ensure `coral` binary has execute permissions

### E2E Test 2: Manual MCP Client Testing

Test the MCP server using the CLI tools (without Claude Desktop).

#### List Available Tools

```bash
./bin/coral colony mcp list-tools
```

Expected output:
```
Available MCP Tools for colony [your-colony-id]:

coral_get_service_health
  Get current health status of services
  Optional: service_filter

coral_get_service_topology
  Get service dependency graph
  Optional: filter, format

coral_query_events
  Query operational events
  Optional: event_type, time_range, service

[... 11 total tools ...]
```

#### Test Individual Tools

```bash
# Test health tool (fully functional)
./bin/coral colony mcp test-tool coral_get_service_health --args '{}'

# Test health tool with filter
./bin/coral colony mcp test-tool coral_get_service_health \
  --args '{"service_filter": "api*"}'

# Test topology tool
./bin/coral colony mcp test-tool coral_get_service_topology --args '{}'

# Test Beyla HTTP metrics (placeholder)
./bin/coral colony mcp test-tool coral_query_beyla_http_metrics \
  --args '{"service": "api-service", "time_range": "1h"}'
```

Expected: Each tool should return formatted text output. Placeholder tools should include "not available yet" messages.

### E2E Test 3: Custom MCP Client

Test using a custom Go program that acts as an MCP client.

#### Example Test Client

Create `test_mcp_client.go`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// MCPRequest represents an MCP JSON-RPC request.
type MCPRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

// MCPResponse represents an MCP JSON-RPC response.
type MCPResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   interface{}     `json:"error,omitempty"`
}

func main() {
	// Start coral MCP server
	cmd := exec.Command("coral", "colony", "proxy", "mcp")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		panic(err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		panic(err)
	}

	if err := cmd.Start(); err != nil {
		panic(err)
	}

	// Test 1: Call coral_get_service_health
	req := MCPRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name":      "coral_get_service_health",
			"arguments": map[string]interface{}{},
		},
	}

	// Send request
	if err := json.NewEncoder(stdin).Encode(req); err != nil {
		panic(err)
	}

	// Read response
	var resp MCPResponse
	if err := json.NewDecoder(stdout).Decode(&resp); err != nil {
		if err == io.EOF {
			fmt.Println("MCP server closed connection")
			return
		}
		panic(err)
	}

	// Print result
	fmt.Printf("Response: %s\n", string(resp.Result))

	// Cleanup
	stdin.Close()
	cmd.Wait()
}
```

Run the test:
```bash
go run test_mcp_client.go
```

Expected: The client should receive a valid MCP response with service health data.

### E2E Test 4: Audit Logging

Verify that MCP tool calls are properly audited.

#### Setup

1. Enable audit logging in colony config (`colony.yaml`):
   ```yaml
   mcp:
     audit_enabled: true
   ```

2. Restart the colony

#### Test Steps

1. Make some tool calls via Claude Desktop or CLI
2. Check colony logs for audit entries:
   ```bash
   # If using structured logging
   tail -f /path/to/colony/logs | grep "MCP tool called"
   ```

Expected audit log entry:
```json
{
  "level": "info",
  "component": "mcp",
  "tool": "coral_get_service_health",
  "args": {"service_filter": "api*"},
  "time": "2025-11-16T12:00:00Z",
  "message": "MCP tool called"
}
```

### E2E Test 5: Multiple Colonies

Test connecting to multiple colonies from Claude Desktop.

#### Setup

1. Start two colonies:
   ```bash
   # Terminal 1: Production colony
   ./bin/coral colony start --config colony-prod.yaml

   # Terminal 2: Staging colony
   ./bin/coral colony start --config colony-staging.yaml
   ```

2. Generate config for all colonies:
   ```bash
   ./bin/coral colony mcp generate-config --all-colonies
   ```

3. Add to Claude Desktop config:
   ```json
   {
     "mcpServers": {
       "coral-prod": {
         "command": "coral",
         "args": ["colony", "proxy", "mcp", "--colony", "production"]
       },
       "coral-staging": {
         "command": "coral",
         "args": ["colony", "proxy", "mcp", "--colony", "staging"]
       }
     }
   }
   ```

#### Test Steps

1. Restart Claude Desktop
2. Ask: "Compare the health of production vs staging"
3. Expected: Claude should call both MCP servers and compare results

## Performance Testing

### Latency Test

Measure MCP tool call latency:

```bash
# Time a simple health check
time ./bin/coral colony mcp test-tool coral_get_service_health --args '{}'
```

Expected: < 100ms for local colony

### Load Test

Test multiple concurrent tool calls:

```bash
# Run 100 parallel health checks
for i in {1..100}; do
  ./bin/coral colony mcp test-tool coral_get_service_health --args '{}' &
done
wait
```

Expected: All calls should complete successfully without errors.

## Troubleshooting

### MCP Server Won't Start

**Symptom**: `coral colony proxy mcp` fails or hangs

**Possible Causes**:
1. Colony is not running
   - Solution: Start colony first with `coral colony start`

2. MCP is disabled in config
   - Solution: Check `colony.yaml` and ensure `mcp.disabled: false`

3. Genkit dependency missing
   - Solution: Run `go get github.com/firebase/genkit/go/plugins/mcp@latest`

### Claude Desktop Not Connecting

**Symptom**: Claude Desktop shows MCP server as disconnected

**Possible Causes**:
1. Invalid command path
   - Solution: Use absolute path to `coral` binary in config

2. Missing permissions
   - Solution: `chmod +x /path/to/coral`

3. Colony not running
   - Solution: Start colony before connecting Claude Desktop

**Debugging**:
- Check Claude Desktop logs: `~/Library/Logs/Claude/mcp*.log` (macOS)
- Run the MCP command manually to see errors:
  ```bash
  coral colony proxy mcp
  # Should wait for stdin input
  ```

### Tools Return Errors

**Symptom**: Tool calls fail with error messages

**Possible Causes**:
1. Tool not enabled in config
   - Solution: Check `enabled_tools` list or set to empty for all tools

2. Invalid arguments
   - Solution: Check tool schema with `coral colony mcp list-tools`

3. Missing data sources (for placeholder tools)
   - Expected: Placeholder tools should return "not available yet" not errors

### No Services in Health Report

**Symptom**: `coral_get_service_health` returns "No services connected"

**Possible Causes**:
1. No agents connected to colony
   - Solution: Start agents with `coral connect`

2. Agents not registered yet
   - Solution: Wait a few seconds for agent registration

## Next Steps

After completing Phase 6 testing:

1. **Integrate Real Data Sources** (Phase 2 completion):
   - Connect Beyla metrics tools to RFD 032 (Beyla RED metrics)
   - Connect OTLP tools to RFD 025 (OTLP ingestion)
   - Implement event storage for `coral_query_events`

2. **Implement Live Debugging Tools** (Phase 3):
   - `coral_start_ebpf_collector` (requires RFD 013)
   - `coral_exec_command` (requires RFD 017)
   - `coral_shell_start` (requires RFD 026)

3. **Implement Analysis Tools** (Phase 4):
   - `coral_correlate_events`
   - `coral_compare_environments`
   - `coral_get_deployment_timeline`

## References

- RFD 004: MCP Server Integration
- RFD 025: Basic OTLP Ingestion
- RFD 032: Beyla RED Metrics Integration
- RFD 036: Beyla Distributed Tracing
- MCP Specification: https://modelcontextprotocol.io
