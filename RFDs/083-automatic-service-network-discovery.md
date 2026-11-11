---
rfd: "083"
title: "Automatic Service Network Discovery"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: true
api_changes: true
dependencies: [ "033", "044", "053" ]
database_migrations: [ "service_registry", "network_endpoints" ]
areas: [ "observability", "networking", "service-discovery", "topology" ]
---

# RFD 083 - Automatic Service Network Discovery

**Status:** 🚧 Draft

## Summary

Enable automatic discovery of all services within the Coral mesh, distinguishing
between coral-internal traffic (service-to-service within mesh) and external
traffic (ingress/egress). Agents automatically detect services running on their
hosts and report them to the colony, which builds a comprehensive service
registry and network topology that includes ingress/egress patterns, enabling
AI-driven debugging to understand the complete application architecture without
manual configuration.

## Problem

**Current behavior/limitations:**

- Services must be manually registered via `coral connect` or static
  configuration to appear in Coral's service registry.
- No automatic detection of services already running on agent hosts (existing
  applications, databases, caches).
- Topology discovery (RFD 033) shows connections between services, but cannot
  distinguish between:
    - **Coral-internal traffic**: Service A (in mesh) → Service B (in mesh)
    - **External ingress**: Internet/Load Balancer → Service A (in mesh)
    - **External egress**: Service A (in mesh) → External API/Database
- No visibility into which services accept external traffic (ingress points) vs.
  which only serve internal requests.
- Beyla dynamic discovery (RFD 053) instruments services on configured ports,
  but doesn't automatically populate the service registry with discovered
  services.
- Users deploying Coral into existing infrastructure see incomplete topology:
  only manually connected services appear, not the full application landscape.

**Why this matters:**

- **Incomplete observability**: AI-driven debugging queries like "Why is
  checkout slow?" fail when external dependencies (payment gateway, database)
  aren't visible in the topology.
- **Manual toil**: Users must explicitly register every service, defeating
  Coral's zero-configuration promise. In large deployments (100+ services), this
  is prohibitive.
- **Missing context for AI**: LLM cannot answer "What external services does my
  app depend on?" or "Which services are internet-facing?" without complete
  ingress/egress visibility.
- **Blind spots in security**: Cannot detect unexpected egress (data
  exfiltration) or unauthorized ingress points without comprehensive network
  discovery.
- **Dynamic environments**: Container orchestration (Kubernetes, Docker Compose)
  creates/destroys services frequently. Manual registration can't keep up.
- **Onboarding friction**: New users deploy agent and see empty service
  registry, requiring extensive configuration before value is realized.

**Use cases affected:**

- AI queries: "What external APIs does my application call?" → Cannot answer
  without egress discovery
- Security auditing: "Which services are exposed to the internet?" → Requires
  ingress detection
- Dependency mapping: "If AWS S3 goes down, what breaks?" → Need external egress
  visibility
- Incident response: "Is this outage from external dependency or internal
  service?" → Requires traffic classification
- Onboarding: Deploy agent, expect automatic service detection → Currently sees
  nothing until manual registration

## Solution

Implement **automatic service network discovery** with three-layer detection:

1. **Service Discovery**: Automatically detect all listening services on agent
   hosts (TCP/UDP ports)
2. **Network Classification**: Classify traffic as coral-internal (
   mesh-to-mesh) vs. external (ingress/egress)
3. **Registry Synchronization**: Auto-populate colony service registry with
   discovered services and network patterns

Agents passively observe network activity and infer service existence from
listening sockets, active connections, and traffic patterns. Colony correlates
this data across all agents to build a complete service registry with
ingress/egress annotations.

### Key Design Decisions

**1. Passive discovery (not active probing)**

Agents observe existing network activity rather than actively probing ports or
services. This avoids generating synthetic traffic and ensures discovery
reflects actual usage patterns.

**2. Registry-based service mapping**

Services are explicitly registered via `coral connect <service>:<port>`:

- Colony maintains service registry (service name → port mapping)
- Agents receive registry updates from colony
- Agents map listening ports to registered service names
- Agents map PIDs to services (PID → listening port → registered service)

Discovery workflow:

1. User registers: `coral connect api:8080`
2. Colony adds to registry: `{port: 8080, service: "api"}`
3. Agent receives registry update
4. Agent detects listening socket on port 8080 → maps to "api"
5. Agent detects connection from PID 1234 → port 8080 → service "api"
6. Agent reports: `{source_service_name: "api", ...}`

Unregistered services are reported as "unknown-{port}" for visibility.

Future enhancements (deferred to later RFDs):

- Automatic service name inference from HTTP/gRPC traffic (Beyla)
- Kubernetes API integration (pod labels, service names)
- Container runtime integration (Docker/containerd APIs)

**3. Coral-internal traffic detection via agent-side service attribution**

Traffic classification uses **agent-side service attribution** as the primary
mechanism, with IP-based classification as a secondary signal. This handles
real-world scenarios with NAT, load balancers, and elastic IPs where network IPs
don't directly correspond to service IPs.

**Primary: Agent-side attribution**
- Agent knows which services it monitors (via `coral connect` or auto-discovery)
- Agent tags all observed connections with the owning service name/ID
- Reports: "service 'api' connected to X" not just "IP A connected to IP B"
- Works regardless of NAT, load balancers, or proxies in between

**Secondary: IP-based classification**
- Colony maintains service IP registry (actual bind IPs, container IPs)
- Used for correlation when agent attribution is unavailable
- Handles connections where only one side has an agent

**Handling network intermediaries:**
- Load balancers: Agent sees actual service, not just LB IP
- NAT gateways: Agent attribution bypasses NAT translation
- Elastic/floating IPs: Agent knows real service regardless of public IP
- Service mesh proxies: Agent sees application container, not sidecar IP

**4. Ingress detection via connection direction**

- **Ingress**: Unknown IP → Coral service IP (external client calling service)
- **Egress**: Coral service IP → Unknown IP (service calling external
  dependency)
- **Internal**: Coral service IP → Coral service IP (service-to-service
  communication)

**5. Simple auto-registration**

Automatically discovered services are added to colony registry with metadata
indicating discovery method and confidence score. This provides immediate
visibility into all services without manual configuration.

**6. Integration with topology discovery (RFD 033)**

RFD 083 and RFD 033 are complementary and work together:

- **RFD 083 (this RFD)**: Discovers services and classifies traffic as
  internal/external
    - Provides the **node set** (services) for topology graph
    - Adds **external context** (ingress/egress endpoints)
    - Builds the service IP registry for classification
- **RFD 033**: Tracks service-to-service connections within the Coral
  ecosystem
    - Provides the **edge set** (connections between services)
    - Captures connection metadata (request rates, protocols, latency)

Both RFDs share the same eBPF connection tracking infrastructure but analyze
connections at different layers. RFD 083 focuses on classifying all connections
(internal + external), while RFD 033 focuses on building topology from internal
connections.

### Benefits

- **Zero-configuration observability**: Deploy agent, immediately see all
  services and their dependencies
- **Complete topology**: Includes manually configured services + automatically
  discovered services + external endpoints
