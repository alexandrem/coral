package colony

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/coral-mesh/coral/internal/config"
)

// newAddRemoteCmd creates the add-remote command for adding remote colony configurations.
func newAddRemoteCmd() *cobra.Command {
	var (
		endpoint   string
		caFile     string
		caData     string
		insecure   bool
		setDefault bool
	)

	cmd := &cobra.Command{
		Use:   "add-remote <colony-name>",
		Short: "Add a remote colony connection (similar to kubectl config set-cluster)",
		Long: `Add a remote colony configuration for CLI access without WireGuard mesh.

This creates a local config file that stores the connection details for a remote
colony. The config is stored at ~/.coral/colonies/<colony-name>/config.yaml.

Similar to kubectl's cluster configuration, you can specify:
  - The colony's public HTTPS endpoint
  - A CA certificate to trust (file path or base64-encoded)
  - Or skip TLS verification for testing (not recommended for production)

Examples:
  # Add remote colony with CA certificate file
  coral colony add-remote prod --endpoint https://colony.example.com:8443 --ca-file ./ca.crt

  # Add remote colony with inline CA certificate (base64)
  coral colony add-remote prod --endpoint https://colony.example.com:8443 --ca-data "LS0tLS1..."

  # Add remote colony with insecure mode (testing only)
  coral colony add-remote dev --endpoint https://localhost:8443 --insecure

  # Add and set as default colony
  coral colony add-remote prod --endpoint https://colony.example.com:8443 --ca-file ./ca.crt --set-default`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			colonyName := args[0]

			// Validate arguments.
			if endpoint == "" {
				return fmt.Errorf("--endpoint is required")
			}

			if !insecure && caFile == "" && caData == "" {
				return fmt.Errorf("TLS verification requires --ca-file, --ca-data, or --insecure flag\n\nFor production, use --ca-file to specify the colony's CA certificate.\nFor testing only, use --insecure to skip TLS verification.")
			}

			if insecure && (caFile != "" || caData != "") {
				return fmt.Errorf("--insecure cannot be used with --ca-file or --ca-data")
			}

			// Create config loader.
			loader, err := config.NewLoader()
			if err != nil {
				return fmt.Errorf("failed to create config loader: %w", err)
			}

			// Check if colony already exists.
			colonyDir := loader.ColonyDir(colonyName)
			configPath := filepath.Join(colonyDir, "config.yaml")
			if _, err := os.Stat(configPath); err == nil {
				return fmt.Errorf("colony %q already exists at %s\n\nTo update, remove it first with: rm -rf %s", colonyName, configPath, colonyDir)
			}

			// Create colony directory.
			if err := os.MkdirAll(colonyDir, 0o755); err != nil {
				return fmt.Errorf("failed to create colony directory: %w", err)
			}

			// Build remote config.
			remoteConfig := config.RemoteConfig{
				Endpoint: endpoint,
			}

			// Handle CA certificate.
			if caFile != "" {
				// Read and validate CA file.
				caBytes, err := os.ReadFile(caFile)
				if err != nil {
					return fmt.Errorf("failed to read CA file %s: %w", caFile, err)
				}

				// Copy CA file to colony directory.
				destCAPath := filepath.Join(colonyDir, "ca.crt")
				if err := copyFile(caFile, destCAPath); err != nil {
					return fmt.Errorf("failed to copy CA file: %w", err)
				}

				// Use relative path in config for portability.
				remoteConfig.CertificateAuthority = destCAPath

				fmt.Printf("CA certificate copied to: %s\n", destCAPath)
				fmt.Printf("CA certificate size: %d bytes\n", len(caBytes))
			} else if caData != "" {
				// Validate base64 encoding.
				decoded, err := base64.StdEncoding.DecodeString(caData)
				if err != nil {
					return fmt.Errorf("invalid base64 CA data: %w", err)
				}
				remoteConfig.CertificateAuthorityData = caData

				fmt.Printf("CA certificate (base64): %d bytes decoded\n", len(decoded))
			} else if insecure {
				remoteConfig.InsecureSkipTLSVerify = true
				fmt.Println("⚠️  WARNING: TLS verification disabled. Never use in production!")
			}

			// Create colony config.
			colonyConfig := config.ColonyConfig{
				Version:   config.SchemaVersion,
				ColonyID:  colonyName,
				Remote:    remoteConfig,
				CreatedAt: time.Now(),
				CreatedBy: os.Getenv("USER"),
			}

			// Write config file.
			configData, err := yaml.Marshal(&colonyConfig)
			if err != nil {
				return fmt.Errorf("failed to marshal config: %w", err)
			}

			if err := os.WriteFile(configPath, configData, 0o644); err != nil {
				return fmt.Errorf("failed to write config file: %w", err)
			}

			fmt.Printf("\nRemote colony %q added successfully!\n", colonyName)
			fmt.Printf("Config: %s\n", configPath)
			fmt.Printf("Endpoint: %s\n", endpoint)

			// Set as default if requested.
			if setDefault {
				if err := setDefaultColony(colonyName); err != nil {
					return fmt.Errorf("failed to set as default: %w", err)
				}
				fmt.Printf("\nSet as default colony.\n")
			}

			// Print usage instructions.
			fmt.Println("\nUsage:")
			if !setDefault {
				fmt.Printf("  coral config use-context %s  # Set as default\n", colonyName)
			}
			fmt.Println("  export CORAL_API_TOKEN=<your-token>")
			fmt.Println("  coral colony status")

			return nil
		},
	}

	cmd.Flags().StringVar(&endpoint, "endpoint", "", "Colony's public HTTPS endpoint URL (required)")
	cmd.Flags().StringVar(&caFile, "ca-file", "", "Path to CA certificate file for TLS verification")
	cmd.Flags().StringVar(&caData, "ca-data", "", "Base64-encoded CA certificate for TLS verification")
	cmd.Flags().BoolVar(&insecure, "insecure", false, "Skip TLS verification (testing only, never in production)")
	cmd.Flags().BoolVar(&setDefault, "set-default", false, "Set this colony as the default")

	_ = cmd.MarkFlagRequired("endpoint")

	return cmd
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// setDefaultColony updates the global config to set the default colony.
func setDefaultColony(colonyName string) error {
	loader, err := config.NewLoader()
	if err != nil {
		return err
	}

	globalConfig, err := loader.LoadGlobalConfig()
	if err != nil {
		// Create new global config if it doesn't exist.
		globalConfig = &config.GlobalConfig{
			Version: config.SchemaVersion,
		}
	}

	globalConfig.DefaultColony = colonyName

	// Write updated global config.
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	configPath := filepath.Join(home, ".coral", "config.yaml")
	configData, err := yaml.Marshal(globalConfig)
	if err != nil {
		return err
	}

	return os.WriteFile(configPath, configData, 0o644)
}
