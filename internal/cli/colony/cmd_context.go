package colony

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/coral-mesh/coral/internal/cli/helpers"
	"github.com/coral-mesh/coral/internal/config"
)

func newUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <colony-id>",
		Short: "Set the default colony",
		Long:  `Set the default colony to use for commands when no explicit colony is specified.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			colonyID := args[0]

			loader, err := config.NewLoader()
			if err != nil {
				return fmt.Errorf("failed to create config loader: %w", err)
			}

			// Verify colony exists
			if _, err := loader.LoadColonyConfig(colonyID); err != nil {
				return fmt.Errorf("colony %q not found: %w", colonyID, err)
			}

			// Load and update global config
			globalConfig, err := loader.LoadGlobalConfig()
			if err != nil {
				return fmt.Errorf("failed to load global config: %w", err)
			}

			globalConfig.DefaultColony = colonyID

			if err := loader.SaveGlobalConfig(globalConfig); err != nil {
				return fmt.Errorf("failed to save global config: %w", err)
			}

			fmt.Printf("✓ Default colony set to: %s\n", colonyID)

			return nil
		},
	}
}

func newCurrentCmd() *cobra.Command {
	var (
		format  string
		verbose bool
	)

	cmd := &cobra.Command{
		Use:   "current",
		Short: "Show the current default colony",
		Long: `Display information about the current default colony.

With --verbose, shows additional resolution information explaining why this
colony was selected (environment variable, project config, or global default).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolver, err := config.NewResolver()
			if err != nil {
				return fmt.Errorf("failed to create resolver: %w", err)
			}

			// Use ResolveWithSource to get resolution info (RFD 050).
			colonyID, source, err := resolver.ResolveWithSource()
			if err != nil {
				return fmt.Errorf("no colony configured: %w", err)
			}

			loader := resolver.GetLoader()
			cfg, err := loader.LoadColonyConfig(colonyID)
			if err != nil {
				return fmt.Errorf("failed to load colony config: %w", err)
			}

			globalConfig, err := loader.LoadGlobalConfig()
			if err != nil {
				return fmt.Errorf("failed to load global config: %w", err)
			}

			// Prepare output data.
			output := map[string]interface{}{
				"colony_id":   cfg.ColonyID,
				"application": cfg.ApplicationName,
				"environment": cfg.Environment,
				"storage":     cfg.StoragePath,
				"discovery":   globalConfig.Discovery.Endpoint,
				"mesh_id":     cfg.Discovery.MeshID,
			}
			// Include resolution info in output (RFD 050).
			output["resolution"] = map[string]string{
				"source": source.Type,
				"path":   source.Path,
			}

			// Use formatter for non-table output.
			if format != string(helpers.FormatTable) {
				formatter, err := helpers.NewFormatter(helpers.OutputFormat(format))
				if err != nil {
					return err
				}
				return formatter.Format(output, os.Stdout)
			}

			fmt.Println("Current Colony:")
			fmt.Printf("  ID: %s\n", cfg.ColonyID)
			fmt.Printf("  Application: %s\n", cfg.ApplicationName)
			fmt.Printf("  Environment: %s\n", cfg.Environment)
			fmt.Printf("  Storage: %s\n", cfg.StoragePath)
			fmt.Printf("  Discovery: %s (mesh_id: %s)\n", globalConfig.Discovery.Endpoint, cfg.Discovery.MeshID)

			// Show resolution info with --verbose flag (RFD 050).
			if verbose {
				fmt.Println()
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
		},
	}

	helpers.AddFormatFlag(cmd, &format, helpers.FormatTable, []helpers.OutputFormat{
		helpers.FormatTable,
		helpers.FormatJSON,
		helpers.FormatYAML,
	})
	helpers.AddVerboseFlag(cmd, &verbose)

	return cmd
}