- **Ingress/egress visibility**: Understand external attack surface and
  dependency footprint
- **AI context richness**: LLM can answer questions about external dependencies,
  internet-facing services, data flows
- **Security insights**: Detect unexpected connections, identify internet-facing
  services, audit egress
- **Dynamic environment support**: Automatically track service lifecycle in
  Kubernetes/Docker
- **Onboarding acceleration**: Immediate value without extensive configuration

### Architecture Overview

```
┌─────────────────────────────────────────────────────────┐
│ Agent (host: api-server-1)                              │
│                                                         │
│  ┌──────────────────────────────────────────────────┐  │
│  │ Service Discovery Engine                         │  │
│  │                                                   │  │
│  │  1. Listening Socket Detector                    │  │
│  │     → Scan /proc/net/tcp, /proc/net/tcp6         │  │
│  │     → Found: :8080 (api), :5432 (postgres)       │  │
│  │                                                   │  │
│  │  2. Process Metadata Resolver                    │  │
│  │     → Map port 8080 → PID 1234 → "node api.js"   │  │
│  │     → Map port 5432 → PID 5678 → "postgres"      │  │
│  │                                                   │  │
│  │  3. Connection Classifier                        │  │
│  │     → 10.42.0.5:8080 → 10.42.0.8:3306 (internal) │  │
│  │     → 203.0.113.42:80 → 10.42.0.5:8080 (ingress) │  │
│  │     → 10.42.0.5:8080 → 54.230.1.1:443 (egress)   │  │
│  │                                                   │  │
│  │  4. Service Name Inference                       │  │
│  │     → HTTP Host header: "api.example.com"        │  │
│  │     → Process name: "node api.js" → "api"        │  │
│  │     → K8s labels: "app=api" (if available)       │  │
│  └──────────────────────────────────────────────────┘  │
│                          │                             │
│            Report to colony via gRPC                   │
└──────────────────────────┼─────────────────────────────┘
                           │
                           ▼
          ┌────────────────────────────────┐
          │ Colony / Service Registry      │
          │                                │
          │  Receives from agents:         │
          │    Agent A (10.42.0.5):        │
          │      - Service "api" on :8080  │
          │      - Ingress from 203.0.113.42 │
          │      - Egress to 54.230.1.1    │
          │                                │
          │    Agent B (10.42.0.8):        │
          │      - Service "mysql" on :3306│
          │      - Internal from 10.42.0.5 │
          │                                │
          │  Builds registry:              │
          │    services:                   │
          │      - api (agent A, :8080)    │
          │        ingress: [203.0.113.42] │
          │        egress: [54.230.1.1]    │
          │      - mysql (agent B, :3306)  │
          │        internal only           │
          │                                │
          │  Stores in DuckDB:             │
          │    service_registry            │
          │    network_endpoints           │
          └────────────────┬───────────────┘
                           │
                           ▼
          ┌────────────────────────────────┐
          │ CLI / Dashboard / MCP          │
          │                                │
          │  coral network services        │
          │  Service registry display      │
          │  Ingress/egress annotations    │
          │  LLM queries with context      │
          └────────────────────────────────┘
```

### Component Changes

1. **Agent (Service Discovery Engine)**

    - **Listening Socket Detector**: Scan `/proc/net/tcp`, `/proc/net/tcp6`,
      `/proc/net/udp` to find listening sockets.
        - Parse files to extract local address and port.
        - Identify inode to map to process (via `/proc/[pid]/fd/`).
        - Report all listening ports with timestamps.
    - **Service Name Mapper**: Map listening ports to registered services.
        - Receive service registry from colony (populated via `coral connect`).
        - For each listening port, lookup in registry.
        - If found: use registered service name.
        - If not found: report as "unknown-{port}".
    - **Connection Classifier**: Analyze active connections and attribute to
      services.
        - Read `/proc/net/tcp` for ESTABLISHED connections.
        - Use eBPF connection tracking (RFD 033) for real-time tracking.
        - **Service attribution**: Map connections to services via process ownership.
            - For outbound: Find process making connection → map to service.
            - For inbound: Find process accepting connection → map to service.
            - Use `/proc/net/tcp` inode → `/proc/[pid]/fd/` mapping.
        - Report connections with service attribution: "service 'api' → IP:port".
        - Include network IPs for correlation but service name is primary.
        - Colony uses service attribution for classification, with IP registry as
          fallback.
    - **Discovery Reporting**: Stream discovery events to colony.
        - Report new services (listening socket detected).
        - Report service metadata (process name, inferred service name).
        - Report ingress/egress/internal connections.
        - Batch events to avoid overwhelming colony.

2. **Colony (Service Registry & Classifier)**

    - **Service Registry**: Maintain comprehensive list of all discovered
      services.
        - Store service metadata (name, agent ID, port, protocol).
        - Track discovery method (manual, auto-detected, inferred).
        - Store confidence score for auto-discovered services.
        - Support promoting discovered services to managed status.
    - **Service IP Registry**: Maintain registry of all IPs used by
      Coral-monitored services.
        - Track listening IPs reported by agents (service bind addresses).
        - Track connection source IPs (IPs services connect from).
        - Build comprehensive set of "known service IPs" for classification.
    - **Connection Correlator**: Best-effort merging of partial attributions from
      multiple agents using heuristics.
        - Index incoming ServiceConnection reports by 5-tuple (source IP, source
          port, dest IP, dest port, protocol).
        - Attempt to correlate matching connections from different agents (
          best-effort, not guaranteed).
        - Merge source_service_name and dest_service_name fields when correlation
          succeeds.
        - Handle timing differences with configurable correlation window (default
          5s).
        - Assign confidence scores to classifications based on attribution
          completeness.
        - **Important**: Coral is not a service mesh and cannot reliably introspect
          all east-west traffic. Some INTERNAL connections may be misclassified as
          INGRESS/EGRESS. This is acceptable for debugging use cases.
    - **Network Endpoint Classifier**: Simple classification based on service
      attribution.
        - Check if IP is in service IP registry (Coral-monitored service).
        - Cross-reference unknown IPs against all agent reports.
        - Classify as ingress/egress/internal based on attribution.
    - **Ingress/Egress Aggregator**: Build network dependency map.
        - For each service, list ingress sources (external IPs calling service).
        - For each service, list egress destinations (external IPs service
          calls).
    - **Storage**: Persist service registry and network endpoints in DuckDB.
        - `service_registry` table: Discovered services with metadata.
        - `network_endpoints` table: Ingress/egress endpoints with
          classifications.
    - **API Handlers**: Expose service registry via gRPC for CLI.
        - List all services (manual + discovered).
        - Filter by discovery method, ingress/egress, agent.

3. **CLI**

    - **`coral network services` command**: Display all discovered services.
        - Show service name, agent, port, discovery method, confidence.
        - Annotate with ingress/egress indicators.
        - Filter by agent, discovery method, or network classification.
    - **`coral network ingress` command**: List all ingress endpoints.
        - Show external IPs calling into mesh.
        - Group by service being accessed.
        - Display connection counts and confidence scores.
    - **`coral network egress` command**: List all egress endpoints.
        - Show external IPs called from mesh.
        - Group by service making calls.
        - Display connection counts and confidence scores.
    - **`coral network topology` command**: Enhanced topology view with ingress/egress.
        - Show internal services (nodes).
        - Show external endpoints.
        - Indicate ingress/egress connections with confidence scores.

