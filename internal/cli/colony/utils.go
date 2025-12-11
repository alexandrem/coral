package colony

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	agentv1 "github.com/coral-mesh/coral/coral/agent/v1"
	colonyv1 "github.com/coral-mesh/coral/coral/colony/v1"
	meshv1 "github.com/coral-mesh/coral/coral/mesh/v1"
	"github.com/coral-mesh/coral/internal/logging"
	"github.com/coral-mesh/coral/internal/privilege"
	runtimepkg "github.com/coral-mesh/coral/internal/runtime"
)

// performPreflightChecks verifies that the system is ready for colony startup.
// This includes checking for required capabilities (CAP_NET_ADMIN on Linux) or root access (macOS).
// Running this early ensures we ask for sudo password at the beginning if needed.
func performPreflightChecks(logger logging.Logger) error {
	logger.Info().Msg("Running preflight checks...")

	// Platform-specific privilege checks.
	if runtime.GOOS == "linux" {
		// On Linux, check for CAP_NET_ADMIN capability (least privilege).
		caps, err := runtimepkg.DetectLinuxCapabilities()
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to detect Linux capabilities, falling back to root check")
			// Fall back to checking for root if capability detection fails.
			if !privilege.IsRoot() {
				return fmt.Errorf("colony requires CAP_NET_ADMIN capability or root access")
			}
		} else if !caps.CapNetAdmin {
			// Missing CAP_NET_ADMIN - provide helpful error message.
			binaryPath, err := os.Executable()
			if err != nil {
				binaryPath = "/path/to/coral"
			}
			return fmt.Errorf(
				"colony requires CAP_NET_ADMIN capability for network management.\n\n"+
					"To grant this capability (one-time setup):\n"+
					"  sudo setcap 'cap_net_admin+ep' %s\n\n"+
					"Then run without sudo:\n"+
					"  coral colony start\n\n"+
					"Or run with sudo:\n"+
					"  sudo coral colony start",
				binaryPath,
			)
		}
		logger.Debug().Msg("CAP_NET_ADMIN capability detected")
	} else {
		// On macOS and other platforms, we need root (no capability system).
		if !privilege.IsRoot() {
			return fmt.Errorf("colony must be run with sudo on macOS:\n  sudo coral colony start")
		}
		logger.Debug().Msg("Running as root")
	}

	// If running via sudo, verify we can detect the original user.
	if privilege.IsRunningUnderSudo() {
		userCtx, err := privilege.DetectOriginalUser()
		if err != nil {
			logger.Warn().Err(err).Msg("Could not detect original user from sudo environment")
		} else {
			logger.Debug().
				Str("user", userCtx.Username).
				Int("uid", userCtx.UID).
				Msg("Detected original user from sudo")
		}
	}

	// Verify we can spawn the tun-helper subprocess.
	// We don't actually create a TUN device here, just verify the helper exists.
	binaryPath := os.Getenv("CORAL_TUN_HELPER_PATH")
	if binaryPath == "" {
		var err error
		binaryPath, err = os.Executable()
		if err != nil {
			return fmt.Errorf("failed to get executable path: %w", err)
		}
	}

	// Check if the binary exists and is executable.
	if _, err := os.Stat(binaryPath); err != nil {
		return fmt.Errorf("coral binary not found at %s: %w", binaryPath, err)
	}

	logger.Info().Msg("Preflight checks passed")
	return nil
}

// gatherPlatformInfo gathers platform information for the colony.
func gatherPlatformInfo() map[string]interface{} {
	// Use a detector to get platform information.
	logger := logging.NewWithComponent(logging.Config{
		Level:  "error", // Only log errors for this detection
		Pretty: false,
	}, "platform-detector")

	detector := runtimepkg.NewDetector(logger, "dev")

	// Detect platform.
	ctx := context.Background()
	runtimeCtx, err := detector.Detect(ctx)
	if err != nil || runtimeCtx == nil || runtimeCtx.Platform == nil {
		// Fallback to basic info if detection fails.
		return map[string]interface{}{
			"os":   runtime.GOOS,
			"arch": runtime.GOARCH,
		}
	}

	// Return platform info.
	return map[string]interface{}{
		"os":         runtimeCtx.Platform.Os,
		"arch":       runtimeCtx.Platform.Arch,
		"os_version": runtimeCtx.Platform.OsVersion,
		"kernel":     runtimeCtx.Platform.Kernel,
	}
}

