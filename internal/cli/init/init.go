package initcmd

import (
	"bufio"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/coral-io/coral/internal/auth"
	"github.com/coral-io/coral/internal/config"
	"github.com/coral-io/coral/internal/constants"

	"github.com/spf13/cobra"
)

// NewInitCmd creates the init command
func NewInitCmd() *cobra.Command {
	var (
		environment  string
		storagePath  string
		discoveryURL string
	)

	cmd := &cobra.Command{
		Use:   "init [app-name]",
		Short: "Initialize a new Coral colony",
		Long: `Initialize a new Coral colony with application identity and security credentials.

This command creates:
- A unique colony ID (format: <app-name>-<environment>-<random>)
- A colony secret for agent authentication
- A WireGuard key pair for mesh encryption
- Configuration files in ~/.coral/

Example:
  coral init my-shop --env production
  coral init payment-api --env staging --storage /data/coral
  coral init  # Interactive mode`,
		Args: cobra.MaxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var appName string
			if len(args) > 0 {
				appName = args[0]
			} else {
				// Interactive mode - prompt for inputs
				var err error
				appName, err = promptForInput("Application name", "", true)
				if err != nil {
					return err
				}

				// Prompt for environment if not set via flag
				if !cmd.Flags().Changed("env") {
					envInput, err := promptForInput("Environment", environment, false)
					if err != nil {
						return err
					}
					if envInput != "" {
						environment = envInput
					}
				}

				// Prompt for storage path if not set via flag
				if !cmd.Flags().Changed("storage") {
					storageInput, err := promptForInput("Storage path", storagePath, false)
					if err != nil {
						return err
					}
					if storageInput != "" {
						storagePath = storageInput
					}
				}

				// Prompt for discovery URL if not set via flag
				if !cmd.Flags().Changed("discovery") {
					discoveryInput, err := promptForInput("Discovery service URL", discoveryURL, false)
					if err != nil {
						return err
					}
					if discoveryInput != "" {
						discoveryURL = discoveryInput
					}
				}
			}

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
	fmt.Print("✓ Created colony ID: ")
	fmt.Println(colonyID)

	if err := loader.SaveColonyConfig(colonyConfig); err != nil {
		return fmt.Errorf("failed to save colony config: %w", err)
	}
	fmt.Print("✓ Generated WireGuard keypair\n")
	fmt.Print("✓ Created colony secret\n")

	if err := loader.SaveGlobalConfig(globalConfig); err != nil {
		return fmt.Errorf("failed to save global config: %w", err)
	}
	fmt.Printf("✓ Configuration saved to %s\n", loader.ColonyConfigPath(colonyID))

	// Create project-local config
	projectConfig := config.DefaultProjectConfig(colonyID)
	if err := config.SaveProjectConfig(".", projectConfig); err != nil {
		return fmt.Errorf("failed to save project config: %w", err)
	}
	fmt.Print("✓ Created project config (.coral/config.yaml)\n")

	fmt.Println("\nColony initialized successfully!")
	fmt.Println("\nNext steps:")
	fmt.Println("  To start the colony:")
	fmt.Println("    coral colony start")
	fmt.Println("\n  To connect agents:")
	fmt.Printf("    coral connect <service> --colony %s\n", colonyID)
	fmt.Println("\n  For remote agents (Kubernetes, VMs), export credentials:")
	fmt.Printf("    coral colony export %s\n", colonyID)

	return nil
}

// promptForInput prompts the user for input with an optional default value.
func promptForInput(prompt, defaultValue string, required bool) (string, error) {
	reader := bufio.NewReader(os.Stdin)

	// Format the prompt
	displayPrompt := prompt
	if defaultValue != "" {
		displayPrompt = fmt.Sprintf("%s [%s]", prompt, defaultValue)
	}
	if required {
		displayPrompt = fmt.Sprintf("%s (required)", displayPrompt)
	}
	fmt.Printf("%s: ", displayPrompt)

	// Read input
	input, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read input: %w", err)
	}

	// Trim whitespace
	input = strings.TrimSpace(input)

	// Use default if no input provided
	if input == "" {
		if defaultValue != "" {
			return defaultValue, nil
		}
		if required {
			return "", fmt.Errorf("%s is required", prompt)
		}
		return "", nil
	}

	return input, nil
}