**Configuration Example:**

```yaml
# agent-config.yaml
service_discovery:
    enabled: true

    # Discovery methods
    listening_sockets:
        enabled: true
        scan_interval: 30s
        protocols: [ tcp, udp ]

    connection_tracking:
        enabled: true
        backend: ebpf  # or "procfs" for fallback

    # Service registry synced from colony
    # (populated when users run `coral connect <service>:<port>`)
    # Agent receives this registry and maps listening ports to service names

    # Reporting
    report_interval: 30s
```

## Implementation Plan

### Phase 1: Foundation & Data Model

- [ ] Define protobuf messages for service discovery (`DiscoveredService`,
  `NetworkEndpoint`)
- [ ] Create DuckDB schema (`service_registry`, `network_endpoints`)
- [ ] Define agent → colony streaming RPC for discovery events
- [ ] Implement service IP registry in colony (track all IPs used by services)

### Phase 2: Agent Listening Socket Detection

- [ ] Implement `/proc/net/tcp` parser for listening sockets
- [ ] Implement `/proc/net/tcp6` and `/proc/net/udp` parsers
- [ ] Map socket inodes to PIDs via `/proc/[pid]/fd/`
- [ ] Report listening ports to colony
- [ ] Add unit tests for procfs parsing

### Phase 3: Service Name Mapping

- [ ] Agent receives service registry from colony (services registered via
  `coral connect`)
- [ ] For each listening port, lookup port in service registry
- [ ] Map port to registered service name, or "unknown-{port}" if unregistered
- [ ] Track which services are registered vs discovered

### Phase 4: Agent-Side Service Attribution

- [ ] Implement connection → process mapping via `/proc/[pid]/fd/` inode lookup
- [ ] Map process to service using local service registry
- [ ] Tag ServiceConnection reports with source/dest service names
- [ ] Integrate with eBPF connection tracking from RFD 033 (shared infrastructure)
- [ ] Report connections with service attribution to colony

### Phase 5: Cross-Node Correlation & Classification

- [ ] Implement 5-tuple indexing for connection correlation
- [ ] Implement best-effort correlation with configurable time window (default 5s)
- [ ] Merge partial attributions from multiple agents
- [ ] Assign confidence scores based on attribution completeness
- [ ] Classify connections as ingress/egress/internal with confidence
- [ ] Feed internal connections to RFD 033 topology builder

### Phase 6: Colony Service Registry

- [ ] Implement in-memory service registry with DuckDB persistence
- [ ] Store discovery metadata (method, confidence, timestamp)
- [ ] Aggregate ingress/egress endpoints per service
- [ ] Add retention and cleanup for stale services

### Phase 7: CLI

- [ ] Implement `coral network services` command
- [ ] Implement `coral network ingress` command
- [ ] Implement `coral network egress` command
- [ ] Implement `coral network topology` command with ingress/egress

### Phase 8: Testing & Documentation

- [ ] Unit tests: procfs parsing, classification logic
- [ ] Integration tests: multi-agent discovery
- [ ] E2E tests: full discovery workflow (agent → colony → CLI)
- [ ] Add service discovery troubleshooting guide
- [ ] Update USER-EXPERIENCE.md with discovery examples

## API Changes

### New Protobuf Messages

```protobuf
syntax = "proto3";
package coral.mesh.v1;

import "google/protobuf/timestamp.proto";

// Discovered service reported by agent
message DiscoveredService {
    string agent_id = 1;
    string service_name = 2;        // inferred or manual
    uint32 port = 3;
    Protocol protocol = 4;           // tcp, udp, http, grpc

    // Discovery metadata
    DiscoveryMethod discovery_method = 5;
    float confidence = 6;            // 0.0 - 1.0
    google.protobuf.Timestamp discovered_at = 7;

    // Process metadata
    ProcessInfo process = 8;
}

enum DiscoveryMethod {
    DISCOVERY_METHOD_UNSPECIFIED = 0;
    DISCOVERY_METHOD_MANUAL = 1;         // coral connect
    DISCOVERY_METHOD_LISTENING_SOCKET = 2;
    DISCOVERY_METHOD_CONNECTION_TRACKING = 3;
    DISCOVERY_METHOD_TRAFFIC_INSPECTION = 4;
    DISCOVERY_METHOD_KUBERNETES_API = 5;
}

message ProcessInfo {
    int32 pid = 1;
    string command_line = 2;
    string executable_path = 3;
    map<string, string> environment = 4;  // subset of env vars
}

// Network endpoint (ingress/egress)
message NetworkEndpoint {
    string ip_address = 1;
    uint32 port = 2;
    EndpointType type = 3;
    string resolved_name = 4;        // DNS or cloud provider name
    google.protobuf.Timestamp first_seen = 5;
    google.protobuf.Timestamp last_seen = 6;
}

enum EndpointType {
    ENDPOINT_TYPE_UNSPECIFIED = 0;
    ENDPOINT_TYPE_INGRESS = 1;       // external → mesh
    ENDPOINT_TYPE_EGRESS = 2;        // mesh → external
    ENDPOINT_TYPE_INTERNAL = 3;      // mesh → mesh
}

// Connection report with service attribution
message ServiceConnection {
    string agent_id = 1;
    string source_service_name = 2;  // service making the connection (if known)
    string dest_service_name = 3;    // service receiving (if known)

    // Network-level connection details
    string source_ip = 4;
    uint32 source_port = 5;
    string dest_ip = 6;
    uint32 dest_port = 7;
    Protocol protocol = 8;

    // Process attribution (how we determined service ownership)
    int32 source_pid = 9;            // process owning this connection
    string attribution_method = 10;   // "process_fd", "listening_socket", "config"

    // Classification metadata
    EndpointType classification = 11;  // ingress, egress, internal (set by colony)
    float confidence = 12;             // 0.0-1.0 confidence in classification
    bool correlation_merged = 13;      // true if merged from multiple agent reports

    google.protobuf.Timestamp timestamp = 14;
}

// Service-to-endpoint relationship (aggregated view)
message ServiceEndpoint {
    string service_name = 1;
    string agent_id = 2;
    NetworkEndpoint endpoint = 3;
    EndpointType endpoint_type = 4;
    uint64 connection_count = 5;     // aggregated
    google.protobuf.Timestamp last_seen = 6;
}

// Request to list all services
message ListServicesRequest {
    string agent_id = 1;             // optional: filter by agent
    DiscoveryMethod discovery_method = 2;  // optional: filter by method
    bool include_discovered = 3;     // include auto-discovered services
}

message ListServicesResponse {
    repeated DiscoveredService services = 1;
}

// Request to get ingress/egress for service
message GetServiceEndpointsRequest {
    string service_name = 1;
    EndpointType endpoint_type = 2;  // ingress, egress, or both
}

message GetServiceEndpointsResponse {
    string service_name = 1;
    repeated ServiceEndpoint endpoints = 2;
}

// Streaming RPC for agents to report discoveries
message ReportDiscoveryRequest {
    string agent_id = 1;
    repeated DiscoveredService services = 2;
    repeated ServiceConnection connections = 3;  // connections with service attribution
}

message ReportDiscoveryResponse {
    uint32 accepted_services = 1;
    uint32 accepted_connections = 2;
}
```

