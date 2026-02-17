// Package ask implements coral ask command.
// nolint:errcheck
package ask

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/llm"
)

// newConfigCmd creates the "coral ask config" command (RFD 055).
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Configure LLM provider for coral ask",
		Long: `Interactive wizard to configure the LLM provider for coral ask.

Guides you through provider selection, API key setup, validation, and saves
the configuration to ~/.coral/config.yaml.

Examples:
  # Run the interactive configuration wizard
  coral ask config

  # Non-interactive configuration
  coral ask config --provider google --model gemini-2.0-flash --api-key-env GOOGLE_API_KEY --yes

  # Validate existing configuration
  coral ask config validate

  # Show current configuration
  coral ask config show`,
		RunE: func(cmd *cobra.Command, args []string) error {
			provider, _ := cmd.Flags().GetString("provider")
			model, _ := cmd.Flags().GetString("model")
			apiKeyEnv, _ := cmd.Flags().GetString("api-key-env")
			colony, _ := cmd.Flags().GetString("colony")
			yes, _ := cmd.Flags().GetBool("yes")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			return runConfig(provider, model, apiKeyEnv, colony, yes, dryRun)
		},
	}

	cmd.Flags().String("provider", "", "Provider name (e.g., google, openai, coral)")
	cmd.Flags().String("model", "", "Model ID (e.g., gemini-2.0-flash)")
	cmd.Flags().String("api-key-env", "", "Environment variable containing the API key")
	cmd.Flags().String("colony", "", "Configure specific colony instead of global defaults")
	cmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompts")
	cmd.Flags().Bool("dry-run", false, "Show what would be changed without saving")

	cmd.AddCommand(newConfigValidateCmd())
	cmd.AddCommand(newConfigShowCmd())

	return cmd
}

// newConfigValidateCmd creates the "coral ask config validate" subcommand.
func newConfigValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate the current LLM configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigValidate()
		},
	}
}

// newConfigShowCmd creates the "coral ask config show" subcommand.
func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the current LLM configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigShow()
		},
	}
}

// runConfig is the main wizard entry point.
func runConfig(providerFlag, modelFlag, apiKeyEnvFlag, colonyFlag string, yes, dryRun bool) error {
	loader, err := config.NewLoader()
	if err != nil {
		return fmt.Errorf("failed to create config loader: %w", err)
	}

	globalCfg, err := loader.LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("failed to load global config: %w", err)
	}

	// Detect current state.
	hasExisting := globalCfg.AI.Ask.DefaultModel != ""

	fmt.Println()
	fmt.Println("╔═══════════════════════════════════════════════════════════╗")
	fmt.Println("║           Coral Ask Configuration Wizard                  ║")
	fmt.Println("╚═══════════════════════════════════════════════════════════╝")
	fmt.Println()

	if hasExisting {
		fmt.Printf("Detected: Existing configuration (model: %s)\n", globalCfg.AI.Ask.DefaultModel)
	} else {
		fmt.Println("Detected: No existing configuration")
	}
	fmt.Println()

	r := bufio.NewReader(os.Stdin)

	// Step 1: Provider selection.
	providerName, err := selectProvider(r, providerFlag)
	if err != nil {
		return err
	}

	// Step 2: Model / use-case selection.
	modelID, err := selectModel(r, providerName, modelFlag)
	if err != nil {
		return err
	}

	// Step 3: API key setup (skip for coral — uses endpoint, not env key).
	var apiKeyEnv string
	if providerName != "coral" {
		apiKeyEnv, err = setupAPIKey(r, providerName, apiKeyEnvFlag)
		if err != nil {
			return err
		}
	}

	// Step 4: Validation.
	fmt.Println("\nStep 4/5: Validation")
	fmt.Println("───────────────────────────────────────────────────────────")
	if err := validateSetup(providerName, modelID, apiKeyEnv); err != nil {
		return err
	}

	// Step 5: Preview and confirm.
	newCfg := buildAskConfig(globalCfg, providerName, modelID, apiKeyEnv)

	fmt.Println("\nStep 5/5: Review & Confirm")
	fmt.Println("───────────────────────────────────────────────────────────")
	fmt.Println("\nConfiguration to be saved:")
	fmt.Printf("\n  File: %s\n\n", loader.GlobalConfigPath())

	preview := buildConfigPreview(newCfg)
	for _, line := range strings.Split(preview, "\n") {
		fmt.Printf("  %s\n", line)
	}

	if !dryRun {
		if !yes {
			fmt.Print("\n? Save this configuration? [Y/n] ")
			answer, _ := r.ReadString('\n')
			answer = strings.TrimSpace(strings.ToLower(answer))
			if answer != "" && answer != "y" && answer != "yes" {
				fmt.Println("\nAborted — no changes made.")
				return nil
			}
		}

		// Back up existing config.
		configPath := loader.GlobalConfigPath()
		if hasExisting {
			if err := backupConfig(configPath); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to create backup: %v\n", err)
			} else {
				ts := time.Now().Format("20060102-150405")
				fmt.Printf("\n✓ Backup created: %s.backup.%s\n", configPath, ts)
			}
		}

		// Apply ask config to global config and save.
		if colonyFlag != "" {
			if err := saveColonyAskConfig(loader, colonyFlag, newCfg); err != nil {
				return fmt.Errorf("failed to save colony config: %w", err)
			}
			fmt.Printf("✓ Colony configuration saved for %q\n", colonyFlag)
		} else {
			globalCfg.AI.Ask = *newCfg
			if err := loader.SaveGlobalConfig(globalCfg); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}
			fmt.Printf("✓ Configuration saved to %s\n", configPath)
		}
	} else {
		fmt.Println("\n[dry-run] No changes saved.")
	}

	fmt.Println("\nNext steps:")
	fmt.Println(`  1. Try it out: coral ask "what services are running?"`)
	fmt.Println("  2. Learn more: coral ask --help")
	fmt.Println("  3. View config: coral ask config show")
	fmt.Println()

	return nil
}

