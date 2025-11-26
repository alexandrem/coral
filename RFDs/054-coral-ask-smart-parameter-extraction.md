---
rfd: "054"
title: "Coral Ask - Smart Parameter Extraction from Natural Language"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: false
dependencies: [ ]
database_migrations: [ ]
areas: [ "agent", "llm", "ask" ]
---

# RFD 054 - Coral Ask - Smart Parameter Extraction from Natural Language

**Status:** 🚧 Draft

## Summary

Improve `coral ask` to intelligently extract service names and other parameters
from natural language queries instead of asking users to repeat information they
already provided. Current behavior: user asks "what are http requests for coral
service?" and LLM responds "I need the service name", despite "coral service"
being mentioned in the query.

## Problem

**Current behavior:**

```bash
$ coral ask "what is the most recent http query in last hour for coral service"
I need to know the **service name**...
```

**Why this matters:**

- Poor user experience - forces repetition of information already provided
- Breaks conversational flow
- Makes `coral ask` feel less intelligent than expected
- Reduces adoption - users expect modern LLMs to understand context

**Root causes:**

1. **No system prompt**: Agent sends only user messages and tool schemas, with
   zero instructions on parameter extraction
2. **No service context**: LLM doesn't know what services exist (registry has
   this via `ListAll()` but never passes it)
3. **Minimal tool descriptions**: Schema just says "Service name (required)"
   with no extraction guidance

**Current architecture gap:**

- Google provider creates `GenerativeModel` but never sets `SystemInstruction`
- Agent sends `GenerateRequest` with no system prompt
- Registry exists but agent doesn't access it for service context

## Solution

**Quick win approach:**
Add minimal system prompt (~100 tokens) + inject available service list from
registry. Target 70-80% accuracy improvement with minimal complexity.

**Key Design Decisions:**

1. **Lean system prompt over examples**:
    - Rationale: User wants to minimize token costs
    - Trade-off: Fewer examples = slightly lower accuracy, but faster
      implementation

2. **Registry direct access over MCP query**:
    - Rationale: User prefers passing registry to agent for live service lists
    - Trade-off: Couples agent to registry, but avoids extra LLM call per
      session

3. **Tool descriptions enhancement as Phase 2**:
    - Rationale: System prompt + context gets 70% improvement, descriptions add
      another 10%
    - Benefit: Can iterate on prompts faster than changing schemas

**Benefits:**

- Immediate UX improvement - users feel heard by the AI
- Reduces back-and-forth in conversations
- Makes parameter extraction work across all tools consistently
- Minimal token overhead (~100 tokens for prompt + ~50 for services)

**Architecture Overview:**

```
User Query: "http requests for coral service"
    ↓
Agent.Ask()
    ├─> Fetch services from registry.ListAll()
    ├─> Build system prompt with extraction rules
    ├─> Inject service context into request
    ↓
LLM Provider (Google Gemini)
    ├─> Set model.SystemInstruction with prompt
    ├─> Process query with service context
    ├─> Extract: service="coral", time_range="1h" (default)
    ↓
Tool Call: coral_query_beyla_http_metrics(service="coral", time_range="1h")
```

### Component Changes

1. **Provider Interface** (`internal/agent/llm/provider.go`):
    - Add `SystemPrompt` field to `GenerateRequest`
    - Enables all LLM providers to receive system instructions

2. **Google Provider** (`internal/agent/llm/google.go`):
    - Use `SystemPrompt` from request to set model's system instruction
    - Applies before chat session starts

3. **Ask Agent** (`internal/agent/ask/agent.go`):
    - Accept registry reference for accessing connected services
    - Build system prompt with parameter extraction rules
    - Inject available service names as context
    - Pass system prompt in generate request

**System Prompt Example:**

```
You are an observability assistant for Coral distributed systems.

PARAMETER EXTRACTION RULES:
1. Service names: Extract exactly as mentioned (e.g., "coral service" → "coral")
   Available services: {service_list}
2. Time ranges: Convert natural language (e.g., "last hour" → "1h", "30 min" → "30m")
3. HTTP methods: Extract from context (e.g., "GET requests" → "GET")
4. Status codes: Map phrases (e.g., "errors" → "5xx", "success" → "2xx")

Always extract ALL relevant parameters before asking for clarification.
```

## Implementation Plan

### Phase 1: Provider Interface & System Prompt

- [ ] Add `SystemPrompt` field to `GenerateRequest` struct
- [ ] Implement system instruction support in Google provider
- [ ] Create system prompt builder in ask agent

### Phase 2: Service Context Integration

- [ ] Pass registry to agent during initialization
- [ ] Fetch available services via registry
- [ ] Inject service list into system prompt

### Phase 3: Testing & Validation

- [ ] Validate parameter extraction from natural language queries
- [ ] Add unit tests for prompt construction
- [ ] Add integration tests with mock registry

## Testing Strategy

### Manual Test Cases

Expected behavior after implementation:

```bash
$ coral ask "what is the most recent http query in last hour for coral service"
# ✓ Extracts: service="coral", time_range="1h"
# ✓ Calls: coral_query_beyla_http_metrics

$ coral ask "show me errors from payment service"
# ✓ Extracts: service="payment", status_code_range="5xx"

$ coral ask "slowest endpoints in user service in last 30 minutes"
# ✓ Extracts: service="user", time_range="30m"

$ coral ask "check health of all services"
# ✓ Works without service parameter
```

### Unit Tests

- System prompt construction with service list
- Service name extraction from registry
- Prompt template rendering

## Future Enhancements

**Enhanced Tool Descriptions** (Phase 2 - separate PR):

- Improve jsonschema descriptions in `internal/colony/mcp/types.go`
- Add extraction hints like "Extract from query: 'last hour' → '1h'"
- Target additional 10% accuracy improvement

**Few-Shot Examples** (Optional - if needed):

- Add 3-5 example queries to system prompt
- Only if Phase 1 doesn't hit 70%+ accuracy
- Adds ~100 tokens but demonstrates patterns clearly

---

## Implementation Status

**Core Capability:** ⏳ Not Started

This RFD defines the minimal implementation needed to enable smart parameter
extraction. Once complete, `coral ask` will understand natural language queries
without requiring users to repeat information they already provided.

**Scope:**

- ✅ System prompt with extraction rules
- ✅ Service context from registry
- ⏳ Tool description enhancements (deferred to Phase 2)
- ⏳ Few-shot examples (only if needed)

## Deferred Features

**Advanced Parameter Extraction** (Future Enhancement):

- Pre-processing layer with regex extraction (high maintenance, defer until
  proven necessary)
- Fuzzy service name matching (e.g., "coral-svc" → "coral-service")
- Historical query learning (track successful extractions)

**Multi-Provider Support** (Blocked by RFD 030):

- OpenAI provider system prompt support
- Anthropic provider system prompt support
- Provider-specific prompt optimizations

---

## Critical Files

### Files to Modify

1. `internal/agent/llm/provider.go` - Provider interface
2. `internal/agent/llm/google.go` - Google provider implementation
3. `internal/agent/ask/agent.go` - Ask agent core logic
4. CLI initialization - Pass registry to agent

### Reference Files

1. `internal/colony/registry/registry.go` - Service list source
2. `internal/colony/mcp/types.go` - Tool parameter patterns
3. `RFDs/030-coral-ask-local.md` - Original agent architecture
