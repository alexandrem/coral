# Coral

Open-source AI debugger for distributed apps. Gives AI assistants direct access
to running systems via eBPF probes, WireGuard mesh, and gRPC — no
instrumentation or redeployment required. Go, DuckDB, Anthropic API.

## Rules

**Testing**: All tests must pass (`make test`) before commits.

**Linting**: Code must pass linting (`make lint`) before commits.

**Architecture Documentation** (`/architecture-docs`): Use the skill for internal
system explanations in `docs/architecture/`. Start at its `README.md` reading map.

**User Documentation** (`/user-docs`): Use the skill for setup, configuration,
operations, and feature guides in `docs/user/`. Start at its `README.md` reading
map. Keep `docs/CLI_REFERENCE.md` as the single man-style command manual.

**RFD Writing** (`/rfd-write`): Use the skill. Read `RFDs/000-RFD-TEMPLATE.md`
first. Scope must be completable — split if >5 phases or blocked by other RFDs.
No time estimates.

**RFD Implementation** (`/rfd-implement`): Use the skill. Keep the RFD in sync:
`🔄 In Progress` at start, check off tasks as you go, `🎉 Implemented` when done.

**Code**: Effective Go conventions, Go Doc Comments style, end comments with a
dot.

**Files**: NEVER create files unless absolutely necessary. ALWAYS prefer editing
existing files.
