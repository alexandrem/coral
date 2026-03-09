# Go Function Inlining & Tracing

## Overview

Go's compiler is highly aggressive about inlining small, simple functions to
improve runtime performance. While this is great for application speed, it
creates core challenges for **dynamic instrumentation** (uprobes) and *
*automated discovery** (semantic indexing).

## The Inlining Problem

When the Go compiler inlines a function, it literally copy-pastes the code of
that function into its callers. In many cases, the compiler then performs **Dead
Symbol Elimination**, effectively deleting the original standalone function from
the binary's symbol table and DWARF metadata.

### Impact on Coral Discovery

If a function is strictly inlined and pruned, it simply **does not exist** as a
standalone entity in the compiled binary. This results in several "blind spots":

1. **Registry Visibility**: The function will not appear in the Agent's local
   index or the Colony's global function registry.
2. **Semantic Indexing**: Because the function is not in the registry, the
   Colony cannot generate embeddings for it. It will not appear in semantic
   search results.
3. **Total Invisibility**: Even if you know the name, Coral cannot attach a
   probe because there is no entry point (no symbol address) in the binary.

## Function Materialization States

| State                         | Compiler Behavior                                                                        | Coral Support                                                                                                                        |
|:------------------------------|:-----------------------------------------------------------------------------------------|:-------------------------------------------------------------------------------------------------------------------------------------|
| **Materialized** (Standalone) | Logic is complex enough that the compiler keeps it as a standalone function.             | **Full Support**: Indexed, Embeddings, Entry Probes, Return Probes.                                                                  |
| **Inlined but Exported**      | Logic is inlined for speed, but a "residual" symbol is kept (e.g., for external access). | **Partial Support**: Metadata indexed, Entry probes work, but **Return probes (size/RET) may fail** if DWARF metadata is incomplete. |
| **Fully Inlined & Pruned**    | Logic is copy-pasted into callers and the symbol is deleted.                             | **No Support**: Function is invisible to all telemetry and discovery.                                                                |

## Impact on Execution Duration (RET-instruction Probes)

Coral uses a **RET-instruction disassembly** approach for tracing function
returns (see RFD 073). Standard kernel `uretprobes` are incompatible with the Go
runtime's segmented stack management and can cause crashes.

To trace a function's exit and calculate duration:

1. Coral finds the function's entry point address.
2. Coral retrieves the function's physical size in bytes (via DWARF).
3. Coral disassembles that byte range to find every `RET` instruction and
   attaches a uprobe to each.

If a function is "thinly" inlined, we might have an entry address but **no size
information**. Without size, Coral cannot safely disassemble the function, and
duration metrics (return events) will be unavailable.

## Best Practices

### For Production Systems

In large-scale production applications, you generally do not need to worry about
this. Any function complex enough to be a meaningful target for debugging (e.g.,
`ServeHTTP`, `ProcessOrder`, `ValidateToken`) contains enough logic (loops,
branches, interface calls) that the compiler's inlining budget will reject it.
These functions remain "Materialized" and fully discoverable.

### For Testing & Micro-functions

If you have a tiny "micro-function" that is critical for your domain logic and
you want to ensure it is always discoverable and traceable, you can use the
`//go:noinline` directive:

```go
// CalculateTotal is a tiny micro-function that would normally be inlined.
// We use //go:noinline to ensure it is always visible in Coral telemetry.
//go:noinline
func CalculateTotal(subtotal float64, taxRate float64) float64 {
return subtotal * (1 + taxRate)
}
```

### Discovery vs. Observability

If a function is inlined, its **logic** is still present inside its callers.
While the "leaf" function _itself_ won't be indexed, semantic search on the *
*caller** will likely still pick up the intent of the logic, as the code is
physically present inside the caller's binary footprint.
