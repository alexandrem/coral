package agent

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/coral-mesh/coral/internal/agent/bootstrap"
	"github.com/coral-mesh/coral/internal/agent/certs"
	"github.com/coral-mesh/coral/internal/cli/helpers"
	"github.com/coral-mesh/coral/internal/config"
	"github.com/coral-mesh/coral/internal/constants"
	"github.com/coral-mesh/coral/internal/logging"
)

// NewBootstrapCmd creates the bootstrap command for agents.
func NewBootstrapCmd() *cobra.Command {
	var (
		colonyID      string
		agentID       string
		caFingerprint string
		discoveryURL  string
		certsDir      string
		force         bool
	)

	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Bootstrap agent certificate for mTLS authentication",
		Long: `Bootstrap agent certificate using Root CA fingerprint validation (RFD 048).

The bootstrap process:
1. Requests a bootstrap token from Discovery service
2. Looks up colony endpoints from Discovery
3. Connects to colony and validates Root CA fingerprint
4. Generates Ed25519 keypair locally
5. Submits CSR with SPIFFE ID to Colony
6. Receives and stores signed certificate

Configuration (in order of precedence):
1. Command-line flags
2. Environment variables (CORAL_*)
3. Config file values

Required configuration:
  --colony        Colony ID to bootstrap with (or CORAL_COLONY_ID)
  --fingerprint   Expected Root CA fingerprint (or CORAL_CA_FINGERPRINT)

The fingerprint should be in format: sha256:hexstring
You can get this from the colony operator or from 'coral colony ca status'.

Examples:
  # Bootstrap with required parameters
  coral agent bootstrap --colony my-colony --fingerprint sha256:a3f2e1d4...

  # Using environment variables
  CORAL_COLONY_ID=my-colony CORAL_CA_FINGERPRINT=sha256:... coral agent bootstrap

  # Force re-bootstrap even if certificate exists
  coral agent bootstrap --colony my-colony --fingerprint sha256:... --force

  # Custom agent ID
  coral agent bootstrap --colony my-colony --fingerprint sha256:... --agent web-prod-1`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBootstrap(cmd.Context(), bootstrapOptions{
				ColonyID:      colonyID,
				AgentID:       agentID,
				CAFingerprint: caFingerprint,
				DiscoveryURL:  discoveryURL,
				CertsDir:      certsDir,
				Force:         force,
			})
		},
	}

	helpers.AddColonyFlag(cmd, &colonyID)
	cmd.Flags().StringVar(&agentID, "agent", os.Getenv("CORAL_AGENT_ID"), "Agent ID (default: auto-generated)")
	cmd.Flags().StringVar(&caFingerprint, "fingerprint", os.Getenv("CORAL_CA_FINGERPRINT"), "Expected Root CA fingerprint (sha256:hex)")
	discoveryDefault := os.Getenv("CORAL_DISCOVERY_ENDPOINT")
	if discoveryDefault == "" {
		discoveryDefault = constants.DefaultDiscoveryEndpoint
	}
	cmd.Flags().StringVar(&discoveryURL, "discovery", discoveryDefault, "Discovery service URL")
	cmd.Flags().StringVar(&certsDir, "certs-dir", os.Getenv("CORAL_CERTS_DIR"), "Directory for storing certificates")
	cmd.Flags().BoolVar(&force, "force", false, "Force re-bootstrap even if certificate exists")

	return cmd
}

type bootstrapOptions struct {
	ColonyID      string
	AgentID       string
	CAFingerprint string
	DiscoveryURL  string
	CertsDir      string
	Force         bool
}

