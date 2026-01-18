package colony

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/coral-mesh/coral/internal/cli/helpers"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/constants"
)

type colonyInfo struct {
	ColonyID      string `json:"colony_id"`
	Application   string `json:"application"`
	Environment   string `json:"environment"`
	IsDefault     bool   `json:"is_default"`
	IsCurrent     bool   `json:"is_current"`
	Resolution    string `json:"resolution,omitempty"`
	CreatedAt     string `json:"created_at"`
	StoragePath   string `json:"storage_path"`
	WireGuardPort int    `json:"wireguard_port"`
	ConnectPort   int    `json:"connect_port"`
	MeshIPv4      string `json:"mesh_ipv4"`
	Running       bool   `json:"running"`
	LocalEndpoint string `json:"local_endpoint,omitempty"`
	MeshEndpoint  string `json:"mesh_endpoint,omitempty"`
}

func newListCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all configured colonies",
		Long: `Display all colonies that have been initialized on this system.

The current active colony is marked with * in the output. The RESOLUTION column
shows where the current colony was resolved from (env, project, or global).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create resolver to get current colony and source (RFD 050).
			resolver, err := config.NewResolver()
			if err != nil {
				return fmt.Errorf("failed to create config resolver: %w", err)
			}

			loader := resolver.GetLoader()
			colonyIDs, err := loader.ListColonies()
			if err != nil {
				return fmt.Errorf("failed to list colonies: %w", err)
			}

			if len(colonyIDs) == 0 {
				fmt.Println("No colonies configured.")
				fmt.Println("\nRun 'coral init <app-name>' to create one.")
				return nil
			}

			globalConfig, err := loader.LoadGlobalConfig()
			if err != nil {
				return fmt.Errorf("failed to load global config: %w", err)
			}

			// Get current colony and resolution source (RFD 050).
			currentColonyID, source, _ := resolver.ResolveWithSource()

			// Collect colony data.
			colonies := []colonyInfo{}
			for _, id := range colonyIDs {
				cfg, err := loader.LoadColonyConfig(id)
				if err != nil {
					continue
				}

				connectPort := cfg.Services.ConnectPort
				if connectPort == 0 {
					connectPort = constants.DefaultColonyPort
				}

				info := colonyInfo{
					ColonyID:      cfg.ColonyID,
					Application:   cfg.ApplicationName,
					Environment:   cfg.Environment,
					IsDefault:     cfg.ColonyID == globalConfig.DefaultColony,
					IsCurrent:     cfg.ColonyID == currentColonyID,
					CreatedAt:     cfg.CreatedAt.Format(time.RFC3339),
					StoragePath:   cfg.StoragePath,
					WireGuardPort: cfg.WireGuard.Port,
					ConnectPort:   connectPort,
					MeshIPv4:      cfg.WireGuard.MeshIPv4,
				}

				// Add resolution source for current colony (RFD 050).
				if info.IsCurrent {
					info.Resolution = source.Type
				}

				// Check if running using shared helper
				// We use a short timeout context for the checking logic inside the helper if needed,
				// but the helper itself takes a context.

				// Use a timeout context for the check
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()

				// We don't care about the client, just if we can connect
				_, _, err = helpers.GetColonyClientWithFallback(ctx, id)
				if err == nil {
					info.Running = true
				}

				colonies = append(colonies, info)
			}

			// Use formatter for non-table output.
			if format != string(helpers.FormatTable) {
				formatter, err := helpers.NewFormatter(helpers.OutputFormat(format))
				if err != nil {
					return err
				}
				return formatter.Format(colonies, os.Stdout)
			}

			// Table output with * marker for current colony (RFD 050).
			fmt.Printf("%-3s %-30s %-15s %-12s %-10s %s\n", "", "COLONY-ID", "APPLICATION", "ENVIRONMENT", "RESOLUTION", "STATUS")
			for _, info := range colonies {
				// Current marker and resolution (RFD 050).
				currentMarker := ""
				resolution := "-"
				if info.IsCurrent {
					currentMarker = "*"
					resolution = info.Resolution
				}

				// Determine running status.
				runningStatus := ""
				if info.Running {
					runningStatus = "running"
				}

				fmt.Printf("%-3s %-30s %-15s %-12s %-10s %s\n",
					currentMarker,
					truncate(info.ColonyID, 30),
					truncate(info.Application, 15),
					truncate(info.Environment, 12),
					resolution,
					runningStatus,
				)
			}

			return nil
		},
	}

	helpers.AddFormatFlag(cmd, &format, helpers.FormatTable, []helpers.OutputFormat{
		helpers.FormatTable,
		helpers.FormatJSON,
		helpers.FormatCSV,
		helpers.FormatYAML,
	})

	return cmd
}
