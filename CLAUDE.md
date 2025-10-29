# Coral - Unified Operations for Distributed Apps

**Status**: Design phase (no implementation yet)
**Purpose**: Unified operations interface that observes, debugs, and controls distributed applications with AI-powered insights

## Quick Reference

- **Features**: RFD format in `RFDs/000-RFD-TEMPLATE.md`

## Core Architecture

**Three-tier system** providing unified operations:

- **Colony** (central coordinator): AI analysis, summaries, historical patterns, control orchestration
- **Agents** (local observers): Recent raw data (~1hr), observe processes/health/connections, coordinate control actions
- **SDK** (optional): Enables control capabilities (feature flags, traffic inspection, profiling, rollbacks) + enhanced metadata

**Layered storage**: Agents store high-res recent data → Colony stores summaries + can query agents on-demand

**Communication**: Encrypted WireGuard control mesh (agents ↔ colony), never touches application data plane

**Integration**: MCP client (queries Grafana/Sentry) + MCP server (exports to Claude Desktop)

## Key Principles

- **Unified operations interface**: One tool for observing, debugging, and controlling distributed apps
- **Two-tier integration**: Works passively (no code changes) for observability, SDK-integrated for full control
- **Self-sufficient local intelligence**: Works standalone, air-gap compatible, no external dependencies required
- **Supervised execution**: AI observes/analyzes/recommends autonomously, human approves actions
- **Application-scoped**: One colony per app (not infrastructure-wide), scales from laptop to production
- **Standards-first**: Uses Prometheus/OTEL/existing tools, doesn't reinvent or replace them
- **Control plane only**: Agents observe locally, never proxy/intercept application traffic

## Core Technology Choices

- **Language**: Go (all components: colony, agent, CLI)
- **Storage**: DuckDB for colony (in-memory analytics + optional persistence), agents use embedded storage
- **AI**: Direct API calls (Anthropic/OpenAI) using user's keys, data never leaves user infrastructure
- **Networking**: WireGuard mesh for control plane, gRPC for agent-colony communication
- **Discovery**: Lightweight coordination service for NAT traversal (similar to Tailscale model)

## Architecture Differentiators

**What makes Coral unique:**
1. **Unified operations interface** - Observe, debug, and control from one tool (not another dashboard to check)
2. **Two-tier integration** - Works passively (no code changes) or SDK-integrated (feature flags, profiling, rollbacks)
3. **Self-sufficient intelligence** - <1s insights from local data alone (agent+colony summaries)
4. **Optional MCP enrichment** - Grafana/Sentry add depth, not core intelligence
5. **Agentic + supervised** - Autonomous investigation, human-approved execution
6. **Control plane only** - Can't break apps, zero performance impact
7. **User-controlled** - Self-hosted, your AI keys, your data stays local

## Documentation

**Technical specs** (for implementation):
- `docs/IMPLEMENTATION.md` - Complete technical implementation details

**Design resources** (for understanding):
- `docs/CONCEPT.md` - High-level vision and principles (for brainstorming/iteration)

## Critical Rules

**Testing**: All tests must pass (`make test`) before commits.

**Code**: Follow Effective Go conventions, Go Doc Comments style.

**Files**: NEVER create files unless absolutely necessary. ALWAYS prefer editing existing files.
