package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/coral-mesh/coral/internal/cli/helpers"
	"github.com/coral-mesh/coral/internal/cli/status"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/discovery/client"
)

// newStatusCmd creates the global status command.
func newStatusCmd() *cobra.Command {
	var (
		format  string
		verbose bool
	)

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show global Coral environment status",
		Long: `Display a unified dashboard view of all configured colonies and the Coral environment.

This command provides a quick overview of:
- Discovery service health
- All configured colonies (running/stopped status)
- Network endpoints and connection information
- Agent counts and uptime for running colonies

Use 'coral colony status <id>' for detailed information about a specific colony.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Load config loader
			loader, err := config.NewLoader()
			if err != nil {
				return fmt.Errorf("failed to create config loader: %w", err)
			}

			// Load global config for discovery endpoint
			globalConfig, err := loader.LoadGlobalConfig()
			if err != nil {
				return fmt.Errorf("failed to load global config: %w", err)
			}

			// Check discovery service health
			discoveryHealthy := false
			discoveryClient := client.New(globalConfig.Discovery.Endpoint, client.WithTimeout(2*time.Second))
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if _, err := discoveryClient.Health(ctx); err == nil {
				discoveryHealthy = true
			}
			cancel()

			// List all configured colonies
			colonyIDs, err := loader.ListColonies()
			if err != nil {
				return fmt.Errorf("failed to list colonies: %w", err)
			}

			// Query all colonies in parallel
			provider := status.NewProvider(loader)
			coloniesInfo := provider.QueryColoniesInParallel(colonyIDs, globalConfig.DefaultColony)

			// Calculate summary statistics
			runningCount := 0
			stoppedCount := 0
			for _, info := range coloniesInfo {
				if info.Running {
					runningCount++
				} else {
					stoppedCount++
				}
			}

			// Output in requested format.
			formatter := status.NewFormatter(globalConfig)
			if format != string(helpers.FormatTable) {
				return formatter.OutputJSON(coloniesInfo, discoveryHealthy, runningCount, stoppedCount)
			}

			return formatter.OutputTable(coloniesInfo, discoveryHealthy, runningCount, stoppedCount, verbose)
		},
	}

	helpers.AddFormatFlag(cmd, &format, helpers.FormatTable, []helpers.OutputFormat{
		helpers.FormatTable,
		helpers.FormatJSON,
	})
	helpers.AddVerboseFlag(cmd, &verbose)

	return cmd
}
