---
name: rfd-implement
description: Implement an approved RFD. Use when the user asks to implement an RFD by number.
argument-hint: <rfd-number>
---

Implement an RFD, keeping the document in sync with progress as you work.

## Setup

1. Find the RFD file matching $ARGUMENTS (e.g. `RFDs/097-*.md`). Read it in
   full.
2. Immediately update the RFD document status to `🔄 In Progress`:
    - Frontmatter: `state: "in-progress"`
    - Status line: `**Status:** 🔄 In Progress`
    - Implementation Status section: change `⏳ Not Started` → `🔄 In Progress`
      and add a brief note on what you're starting.

## Implementing

Work through the Implementation Plan phases in order. After completing each
checkbox task:

- Mark it done in the RFD: `- [x] Task description`
- If the task reveals significant findings (design change, unexpected
  complexity), note it in the Implementation Status section.

Keep `make test` and `make lint` passing throughout. Do not defer fixes.

## Completion

When all phases are done and tests pass:

1. Update the RFD document:
    - Frontmatter: `state: "implemented"`
    - Status line: `**Status:** 🎉 Implemented`
    - Implementation Status section: replace `🔄 In Progress` with `🎉 Implemented`,
      describe what was built, list operational components with ✅, and summarize
      what works now.
2. Move any unfinished work to the **Future Work** section with a rationale.
3. Run `make test` and `make lint` one final time to confirm everything is
   clean.