### New RPC Endpoints

```protobuf
service ColonyService {
    // Existing RPCs...

    // Service discovery
    rpc ListServices(ListServicesRequest) returns (ListServicesResponse);
    rpc GetServiceEndpoints(GetServiceEndpointsRequest) returns (GetServiceEndpointsResponse);
    rpc ReportDiscovery(stream ReportDiscoveryRequest) returns (ReportDiscoveryResponse);
}
```

### CLI Commands

```bash
# List all services (manual + discovered)
$ coral network services

DISCOVERED SERVICES
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

SERVICE         AGENT            PORT    METHOD              INGRESS  EGRESS  STATUS
────────────────────────────────────────────────────────────────────────────────────
api             hostname-api     8080    manual              yes      yes     managed
frontend        hostname-web     3000    manual              yes      no      managed
postgres        hostname-db      5432    listening_socket    no       no      discovered
redis           hostname-cache   6379    listening_socket    no       no      discovered
worker          hostname-worker  -       traffic_inspection  no       yes     discovered
prometheus      hostname-mon     9090    listening_socket    yes      no      discovered

Total: 6 services (2 managed, 4 discovered)

# List ingress endpoints
$ coral network ingress

INGRESS ENDPOINTS
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

SERVICE         EXTERNAL IP          RESOLVED NAME                   CONNECTIONS
────────────────────────────────────────────────────────────────────────────────
api             203.0.113.42         lb-prod.example.com             1,245
api             198.51.100.15        lb-staging.example.com          342
frontend        203.0.113.42         lb-prod.example.com             5,678
prometheus      192.0.2.10           monitoring.corp.internal        23

Total: 4 external sources

# List egress endpoints
$ coral network egress

EGRESS ENDPOINTS
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

SERVICE         EXTERNAL IP          RESOLVED NAME                   CONNECTIONS
────────────────────────────────────────────────────────────────────────────────
api             54.230.1.1:443       d111111abcdef8.cloudfront.net   234
api             52.94.236.248:443    s3.amazonaws.com                127
worker          34.120.127.130:443   storage.googleapis.com          89
worker          13.107.42.14:443     api.stripe.com                  45
api             151.101.1.140:443    api.github.com                  12

Total: 5 external dependencies

# Filter services by discovery method
$ coral network services --discovered

# Promote discovered service to managed
$ coral connect postgres:5432
✓ Service 'postgres' promoted to managed
  Previously discovered via: listening_socket
  Now managed with health monitoring enabled

# Show topology with ingress/egress
$ coral network topology

SERVICE TOPOLOGY (with external dependencies)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

[INGRESS]
  lb-prod.example.com (203.0.113.42)
    → frontend (10.100.0.7)              [5,678 req/min, HTTP]
    → api (10.100.0.5)                   [1,245 req/min, HTTP]

[INTERNAL]
  frontend (10.100.0.7)
    → api (10.100.0.5)                   [45 req/min, HTTP]

  api (10.100.0.5)
    → worker (10.100.0.6)                [18 req/min, HTTP]
    → redis (10.100.0.9)                 [156 ops/min, Redis]
    → postgres (10.100.0.8)              [42 queries/min, PostgreSQL]

  worker (10.100.0.6)
    → postgres (10.100.0.8)              [12 queries/min, PostgreSQL]

[EGRESS]
  api (10.100.0.5)
    → cloudfront.net (54.230.1.1:443)    [234 req/min, HTTPS]
    → s3.amazonaws.com (52.94.236.248:443) [127 req/min, HTTPS]
    → github.com (151.101.1.140:443)     [12 req/min, HTTPS]

  worker (10.100.0.6)
    → storage.googleapis.com (34.120.127.130:443) [89 uploads/min, HTTPS]
    → stripe.com (13.107.42.14:443)      [45 API calls/min, HTTPS]

Internal Services: 5
External Ingress: 2 sources
External Egress: 5 destinations
```

### Configuration Changes

**Agent config** (`agent-config.yaml`):

- New `service_discovery` section
- `service_discovery.methods` list (listening_sockets, connection_tracking,
  traffic_inspection)
- `service_discovery.name_inference` for port mapping and process patterns
- `service_discovery.network` for mesh CIDR and external IP resolution

**Colony config** (`colony-config.yaml`):

```yaml
service_discovery:
    # Retention for discovered services
    retention: 30d

    # How often to clean up stale services
    staleness_threshold: 1h

    # External IP resolution
    external_resolution:
        enabled: true
        dns_cache_ttl: 1h
        cloud_provider_detection: true

    # Auto-promotion rules
    auto_promotion:
        # Automatically promote services with high confidence
        enabled: false
        min_confidence: 0.9
        min_observation_time: 5m
```

## Testing Strategy

### Unit Tests

- Procfs parsing (`/proc/net/tcp`, `/proc/net/tcp6`, `/proc/net/udp`)
- Process metadata extraction (cmdline, exe, environ)
- Service name inference (port mapping, process patterns)
- Network classification (ingress/egress/internal detection)
- Service IP registry lookups (is IP in known service set)

### Integration Tests

- Multi-agent service discovery (3+ agents reporting services)
- Ingress/egress classification across agents
- External endpoint resolution (DNS lookup, cloud provider detection)
- Service promotion workflow (discovered → managed)

### E2E Tests

- Full workflow: agent discovers service → reports to colony → appears in CLI
- Ingress detection: external client connects → appears in ingress list
- Egress detection: service calls external API → appears in egress list
- Topology integration: discovered services appear in topology graph with
  ingress/egress annotations

## System Requirements

### Linux Capabilities

Both RFD 083 and RFD 033 require specific Linux capabilities to access network
and process information. Agents must run with elevated privileges.

**Required for RFD 083 (Service Discovery & Classification):**

1. **CAP_SYS_PTRACE** - Access other processes' file descriptors
   - Required to read `/proc/[pid]/fd/` for socket inode mapping
   - Maps connections to processes: socket inode → PID → service
   - Alternative: Run agent as root (not recommended for production)

2. **CAP_NET_ADMIN** - Read network connection state
   - Required to read `/proc/net/tcp`, `/proc/net/tcp6`, `/proc/net/udp`
   - Lists all listening sockets and established connections
   - Note: These files are world-readable on most systems, but capability
     ensures consistent access

3. **CAP_BPF** + **CAP_PERFMON** (Linux 5.8+) - eBPF programs
   - Required for real-time connection tracking (shared with RFD 033)
   - Allows loading eBPF programs for connection event capture
   - On older kernels: **CAP_SYS_ADMIN** required instead

**Required for RFD 033 (Topology Discovery via eBPF):**

