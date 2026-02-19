---
rfd: "055"
title: "Coral Ask Config - Interactive LLM Configuration"
state: "implemented"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: false
dependencies: [ "030" ]
database_migrations: [ ]
areas: [ "ai", "cli", "ux" ]
---

# RFD 055 - Coral Ask Config: Interactive LLM Configuration

**Status:** 🎉 Implemented

<!--
Status progression:
  🚧 Draft → 👀 Under Review → ✅ Approved → 🔄 In Progress → 🎉 Implemented
-->

## Summary

Implement `coral ask config` as an interactive CLI command to guide users through
LLM provider selection and configuration for the `coral ask` feature. This
command replaces manual YAML editing with a conversational setup experience that
validates API keys, suggests appropriate models, and ensures secure credential
storage.

## Problem

**Current behavior/limitations:**

- Users must manually edit `~/.coral/config.yaml` to configure LLM providers
- YAML syntax errors can break configuration
- No guidance on which provider or model to choose
- API keys might be stored insecurely (plain text)
- No validation that API keys work before saving
- Configuration hierarchy (global vs colony-specific) is unclear

**Why this matters:**

- First-time users struggle with YAML configuration
- Invalid API keys only discovered when running `coral ask`
- Security risks from accidental plain-text key storage
- Users unsure which model suits their use case (cost vs quality trade-offs)
- Poor onboarding experience reduces adoption

**Use cases affected:**

- **First-time setup**: New users want to start using `coral ask` quickly
- **Provider switching**: Users switching from Google to OpenAI need guided
  migration
- **Multi-colony workflows**: Users need different models per colony (production
  vs development)
- **Team onboarding**: Consistent configuration across team members
- **Troubleshooting**: Users validating their configuration when `coral ask`
  fails

## Solution

Implement `coral ask config` as an interactive command that:

1. Detects current configuration state (new setup vs reconfiguration)
2. Presents provider options with recommendations
3. Guides API key setup with secure storage (env variables)
4. Suggests models based on user's use case
5. Validates configuration by testing API connectivity
6. Supports both global and colony-specific configuration
7. Provides preview and confirmation before saving

**Key Design Decisions:**

- **Interactive prompts with sensible defaults**: Use library like
  `github.com/AlecAivazis/survey/v2` for rich CLI interactions
- **Validation before saving**: Test API keys against provider APIs to catch
  errors early
- **Security-first**: Never allow plain-text API keys, enforce env variable
  references
- **Context-aware**: Detect existing configuration and offer migration/update
  paths
- **Idempotent**: Can be run multiple times safely (update existing config)
- **Scriptable mode**: Support non-interactive mode with flags for automation

**Benefits:**

- Faster onboarding (5 minutes → 1 minute)
- Prevents configuration errors before they cause runtime failures
- Educates users about provider trade-offs (cost, quality, features)
- Enforces security best practices automatically
- Reduces support burden (fewer "my config is broken" issues)

**Architecture Overview:**

```
User runs: coral ask config
          ↓
┌─────────────────────────────────────────┐
│ 1. Detect Current State                 │
│    - Check ~/.coral/config.yaml         │
│    - Detect existing providers          │
│    - Check environment variables        │
└─────────────────────────────────────────┘
          ↓
┌─────────────────────────────────────────┐
│ 2. Interactive Prompts                  │
│    - Select provider (Google/OpenAI/    │
│      Coral AI)                          │
│    - Choose use case (fast/balanced/    │
│      quality)                           │
│    - Suggest model based on use case    │
│    - Guide API key setup                │
└─────────────────────────────────────────┘
          ↓
┌─────────────────────────────────────────┐
│ 3. Validation                           │
│    - Check env variable exists          │
│    - Test API key with provider API     │
│    - Verify model availability          │
│    - Estimate costs (if applicable)     │
└─────────────────────────────────────────┘
          ↓
┌─────────────────────────────────────────┐
│ 4. Configuration Update                 │
│    - Show preview of changes            │
│    - Confirm with user                  │
│    - Update YAML (global or colony)     │
│    - Create backup of old config        │
└─────────────────────────────────────────┘
          ↓
┌─────────────────────────────────────────┐
│ 5. Verification                         │
│    - Test with simple query             │
│    - Confirm everything works           │
│    - Provide next steps                 │
└─────────────────────────────────────────┘
```

### Component Changes

1. **CLI Command** (`internal/cli/ask/config.go` - new file):

   - Implement `coral ask config` command
   - Interactive prompts for provider/model selection
   - API key validation logic
   - YAML config generation and merging
   - Configuration preview and confirmation

