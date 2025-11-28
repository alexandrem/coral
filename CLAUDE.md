# Coral - LLM-orchestrated debugging for Distributed Apps

**Status**: Design phase

## Architecture

**Three-tier**: Colony (AI coordinator) → Agents (local observers) → SDK (optional control)
**Storage**: Agents keep recent data (~1hr), Colony stores summaries
**Network**: WireGuard mesh + gRPC, control plane only
**Integration**: MCP client (Grafana/Sentry) + server (Claude Desktop)

## Tech Stack

- **Language**: Go
- **Storage**: DuckDB + embedded
- **AI**: Direct API (user keys)
- **Network**: WireGuard mesh, gRPC
- **Discovery**: Lightweight NAT traversal service

## Critical Rules

**Testing**: All tests must pass (`make test`) before commits.

**RFD**: Follow guideline in RFDs/000-RFD-TEMPLATE.md

**Code**: Follow Effective Go conventions, Go Doc Comments style, end comments with a dot.

**Files**: NEVER create files unless absolutely necessary. ALWAYS prefer editing existing files.
