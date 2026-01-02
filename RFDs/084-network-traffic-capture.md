---
rfd: "084"
title: "Network Traffic Capture"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "013", "033", "083" ]
database_migrations: [ "packet_captures", "capture_metadata" ]
areas: [ "observability", "networking", "ebpf", "debugging" ]
---

# RFD 084 - Network Traffic Capture

**Status:** 🚧 Draft

## Summary

Enable on-demand network packet capture for services within the Coral mesh using
eBPF-based filtering and traditional packet capture mechanisms. Agents can
capture, filter, and stream network traffic to the colony for storage and
analysis, providing deep network-level debugging capabilities for diagnosing
connectivity issues, protocol errors, and performance problems without requiring
root access to production systems.

## Problem

**Current behavior/limitations:**

- Network-level debugging requires SSH access to production hosts and root
  privileges to run `tcpdump`.
- Traditional packet capture tools (`tcpdump`, `tshark`) are not integrated with
  Coral's service topology, requiring manual IP/port mapping.
- Captured packets must be manually transferred from production hosts for
  analysis in Wireshark or similar tools.
- No automatic filtering by Coral services: operators must construct complex
  BPF filters manually.
- Large packet captures consume significant disk space and are difficult to
  manage.
- Cannot correlate packet captures with distributed traces, metrics, or topology
  data.
- AI-driven debugging has no access to packet-level data for deep network issue
  analysis.

**Why this matters:**

- **Network debugging**: Many production issues involve network-level problems (
  TCP retransmissions, TLS handshake failures, DNS resolution issues) that
  cannot be diagnosed with application-level logs alone.
- **Protocol analysis**: Understanding wire-level protocol behavior (HTTP/2
  flow control, gRPC stream errors, database protocol quirks) requires packet
  inspection.
- **Security investigation**: Detecting unauthorized connections, data
  exfiltration, or malicious traffic requires packet-level visibility.
- **Performance troubleshooting**: Diagnosing slow requests often requires
  analyzing TCP window sizes, retransmissions, and network latency.
- **Compliance**: Some regulatory requirements mandate packet capture
  capabilities for audit trails.
- **AI context**: LLM cannot answer questions like "Why is this HTTP request
  failing?" without seeing the actual network traffic.

**Use cases affected:**

- SRE investigating intermittent connection failures between services
- Security team analyzing suspicious network traffic patterns
- Developer debugging TLS certificate issues in production
- AI operator asked "Why can't the API reach the database?" (needs packet-level
  evidence)
- Performance team analyzing TCP retransmission patterns

## Solution

Implement **network traffic capture** with eBPF-based filtering and integration
with Coral's service topology:

1. **On-demand capture**: Start/stop packet capture for specific services,
   connections, or protocols
2. **Smart filtering**: Automatically filter by Coral service names, agent IDs,
   or topology relationships
3. **Storage**: Store captured packets in colony with configurable retention
4. **Export**: Download captures in PCAP format for Wireshark analysis
5. **Live streaming**: Stream packets in real-time for live debugging
6. **MCP integration**: Expose capture capabilities to AI for automated network
   debugging

### Key Design Decisions

**1. eBPF-based capture for efficiency**

Use eBPF programs attached to network interfaces to filter packets in-kernel
before copying to userspace. This minimizes overhead compared to traditional
libpcap approaches that copy all packets to userspace before filtering.

**2. Service-aware filtering**

Integrate with service discovery (RFD 083) and topology (RFD 033) to enable
filtering by service names rather than raw IP addresses. Users request "capture
traffic between api and postgres" and Coral automatically translates to
appropriate IP/port filters.

**3. Hybrid capture approach**

Support multiple capture backends:

- **eBPF (preferred)**: Low overhead, kernel-level filtering, Linux only
- **AF_PACKET + BPF**: Traditional packet socket with BPF filters, broader
  compatibility
- **libpcap (fallback)**: Cross-platform support for macOS/Windows agents

**4. Capture sessions with lifecycle management**

Each capture is a session with start/stop control, automatic timeout, size
limits, and cleanup. Prevents runaway captures from filling disk.

