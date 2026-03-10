# Binary Function Indexing and Metadata

Coral implements an "Intelligent Discovery" pipeline to map the internal
structure of distributed services without requiring manual instrumentation. This
indexing allows LLMs and operators to treat function-level triggers (uprobes) as
high-level semantic tools.

## The Discovery Pipeline (`internal/agent/debug`)

The agent maintains a persistent **Function Cache** powered by its local DuckDB.
When a service is discovered or updated, the agent executes a 3-tier fallback
strategy (RFD 065):

### 1. SDK Bulk Export (The "Green Field" path)

If the service uses the Coral Go SDK (`pkg/sdk`), the agent connects to the
SDK's HTTP debug server (`:9002`).

- **Mechanism**: The SDK iterates its own symbols and streams them via a *
  *Gzipped NDJSON** export (`/debug/functions/export`).
- **Benefit**: Zero filesystem access required; provides the most accurate "
  live" view of the running binary.

### 2. Binary Scanner (The "Brown Field" path)

If no SDK is detected but the service is running, the agent uses its **Binary
Scanner** (`internal/agent/ebpf/binaryscanner`).

- **Mechanism**: Reads symbol tables directly from the process memory or the
  binary via `/proc/<pid>/exe`.
- **Benefit**: Works on uninstrumented binaries (Go, C++, Rust).

### 3. Direct DWARF Parsing (The Fallback)

As a final fallback, the agent parses the binary on disk using the `debug/elf`
or `debug/macho` packages.

- **Offset Calculation**: Virtual addresses (`LowPC`) are converted to file
  offsets by subtracting the **Base Address**.
- **Deep Introspection**: If DWARF info is present, the agent extracts
  `TagFormalParameter` metadata, mapping arguments to stack/register offsets.

## Centralized Function Registry (`internal/colony/function_registry.go`)

While discovery happens on the edge, the Colony maintains a **Centralized
Function Registry** in its primary DuckDB to enable global semantic search and
cross-agent orchestrations.

- **Polling Logic**: The `FunctionPoller` periodically pulls the discovered
  function lists from all active agents using `ListFunctions` RPCs.
- **Change Detection**: To optimize bandwidth, the system uses a **SHA256
  Fingerprint** of the function list. Updates are only committed to the central
  registry if the binary version (`binary_hash`) or the symbol metadata has
  changed.
- **Global Table**: The Colony stores all Mesh-wide symbols in the `functions`
  table, indexed by `service_name`, `function_name`, and `binary_hash`.

## Semantic Indexing & Embeddings

A unique feature of Coral is that indexed functions are **semantically enriched
** for AI discovery using a high-performance, deterministic embedding engine (
`pkg/embedding`).

### 1. The Algorithm: xxHash3 SimHash

Unlike heavy neural-network-based embeddings, Coral uses an **xxHash3-based
SimHash**.

- **Locality Sensitive Hashing (LSH)**: Shared semantic components (e.g.,
  `handleCheckout` and `checkout_process`) result in vectors with high cosine
  similarity.
- **Deterministic & Local**: Generation takes **< 2 µs** per function and is
  fully deterministic across all nodes.
- **Dimensionality**: Every function is mapped to a **384-dimensional vector**
  of $\pm 1.0$, enabling sub-millisecond similarity search via DuckDB's
  `array_cosine_similarity`.

### 2. Search Quality

- **Recall**: Benchmarks on large-scale Go codebases show **> 90% Recall@10**.
- **Colony-Side Retrieval**: When an LLM asks a question, the Colony converts
  the query into a SimHash and performs a vector search against the **Central
  Function Registry**.

## Resilience and Change Detection

- **Binary Fingerprinting**: Every discovery cycle starts by computing a *
  *SHA256 hash** of the target binary. This hash is used as a primary key in
  both the agent's `functions_cache` and the Colony's `functions` table. The
  system only performs expensive DWARF/Symbol parsing if the binary hash has
  changed.
- **ACID Constraints**: DuckDB ensures that symbol-to-offset mappings are
  persisted transactionally, providing a stable "ground truth" for uprobe
  attachment even across agent restarts.
- **Address Space Awareness (ASLR)**: The indexer detects if a binary is
  position-independent (PIE). It persists the **Base Address** and calculates
  relative offsets to ensure that uprobes remain stable even when ASLR shifts
  the binary's load address in memory.

## Future Engineering Note: LanceDB Scaling

While the current DuckDB-backed registry is sufficient for thousands of
services, the high-density vector nature of the symbols makes it an ideal
candidate for specialized vector storage:

- **LanceDB Migration**: Moving the `functions` table from the central DuckDB to
  a **LanceDB** instance.
- **Operational Benefits**: Given that LanceDB can be queried directly via
  DuckDB, the Colony could perform unified SQL JOINs across telemetry and
  semantic function metadata while offloading the vector indexing to a
  disk-native, columnar format better optimized for massive high-dimensional
  datasets.

## Related Design Documents (RFDs)

- [**RFD 063
  **: Intelligent Function Discovery](../../RFDs/063-intelligent-function-discovery.md)
- [**RFD 065
  **: Agentless Binary Scanning](../../RFDs/065-agentless-binary-scanning.md)
- [**RFD 066**: SDK HTTP API](../../RFDs/066-sdk-http-api.md)
- [**RFD 075
  **: Hybrid Function Metadata](../../RFDs/075-hybrid-function-metadata.md)