2. **Configuration Validator** (`internal/config/ask_validator.go` - new file):

   - Validate API key references (env:// format)
   - Test API keys against provider APIs
   - Verify model availability
   - Check configuration completeness
   - Note: basic format validation already exists in `config.ValidateAskConfig`
     (`internal/config/ask_resolver.go`); this file extends it for the wizard

3. **Provider Registry** (`internal/llm/provider.go` - **already implemented**, PR #177):

   - Registry with `ProviderMetadata` / `ModelMetadata` structs is live
   - Currently registered: `google`, `openai`, `coral`
   - `coral ask list-providers` already uses the registry for display
   - **Action required**: extend `ModelMetadata` with `UseCase`, `CostPer1MTokens`,
     `ContextWindow`, and `Recommended` fields before Step 2 (use-case selection)
     and cost display can be built

4. **Interactive Helpers** (`internal/cli/ask/prompts.go` - new file):
   - Reusable prompt functions
   - Input validation
   - Error handling and retry logic
   - Terminal UI helpers

**Configuration Example:**

The command generates configuration like:

```yaml
# ~/.coral/config.yaml (global configuration)
version: "1"
ai:
  # Note: ai.provider is not used by coral ask. Provider is inferred from
  # ai.ask.default_model (e.g., "google:gemini-2.0-flash"). Do not write this field.
  ask:
    # Default model for all colonies
    default_model: "google:gemini-2.0-flash"

    # API keys (environment variable references)
    api_keys:
      google: "env://GOOGLE_API_KEY"
      openai: "env://OPENAI_API_KEY" # Optional, for OpenAI models

    # Conversation settings
    conversation:
      max_turns: 10
      context_window: 8192
      auto_prune: true

    # Agent deployment mode (required — ValidateAskConfig rejects empty string)
    agent:
      mode: embedded

# Per-colony override (optional)
# Generated when using: coral ask config --colony production
# Colony ask overrides live in ~/.coral/colonies/<colony-id>.yaml, not here.
colonies:
  my-app-production-xyz:
    ask:
      default_model: "google:gemini-1.5-pro" # More capable model for prod
```

## Implementation Plan

### Phase 1: Foundation

- [x] Create `internal/cli/ask/config.go` for command implementation
- [x] Use existing charmbracelet stack (bubbletea/lipgloss already in go.mod — `survey/v2` not needed)
- [x] Extend `ModelMetadata` in `internal/llm/provider.go` with wizard fields
      (`UseCase`, `CostPer1MTokens`, `ContextWindow`, `Recommended`)
- [x] Fix `ValidateAskConfig`: empty `agent.mode` now treated as `"embedded"` default
- [ ] `GlobalConfig.Validate` still only accepts "anthropic"/"openai" for `ai.provider` —
      wizard omits the field entirely, so this is not a blocker

### Phase 2: Core Implementation

- [x] Extend `ModelMetadata` with `UseCase`, `CostPer1MTokens`, `ContextWindow`,
      `Recommended` fields — populated for `google` and `openai` providers
- [x] Implement interactive prompts for provider/model selection
- [x] Add API key validation (env variable check + HTTP connectivity test)
- [x] Implement configuration preview and confirmation flow
- [x] Add YAML config generation and merging logic

### Phase 3: Validation & Testing

- [x] Implement API key testing for Google and OpenAI providers (HTTP endpoint check)
- [x] Model availability verification via `registry.ValidateModel`
- [x] Implement configuration backup (timestamped, keeps last 5)
- [x] Dry-run mode (`--dry-run`) implemented

### Phase 4: Enhancement & Polish

- [x] Non-interactive mode with `--provider`, `--model`, `--api-key-env`, `--yes` flags
- [x] `coral ask config validate` subcommand
- [x] `coral ask config show` subcommand with colony overrides display
- [x] Cost display during model selection

### Phase 5: Testing & Documentation

- [ ] Unit tests for config wizard logic
- [ ] Integration tests for end-to-end flow
- [ ] Update documentation with setup guides

## API Changes

### CLI Commands

```bash
# Interactive configuration wizard (main use case)
coral ask config

# Expected output:
╔═══════════════════════════════════════════════════════════╗
║           Coral Ask Configuration Wizard                  ║
╚═══════════════════════════════════════════════════════════╝

? Select an LLM provider:
  ▸ Google (Gemini) - Fast, cost-effective [RECOMMENDED]
    OpenAI (GPT) - High quality reasoning
    Coral AI - Hosted diagnostics (free tier, no API key required)
    ──────────
    I already have my config set up (skip)

Note: Anthropic and Ollama providers are not yet implemented and will not appear
until their integrations are added to internal/llm/.

? What's your primary use case?
  ▸ Fast debugging (low cost, quick responses)
    Balanced (good quality, reasonable cost)
    Complex analysis (best quality, higher cost)

Based on your selection:
  Provider: Google (Gemini)
  Recommended model: gemini-2.0-flash
  Cost: ~$0.01 per 1000 queries
  Speed: ~500ms average response

? Enter your Google API key environment variable:
  (Enter the name of the env var containing your API key)
  ▸ GOOGLE_API_KEY

✓ Environment variable GOOGLE_API_KEY found
✓ Testing API key... Success!
✓ Model gemini-2.0-flash-exp is available

Configuration preview:
───────────────────────────────────────────────────────────
ai:
  ask:
    default_model: "google:gemini-2.0-flash"
    api_keys:
      google: "env://GOOGLE_API_KEY"
    agent:
      mode: embedded
───────────────────────────────────────────────────────────

? Save this configuration? (Y/n) ▸ Yes

✓ Configuration saved to ~/.coral/config.yaml
✓ Backup created at ~/.coral/config.yaml.backup

Next steps:
  1. Try it out: coral ask "what services are running?"
  2. Learn more: coral ask --help
  3. View config: coral ask config show

---

# Colony-specific configuration
coral ask config --colony production

# Expected output:
Current global config:
  Default model: google:gemini-2.0-flash-exp

? Override model for colony 'production'?
  ▸ Yes, use a different model
    No, use global default

? Select model for production colony:
  ▸ google:gemini-1.5-pro (More capable, better for production)
    google:gemini-2.0-flash (Current global default)
    openai:gpt-4o (Highest quality)
    [Custom model...]

✓ Colony-specific configuration saved

---

# Non-interactive mode (for scripts)
coral ask config \
  --provider google \
  --model gemini-2.0-flash \
  --api-key-env GOOGLE_API_KEY \
  --yes  # Skip confirmation

# Validate existing configuration
coral ask config validate

# Expected output:
✓ Global configuration is valid
✓ API key GOOGLE_API_KEY is set
✓ API connectivity test passed
✓ Model gemini-2.0-flash is available

Warnings:
  ⚠ No fallback models configured
  ⚠ Colony 'staging' has no specific config (using global default)

---

# Show current configuration
coral ask config show

# Expected output:
Global Configuration (default for all colonies):
  Provider: google
  Model: gemini-2.0-flash
  API Key: env://GOOGLE_API_KEY ✓
  Fallback: none

Colony Overrides:
  production:
    Model: gemini-1.5-pro ✓
  staging:
    (using global default)

Last validated: 2025-11-25 14:30:00
```

### Configuration Changes

No schema changes - the command generates existing `AskConfig` structure. It
provides a user-friendly interface to create/update the configuration defined in
RFD 030.

**Known issues to fix before implementation:**

- `GlobalConfig.Validate()` (`internal/config/validator.go:77`) only accepts
  `"anthropic"` or `"openai"` for `ai.provider`. The wizard must not write this
  field, or the validator must be extended to accept all registered providers.
- `ValidateAskConfig()` (`internal/config/ask_resolver.go:129`) rejects an empty
  `agent.mode`. The wizard must always write `agent.mode: embedded` (or a chosen
  mode), or the validator must treat empty as the `embedded` default.
- `ai.api_key_source` is not used by `coral ask`. The wizard must not write this
  field.

### New Flags

- `--provider <name>`: Non-interactive provider selection
- `--model <name>`: Non-interactive model selection
- `--api-key-env <var>`: Non-interactive API key env variable
- `--colony <id>`: Configure specific colony instead of global defaults
- `--yes` / `-y`: Skip confirmation prompts
- `--validate`: Validate configuration without prompting
- `--dry-run`: Show what would be changed without saving

## Testing Strategy

### Unit Tests

**Configuration Validator:**

- Valid env variable references (env://VAR_NAME)
- Invalid references (plain text keys rejected)
- Missing environment variables detected
- Provider-specific validation logic

**Provider Registry:**

- All providers registered correctly
- Model metadata accurate
- Recommendation logic works for each use case

**Prompt Logic:**

- Default value selection
- Input validation and sanitization
- Error handling and retry flows

### Integration Tests

**End-to-end Configuration Flow:**

- New configuration creation (empty config)
- Configuration update (existing config)
- Colony-specific override
- Multiple providers configured
- Validation catches errors

**API Key Testing:**

- Google API key validation
- OpenAI API key validation
- Anthropic API key validation
- Invalid key rejection
- Network error handling

### E2E Tests

**Scenario 1: First-time Setup**

```bash
# Setup: Clean ~/.coral/config.yaml
# Set GOOGLE_API_KEY env variable
# Run interactive wizard
coral ask config
# Verify: Config created, validated, ready to use
coral ask "test query"
```

**Scenario 2: Provider Migration**

```bash
# Setup: Existing Google config
# Add OPENAI_API_KEY
# Run wizard, select OpenAI
coral ask config
# Verify: Config updated to OpenAI, Google preserved as fallback
```

**Scenario 3: Colony Override**

```bash
# Setup: Global config exists
# Configure colony-specific model
coral ask config --colony production
# Verify: Colony config uses different model, global unchanged
coral ask "query" --colony production
```

## Security Considerations

### API Key Storage

**Requirements:**

- NEVER allow plain-text API keys in config files
- Enforce environment variable references (`env://VAR_NAME`)
- Validate env variables exist before saving configuration
- Support optional keyring integration (future enhancement)

**Validation:**

```go
// Example validation logic
func validateAPIKeyReference(ref string) error {
    if !strings.HasPrefix(ref, "env://") {
        return errors.New("API keys must use env:// format")
    }

    varName := strings.TrimPrefix(ref, "env://")
    if os.Getenv(varName) == "" {
        return fmt.Errorf("environment variable %s not set", varName)
    }

    return nil
}
```

**Warning on plain-text detection:**

```
❌ ERROR: Plain-text API key detected

You entered: sk-proj-abc123...

API keys must be stored in environment variables, not config files.

How to fix:
  1. Add to your shell profile:
     export GOOGLE_API_KEY=sk-proj-abc123...

  2. Reference it in config:
     api_keys:
       google: "env://GOOGLE_API_KEY"

For help: https://docs.coral.io/security/api-keys
```

### API Key Testing

**Threat:** API key validation sends keys to provider APIs

**Mitigations:**

- Only test when explicitly requested (`--validate` or during setup)
- Use minimal test queries (cheapest possible API call)
- Show warning before testing: "About to test API key with provider..."
- Support `--skip-validation` for offline scenarios
- Never log API keys or responses

**Test Query Examples:**

```go
// Google: List models (free API call)
models, err := client.ListModels(ctx)

// OpenAI: Get model info (minimal cost)
model, err := client.GetModel(ctx, "gpt-4o-mini")

// Coral AI: Validate endpoint URL format and basic connectivity only.
// No API call or cost — token is optional (free tier allows anonymous access).
```

Note: Anthropic and Ollama are not yet implemented providers. Validation
examples for them will be added when the providers land in `internal/llm/`.

### Configuration Backup

Before modifying configuration:

1. Create timestamped backup: `~/.coral/config.yaml.backup.TIMESTAMP`
2. Keep last 5 backups (rotate old ones)
3. Provide restore command: `coral ask config restore <timestamp>`

## Migration Strategy

**Deployment:**

1. Add `coral ask config` command to CLI
2. Update `coral ask` to suggest running `coral ask config` if config missing
3. Documentation updated with new setup flow
4. Announce in release notes

**Rollout:**

- No breaking changes (generates same config format as RFD 030)
- Existing manual configurations continue working
- Users can migrate incrementally using `coral ask config`

**First-run Experience:**

When `coral ask` runs without configuration:

```
✗ Error: LLM provider not configured

To get started, run the configuration wizard:
  coral ask config

Or configure manually:
  https://docs.coral.io/coral-ask/configuration
```

## Future Enhancements

### Advanced Features

**Cost Estimation:**

- Estimate monthly costs based on query volume
- Show per-query costs during model selection
- Budget warnings and alerts

**Team Configuration:**

- Export/import configuration for team sharing
- Organizational presets (recommended config for company)
- Configuration templates

**Provider Fallbacks:**

- Configure automatic fallback chain (primary → secondary → tertiary)
- Test fallback logic during validation
- Health checks for provider APIs

**Model Testing:**

- Benchmark different models on sample queries
- Compare quality/speed/cost trade-offs
- A/B testing support

### Enhanced Validation

- Quota checks (API rate limits)
- Region availability (some models geo-restricted)
- Feature compatibility (tool calling support)
- Version compatibility (model deprecations)

### Integration

- CI/CD validation (ensure config valid in pipelines)
- Terraform provider for configuration
- Web UI for visual configuration
- MCP tool for AI-assisted configuration

## Appendix

### Interactive Flow Mockup

```
$ coral ask config

╔═══════════════════════════════════════════════════════════╗
║           Coral Ask Configuration Wizard                  ║
╚═══════════════════════════════════════════════════════════╝

Let's set up your LLM provider for coral ask.

Detected: No existing configuration

Step 1/5: Provider Selection
───────────────────────────────────────────────────────────

? Select an LLM provider:
  ▸ Google (Gemini)    [RECOMMENDED - Fast, cost-effective]
    OpenAI (GPT)       [High quality reasoning]
    Coral AI           [Hosted diagnostics, free tier]
    ──────────
    I already have my config set up (skip)

Note: Anthropic and Ollama will appear here once their provider integrations
are added. Coral AI uses a different configuration flow (endpoint URL +
optional token) and requires a dedicated branch in the wizard.

Step 2/5: Use Case
───────────────────────────────────────────────────────────

You selected: Google (Gemini)

? What's your primary use case?
  ▸ Fast debugging         ($0.01/1k queries, ~500ms)
    Balanced               ($0.10/1k queries, ~1s)
    Complex analysis       ($0.50/1k queries, ~2s)
    I'll choose manually

Note: Use-case display and cost estimates require adding UseCase,
CostPer1MTokens, ContextWindow, and Recommended fields to ModelMetadata
(internal/llm/provider.go) before this step can be implemented.

Step 3/5: API Key Setup
───────────────────────────────────────────────────────────

Recommended model: gemini-2.0-flash
  • Tool calling: ✓
  • Streaming: ✓
  • Context: 1M tokens

You'll need a Google API key:
  1. Visit: https://aistudio.google.com/app/apikey
  2. Create a new API key
  3. Set environment variable: export GOOGLE_API_KEY=your-key

? Enter the environment variable name: ▸ GOOGLE_API_KEY

Step 4/5: Validation
───────────────────────────────────────────────────────────

✓ Environment variable GOOGLE_API_KEY found
⏳ Testing API key...
✓ API key is valid!
✓ Model gemini-2.0-flash-exp is available
✓ Tool calling is supported

Estimated costs (based on average usage):
  • ~100 queries/month: $1.00/month
  • ~1000 queries/month: $10.00/month

Step 5/5: Review & Confirm
───────────────────────────────────────────────────────────

Configuration to be saved:

  File: ~/.coral/config.yaml

  ai:
    ask:
      default_model: "google:gemini-2.0-flash"
      api_keys:
        google: "env://GOOGLE_API_KEY"
      conversation:
        max_turns: 10
        context_window: 8192
        auto_prune: true
      agent:
        mode: embedded

? Save this configuration? (Y/n) ▸

───────────────────────────────────────────────────────────

✓ Configuration saved!
✓ Backup created: ~/.coral/config.yaml.backup.20251125-143000

🎉 All set! Try it out:

  coral ask "what services are running?"
  coral ask "show me HTTP latency"

For help: coral ask --help
```

---

## Implementation Status

**Core Capability:** 🎉 Implemented

`coral ask config` is implemented in `internal/cli/ask/config.go`.

**What's implemented:**

- ✅ RFD 030: `coral ask` command (implemented)
- ✅ Configuration schema: `AskConfig` structure (implemented)
- ✅ Provider registry: `internal/llm/provider.go` — `google`, `openai`, `coral`
- ✅ `coral ask list-providers`: Uses registry directly
- ✅ `ModelMetadata` wizard extensions: `UseCase`, `CostPer1MTokens`,
  `ContextWindow`, `Recommended` added; populated for `google` and `openai`
- ✅ `ValidateAskConfig`: Empty `agent.mode` now treated as `"embedded"` default
- ✅ Wizard does not write `ai.provider` (avoids `GlobalConfig.Validate` issue)
- ✅ Interactive wizard with 5-step flow (uses stdlib `bufio` — no extra dep)
- ✅ Non-interactive flags: `--provider`, `--model`, `--api-key-env`, `--yes`
- ✅ API key env var validation + HTTP connectivity test (Google, OpenAI)
- ✅ Coral AI special case (endpoint URL + optional token)
- ✅ Config preview before saving
- ✅ Timestamped backup (keeps last 5)
- ✅ `--dry-run` mode
- ✅ `coral ask config validate` subcommand
- ✅ `coral ask config show` subcommand with colony overrides

**Remaining / future work:**

- ⏳ `GlobalConfig.Validate()` still only accepts "anthropic"/"openai" for
  `ai.provider` — not a blocker since the wizard omits the field entirely
- ⏳ Anthropic and Ollama providers: not yet in `internal/llm/`; will appear in
  wizard automatically once implemented (registry-driven)
- ⏳ Unit and integration tests for wizard
- ⏳ `coral ask config restore <timestamp>` subcommand