// selectProvider prompts the user to choose a provider.
func selectProvider(r *bufio.Reader, flag string) (string, error) {
	fmt.Println("Step 1/5: Provider Selection")
	fmt.Println("───────────────────────────────────────────────────────────")
	fmt.Println()

	registry := llm.Get()
	providers := registry.ListProviders()

	if flag != "" {
		if !registry.IsValid(flag) {
			return "", fmt.Errorf("unknown provider %q, run 'coral ask list-providers' to see available providers", flag)
		}
		fmt.Printf("Provider: %s (from flag)\n", flag)
		return flag, nil
	}

	// Display numbered list.
	for i, p := range providers {
		suffix := ""
		if p.Name == "google" {
			suffix = " [RECOMMENDED]"
		}
		fmt.Printf("  %d. %s — %s%s\n", i+1, p.DisplayName, p.Description, suffix)
	}
	fmt.Println()

	fmt.Printf("? Select a provider [1-%d]: ", len(providers))
	input, _ := r.ReadString('\n')
	input = strings.TrimSpace(input)

	n, err := strconv.Atoi(input)
	if err != nil || n < 1 || n > len(providers) {
		return "", fmt.Errorf("invalid selection %q", input)
	}

	chosen := providers[n-1]
	fmt.Printf("\nYou selected: %s\n", chosen.DisplayName)
	return chosen.Name, nil
}

