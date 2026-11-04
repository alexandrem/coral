# Function Indexing

Function indexing maps service names and code identifiers to metadata that an
operator or investigation can search. Probe attachment additionally needs the
correct process, executable, and file offset for the running binary. A cached
function name is useful for discovery, but is not sufficient evidence that a
probe can attach to the current process.

## Local discovery and the cache

The agent's `FunctionCache` stores function metadata in DuckDB, with a separate
`binary_hashes` record for each indexed service. Its discovery chain tries the
SDK HTTP export when an SDK address is available, then a binary scanner when
a PID was supplied, and finally direct binary/DWARF discovery.

The SDK export supplies gzipped NDJSON function records. The scanner works
against an accessible process executable; direct discovery uses the SDK's
metadata provider for on-disk symbols and debug information. Available metadata
depends on the binary format, stripping, permissions, and access to the correct
executable. SDK availability does not imply that stripped binaries retain every
piece of DWARF metadata.

Not every caller traverses every fallback. `EnsureIndexed` calls
`DiscoverAndCache` without a PID, so that route can use the SDK or direct binary
path but does not activate the PID-based scanner by itself. The separate
`DiscoverFunction` service used for individual probe metadata can try an SDK
and then binary scanning with the target PID. Its pprof discovery option is
present but unimplemented.

Implementation: [cache and fallback chain](../../internal/agent/debug/function_storage.go),
[direct discovery](../../internal/agent/debug/functions.go),
[probe metadata discovery](../../internal/agent/ebpf/discovery.go), and
[binary scanner](../../internal/agent/ebpf/binaryscanner/).

## Lazy indexing and visibility

`EnsureIndexed` first checks whether the service's cache is missing or stale.
It discovers and stores the function list when an update is needed. Indexing is
triggered by an explicit need rather than every newly observed process: debug
session startup can request it in the background, and `GetFunctions` schedules
it for eligible watched services before reading the current cache.

This makes the first result potentially incomplete. Starting asynchronous
indexing and immediately reading the table does not wait for a newly indexed
list to become visible. The Colony's later function poll can pick it up.
Automatically observed entries without a monitor are not indexed through that
watched-service path. [Service discovery](service-discovery.md) explains the
observed/watched distinction, including executable-pattern services.

Implementation: [session startup](../../internal/agent/debug/session.go),
[GetFunctions](../../internal/agent/service_handler.go), and
[cache tests](../../internal/agent/debug/function_storage_test.go).

## Binary identity and replacement

When the binary file is accessible, the cache computes its SHA-256 hash and
uses it to decide whether rediscovery is needed. Function rows are keyed by
service and function name; the binary hash records which version supplied the
metadata, rather than acting as the table's sole primary key. Cache writes are
serialized and stored transactionally.

A supplied runtime hash can avoid rereading a binary. If file hashing fails but
an SDK or PID-based path is available, discovery can proceed with a
service-derived placeholder. That fallback is not a content fingerprint and
should not be treated as equally strong evidence of binary freshness.

Probe metadata must still account for executable layout and file offsets.
Runtime addresses, including addresses affected by position-independent loading,
are not interchangeable with the file offset expected by a uprobe. The binary
scanner and SDK metadata paths perform the relevant translation; callers must
use metadata for the binary actually running.

## Colony registry and name-based search

The Colony function poller collects cached function lists into its `functions`
registry. It checks fingerprints and binary metadata to avoid replacing an
unchanged list. The registry gives fleet-wide search a central home without
requiring each search to scan every process directly, at the cost of discovery
and polling lag.

Function embeddings are deterministic, local, 384-dimensional SimHash vectors.
The generator tokenizes the function name and hashes its tokens with xxHash3;
package paths and parameter bodies are not inputs to the primary vector.
Queries use compatible tokenization and vector comparison. Similar identifier
vocabulary can therefore aid retrieval, but the embedding does not encode the
function's behavior or prove semantic equivalence.

Implementation: [registry](../../internal/colony/function_registry.go),
[function poller](../../internal/colony/function_poller.go), and
[embedding generator](../../pkg/embedding/embedding.go).

## Design history

- [RFD 063: Function Discovery](../../RFDs/063-intelligent-function-discovery.md)
- [RFD 065: Binary Scanning](../../RFDs/065-agentless-binary-scanning.md)
- [RFD 066: SDK HTTP API](../../RFDs/066-sdk-http-api.md)
- [RFD 075: Hybrid Metadata](../../RFDs/075-hybrid-function-metadata.md)
- [RFD 104: Service Map and Lazy Indexing](../../RFDs/104-service-name-adaptor.md)
