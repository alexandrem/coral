package query

import (
	"context"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	colonypb "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-mesh/coral/internal/cli/helpers"
)

func NewServicesCmd() *cobra.Command {
	var namespace string
	var timeRange string
	var sourceFilter string
	var shadowMode bool

	cmd := &cobra.Command{
		Use:   "services",
		Short: "List discovered services",
		Long: `List all services discovered by Coral.

Shows services from both registry (explicitly connected) and telemetry data (auto-observed).

Visual Indicators:
  ● (solid circle)  - Active and verified (registered + telemetry)
  ◐ (half circle)   - Observed from telemetry only
  ○ (hollow circle) - Registered but unhealthy/no telemetry

Examples:
  coral query services                          # All services (default: 1h)
  coral query services --namespace prod         # Filter by namespace
  coral query services --since 24h              # Extend telemetry lookback
  coral query services --source registered      # Only registered services
  coral query services --source observed        # Only telemetry-observed
  coral query services --source verified        # Only verified services
  coral query services --shadow                 # Alias for --source observed
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			// Handle --shadow flag as alias for --source observed.
			if shadowMode {
				if sourceFilter != "" {
					return fmt.Errorf("cannot use both --shadow and --source flags")
				}
				sourceFilter = "observed"
			}

			// Resolve colony URL
			colonyAddr, err := helpers.GetColonyURL("")
			if err != nil {
				return fmt.Errorf("failed to resolve colony address: %w", err)
			}

			// Create colony client
			client := colonyv1connect.NewColonyServiceClient(
				http.DefaultClient,
				colonyAddr,
			)

			// Build request
			req := &colonypb.ListServicesRequest{
				Namespace: namespace,
				TimeRange: timeRange,
			}

			// Parse source filter if provided.
			if sourceFilter != "" {
				var source colonypb.ServiceSource
				switch sourceFilter {
				case "registered":
					source = colonypb.ServiceSource_SERVICE_SOURCE_REGISTERED
				case "observed":
					source = colonypb.ServiceSource_SERVICE_SOURCE_OBSERVED
				case "verified":
					source = colonypb.ServiceSource_SERVICE_SOURCE_VERIFIED
				default:
					return fmt.Errorf("invalid source filter: %s (must be 'registered', 'observed', or 'verified')", sourceFilter)
				}
				req.SourceFilter = &source
			}

			// Execute RPC
			resp, err := client.ListServices(ctx, connect.NewRequest(req))
			if err != nil {
				return fmt.Errorf("failed to list services: %w", err)
			}

			// Print result
			if len(resp.Msg.Services) == 0 {
				fmt.Println("No services found")
				return nil
			}

			fmt.Printf("Found %d service(s):\n\n", len(resp.Msg.Services))
			for _, svc := range resp.Msg.Services {
				// Visual indicator based on source and status.
				icon := getServiceIcon(svc.Source, svc.Status)
				fmt.Printf("%s %s", icon, svc.Name)

				if svc.Namespace != "" {
					fmt.Printf(" (%s)", svc.Namespace)
				}

				fmt.Printf(" - %d instance(s)", svc.InstanceCount)

				// Display status if available.
				if svc.Status != nil {
					statusStr := formatServiceStatus(*svc.Status)
					if statusStr != "" {
						fmt.Printf(" [%s]", statusStr)
					}
				}

				fmt.Println()

				// Display source and last seen on second line.
				fmt.Printf("  Source: %s", formatServiceSource(svc.Source))
				if svc.LastSeen != nil {
					fmt.Printf(" | Last seen: %s", svc.LastSeen.AsTime().Format("15:04:05"))
				}
				if svc.AgentId != nil {
					fmt.Printf(" | Agent: %s", *svc.AgentId)
				}
				fmt.Println()
				fmt.Println()
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&namespace, "namespace", "", "Filter by namespace")
	cmd.Flags().StringVar(&timeRange, "since", "", "Time range for telemetry discovery (e.g., '1h', '24h')")
	cmd.Flags().StringVar(&sourceFilter, "source", "", "Filter by source: 'registered', 'observed', or 'verified'")
	cmd.Flags().BoolVar(&shadowMode, "shadow", false, "Show only observed services (alias for --source observed)")
	return cmd
}

// getServiceIcon returns a visual indicator based on service source and status.
func getServiceIcon(source colonypb.ServiceSource, status *colonypb.ServiceStatus) string {
	if source == colonypb.ServiceSource_SERVICE_SOURCE_VERIFIED &&
		status != nil && *status == colonypb.ServiceStatus_SERVICE_STATUS_ACTIVE {
		return "●" // Solid circle - verified and active
	}
	if source == colonypb.ServiceSource_SERVICE_SOURCE_OBSERVED {
		return "◐" // Half circle - observed from telemetry only
	}
	return "○" // Hollow circle - registered but unhealthy/no telemetry
}

// formatServiceSource returns a human-readable source string.
func formatServiceSource(source colonypb.ServiceSource) string {
	switch source {
	case colonypb.ServiceSource_SERVICE_SOURCE_REGISTERED:
		return "REGISTERED (Registry only)"
	case colonypb.ServiceSource_SERVICE_SOURCE_OBSERVED:
		return "OBSERVED (Telemetry only)"
	case colonypb.ServiceSource_SERVICE_SOURCE_VERIFIED:
		return "VERIFIED (Registered + Observed)"
	default:
		return "UNKNOWN"
	}
}

// formatServiceStatus returns a human-readable status string.
func formatServiceStatus(status colonypb.ServiceStatus) string {
	switch status {
	case colonypb.ServiceStatus_SERVICE_STATUS_ACTIVE:
		return "ACTIVE"
	case colonypb.ServiceStatus_SERVICE_STATUS_UNHEALTHY:
		return "UNHEALTHY"
	case colonypb.ServiceStatus_SERVICE_STATUS_DISCONNECTED:
		return "DISCONNECTED"
	case colonypb.ServiceStatus_SERVICE_STATUS_OBSERVED_ONLY:
		return "OBSERVED"
	default:
		return ""
	}
}
