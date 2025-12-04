package status

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/pkg/version"
)

// Output holds the complete status output structure for JSON mode.
type Output struct {
	Discovery struct {
		Endpoint string `json:"endpoint"`
		Healthy  bool   `json:"healthy"`
	} `json:"discovery"`
	Colonies []ColonyStatusInfo `json:"colonies"`
	Summary  struct {
		Total   int `json:"total"`
		Running int `json:"running"`
		Stopped int `json:"stopped"`
	} `json:"summary"`
	Version string `json:"version"`
}

// Formatter handles formatting status output.
type Formatter struct {
	globalConfig *config.GlobalConfig
}

// NewFormatter creates a new status formatter.
func NewFormatter(globalConfig *config.GlobalConfig) *Formatter {
	return &Formatter{
		globalConfig: globalConfig,
	}
}

// OutputJSON outputs the status in JSON format.
func (f *Formatter) OutputJSON(colonies []ColonyStatusInfo, discoveryHealthy bool, runningCount, stoppedCount int) error {
	output := Output{
		Colonies: colonies,
		Version:  version.Version,
	}

	output.Discovery.Endpoint = f.globalConfig.Discovery.Endpoint
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

// OutputTable outputs the status in human-readable table format.
func (f *Formatter) OutputTable(colonies []ColonyStatusInfo, discoveryHealthy bool, runningCount, stoppedCount int, verbose bool) error {
	fmt.Println("Coral Environment Status")
	fmt.Println("========================")
	fmt.Println()

	// Discovery section
	healthStatus := "healthy"
	if !discoveryHealthy {
		healthStatus = "unhealthy"
	}
	fmt.Printf("Discovery: %s (%s)\n", f.globalConfig.Discovery.Endpoint, healthStatus)

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
		fmt.Printf("%-25s %-8s %-11s %-9s %-7s %-14s %-12s %-20s\n",
			"COLONY ID", "ENV", "STATUS", "UPTIME", "AGENTS", "NETWORK", "MESH IP", "PUBLIC KEY")
	} else {
		fmt.Printf("%-25s %-8s %-11s %-9s %-7s %-14s %s\n",
			"COLONY ID", "ENV", "STATUS", "UPTIME", "AGENTS", "NETWORK", "ENDPOINTS")
	}

	// Table rows
	for _, info := range colonies {
		colonyIDDisplay := info.ColonyID
		if info.IsDefault {
			colonyIDDisplay += "*"
		}

		// Truncate colony ID if too long
		if len(colonyIDDisplay) > 25 {
			colonyIDDisplay = colonyIDDisplay[:22] + "..."
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

			fmt.Printf("%-25s %-8s %-11s %-9s %-7s %-14s %-12s %-20s\n",
				colonyIDDisplay, envStr, info.Status, uptimeStr, agentsStr, networkStr, meshIP, pubkey)
		} else {
			// Normal mode: show endpoints
			endpointsStr := "-"
			if info.Running {
				endpointsStr = fmt.Sprintf("localhost:%d, %s:%d", info.ConnectPort, info.MeshIPv4, info.ConnectPort)
			}

			fmt.Printf("%-25s %-8s %-11s %-9s %-7s %-14s %s\n",
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
