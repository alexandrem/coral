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

## System Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│  External AI Assistants / coral ask                             │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐           │
│  │ Claude       │  │ VS Code /    │  │ coral ask    │           │
│  │ Desktop      │  │ Cursor       │  │ (terminal)   │           │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘           │
│         │ Anthropic       │ OpenAI          │ Ollama            │
│         └─────────────────┴─────────────────┘                   │
└─────────────────────────┬───────────────────────────────────────┘
                          │ MCP Protocol (stdio)
                          │ Natural language queries
                          ▼
                 ┌────────────────────┐
                 │  MCP Proxy         │
                 │  (Protocol Bridge) │
                 └─────────┬──────────┘
                           │ gRPC
                           ▼
                 ┌────────────────────┐
                 │  Colony Server     │
                 │  • MCP Server      │
                 │  • Tool Registry   │
                 │  • DuckDB          │
                 └─────────┬──────────┘
                           │ Mesh Network
                           ▼
      ┌────────────────────┴────────────────────┐
      │                                         │
      ▼                                         ▼
┌───────────┐                             ┌───────────┐
│  Agent    │                             │  Agent    │
│  • eBPF   │        ...more agents...    │  • eBPF   │
│  • OTLP   │                             │  • OTLP   │
└─────┬─────┘                             └─────┬─────┘
      │                                         │
┌─────▼─────┐                             ┌─────▼─────┐
│ Service A │                             │ Service B │
│ (+ SDK)   │                             │ (No SDK)  │
└───────────┘                             └───────────┘
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
