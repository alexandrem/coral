---
name: rfd-write
description: Draft a new RFD for the Coral project. Use when the user asks to write, create, or draft an RFD.
argument-hint: <rfd-number> <title>
user-invocable: true
---

Draft a new RFD following the Coral template and conventions.

## Steps

1. Read `RFDs/000-RFD-TEMPLATE.md` in full to internalize structure and
   guidelines.
2. Determine the RFD number and title from $ARGUMENTS (e.g.
   `/rfd-write 097 coral-mesh-stats`).
   If no number is given, list existing RFDs to find the next available number.
3. Create `RFDs/<NNN>-<kebab-title>.md` using the template structure.

## Scope Check (do this before writing)

Before drafting, evaluate scope against the template's "Scope Management"
guidelines:

- Can this RFD realistically reach **"Implemented"** status on its own?
- Does it have **>5 phases**? If so, propose splitting into multiple focused
  RFDs.
- Are there features **blocked by other RFDs**? Move them to Future Work with
  RFD references.
- Is the core capability **shippable independently**? If not, narrow the scope.

If the requested scope is too large, pause and propose a split before writing.

## Writing Rules

- Start status at `🚧 Draft` in both the frontmatter (`state: "draft"`) and the *
  *Status** line.
- Fill all required sections: Summary, Problem, Solution, API Changes,
  Implementation Plan, Implementation Status, Future Work.
- Implementation Plan: write concrete, testable checkbox tasks grouped into
  phases. No time estimates.
- Implementation Status: set to `⏳ Not Started` with a brief description of what
  will be built.
- Keep API Changes detailed (full protobuf, CLI examples, config). Keep Solution
  high-level.
- Use Future Work for anything out of scope — with explicit RFD references where
  possible.
- Follow all ❌ DO NOT rules from the template's Content Guidelines section.

## Docs Impact Check

Before finalising the Implementation Plan, assess whether the RFD's surface area
touches any user-facing docs. Add doc-update tasks to the **last phase** for
every file that will need updating:

| Surface area | Docs to update |
|---|---|
| New or changed CLI commands / flags | `docs/CLI.md`, `docs/CLI_REFERENCE.md` |
| New or changed MCP tools | `docs/MCP.md`, `docs/CLI_MCP_MAPPING.md` |
| New or changed config keys | `docs/CONFIG.md` |
| New or changed agent behaviour | `docs/AGENT.md` |
| New or changed colony/server behaviour | `docs/COLONY.md` |
| New or changed service-discovery behaviour | `docs/SERVICE_DISCOVERY.md`, `docs/DISCOVERY.md` |
| New or changed storage/DuckDB behaviour | `docs/STORAGE.md` |
| New or changed provider support | `docs/PROVIDERS.md` |
| New or changed SDK/scripting API | `docs/SDK_REFERENCE.md` |
| New or changed installation/setup steps | `docs/INSTALLATION.md` |
| New or changed security model | `docs/SECURITY.md` |

Only include tasks for docs that actually apply — do not add spurious doc tasks
for unrelated files.
