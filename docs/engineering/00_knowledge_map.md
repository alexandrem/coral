# Engineering Knowledge Map

This repository contains the architectural and engineering knowledge distilled
from the Coral codebase. It is organized as a narrative journey through the
distributed systems principles that power the platform.

## 1. Core Foundations

- **01_system_architecture**: High-level component interactions and the
  pull-based telemetry model.
- **02_mesh_networking_and_connectivity**: WireGuard overlay networking, NAT
  traversal, and persistent IPAM.

## 2. Telemetry Collection (The Edge)

- **03_ebpf_instrumentation_engine**: Zero-instrumentation monitoring,
  kernel-side filtering, and eBPF managers.
- **04_otlp_ingestion_and_transformation**: Native support for OpenTelemetry
  protocol and internal data mapping.
- **05_system_host_metrics_collection**: High-precision infrastructure metrics
  collection using `gopsutil`.

## 3. The Data Backbone

- **06_data_strategy_and_persistence**: Strategic use of DuckDB at the edge vs.
  central data aggregation.
- **07_reliable_telemetry_polling**: Detailed exploration of sequence-based
  checkpoints and gap recovery logic.

## 4. Intelligence & Analysis

- **08_sdk_and_scripting_capabilities**: Application-level Go SDK and automated
  analysis via sandboxed TypeScript scripts.
- **09_mcp_and_llm_interfacing**: Integration with LLMs via Model Context
  Protocol and the local Agent reasoning loop.

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