// selectModel prompts the user to select a model for the given provider.
func selectModel(r *bufio.Reader, providerName, flag string) (string, error) {
	fmt.Println("\nStep 2/5: Model Selection")
	fmt.Println("───────────────────────────────────────────────────────────")
	fmt.Println()

	registry := llm.Get()

	if flag != "" {
		if err := registry.ValidateModel(providerName, flag); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
		}
		fmt.Printf("Model: %s (from flag)\n", flag)
		return flag, nil
	}

	// Coral AI has no user-selectable model.
	if providerName == "coral" {
		fmt.Println("Coral AI manages the model server-side — no selection needed.")
		return "", nil
	}

	// Get provider metadata.
	providers := registry.ListProviders()
	var providerMeta *llm.ProviderMetadata
	for i := range providers {
		if providers[i].Name == providerName {
			providerMeta = &providers[i]
			break
		}
	}

	if providerMeta == nil || len(providerMeta.SupportedModels) == 0 {
		fmt.Print("? Enter model ID: ")
		input, _ := r.ReadString('\n')
		return strings.TrimSpace(input), nil
	}

	// Group non-deprecated models by use case.
	type displayModel struct {
		llm.ModelMetadata
		idx int
	}

	useCaseOrder := []string{"fast", "balanced", "quality"}
	byUseCase := make(map[string][]displayModel)

	counter := 1
	var allModels []displayModel
	for _, m := range providerMeta.SupportedModels {
		if m.Deprecated {
			continue
		}
		dm := displayModel{ModelMetadata: m, idx: counter}
		byUseCase[m.UseCase] = append(byUseCase[m.UseCase], dm)
		allModels = append(allModels, dm)
		counter++
	}

	// Print grouped.
	for _, uc := range useCaseOrder {
		models, ok := byUseCase[uc]
		if !ok {
			continue
		}
		// Sort: recommended first.
		sort.Slice(models, func(i, j int) bool {
			return models[i].Recommended && !models[j].Recommended
		})
		for _, m := range models {
			rec := ""
			if m.Recommended {
				rec = " [RECOMMENDED]"
			}
			cost := ""
			if m.CostPer1MTokens > 0 {
				cost = fmt.Sprintf(" — $%.3f/1M tokens", m.CostPer1MTokens)
			}
			ctx := ""
			if m.ContextWindow > 0 {
				ctx = fmt.Sprintf(", %s context", formatTokenCount(m.ContextWindow))
			}
			fmt.Printf("  %d. %-45s %s%s%s%s\n",
				m.idx, m.ID, m.Description, cost, ctx, rec)
		}
	}

	// Models without a use case.
	for _, m := range allModels {
		if m.UseCase == "" {
			fmt.Printf("  %d. %-45s %s\n", m.idx, m.ID, m.Description)
		}
	}

	fmt.Println()
	fmt.Printf("? Select a model [1-%d]: ", len(allModels))
	input, _ := r.ReadString('\n')
	input = strings.TrimSpace(input)

	n, err := strconv.Atoi(input)
	if err != nil || n < 1 || n > len(allModels) {
		return "", fmt.Errorf("invalid selection %q", input)
	}

	// Find the selected model by sequential index.
	selected := allModels[n-1]
	fmt.Printf("\nSelected: %s\n", selected.ID)
	if selected.CostPer1MTokens > 0 {
		fmt.Printf("  Cost: $%.3f per 1M tokens\n", selected.CostPer1MTokens)
	}
	if selected.ContextWindow > 0 {
		fmt.Printf("  Context: %s tokens\n", formatTokenCount(selected.ContextWindow))
	}
	fmt.Printf("  Capabilities: %s\n", strings.Join(selected.Capabilities, ", "))

	return selected.ID, nil
}

// setupAPIKey guides the user through providing their API key environment variable.
func setupAPIKey(r *bufio.Reader, providerName, flag string) (string, error) {
	fmt.Println("\nStep 3/5: API Key Setup")
	fmt.Println("───────────────────────────────────────────────────────────")
	fmt.Println()

	registry := llm.Get()
	providers := registry.ListProviders()

	var defaultEnv string
	for _, p := range providers {
		if p.Name == providerName {
			defaultEnv = p.DefaultEnvVar
			break
		}
	}

	if flag != "" {
		fmt.Printf("API key env: %s (from flag)\n", flag)
		return flag, nil
	}

	if defaultEnv != "" {
		fmt.Printf("Default environment variable: %s\n", defaultEnv)
	}
	fmt.Printf("? Enter the environment variable name containing your API key")
	if defaultEnv != "" {
		fmt.Printf(" [%s]", defaultEnv)
	}
	fmt.Print(": ")

	input, _ := r.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		input = defaultEnv
	}

	if input == "" {
		return "", fmt.Errorf("no environment variable specified")
	}

	return input, nil
}

// validateSetup checks that the API key is set and the model is reachable.
func validateSetup(providerName, modelID, apiKeyEnv string) error {
	if providerName == "coral" {
		// Coral AI just needs an endpoint URL.
		endpoint := os.Getenv("CORAL_AI_ENDPOINT")
		if endpoint == "" {
			fmt.Println("⚠ CORAL_AI_ENDPOINT not set — anonymous free-tier access will be used.")
		} else {
			fmt.Printf("✓ CORAL_AI_ENDPOINT found: %s\n", endpoint)
		}
		return nil
	}

	// Check env variable.
	if apiKeyEnv == "" {
		fmt.Println("⚠ No API key environment variable configured — skipping key validation.")
		return nil
	}

	apiKey := os.Getenv(apiKeyEnv)
	if apiKey == "" {
		return fmt.Errorf("environment variable %s is not set\n\nSet it with:\n  export %s=your-api-key", apiKeyEnv, apiKeyEnv)
	}
	fmt.Printf("✓ Environment variable %s found\n", apiKeyEnv)

	// Test API key reachability.
	fmt.Print("⏳ Testing API key connectivity... ")
	if err := testProviderConnectivity(providerName, apiKey); err != nil {
		fmt.Println()
		fmt.Fprintf(os.Stderr, "⚠ Connectivity test failed: %v\n", err)
		fmt.Println("  The configuration will be saved, but verify your API key is valid.")
	} else {
		fmt.Println("✓ Success!")
	}

	if modelID != "" {
		fmt.Printf("✓ Model %s selected\n", modelID)
	}

	return nil
}

