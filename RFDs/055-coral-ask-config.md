---
rfd: "055"
title: "Coral Ask Config - Interactive LLM Configuration"
state: "draft"
breaking_changes: false
testing_required: true
database_changes: false
api_changes: false
dependencies: [ "030" ]
database_migrations: [ ]
areas: [ "ai", "cli", "ux" ]
---

# RFD 055 - Coral Ask Config: Interactive LLM Configuration

**Status:** 🚧 Draft

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
│ 1. Detect Current State                │
│    - Check ~/.coral/config.yaml         │
│    - Detect existing providers          │
│    - Check environment variables        │
└─────────────────────────────────────────┘
          ↓
┌─────────────────────────────────────────┐
│ 2. Interactive Prompts                  │
│    - Select provider (Google/OpenAI/    │
│      Anthropic/Ollama)                  │
│    - Choose use case (fast/balanced/    │
│      quality/local)                     │
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

3. **Provider Registry** (`internal/llm/registry.go` - new file):

   - Central registry of supported providers
   - Provider metadata (available models, features, costs)
   - Validation functions per provider
   - Model recommendation logic

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
  provider: "google" # Default provider for coral ask
  api_key_source: "env"

  ask:
    # Default model for all colonies
    default_model: "google:gemini-2.0-flash-exp"

    # API keys (environment variable references)
    api_keys:
      google: "env://GOOGLE_API_KEY"
      openai: "env://OPENAI_API_KEY" # Optional fallback
      anthropic: "env://ANTHROPIC_API_KEY"

    # Conversation settings
    conversation:
      max_turns: 10
      context_window: 8192
      auto_prune: true

# Per-colony override (optional)
# Generated when using: coral ask config --colony production
colonies:
  my-app-production-xyz:
    ask:
      default_model: "google:gemini-1.5-pro" # More capable model for prod
```

## Implementation Plan

### Phase 1: Foundation

- [ ] Create `internal/cli/ask/config.go` for command implementation
- [ ] Add `github.com/AlecAivazis/survey/v2` dependency for interactive prompts
- [ ] Define provider registry structure (`internal/llm/registry.go`)
- [ ] Create configuration validator (`internal/config/ask_validator.go`)

### Phase 2: Core Implementation

- [ ] Implement provider registry with metadata for Google, OpenAI, Anthropic,
      Ollama
- [ ] Implement interactive prompts for provider/model selection
- [ ] Add API key validation (env variable check + API connectivity test)
- [ ] Implement configuration preview and confirmation flow
- [ ] Add YAML config generation and merging logic

### Phase 3: Validation & Testing

- [ ] Implement API key testing for each provider
- [ ] Add model availability verification
- [ ] Implement configuration backup/restore
- [ ] Add dry-run mode for testing without saving

### Phase 4: Enhancement & Polish

- [ ] Add non-interactive mode with flags (for scripting)
- [ ] Implement migration from old config format (if needed)
- [ ] Add `coral ask config validate` subcommand
- [ ] Add `coral ask config show` to display current config
- [ ] Add cost estimation for cloud providers

### Phase 5: Testing & Documentation

- [ ] Unit tests for validator and registry
- [ ] Integration tests for end-to-end flow
- [ ] CLI output testing (interactive prompts)
- [ ] Update documentation with setup guides
- [ ] Add troubleshooting guide

## API Changes

### CLI Commands

```bash
# Interactive configuration wizard (main use case)
coral ask config

# Expected output:
╔═══════════════════════════════════════════════════════════╗
║           Coral Ask Configuration Wizard                 ║
╚═══════════════════════════════════════════════════════════╝

? Select an LLM provider:
  ▸ Google (Gemini) - Fast, cost-effective [RECOMMENDED]
    OpenAI (GPT) - High quality reasoning
    Anthropic (Claude) - Best for code analysis
    Ollama - Local/offline (no API key required)

? What's your primary use case?
  ▸ Fast debugging (low cost, quick responses)
    Balanced (good quality, reasonable cost)
    Complex analysis (best quality, higher cost)
    Local/offline (air-gapped environments)

Based on your selection:
  Provider: Google (Gemini)
  Recommended model: gemini-2.0-flash-exp
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
  provider: google
  ask:
    default_model: "google:gemini-2.0-flash-exp"
    api_keys:
      google: "env://GOOGLE_API_KEY"
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
    google:gemini-2.0-flash-exp (Current global default)
    openai:gpt-4o (Highest quality)
    [Custom model...]

✓ Colony-specific configuration saved

---

# Non-interactive mode (for scripts)
coral ask config \
  --provider google \
  --model gemini-2.0-flash-exp \
  --api-key-env GOOGLE_API_KEY \
  --yes  # Skip confirmation

# Validate existing configuration
coral ask config validate

