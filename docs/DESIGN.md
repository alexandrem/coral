# Design

**Decentralized, self-hosted, user-controlled.**

Coral is **fully decentralized** - no Coral-owned servers, no telemetry sent to
us, no vendor lock-in. You run the Colony wherever you want (laptop, VM, K8s),
and you use **your own LLM API keys**. Your data stays on your infrastructure.

## Three-tier design with WireGuard mesh substrate

Coral creates a **secure WireGuard mesh** that connects all your infrastructure -
laptops, VMs, Kubernetes pods, across clouds and VPCs. This mesh is the substrate
that enables unified observability and control across fragmented environments.

**Why WireGuard mesh matters:**
- **Works anywhere** - Laptop ↔ AWS VPC ↔ GKE cluster ↔ on-prem VM
- **Crosses network boundaries** - No VPN configuration, no firewall rules
- **Encrypted by default** - All mesh traffic is secured with WireGuard
- **Orchestration substrate** - Debug commands work the same everywhere
- **Application-scoped** - One mesh per app, not infrastructure-wide

## Three-tier design with separated LLM

```
Developer Workstation               Enterprise (Optional)
┌────────────────────┐             ┌──────────────────────┐
│  coral ask         │             │   Reef               │
│  (Local Genkit)    │             │   Multi-colony       │
│                    │             │   Server-side LLM    │
│  Uses your own     │             │   ClickHouse         │
│  LLM API keys      │             │   (Aggregated data)  │
│  (OpenAI/Anthropic │             └──────────┬───────────┘
│   /Ollama)         │                        │
│                    │                        │ Federation
└─────────┬──────────┘                        │ (WireGuard)
          │ MCP Client                        │
          ▼                                   ▼
         ┌─────────────────────┐    ┌─────────────────────┐
         │   Colony            │◄───┤   Colony            │
         │   MCP Gateway       │    │   MCP Gateway       │
         │   Aggregates data   │    │   (Production)      │
         │   DuckDB/ClickHouse │    │   ClickHouse        │
         └──┬────────┬─────────┘    └─────────────────────┘
            │        │
    ┌───────▼──┐  ┌──▼───────┐
    │ Agent    │  │ Agent    │      ← Local observers
    │ Frontend │  │ API      │        Watch processes, connections
    └────┬─────┘  └─────┬────┘        Coordinate control actions
         │              │              Embedded DuckDB
    ┌────▼─────┐   ┌────▼─────┐
    │ Your     │   │ Your     │      ← Your services
    │ Frontend │   │ API      │        Run normally
    │ + SDK    │   │ + SDK    │        (SDK optional)
    └──────────┘   └──────────┘
```

**Key principles:**

- **WireGuard mesh substrate** - Connects fragmented infrastructure (laptop,
  clouds, K8s, VPCs) into one unified control plane
- **Decentralized by design** - No central servers (except optional Reef).
  Colony runs where you want it. Your data stays local.
- **You own the AI** - Use your own LLM API keys (OpenAI/Anthropic/Ollama). No
  vendor lock-in, no sending telemetry to Coral servers. You control the model,
  costs, and data.
- **Works anywhere** - Same debugging commands whether app runs on laptop, AWS,
  GKE, or on-prem
- **Control plane only** - Agents never proxy/intercept application traffic
- **Application-scoped** - One mesh per app (not infrastructure-wide)
- **SDK optional** - Basic observability works without code changes

## Multi-Colony Federation (Reef)

**Optional centralized layer for enterprises.**

For enterprises managing multiple environments (dev, staging, prod) or multiple
applications, Coral offers **Reef** - a federation layer that aggregates data
across colonies.

**Note:** Reef is the **only centralized component** in Coral, and it's
**optional**. Most users run Coral fully decentralized (just Colony + Agents).
Reef is for enterprises that need cross-colony analysis and want to provide a
centralized LLM for their organization.

### Architecture

```
Developer/External          Reef (Enterprise)           Colonies
┌──────────────┐          ┌────────────────┐        ┌──────────────┐
│ coral reef   │──HTTPS──▶│  Reef Server   │◄──────▶│ my-app-prod  │
│ CLI          │          │                │ Mesh   │              │
│              │          │ Server-side    │        └──────────────┘
└──────────────┘          │ LLM (Genkit)   │        ┌──────────────┐
                          │                │◄──────▶│ my-app-dev   │
┌──────────────┐          │ ClickHouse     │ Mesh   │              │
│ Slack Bot    │──HTTPS──▶│                │        └──────────────┘
└──────────────┘          │ Public HTTPS + │        ┌──────────────┐
                          │ Private Mesh   │◄──────▶│ other-app    │
┌──────────────┐          │                │ Mesh   │              │
│ GitHub       │──HTTPS──▶└────────────────┘        └──────────────┘
│ Actions      │
└──────────────┘
```

### Key Features

- **Dual Interface**: Private WireGuard mesh (colonies) + public HTTPS (
  external integrations)
- **Aggregated Analytics**: Query across all colonies for cross-environment
  analysis
- **Server-side LLM**: Reef hosts its own Genkit service with org-wide LLM
  configuration
- **ClickHouse Storage**: Scalable time-series database for federated metrics
- **External Integrations**: Slack bots, GitHub Actions, mobile apps via public
  API/MCP
- **Authentication**: API tokens, JWT, and mTLS for secure access
- **RBAC**: Role-based permissions for different operations
