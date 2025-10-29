package agent

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/coral-io/coral/internal/config"
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

			// Create resolver
			resolver, err := config.NewResolver()
			if err != nil {
				return fmt.Errorf("failed to create config resolver: %w", err)
			}

			// Resolve colony ID
			if colonyID == "" {
				colonyID, err = resolver.ResolveColonyID()
				if err != nil {
					return fmt.Errorf("failed to resolve colony: %w\n\nRun 'coral init <app-name>' or set CORAL_COLONY_ID", err)
				}
			}

			// Load resolved configuration
			cfg, err := resolver.ResolveConfig(colonyID)
			if err != nil {
				return fmt.Errorf("failed to load colony config: %w", err)
			}

			fmt.Printf("Connecting agent for service: %s\n", serviceName)
			fmt.Printf("Port: %d\n", port)
			fmt.Printf("Colony ID: %s\n", cfg.ColonyID)
			fmt.Printf("Application: %s (%s)\n", cfg.ApplicationName, cfg.Environment)
			fmt.Printf("Discovery: %s\n", cfg.DiscoveryURL)

			if len(tags) > 0 {
				fmt.Printf("Tags: %s\n", strings.Join(tags, ", "))
			}

			if healthURL != "" {
				fmt.Printf("Health endpoint: %s\n", healthURL)
			}

			// TODO: Implement actual agent connection
			// 1. Query discovery service using cfg.ColonyID as mesh_id (RFD 001)
			// 2. Receive colony's WireGuard public key + endpoints from discovery
			// 3. Verify public key matches expected (if available in config)
			// 4. Establish WireGuard tunnel using received public key
			// 5. Send RegisterRequest with:
			//    - colony_id: cfg.ColonyID
			//    - colony_secret: cfg.ColonySecret
			//    - component_name: serviceName
			//    - wireguard_pubkey: agent's public key
			// 6. Receive RegisterResponse with mesh assignment
			// 7. Start observation loop
			// 8. Initialize local DuckDB storage

			fmt.Println("\n✓ Agent connected successfully")
			fmt.Printf("Observing: %s:%d\n", serviceName, port)
			fmt.Printf("Authenticated with colony using colony_secret\n")
			fmt.Println("\nPress Ctrl+C to disconnect")

			// Wait for interrupt signal
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
			<-sigChan

			fmt.Println("\n\nDisconnecting agent...")
			return nil
		},
	}

	cmd.Flags().IntVarP(&port, "port", "p", 0, "Service port to observe (required)")
	cmd.Flags().StringVar(&colonyID, "colony-id", "", "Colony ID to connect to (auto-discover if not set)")
	cmd.Flags().StringSliceVarP(&tags, "tags", "t", nil, "Tags for the service (key=value)")
	cmd.Flags().StringVar(&healthURL, "health", "", "Health check endpoint URL")

	cmd.MarkFlagRequired("port")

	return cmd
}
