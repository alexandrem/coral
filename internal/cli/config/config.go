// Package config implements the 'coral config' command family (RFD 050).
// Provides kubectl-inspired configuration management commands.
package config

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"connectrpc.com/connect"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/internal/cli/helpers"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/constants"
)

// NewConfigCmd creates the config command and its subcommands.
func NewConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage Coral configuration",
		Long: `Manage Coral configuration.

The config command provides unified access to colony configuration management
including context switching, validation, and config inspection.

Configuration Priority:
  1. CORAL_COLONY_ID environment variable (highest)
  2. Project config (.coral/config.yaml in current directory)
  3. Global config (~/.coral/config.yaml)

Environment Variables:
  CORAL_CONFIG    Override config directory (default: ~/.coral)
  CORAL_COLONY_ID Override active colony`,
	}

	cmd.AddCommand(newGetContextsCmd())
	cmd.AddCommand(newCurrentContextCmd())
	cmd.AddCommand(newUseContextCmd())
	cmd.AddCommand(newViewCmd())
	cmd.AddCommand(newValidateCmd())
	cmd.AddCommand(newDeleteContextCmd())

	return cmd
}

// newGetContextsCmd creates the 'config get-contexts' command.
func newGetContextsCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "get-contexts",
		Short: "List all colonies with current context marked",
		Long: `Display all configured colonies, marking the current active context with *.

The RESOLUTION column shows where the current colony was resolved from:
  env     - CORAL_COLONY_ID environment variable
  project - .coral/config.yaml in current directory
  global  - ~/.coral/config.yaml default_colony`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGetContexts(format)
		},
	}

	helpers.AddFormatFlag(cmd, &format, helpers.FormatTable, []helpers.OutputFormat{
		helpers.FormatTable,
		helpers.FormatJSON,
		helpers.FormatYAML,
	})

	return cmd
}

func runGetContexts(format string) error {
	resolver, err := config.NewResolver()
	if err != nil {
		return fmt.Errorf("failed to create resolver: %w", err)
	}

	loader := resolver.GetLoader()
	colonyIDs, err := loader.ListColonies()
	if err != nil {
		return fmt.Errorf("failed to list colonies: %w", err)
	}

	if len(colonyIDs) == 0 {
		if format != string(helpers.FormatTable) {
			// Return empty structure for structured output.
			output := struct {
				CurrentColony    string        `json:"current_colony"`
				ResolutionSource string        `json:"resolution_source"`
				Colonies         []interface{} `json:"colonies"`
			}{
				CurrentColony:    "",
				ResolutionSource: "",
				Colonies:         []interface{}{},
			}
			formatter, err := helpers.NewFormatter(helpers.OutputFormat(format))
			if err != nil {
				return err
			}
			return formatter.Format(output, os.Stdout)
		}
		fmt.Println("No colonies configured.")
		fmt.Println("\nRun 'coral init <app-name>' to create one.")
		return nil
	}

	// Get current colony and resolution source.
	currentColonyID, source, _ := resolver.ResolveWithSource()

	if format != string(helpers.FormatTable) {
		return outputGetContextsFormatted(loader, colonyIDs, currentColonyID, source, format)
	}

	return outputGetContextsTable(loader, colonyIDs, currentColonyID, source)
}

func outputGetContextsFormatted(loader *config.Loader, colonyIDs []string, currentColonyID string, source config.ResolutionSource, format string) error {
	type contextInfo struct {
		ColonyID    string `json:"colony_id"`
		Application string `json:"application"`
		Environment string `json:"environment"`
		IsCurrent   bool   `json:"is_current"`
		Resolution  string `json:"resolution,omitempty"`
	}

	output := struct {
		CurrentColony    string        `json:"current_colony"`
		ResolutionSource string        `json:"resolution_source"`
		Colonies         []contextInfo `json:"colonies"`
	}{
		CurrentColony:    currentColonyID,
		ResolutionSource: source.Type,
		Colonies:         []contextInfo{},
	}

	for _, id := range colonyIDs {
		cfg, err := loader.LoadColonyConfig(id)
		if err != nil {
			continue
		}

		info := contextInfo{
			ColonyID:    cfg.ColonyID,
			Application: cfg.ApplicationName,
			Environment: cfg.Environment,
			IsCurrent:   cfg.ColonyID == currentColonyID,
		}
		if info.IsCurrent {
			info.Resolution = source.Type
		}

		output.Colonies = append(output.Colonies, info)
	}

	formatter, err := helpers.NewFormatter(helpers.OutputFormat(format))
	if err != nil {
		return err
	}
	return formatter.Format(output, os.Stdout)
}

