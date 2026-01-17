package colony

import (
	"context"
	"fmt"
	"os"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	"github.com/coral-mesh/coral/internal/cli/helpers"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/constants"
)

func newStatusCmd() *cobra.Command {
	var (
		format   string
		colonyID string
	)

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show colony status and WireGuard configuration",
		Long: `Display the current status of the colony including:
- Basic colony information
- WireGuard configuration (public key, mesh IPs, ports)
- Discovery service endpoint
- Connected peers (when colony is running)

This command is useful for troubleshooting connectivity issues.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create resolver
			resolver, err := config.NewResolver()
			if err != nil {
				return fmt.Errorf("failed to create config resolver: %w", err)
			}

			// Resolve colony ID
			if colonyID == "" {
				colonyID, err = resolver.ResolveColonyID()
				if err != nil {
					return fmt.Errorf("failed to resolve colony: %w\n\nRun 'coral init <app-name>' to create a colony", err)
				}
			}

			// Load colony configuration
			loader := resolver.GetLoader()
			colonyConfig, err := loader.LoadColonyConfig(colonyID)
			if err != nil {
				return fmt.Errorf("failed to load colony config: %w", err)
			}

			// Load global config
			globalConfig, err := loader.LoadGlobalConfig()
			if err != nil {
				return fmt.Errorf("failed to load global config: %w", err)
			}

			// Get connect port
			connectPort := colonyConfig.Services.ConnectPort
			if connectPort == 0 {
				connectPort = 9000
			}

			// Try to query running colony for real-time status.
			// Use shared helper which tries env var -> remote -> local -> mesh.
			var runtimeStatus *colonyv1.GetStatusResponse

			// We don't want to error out if connection fails, because we can still show static config.
			client, _, err := helpers.GetColonyClientWithFallback(cmd.Context(), colonyID)
			if err == nil {
				// Connection successful, get full status
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				if resp, err := client.GetStatus(ctx, connect.NewRequest(&colonyv1.GetStatusRequest{})); err == nil {
					runtimeStatus = resp.Msg
				}
			}

			// Prepare output data.
			output := map[string]interface{}{
				"colony_id":    colonyConfig.ColonyID,
				"application":  colonyConfig.ApplicationName,
				"environment":  colonyConfig.Environment,
				"storage_path": colonyConfig.StoragePath,
				"wireguard": map[string]interface{}{
					"public_key":        colonyConfig.WireGuard.PublicKey,
					"port":              colonyConfig.WireGuard.Port,
					"mesh_ipv4":         colonyConfig.WireGuard.MeshIPv4,
					"mesh_ipv6":         colonyConfig.WireGuard.MeshIPv6,
					"mesh_network_ipv4": colonyConfig.WireGuard.MeshNetworkIPv4,
					"mesh_network_ipv6": colonyConfig.WireGuard.MeshNetworkIPv6,
					"mtu":               colonyConfig.WireGuard.MTU,
				},
				"discovery_endpoint": globalConfig.Discovery.Endpoint,
				"connect_port":       connectPort,
			}

			// Add runtime status if colony is running.
			if runtimeStatus != nil {
				output["status"] = runtimeStatus.Status
				output["uptime_seconds"] = runtimeStatus.UptimeSeconds
				output["agent_count"] = runtimeStatus.AgentCount
				output["active_agent_count"] = runtimeStatus.ActiveAgentCount
				output["degraded_agent_count"] = runtimeStatus.DegradedAgentCount
				output["storage_bytes"] = runtimeStatus.StorageBytes
				output["dashboard_url"] = runtimeStatus.DashboardUrl
				output["started_at"] = runtimeStatus.StartedAt.AsTime().Format(time.RFC3339)

				// Add network endpoint information from runtime.
				networkEndpoints := map[string]interface{}{
					"local_endpoint":       fmt.Sprintf("http://localhost:%d", runtimeStatus.ConnectPort),
					"mesh_endpoint":        fmt.Sprintf("http://%s:%d", runtimeStatus.MeshIpv4, runtimeStatus.ConnectPort),
					"wireguard_port":       runtimeStatus.WireguardPort,
					"wireguard_public_key": runtimeStatus.WireguardPublicKey,
					"wireguard_endpoints":  runtimeStatus.WireguardEndpoints,
				}
				if runtimeStatus.PublicEndpointUrl != "" {
					networkEndpoints["public_endpoint"] = runtimeStatus.PublicEndpointUrl
				}
				output["network_endpoints"] = networkEndpoints
			} else {
				output["status"] = "configured"
			}

			// Use formatter for non-table output.
			if format != string(helpers.FormatTable) {
				formatter, err := helpers.NewFormatter(helpers.OutputFormat(format))
				if err != nil {
					return err
				}
				return formatter.Format(output, os.Stdout)
			}

			// Human-readable output
			fmt.Println("Colony Status")
			fmt.Println("=============")
			fmt.Println()
			fmt.Printf("Colony ID:     %s\n", colonyConfig.ColonyID)
			fmt.Printf("Application:   %s\n", colonyConfig.ApplicationName)
			fmt.Printf("Environment:   %s\n", colonyConfig.Environment)
			fmt.Printf("Storage:       %s\n", colonyConfig.StoragePath)

			// Show runtime status if colony is running
			if runtimeStatus != nil {
				fmt.Println()
				fmt.Println("Runtime Status:")
				fmt.Printf("  Status:        %s\n", runtimeStatus.Status)
				fmt.Printf("  Uptime:        %s\n", formatDuration(time.Duration(runtimeStatus.UptimeSeconds)*time.Second))

				// Format agent count with health breakdown
				agentCountStr := fmt.Sprintf("%d connected", runtimeStatus.AgentCount)
				if runtimeStatus.ActiveAgentCount > 0 || runtimeStatus.DegradedAgentCount > 0 {
					agentCountStr = fmt.Sprintf("%d connected (✓%d ⚠%d)", runtimeStatus.AgentCount, runtimeStatus.ActiveAgentCount, runtimeStatus.DegradedAgentCount)
				}
				fmt.Printf("  Agents:        %s\n", agentCountStr)

				if runtimeStatus.StorageBytes > 0 {
					fmt.Printf("  Storage Used:  %s\n", formatBytes(runtimeStatus.StorageBytes))
				}

				if runtimeStatus.DashboardUrl != "" {
					fmt.Printf("  Dashboard:     %s\n", runtimeStatus.DashboardUrl)
				}

				// Show network endpoints
				fmt.Println()
				fmt.Println("Network Endpoints (Running):")
				fmt.Printf("  Local (CLI):       http://localhost:%d\n", runtimeStatus.ConnectPort)
				fmt.Printf("  Mesh (Agents):     http://%s:%d\n", runtimeStatus.MeshIpv4, runtimeStatus.ConnectPort)
				if runtimeStatus.PublicEndpointUrl != "" {
					fmt.Printf("  Public (CLI):      %s\n", runtimeStatus.PublicEndpointUrl)
				}
				fmt.Printf("  WireGuard Listen:  udp://0.0.0.0:%d\n", runtimeStatus.WireguardPort)
				fmt.Printf("  WireGuard Pubkey:  %s\n", truncateKey(runtimeStatus.WireguardPublicKey))
				if len(runtimeStatus.WireguardEndpoints) > 0 {
					fmt.Printf("  Registered Endpoints: %s\n", runtimeStatus.WireguardEndpoints[0])
					for _, ep := range runtimeStatus.WireguardEndpoints[1:] {
						fmt.Printf("                        %s\n", ep)
					}
				}
			}

			fmt.Println()

			fmt.Println("WireGuard Mesh Configuration:")
			fmt.Printf("  Public Key:     %s\n", colonyConfig.WireGuard.PublicKey)
			fmt.Printf("  Listen Port:    %d (UDP)\n", colonyConfig.WireGuard.Port)

			// Show interface name - use stored name if available, otherwise predict
			if colonyConfig.WireGuard.InterfaceName != "" {
				fmt.Printf("  Interface:      %s (last used)\n", colonyConfig.WireGuard.InterfaceName)
			} else {
				interfaceInfo := getInterfaceInfo()
				fmt.Printf("  Interface:      %s\n", interfaceInfo)
			}

			fmt.Printf("  Mesh IPv4:      %s\n", colonyConfig.WireGuard.MeshIPv4)
			if colonyConfig.WireGuard.MeshIPv6 != "" {
				fmt.Printf("  Mesh IPv6:      %s\n", colonyConfig.WireGuard.MeshIPv6)
			}
			fmt.Printf("  Mesh Subnet:    %s\n", colonyConfig.WireGuard.MeshNetworkIPv4)
			if colonyConfig.WireGuard.MeshNetworkIPv6 != "" {
				fmt.Printf("  IPv6 Subnet:    %s\n", colonyConfig.WireGuard.MeshNetworkIPv6)
			}
			fmt.Printf("  MTU:            %d\n", colonyConfig.WireGuard.MTU)
			fmt.Println()

			fmt.Println("Services:")
			fmt.Printf("  Discovery:      %s\n", globalConfig.Discovery.Endpoint)
			fmt.Printf("  Agent Connect:  %s:%d (gRPC/Connect)\n", colonyConfig.WireGuard.MeshIPv4, connectPort)
			fmt.Printf("  Dashboard:      http://localhost:%d (planned)\n", constants.DefaultDashboardPort)
			fmt.Println()

			fmt.Println("Agent Connection Info:")
			fmt.Println("  1. Agents query discovery service:")
			fmt.Printf("     Mesh ID: %s\n", colonyConfig.ColonyID)
			fmt.Println()
			fmt.Println("  2. Discovery returns WireGuard endpoint:")
			fmt.Printf("     Public Key: %s\n", colonyConfig.WireGuard.PublicKey)
			fmt.Printf("     UDP Port:   %d\n", colonyConfig.WireGuard.Port)
			fmt.Println()
			fmt.Println("  3. Agents establish WireGuard tunnel, then register:")
			fmt.Printf("     Colony Mesh IP: %s:%d\n", colonyConfig.WireGuard.MeshIPv4, connectPort)
			fmt.Println()

			if runtimeStatus != nil {
				fmt.Printf("Status: Colony is running (%s)\n", runtimeStatus.Status)
			} else {
				fmt.Println("Status: Colony is configured (not running)")
				fmt.Println()
				fmt.Println("To start the colony:")
				fmt.Println("  coral colony start")
			}
			fmt.Println()

			return nil
		},
	}

	helpers.AddFormatFlag(cmd, &format, helpers.FormatTable, []helpers.OutputFormat{
		helpers.FormatTable,
		helpers.FormatJSON,
		helpers.FormatYAML,
	})
	helpers.AddColonyFlag(cmd, &colonyID)

	return cmd
}
