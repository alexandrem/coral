package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"connectrpc.com/connect"

	colonyv1 "github.com/coral-io/coral/coral/colony/v1"
	"github.com/coral-io/coral/coral/colony/v1/colonyv1connect"
	"github.com/coral-io/coral/internal/config"
	"github.com/coral-io/coral/internal/discovery/client"
	"github.com/coral-io/coral/pkg/version"
)

// colonyStatusInfo holds status information for a single colony.
type colonyStatusInfo struct {
	ColonyID        string `json:"colony_id"`
	Application     string `json:"application"`
	Environment     string `json:"environment"`
	IsDefault       bool   `json:"is_default"`
	Running         bool   `json:"running"`
	Status          string `json:"status"`
	UptimeSeconds   int64  `json:"uptime_seconds,omitempty"`
	AgentCount      int32  `json:"agent_count,omitempty"`
	WireGuardPort   int    `json:"wireguard_port"`
	ConnectPort     int    `json:"connect_port"`
	LocalEndpoint   string `json:"local_endpoint,omitempty"`
	MeshEndpoint    string `json:"mesh_endpoint,omitempty"`
	MeshIPv4        string `json:"mesh_ipv4"`
	WireGuardPubkey string `json:"wireguard_pubkey,omitempty"`
}

// statusOutput holds the complete status output structure for JSON mode.
type statusOutput struct {
	Discovery struct {
		Endpoint string `json:"endpoint"`
		Healthy  bool   `json:"healthy"`
	} `json:"discovery"`
	Colonies []colonyStatusInfo `json:"colonies"`
	Summary  struct {
		Total   int `json:"total"`
		Running int `json:"running"`
		Stopped int `json:"stopped"`
	} `json:"summary"`
	Version string `json:"version"`
}