1. **CAP_BPF** + **CAP_PERFMON** (Linux 5.8+) - eBPF programs
   - Required to hook `tcp_v4_connect`, `tcp_v6_connect` syscalls
   - Captures connection lifecycle events (connect, close)
   - On older kernels: **CAP_SYS_ADMIN** required instead

2. **CAP_NET_ADMIN** - Network introspection
   - Read connection state and network statistics
   - Access network namespace information

**Deployment Options:**

1. **Recommended**: Run agent with minimal capabilities
   ```bash
   # systemd service with capabilities
   [Service]
   CapabilityBoundingSet=CAP_SYS_PTRACE CAP_NET_ADMIN CAP_BPF CAP_PERFMON
   AmbientCapabilities=CAP_SYS_PTRACE CAP_NET_ADMIN CAP_BPF CAP_PERFMON
   ```

2. **Alternative**: Run as root (simpler but less secure)
   ```bash
   # Not recommended for production
   User=root
   ```

3. **Container deployment**: Grant capabilities via Docker/Kubernetes
   ```yaml
   # Docker
   docker run --cap-add=SYS_PTRACE --cap-add=NET_ADMIN --cap-add=BPF ...

   # Kubernetes
   securityContext:
     capabilities:
       add:
       - SYS_PTRACE
       - NET_ADMIN
       - BPF
       - PERFMON
   ```

**Kernel version compatibility:**

- **Linux 4.4+**: Basic support (requires CAP_SYS_ADMIN for eBPF)
- **Linux 5.8+**: Recommended (supports CAP_BPF and CAP_PERFMON for fine-grained
  permissions)
- **Linux 5.13+**: Full support for all eBPF features

**Security implications:**

- **CAP_SYS_PTRACE**: Allows reading file descriptors of all processes. This is
  necessary for PID-to-service mapping but grants significant inspection
  capabilities.
- **CAP_BPF**: Allows loading eBPF programs. Modern kernels provide fine-grained
  control, but older kernels require CAP_SYS_ADMIN (very broad).
- **Mitigation**: Deploy agents in trusted environments only. Use network
  namespaces and cgroups to limit blast radius.

## Security Considerations

- **Listening socket visibility**: Agents can see all listening sockets on
  host. Ensure agents run in appropriate trust boundaries.
- **Process metadata exposure**: Command lines and environment variables may
  contain sensitive information (API keys). Filter sensitive env vars before
  reporting to colony.
- **External IP disclosure**: Egress endpoints reveal external dependencies.
  Consider RBAC restrictions on who can view egress list.
- **Network classification accuracy**: Service IP registry must be kept
  up-to-date to accurately classify traffic. Handle dynamic IP changes (
  container restarts, DHCP).
- **DNS privacy**: Reverse DNS lookups may leak information to DNS servers.
  Support private DNS servers for resolution.

## Migration Strategy

**Deployment Steps:**

1. Deploy colony with service discovery schema (new DuckDB tables)
2. Deploy agents with service discovery enabled (opt-in initially)
3. Verify discovered services appear in colony registry
4. Enable auto-promotion rules (optional)
5. Deploy CLI with new service discovery commands
6. Integrate with dashboard and MCP tools

**Rollback Plan:**

- Disable service discovery via feature flag
- Existing manual service registration continues working
- Service discovery tables can remain in DuckDB (no migration required)

**Backward Compatibility:**

- Older agents without service discovery: continue reporting manually registered
  services only
- CLI gracefully handles empty discovered service list
- Manual service registration always takes precedence over auto-discovery

## Future Work

The following features are deferred to future RFDs to keep RFD 083 focused on the
core discovery and classification mechanism.

**Traffic-based service name inference** (Future - RFD TBD)

- Extract service names from HTTP Host headers via Beyla eBPF tracing
- Extract gRPC service names from gRPC metadata
- Use traffic patterns to improve service naming confidence
- Integration with OpenTelemetry for richer metadata

**Kubernetes integration** (Future - RFD TBD)

- Discover services from Kubernetes API (Services, Endpoints, Pods)
- Extract service names from pod labels (app, service, etc.)
- Track pod lifecycle and update service registry dynamically
- Map K8s Services to Coral services
- Integration with service mesh (Istio, Linkerd sidecar detection)

**Container runtime integration** (Future - RFD TBD)

- Discover services from container runtime APIs (Docker, containerd)
- Extract service names from container labels
- Track container lifecycle (created, started, stopped)
- Map container networks to mesh topology

**External endpoint enrichment** (Future - RFD TBD)

- Reverse DNS lookup for external IPs with caching
- Cloud provider IP range detection (AWS, GCP, Azure)
- Map cloud IPs to service names (e.g., S3, CloudFront, GCS)
- SaaS API detection (Stripe, Twilio, SendGrid, etc.)
- Store resolved names in DuckDB for fast lookups

**MCP integration for network discovery** (Future - RFD TBD)

- `coral_list_services` MCP tool
- `coral_get_ingress` and `coral_get_egress` MCP tools
- `coral_get_external_dependencies` MCP tool
- Update `coral_get_topology` to include ingress/egress annotations
- Enable LLM queries: "What external services does my app use?"

**Service promotion workflows** (Future - RFD TBD)

- Auto-promotion rules (confidence threshold, observation time)
- Manual promotion via CLI (`coral connect <discovered-service>`)
- Promotion enables health monitoring, instrumentation
- Distinguish "managed" vs "discovered" services in UI

**Dashboard visualization** (Future - RFD TBD)

- Network graph with ingress/egress nodes
- Visual confidence indicators for classifications
- Interactive filtering by service, endpoint type, confidence
- Export to GraphViz, JSON, CSV

## Appendix

### Procfs Parsing Example

```
/proc/net/tcp format:
  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 ...
       ^^^^^^^^ ^^^^
       IP:Port  (hex)

Parsing:
  - local_address: 00000000:1F90 → 0.0.0.0:8080 (listening on all interfaces)
  - st: 0A → TCP_LISTEN
  - inode: 12345 → Map to process via /proc/[pid]/fd/

Process mapping:
  1. Scan /proc/[pid]/fd/ for all PIDs
  2. Find fd pointing to socket:[12345]
  3. Read /proc/[pid]/cmdline → "node api.js"
  4. Infer service name: "api"
```

### DuckDB Storage Schema

