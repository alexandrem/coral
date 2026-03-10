---
name: Engineering Documentation Maintenance
description: Guidelines for maintaining and expanding the Coral Engineering Knowledge Book in /docs/engineering/
---

# Engineering Documentation Skill

Use this skill when you need to update, refine, or add new chapters to the
technical documentation in `/docs/engineering/`. This documentation is designed
as a "Technical Book" for future engineers and AI systems.

## 1. Documentation Structure

- **Location**: All engineering docs must reside in `/docs/engineering/`.
- **Naming Convention**: `XX_snake_case_title.md` (e.g.,
  `03_ebpf_instrumentation_engine.md`).
- **Ordering**: Numbers are used to maintain a logical narrative flow.
- **Index**: The entry point is always `00_knowledge_map.md`.

## 2. Narrative Blocks

Maintain the documentation within these four high-level blocks:

1. **Core Foundations**: Shared architecture, networking mesh, and coordination
   protocols.
2. **Telemetry Collection**: The "Edge" mechanics (eBPF, OTLP, Host Metrics).
3. **The Data Backbone**: Storage strategies (DuckDB), reliable polling, and
   sequence checkpoints.
4. **Intelligence & Analysis**: SDKs, scripting, and LLM/MCP integrations.

## 3. Writing Guidelines

- **Audience**: Future project engineers and LLMs.
- **Tone**: Terse, high-density, and technically precise.
- **Deep Dives**: For complex topics (e.g., eBPF maps, sequence gap detection),
  provide detailed engineering rationale.
- **Internal References**: Always mention relevant Go packages (e.g.,
  `internal/agent/ebpf`) or directory structures.
- **No Line Numbers**: Never reference specific line numbers as they drift;
  reference symbols (functions/structs) instead.
- **Philosophical Tenets**: Ensure every doc reflects the core tenets (
  Edge-first, Sequence-based reliability, Zero-instrumentation).

## 4. Mandatory Sections

Every documentation chapter MUST include:

### Future Engineering Notes

A section titled `## Future Engineering Note` or at the end of sections
describing how the current implementation is expected to scale or evolve (e.g.,
moving from sequential to parallel visitors).

### RFD References

A section at the end titled `## Related Design Documents (RFDs)` with relative
links to relevant RFD files.
Example:

```markdown
## Related Design Documents (RFDs)

- [**RFD 089
  **: Sequence Based Polling Checkpoints](../../RFDs/089-sequence-based-polling-checkpoints.md)
```

## 5. Maintenance Workflow

1. **Re-evaluate the Map**: When adding a new topic, decide where it fits in the
   1-4 narrative blocks.
2. **Renumbering**: If a new chapter must be inserted in the middle, renumber
   the subsequent files using `mv` and update all relative links.
3. **Update Index**: Reflect the new chapter in `00_knowledge_map.md`.
4. **Link Verification**: Ensure all internal links and RFD references are
   valid.