func outputGetContextsTable(loader *config.Loader, colonyIDs []string, currentColonyID string, source config.ResolutionSource) error {
	fmt.Printf("%-10s %-30s %-15s %-12s %s\n", "CURRENT", "COLONY-ID", "APPLICATION", "ENVIRONMENT", "RESOLUTION")

	for _, id := range colonyIDs {
		cfg, err := loader.LoadColonyConfig(id)
		if err != nil {
			fmt.Printf("%-10s %-30s %-15s %-12s %s\n", "", id, "(error)", "", "-")
			continue
		}

		currentMarker := ""
		resolution := "-"
		if cfg.ColonyID == currentColonyID {
			currentMarker = "*"
			resolution = source.Type
		}

		fmt.Printf("%-10s %-30s %-15s %-12s %s\n",
			currentMarker,
			truncate(cfg.ColonyID, 30),
			truncate(cfg.ApplicationName, 15),
			truncate(cfg.Environment, 12),
			resolution,
		)
	}

	return nil
}

// newCurrentContextCmd creates the 'config current-context' command.
func newCurrentContextCmd() *cobra.Command {
	var verbose bool

	cmd := &cobra.Command{
		Use:   "current-context",
		Short: "Show the current active colony",
		Long: `Display the current active colony ID.

With --verbose, shows additional resolution information explaining why this
colony was selected (environment variable, project config, or global default).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCurrentContext(verbose)
		},
	}

	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show resolution source details")

	return cmd
}

func runCurrentContext(verbose bool) error {
	resolver, err := config.NewResolver()
	if err != nil {
		return fmt.Errorf("failed to create resolver: %w", err)
	}

	colonyID, source, err := resolver.ResolveWithSource()
	if err != nil {
		return err
	}

	fmt.Println(colonyID)

	if verbose {
		switch source.Type {
		case "env":
			fmt.Printf("Resolution: environment variable (%s)\n", source.Path)
		case "project":
			fmt.Printf("Resolution: project config (%s)\n", source.Path)
		case "global":
			fmt.Printf("Resolution: global default (%s)\n", source.Path)
		}
	}

	return nil
}

// newUseContextCmd creates the 'config use-context' command.
func newUseContextCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use-context <colony-id>",
		Short: "Set the default colony",
		Long: `Set the default colony to use for commands when no explicit colony is specified.

This is equivalent to 'coral colony use <colony-id>'.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUseContext(args[0])
		},
	}
}

func runUseContext(colonyID string) error {
	loader, err := config.NewLoader()
	if err != nil {
		return fmt.Errorf("failed to create config loader: %w", err)
	}

	// Verify colony exists.
	if _, err := loader.LoadColonyConfig(colonyID); err != nil {
		return fmt.Errorf("colony %q not found: %w", colonyID, err)
	}

	// Load and update global config.
	globalConfig, err := loader.LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("failed to load global config: %w", err)
	}

	globalConfig.DefaultColony = colonyID

	if err := loader.SaveGlobalConfig(globalConfig); err != nil {
		return fmt.Errorf("failed to save global config: %w", err)
	}

	fmt.Printf("Default colony set to: %s\n", colonyID)

	// Check if higher-priority config will override this setting.
	resolver, err := config.NewResolver()
	if err != nil {
		return nil // Don't fail, just skip the warning
	}

	effectiveID, source, err := resolver.ResolveWithSource()
	if err != nil {
		return nil // Don't fail, just skip the warning
	}

	// Warn if the effective colony differs from what was just set.
	if effectiveID != colonyID {
		fmt.Println()
		fmt.Printf("⚠ Note: %s overrides the global default.\n", source.Description())
		fmt.Printf("  Current effective colony: %s\n", effectiveID)
		if source.Type == "env" {
			fmt.Printf("  To use global default, unset %s\n", source.Path)
		} else if source.Type == "project" {
			fmt.Printf("  To use global default, remove or update %s\n", source.Path)
		}
	}

	return nil
}

