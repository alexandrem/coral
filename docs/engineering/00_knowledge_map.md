# Engineering Knowledge Map

This repository contains the architectural and engineering knowledge distilled
from the Coral codebase. It is organized as a narrative journey through the
distributed systems principles that power the platform.

## 1. Core Foundations

- **[01_system_architecture](01_system_architecture.md)**: High-level component
  interactions and the pull-based telemetry model.
- **[02_mesh_networking_and_connectivity](02_mesh_networking_and_connectivity.md)**: WireGuard overlay networking, NAT traversal, and persistent IPAM.
- **[12_pki_infrastructure_and_trust_model](12_pki_infrastructure_and_trust_model.md)**: Hierarchical CA structure, SPIFFE identities, and fingerprint-based bootstrap.
- **[13_discovery_service_and_coordinated_enrollment](13_discovery_service_and_coordinated_enrollment.md)**: The rendezvous point, Referral Tickets, and the trust triad (Colony/Agent/Discovery).

## 2. The Observable Edge

- **[03_ebpf_instrumentation_engine](03_ebpf_instrumentation_engine.md)**:
  Zero-instrumentation monitoring, kernel-side filtering, and stateful edge
  correlation.
- **[04_binary_function_indexing_and_metadata](04_binary_function_indexing_and_metadata.md)**:
  3-tier discovery pipeline, SDK-assisted introspection, semantic enrichment
  (xxHash3 SimHash), and DuckDB symbol caching.
- **[05_otlp_ingestion_and_transformation](05_otlp_ingestion_and_transformation.md)**: Native support for
  OpenTelemetry protocol and internal data mapping.
- **[06_system_host_metrics_collection](06_system_host_metrics_collection.md)**:
  High-precision infrastructure metrics collection using `gopsutil`.
- **[14_continuous_profiling_and_dynamic_introspection](14_continuous_profiling_and_dynamic_introspection.md)**:
  Always-on CPU/Memory profiling, frame dictionary compression, and
  trace-driven triggers.

## 3. The Active Edge

- **[07_active_edge_and_remote_orchestration](07_active_edge_and_remote_orchestration.md)**: Interactive shell access,
  container namespace entry (nsenter), and session auditing.

## 4. The Data Backbone

- **[08_data_strategy_and_persistence](08_data_strategy_and_persistence.md)**:
  Strategic use of DuckDB at the edge vs. central data aggregation.
- **[09_reliable_telemetry_polling](09_reliable_telemetry_polling.md)**:
  Detailed exploration of sequence-based checkpoints and gap recovery logic.
- **[15_service_topology_and_graph_materialization](15_service_topology_and_graph_materialization.md)**:
  Two-layer service dependency graph: L7 edges from parent-span self-join on
  `beyla_traces` (RFD 092) merged with L4 edges from agent-reported TCP
  connections via `ReportConnections` RPC (RFD 033). Covers `topology_connections`
  upsert semantics, `EvidenceLayer` enum, `LAYER` column in CLI output, and
  the `--include-l4` filter flag.

## 5. Intelligence & Analysis

- **[10_typescript_investigative_engine_and_skills](10_typescript_investigative_engine_and_skills.md)**:
  LLM-driven diagnostic 'Skills' and the autonomous TypeScript reasoning
  engine.
- **[11_mcp_and_llm_interfacing](11_mcp_and_llm_interfacing.md)**: Integration
  with LLMs via Model Context Protocol and the local Agent reasoning loop.

---

## Core Philosophical Tenets

1. **Edge-First Buffering**: Use the edge's compute and storage (DuckDB) to
   minimize central ingestion pressure.
2. **Predictable Sequences**: Rely on strict ordering (`seq_id`) rather than
   best-effort timestamps for reliability.
3. **Zero-Instrumentation**: Prefer eBPF and runtime probes over
   application-level code changes.
4. **Secure by Default Mesh**: All internal traffic resides within the WireGuard
   overlay.
