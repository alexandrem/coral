# MCP Server Validation

## Proxy jsonRPC

Make sure colony is running before.

Then launch the proxy:

```console
coral colony mcp proxy start | jq
```

Then copy/paste the following snippets in the terminal.

### Initialize

**Request**:

```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}
```

**Expected response**:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "capabilities": {
      "tools": {}
    },
    "protocolVersion": "2024-11-05",
    "serverInfo": {
      "name": "coral-my-shop-dev-af9c49",
      "version": "1.0.0"
    }
  }
}
```

### List tools

**Request**:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/list",
  "params": {}
}
```

**Expected response**:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "tools": [
      {
        "description": "",
        "inputSchema": {
          "properties": {},
          "type": "object"
        },
        "name": "coral_get_service_health"
      },
      {
        "description": "",
        "inputSchema": {
          "properties": {},
          "type": "object"
        },
        "name": "coral_get_service_topology"
      },
      {
        "description": "",
        "inputSchema": {
          "properties": {},
          "type": "object"
        },
        "name": "coral_query_events"
      },
      {
        "description": "",
        "inputSchema": {
          "properties": {},
          "type": "object"
        },
        "name": "coral_query_beyla_http_metrics"
      },
      {
        "description": "",
        "inputSchema": {
          "properties": {},
          "type": "object"
        },
        "name": "coral_query_beyla_grpc_metrics"
      },
      {
        "description": "",
        "inputSchema": {
          "properties": {},
          "type": "object"
        },
        "name": "coral_query_beyla_sql_metrics"
      },
      {
        "description": "",
        "inputSchema": {
          "properties": {},
          "type": "object"
        },
        "name": "coral_query_beyla_traces"
      },
      {
        "description": "",
        "inputSchema": {
          "properties": {},
          "type": "object"
        },
        "name": "coral_get_trace_by_id"
      },
      {
        "description": "",
        "inputSchema": {
          "properties": {},
          "type": "object"
        },
        "name": "coral_query_telemetry_spans"
      },
      {
        "description": "",
        "inputSchema": {
          "properties": {},
          "type": "object"
        },
        "name": "coral_query_telemetry_metrics"
      },
      {
        "description": "",
        "inputSchema": {
          "properties": {},
          "type": "object"
        },
        "name": "coral_query_telemetry_logs"
      }
    ]
  }
}
```
