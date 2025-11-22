package initcmd

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/coral-io/coral/internal/auth"
	"github.com/coral-io/coral/internal/colony/ca"
	"github.com/coral-io/coral/internal/config"
	"github.com/coral-io/coral/internal/constants"
)

// NewInitCmd creates the init command
func NewInitCmd() *cobra.Command {
	var (
		environment  string
		storagePath  string
		discoveryURL string
	)

	cmd := &cobra.Command{
		Use:   "init <app-name>",
		Short: "Initialize a new Coral colony",
		Long: `Initialize a new Coral colony with application identity and security credentials.

This command creates:
- A unique colony ID (format: <app-name>-<environment>-<random>)
- A colony secret for agent authentication
- A WireGuard key pair for mesh encryption
- Configuration files in ~/.coral/

Example:
  coral init my-shop --env production
  coral init payment-api --env staging --storage /data/coral`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName := args[0]
			return runInit(appName, environment, storagePath, discoveryURL)
		},
	}

	cmd.Flags().StringVar(&environment, "env", "dev", "Environment name (dev, staging, production, etc.)")
	cmd.Flags().StringVar(&storagePath, "storage", constants.DefaultDir, "Storage directory path")
	cmd.Flags().StringVar(&discoveryURL, "discovery", constants.DefaultDiscoveryEndpoint, "Discovery service URL")

	return cmd
}

func runInit(appName, environment, storagePath, discoveryURL string) error {
	fmt.Println("Initializing Coral colony...")

	// Generate colony ID
	colonyID, err := auth.GenerateColonyID(appName, environment)
	if err != nil {
		return fmt.Errorf("failed to generate colony ID: %w", err)
	}

	// Generate colony secret
	colonySecret, err := auth.GenerateColonySecret()
	if err != nil {
		return fmt.Errorf("failed to generate colony secret: %w", err)
	}

	// Generate WireGuard key pair
	wgKeys, err := auth.GenerateWireGuardKeyPair()
	if err != nil {
		return fmt.Errorf("failed to generate WireGuard keys: %w", err)
	}

	// Determine storage path
	if storagePath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		storagePath = filepath.Join(cwd, ".coral")
	}

	// Get current user for created_by field
	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}
	hostname, _ := os.Hostname()
	createdBy := fmt.Sprintf("%s@%s", currentUser.Username, hostname)

	// Create colony config
	colonyConfig := config.DefaultColonyConfig(colonyID, appName, environment)
	colonyConfig.ColonySecret = colonySecret
	colonyConfig.WireGuard.PrivateKey = wgKeys.PrivateKey
	colonyConfig.WireGuard.PublicKey = wgKeys.PublicKey
	colonyConfig.StoragePath = storagePath
	colonyConfig.CreatedBy = createdBy

	// Load or create global config
	loader, err := config.NewLoader()
	if err != nil {
		return fmt.Errorf("failed to create config loader: %w", err)
	}

	globalConfig, err := loader.LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("failed to load global config: %w", err)
	}

	// Update discovery endpoint if specified
	if discoveryURL != "" {
		globalConfig.Discovery.Endpoint = discoveryURL
	}

	// Set as default colony if no default exists
	if globalConfig.DefaultColony == "" {
		globalConfig.DefaultColony = colonyID
	}

	// Save configurations
	fmt.Printf("Initializing colony: %s...\n\n", appName)

	if err := loader.SaveColonyConfig(colonyConfig); err != nil {
		return fmt.Errorf("failed to save colony config: %w", err)
	}

	if err := loader.SaveGlobalConfig(globalConfig); err != nil {
		return fmt.Errorf("failed to save global config: %w", err)
	}

	// Generate Certificate Authority (RFD 047).
	// CA is stored in ~/.coral/colonies/<colony-id>/ca/
	colonyDir := loader.ColonyDir(colonyID)
	caDir := filepath.Join(colonyDir, "ca")
	caResult, err := ca.Initialize(caDir, colonyID)
	if err != nil {
		return fmt.Errorf("failed to initialize CA: %w", err)
	}

	// Print output per RFD 047 format.
	fmt.Println("Generated Certificate Authority:")
	fmt.Printf("  Root CA:              %s\n", caResult.RootCAPath)
	fmt.Printf("  Root CA Key:          %s (SECRET)\n", filepath.Join(caDir, "root-ca.key"))
	fmt.Printf("  Server Intermediate:  %s\n", caResult.ServerIntPath)
	fmt.Printf("  Agent Intermediate:   %s\n", caResult.AgentIntPath)
	fmt.Printf("  Policy Signing Cert:  %s\n", caResult.PolicySignPath)

	fmt.Println("\nRoot CA Fingerprint (distribute to agents):")
	fmt.Printf("  sha256:%s\n", caResult.RootFingerprint)

	fmt.Println("\nColony Server Identity:")
	fmt.Printf("  SPIFFE ID: %s\n", caResult.ColonySPIFFEID)

	fmt.Println("\n⚠️  IMPORTANT: Keep root-ca.key secure (offline storage or HSM recommended)")

	// Create project-local config
	projectConfig := config.DefaultProjectConfig(colonyID)
	if err := config.SaveProjectConfig(".", projectConfig); err != nil {
		return fmt.Errorf("failed to save project config: %w", err)
	}

	fmt.Println("\nDeploy agents with:")
	fmt.Printf("  export CORAL_COLONY_ID=%s\n", colonyID)
	fmt.Printf("  export CORAL_CA_FINGERPRINT=sha256:%s\n", caResult.RootFingerprint)
	fmt.Println("  coral agent start")

	fmt.Println("\n✓ Colony initialized successfully")

	return nil
}
