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

	cmd := &cobra.Command{
		Use:   "services",
		Short: "List discovered services",
		Long: `List all services discovered by Coral.

Shows service names, namespaces, and instance counts.

Examples:
  coral query services                    # All services
  coral query services --namespace prod   # Filter by namespace
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

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

			// Execute RPC
			req := &colonypb.ListServicesRequest{
				Namespace: namespace,
			}

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
				fmt.Printf("• %s", svc.Name)
				if svc.Namespace != "" {
					fmt.Printf(" (%s)", svc.Namespace)
				}
				fmt.Printf(" - %d instance(s)", svc.InstanceCount)
				if svc.LastSeen != nil {
					fmt.Printf(" (last seen: %s)", svc.LastSeen.AsTime().Format("15:04:05"))
				}
				fmt.Println()
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&namespace, "namespace", "", "Filter by namespace")
	return cmd
}
