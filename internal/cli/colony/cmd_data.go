package colony

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/coral-mesh/coral/internal/cli/helpers"
	"github.com/coral-mesh/coral/internal/config"
)

func newExportCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "export [colony-id]",
		Short: "Export agent enrollment credentials",
		Long: `Export the colony ID, CA fingerprint, discovery endpoint, and active bootstrap
PSK needed to provision agents on remote systems.

Defaults to the current colony when no colony ID is supplied.
Select the output format with --format (for example, coral colony export --format env).

Supported formats:
  env  - Shell environment variables (default)
  yaml - YAML format
  json - JSON format
  k8s  - Kubernetes Secret manifest

Security Warning: The bootstrap PSK authorizes new agents to join the colony.
Keep these credentials secure and do not commit to version control.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := helpers.ValidateFormat(format, []helpers.OutputFormat{"env", "yaml", "json", "k8s"}); err != nil {
				return err
			}

			resolver, err := config.NewResolver()
			if err != nil {
				return fmt.Errorf("failed to create config resolver: %w", err)
			}
			var colonyID string
			if len(args) == 1 {
				colonyID = args[0]
			} else {
				colonyID, err = resolver.ResolveColonyID()
				if err != nil {
					return err
				}
			}

			// Export requires a configured colony; do not let the config-less
			// fallback create a CA for an unknown ID against the project database.
			if _, err := resolver.GetLoader().LoadColonyConfig(colonyID); err != nil {
				return fmt.Errorf("failed to load colony %q: %w", colonyID, err)
			}

			// Get CA manager and configuration (RFD 048).
			manager, db, cfg, err := helpers.GetCAManager(colonyID)
			if err != nil {
				return err
			}
			defer db.Close() // nolint:errcheck

			ctx := cmd.Context()
			if err := manager.ImportPSKFromFile(ctx); err != nil {
				return fmt.Errorf("failed to import bootstrap PSK: %w", err)
			}
			bootstrapPSK, err := manager.GetActivePSK(ctx)
			if err != nil {
				return fmt.Errorf("failed to get active bootstrap PSK for colony %q: %w", colonyID, err)
			}

			status := manager.GetStatus()
			caFingerprint := "sha256:" + status.RootCA.Fingerprint

			var output string
			switch format {
			case "env":
				output = fmt.Sprintf(`# Coral Agent Enrollment Credentials
# Generated: %s
# SECURITY: Keep these credentials secure. Do not commit to version control.

export CORAL_COLONY_ID="%s"
export CORAL_CA_FINGERPRINT="%s"
export CORAL_DISCOVERY_ENDPOINT="%s"
export CORAL_BOOTSTRAP_PSK="%s"

# To deploy agents:
#   eval "$(coral colony export %s)"
`, time.Now().Format("2006-01-02 15:04:05"), cfg.ColonyID, caFingerprint, cfg.DiscoveryURL, bootstrapPSK, colonyID)

			case "yaml":
				output = fmt.Sprintf(`# Coral Agent Enrollment Credentials (YAML)
colony_id: %s
ca_fingerprint: %s
discovery_endpoint: %s
bootstrap_psk: %s
`, cfg.ColonyID, caFingerprint, cfg.DiscoveryURL, bootstrapPSK)

			case "json":
				data := map[string]string{
					"colony_id":          cfg.ColonyID,
					"ca_fingerprint":     caFingerprint,
					"discovery_endpoint": cfg.DiscoveryURL,
					"bootstrap_psk":      bootstrapPSK,
				}
				encoded, err := json.MarshalIndent(data, "", "  ")
				if err != nil {
					return err
				}
				output = string(encoded) + "\n"

			case "k8s":
				output = fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: coral-secrets
type: Opaque
stringData:
  colony-id: %s
  ca-fingerprint: %s
  discovery-endpoint: %s
  bootstrap-psk: %s
`, cfg.ColonyID, caFingerprint, cfg.DiscoveryURL, bootstrapPSK)

			default:
				return fmt.Errorf("unknown format: %s (supported: env, yaml, json, k8s)", format)
			}

			if _, err := fmt.Fprint(cmd.OutOrStdout(), output); err != nil {
				return fmt.Errorf("failed to write agent enrollment credentials: %w", err)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&format, "format", "env", "Output format (env, yaml, json, k8s)")

	return cmd
}

func newImportCmd() *cobra.Command {
	var (
		colonyID     string
		colonySecret string
		useStdin     bool
	)

	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import colony credentials",
		Long: `Import colony credentials from environment variables or flags.

This is typically used on remote systems (Kubernetes, VMs) that need to
connect to an existing colony.

Note: The colony's WireGuard public key will be retrieved from discovery service on first connection.
      The colony's private key never leaves the colony and is not needed by agents.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if useStdin {
				return fmt.Errorf("stdin import not yet implemented")
			}

			if colonyID == "" {
				colonyID = os.Getenv("CORAL_COLONY_ID")
			}

			if colonyID == "" || colonySecret == "" {
				return fmt.Errorf("colony-id and secret are required (use flags or env vars)")
			}

			loader, err := config.NewLoader()
			if err != nil {
				return fmt.Errorf("failed to create config loader: %w", err)
			}

			// Create a minimal colony config
			// Note: We don't have full colony details, just the essentials
			cfg := &config.ColonyConfig{
				Version:      config.SchemaVersion,
				ColonyID:     colonyID,
				ColonySecret: colonySecret,
				CreatedAt:    time.Now(),
				Discovery: config.DiscoveryColony{
					MeshID: colonyID,
				},
			}

			if err := loader.SaveColonyConfig(cfg); err != nil {
				return fmt.Errorf("failed to save colony config: %w", err)
			}

			fmt.Println("✓ Colony configuration imported")
			fmt.Printf("✓ Saved to %s\n", loader.ColonyConfigPath(colonyID))
			fmt.Println("\nNote: The colony's WireGuard public key will be retrieved from discovery service on first connection.")
			fmt.Println("      The colony's private key never leaves the colony and is not needed by agents.")

			return nil
		},
	}

	helpers.AddColonyFlag(cmd, &colonyID)
	cmd.Flags().StringVar(&colonySecret, "secret", "", "Colony secret")
	cmd.Flags().BoolVar(&useStdin, "stdin", false, "Read from stdin")

	return cmd
}
