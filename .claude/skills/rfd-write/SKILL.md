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