// testProviderConnectivity does a cheap connectivity check for the provider.
func testProviderConnectivity(providerName, apiKey string) error {
	var url string
	switch providerName {
	case "google":
		url = "https://generativelanguage.googleapis.com/v1beta/models?key=" + apiKey
	case "openai":
		// Just check the models endpoint.
		req, err := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/models", nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close() // nolint:errcheck
		if resp.StatusCode == http.StatusUnauthorized {
			return fmt.Errorf("invalid API key")
		}
		return nil
	default:
		// Unknown provider — skip connectivity test.
		return nil
	}

	if url != "" {
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(url)
		if err != nil {
			return err
		}
		defer resp.Body.Close() // nolint:errcheck
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("invalid API key (HTTP %d)", resp.StatusCode)
		}
	}

	return nil
}

// buildAskConfig constructs an AskConfig from wizard selections.
func buildAskConfig(globalCfg *config.GlobalConfig, providerName, modelID, apiKeyEnv string) *config.AskConfig {
	// Start from existing ask config.
	cfg := globalCfg.AI.Ask

	// Set model.
	if providerName == "coral" {
		cfg.DefaultModel = "coral"
	} else if modelID != "" {
		cfg.DefaultModel = providerName + ":" + modelID
	}

	// Set API key reference.
	if apiKeyEnv != "" {
		if cfg.APIKeys == nil {
			cfg.APIKeys = make(map[string]string)
		}
		cfg.APIKeys[providerName] = "env://" + apiKeyEnv
	}

	return &cfg
}

// buildConfigPreview returns a YAML preview of the ask config.
func buildConfigPreview(cfg *config.AskConfig) string {
	type previewAsk struct {
		DefaultModel string                        `yaml:"default_model,omitempty"`
		APIKeys      map[string]string             `yaml:"api_keys,omitempty"`
		Conversation *config.AskConversationConfig `yaml:"conversation,omitempty"`
	}
	type previewAI struct {
		Ask previewAsk `yaml:"ask"`
	}
	type preview struct {
		AI previewAI `yaml:"ai"`
	}

	var conv *config.AskConversationConfig
	if cfg.Conversation.MaxTurns > 0 || cfg.Conversation.ContextWindow > 0 {
		c := cfg.Conversation
		conv = &c
	}

	p := preview{
		AI: previewAI{
			Ask: previewAsk{
				DefaultModel: cfg.DefaultModel,
				APIKeys:      cfg.APIKeys,
				Conversation: conv,
			},
		},
	}

	data, err := yaml.Marshal(p)
	if err != nil {
		return fmt.Sprintf("(failed to render preview: %v)", err)
	}
	return string(data)
}

// saveColonyAskConfig saves ask config overrides to a colony config file.
func saveColonyAskConfig(loader *config.Loader, colonyID string, askCfg *config.AskConfig) error {
	colonyCfg, err := loader.LoadColonyConfig(colonyID)
	if err != nil {
		return fmt.Errorf("colony %q not found: %w", colonyID, err)
	}
	colonyCfg.Ask = askCfg
	return loader.SaveColonyConfig(colonyCfg)
}

// backupConfig creates a timestamped backup of the config file.
func backupConfig(configPath string) error {
	data, err := os.ReadFile(configPath) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Nothing to back up.
		}
		return err
	}

	ts := time.Now().Format("20060102-150405")
	backupPath := configPath + ".backup." + ts

	// Keep only last 5 backups.
	pruneOldBackups(configPath, 5)

	//nolint:gosec // G306: Backup has same sensitivity as original config.
	return os.WriteFile(backupPath, data, 0644)
}

// pruneOldBackups removes old config backups keeping the n most recent.
func pruneOldBackups(configPath string, keep int) {
	dir := filepath.Dir(configPath)
	base := filepath.Base(configPath)
	prefix := base + ".backup."

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	var backups []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			backups = append(backups, filepath.Join(dir, e.Name()))
		}
	}

	sort.Strings(backups) // Lexicographic = chronological for timestamp format.
	for len(backups) >= keep {
		_ = os.Remove(backups[0])
		backups = backups[1:]
	}
}