// newStatusCmd creates the global status command.
func newStatusCmd() *cobra.Command {
	var (
		jsonOutput bool
		verbose    bool
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
			discoveryClient := client.New(globalConfig.Discovery.Endpoint, 2*time.Second)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if err := discoveryClient.Health(ctx); err == nil {
				discoveryHealthy = true
			}
			cancel()

			// List all configured colonies
			colonyIDs, err := loader.ListColonies()
			if err != nil {
				return fmt.Errorf("failed to list colonies: %w", err)
			}

			// Query all colonies in parallel
			coloniesInfo := queryColoniesInParallel(loader, colonyIDs, globalConfig.DefaultColony)

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

			// Output in requested format
			if jsonOutput {
				return outputJSON(globalConfig, coloniesInfo, discoveryHealthy, runningCount, stoppedCount)
			}

			return outputTable(globalConfig, coloniesInfo, discoveryHealthy, runningCount, stoppedCount, verbose)
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show additional details (WireGuard public keys, endpoints)")

	return cmd
}

// queryColoniesInParallel queries all colonies concurrently with timeout.
func queryColoniesInParallel(loader *config.Loader, colonyIDs []string, defaultColony string) []colonyStatusInfo {
	var wg sync.WaitGroup
	results := make([]colonyStatusInfo, len(colonyIDs))

	for i, colonyID := range colonyIDs {
		wg.Add(1)
		go func(index int, id string) {
			defer wg.Done()
			results[index] = queryColonyStatus(loader, id, defaultColony)
		}(i, colonyID)
	}

	wg.Wait()
	return results
}

// queryColonyStatus queries a single colony's status with timeout.
func queryColonyStatus(loader *config.Loader, colonyID string, defaultColony string) colonyStatusInfo {
	info := colonyStatusInfo{
		ColonyID:  colonyID,
		IsDefault: colonyID == defaultColony,
		Status:    "stopped",
		Running:   false,
	}

	// Load colony config
	cfg, err := loader.LoadColonyConfig(colonyID)
	if err != nil {
		return info
	}

	info.Application = cfg.ApplicationName
	info.Environment = cfg.Environment
	info.WireGuardPort = cfg.WireGuard.Port
	info.MeshIPv4 = cfg.WireGuard.MeshIPv4
	info.WireGuardPubkey = cfg.WireGuard.PublicKey

	// Get connect port
	connectPort := cfg.Services.ConnectPort
	if connectPort == 0 {
		connectPort = 9000
	}
	info.ConnectPort = connectPort

	// Try to query running colony (quick timeout)
	baseURL := fmt.Sprintf("http://localhost:%d", connectPort)
	client := colonyv1connect.NewColonyServiceClient(http.DefaultClient, baseURL)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	req := connect.NewRequest(&colonyv1.GetStatusRequest{})
	resp, err := client.GetStatus(ctx, req)
	if err == nil && resp.Msg != nil {
		// Colony is running
		info.Running = true
		info.Status = resp.Msg.Status
		info.UptimeSeconds = resp.Msg.UptimeSeconds
		info.AgentCount = resp.Msg.AgentCount
		info.LocalEndpoint = fmt.Sprintf("http://localhost:%d", resp.Msg.ConnectPort)
		info.MeshEndpoint = fmt.Sprintf("http://%s:%d", resp.Msg.MeshIpv4, resp.Msg.ConnectPort)
	}

	return info
}

// outputJSON outputs the status in JSON format.
func outputJSON(globalConfig *config.GlobalConfig, colonies []colonyStatusInfo, discoveryHealthy bool, runningCount, stoppedCount int) error {
	output := statusOutput{
		Colonies: colonies,
		Version:  version.Version,
	}

	output.Discovery.Endpoint = globalConfig.Discovery.Endpoint
	output.Discovery.Healthy = discoveryHealthy

	output.Summary.Total = len(colonies)
	output.Summary.Running = runningCount
	output.Summary.Stopped = stoppedCount

	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	fmt.Println(string(data))
	return nil
}

// outputTable outputs the status in human-readable table format.
func outputTable(globalConfig *config.GlobalConfig, colonies []colonyStatusInfo, discoveryHealthy bool, runningCount, stoppedCount int, verbose bool) error {
	fmt.Println("Coral Environment Status")
	fmt.Println("========================")
	fmt.Println()

	// Discovery section
	healthStatus := "healthy"
	if !discoveryHealthy {
		healthStatus = "unhealthy"
	}
	fmt.Printf("Discovery: %s (%s)\n", globalConfig.Discovery.Endpoint, healthStatus)

	// Summary section
	fmt.Printf("Colonies:  %d total (%d running, %d stopped)\n", len(colonies), runningCount, stoppedCount)
	fmt.Printf("Version:   coral %s\n", version.Version)
	fmt.Println()

	// No colonies configured
	if len(colonies) == 0 {
		fmt.Println("No colonies configured.")
		fmt.Println()
		fmt.Println("Run 'coral init <app-name>' to create one.")
		return nil
	}

	// Table header
	if verbose {
		fmt.Printf("%-16s %-8s %-11s %-9s %-7s %-14s %-12s %-20s\n",
			"COLONY ID", "ENV", "STATUS", "UPTIME", "AGENTS", "NETWORK", "MESH IP", "PUBLIC KEY")
		fmt.Println("-------------------------------------------------------------------------------------------------------------------------------------")
	} else {
		fmt.Printf("%-16s %-8s %-11s %-9s %-7s %-14s %s\n",
			"COLONY ID", "ENV", "STATUS", "UPTIME", "AGENTS", "NETWORK", "ENDPOINTS")
		fmt.Println("------------------------------------------------------------------------------------------------------------------")
	}

	// Table rows
	for _, info := range colonies {
		colonyIDDisplay := info.ColonyID
		if info.IsDefault {
			colonyIDDisplay += "*"
		}

		// Truncate colony ID if too long
		if len(colonyIDDisplay) > 16 {
			colonyIDDisplay = colonyIDDisplay[:13] + "..."
		}

		// Format uptime
		uptimeStr := "-"
		if info.Running && info.UptimeSeconds > 0 {
			uptimeStr = formatUptime(time.Duration(info.UptimeSeconds) * time.Second)
		}

		// Format agent count
		agentsStr := "-"
		if info.Running {
			agentsStr = fmt.Sprintf("%d", info.AgentCount)
		}

		// Format network ports
		networkStr := fmt.Sprintf("%d/%d", info.WireGuardPort, info.ConnectPort)

		// Format environment (truncate if needed)
		envStr := info.Environment
		if len(envStr) > 8 {
			envStr = envStr[:5] + "..."
		}

		if verbose {
			// Verbose mode: show mesh IP and public key
			meshIP := info.MeshIPv4
			if meshIP == "" {
				meshIP = "-"
			}

			pubkey := "-"
			if info.WireGuardPubkey != "" {
				pubkey = truncateKey(info.WireGuardPubkey)
			}

			fmt.Printf("%-16s %-8s %-11s %-9s %-7s %-14s %-12s %-20s\n",
				colonyIDDisplay, envStr, info.Status, uptimeStr, agentsStr, networkStr, meshIP, pubkey)
		} else {
			// Normal mode: show endpoints
			endpointsStr := "-"
			if info.Running {
				endpointsStr = fmt.Sprintf("localhost:%d, %s:%d", info.ConnectPort, info.MeshIPv4, info.ConnectPort)
			}

			fmt.Printf("%-16s %-8s %-11s %-9s %-7s %-14s %s\n",
				colonyIDDisplay, envStr, info.Status, uptimeStr, agentsStr, networkStr, endpointsStr)
		}
	}

	fmt.Println()

	// Footer with default colony marker explanation
	hasDefault := false
	for _, info := range colonies {
		if info.IsDefault {
			hasDefault = true
			break
		}
	}
	if hasDefault {
		fmt.Println("* default colony")
		fmt.Println()
	}

	// Verbose mode: show detailed endpoints
	if verbose {
		fmt.Println("Endpoints:")
		for _, info := range colonies {
			if info.Running {
				fmt.Printf("  %-16s http://localhost:%d (local), http://%s:%d (mesh)\n",
					info.ColonyID+":", info.ConnectPort, info.MeshIPv4, info.ConnectPort)
			}
		}
		fmt.Println()
	}

	// Action hints
	fmt.Println("Use 'coral colony status <id>' for detailed information")
	fmt.Println()

	return nil
}

// formatUptime formats uptime duration according to RFD 009 spec.
// < 1h: Show minutes and seconds (e.g., "15m 30s")
// 1h - 24h: Show hours and minutes (e.g., "5h 20m")
// > 24h: Show days and hours (e.g., "2d 3h")
func formatUptime(d time.Duration) string {
	if d < time.Hour {
		minutes := int(d.Minutes())
		seconds := int(d.Seconds()) % 60
		if minutes == 0 {
			return fmt.Sprintf("%ds", seconds)
		}
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}

	hours := int(d.Hours())
	if hours < 24 {
		minutes := int(d.Minutes()) % 60
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}

	days := hours / 24
	remainingHours := hours % 24
	return fmt.Sprintf("%dd %dh", days, remainingHours)
}

// truncateKey truncates a WireGuard public key for display.
func truncateKey(key string) string {
	if len(key) <= 20 {
		return key
	}
	return key[:12] + "..." + key[len(key)-4:]
}