func runBootstrap(ctx context.Context, opts bootstrapOptions) error {
	logger := logging.NewWithComponent(logging.Config{
		Level:  "info",
		Pretty: true,
	}, "bootstrap")

	// Load global config for discovery endpoint if not provided.
	var globalCfg *config.GlobalConfig
	loader, err := config.NewLoader()
	if err == nil {
		globalCfg, err = loader.LoadGlobalConfig()
		if err != nil {
			logger.Debug().Err(err).Msg("Failed to load global config, using defaults")
		}
	}

	// Resolve discovery URL.
	if opts.DiscoveryURL == "" {
		if globalCfg != nil && globalCfg.Discovery.Endpoint != "" {
			opts.DiscoveryURL = globalCfg.Discovery.Endpoint
		} else {
			return fmt.Errorf("discovery URL is required (--discovery or CORAL_DISCOVERY_ENDPOINT)")
		}
	}

	// Validate required parameters.
	if opts.ColonyID == "" {
		return fmt.Errorf("colony ID is required (--colony or CORAL_COLONY_ID)")
	}

	if opts.CAFingerprint == "" {
		return fmt.Errorf("CA fingerprint is required (--fingerprint or CORAL_CA_FINGERPRINT)")
	}

	// Generate agent ID if not provided.
	if opts.AgentID == "" {
		hostname, err := os.Hostname()
		if err != nil {
			hostname = "agent"
		}
		opts.AgentID = fmt.Sprintf("%s-%s", hostname, generateShortID())
		logger.Info().Str("agent_id", opts.AgentID).Msg("Auto-generated agent ID")
	}

	// Create certificate manager.
	certManager := certs.NewManager(certs.Config{
		CertsDir: opts.CertsDir,
		Logger:   logger,
	})

	// Check if certificate already exists.
	if !opts.Force && certManager.CertificateExists() {
		if err := certManager.Load(); err == nil {
			info := certManager.GetCertificateInfo()
			if info.Status == certs.CertStatusValid || info.Status == certs.CertStatusRenewalNeeded {
				fmt.Printf("Certificate already exists and is valid.\n")
				fmt.Printf("  Agent ID:     %s\n", info.AgentID)
				fmt.Printf("  Colony ID:    %s\n", info.ColonyID)
				fmt.Printf("  SPIFFE ID:    %s\n", info.SPIFFEID)
				fmt.Printf("  Expires:      %s (%d days)\n", info.NotAfter.Format(time.RFC3339), info.DaysRemaining)
				fmt.Printf("\nUse --force to re-bootstrap.\n")
				return nil
			}
		}
	}

	// Print bootstrap info.
	fmt.Printf("Starting certificate bootstrap...\n")
	fmt.Printf("  Colony ID:    %s\n", opts.ColonyID)
	fmt.Printf("  Agent ID:     %s\n", opts.AgentID)
	fmt.Printf("  Fingerprint:  %s\n", truncateFingerprint(opts.CAFingerprint))
	fmt.Printf("  Discovery:    %s\n", opts.DiscoveryURL)
	fmt.Println()

	// Create bootstrap client.
	client := bootstrap.NewClient(bootstrap.Config{
		AgentID:           opts.AgentID,
		ColonyID:          opts.ColonyID,
		CAFingerprint:     opts.CAFingerprint,
		DiscoveryEndpoint: opts.DiscoveryURL,
		Logger:            logger,
	})

	// Run bootstrap with timeout.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	fmt.Print("Requesting bootstrap token from Discovery... ")
	result, err := client.Bootstrap(ctx)
	if err != nil {
		fmt.Println("✗")
		return fmt.Errorf("bootstrap failed: %w", err)
	}
	fmt.Println("✓")

	// Save certificates.
	fmt.Print("Saving certificates... ")
	if err := certManager.Save(result); err != nil {
		fmt.Println("✗")
		return fmt.Errorf("failed to save certificates: %w", err)
	}
	fmt.Println("✓")

	// Save agent ID for future reference.
	if err := certManager.SaveAgentID(opts.AgentID); err != nil {
		logger.Warn().Err(err).Msg("Failed to save agent ID")
	}

	fmt.Println()
	fmt.Printf("✓ Bootstrap complete!\n")
	fmt.Println()
	fmt.Printf("Certificate Details:\n")
	fmt.Printf("  SPIFFE ID:    %s\n", result.AgentSPIFFEID)
	fmt.Printf("  Valid until:  %s\n", result.ExpiresAt.Format(time.RFC3339))
	fmt.Println()
	fmt.Printf("Saved to:\n")
	fmt.Printf("  Certificate:  %s\n", certManager.GetCertPath())
	fmt.Printf("  Private key:  %s\n", certManager.GetKeyPath())
	fmt.Printf("  Root CA:      %s\n", certManager.GetRootCAPath())

	return nil
}

// generateShortID generates a short random ID suffix.
func generateShortID() string {
	// Use current time to generate a simple unique suffix.
	return fmt.Sprintf("%x", time.Now().UnixNano()%0xFFFFFFFF)[:8]
}

// truncateFingerprint truncates a fingerprint for display.
func truncateFingerprint(fp string) string {
	if len(fp) > 20 {
		return fp[:20] + "..."
	}
	return fp
}
