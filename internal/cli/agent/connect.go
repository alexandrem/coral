package agent

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// NewConnectCmd creates the connect command for agents
func NewConnectCmd() *cobra.Command {
	var (
		port      int
		colonyID  string
		tags      []string
		healthURL string
	)

	cmd := &cobra.Command{
		Use:   "connect <service-name>",
		Short: "Connect an agent to observe a service",
		Long: `Connect a Coral agent to observe a service or application component.

The agent will:
- Monitor the process health and resource usage
- Detect network connections and dependencies
- Report observations to the colony
- Store recent metrics locally

Example:
  coral connect frontend --port 3000
  coral connect api --port 8080 --tags version=2.1.0,region=us-east
  coral connect database --port 5432 --health http://localhost:5432/health`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			serviceName := args[0]

			fmt.Printf("Connecting agent for service: %s\n", serviceName)
			fmt.Printf("Port: %d\n", port)

			if colonyID != "" {
				fmt.Printf("Colony ID: %s\n", colonyID)
			} else {
				fmt.Println("Colony: auto-discover (local)")
			}

			if len(tags) > 0 {
				fmt.Printf("Tags: %s\n", strings.Join(tags, ", "))
			}

			if healthURL != "" {
				fmt.Printf("Health endpoint: %s\n", healthURL)
			}

			// TODO: Implement actual agent connection
			// - Discover colony (local or via discovery service)
			// - Establish WireGuard connection
			// - Register with colony
			// - Start observation loop
			// - Initialize local DuckDB storage

			fmt.Println("\n✓ Agent connected successfully")
			fmt.Printf("Observing: %s:%d\n", serviceName, port)
			fmt.Println("\nPress Ctrl+C to disconnect")

			// Block forever (would be replaced with actual observation loop)
			select {}
		},
	}

	cmd.Flags().IntVarP(&port, "port", "p", 0, "Service port to observe (required)")
	cmd.Flags().StringVar(&colonyID, "colony-id", "", "Colony ID to connect to (auto-discover if not set)")
	cmd.Flags().StringSliceVarP(&tags, "tags", "t", nil, "Tags for the service (key=value)")
	cmd.Flags().StringVar(&healthURL, "health", "", "Health check endpoint URL")

	cmd.MarkFlagRequired("port")

	return cmd
}
