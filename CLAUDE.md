# Coral - Unified Operations for Distributed Apps

**Status**: Design phase • **Docs**: `docs/IMPLEMENTATION.md` (tech), `docs/CONCEPT.md` (vision)

## Architecture

**Three-tier**: Colony (AI coordinator) → Agents (local observers) → SDK (optional control)
**Storage**: Agents keep recent data (~1hr), Colony stores summaries
**Network**: WireGuard mesh + gRPC, control plane only
**Integration**: MCP client (Grafana/Sentry) + server (Claude Desktop)

## Tech Stack

- **Language**: Go • **Storage**: DuckDB + embedded • **AI**: Direct API (user keys)
- **Network**: WireGuard mesh, gRPC • **Discovery**: Lightweight NAT traversal service

## Key Principles

- Unified ops (observe/debug/control), works passively or SDK-integrated for full control
- Self-sufficient (air-gap compatible), AI recommends/human approves
- Application-scoped, standards-first (Prometheus/OTEL), control plane only

## Critical Rules

**Testing**: All tests must pass (`make test`) before commits.

**Code**: Follow Effective Go conventions, Go Doc Comments style, end comments with a dot.

**Files**: NEVER create files unless absolutely necessary. ALWAYS prefer editing existing files.