// formatDuration formats a duration in a human-readable format.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		minutes := int(d.Minutes())
		seconds := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh %dm", hours, minutes)
}

// formatBytes formats bytes in a human-readable format.
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// truncateKey truncates a WireGuard public key for display.
func truncateKey(key string) string {
	if len(key) <= 16 {
		return key
	}
	return key[:12] + "..." + key[len(key)-4:]
}

// truncate truncates a string to a maximum length.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// formatRuntimeTypeShort formats runtime type in short form.
func formatRuntimeTypeShort(rt agentv1.RuntimeContext) string {
	switch rt {
	case agentv1.RuntimeContext_RUNTIME_CONTEXT_NATIVE:
		return "Native"
	case agentv1.RuntimeContext_RUNTIME_CONTEXT_DOCKER:
		return "Docker"
	case agentv1.RuntimeContext_RUNTIME_CONTEXT_K8S_SIDECAR:
		return "K8s Sidecar"
	case agentv1.RuntimeContext_RUNTIME_CONTEXT_K8S_DAEMONSET:
		return "K8s DaemonSet"
	default:
		return "Unknown"
	}
}

// formatSidecarModeShort formats sidecar mode in short form.
func formatSidecarModeShort(sm agentv1.SidecarMode) string {
	switch sm {
	case agentv1.SidecarMode_SIDECAR_MODE_CRI:
		return "CRI"
	case agentv1.SidecarMode_SIDECAR_MODE_SHARED_NS:
		return "SharedNS"
	case agentv1.SidecarMode_SIDECAR_MODE_PASSIVE:
		return "Passive"
	default:
		return ""
	}
}

// formatCapabilitySymbol formats capability as a checkmark or X.
func formatCapabilitySymbol(supported bool) string {
	if supported {
		return "✅"
	}
	return "❌"
}

// formatVisibilityShort formats visibility scope in short form.
func formatVisibilityShort(vis *agentv1.VisibilityScope) string {
	if vis.AllPids {
		return "All host processes"
	}
	if vis.AllContainers {
		return "All containers"
	}
	if vis.PodScope {
		return "Pod only"
	}
	return "Limited"
}

// formatServicesList formats the services array for display (RFD 044).
func formatServicesList(services []*meshv1.ServiceInfo) string {
	if len(services) == 0 {
		return ""
	}

	serviceNames := make([]string, 0, len(services))
	for _, svc := range services {
		serviceNames = append(serviceNames, svc.Name)
	}
	return strings.Join(serviceNames, ", ")
}

// formatAgentStatus formats agent status with last seen time.
func formatAgentStatus(agent *colonyv1.Agent) string {
	lastSeen := agent.LastSeen.AsTime()
	elapsed := time.Since(lastSeen)
	var lastSeenStr string
	if elapsed < time.Minute {
		lastSeenStr = fmt.Sprintf("%ds ago", int(elapsed.Seconds()))
	} else if elapsed < time.Hour {
		lastSeenStr = fmt.Sprintf("%dm ago", int(elapsed.Minutes()))
	} else {
		lastSeenStr = fmt.Sprintf("%dh ago", int(elapsed.Hours()))
	}

	statusSymbol := "✅"
	switch agent.Status {
	case "degraded":
		statusSymbol = "⚠️"
	case "unhealthy":
		statusSymbol = "❌"
	}

	return fmt.Sprintf("%s %s (%s)", statusSymbol, agent.Status, lastSeenStr)
}
