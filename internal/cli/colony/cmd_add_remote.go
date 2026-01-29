package colony

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/discovery/client"
	"github.com/coral-mesh/coral/internal/safe"
)

// newAddRemoteCmd creates the add-remote command for adding remote colony configurations.
func newAddRemoteCmd() *cobra.Command {
	var (
		endpoint          string
		caFile            string
		caData            string
		caFingerprint     string
		insecure          bool
		setDefault        bool
		fromDiscovery     bool
		colonyID          string
		discoveryEndpoint string
	)

	cmd := &cobra.Command{
		Use:   "add-remote <colony-name>",
		Short: "Add a remote colony connection (similar to kubectl config set-cluster)",
		Long: `Add a remote colony configuration for CLI access without WireGuard mesh.

This creates a local config file that stores the connection details for a remote
colony. The config is stored at ~/.coral/colonies/<colony-name>/config.yaml.

Two modes are supported:

1. Discovery Mode (Recommended): Fetch endpoint and CA from Discovery Service
   Requires colony ID and CA fingerprint (get from colony owner via 'coral colony export')
   The fingerprint is used to cryptographically verify the CA certificate.

2. Manual Mode: Specify endpoint and CA directly
   Similar to kubectl's cluster configuration.

Examples:
  # Connect using Discovery (recommended) - get credentials from colony owner
  coral colony add-remote prod \
      --from-discovery \
      --colony-id my-app-prod-a3f2e1 \
      --ca-fingerprint sha256:e3b0c44298fc1c149afbf4c8996fb924...

  # Use custom Discovery endpoint
  coral colony add-remote prod \
      --from-discovery \
      --colony-id my-app-prod-a3f2e1 \
      --ca-fingerprint sha256:e3b0c44298fc1c149afbf4c8996fb924... \
      --discovery-endpoint https://discovery.internal:8080

  # Manual mode: Add remote colony with CA certificate file
  coral colony add-remote prod --endpoint https://colony.example.com:8443 --ca-file ./ca.crt

  # Manual mode: Add with inline CA certificate (base64)
  coral colony add-remote prod --endpoint https://colony.example.com:8443 --ca-data "LS0tLS1..."

  # Manual mode: Insecure (testing only)
  coral colony add-remote dev --endpoint https://localhost:8443 --insecure

  # Set as default colony
  coral colony add-remote prod --from-discovery --colony-id ... --ca-fingerprint ... --set-default`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			colonyName := args[0]

			// Validate mode-specific arguments.
			if fromDiscovery {
				// Discovery mode validation.
				if colonyID == "" || caFingerprint == "" {
					return fmt.Errorf("--colony-id and --ca-fingerprint are required with --from-discovery\n\nUsage:\n  coral colony add-remote %s \\\n      --from-discovery \\\n      --colony-id <colony-id> \\\n      --ca-fingerprint <sha256:...>\n\nGet these values from the colony owner (coral colony export)", colonyName)
				}
				if insecure {
					return fmt.Errorf("--insecure cannot be used with --from-discovery\n\nThe --from-discovery flow requires fingerprint verification for security.\nUse --insecure only with manual mode (--endpoint + --ca-file) for testing")
				}
				if endpoint != "" || caFile != "" || caData != "" {
					return fmt.Errorf("--endpoint, --ca-file, and --ca-data cannot be used with --from-discovery")
				}
			} else {
				// Manual mode validation.
				if endpoint == "" {
					return fmt.Errorf("--endpoint is required (or use --from-discovery)")
				}
				if !insecure && caFile == "" && caData == "" {
					return fmt.Errorf("TLS verification requires --ca-file, --ca-data, or --insecure flag\n\nFor production, use --ca-file to specify the colony's CA certificate.\nFor testing only, use --insecure to skip TLS verification")
				}
				if insecure && (caFile != "" || caData != "") {
					return fmt.Errorf("--insecure cannot be used with --ca-file or --ca-data")
				}
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
			if err := os.MkdirAll(colonyDir, 0o700); err != nil {
				return fmt.Errorf("failed to create colony directory: %w", err)
			}

			// Build remote config.
			var remoteConfig config.RemoteConfig
			var resolvedEndpoint string

			if fromDiscovery {
				// Discovery mode: fetch endpoint and CA from Discovery Service.
				remoteConfig, resolvedEndpoint, err = fetchFromDiscovery(
					colonyID, caFingerprint, discoveryEndpoint, colonyDir, loader,
				)
				if err != nil {
					// Clean up colony directory on error.
					_ = os.RemoveAll(colonyDir)
					return err
				}
			} else {
				// Manual mode: use provided endpoint and CA.
				remoteConfig = config.RemoteConfig{
					Endpoint: endpoint,
				}
				resolvedEndpoint = endpoint

				// Handle CA certificate.
				if caFile != "" {
					// Copy CA file to colony directory (includes security validations).
					destCAPath := filepath.Join(colonyDir, "ca.crt")
					if err := safe.CopyFile(caFile, destCAPath, nil); err != nil {
						_ = os.RemoveAll(colonyDir)
						return fmt.Errorf("failed to copy CA file: %w", err)
					}

					// Read the copied file to get its size for display.
					caBytes, err := os.ReadFile(destCAPath) // #nosec G304 - validated with safe prior
					if err != nil {
						_ = os.RemoveAll(colonyDir)
						return fmt.Errorf("failed to read copied CA file: %w", err)
					}

					// Use the destination path in config.
					remoteConfig.CertificateAuthority = destCAPath

					// Store fingerprint for continuous verification if provided.
					if caFingerprint != "" {
						fp, err := parseFingerprint(caFingerprint)
						if err != nil {
							_ = os.RemoveAll(colonyDir)
							return fmt.Errorf("invalid --ca-fingerprint: %w", err)
						}
						// Verify the fingerprint matches the CA file.
						computed := sha256.Sum256(caBytes)
						if hex.EncodeToString(computed[:]) != fp.Value {
							_ = os.RemoveAll(colonyDir)
							return fmt.Errorf("CA certificate fingerprint mismatch!\n\n  Expected: %s\n  Computed: sha256:%x\n\nVerify the --ca-fingerprint with the colony owner", caFingerprint, computed)
						}
						remoteConfig.CAFingerprint = fp
					}

					fmt.Printf("CA certificate copied to: %s\n", destCAPath)
					fmt.Printf("CA certificate size: %d bytes\n", len(caBytes))
				} else if caData != "" {
					// Validate base64 encoding.
					decoded, err := base64.StdEncoding.DecodeString(caData)
					if err != nil {
						_ = os.RemoveAll(colonyDir)
						return fmt.Errorf("invalid base64 CA data: %w", err)
					}
					remoteConfig.CertificateAuthorityData = caData

					// Store fingerprint for continuous verification if provided.
					if caFingerprint != "" {
						fp, err := parseFingerprint(caFingerprint)
						if err != nil {
							_ = os.RemoveAll(colonyDir)
							return fmt.Errorf("invalid --ca-fingerprint: %w", err)
						}
						// Verify the fingerprint matches the CA data.
						computed := sha256.Sum256(decoded)
						if hex.EncodeToString(computed[:]) != fp.Value {
							_ = os.RemoveAll(colonyDir)
							return fmt.Errorf("CA certificate fingerprint mismatch!\n\n  Expected: %s\n  Computed: sha256:%x\n\nVerify the --ca-fingerprint with the colony owner", caFingerprint, computed)
						}
						remoteConfig.CAFingerprint = fp
					}

					fmt.Printf("CA certificate (base64): %d bytes decoded\n", len(decoded))
				} else if insecure {
					remoteConfig.InsecureSkipTLSVerify = true
					fmt.Println("WARNING: TLS verification disabled. Never use in production!")
				}
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
				_ = os.RemoveAll(colonyDir)
				return fmt.Errorf("failed to marshal config: %w", err)
			}

			if err := os.WriteFile(configPath, configData, 0o600); err != nil {
				_ = os.RemoveAll(colonyDir)
				return fmt.Errorf("failed to write config file: %w", err)
			}

			fmt.Printf("\nRemote colony %q added successfully!\n", colonyName)
			fmt.Printf("Config: %s\n", configPath)
			fmt.Printf("Endpoint: %s\n", resolvedEndpoint)

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

	// Manual mode flags.
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "Colony's public HTTPS endpoint URL (manual mode)")
	cmd.Flags().StringVar(&caFile, "ca-file", "", "Path to CA certificate file for TLS verification (manual mode)")
	cmd.Flags().StringVar(&caData, "ca-data", "", "Base64-encoded CA certificate for TLS verification (manual mode)")
	cmd.Flags().BoolVar(&insecure, "insecure", false, "Skip TLS verification (testing only, manual mode only)")

	// Discovery mode flags (RFD 085).
	cmd.Flags().BoolVar(&fromDiscovery, "from-discovery", false, "Fetch endpoint and CA from Discovery Service")
	cmd.Flags().StringVar(&colonyID, "colony-id", "", "Colony ID (required with --from-discovery)")
	cmd.Flags().StringVar(&caFingerprint, "ca-fingerprint", "", "CA fingerprint for verification (required with --from-discovery, format: sha256:hex)")
	cmd.Flags().StringVar(&discoveryEndpoint, "discovery-endpoint", "", "Override Discovery Service URL")

	// Common flags.
	cmd.Flags().BoolVar(&setDefault, "set-default", false, "Set this colony as the default")

	return cmd
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

	return os.WriteFile(configPath, configData, 0o600)
}

// fetchFromDiscovery fetches public endpoint info from Discovery Service and verifies the CA fingerprint.
func fetchFromDiscovery(colonyID, caFingerprint, discoveryEndpoint, colonyDir string, loader *config.Loader) (config.RemoteConfig, string, error) {
	// Parse expected fingerprint.
	expectedFP, err := parseFingerprint(caFingerprint)
	if err != nil {
		return config.RemoteConfig{}, "", fmt.Errorf("invalid --ca-fingerprint: %w", err)
	}

	// Determine Discovery endpoint from global config if not overridden.
	if discoveryEndpoint == "" {
		globalConfig, err := loader.LoadGlobalConfig()
		if err != nil {
			return config.RemoteConfig{}, "", fmt.Errorf("failed to load global config: %w", err)
		}
		discoveryEndpoint = globalConfig.Discovery.Endpoint
	}

	fmt.Println("Fetching colony info from Discovery Service...")
	fmt.Printf("  Discovery: %s\n", discoveryEndpoint)
	fmt.Printf("  Colony ID: %s\n", colonyID)

	// Create Discovery client and lookup colony.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	discoveryClient := client.New(discoveryEndpoint)
	resp, err := discoveryClient.LookupColony(ctx, colonyID)
	if err != nil {
		return config.RemoteConfig{}, "", fmt.Errorf("failed to lookup colony from Discovery: %w", err)
	}

	// Check if public endpoint info is available.
	if resp.PublicEndpoint == nil || !resp.PublicEndpoint.Enabled {
		return config.RemoteConfig{}, "", fmt.Errorf("colony %q does not have a public endpoint registered with Discovery.\n\nThe colony owner may need to:\n  1. Enable public_endpoint in colony config\n  2. Ensure discovery.register is not disabled\n  3. Restart the colony", colonyID)
	}

	fmt.Printf("  Public Endpoint: %s\n", resp.PublicEndpoint.URL)

	// Decode the CA certificate.
	caCertPEM, err := base64.StdEncoding.DecodeString(resp.PublicEndpoint.CACert)
	if err != nil {
		return config.RemoteConfig{}, "", fmt.Errorf("failed to decode CA certificate from Discovery: %w", err)
	}

	// Verify the CA fingerprint.
	fmt.Println("\nVerifying CA certificate...")
	fmt.Printf("  Expected fingerprint: %s\n", caFingerprint)

	computedHash := sha256.Sum256(caCertPEM)
	computedFP := hex.EncodeToString(computedHash[:])
	fmt.Printf("  Received fingerprint: sha256:%s\n", computedFP)

	if computedFP != expectedFP.Value {
		return config.RemoteConfig{}, "", fmt.Errorf(`CA fingerprint mismatch!

The CA certificate from Discovery does not match the expected fingerprint.
This could indicate:
  - A man-in-the-middle attack
  - Compromised Discovery Service
  - Incorrect fingerprint provided by colony owner
  - Colony CA was rotated (get new fingerprint from colony owner)

Connection aborted. Verify the fingerprint with the colony owner`)
	}

	fmt.Println("  Fingerprint verified")

	// Write CA certificate to colony directory.
	destCAPath := filepath.Join(colonyDir, "ca.crt")
	if err := os.WriteFile(destCAPath, caCertPEM, 0o600); err != nil {
		return config.RemoteConfig{}, "", fmt.Errorf("failed to write CA certificate: %w", err)
	}

	fmt.Printf("\nCA cert: %s\n", destCAPath)

	// Build remote config with fingerprint for continuous verification.
	remoteConfig := config.RemoteConfig{
		Endpoint:             resp.PublicEndpoint.URL,
		CertificateAuthority: destCAPath,
		CAFingerprint:        expectedFP,
	}

	return remoteConfig, resp.PublicEndpoint.URL, nil
}

// parseFingerprint parses a fingerprint string in the format "sha256:hex".
func parseFingerprint(fp string) (*config.CAFingerprintConfig, error) {
	if !strings.HasPrefix(fp, "sha256:") {
		return nil, fmt.Errorf("fingerprint must start with 'sha256:' (got: %q)", fp)
	}

	hexValue := strings.TrimPrefix(fp, "sha256:")

	// Validate hex encoding.
	if _, err := hex.DecodeString(hexValue); err != nil {
		return nil, fmt.Errorf("invalid hex encoding in fingerprint: %w", err)
	}

	// SHA256 produces 32 bytes = 64 hex characters.
	if len(hexValue) != 64 {
		return nil, fmt.Errorf("SHA256 fingerprint must be 64 hex characters (got %d)", len(hexValue))
	}

	return &config.CAFingerprintConfig{
		Algorithm: "sha256",
		Value:     hexValue,
	}, nil
}