```sql
-- Discovered services
CREATE TABLE service_registry
(
    service_name      VARCHAR PRIMARY KEY,
    agent_id          VARCHAR     NOT NULL,
    port              INTEGER     NOT NULL,
    protocol          VARCHAR     NOT NULL,  -- tcp, udp, http, grpc
    discovery_method  VARCHAR     NOT NULL,  -- manual, listening_socket, etc.
    confidence        FLOAT,                 -- 0.0 - 1.0
    process_pid       INTEGER,
    process_cmdline   VARCHAR,
    process_exe       VARCHAR,
    discovered_at     TIMESTAMPTZ NOT NULL,
    last_seen         TIMESTAMPTZ NOT NULL,
    status            VARCHAR,               -- discovered, managed, stale
    UNIQUE (agent_id, port)
);

CREATE INDEX idx_service_registry_agent ON service_registry (agent_id);
CREATE INDEX idx_service_registry_discovery ON service_registry (discovery_method);
CREATE INDEX idx_service_registry_status ON service_registry (status);

-- Network endpoints (ingress/egress)
CREATE TABLE network_endpoints
(
    id            BIGSERIAL PRIMARY KEY,
    ip_address    VARCHAR     NOT NULL,
    port          INTEGER     NOT NULL,
    endpoint_type VARCHAR     NOT NULL,  -- ingress, egress, internal
    resolved_name VARCHAR,
    first_seen    TIMESTAMPTZ NOT NULL,
    last_seen     TIMESTAMPTZ NOT NULL,
    UNIQUE (ip_address, port)
);

CREATE INDEX idx_network_endpoints_type ON network_endpoints (endpoint_type);
CREATE INDEX idx_network_endpoints_last_seen ON network_endpoints (last_seen DESC);

-- Service-to-endpoint relationships
CREATE TABLE service_endpoints
(
    service_name     VARCHAR     NOT NULL,
    agent_id         VARCHAR     NOT NULL,
    endpoint_id      BIGINT      NOT NULL REFERENCES network_endpoints (id),
    endpoint_type    VARCHAR     NOT NULL,  -- ingress, egress
    connection_count BIGINT      NOT NULL DEFAULT 0,
    first_seen       TIMESTAMPTZ NOT NULL,
    last_seen        TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (service_name, endpoint_id)
);

CREATE INDEX idx_service_endpoints_service ON service_endpoints (service_name);
CREATE INDEX idx_service_endpoints_type ON service_endpoints (endpoint_type);
```

### Service Name Mapping Algorithm

```
Input: Port number, service registry (from colony)
Output: Service name

The service registry is populated when users run `coral connect <service>:<port>`.
For example:
  $ coral connect api:8080
  $ coral connect postgres:5432

This creates registry entries:
  {port: 8080, service: "api"}
  {port: 5432, service: "postgres"}

Agent mapping logic:

1. Agent receives service registry from colony
   Registry: {8080: "api", 5432: "postgres", 6379: "redis"}

2. Agent detects listening socket on port 8080
   → Lookup port 8080 in registry
   → Found: "api"
   → Return service_name="api"

3. Agent detects listening socket on port 3000
   → Lookup port 3000 in registry
   → Not found
   → Return service_name="unknown-3000"

4. Agent maps connection to PID
   → PID 1234 owns socket on port 8080
   → Port 8080 → service "api"
   → Tag connection with source_service_name="api"

Note: This eliminates complex heuristics (regex patterns, binary path parsing).
Users explicitly register services they care about via `coral connect`. Anything
else is "unknown-{port}".

Advanced inference methods (K8s labels, HTTP/gRPC traffic inspection) are
deferred to future RFDs. See "Traffic-based service name inference" in Future Work.
```

### Network Classification Algorithm

**Important**: Classification uses **agent-side service attribution** as primary
mechanism to handle NAT, load balancers, and elastic IPs. IP-based classification
is a fallback for connections where only one side has an agent.

```
Input: ServiceConnection reports from agents (include service attribution + network IPs)
Output: Classification (ingress, egress, internal)

STEP 0: CROSS-NODE CORRELATION (Colony-side, best-effort)

**Important**: Coral is not a service mesh. Correlation is heuristic-based and
best-effort. Some INTERNAL connections will be misclassified as INGRESS/EGRESS.
This is acceptable for debugging use cases.

For multi-node deployments, colony attempts to correlate partial attributions:

Example: Service A (Node 1) → Service B (Node 2)

  Agent on Node 1 reports:
    {source_service_name: "A", dest_service_name: "",
     source_ip: "10.0.1.5:45678", dest_ip: "10.0.2.8:5432"}

  Agent on Node 2 reports:
    {source_service_name: "", dest_service_name: "B",
     source_ip: "10.0.1.5:45678", dest_ip: "10.0.2.8:5432"}

  Colony attempts correlation by 5-tuple:
    - If match found within time window (5s) → merge
      source_service_name="A" (from Node 1)
      dest_service_name="B" (from Node 2)
      → Classify as INTERNAL (confidence: 0.9)

    - If no match found → classify partial view
      → Node 1 sees EGRESS: A → unknown (confidence: 0.5)
      → Node 2 sees INGRESS: unknown → B (confidence: 0.5)

Correlation limitations:
- NAT/proxies may change 5-tuple between observations → correlation fails
- Clock skew between nodes → mismatched timestamps
- Short-lived connections → complete before both agents report
- High-volume services → correlation becomes expensive
- Asymmetric routing → only one side sees traffic

PRIMARY CLASSIFICATION (Agent Attribution with Confidence):

After correlation attempt, classify using available service attributions:

1. Check source_service_name field:
   → If set: Connection attributed to Coral service (agent knows which process)
   → If empty: Source is external (no Coral agent/service)

2. Check dest_service_name field:
   → If set: Destination is Coral service (agent knows which process)
   → If empty: Destination is external

3. Classify based on service attribution with confidence scoring:
   ┌────────────────────┬──────────────────┬─────────────────┬────────────┐
   │ Source Service     │ Dest Service     │ Classification  │ Confidence │
   ├────────────────────┼──────────────────┼─────────────────┼────────────┤
   │ empty (unknown)    │ set (known)      │ INGRESS         │ 0.5-0.9    │
   │ set (known)        │ empty (unknown)  │ EGRESS          │ 0.5-0.9    │
   │ set (known)        │ set (known)      │ INTERNAL        │ 0.9        │
   │ empty (unknown)    │ empty (unknown)  │ N/A (skip)      │ -          │
   └────────────────────┴──────────────────┴─────────────────┴────────────┘

   Confidence scoring:
   - 0.9: Both endpoints attributed (merged from cross-node correlation)
   - 0.7: One endpoint attributed, correlation attempted but no match
   - 0.5: One endpoint attributed, no correlation attempted (single agent deployment)

   Note: INTERNAL with confidence < 0.9 likely indicates correlation failure.
         These connections appear as INGRESS or EGRESS but may actually be
         internal cross-node traffic.

4. Store with actual network IPs for debugging/audit:
   - INGRESS: (external_ip → service_name, confidence)
   - EGRESS: (service_name → external_ip, confidence)
   - INTERNAL: (source_service → dest_service, confidence)

FALLBACK CLASSIFICATION (IP-Based):

Used when agent attribution is unavailable (partial deployments, legacy systems):

1. Load service IP registry from colony
   → Registry contains known IPs from previous agent reports
   → Example: {172.17.0.5: "api", 172.17.0.8: "postgres"}

2. Lookup source_ip in service IP registry:
   → Found: source_service = registry[source_ip]
   → Not found: source_service = null

3. Lookup dest_ip in service IP registry:
   → Found: dest_service = registry[dest_ip]
   → Not found: dest_service = null

4. Classify using same table as above

HANDLING NETWORK INTERMEDIARIES:

Example: Load balancer scenario
  - Service 'api' behind LB at 203.0.113.42
  - Client connects to 203.0.113.42:80
  - LB forwards to actual service IP 172.17.0.5:8080

Agent report:
  {
    source_service_name: "",              // external client (unknown)
    dest_service_name: "api",             // agent knows this is 'api' service
    source_ip: "1.2.3.4",                 // actual client IP (may be NATed)
    dest_ip: "172.17.0.5",                // actual service IP (post-LB)
    ...
  }

Classification: INGRESS (unknown → known service)
  → Stored as: (1.2.3.4 → api)
  → LB IP (203.0.113.42) is transparent to classification

Example: NAT gateway scenario
  - Service 'api' makes outbound call to external API
  - NAT gateway at 203.0.113.50
  - External API sees 203.0.113.50, not actual service IP

Agent report (from 'api' agent):
  {
    source_service_name: "api",           // agent knows this is 'api'
    dest_service_name: "",                // external API (unknown)
    source_ip: "172.17.0.5",              // actual service IP (pre-NAT)
    dest_ip: "54.230.1.1",                // external API IP
    ...
  }

Classification: EGRESS (known service → unknown)
  → Stored as: (api → 54.230.1.1)
  → NAT IP (203.0.113.50) is transparent to classification

Example: Service mesh (Envoy sidecar) scenario
  - Service 'api' running in container
  - Envoy sidecar proxy at 127.0.0.1:15001
  - Actual service at 127.0.0.1:8080

Agent report:
  {
    source_service_name: "frontend",
    dest_service_name: "api",
    source_ip: "172.17.0.5:45678",        // frontend IP
    dest_ip: "127.0.0.1:15001",           // Envoy proxy IP
    ...
  }

Classification: INTERNAL (both services known)
  → Stored as: (frontend → api)
  → Proxy IP is included for debugging but doesn't affect classification
```