**5. Streaming to colony for centralized storage**

Agents stream captured packets to colony instead of storing locally. This
enables:

- Centralized management and retention
- Access control via colony RBAC
- Correlation with other observability data
- AI access to packet data without agent access

**6. PCAP format for interoperability**

Store packets in standard PCAP format for compatibility with Wireshark, tshark,
and other network analysis tools.

### Benefits

- **Zero-SSH debugging**: Capture packets without SSH access to production
  hosts
- **Service-aware**: Filter by service names, not manual IP/port mapping
- **Centralized**: All captures stored in colony, accessible via CLI or
  dashboard
- **AI-enabled**: LLM can analyze packet captures to diagnose network issues
- **Secure**: RBAC-controlled access, no root privileges required on hosts
- **Efficient**: eBPF filtering reduces overhead compared to full packet capture
- **Integrated**: Correlate packets with traces, metrics, and topology

### Architecture Overview

```
┌─────────────────────────────────────────────────────────┐
│ User / AI Operator                                      │
│                                                         │
│  Start capture:                                         │
│    coral network capture start \                        │
│      --service api \                                    │
│      --filter "dst port 5432" \                         │
│      --duration 5m                                      │
│                                                         │
│  Download capture:                                      │
│    coral network capture download \                     │
│      <capture-id> -o api-traffic.pcap                   │
└─────────────────┬───────────────────────────────────────┘
                  │
                  │ gRPC
                  ▼
┌─────────────────────────────────────────────────────────┐
│ Colony: Capture Orchestrator                            │
│                                                         │
│  1. Resolve service "api" → agent IDs                   │
│  2. Translate to IP/port filter                         │
│  3. Send StartCapture RPC to agents                     │
│  4. Receive packet stream from agents                   │
│  5. Store in DuckDB + blob storage                      │
│  6. Serve download requests                             │
└─────────────────┬───────────────────────────────────────┘
                  │
                  │ StartCapture RPC
                  ▼
┌─────────────────────────────────────────────────────────┐
│ Agent (api-server-1)                                    │
│                                                         │
│  ┌──────────────────────────────────────────────────┐  │
│  │ Capture Manager                                  │  │
│  │                                                   │  │
│  │  1. Load eBPF program (filter packets)           │  │
│  │     → Attach to network interface (eth0, wg0)    │  │
│  │     → Filter: src/dst IP, ports, protocols       │  │
│  │                                                   │  │
│  │  2. Read packets from eBPF perf buffer           │  │
│  │     → Parse Ethernet/IP/TCP headers              │  │
│  │     → Assemble PCAP-format packets               │  │
│  │                                                   │  │
│  │  3. Stream to colony via gRPC                    │  │
│  │     → Batch packets (reduce RPC overhead)        │  │
│  │     → Compress (optional)                        │  │
│  │                                                   │  │
│  │  4. Enforce limits                               │  │
│  │     → Max duration (default: 10 minutes)         │  │
│  │     → Max size (default: 100 MB)                 │  │
│  │     → Packet count limit                         │  │
│  └──────────────────────────────────────────────────┘  │
│                                                         │
│  ┌──────────────────────────────────────────────────┐  │
│  │ eBPF Packet Filter (kernel)                      │  │
│  │                                                   │  │
│  │  Attached to: eth0, wg0                          │  │
│  │  Filter logic:                                    │  │
│  │    if (ip.src == 10.42.0.5 || ip.dst == 10.42.0.5) │  │
│  │       && tcp.port == 5432:                        │  │
│  │         copy packet to perf buffer                │  │
│  └──────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────┘
```

### Component Changes