// newViewCmd creates the 'config view' command.
func newViewCmd() *cobra.Command {
	var (
		colonyID string
		raw      bool
	)

	cmd := &cobra.Command{
		Use:   "view",
		Short: "Show merged configuration",
		Long: `Display the merged configuration for the current or specified colony.

The output shows the effective configuration after all sources are merged,
with comments indicating where each value came from (env, project, colony, global).

Use --raw to output the merged config without annotations.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runView(colonyID, raw)
		},
	}

	cmd.Flags().StringVar(&colonyID, "colony", "", "Colony ID (defaults to current)")
	cmd.Flags().BoolVar(&raw, "raw", false, "Output raw YAML without annotations")

	return cmd
}

func runView(colonyID string, raw bool) error {
	resolver, err := config.NewResolver()
	if err != nil {
		return fmt.Errorf("failed to create resolver: %w", err)
	}

	var source config.ResolutionSource
	if colonyID == "" {
		colonyID, source, err = resolver.ResolveWithSource()
		if err != nil {
			return err
		}
	} else {
		source = config.ResolutionSource{Type: "flag", Path: "--colony"}
	}

	loader := resolver.GetLoader()

	// Load colony config.
	colonyConfig, err := loader.LoadColonyConfig(colonyID)
	if err != nil {
		return fmt.Errorf("failed to load colony config: %w", err)
	}

	// Load global config.
	globalConfig, err := loader.LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("failed to load global config: %w", err)
	}

	// Get CA fingerprint (RFD 048).
	var caFingerprint string
	manager, db, _, err := helpers.GetCAManager(colonyID)
	if err == nil {
		status := manager.GetStatus()
		caFingerprint = "sha256:" + status.RootCA.Fingerprint
		db.Close() // nolint:errcheck
	}

	if raw {
		// Output raw YAML.
		output := struct {
			*config.ColonyConfig `yaml:",inline"`
			CAFingerprint        string `yaml:"ca_fingerprint,omitempty"`
		}{
			ColonyConfig:  colonyConfig,
			CAFingerprint: caFingerprint,
		}

		data, err := yaml.Marshal(output)
		if err != nil {
			return err
		}
		fmt.Print(string(data))
		return nil
	}

	// Output annotated view.
	fmt.Printf("# Colony: %s\n", colonyID)
	fmt.Printf("# Resolution: %s\n", source.String())
	fmt.Println("#")
	fmt.Println("# Config sources (priority order):")
	fmt.Println("#   1. Environment variables (highest)")
	fmt.Printf("#   2. Project config (.coral/config.yaml) - %s\n", checkProjectConfig())
	fmt.Printf("#   3. Colony config (~/.coral/colonies/%s/config.yaml)\n", colonyID)
	fmt.Println("#   4. Global config (~/.coral/config.yaml)")
	fmt.Println()

	// Output key config values with source annotations.
	fmt.Printf("colony_id: %s\n", colonyConfig.ColonyID)
	fmt.Printf("application_name: %s\n", colonyConfig.ApplicationName)
	fmt.Printf("environment: %s\n", colonyConfig.Environment)

	if caFingerprint != "" {
		fmt.Printf("ca_fingerprint: %s\n", caFingerprint)
	}

	fmt.Println()
	fmt.Println("discovery:")
	fmt.Printf("  endpoint: %s\n", globalConfig.Discovery.Endpoint)

	if colonyConfig.StoragePath != "" {
		fmt.Printf("\nstorage_path: %s\n", colonyConfig.StoragePath)
	}

	fmt.Println()
	fmt.Println("wireguard:")
	fmt.Printf("  port: %d\n", colonyConfig.WireGuard.Port)
	fmt.Printf("  mesh_ipv4: %s\n", colonyConfig.WireGuard.MeshIPv4)
	fmt.Printf("  mesh_network_ipv4: %s\n", colonyConfig.WireGuard.MeshNetworkIPv4)

	return nil
}

func checkProjectConfig() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "not present"
	}
	projectConfig, err := config.LoadProjectConfig(cwd)
	if err != nil || projectConfig == nil {
		return "not present"
	}
	return "present"
}

// newValidateCmd creates the 'config validate' command.
func newValidateCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate all colony configurations",
		Long: `Validate all configured colonies and report any errors.

Checks each colony's configuration for:
- Required fields (colony_id, application_name)
- Valid mesh subnet CIDR notation
- Valid port ranges
- Valid MTU values`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(format)
		},
	}

	helpers.AddFormatFlag(cmd, &format, helpers.FormatTable, []helpers.OutputFormat{
		helpers.FormatTable,
		helpers.FormatJSON,
	})

	return cmd
}

func runValidate(format string) error {
	loader, err := config.NewLoader()
	if err != nil {
		return fmt.Errorf("failed to create config loader: %w", err)
	}

	results, err := loader.ValidateAll()
	if err != nil {
		return err
	}

	if len(results) == 0 {
		fmt.Println("No colonies configured.")
		return nil
	}

	if format != string(helpers.FormatTable) {
		return outputValidateFormatted(results, format)
	}

	return outputValidateTable(results)
}

func outputValidateFormatted(results map[string]error, format string) error {
	type validationResult struct {
		ColonyID string `json:"colony_id"`
		Valid    bool   `json:"valid"`
		Error    string `json:"error,omitempty"`
	}

	output := struct {
		Results      []validationResult `json:"results"`
		ValidCount   int                `json:"valid_count"`
		InvalidCount int                `json:"invalid_count"`
	}{
		Results: []validationResult{},
	}

	for colonyID, err := range results {
		result := validationResult{
			ColonyID: colonyID,
			Valid:    err == nil,
		}
		if err != nil {
			result.Error = err.Error()
			output.InvalidCount++
		} else {
			output.ValidCount++
		}
		output.Results = append(output.Results, result)
	}

	formatter, err := helpers.NewFormatter(helpers.OutputFormat(format))
	if err != nil {
		return err
	}
	return formatter.Format(output, os.Stdout)
}

func outputValidateTable(results map[string]error) error {
	validCount := 0
	invalidCount := 0

	for colonyID, err := range results {
		if err == nil {
			fmt.Printf("  %s: valid\n", colonyID)
			validCount++
		} else {
			fmt.Printf("  %s: %s\n", colonyID, err.Error())
			invalidCount++
		}
	}

	fmt.Println()
	fmt.Printf("Validation summary: %d valid, %d invalid\n", validCount, invalidCount)

	if invalidCount > 0 {
		return fmt.Errorf("validation failed for %d colonies", invalidCount)
	}

	return nil
}

// newDeleteContextCmd creates the 'config delete-context' command.
func newDeleteContextCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete-context <colony-id>",
		Short: "Delete a colony configuration",
		Long: `Delete a colony configuration including all associated data.

This command requires interactive confirmation - you must type the colony name
to confirm deletion. This action is irreversible.

The following will be deleted:
- Colony config file
- CA certificates
- All colony data`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeleteContext(args[0])
		},
	}
}

func runDeleteContext(colonyID string) error {
	loader, err := config.NewLoader()
	if err != nil {
		return fmt.Errorf("failed to create config loader: %w", err)
	}

	// Verify colony exists.
	if _, err := loader.LoadColonyConfig(colonyID); err != nil {
		return fmt.Errorf("colony %q not found: %w", colonyID, err)
	}

	colonyDir := loader.ColonyDir(colonyID)

	// Check if colony is currently running.
	if isColonyRunning(colonyID, loader) {
		fmt.Println("Warning: This colony appears to be running. Stop it before deleting.")
		fmt.Println()
	}

	// Display warning.
	fmt.Printf("  This will permanently delete colony %q including:\n", colonyID)
	fmt.Println("   - Config, CA certificates, and all colony data")
	fmt.Printf("   - Directory: %s\n", colonyDir)
	fmt.Println()

	// Prompt for confirmation.
	fmt.Printf("To confirm, type the colony name: ")
	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed to read input: %w", err)
	}

	input = strings.TrimSpace(input)
	if input != colonyID {
		fmt.Println("Confirmation failed. Colony not deleted.")
		return nil
	}

	// Delete the colony.
	if err := loader.DeleteColonyDir(colonyID); err != nil {
		return fmt.Errorf("failed to delete colony: %w", err)
	}

	// Clear default if this was the default colony.
	globalConfig, err := loader.LoadGlobalConfig()
	if err == nil && globalConfig.DefaultColony == colonyID {
		globalConfig.DefaultColony = ""
		_ = loader.SaveGlobalConfig(globalConfig)
	}

	fmt.Printf("Deleted colony: %s\n", colonyID)

	return nil
}

func isColonyRunning(colonyID string, loader *config.Loader) bool {
	colonyConfig, err := loader.LoadColonyConfig(colonyID)
	if err != nil {
		return false
	}

	connectPort := colonyConfig.Services.ConnectPort
	if connectPort == 0 {
		connectPort = constants.DefaultColonyPort
	}

	baseURL := fmt.Sprintf("http://localhost:%d", connectPort)
	client := colonyv1connect.NewColonyServiceClient(http.DefaultClient, baseURL)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err = client.GetStatus(ctx, connect.NewRequest(&colonyv1.GetStatusRequest{}))
	return err == nil
}

// truncate truncates a string to a maximum length.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
