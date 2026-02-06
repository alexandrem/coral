package config

import (
	"fmt"
	"os"
	"strings"
)

// ResolveAskConfig resolves the final ask configuration using the standard
// Coral configuration hierarchy (RFD 030).
//
// Precedence (highest to lowest):
// 1. Environment variables
// 2. Colony-specific overrides
// 3. Global configuration
// 4. Default values
func ResolveAskConfig(globalCfg *GlobalConfig, colonyCfg *ColonyConfig) (*AskConfig, error) {
	// Start with global config defaults.
	resolved := globalCfg.AI.Ask

	// Apply colony-specific overrides if present.
	if colonyCfg != nil && colonyCfg.Ask != nil {
		if colonyCfg.Ask.DefaultModel != "" {
			resolved.DefaultModel = colonyCfg.Ask.DefaultModel
		}
		if len(colonyCfg.Ask.FallbackModels) > 0 {
			resolved.FallbackModels = colonyCfg.Ask.FallbackModels
		}
		if len(colonyCfg.Ask.APIKeys) > 0 {
			// Merge API keys (colony overrides take precedence).
			if resolved.APIKeys == nil {
				resolved.APIKeys = make(map[string]string)
			}
			for k, v := range colonyCfg.Ask.APIKeys {
				resolved.APIKeys[k] = v
			}
		}
		// Override conversation settings if specified.
		if colonyCfg.Ask.Conversation.MaxTurns > 0 {
			resolved.Conversation.MaxTurns = colonyCfg.Ask.Conversation.MaxTurns
		}
		if colonyCfg.Ask.Conversation.ContextWindow > 0 {
			resolved.Conversation.ContextWindow = colonyCfg.Ask.Conversation.ContextWindow
		}
		// Override agent settings if specified.
		if colonyCfg.Ask.Agent.Mode != "" {
			resolved.Agent.Mode = colonyCfg.Ask.Agent.Mode
		}
	}

	// Resolve API key references (env:// prefix).
	if err := resolveAPIKeyReferences(&resolved); err != nil {
		return nil, fmt.Errorf("failed to resolve API keys: %w", err)
	}

	return &resolved, nil
}

// resolveAPIKeyReferences resolves API key references like "env://OPENAI_API_KEY".
func resolveAPIKeyReferences(cfg *AskConfig) error {
	if cfg.APIKeys == nil {
		return nil
	}

	for provider, ref := range cfg.APIKeys {
		if strings.HasPrefix(ref, "env://") {
			envVar := strings.TrimPrefix(ref, "env://")
			value := os.Getenv(envVar)
			if value == "" {
				// Don't error on missing keys - provider might not be used.
				continue
			}
			cfg.APIKeys[provider] = value
		} else if strings.HasPrefix(ref, "keyring://") {
			// TODO: Implement keyring support in future.
			return fmt.Errorf("keyring:// API key references not yet implemented for provider %q", provider)
		} else if ref == "" {
			// Empty reference - try to find API key from common env vars.
			cfg.APIKeys[provider] = getDefaultAPIKey(provider)
		}
	}

	return nil
}

// getDefaultAPIKey attempts to get API key from common environment variables.
func getDefaultAPIKey(provider string) string {
	switch provider {
	case "openai":
		return os.Getenv("OPENAI_API_KEY")
	case "anthropic":
		return os.Getenv("ANTHROPIC_API_KEY")
	case "google":
		return os.Getenv("GOOGLE_API_KEY")
	default:
		return ""
	}
}

// ValidateAskConfig validates the ask configuration.
func ValidateAskConfig(cfg *AskConfig) error {
	if cfg.DefaultModel == "" {
		return fmt.Errorf("default_model is required")
	}

	// Validate model format (provider:model-id).
	parts := strings.SplitN(cfg.DefaultModel, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid model format %q, expected provider:model-id (e.g., openai:gpt-4o-mini)", cfg.DefaultModel)
	}

	// Validate conversation settings.
	if cfg.Conversation.MaxTurns < 0 {
		return fmt.Errorf("max_turns must be non-negative, got %d", cfg.Conversation.MaxTurns)
	}
	if cfg.Conversation.ContextWindow < 0 {
		return fmt.Errorf("context_window must be non-negative, got %d", cfg.Conversation.ContextWindow)
	}

	// Validate agent mode.
	validModes := map[string]bool{"embedded": true, "daemon": true, "ephemeral": true}
	if !validModes[cfg.Agent.Mode] {
		return fmt.Errorf("invalid agent mode %q, must be one of: embedded, daemon, ephemeral", cfg.Agent.Mode)
	}

	return nil
}
