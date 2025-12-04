# Reef

> Ideation process

**Optional centralized layer**

For users managing multiple environments (dev, staging, prod) or multiple
applications, Coral offers **Reef** - a federation layer that aggregates data
across colonies.

**Note:** Reef is the **only centralized component** in Coral, and it's
**optional**. Most users run Coral fully decentralized (just Colony + Agents).
Reef is for enterprises that need cross-colony analysis and want to provide a
centralized LLM for their organization.

## Architecture

```
Developer/External               Reef                   Colonies
┌──────────────┐          ┌────────────────┐        ┌──────────────┐
│ coral reef   │──HTTPS──▶│  Reef Server   │◄──────▶│ my-app-prod  │
│ CLI          │          │                │ Mesh   │              │
│              │          │ Server-side    │        └──────────────┘
└──────────────┘          │ LLM            │        ┌──────────────┐
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

## Key Features

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
