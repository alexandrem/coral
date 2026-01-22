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
		Use:   "export <colony-id>",
		Short: "Export colony credentials",
		Long: `Export colony credentials for deployment to remote systems.

Supported formats:
  env  - Shell environment variables (default)
  yaml - YAML format
  json - JSON format
  k8s  - Kubernetes Secret manifest

Security Warning: These credentials provide full access to the colony.
Keep them secure and do not commit to version control.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			colonyID := args[0]

			// Get CA manager and configuration (RFD 048).
			manager, db, cfg, err := helpers.GetCAManager(colonyID)
			if err != nil {
				return err
			}
			defer db.Close() // nolint:errcheck

			status := manager.GetStatus()
			caFingerprint := "sha256:" + status.RootCA.Fingerprint

			switch format {
			case "env":
				fmt.Println("# Coral Colony Credentials")
				fmt.Printf("# Generated: %s\n", time.Now().Format("2006-01-02 15:04:05"))
				fmt.Println("# SECURITY: Keep these credentials secure. Do not commit to version control.")
				fmt.Println()
				fmt.Printf("export CORAL_COLONY_ID=\"%s\"\n", cfg.ColonyID)
				fmt.Printf("export CORAL_CA_FINGERPRINT=\"%s\"\n", caFingerprint)
				fmt.Printf("export CORAL_DISCOVERY_ENDPOINT=\"%s\"\n", cfg.DiscoveryURL)
				fmt.Println()
				fmt.Println("# To use in your shell:")
				fmt.Printf("#   eval $(coral colony export %s)\n", colonyID)

			case "yaml":
				fmt.Println("# Coral Colony Credentials (YAML)")
				fmt.Printf("colony_id: %s\n", cfg.ColonyID)
				fmt.Printf("ca_fingerprint: %s\n", caFingerprint)
				fmt.Printf("discovery_endpoint: %s\n", cfg.DiscoveryURL)

			case "json":
				data := map[string]string{
					"colony_id":          cfg.ColonyID,
					"ca_fingerprint":     caFingerprint,
					"discovery_endpoint": cfg.DiscoveryURL,
				}
				output, err := json.MarshalIndent(data, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(output))

			case "k8s":
				fmt.Println("apiVersion: v1")
				fmt.Println("kind: Secret")
				fmt.Println("metadata:")
				fmt.Printf("  name: coral-secrets\n")
				fmt.Println("type: Opaque")
				fmt.Println("stringData:")
				fmt.Printf("  colony-id: %s\n", cfg.ColonyID)
				fmt.Printf("  ca-fingerprint: %s\n", caFingerprint)
				fmt.Printf("  discovery-endpoint: %s\n", cfg.DiscoveryURL)

			default:
				return fmt.Errorf("unknown format: %s (supported: env, yaml, json, k8s)", format)
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