### Service Attribution Implementation

**How agents map connections to services:**

```
Step 1: Discover services on agent
  - Via `coral connect api:8080` (manual)
  - Via listening socket detection (automatic)
  - Via process discovery (automatic)

  Result: Agent knows "service 'api' = PID 1234"

Step 2: Observe network connections
  - Read /proc/net/tcp for active connections
  - Or use eBPF connection tracking (RFD 033)

  Result: Connection {inode: 12345, src: 172.17.0.5:45678, dst: 54.230.1.1:443}

Step 3: Map connection → process
  - Scan /proc/[pid]/fd/ for socket inodes
  - Find: /proc/1234/fd/3 → socket:[12345]

  Result: Connection 12345 belongs to PID 1234

Step 4: Map process → service
  - Lookup PID 1234 in service registry
  - Find: PID 1234 = service "api"

  Result: Connection belongs to service "api"

Step 5: Report with attribution
  {
    source_service_name: "api",        // from step 4
    dest_service_name: "",             // unknown (external)
    source_ip: "172.17.0.5",
    dest_ip: "54.230.1.1",
    source_pid: 1234,
    attribution_method: "process_fd"
  }
```

**Attribution methods:**

1. **process_fd**: Map connection inode to process FD (most reliable)
   - Works for both inbound and outbound connections
   - Requires root or CAP_SYS_PTRACE to read /proc/[pid]/fd

2. **listening_socket**: Match listening socket to service
   - For inbound connections to known ports
   - Agent knows "port 8080 → service 'api'"

3. **config**: User-provided mapping
   - Fallback when process mapping fails
   - Configured via coral.yaml or CLI flags

**Handling edge cases:**

- **Short-lived connections**: May complete before attribution
  - Use eBPF to capture connection events in real-time
  - eBPF can attribute at connection time, not post-hoc

- **Multiple services on same host**: Process-level attribution disambiguates
  - Each service runs as separate process
  - PID mapping ensures correct attribution

- **Containerized services**: Agent must access container namespaces
  - Node agent: Can see all container processes via host /proc
  - Sidecar agent: Only sees own container's processes

- **Load balancer hairpinning**: Service calls itself via LB
  - Agent sees service 'api' → LB IP → service 'api'
  - With attribution: Correctly identifies as self-call

### Cross-Node Correlation Examples

**Important**: These examples show best-case scenarios. In practice, correlation
often fails due to NAT, proxies, clock skew, or timing issues. Coral is designed
to work with partial information and provide confidence scores.

**Example 1: Successful correlation (best case)**

```
Scenario: API service (Node 1) queries Postgres (Node 2)

┌──────────────────────────────────────────────────────────────────┐
│ Node 1: api-server                                               │
│   Agent observes outbound connection from PID 1234 (api service) │
│                                                                  │
│   Report to colony:                                              │
│   {                                                              │
│     agent_id: "node1",                                           │
│     source_service_name: "api",     ← knows (local process)      │
│     dest_service_name: "",          ← doesn't know (remote)      │
│     source_ip: "10.0.1.5",                                       │
│     source_port: 45678,                                          │
│     dest_ip: "10.0.2.8",                                         │
│     dest_port: 5432,                                             │
│     protocol: TCP,                                               │
│     timestamp: "2024-01-15T10:30:00Z"                            │
│   }                                                              │
└──────────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────────┐
│ Node 2: db-server                                                │
│   Agent observes inbound connection to PID 5678 (postgres)       │
│                                                                  │
│   Report to colony:                                              │
│   {                                                              │
│     agent_id: "node2",                                           │
│     source_service_name: "",        ← doesn't know (remote)      │
│     dest_service_name: "postgres",  ← knows (local process)      │
│     source_ip: "10.0.1.5",                                       │
│     source_port: 45678,                                          │
│     dest_ip: "10.0.2.8",                                         │
│     dest_port: 5432,                                             │
│     protocol: TCP,                                               │
│     timestamp: "2024-01-15T10:30:01Z"  ← 1 sec later             │
│   }                                                              │
└──────────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────────┐
│ Colony: Connection Correlator                                    │
│                                                                  │
│   1. Index by 5-tuple: (10.0.1.5:45678, 10.0.2.8:5432, TCP)      │
│                                                                  │
│   2. Match found within 5s window                                │
│      - Merge: source="api", dest="postgres"                      │
│                                                                  │
│   3. Classify:                                                   │
│      Type: INTERNAL                                              │
│      Confidence: 0.9 (both endpoints attributed)                 │
│      correlation_merged: true                                    │
└──────────────────────────────────────────────────────────────────┘

Result: INTERNAL (confidence: 0.9)
```

**Example 2: Correlation failure (common case)**

