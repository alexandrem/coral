# System Architecture

## Overview: Edge Computing Architecture

Coral follows a distributed **Colony-Agent** architecture that prioritizes *
*Edge Computing**. By moving data processing and high-precision storage (DuckDB)
to the edge (Agents), the system reduces "central bottleneck" risks and
minimizes expensive data egress from target environments.

## Key Distributed Components

### Central Colony (Orchestrator)

The **Colony** acts as the control plane and central aggregator.

- **Stateful Registry**: Tracks registered agents and their health status based
  on "Last Seen" heartbeats.
- **Pull-based Scheduling**: Instead of receiving pushed streams, Colony
  schedules periodic polling cycles. This allows the Colony to control its own
  ingestion rate (backpressure management) and simplifies agent design.
- **Global Checkpoints**: Maintains `seq_id` checkpoints to ensure ordered,
  at-least-once delivery of data pulled from agents.

### Edge Agents (Data Plane)

**Agents** are lightweight but capable nodes running in the target environment.

- **Autonomous Buffering**: Uses **DuckDB** for high-performance, local
  buffering. This allows the system to survive network partitions; agents
  continue collecting data and the Colony catches up once connectivity is
  restored.
- **Zero-Trust Networking**: Participates in the WireGuard-based overlay
  network, ensuring all inter-component traffic is encrypted and authenticated.
- **Network Observation**: Continuously observes outbound L4 TCP connections
  via eBPF (Linux) or `ss`/`netstat` polling (non-Linux), streaming aggregated
  connection metrics to the Colony for infrastructure dependency discovery
  (RFD 033).

## Communication Pattern: The Pull Model

- **gRPC/ConnectRPC**: Standardized communication interface.
- **Sequence Monotonicity**: Every telemetry record is uniquely identified by a
  `seq_id` at the source. The system relies on this monotonicity rather than
  wall-clock time for synchronization across the distributed fleet.

## Engineering Note: Visit-Pattern Scalability

The current `ForEachHealthyAgent` pattern uses a sequential visitor. In
large-scale distributed systems, this must be evolved into a **Fan-out/Fan-in**
pattern or a worker pool to query thousands of agents in parallel, preventing
the slowest agent from delaying the global polling cycle.

## Related Design Documents (RFDs)

- [**RFD 001**: Discovery Service](../../RFDs/001-discovery-service.md)
- [**RFD 011**: Multi-service Agents](../../RFDs/011-multi-service-agents.md)
- [**RFD 044**: Agent ID Standardization and Routing](../../RFDs/044-agent-id-standardization-and-routing.md)
- [**RFD 048**: Agent Certificate Bootstrap](../../RFDs/048-agent-certificate-bootstrap.md)
- [**RFD 033**: Infrastructure & L4 Topology Discovery](../../RFDs/033-service-topology-discovery.md)