// runConfigValidate validates the current ask configuration.
func runConfigValidate() error {
	loader, err := config.NewLoader()
	if err != nil {
		return fmt.Errorf("failed to create config loader: %w", err)
	}

	globalCfg, err := loader.LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("failed to load global config: %w", err)
	}

	// Resolve without colony overrides for a global check.
	askCfg, err := config.ResolveAskConfig(globalCfg, nil)
	if err != nil {
		return fmt.Errorf("failed to resolve ask config: %w", err)
	}

	var warnings []string

	// Basic format validation.
	if err := config.ValidateAskConfig(askCfg); err != nil {
		fmt.Printf("✗ Configuration invalid: %v\n", err)
		return err
	}
	fmt.Println("✓ Global configuration is valid")

	// Check API key env vars.
	registry := llm.Get()
	providerName, modelID := parseModelString(askCfg.DefaultModel)
	if providerName != "coral" && providerName != "" {
		// Check configured api_keys map first.
		configuredKey := globalCfg.AI.Ask.APIKeys[providerName]
		if configuredKey != "" {
			envVar := strings.TrimPrefix(configuredKey, "env://")
			if os.Getenv(envVar) != "" {
				fmt.Printf("✓ API key %s is set\n", envVar)
			} else {
				fmt.Printf("✗ API key %s is not set\n", envVar)
				warnings = append(warnings, fmt.Sprintf("set %s to enable %s", envVar, providerName))
			}
		} else {
			// Fall back to provider default env var.
			providers := registry.ListProviders()
			for _, p := range providers {
				if p.Name == providerName {
					if p.DefaultEnvVar != "" && os.Getenv(p.DefaultEnvVar) != "" {
						fmt.Printf("✓ API key %s is set\n", p.DefaultEnvVar)
					} else if p.DefaultEnvVar != "" {
						fmt.Printf("⚠ API key %s is not set\n", p.DefaultEnvVar)
						warnings = append(warnings, fmt.Sprintf("set %s to authenticate with %s", p.DefaultEnvVar, providerName))
					}
					break
				}
			}
		}
	}

	// Model validation.
	if modelID != "" {
		if err := registry.ValidateModel(providerName, modelID); err != nil {
			fmt.Printf("⚠ Model warning: %v\n", err)
			warnings = append(warnings, err.Error())
		} else {
			fmt.Printf("✓ Model %s is valid\n", modelID)
		}
	}

	// Fallback models.
	if len(askCfg.FallbackModels) == 0 {
		warnings = append(warnings, "no fallback models configured")
	}

	if len(warnings) > 0 {
		fmt.Println("\nWarnings:")
		for _, w := range warnings {
			fmt.Printf("  ⚠ %s\n", w)
		}
	}

	return nil
}

// runConfigShow displays the current ask configuration.
func runConfigShow() error {
	loader, err := config.NewLoader()
	if err != nil {
		return fmt.Errorf("failed to create config loader: %w", err)
	}

	globalCfg, err := loader.LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("failed to load global config: %w", err)
	}

	ask := globalCfg.AI.Ask

	fmt.Println("Global Configuration (default for all colonies):")

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if ask.DefaultModel != "" {
		providerName, _ := parseModelString(ask.DefaultModel)
		fmt.Fprintf(w, "  Provider:\t%s\n", providerName)
		fmt.Fprintf(w, "  Model:\t%s\n", ask.DefaultModel)
	} else {
		fmt.Fprintf(w, "  Model:\t(not configured)\n")
	}

	// Show API key status.
	for provider, ref := range ask.APIKeys {
		if strings.HasPrefix(ref, "env://") {
			envVar := strings.TrimPrefix(ref, "env://")
			status := "✗ not set"
			if os.Getenv(envVar) != "" {
				status = "✓"
			}
			fmt.Fprintf(w, "  API key (%s):\tenv://%s %s\n", provider, envVar, status)
		}
	}

	fallback := "(none)"
	if len(ask.FallbackModels) > 0 {
		fallback = strings.Join(ask.FallbackModels, ", ")
	}
	fmt.Fprintf(w, "  Fallback:\t%s\n", fallback)
	w.Flush()

	// Colony overrides.
	colonyIDs, err := loader.ListColonies()
	if err == nil && len(colonyIDs) > 0 {
		fmt.Println("\nColony Overrides:")
		for _, id := range colonyIDs {
			colonyCfg, err := loader.LoadColonyConfig(id)
			if err != nil {
				continue
			}
			if colonyCfg.Ask != nil && colonyCfg.Ask.DefaultModel != "" {
				fmt.Printf("  %s:\n    Model: %s\n", id, colonyCfg.Ask.DefaultModel)
			} else {
				fmt.Printf("  %s: (using global default)\n", id)
			}
		}
	}

	return nil
}

// formatTokenCount formats a token count with K/M suffix.
func formatTokenCount(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%dM", n/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dK", n/1_000)
	default:
		return strconv.Itoa(n)
	}
}