```
Scenario: API service (Node 1) → Postgres (Node 2)
          NAT gateway between nodes changes source port

┌──────────────────────────────────────────────────────────────────┐
│ Node 1: api-server                                               │
│   Agent observes outbound connection                             │
│                                                                  │
│   Report to colony:                                              │
│   {                                                              │
│     source_service_name: "api",                                  │
│     dest_service_name: "",                                       │
│     source_ip: "10.0.1.5",                                       │
│     source_port: 45678,        ← pre-NAT port                    │
│     dest_ip: "10.0.2.8",                                         │
│     dest_port: 5432                                              │
│   }                                                              │
└──────────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────────┐
│ NAT Gateway (between nodes)                                      │
│   Performs SNAT: 10.0.1.5:45678 → 10.99.0.1:12345                │
└──────────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────────┐
│ Node 2: db-server                                                │
│   Agent observes inbound connection                              │
│                                                                  │
│   Report to colony:                                              │
│   {                                                              │
│     source_service_name: "",                                     │
│     dest_service_name: "postgres",                               │
│     source_ip: "10.99.0.1",    ← post-NAT IP (different!)        │
│     source_port: 12345,        ← post-NAT port (different!)      │
│     dest_ip: "10.0.2.8",                                         │
│     dest_port: 5432                                              │
│   }                                                              │
└──────────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────────┐
│ Colony: Connection Correlator                                    │
│                                                                  │
│   1. Index reports:                                              │
│      Key1: (10.0.1.5:45678, 10.0.2.8:5432, TCP)   ← node1        │
│      Key2: (10.99.0.1:12345, 10.0.2.8:5432, TCP)  ← node2        │
│                                                                  │
│   2. 5-tuple mismatch → correlation FAILS                        │
│                                                                  │
│   3. Classify each report separately:                            │
│      Report 1: EGRESS (api → unknown)                            │
│                Confidence: 0.7 (correlation attempted, no match) │
│                                                                  │
│      Report 2: INGRESS (unknown → postgres)                      │
│                Confidence: 0.7 (correlation attempted, no match) │
└──────────────────────────────────────────────────────────────────┘

Result: EGRESS (api → unknown, confidence: 0.7)
        INGRESS (unknown → postgres, confidence: 0.7)

Note: This INTERNAL connection is misclassified as separate INGRESS/EGRESS
      due to NAT. This is acceptable for Coral's debugging use case. The low
      confidence scores (0.7) indicate potential correlation failure.
```

**Example 3: External egress (no correlation needed)**

```
Scenario: API (Node 1) → External API (no Coral agent)

Agent report:
  {
    source_service_name: "api",
    dest_service_name: "",       ← external (no Coral agent)
    dest_ip: "54.230.1.1"
  }

Colony classification:
  Type: EGRESS (api → unknown)
  Confidence: 0.9 (external endpoint clearly identified)
  correlation_merged: false

Result: EGRESS (confidence: 0.9)
```

**Key Takeaway:**

Correlation is best-effort and often fails in real deployments. Coral relies on
confidence scores to indicate classification certainty. Lower confidence (0.5-0.7)
suggests potential misclassification due to correlation failure, which is
acceptable for debugging workflows.

### Shared Infrastructure with RFD 033

**Single eBPF pipeline, dual analysis layers:**

```
┌─────────────────────────────────────────────────────────────┐
│ Agent: eBPF Connection Tracking (shared infrastructure)    │
│                                                             │
│  ┌───────────────────────────────────────────────────────┐ │
│  │ eBPF Program (RFD 033 infrastructure)                 │ │
│  │   - Hook tcp_v4_connect, tcp_v6_connect               │ │
│  │   - Track connection lifecycle                         │ │
│  │   - Extract source IP, dest IP, ports, protocol       │ │
│  └───────────────────────────────────────────────────────┘ │
│                          │                                  │
│                          │ Connection events                │
│                          ▼                                  │
│         ┌────────────────────────────────────┐              │
│         │   Connection Event Stream          │              │
│         │                                    │              │
│         │   {src: 172.17.0.5:45678,         │              │
│         │    dst: 172.17.0.8:5432,          │              │
│         │    proto: TCP,                     │              │
│         │    state: ESTABLISHED}             │              │
│         └────────────────┬───────────────────┘              │
│                          │                                  │
│          ┌───────────────┴───────────────┐                  │
│          │                               │                  │
│          ▼                               ▼                  │
│  ┌──────────────────┐         ┌─────────────────────────┐  │
│  │ RFD 083 Layer    │         │ RFD 033 Layer           │  │
│  │ (This RFD)       │         │ (Topology)              │  │
│  │                  │         │                         │  │
│  │ • Report all IPs │         │ • Focus on internal     │  │
│  │ • Build service  │         │   connections only      │  │
│  │   IP registry    │         │ • Track request rates   │  │
│  │ • Classify:      │         │ • Build topology edges  │  │
│  │   internal/      │         │ • Capture metadata      │  │
│  │   external       │         │   (protocol, latency)   │  │
│  └──────────────────┘         └─────────────────────────┘  │
│          │                               │                  │
│          │                               │                  │
│          ▼                               ▼                  │
│  To colony:                      To colony:                 │
│  - Service IPs                   - Topology connections     │
│  - Ingress endpoints             - Connection metadata      │
│  - Egress endpoints              - Request rates            │
└─────────────────────────────────────────────────────────────┘
                          │
                          ▼
                ┌──────────────────────┐
                │ Colony: Merge Data   │
                │                      │
                │ Complete Topology:   │
                │  - Nodes (RFD 083)   │
                │  - Edges (RFD 033)   │
                │  - External (083)    │
                │  - Metrics (033)     │
                └──────────────────────┘
```

**Benefits of shared infrastructure:**

1. **Single eBPF overhead**: One set of eBPF programs instead of two
2. **Consistent data source**: Both RFDs analyze the same connection events
3. **Efficient resource usage**: Share kernel hooks and perf buffers
4. **Simpler deployment**: Deploy once, get both service discovery and topology
5. **Coherent analysis**: Service IP registry (083) informs topology
   classification (033)

**Implementation strategy:**

- RFD 033's eBPF connection tracking is the foundation
- RFD 083 extends the analysis layer with classification logic
- Agent streams connection events once, colony processes them for both purposes
- Service IP registry (RFD 083) is used by both RFDs for classification

---

## Related RFDs

### Complementary RFDs

**RFD 033: Service Topology Discovery via eBPF**

- **Relationship**: RFD 033 and RFD 083 work together to build complete
  topology
- **RFD 033 provides**: Service-to-service connections (edge set) with metadata
  (request rates, protocols)
- **RFD 083 provides**: Service discovery (node set) and external endpoint
  classification
- **Shared infrastructure**: Both use eBPF connection tracking, analyzed at
  different layers
- **Combined output**: Complete topology graph showing internal services,
  service-to-service connections, and external dependencies

**Implementation approach**: Deploy both RFDs together with a single eBPF
connection tracking pipeline feeding two analysis layers:

1. **RFD 083 layer**: Classify all connections → build service IP registry →
   identify ingress/egress
2. **RFD 033 layer**: Analyze internal connections → build service topology →
   capture connection metadata

### Supporting RFDs

- **RFD 044**: Agent ID Standardization and Routing (agent identification for
  service registry)
- **RFD 001**: Discovery Service (colony/agent discovery, different scope from
  service discovery)
- **RFD 084**: Network Traffic Capture (uses service IP registry for filtering)
- **RFD 019**: Persistent IP Allocation (manages WireGuard mesh IPs for control
  plane only; application traffic uses actual host/container IPs tracked by
  service IP registry)

**Future integrations** (deferred to later RFDs):

- Beyla for traffic-based service name inference (HTTP/gRPC)
- MCP for exposing discovery data to LLMs
- Kubernetes API for pod label extraction