# Expected output:
✓ Global configuration is valid
✓ API key GOOGLE_API_KEY is set
✓ API connectivity test passed
✓ Model gemini-2.0-flash-exp is available

Warnings:
  ⚠ No fallback models configured
  ⚠ Colony 'staging' has no specific config (using global default)

---

# Show current configuration
coral ask config show

# Expected output:
Global Configuration (default for all colonies):
  Provider: google
  Model: gemini-2.0-flash-exp
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

// Anthropic: Send minimal message (lowest cost)
resp, err := client.CreateMessage(ctx, &MessageRequest{
    Model: "claude-3-5-haiku",
    Messages: []Message{{Role: "user", Content: "Hi"}},
    MaxTokens: 1,
})
```

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

### Provider Metadata Structure

```go
// ProviderInfo contains metadata about an LLM provider.
type ProviderInfo struct {
    ID          string   // "google", "openai", "anthropic", "ollama"
    Name        string   // "Google (Gemini)"
    Description string   // "Fast, cost-effective models"
    Features    []string // ["tool_calling", "streaming", "long_context"]
    RequiresKey bool     // true for cloud, false for local (Ollama)
    Models      []ModelInfo
}

// ModelInfo contains metadata about a specific model.
type ModelInfo struct {
    ID              string  // "gemini-2.0-flash-exp"
    DisplayName     string  // "Gemini 2.0 Flash (Experimental)"
    Provider        string  // "google"
    UseCase         string  // "fast", "balanced", "quality"
    CostPer1MTokens float64 // Estimated cost
    ContextWindow   int     // Max tokens
    Features        []string
    Recommended     bool // Highlight in UI
}
```

### Example Registry

```go
var ProviderRegistry = []ProviderInfo{
    {
        ID:          "google",
        Name:        "Google (Gemini)",
        Description: "Fast, cost-effective models with tool calling",
        Features:    []string{"tool_calling", "streaming", "long_context"},
        RequiresKey: true,
        Models: []ModelInfo{
            {
                ID:              "gemini-2.0-flash-exp",
                DisplayName:     "Gemini 2.0 Flash (Experimental)",
                Provider:        "google",
                UseCase:         "fast",
                CostPer1MTokens: 0.01,
                ContextWindow:   1000000,
                Features:        []string{"tool_calling", "streaming"},
                Recommended:     true,
            },
            {
                ID:              "gemini-1.5-pro",
                DisplayName:     "Gemini 1.5 Pro",
                Provider:        "google",
                UseCase:         "quality",
                CostPer1MTokens: 0.50,
                ContextWindow:   2000000,
                Features:        []string{"tool_calling", "streaming", "long_context"},
                Recommended:     false,
            },
        },
    },
    // ... other providers
}
```

### Interactive Flow Mockup

```
$ coral ask config

╔═══════════════════════════════════════════════════════════╗
║           Coral Ask Configuration Wizard                 ║
╚═══════════════════════════════════════════════════════════╝

Let's set up your LLM provider for coral ask.

Detected: No existing configuration

Step 1/5: Provider Selection
───────────────────────────────────────────────────────────

? Select an LLM provider:
  ▸ Google (Gemini)    [RECOMMENDED - Fast, cost-effective]
    OpenAI (GPT)       [High quality reasoning]
    Anthropic (Claude) [Best for code analysis]
    Ollama            [Local/offline, no API key needed]
    ──────────
    I already have my config set up (skip)

Step 2/5: Use Case
───────────────────────────────────────────────────────────

You selected: Google (Gemini)

? What's your primary use case?
  ▸ Fast debugging         ($0.01/1k queries, ~500ms)
    Balanced               ($0.10/1k queries, ~1s)
    Complex analysis       ($0.50/1k queries, ~2s)
    I'll choose manually

Step 3/5: API Key Setup
───────────────────────────────────────────────────────────

Recommended model: gemini-2.0-flash-exp
  • Tool calling: ✓
  • Streaming: ✓
  • Context: 1M tokens

You'll need a Google API key:
  1. Visit: https://makersuite.google.com/app/apikey
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
    provider: google
    api_key_source: env
    ask:
      default_model: "google:gemini-2.0-flash-exp"
      api_keys:
        google: "env://GOOGLE_API_KEY"
      conversation:
        max_turns: 10
        context_window: 8192
        auto_prune: true

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

**Core Capability:** ⏳ Not Started

The `coral ask config` command has not been implemented yet. This RFD defines
the design for the interactive configuration wizard.

**Dependencies:**

- ✅ RFD 030: `coral ask` command (implemented)
- ✅ Configuration schema: `AskConfig` structure (implemented)
- ⏳ Provider registry: Not yet implemented
- ⏳ Interactive prompts: Not yet implemented
- ⏳ Validation logic: Not yet implemented
