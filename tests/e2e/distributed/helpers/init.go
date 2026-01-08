package helpers

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// RunCoralInit executes `coral init` and returns the generated colony ID.
func RunCoralInit(ctx context.Context, appName, environment, storagePath string) (string, error) {
	return RunCoralInitWithDiscovery(ctx, appName, environment, storagePath, "")
}

// RunCoralInitWithDiscovery executes `coral init` with custom discovery URL.
func RunCoralInitWithDiscovery(ctx context.Context, appName, environment, storagePath, discoveryURL string) (string, error) {
	args := []string{"init", appName, "--env", environment, "--storage", storagePath}

	if discoveryURL != "" {
		args = append(args, "--discovery", discoveryURL)
	}

	cmd := exec.CommandContext(ctx, "coral", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("coral init failed: %w\nOutput: %s", err, string(output))
	}

	// Parse colony ID from output
	// Output contains: "Initializing colony: <app-name>..."
	// And later shows the colony ID in various places
	// Extract it from "export CORAL_COLONY_ID=<colony-id>" line
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, "export CORAL_COLONY_ID=") {
			parts := strings.Split(line, "=")
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1]), nil
			}
		}
	}

	return "", fmt.Errorf("failed to parse colony ID from init output:\n%s", string(output))
}
