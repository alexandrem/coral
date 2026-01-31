package helpers

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// InitResult contains parsed output from `coral init`.
type InitResult struct {
	ColonyID     string
	BootstrapPSK string
	Output       string
}

// RunCoralInit executes `coral init` and returns the generated colony ID.
func RunCoralInit(ctx context.Context, appName, environment, storagePath string) (string, error) {
	result, err := RunCoralInitFull(ctx, appName, environment, storagePath, "")
	if err != nil {
		return "", err
	}
	return result.ColonyID, nil
}

// RunCoralInitFull executes `coral init` and returns full parsed output including PSK.
func RunCoralInitFull(ctx context.Context, appName, environment, storagePath, discoveryURL string) (*InitResult, error) {
	args := []string{"init", appName, "--env", environment, "--storage", storagePath}

	if discoveryURL != "" {
		args = append(args, "--discovery", discoveryURL)
	}

	cmd := exec.CommandContext(ctx, "coral", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("coral init failed: %w\nOutput: %s", err, string(output))
	}

	result := &InitResult{Output: string(output)}

	// Parse colony ID and bootstrap PSK from output.
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, "export CORAL_COLONY_ID=") {
			parts := strings.Split(line, "=")
			if len(parts) == 2 {
				result.ColonyID = strings.TrimSpace(parts[1])
			}
		}
		if strings.Contains(line, "export CORAL_BOOTSTRAP_PSK=") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				result.BootstrapPSK = strings.TrimSpace(parts[1])
			}
		}
		// Also match the standalone PSK display line.
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "coral-psk:") && result.BootstrapPSK == "" {
			result.BootstrapPSK = trimmed
		}
	}

	if result.ColonyID == "" {
		return nil, fmt.Errorf("failed to parse colony ID from init output:\n%s", string(output))
	}

	return result, nil
}

// RunCoralInitWithDiscovery executes `coral init` with custom discovery URL.
func RunCoralInitWithDiscovery(ctx context.Context, appName, environment, storagePath, discoveryURL string) (string, error) {
	result, err := RunCoralInitFull(ctx, appName, environment, storagePath, discoveryURL)
	if err != nil {
		return "", err
	}
	return result.ColonyID, nil
}