1. **Agent (Capture Manager)**

    - **eBPF Packet Capture**: Implement eBPF program to filter and capture
      packets.
        - Attach XDP or tc (traffic control) programs to network interfaces.
        - Filter packets based on IP addresses, ports, protocols.
        - Copy matching packets to BPF perf buffer.
        - Read packets from userspace, convert to PCAP format.
    - **AF_PACKET Fallback**: Use AF_PACKET sockets with BPF filters when eBPF
      unavailable.
    - **Session Management**: Track active capture sessions.
        - Enforce max duration, size limits.
        - Auto-stop captures on timeout.
        - Support start/stop/pause operations.
    - **Packet Streaming**: Stream captured packets to colony.
        - Batch packets into chunks (e.g., 1000 packets or 1 MB).
        - Optionally compress before streaming.
        - Handle backpressure (if colony can't keep up, drop packets or pause).
    - **Interface Selection**: Automatically detect relevant network interfaces.
        - Capture on host network interface (eth0, ens192, etc.).
        - Capture on WireGuard interface (wg0) for mesh traffic.
        - Support multiple interfaces simultaneously.

2. **Colony (Capture Orchestrator)**

    - **Service-to-IP Translation**: Resolve service names to IP addresses.
        - Query service IP registry (RFD 083) for service → actual service IPs.
        - Service IPs are application IPs (host/container/pod), not mesh IPs.
        - Translate service names to BPF filter expressions.
    - **Capture Coordination**: Orchestrate captures across multiple agents.
        - Support capturing traffic between two services (both sides).
        - Merge packet streams from multiple agents.
        - Correlate packets by timestamp.
    - **Storage**: Store captured packets with metadata.
        - PCAP files stored in blob storage (filesystem or S3-compatible).
        - Metadata in DuckDB (capture ID, service, filters, timestamps).
        - Configurable retention (default: 7 days).
    - **API Handlers**: Expose capture operations via gRPC.
        - StartCapture, StopCapture, ListCaptures, DownloadCapture.
        - Stream packets in real-time (LiveCapture RPC).
    - **Access Control**: RBAC for capture operations.
        - Restrict who can start captures (privileged operation).
        - Audit log all capture requests.

3. **CLI (`coral network capture`)**

    - **`coral network capture start`**: Start a new capture session.
        - Filter by service, agent, protocol, port.
        - Set duration and size limits.
        - Return capture ID for later retrieval.
    - **`coral network capture stop`**: Stop an active capture.
    - **`coral network capture list`**: List all captures (active and stored).
    - **`coral network capture download`**: Download capture as PCAP file.
    - **`coral network capture stream`**: Live stream packets to terminal (for
      real-time debugging).
    - **`coral network capture delete`**: Delete stored capture.

4. **MCP Integration (RFD 004)**

    - New MCP tools for AI-driven packet capture:
        - `coral_start_capture`: Start packet capture for debugging.
        - `coral_get_capture`: Retrieve capture summary and statistics.
        - `coral_analyze_capture`: Analyze PCAP for common issues (
          retransmissions, errors).
    - Enable AI queries like:
        - "Capture traffic between api and database for 2 minutes"
        - "Show me the TLS handshake between frontend and api"
        - "Why are there so many TCP retransmissions?"

**Configuration Example:**

```yaml
# agent-config.yaml
packet_capture:
    enabled: true

    # Capture backends (priority order)
    backends:
        -   type: ebpf
            enabled: true
            config:
                # XDP or tc (traffic control)
                attach_mode: tc
                # Interfaces to monitor
                interfaces: [ eth0, wg0 ]

        -   type: af_packet
            enabled: true
            config:
                # BPF filter support
                bpf_jit: true

        -   type: libpcap
            enabled: true
            # Fallback for non-Linux

    # Capture limits (per session)
    limits:
        max_duration: 10m
        max_size: 100MB
        max_packets: 1000000
        max_concurrent_captures: 5  # per agent

    # Streaming
    streaming:
        batch_size: 1000  # packets per RPC
        compression: gzip
        buffer_size: 10MB

# colony-config.yaml
packet_capture:
    # Storage
    storage:
        backend: filesystem  # or s3
        path: /var/lib/coral/captures
        retention: 7d

    # Limits (global)
    limits:
        max_concurrent_captures: 50
        max_capture_size: 1GB

    # Access control
    rbac:
        # Require privilege for capture operations
        require_privilege: capture:write
```

## Implementation Plan

### Phase 1: Foundation & Protobuf

- [ ] Define protobuf messages (`CaptureRequest`, `PacketChunk`, `CaptureMetadata`)
- [ ] Create DuckDB schema for capture metadata
- [ ] Define agent → colony streaming RPC for packet data
- [ ] Define colony → agent control RPC (start/stop)

### Phase 2: Agent eBPF Capture

- [ ] Implement eBPF program for packet filtering (tc/XDP)
- [ ] Attach eBPF to network interfaces (eth0, wg0)
- [ ] Read packets from BPF perf buffer
- [ ] Convert raw packets to PCAP format
- [ ] Implement session management (start/stop/limits)

### Phase 3: Agent Fallback Capture

- [ ] Implement AF_PACKET capture with BPF filters
- [ ] Implement libpcap-based capture (cross-platform)
- [ ] Add backend selection logic (eBPF → AF_PACKET → libpcap)

### Phase 4: Packet Streaming

- [ ] Implement packet batching and compression
- [ ] Stream packets to colony via gRPC
- [ ] Handle backpressure (pause/resume)
- [ ] Implement network error handling and retry

### Phase 5: Colony Storage & Retrieval

- [ ] Implement blob storage for PCAP files (filesystem)
- [ ] Store capture metadata in DuckDB
- [ ] Implement StartCapture RPC handler
- [ ] Implement StopCapture RPC handler
- [ ] Implement DownloadCapture RPC handler
- [ ] Add retention and cleanup for old captures

### Phase 6: Service-Aware Filtering

- [ ] Integrate with service registry (RFD 083)
- [ ] Translate service names to IP/port filters
- [ ] Support topology-based filtering ("between service A and B")
- [ ] Generate BPF filter expressions from high-level queries

### Phase 7: CLI Implementation

- [ ] Implement `coral network capture start` command
- [ ] Implement `coral network capture stop` command
- [ ] Implement `coral network capture list` command
- [ ] Implement `coral network capture download` command
- [ ] Implement `coral network capture stream` (live view)
- [ ] Implement `coral network capture delete` command

### Phase 8: MCP Integration

- [ ] Implement `coral_start_capture` MCP tool
- [ ] Implement `coral_get_capture` MCP tool
- [ ] Implement `coral_analyze_capture` MCP tool (basic analysis)
- [ ] Add packet capture context to AI debugging workflows

### Phase 9: Testing & Documentation

- [ ] Unit tests: eBPF program, packet parsing, PCAP generation
- [ ] Integration tests: end-to-end capture workflow
- [ ] Performance tests: overhead measurement, packet loss detection
- [ ] Add capture troubleshooting guide
- [ ] Document PCAP analysis best practices

## API Changes

### New Protobuf Messages

```protobuf
syntax = "proto3";
package coral.mesh.v1;

import "google/protobuf/timestamp.proto";

// Request to start packet capture
message StartCaptureRequest {
    string agent_id = 1;             // optional: specific agent
    string service_name = 2;          // optional: capture for service

    // Capture filter
    CaptureFilter filter = 3;

    // Limits
    int32 duration_seconds = 4;       // max duration (default: 300s)
    int64 max_size_bytes = 5;         // max capture size (default: 100MB)
    int64 max_packets = 6;            // max packet count

    // Options
    bool compress = 7;                // compress before streaming
    repeated string interfaces = 8;   // network interfaces (default: auto)
}

message CaptureFilter {
    // Simple filters
    repeated string src_ips = 1;
    repeated string dst_ips = 2;
    repeated uint32 src_ports = 3;
    repeated uint32 dst_ports = 4;
    repeated string protocols = 5;    // tcp, udp, icmp, etc.

    // High-level filters
    string src_service = 6;           // coral service name
    string dst_service = 7;           // coral service name

    // Raw BPF filter (advanced)
    string bpf_filter = 8;
}

message StartCaptureResponse {
    string capture_id = 1;
    google.protobuf.Timestamp started_at = 2;
    repeated string agent_ids = 3;    // agents performing capture
}

// Request to stop capture
message StopCaptureRequest {
    string capture_id = 1;
}

message StopCaptureResponse {
    bool success = 1;
    uint64 packets_captured = 2;
    uint64 bytes_captured = 3;
    google.protobuf.Timestamp stopped_at = 4;
}

// Packet chunk streamed from agent to colony
message PacketChunk {
    string capture_id = 1;
    string agent_id = 2;
    bytes data = 3;                   // PCAP-format packet data
    bool compressed = 4;
    uint64 packet_count = 5;          // packets in this chunk
}

// Capture metadata
message CaptureMetadata {
    string capture_id = 1;
    string service_name = 2;
    CaptureFilter filter = 3;
    CaptureStatus status = 4;
    google.protobuf.Timestamp started_at = 5;
    google.protobuf.Timestamp stopped_at = 6;
    uint64 packets_captured = 7;
    uint64 bytes_captured = 8;
    repeated string agent_ids = 9;
    string storage_path = 10;         // blob storage path
}

enum CaptureStatus {
    CAPTURE_STATUS_UNSPECIFIED = 0;
    CAPTURE_STATUS_STARTING = 1;
    CAPTURE_STATUS_ACTIVE = 2;
    CAPTURE_STATUS_STOPPING = 3;
    CAPTURE_STATUS_COMPLETED = 4;
    CAPTURE_STATUS_FAILED = 5;
}

// List captures
message ListCapturesRequest {
    string service_name = 1;          // optional: filter by service
    CaptureStatus status = 2;         // optional: filter by status
}

message ListCapturesResponse {
    repeated CaptureMetadata captures = 1;
}

// Download capture
message DownloadCaptureRequest {
    string capture_id = 1;
}

message DownloadCaptureResponse {
    bytes pcap_data = 1;              // PCAP file content
    // or stream via server-side streaming
}

// Stream packets to agent (control channel)
message StreamPacketsRequest {
    string capture_id = 1;
    string agent_id = 2;
}

// Agent streams packets to colony
message StreamPacketsResponse {
    PacketChunk chunk = 1;
}
```

### New RPC Endpoints

```protobuf
service ColonyService {
    // Existing RPCs...

    // Packet capture
    rpc StartCapture(StartCaptureRequest) returns (StartCaptureResponse);
    rpc StopCapture(StopCaptureRequest) returns (StopCaptureResponse);
    rpc ListCaptures(ListCapturesRequest) returns (ListCapturesResponse);
    rpc DownloadCapture(DownloadCaptureRequest) returns (stream DownloadCaptureResponse);
}

service AgentService {
    // Existing RPCs...

    // Agent receives control messages from colony
    rpc StreamCapture(StreamCaptureRequest) returns (stream PacketChunk);
}
```

### CLI Commands

```bash
# Start a capture for a specific service
$ coral network capture start --service api --duration 5m

Started capture: capture-abc123
Capturing traffic for service 'api' on 2 agents
Duration: 5 minutes
Max size: 100 MB
Filters: service=api

Press Ctrl+C to stop early

# Start capture with custom filter
$ coral network capture start \
  --service api \
  --filter "tcp and dst port 5432" \
  --duration 2m \
  --max-size 50MB

Started capture: capture-def456
Filtering: tcp and dst port 5432
Capturing PostgreSQL traffic from 'api' service

# Start capture between two services
$ coral network capture start \
  --src-service frontend \
  --dst-service api \
  --duration 3m

Started capture: capture-ghi789
Capturing traffic: frontend → api
Agents involved: 2 (hostname-web, hostname-api)

# List all captures
$ coral network capture list

PACKET CAPTURES
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

ID              SERVICE    STATUS      STARTED              PACKETS    SIZE
──────────────────────────────────────────────────────────────────────────
capture-abc123  api        active      2m ago               12,453     5.2 MB
capture-def456  api        completed   1h ago               8,921      3.8 MB
capture-ghi789  frontend   completed   3h ago               45,231     18.4 MB

# List only active captures
$ coral network capture list --active

# Stop an active capture
$ coral network capture stop capture-abc123

Stopping capture: capture-abc123
✓ Capture stopped
  Duration: 2m 15s
  Packets: 12,453
  Size: 5.2 MB

# Download capture as PCAP
$ coral network capture download capture-def456 -o api-postgres.pcap

Downloading capture: capture-def456
✓ Downloaded: api-postgres.pcap (3.8 MB)

Open with: wireshark api-postgres.pcap

# Live stream packets to terminal (similar to tcpdump)
$ coral network capture stream --service api --filter "tcp port 5432"

Streaming live packets (press Ctrl+C to stop)...

14:23:15.123456 IP 10.42.0.5.45678 > 10.42.0.8.5432: Flags [S], seq 123456, win 29200
14:23:15.123890 IP 10.42.0.8.5432 > 10.42.0.5.45678: Flags [S.], seq 789012, ack 123457
14:23:15.124123 IP 10.42.0.5.45678 > 10.42.0.8.5432: Flags [.], ack 1, win 29200
14:23:15.125456 IP 10.42.0.5.45678 > 10.42.0.8.5432: Flags [P.], seq 1:43, ack 1
   PostgreSQL startup message: database=myapp, user=appuser

# Delete old captures
$ coral network capture delete capture-ghi789

Deleted capture: capture-ghi789

# Get capture details
$ coral network capture info capture-def456

CAPTURE DETAILS
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Capture ID:   capture-def456
Service:      api
Status:       completed
Started:      2025-01-01 14:15:23 UTC
Stopped:      2025-01-01 14:17:23 UTC
Duration:     2m 0s

Packets:      8,921
Size:         3.8 MB
Agents:       hostname-api (10.42.0.5)

Filter:       tcp and dst port 5432

Storage:      /var/lib/coral/captures/capture-def456.pcap
Retention:    6 days remaining

Download:     coral network capture download capture-def456
```

### MCP Tool Examples

```json
{
    "name": "coral_start_capture",
    "description": "Start network packet capture for debugging connectivity or protocol issues",
    "inputSchema": {
        "type": "object",
        "properties": {
            "service_name": {
                "type": "string",
                "description": "Service to capture traffic for"
            },
            "filter": {
                "type": "string",
                "description": "Optional BPF filter (e.g., 'tcp port 443')"
            },
            "duration_seconds": {
                "type": "integer",
                "description": "How long to capture (default: 60s, max: 300s)",
                "default": 60
            },
            "src_service": {
                "type": "string",
                "description": "Optional: capture traffic from this service"
            },
            "dst_service": {
                "type": "string",
                "description": "Optional: capture traffic to this service"
            }
        },
        "required": ["service_name"]
    }
}
```

**AI usage example:**

```
User: "The API can't connect to the database. Can you investigate?"

Claude: I'll capture network traffic between the API and database to diagnose
the connection issue.

[Uses coral_start_capture tool with src_service="api", dst_service="postgres"]

Started packet capture (ID: capture-xyz789)
Capturing for 60 seconds...

[After capture completes, uses coral_analyze_capture tool]

I found the issue. The packet capture shows:
- API is sending SYN packets to 10.42.0.8:5432 (postgres)
- No SYN-ACK responses are being received
- This indicates the database is not listening on that port

Checking the service registry, I see the postgres service is actually listening
on port 5433, not 5432. The API configuration needs to be updated.

Would you like me to help update the API's database connection string?
```

## Testing Strategy

### Unit Tests

- eBPF program compilation and loading
- Packet parsing and PCAP format generation
- BPF filter translation (service name → IP/port)
- Session lifecycle management (start/stop/timeout)

### Integration Tests

- End-to-end capture workflow (start → stream → download)
- Multi-agent capture coordination
- Service-aware filtering (resolve service names)
- Storage and retrieval of PCAP files

### Performance Tests

- Capture overhead measurement (CPU, memory)
- Packet loss detection (compare captured vs. actual traffic)
- High-throughput scenarios (10K+ packets/sec)
- Concurrent capture sessions

### E2E Tests

- Full workflow: user requests capture → agent captures → colony stores → user
  downloads
- Live stream to terminal (real-time display)
- Integration with Wireshark (validate PCAP format)
- MCP tool usage by AI

## Security Considerations

- **Privileged operation**: Packet capture requires elevated privileges (
  CAP_NET_RAW or root). Agents must run with appropriate capabilities.
- **Data sensitivity**: Captured packets may contain sensitive data (passwords,
  API keys, PII). Implement:
    - RBAC restrictions on who can start captures
    - Encryption at rest for stored PCAP files
    - Audit logging for all capture operations
    - Automatic redaction of sensitive headers (Authorization, Cookie)
- **Resource limits**: Enforce strict limits to prevent DoS:
    - Max concurrent captures per agent
    - Max capture duration and size
    - Rate limiting on capture requests
- **Network visibility**: Captures can expose internal network topology and
  traffic patterns. Restrict access to authorized users only.

## Migration Strategy

**Deployment Steps:**

1. Deploy colony with packet capture storage and RPC handlers
2. Deploy agents with eBPF capture capability (requires CAP_NET_RAW)
3. Enable packet capture via feature flag (opt-in initially)
4. Verify captures work correctly (start → download → open in Wireshark)
5. Deploy CLI with capture commands
6. Integrate with MCP for AI access

**Rollback Plan:**

- Disable packet capture via feature flag
- Existing observability continues working (metrics, traces, logs)
- No data loss (captures are ephemeral with 7-day retention)

**Backward Compatibility:**

- Older agents without capture support: gracefully decline capture requests
- CLI handles capture unavailability with clear error message

## Future Work

**Advanced filtering** (Future - RFD TBD)

- Application-layer filtering (HTTP path, gRPC method)
- Distributed trace correlation (capture only traced requests)
- Dynamic sampling (capture 1% of traffic)

**Automated analysis** (Future - RFD TBD)

- Detect TCP retransmissions and suggest fixes
- Identify TLS handshake failures
- Detect DNS resolution issues
- Generate human-readable summaries of captures

**Container network capture** (Future - RFD TBD)

- Capture traffic within container namespaces
- Support for encrypted service meshes (Istio, Linkerd)
- Sidecar-based capture without host privileges

**Real-time anomaly detection** (Low Priority)

- Detect unusual traffic patterns during capture
- Alert on security events (port scans, data exfiltration)
- Integration with IDS/IPS systems

**Distributed capture merge** (Future - RFD TBD)

- Merge captures from multiple agents into single PCAP
- Time synchronization for accurate packet ordering
- Reconstruct end-to-end flows across service hops

## Appendix

### eBPF Packet Capture Implementation

```c
// Simplified eBPF program for packet filtering

#include <linux/bpf.h>
#include <linux/if_ether.h>
#include <linux/ip.h>
#include <linux/tcp.h>

struct {
    __uint(type, BPF_MAP_TYPE_PERF_EVENT_ARRAY);
} packet_events SEC(".maps");

SEC("tc/egress")
int capture_packets(struct __sk_buff *skb) {
    // Parse Ethernet header
    void *data = (void *)(long)skb->data;
    void *data_end = (void *)(long)skb->data_end;
    struct ethhdr *eth = data;

    if ((void *)(eth + 1) > data_end)
        return TC_ACT_OK;

    if (eth->h_proto != htons(ETH_P_IP))
        return TC_ACT_OK;  // Not IPv4

    // Parse IP header
    struct iphdr *ip = (void *)(eth + 1);
    if ((void *)(ip + 1) > data_end)
        return TC_ACT_OK;

    // Apply filter (example: capture traffic to/from specific service IP)
    // Note: Service IPs are actual container/host IPs, not mesh IPs
    __u32 target_ip = 0xac110005;  // 172.17.0.5 (container IP)
    if (ip->saddr != target_ip && ip->daddr != target_ip)
        return TC_ACT_OK;  // Not matching, skip

    // Parse TCP header (if TCP)
    if (ip->protocol == IPPROTO_TCP) {
        struct tcphdr *tcp = (void *)ip + (ip->ihl * 4);
        if ((void *)(tcp + 1) > data_end)
            return TC_ACT_OK;

        // Additional TCP filtering (e.g., port 5432)
        if (ntohs(tcp->dest) != 5432 && ntohs(tcp->source) != 5432)
            return TC_ACT_OK;
    }

    // Copy packet to perf buffer
    bpf_perf_event_output(skb, &packet_events, BPF_F_CURRENT_CPU,
                          data, skb->len);

    return TC_ACT_OK;  // Let packet continue
}
```

### PCAP File Format

Standard PCAP format (libpcap):

```
PCAP File Header:
  Magic Number: 0xa1b2c3d4 (or 0xd4c3b2a1 for swapped)
  Version Major: 2
  Version Minor: 4
  Timezone offset: 0
  Timestamp accuracy: 0
  Snapshot length: 65535
  Link-layer type: 1 (Ethernet)

Packet Record:
  Timestamp seconds: 32-bit
  Timestamp microseconds: 32-bit
  Captured length: 32-bit
  Original length: 32-bit
  Packet data: variable length
```

### Service-to-Filter Translation

**Important**: Application traffic uses actual host/container IPs, not mesh IPs.
The service IP registry (RFD 083) tracks actual IPs used by services.

```
Input: --src-service api --dst-service postgres

Translation:
  1. Query service IP registry for "api"
     → Agent: hostname-api
     → Service IPs: [172.17.0.5, 10.0.1.42]  (container IP, host IP)

  2. Query service IP registry for "postgres"
     → Agent: hostname-db
     → Service IPs: [172.17.0.8]  (container IP)
     → Port: 5432

  3. Generate BPF filter:
     (src host 172.17.0.5 and dst host 172.17.0.8 and dst port 5432) or
     (src host 10.0.1.42 and dst host 172.17.0.8 and dst port 5432) or
     (src host 172.17.0.8 and src port 5432 and dst host 172.17.0.5) or
     (src host 172.17.0.8 and src port 5432 and dst host 10.0.1.42)

  4. Deploy filter to agents:
     - hostname-api: capture egress to 172.17.0.8:5432
     - hostname-db: capture ingress from api service IPs

Note: Mesh IPs (10.42.x.x) are control plane only and never appear in
application traffic.
```

### Capture Storage Schema

```sql
-- DuckDB schema for capture metadata
CREATE TABLE packet_captures
(
    capture_id     VARCHAR PRIMARY KEY,
    service_name   VARCHAR,
    status         VARCHAR,       -- starting, active, completed, failed
    started_at     TIMESTAMPTZ NOT NULL,
    stopped_at     TIMESTAMPTZ,
    packets        BIGINT DEFAULT 0,
    bytes          BIGINT DEFAULT 0,
    agent_ids      VARCHAR[],
    storage_path   VARCHAR,       -- blob storage path
    filter_expr    VARCHAR,       -- BPF filter expression
    created_by     VARCHAR,       -- user who started capture
    retention_until TIMESTAMPTZ   -- auto-delete after this
);

CREATE INDEX idx_captures_service ON packet_captures (service_name);
CREATE INDEX idx_captures_status ON packet_captures (status);
CREATE INDEX idx_captures_started ON packet_captures (started_at DESC);

-- Capture statistics (updated during capture)
CREATE TABLE capture_stats
(
    capture_id VARCHAR PRIMARY KEY REFERENCES packet_captures (capture_id),
    protocols  MAP(VARCHAR, BIGINT),  -- tcp: 1234, udp: 567
    top_src_ips MAP(VARCHAR, BIGINT), -- IP -> packet count
    top_dst_ips MAP(VARCHAR, BIGINT),
    retransmissions BIGINT,
    errors     BIGINT,
    updated_at TIMESTAMPTZ
);
```

---

## Related RFDs

- **RFD 013**: eBPF-Based Application Introspection (eBPF infrastructure)
- **RFD 033**: Service Topology Discovery (topology context for filtering)
- **RFD 083**: Automatic Service Network Discovery (service registry for
  name-to-IP mapping)
- **RFD 004**: MCP Server Integration (AI access to packet captures)
- **RFD 043**: Shell RBAC and Approval Workflows (RBAC model for capture
  permissions)
